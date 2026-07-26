package postgres_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
	"github.com/upcomers-org/platformgo/internal/engine"
	"github.com/upcomers-org/platformgo/testkit"
)

// Ported from:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/trading/e2e_fills.rs:541
//	test: terminal_only_audit_skips_running_history_but_keeps_recovery_and_terminal_row
//
// Adaptations:
//
//   - The current Go command and receipt model replaces the legacy Rust saga
//     tables: a pending command plus its idempotency record is the durable
//     running recovery record, and an immutable engine input receipt is the
//     terminal audit fact.
//   - The stable idempotency scope and key replace the legacy saga type and
//     correlation key. Repeated Begin and Replay calls exercise the current Go
//     locking and recovery readers without importing a saga framework.
//   - Random UUIDs and process-clock setup are replaced by a deterministic ID
//     and explicit logical time.
//
// Assertions preserved:
//
//   - Starting work creates one durable running recovery record.
//   - A committed nonterminal repetition remains recoverable by its stable
//     correlation identity and creates no terminal audit fact.
//   - A committed terminal transition creates exactly one terminal audit fact
//     and persists completed recovery state.
//
// Strengthening:
//
//   - PostgreSQL transaction rollback, fresh-store recovery, same-sequence
//     replay, and later-sequence duplicate delivery cannot add a second
//     terminal fact or business effect.
//   - The terminal decision may persist only the configured NETTING account;
//     every other economic, event, and realtime projection remains empty
//     across rollback, replay, duplicate delivery, and restart.
func TestTerminalOnlyAuditSkipsRunningHistoryButKeepsRecoveryAndTerminalRow(
	t *testing.T,
) {
	ctx := context.Background()
	rootPool := postgresPool(t)
	resetDurableSchemas(t, rootPool)
	if err := platformpostgres.NewMigrator(
		rootPool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).MigrateAndProvision(ctx, 7); err != nil {
		t.Fatalf("MigrateAndProvision: %v", err)
	}

	apiPool := postgresRolePool(t, "platformgo_api")
	enginePool := postgresRolePool(t, "platformgo_engine")
	assertCurrentRole(t, apiPool, "platformgo_api")
	assertCurrentRole(t, enginePool, "platformgo_engine")

	const accountID = "account-terminal-audit"
	commandID := engine.IDFromSequence(engine.ID{}, 541)
	logicalTime := time.Date(2026, time.July, 26, 20, 0, 0, 0, time.UTC)
	request := terminalAuditCommandRequest(
		t,
		commandID,
		accountID,
		7,
		logicalTime,
	)
	journal := platformpostgres.NewCommandJournal(apiPool)
	started, err := journal.Begin(ctx, request)
	if err != nil {
		t.Fatalf("Begin running command: %v", err)
	}
	if !started.Created ||
		started.CommandID != commandID ||
		started.State != platformpostgres.IdempotencyInProgress {
		t.Fatalf("started command = %+v", started)
	}
	assertTerminalAuditLifecycle(
		t,
		rootPool,
		commandID,
		accountID,
		"pending",
		"in_progress",
		false,
		0,
		0,
		0,
		0,
	)
	assertTerminalAuditDurableEffects(
		t,
		rootPool,
		commandID,
		terminalAuditDurableEffects{},
	)

	// A new journal instance represents the independent recovery path. The
	// same correlation must lock and return the original running identity.
	repeated, err := platformpostgres.NewCommandJournal(apiPool).Begin(
		ctx,
		request,
	)
	if err != nil {
		t.Fatalf("repeat running Begin: %v", err)
	}
	if repeated.Created ||
		repeated.CommandID != commandID ||
		repeated.State != platformpostgres.IdempotencyInProgress {
		t.Fatalf("repeated running command = %+v", repeated)
	}
	replayed, found, err := platformpostgres.NewCommandJournal(apiPool).Replay(
		ctx,
		request.Scope,
		request.IdempotencyKey,
		request.RequestHash,
	)
	if err != nil {
		t.Fatalf("Replay running command: %v", err)
	}
	if !found ||
		replayed.CommandID != commandID ||
		replayed.State != platformpostgres.IdempotencyInProgress {
		t.Fatalf("replayed running command found=%t result=%+v", found, replayed)
	}
	assertTerminalAuditLifecycle(
		t,
		rootPool,
		commandID,
		accountID,
		"pending",
		"in_progress",
		false,
		0,
		0,
		0,
		0,
	)
	assertTerminalAuditDurableEffects(
		t,
		rootPool,
		commandID,
		terminalAuditDurableEffects{},
	)

	input, action, err := engine.DecodeInputMessage(request.OutboxPayload)
	if err != nil {
		t.Fatalf("DecodeInputMessage: %v", err)
	}
	input.StreamSequence = 1

	store := platformpostgres.NewEngineStore(enginePool)
	ownership, err := store.AcquireShardOwnership(ctx, 7)
	if err != nil {
		t.Fatalf("AcquireShardOwnership: %v", err)
	}
	t.Cleanup(func() {
		if ownership != nil {
			_ = ownership.Close(context.Background())
		}
	})
	runningState, err := store.RecoverTradingState(ctx, 7)
	if err != nil {
		t.Fatalf("RecoverTradingState before terminal transition: %v", err)
	}
	freshState := engine.NewState(7)
	if runningState.Hash() != freshState.Hash() ||
		runningState.NextStreamSequence() != 1 ||
		!runningState.Ready() {
		t.Fatalf(
			"running recovery = hash %s next %d ready %t, want %s/1/true",
			runningState.Hash(),
			runningState.NextStreamSequence(),
			runningState.Ready(),
			freshState.Hash(),
		)
	}

	faultedState, _, duplicate, err := store.ApplyTrading(
		ctx,
		runningState,
		input,
		action,
		platformpostgres.ApplyOptions{
			Ownership: ownership,
			Faults: testkit.NewFaults(
				platformpostgres.FailpointAfterPersistBeforeCommit,
			),
		},
	)
	if !errors.Is(err, platformpostgres.ErrInjectedFault) {
		t.Fatalf("faulted terminal transition error = %v", err)
	}
	if duplicate {
		t.Fatal("faulted first terminal transition was reported as duplicate")
	}
	if faultedState.Hash() != runningState.Hash() ||
		faultedState.NextStreamSequence() != runningState.NextStreamSequence() {
		t.Fatalf(
			"faulted state = hash %s next %d, want %s/%d",
			faultedState.Hash(),
			faultedState.NextStreamSequence(),
			runningState.Hash(),
			runningState.NextStreamSequence(),
		)
	}
	assertTerminalAuditLifecycle(
		t,
		rootPool,
		commandID,
		accountID,
		"pending",
		"in_progress",
		false,
		0,
		0,
		0,
		0,
	)
	assertTerminalAuditDurableEffects(
		t,
		rootPool,
		commandID,
		terminalAuditDurableEffects{},
	)

	if err := ownership.Close(ctx); err != nil {
		t.Fatalf("close pre-fault ownership: %v", err)
	}
	ownership = nil

	restartReplay, found, err := platformpostgres.NewCommandJournal(apiPool).Replay(
		ctx,
		request.Scope,
		request.IdempotencyKey,
		request.RequestHash,
	)
	if err != nil {
		t.Fatalf("Replay running command after fault: %v", err)
	}
	if !found ||
		restartReplay.CommandID != commandID ||
		restartReplay.State != platformpostgres.IdempotencyInProgress {
		t.Fatalf(
			"post-fault running replay found=%t result=%+v",
			found,
			restartReplay,
		)
	}

	restartedStore := platformpostgres.NewEngineStore(enginePool)
	restartedOwnership, err := restartedStore.AcquireShardOwnership(ctx, 7)
	if err != nil {
		t.Fatalf("AcquireShardOwnership after fault: %v", err)
	}
	t.Cleanup(func() {
		if restartedOwnership != nil {
			_ = restartedOwnership.Close(context.Background())
		}
	})
	recoveredRunning, err := restartedStore.RecoverTradingState(ctx, 7)
	if err != nil {
		t.Fatalf("RecoverTradingState after fault: %v", err)
	}
	if recoveredRunning.Hash() != runningState.Hash() ||
		recoveredRunning.NextStreamSequence() !=
			runningState.NextStreamSequence() {
		t.Fatalf(
			"post-fault recovery = hash %s next %d, want %s/%d",
			recoveredRunning.Hash(),
			recoveredRunning.NextStreamSequence(),
			runningState.Hash(),
			runningState.NextStreamSequence(),
		)
	}
	assertTerminalAuditDurableEffects(
		t,
		rootPool,
		commandID,
		terminalAuditDurableEffects{},
	)

	terminalState, terminalDecision, duplicate, err := restartedStore.ApplyTrading(
		ctx,
		recoveredRunning,
		input,
		action,
		platformpostgres.ApplyOptions{Ownership: restartedOwnership},
	)
	if err != nil {
		t.Fatalf("retry terminal transition: %v", err)
	}
	if duplicate ||
		terminalDecision.CommandResult.Status != engine.CommandStatusAccepted ||
		terminalState.Hash() == recoveredRunning.Hash() ||
		terminalState.NextStreamSequence() != 2 {
		t.Fatalf(
			"terminal transition duplicate=%t status=%s hash=%s next=%d",
			duplicate,
			terminalDecision.CommandResult.Status,
			terminalState.Hash(),
			terminalState.NextStreamSequence(),
		)
	}
	assertTerminalConfigureAccountEffects(t, terminalDecision, accountID)
	terminalEffects := terminalAuditDurableEffects{accounts: 1}
	assertTerminalAuditLifecycle(
		t,
		rootPool,
		commandID,
		accountID,
		"accepted",
		"completed",
		true,
		1,
		0,
		1,
		1,
	)
	assertTerminalAuditDurableEffects(
		t,
		rootPool,
		commandID,
		terminalEffects,
	)
	assertStoredTerminalDecision(
		t,
		rootPool,
		commandID,
		terminalDecision,
	)

	completedReplay, found, err := platformpostgres.NewCommandJournal(apiPool).Replay(
		ctx,
		request.Scope,
		request.IdempotencyKey,
		request.RequestHash,
	)
	if err != nil {
		t.Fatalf("Replay completed command: %v", err)
	}
	if !found ||
		completedReplay.CommandID != commandID ||
		completedReplay.State != platformpostgres.IdempotencyCompleted ||
		completedReplay.Response.Status != request.Response.Status ||
		string(completedReplay.Response.Headers) !=
			string(request.Response.Headers) ||
		string(completedReplay.Response.Body) != string(request.Response.Body) {
		t.Fatalf(
			"completed replay found=%t result=%+v",
			found,
			completedReplay,
		)
	}
	assertTerminalAuditDurableEffects(
		t,
		rootPool,
		commandID,
		terminalEffects,
	)

	sameSequenceState, sameSequenceDecision, duplicate, err :=
		restartedStore.ApplyTrading(
			ctx,
			terminalState,
			input,
			action,
			platformpostgres.ApplyOptions{Ownership: restartedOwnership},
		)
	if err != nil {
		t.Fatalf("same-sequence terminal replay: %v", err)
	}
	if !duplicate ||
		sameSequenceState.Hash() != terminalState.Hash() ||
		sameSequenceState.NextStreamSequence() !=
			terminalState.NextStreamSequence() {
		t.Fatalf(
			"same-sequence replay duplicate=%t hash=%s next=%d decision=%s",
			duplicate,
			sameSequenceState.Hash(),
			sameSequenceState.NextStreamSequence(),
			sameSequenceDecision.DecisionHash,
		)
	}
	assertDecisionEqual(
		t,
		"same-sequence business replay",
		terminalDecision,
		sameSequenceDecision,
	)
	assertTerminalAuditLifecycle(
		t,
		rootPool,
		commandID,
		accountID,
		"accepted",
		"completed",
		true,
		1,
		0,
		1,
		1,
	)
	assertTerminalAuditDurableEffects(
		t,
		rootPool,
		commandID,
		terminalEffects,
	)

	republishedInput := input
	republishedInput.StreamSequence = terminalState.NextStreamSequence()
	republishedState, republishedDecision, duplicate, err :=
		restartedStore.ApplyTrading(
			ctx,
			terminalState,
			republishedInput,
			action,
			platformpostgres.ApplyOptions{Ownership: restartedOwnership},
		)
	if err != nil {
		t.Fatalf("later-sequence terminal replay: %v", err)
	}
	if !duplicate ||
		republishedDecision.DuplicateOfDecisionHash !=
			terminalDecision.DecisionHash ||
		republishedDecision.PreviousStateHash != terminalState.Hash() ||
		republishedDecision.NextStateHash != republishedState.Hash() ||
		republishedDecision.StreamSequence != republishedInput.StreamSequence ||
		republishedState.Hash() == terminalState.Hash() ||
		republishedState.NextStreamSequence() != 3 {
		t.Fatalf(
			"later-sequence replay duplicate=%t duplicateOf=%s "+
				"previous=%s nextHash=%s state=%s next=%d",
			duplicate,
			republishedDecision.DuplicateOfDecisionHash,
			republishedDecision.PreviousStateHash,
			republishedDecision.NextStateHash,
			republishedState.Hash(),
			republishedState.NextStreamSequence(),
		)
	}
	assertNoEconomicEffects(t, republishedDecision)
	assertTerminalAuditLifecycle(
		t,
		rootPool,
		commandID,
		accountID,
		"accepted",
		"completed",
		true,
		1,
		1,
		1,
		1,
	)
	assertTerminalAuditDurableEffects(
		t,
		rootPool,
		commandID,
		terminalEffects,
	)
	assertStoredDuplicateDecision(
		t,
		rootPool,
		commandID,
		republishedInput.StreamSequence,
		republishedDecision,
	)

	replayedDeliveryState, replayedDeliveryDecision, duplicate, err :=
		restartedStore.ApplyTrading(
			ctx,
			republishedState,
			republishedInput,
			action,
			platformpostgres.ApplyOptions{Ownership: restartedOwnership},
		)
	if err != nil {
		t.Fatalf("exact duplicate-delivery replay: %v", err)
	}
	if !duplicate ||
		replayedDeliveryState.Hash() != republishedState.Hash() ||
		replayedDeliveryState.NextStreamSequence() !=
			republishedState.NextStreamSequence() {
		t.Fatalf(
			"exact duplicate-delivery replay duplicate=%t hash=%s next=%d",
			duplicate,
			replayedDeliveryState.Hash(),
			replayedDeliveryState.NextStreamSequence(),
		)
	}
	assertDecisionEqual(
		t,
		"exact duplicate-delivery replay",
		republishedDecision,
		replayedDeliveryDecision,
	)
	assertTerminalAuditLifecycle(
		t,
		rootPool,
		commandID,
		accountID,
		"accepted",
		"completed",
		true,
		1,
		1,
		1,
		1,
	)
	assertTerminalAuditDurableEffects(
		t,
		rootPool,
		commandID,
		terminalEffects,
	)

	if err := restartedOwnership.Close(ctx); err != nil {
		t.Fatalf("close terminal ownership: %v", err)
	}
	restartedOwnership = nil

	finalStore := platformpostgres.NewEngineStore(enginePool)
	finalOwnership, err := finalStore.AcquireShardOwnership(ctx, 7)
	if err != nil {
		t.Fatalf("AcquireShardOwnership for final recovery: %v", err)
	}
	t.Cleanup(func() {
		if finalOwnership != nil {
			_ = finalOwnership.Close(context.Background())
		}
	})
	recoveredTerminal, err := finalStore.RecoverTradingState(ctx, 7)
	if err != nil {
		t.Fatalf("RecoverTradingState after terminal replay: %v", err)
	}
	if recoveredTerminal.Hash() != republishedState.Hash() ||
		recoveredTerminal.NextStreamSequence() !=
			republishedState.NextStreamSequence() ||
		!recoveredTerminal.Ready() {
		t.Fatalf(
			"terminal recovery = hash %s next %d ready %t, want %s/%d/true",
			recoveredTerminal.Hash(),
			recoveredTerminal.NextStreamSequence(),
			recoveredTerminal.Ready(),
			republishedState.Hash(),
			republishedState.NextStreamSequence(),
		)
	}
	assertTerminalAuditLifecycle(
		t,
		rootPool,
		commandID,
		accountID,
		"accepted",
		"completed",
		true,
		1,
		1,
		1,
		1,
	)
	assertTerminalAuditDurableEffects(
		t,
		rootPool,
		commandID,
		terminalEffects,
	)
	assertStoredTerminalDecision(
		t,
		rootPool,
		commandID,
		terminalDecision,
	)
	assertStoredDuplicateDecision(
		t,
		rootPool,
		commandID,
		republishedInput.StreamSequence,
		republishedDecision,
	)

	if err := finalOwnership.Close(ctx); err != nil {
		t.Fatalf("close final recovery ownership: %v", err)
	}
	finalOwnership = nil
	report, err := finalStore.ReconcileShard(ctx, 7)
	if err != nil {
		t.Fatalf("ReconcileShard after terminal replay: %v", err)
	}
	if !report.Ready ||
		report.ReceiptCount != 1 ||
		report.DuplicateDeliveryCount != 1 ||
		report.NextStreamSequence != 3 ||
		report.DeliveryMismatchCount != 0 ||
		report.CommandMismatchCount != 0 ||
		report.ConfigurationMismatchCount != 0 ||
		report.MessagingMismatchCount != 0 {
		t.Fatalf("terminal audit reconciliation = %+v", report)
	}
}

func terminalAuditCommandRequest(
	t *testing.T,
	commandID engine.ID,
	accountID string,
	shardID engine.ShardID,
	logicalTime time.Time,
) platformpostgres.BeginCommandRequest {
	t.Helper()
	action := engine.TradingAction{
		Kind: engine.TradingActionConfigureAccount,
		ConfigureAccount: &engine.ConfigureAccount{
			AccountID: accountID,
			OmsMode:   engine.OmsModeNetting,
		},
	}
	payload, err := engine.EncodeTradingAction(action)
	if err != nil {
		t.Fatalf("EncodeTradingAction: %v", err)
	}
	input := engine.InputEnvelope{
		InputID:              commandID,
		SchemaVersion:        engine.CurrentSchemaVersion,
		ShardID:              shardID,
		Kind:                 engine.InputKindCommand,
		SourceID:             "terminal-audit",
		SourceSequence:       1,
		LogicalTime:          engine.NewLogicalTime(logicalTime),
		ConfigurationVersion: 1,
		InstrumentVersion:    1,
		Payload:              payload,
	}
	outboxPayload, err := engine.EncodeInputMessage(input)
	if err != nil {
		t.Fatalf("EncodeInputMessage: %v", err)
	}
	return platformpostgres.BeginCommandRequest{
		Scope:            "terminal-audit:" + accountID,
		IdempotencyKey:   "correlation-1",
		RequestHash:      sha256.Sum256(outboxPayload),
		CommandID:        commandID,
		AccountID:        accountID,
		AccountSequence:  1,
		CommandType:      string(action.Kind),
		SchemaVersion:    input.SchemaVersion,
		CanonicalPayload: payload.Bytes(),
		OutboxSubject: fmt.Sprintf(
			"engine.input.%d.command.v%d",
			shardID,
			input.SchemaVersion,
		),
		OutboxPayload: outboxPayload,
		LogicalTime:   logicalTime,
		ExpiresAt:     logicalTime.Add(24 * time.Hour),
		Response: platformpostgres.StoredResponse{
			Status:  202,
			Headers: []byte(`{"content-type":["application/json"]}`),
			Body:    []byte(`{"status":"accepted"}`),
		},
	}
}

func assertTerminalAuditLifecycle(
	t *testing.T,
	pool *pgxpool.Pool,
	commandID engine.ID,
	accountID string,
	wantCommandStatus string,
	wantIdempotencyState string,
	wantTerminal bool,
	wantBusinessReceipts int,
	wantDuplicateReceipts int,
	wantAccounts int,
	wantCheckpoints int,
) {
	t.Helper()
	var (
		commandRows       int
		commandStatus     string
		resultPresent     bool
		completedPresent  bool
		idempotencyRows   int
		idempotencyState  string
		commandOutboxRows int
		businessReceipts  int
		duplicateReceipts int
		accountRows       int
		accountMode       string
		accountVersion    uint64
		checkpointRows    int
	)
	if err := pool.QueryRow(context.Background(), `
		SELECT
			(SELECT count(*)
			   FROM trading.commands
			  WHERE command_id = $1),
			COALESCE((
				SELECT status
				  FROM trading.commands
				 WHERE command_id = $1
			), ''),
			COALESCE((
				SELECT result IS NOT NULL
				  FROM trading.commands
				 WHERE command_id = $1
			), false),
			COALESCE((
				SELECT completed_at IS NOT NULL
				  FROM trading.commands
				 WHERE command_id = $1
			), false),
			(SELECT count(*)
			   FROM trading.idempotency_records
			  WHERE command_id = $1),
			COALESCE((
				SELECT state
				  FROM trading.idempotency_records
				 WHERE command_id = $1
			), ''),
			(SELECT count(*)
			   FROM messaging.outbox
			  WHERE message_id = $1),
			(SELECT count(*)
			   FROM engine.input_receipts
			  WHERE input_id = $1),
			(SELECT count(*)
			   FROM engine.duplicate_delivery_receipts
			  WHERE input_id = $1),
			(SELECT count(*)
			   FROM trading.accounts
			  WHERE account_id = $2),
			COALESCE((
				SELECT oms_mode
				  FROM trading.accounts
				 WHERE account_id = $2
			), ''),
			COALESCE((
				SELECT version
				  FROM trading.accounts
				 WHERE account_id = $2
			), 0),
			(SELECT count(*)
			   FROM engine.shard_checkpoints
			  WHERE shard_id = 7)`,
		commandID.String(),
		accountID,
	).Scan(
		&commandRows,
		&commandStatus,
		&resultPresent,
		&completedPresent,
		&idempotencyRows,
		&idempotencyState,
		&commandOutboxRows,
		&businessReceipts,
		&duplicateReceipts,
		&accountRows,
		&accountMode,
		&accountVersion,
		&checkpointRows,
	); err != nil {
		t.Fatalf("inspect terminal audit lifecycle: %v", err)
	}
	wantAccountMode := ""
	var wantAccountVersion uint64
	if wantAccounts == 1 {
		wantAccountMode = string(engine.OmsModeNetting)
		wantAccountVersion = 1
	}
	if commandRows != 1 ||
		commandStatus != wantCommandStatus ||
		resultPresent != wantTerminal ||
		completedPresent != wantTerminal ||
		idempotencyRows != 1 ||
		idempotencyState != wantIdempotencyState ||
		commandOutboxRows != 1 ||
		businessReceipts != wantBusinessReceipts ||
		duplicateReceipts != wantDuplicateReceipts ||
		accountRows != wantAccounts ||
		accountMode != wantAccountMode ||
		accountVersion != wantAccountVersion ||
		checkpointRows != wantCheckpoints {
		t.Fatalf(
			"terminal audit lifecycle = commands %d/%s result %t completed %t "+
				"idempotency %d/%s outbox %d business receipts %d "+
				"duplicate receipts %d accounts %d/%s/v%d checkpoints %d",
			commandRows,
			commandStatus,
			resultPresent,
			completedPresent,
			idempotencyRows,
			idempotencyState,
			commandOutboxRows,
			businessReceipts,
			duplicateReceipts,
			accountRows,
			accountMode,
			accountVersion,
			checkpointRows,
		)
	}
}

func assertStoredTerminalDecision(
	t *testing.T,
	pool *pgxpool.Pool,
	commandID engine.ID,
	want engine.Decision,
) {
	t.Helper()
	var decisionJSON []byte
	var commandResultJSON []byte
	if err := pool.QueryRow(context.Background(), `
		SELECT receipt.decision, command.result
		  FROM engine.input_receipts AS receipt
		  JOIN trading.commands AS command
		    ON command.command_id = receipt.input_id
		 WHERE receipt.input_id = $1`,
		commandID.String(),
	).Scan(&decisionJSON, &commandResultJSON); err != nil {
		t.Fatalf("read stored terminal audit decision: %v", err)
	}
	var storedDecision engine.Decision
	if err := json.Unmarshal(decisionJSON, &storedDecision); err != nil {
		t.Fatalf("decode stored terminal audit decision: %v", err)
	}
	var storedCommandResult engine.CommandResult
	if err := json.Unmarshal(commandResultJSON, &storedCommandResult); err != nil {
		t.Fatalf("decode stored terminal command result: %v", err)
	}
	assertCanonicalJSONEqual(
		t,
		"stored terminal audit decision",
		want,
		storedDecision,
	)
	assertCanonicalJSONEqual(
		t,
		"stored terminal command result",
		want.CommandResult,
		storedCommandResult,
	)
}

func assertStoredDuplicateDecision(
	t *testing.T,
	pool *pgxpool.Pool,
	commandID engine.ID,
	streamSequence uint64,
	want engine.Decision,
) {
	t.Helper()
	var decisionJSON []byte
	if err := pool.QueryRow(context.Background(), `
		SELECT decision
		  FROM engine.duplicate_delivery_receipts
		 WHERE input_id = $1
		   AND stream_sequence = $2`,
		commandID.String(),
		streamSequence,
	).Scan(&decisionJSON); err != nil {
		t.Fatalf("read stored duplicate-delivery decision: %v", err)
	}
	var storedDecision engine.Decision
	if err := json.Unmarshal(decisionJSON, &storedDecision); err != nil {
		t.Fatalf("decode stored duplicate-delivery decision: %v", err)
	}
	assertCanonicalJSONEqual(
		t,
		"stored duplicate-delivery decision",
		want,
		storedDecision,
	)
}

func assertDecisionEqual(
	t *testing.T,
	label string,
	want engine.Decision,
	got engine.Decision,
) {
	t.Helper()
	assertCanonicalJSONEqual(t, label, want, got)
}

func assertCanonicalJSONEqual(
	t *testing.T,
	label string,
	want any,
	got any,
) {
	t.Helper()
	wantJSON, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("encode wanted %s: %v", label, err)
	}
	gotJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("encode actual %s: %v", label, err)
	}
	if string(gotJSON) != string(wantJSON) {
		t.Fatalf(
			"%s differs:\n got %s\nwant %s",
			label,
			gotJSON,
			wantJSON,
		)
	}
}

type terminalAuditDurableEffects struct {
	instruments            int
	currencyScales         int
	accounts               int
	userAccounts           int
	accountProfiles        int
	riskConfigs            int
	balances               int
	ledgerTransactions     int
	ledgerEntries          int
	fundingSettlements     int
	fundingHistory         int
	books                  int
	orders                 int
	fills                  int
	positions              int
	engineOutboxEvents     int
	realtimeSequences      int
	realtimePublications   int
	realtimeRequeueRecords int
}

func assertTerminalAuditDurableEffects(
	t *testing.T,
	pool *pgxpool.Pool,
	commandID engine.ID,
	want terminalAuditDurableEffects,
) {
	t.Helper()
	var got terminalAuditDurableEffects
	if err := pool.QueryRow(context.Background(), `
		SELECT
			(SELECT count(*) FROM trading.instruments),
			(SELECT count(*) FROM trading.currency_scales),
			(SELECT count(*) FROM trading.accounts),
			(SELECT count(*) FROM identity.user_accounts),
			(SELECT count(*) FROM identity.account_profiles),
			(SELECT count(*) FROM trading.risk_configs),
			(SELECT count(*) FROM ledger.balances),
			(SELECT count(*) FROM ledger.transactions),
			(SELECT count(*) FROM ledger.entries),
			(SELECT count(*) FROM trading.funding_settlements),
			(SELECT count(*) FROM trading.funding_history_projection),
			(SELECT count(*) FROM market.books),
			(SELECT count(*) FROM trading.orders),
			(SELECT count(*) FROM trading.fills),
			(SELECT count(*) FROM trading.positions),
			(SELECT count(*)
			   FROM messaging.outbox
			  WHERE producer_class = 'engine'
			    AND engine_input_id = $1),
			(SELECT count(*) FROM realtime.channel_sequences),
			(SELECT count(*) FROM realtime.publications),
			(SELECT count(*) FROM realtime.publication_requeues)`,
		commandID.String(),
	).Scan(
		&got.instruments,
		&got.currencyScales,
		&got.accounts,
		&got.userAccounts,
		&got.accountProfiles,
		&got.riskConfigs,
		&got.balances,
		&got.ledgerTransactions,
		&got.ledgerEntries,
		&got.fundingSettlements,
		&got.fundingHistory,
		&got.books,
		&got.orders,
		&got.fills,
		&got.positions,
		&got.engineOutboxEvents,
		&got.realtimeSequences,
		&got.realtimePublications,
		&got.realtimeRequeueRecords,
	); err != nil {
		t.Fatalf("inspect terminal audit durable effects: %v", err)
	}
	if got != want {
		t.Fatalf("terminal audit durable effects = %+v, want %+v", got, want)
	}
}

func assertTerminalConfigureAccountEffects(
	t *testing.T,
	decision engine.Decision,
	accountID string,
) {
	t.Helper()
	if decision.CommandResult.Status != engine.CommandStatusAccepted ||
		decision.CommandResult.Reason != "" ||
		len(decision.AccountChanges) != 1 ||
		decision.AccountChanges[0].AccountID != accountID ||
		decision.AccountChanges[0].OmsMode != engine.OmsModeNetting {
		t.Fatalf(
			"terminal result/effects = %+v/%+v, want accepted with one %s/NETTING account",
			decision.CommandResult,
			decision.AccountChanges,
			accountID,
		)
	}
	if len(decision.InstrumentChanges) != 0 ||
		len(decision.RiskChanges) != 0 ||
		len(decision.BalanceChanges) != 0 ||
		len(decision.LedgerChanges) != 0 ||
		len(decision.FundingChanges) != 0 ||
		len(decision.BookChanges) != 0 ||
		len(decision.OrderChanges) != 0 ||
		len(decision.Fills) != 0 ||
		len(decision.PositionChanges) != 0 ||
		len(decision.Events) != 0 {
		t.Fatalf(
			"terminal ConfigureAccount contains forbidden effects: %+v",
			decision,
		)
	}
}

func assertNoEconomicEffects(t *testing.T, decision engine.Decision) {
	t.Helper()
	if len(decision.InstrumentChanges) != 0 ||
		len(decision.AccountChanges) != 0 ||
		len(decision.RiskChanges) != 0 ||
		len(decision.BalanceChanges) != 0 ||
		len(decision.LedgerChanges) != 0 ||
		len(decision.FundingChanges) != 0 ||
		len(decision.BookChanges) != 0 ||
		len(decision.OrderChanges) != 0 ||
		len(decision.Fills) != 0 ||
		len(decision.PositionChanges) != 0 ||
		len(decision.Events) != 0 {
		t.Fatalf("duplicate delivery contains economic effects: %+v", decision)
	}
}
