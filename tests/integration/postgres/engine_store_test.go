package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	platformnats "github.com/upcomers-org/platformgo/internal/adapters/nats"
	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
	"github.com/upcomers-org/platformgo/internal/engine"
	"github.com/upcomers-org/platformgo/testkit"
)

func TestEngineStoreCommitsReceiptLedgerStateAndCheckpointAtomically(t *testing.T) {
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	migrator := platformpostgres.NewMigrator(
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	)
	if err := migrator.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	store := platformpostgres.NewEngineStore(pool)
	state := engine.NewState(7)
	ids := testkit.NewShardIDSequence(7)
	clock := testkit.NewManualClock(
		engine.NewLogicalTime(time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)),
	)

	configureInstrument := engine.TradingAction{
		Kind: engine.TradingActionConfigureInstrument,
		ConfigureInstrument: &engine.ConfigureInstrument{
			InstrumentID:            "BTC-PERP",
			Revision:                1,
			PriceScale:              2,
			QuantityScale:           3,
			SettlementCurrency:      "USDC",
			SettlementCurrencyScale: 2,
			InitialMarginRate:       "0.1",
			MaintenanceMarginRate:   "0.05",
			MaxLeverage:             "10",
			MakerFeeRate:            "0.001",
			TakerFeeRate:            "0.002",
		},
	}
	state, _, _, _ = applyStoredTrading(
		t, pool, store, state, ids, clock, configureInstrument,
		platformpostgres.ApplyOptions{},
	)

	configureAccount := engine.TradingAction{
		Kind: engine.TradingActionConfigureAccount,
		ConfigureAccount: &engine.ConfigureAccount{
			AccountID: "account-1",
			OmsMode:   engine.OmsModeNetting,
		},
	}
	state, _, _, _ = applyStoredTrading(
		t, pool, store, state, ids, clock, configureAccount,
		platformpostgres.ApplyOptions{},
	)

	deposit := engine.TradingAction{
		Kind: engine.TradingActionAdjustBalance,
		AdjustBalance: &engine.AdjustBalance{
			AccountID:     "account-1",
			Currency:      "USDC",
			CurrencyScale: 2,
			Operation:     engine.BalanceOperationDeposit,
			Amount:        "10",
		},
	}
	var depositInput engine.InputEnvelope
	var depositDecision engine.Decision
	state, depositDecision, depositInput, _ = applyStoredTrading(
		t, pool, store, state, ids, clock, deposit, platformpostgres.ApplyOptions{},
	)

	faultedDeposit := deposit
	faultedDeposit.AdjustBalance = &engine.AdjustBalance{
		AccountID:     "account-1",
		Currency:      "USDC",
		CurrencyScale: 2,
		Operation:     engine.BalanceOperationDeposit,
		Amount:        "5",
	}
	faultInput := nextStoredInput(t, state, ids, clock, faultedDeposit)
	seedPendingCommand(t, pool, faultInput, faultedDeposit)
	beforeFaultHash := state.Hash()
	faults := testkit.NewFaults(platformpostgres.FailpointAfterPersistBeforeCommit)
	next, _, _, err := store.ApplyTrading(
		context.Background(),
		state,
		faultInput,
		faultedDeposit,
		platformpostgres.ApplyOptions{Faults: faults},
	)
	if !errors.Is(err, platformpostgres.ErrInjectedFault) {
		t.Fatalf("faulted ApplyTrading error = %v, want ErrInjectedFault", err)
	}
	if next.Hash() != beforeFaultHash {
		t.Fatal("rolled-back transaction returned mutated engine state")
	}
	assertRowCount(t, pool, "engine.input_receipts", 3)
	assertRowCount(t, pool, "ledger.transactions", 1)
	assertRowCount(t, pool, "ledger.entries", 2)

	var faultDecision engine.Decision
	state, faultDecision, _, _ = applyStoredInput(
		t,
		pool,
		store,
		state,
		faultInput,
		faultedDeposit,
		platformpostgres.ApplyOptions{},
	)
	if faultDecision.CommandResult.Status != engine.CommandStatusAccepted {
		t.Fatalf("retried deposit status = %s", faultDecision.CommandResult.Status)
	}

	book := engine.TradingAction{
		Kind: engine.TradingActionUpdateBook,
		UpdateBook: &engine.UpdateBook{
			InstrumentID: "BTC-PERP",
			MarkPrice:    "100",
			Bids:         []engine.BookLevel{{Price: "99", Quantity: "1"}},
			Asks:         []engine.BookLevel{{Price: "101", Quantity: "1"}},
		},
	}
	state, _, _, _ = applyStoredTrading(
		t, pool, store, state, ids, clock, book, platformpostgres.ApplyOptions{},
	)

	beforeDuplicateHash := state.Hash()
	duplicateState, duplicateDecision, duplicate, err := store.ApplyTrading(
		context.Background(),
		state,
		depositInput,
		deposit,
		platformpostgres.ApplyOptions{},
	)
	if err != nil {
		t.Fatalf("historical duplicate ApplyTrading: %v", err)
	}
	if !duplicate {
		t.Fatal("historical duplicate was treated as new")
	}
	if duplicateState.Hash() != beforeDuplicateHash {
		t.Fatal("historical duplicate mutated state")
	}
	if duplicateDecision.DecisionHash != depositDecision.DecisionHash {
		t.Fatal("historical duplicate returned a different decision")
	}
	assertRowCount(t, pool, "engine.input_receipts", 5)
	assertRowCount(t, pool, "ledger.transactions", 2)
	assertRowCount(t, pool, "ledger.entries", 4)

	republishedInput := depositInput
	republishedInput.StreamSequence = state.NextStreamSequence()
	republishedState, republishedDecision, duplicate, err := store.ApplyTrading(
		context.Background(),
		state,
		republishedInput,
		deposit,
		platformpostgres.ApplyOptions{},
	)
	if err != nil || !duplicate {
		t.Fatalf(
			"re-published duplicate = duplicate %t error %v",
			duplicate,
			err,
		)
	}
	if republishedDecision.DuplicateOfDecisionHash != depositDecision.DecisionHash ||
		republishedState.NextStreamSequence() != state.NextStreamSequence()+1 {
		t.Fatalf(
			"re-published decision/state = %+v / next %d",
			republishedDecision,
			republishedState.NextStreamSequence(),
		)
	}
	state = republishedState
	assertRowCount(t, pool, "engine.duplicate_delivery_receipts", 1)
	assertRowCount(t, pool, "ledger.transactions", 2)
	assertRowCount(t, pool, "ledger.entries", 4)

	recovered, err := store.RecoverTradingState(context.Background(), 7)
	if err != nil {
		t.Fatalf("RecoverTradingState: %v", err)
	}
	if recovered.Hash() != state.Hash() ||
		recovered.NextStreamSequence() != state.NextStreamSequence() {
		t.Fatalf(
			"recovered checkpoint = (%s, %d), want (%s, %d)",
			recovered.Hash(),
			recovered.NextStreamSequence(),
			state.Hash(),
			state.NextStreamSequence(),
		)
	}
	balance, ok := recovered.Balance("account-1", "USDC")
	if !ok || balance.Total != "15" {
		t.Fatalf("recovered balance = %+v, %t, want total 15", balance, ok)
	}

	gapAction := engine.TradingAction{
		Kind: engine.TradingActionUpdateBook,
		UpdateBook: &engine.UpdateBook{
			InstrumentID: "BTC-PERP",
			MarkPrice:    "102",
			Bids:         []engine.BookLevel{{Price: "101", Quantity: "1"}},
			Asks:         []engine.BookLevel{{Price: "103", Quantity: "1"}},
		},
	}
	gapInput := nextStoredInput(t, state, ids, clock, gapAction)
	gapInput.StreamSequence++
	halted, _, _, err := store.ApplyTrading(
		context.Background(),
		state,
		gapInput,
		gapAction,
		platformpostgres.ApplyOptions{},
	)
	if !errors.Is(err, engine.ErrSequenceGap) {
		t.Fatalf("sequence gap error = %v, want ErrSequenceGap", err)
	}
	if halted.Ready() {
		t.Fatal("sequence gap did not durably halt the shard")
	}
	assertRowCount(t, pool, "engine.shard_faults", 1)

	recoveredHalted, err := store.RecoverTradingState(context.Background(), 7)
	if err != nil {
		t.Fatalf("RecoverTradingState after halt: %v", err)
	}
	if recoveredHalted.Ready() || recoveredHalted.Hash() != halted.Hash() {
		t.Fatalf(
			"recovered halt = (%t, %s), want (%t, %s)",
			recoveredHalted.Ready(),
			recoveredHalted.Hash(),
			halted.Ready(),
			halted.Hash(),
		)
	}
	report, err := store.ReconcileShard(context.Background(), 7)
	if err != nil {
		t.Fatalf("ReconcileShard: %v", err)
	}
	if report.ReceiptCount != 5 ||
		report.DuplicateDeliveryCount != 1 ||
		report.NextStreamSequence != 7 ||
		report.Ready {
		t.Fatalf("reconciliation report = %+v", report)
	}
	if _, err := pool.Exec(context.Background(), `
		UPDATE ledger.balances
		   SET total = 16, free = 16, equity = 16
		 WHERE account_id = 'account-1' AND currency = 'USDC'`,
	); err != nil {
		t.Fatalf("tamper balance projection: %v", err)
	}
	if _, err := store.ReconcileShard(
		context.Background(),
		7,
	); !errors.Is(err, platformpostgres.ErrReconciliationMismatch) {
		t.Fatalf(
			"tampered reconciliation error = %v, want ErrReconciliationMismatch",
			err,
		)
	}
}

func TestEngineStorePersistsOrdersFillsPositionsAndEvents(t *testing.T) {
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := platformpostgres.NewMigrator(
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	store := platformpostgres.NewEngineStore(pool)
	state := engine.NewState(8)
	ids := testkit.NewShardIDSequence(8)
	clock := testkit.NewManualClock(
		engine.NewLogicalTime(time.Date(2026, time.July, 24, 13, 0, 0, 0, time.UTC)),
	)
	state, _, _, _ = applyStoredTrading(t, pool, store, state, ids, clock, engine.TradingAction{
		Kind: engine.TradingActionConfigureInstrument,
		ConfigureInstrument: &engine.ConfigureInstrument{
			InstrumentID:            "BTC-PERP",
			Revision:                1,
			PriceScale:              2,
			QuantityScale:           3,
			SettlementCurrency:      "USDC",
			SettlementCurrencyScale: 2,
			InitialMarginRate:       "0.1",
			MaintenanceMarginRate:   "0.05",
			MaxLeverage:             "10",
			MakerFeeRate:            "0",
			TakerFeeRate:            "0",
		},
	}, platformpostgres.ApplyOptions{})
	state, _, _, _ = applyStoredTrading(t, pool, store, state, ids, clock, engine.TradingAction{
		Kind: engine.TradingActionConfigureAccount,
		ConfigureAccount: &engine.ConfigureAccount{
			AccountID: "account-1",
			OmsMode:   engine.OmsModeNetting,
		},
	}, platformpostgres.ApplyOptions{})
	state, _, _, _ = applyStoredTrading(t, pool, store, state, ids, clock, engine.TradingAction{
		Kind: engine.TradingActionAdjustBalance,
		AdjustBalance: &engine.AdjustBalance{
			AccountID:     "account-1",
			Currency:      "USDC",
			CurrencyScale: 2,
			Operation:     engine.BalanceOperationDeposit,
			Amount:        "1000",
		},
	}, platformpostgres.ApplyOptions{})
	state, _, _, _ = applyStoredTrading(t, pool, store, state, ids, clock, engine.TradingAction{
		Kind: engine.TradingActionUpdateBook,
		UpdateBook: &engine.UpdateBook{
			InstrumentID: "BTC-PERP",
			MarkPrice:    "100",
			Bids:         []engine.BookLevel{{Price: "99", Quantity: "10"}},
			Asks:         []engine.BookLevel{{Price: "100", Quantity: "10"}},
		},
	}, platformpostgres.ApplyOptions{})

	orderID := engine.IDFromSequence(engine.ID{}, 801)
	var orderInput engine.InputEnvelope
	var orderDecision engine.Decision
	state, orderDecision, orderInput, _ = applyStoredTrading(
		t,
		pool,
		store,
		state,
		ids,
		clock,
		engine.TradingAction{
			Kind: engine.TradingActionSubmitOrder,
			SubmitOrder: &engine.SubmitOrder{
				OrderID:      orderID,
				AccountID:    "account-1",
				InstrumentID: "BTC-PERP",
				Side:         engine.SideBuy,
				Type:         engine.OrderTypeMarket,
				TimeInForce:  engine.TimeInForceIOC,
				Quantity:     "1",
			},
		},
		platformpostgres.ApplyOptions{},
	)
	if len(orderDecision.Fills) != 1 ||
		len(orderDecision.PositionChanges) != 1 ||
		len(orderDecision.Events) == 0 {
		t.Fatalf("order decision = %+v", orderDecision)
	}
	assertRowCount(t, pool, "trading.orders", 1)
	assertRowCount(t, pool, "trading.fills", 1)
	assertRowCount(t, pool, "trading.positions", 1)
	assertRowCount(t, pool, "messaging.domain_outbox", len(orderDecision.Events))
	var eventSubject string
	var envelopeMessageID string
	if err := pool.QueryRow(context.Background(), `
		SELECT subject, payload ->> 'messageId'
		  FROM messaging.outbox
		 WHERE subject LIKE 'domain.v1.%'
		 ORDER BY created_at, message_id
		 LIMIT 1`,
	).Scan(&eventSubject, &envelopeMessageID); err != nil {
		t.Fatalf("read event outbox envelope: %v", err)
	}
	if !strings.HasPrefix(eventSubject, "domain.v1.") ||
		envelopeMessageID != orderDecision.Events[0].EventID.String() {
		t.Fatalf(
			"event outbox = subject %q message ID %q",
			eventSubject,
			envelopeMessageID,
		)
	}

	duplicate, _, duplicateFound, err := store.ApplyTrading(
		context.Background(),
		state,
		orderInput,
		engine.TradingAction{
			Kind: engine.TradingActionSubmitOrder,
			SubmitOrder: &engine.SubmitOrder{
				OrderID:      orderID,
				AccountID:    "account-1",
				InstrumentID: "BTC-PERP",
				Side:         engine.SideBuy,
				Type:         engine.OrderTypeMarket,
				TimeInForce:  engine.TimeInForceIOC,
				Quantity:     "1",
			},
		},
		platformpostgres.ApplyOptions{},
	)
	if err != nil || !duplicateFound || duplicate.Hash() != state.Hash() {
		t.Fatalf(
			"duplicate order = found %t hash %s error %v",
			duplicateFound,
			duplicate.Hash(),
			err,
		)
	}
	assertRowCount(t, pool, "trading.fills", 1)
	assertRowCount(t, pool, "messaging.domain_outbox", len(orderDecision.Events))

	recovered, err := store.RecoverTradingState(context.Background(), 8)
	if err != nil {
		t.Fatalf("RecoverTradingState: %v", err)
	}
	if recovered.Hash() != state.Hash() ||
		len(recovered.FillsForOrder(orderID)) != 1 ||
		len(recovered.OpenPositions("account-1")) != 1 {
		t.Fatalf(
			"recovered order state = hash %s fills %d positions %d",
			recovered.Hash(),
			len(recovered.FillsForOrder(orderID)),
			len(recovered.OpenPositions("account-1")),
		)
	}
}

func TestEngineStoreRejectsCommandInputThatDiffersFromJournal(t *testing.T) {
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := platformpostgres.NewMigrator(
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	storedAction := engine.TradingAction{
		Kind: engine.TradingActionConfigureAccount,
		ConfigureAccount: &engine.ConfigureAccount{
			AccountID: "account-1",
			OmsMode:   engine.OmsModeNetting,
		},
	}
	storedPayload, err := engine.EncodeTradingAction(storedAction)
	if err != nil {
		t.Fatalf("EncodeTradingAction stored: %v", err)
	}
	deliveredAction := engine.TradingAction{
		Kind: engine.TradingActionConfigureAccount,
		ConfigureAccount: &engine.ConfigureAccount{
			AccountID: "account-2",
			OmsMode:   engine.OmsModeHedging,
		},
	}
	deliveredPayload, err := engine.EncodeTradingAction(deliveredAction)
	if err != nil {
		t.Fatalf("EncodeTradingAction delivered: %v", err)
	}

	commandID := engine.IDFromSequence(engine.ID{}, 301)
	now := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	storedInput := engine.InputEnvelope{
		InputID:              commandID,
		SchemaVersion:        engine.CurrentSchemaVersion,
		ShardID:              7,
		Kind:                 engine.InputKindCommand,
		SourceID:             "command-journal",
		SourceSequence:       1,
		LogicalTime:          engine.NewLogicalTime(now),
		ConfigurationVersion: 1,
		InstrumentVersion:    1,
		Payload:              storedPayload,
	}
	outboxPayload, err := engine.EncodeInputMessage(storedInput)
	if err != nil {
		t.Fatalf("EncodeInputMessage stored: %v", err)
	}
	if _, err := platformpostgres.NewCommandJournal(pool).Begin(
		context.Background(),
		platformpostgres.BeginCommandRequest{
			Scope:            "account:account-1",
			IdempotencyKey:   "configure-account",
			RequestHash:      [32]byte{1},
			CommandID:        commandID,
			AccountID:        "account-1",
			AccountSequence:  1,
			CommandType:      string(storedAction.Kind),
			SchemaVersion:    engine.CurrentSchemaVersion,
			CanonicalPayload: storedPayload.Bytes(),
			OutboxSubject:    "engine.input.7.command.v1",
			OutboxPayload:    outboxPayload,
			LogicalTime:      now,
			ExpiresAt:        now.Add(24 * time.Hour),
		},
	); err != nil {
		t.Fatalf("Begin: %v", err)
	}

	input := engine.InputEnvelope{
		InputID:              commandID,
		SchemaVersion:        engine.CurrentSchemaVersion,
		ShardID:              7,
		Kind:                 engine.InputKindCommand,
		SourceID:             "command-journal",
		SourceSequence:       1,
		StreamSequence:       1,
		LogicalTime:          engine.NewLogicalTime(now),
		ConfigurationVersion: 1,
		InstrumentVersion:    1,
		Payload:              deliveredPayload,
	}
	state := engine.NewState(7)
	next, _, _, err := platformpostgres.NewEngineStore(pool).ApplyTrading(
		context.Background(),
		state,
		input,
		deliveredAction,
		platformpostgres.ApplyOptions{},
	)
	if !errors.Is(err, platformpostgres.ErrCommandInputConflict) {
		t.Fatalf("ApplyTrading error = %v, want ErrCommandInputConflict", err)
	}
	if next.Ready() || next.Hash() == state.Hash() || next.NextStreamSequence() != 1 {
		t.Fatalf("failed command did not halt state: %+v", next)
	}
	assertRowCount(t, pool, "trading.accounts", 0)
	assertRowCount(t, pool, "engine.input_receipts", 0)
	assertRowCount(t, pool, "engine.shard_checkpoints", 1)
	assertRowCount(t, pool, "engine.shard_faults", 1)
	recovered, err := platformpostgres.NewEngineStore(pool).RecoverTradingState(
		context.Background(),
		7,
	)
	if err != nil {
		t.Fatalf("RecoverTradingState: %v", err)
	}
	if recovered.Ready() || recovered.Hash() != next.Hash() {
		t.Fatalf("recovered conflict state = %+v, want halted hash %s", recovered, next.Hash())
	}
}

func TestEngineStoreRecoveryRejectsInvalidBusinessHashMetadata(t *testing.T) {
	tests := []struct {
		name    string
		corrupt string
	}{
		{
			name: "unknown version",
			corrupt: `
				UPDATE engine.input_receipts
				   SET business_input_hash_version = 999`,
		},
		{
			name: "mismatched hash",
			corrupt: `
				UPDATE engine.input_receipts
				   SET business_input_hash = decode(repeat('ff', 32), 'hex')`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pool := postgresPool(t)
			resetDurableSchemas(t, pool)
			if err := platformpostgres.NewMigrator(
				pool,
				os.DirFS(filepath.Join("..", "..", "..", "migrations")),
			).Migrate(context.Background()); err != nil {
				t.Fatalf("Migrate: %v", err)
			}
			store := platformpostgres.NewEngineStore(pool)
			state := engine.NewState(7)
			ids := testkit.NewShardIDSequence(7)
			clock := testkit.NewManualClock(engine.NewLogicalTime(time.Date(
				2026,
				time.July,
				24,
				12,
				0,
				0,
				0,
				time.UTC,
			)))
			action := engine.TradingAction{
				Kind: engine.TradingActionConfigureAccount,
				ConfigureAccount: &engine.ConfigureAccount{
					AccountID: "account-1",
					OmsMode:   engine.OmsModeNetting,
				},
			}
			applyStoredTrading(
				t,
				pool,
				store,
				state,
				ids,
				clock,
				action,
				platformpostgres.ApplyOptions{},
			)
			if _, err := pool.Exec(context.Background(), `
				ALTER TABLE engine.input_receipts
				DISABLE TRIGGER input_receipts_are_immutable`); err != nil {
				t.Fatalf("disable receipt immutability trigger: %v", err)
			}
			if _, err := pool.Exec(context.Background(), test.corrupt); err != nil {
				t.Fatalf("corrupt business hash metadata: %v", err)
			}
			if _, err := pool.Exec(context.Background(), `
				ALTER TABLE engine.input_receipts
				ENABLE TRIGGER input_receipts_are_immutable`); err != nil {
				t.Fatalf("restore receipt immutability trigger: %v", err)
			}
			if _, err := store.RecoverTradingState(
				context.Background(),
				7,
			); !errors.Is(err, platformpostgres.ErrCheckpointMismatch) {
				t.Fatalf(
					"RecoverTradingState error = %v, want ErrCheckpointMismatch",
					err,
				)
			}
		})
	}
}

func TestEngineStoreBindsNonCommandAccountInputToOneShard(t *testing.T) {
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := platformpostgres.NewMigrator(
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	store := platformpostgres.NewEngineStore(pool)
	now := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO engine.account_shards (account_id, shard_id)
		VALUES ('account-1', 7)`); err != nil {
		t.Fatalf("preassign non-command account shard: %v", err)
	}
	firstAction := engine.TradingAction{
		Kind: engine.TradingActionConfigureAccount,
		ConfigureAccount: &engine.ConfigureAccount{
			AccountID: "account-1",
			OmsMode:   engine.OmsModeNetting,
		},
	}
	firstPayload, err := engine.EncodeTradingAction(firstAction)
	if err != nil {
		t.Fatalf("EncodeTradingAction first: %v", err)
	}
	firstInput := engine.InputEnvelope{
		InputID:              engine.IDFromSequence(engine.ID{}, 951),
		SchemaVersion:        engine.CurrentSchemaVersion,
		ShardID:              7,
		Kind:                 engine.InputKindConfiguration,
		SourceID:             "configuration",
		SourceSequence:       1,
		StreamSequence:       1,
		LogicalTime:          engine.NewLogicalTime(now),
		ConfigurationVersion: 1,
		InstrumentVersion:    1,
		Payload:              firstPayload,
	}
	firstState, _, _, err := store.ApplyTrading(
		context.Background(),
		engine.NewState(7),
		firstInput,
		firstAction,
		platformpostgres.ApplyOptions{},
	)
	if err != nil {
		t.Fatalf("first configuration input: %v", err)
	}
	if firstState.NextStreamSequence() != 2 {
		t.Fatalf(
			"first configuration next sequence = %d, want 2",
			firstState.NextStreamSequence(),
		)
	}

	conflictingAction := engine.TradingAction{
		Kind: engine.TradingActionConfigureAccount,
		ConfigureAccount: &engine.ConfigureAccount{
			AccountID: "account-1",
			OmsMode:   engine.OmsModeHedging,
		},
	}
	conflictingPayload, err := engine.EncodeTradingAction(conflictingAction)
	if err != nil {
		t.Fatalf("EncodeTradingAction conflicting: %v", err)
	}
	conflictingInput := engine.InputEnvelope{
		InputID:              engine.IDFromSequence(engine.ID{}, 952),
		SchemaVersion:        engine.CurrentSchemaVersion,
		ShardID:              8,
		Kind:                 engine.InputKindConfiguration,
		SourceID:             "configuration",
		SourceSequence:       1,
		StreamSequence:       1,
		LogicalTime:          engine.NewLogicalTime(now.Add(time.Second)),
		ConfigurationVersion: 2,
		InstrumentVersion:    1,
		Payload:              conflictingPayload,
	}
	halted, _, _, err := store.ApplyTrading(
		context.Background(),
		engine.NewState(8),
		conflictingInput,
		conflictingAction,
		platformpostgres.ApplyOptions{},
	)
	if !errors.Is(err, platformpostgres.ErrAccountShardConflict) ||
		!errors.Is(err, engine.ErrDurableInputConflict) {
		t.Fatalf(
			"cross-shard configuration error = %v, want account-shard and durable conflicts",
			err,
		)
	}
	if halted.Ready() || halted.NextStreamSequence() != 1 {
		t.Fatalf("cross-shard configuration state = %+v", halted)
	}

	var assignedShardID int64
	var omsMode string
	var accountVersion uint64
	if err := pool.QueryRow(
		context.Background(),
		"SELECT shard_id FROM engine.account_shards WHERE account_id = 'account-1'",
	).Scan(&assignedShardID); err != nil {
		t.Fatalf("read non-command shard assignment: %v", err)
	}
	if err := pool.QueryRow(context.Background(), `
		SELECT oms_mode, version
		  FROM trading.accounts
		 WHERE account_id = 'account-1'`,
	).Scan(&omsMode, &accountVersion); err != nil {
		t.Fatalf("read account after cross-shard conflict: %v", err)
	}
	if assignedShardID != 7 || omsMode != "NETTING" || accountVersion != 1 {
		t.Fatalf(
			"account authority changed: shard=%d mode=%s version=%d",
			assignedShardID,
			omsMode,
			accountVersion,
		)
	}
	assertRowCount(t, pool, "engine.input_receipts", 1)
	assertRowCount(t, pool, "engine.shard_faults", 1)
	recovered, err := store.RecoverTradingState(context.Background(), 8)
	if err != nil {
		t.Fatalf("RecoverTradingState conflicting shard: %v", err)
	}
	if recovered.Ready() || recovered.Hash() != halted.Hash() {
		t.Fatalf(
			"recovered conflicting shard = ready %t hash %s, want false %s",
			recovered.Ready(),
			recovered.Hash(),
			halted.Hash(),
		)
	}
}

func TestEngineStoreMissingAccountShardFailsClosedWithoutBinding(t *testing.T) {
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := platformpostgres.NewMigrator(
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	action := engine.TradingAction{
		Kind: engine.TradingActionLiquidateAccount,
		LiquidateAccount: &engine.LiquidateAccount{
			AccountID: "missing-account",
		},
	}
	payload, err := engine.EncodeTradingAction(action)
	if err != nil {
		t.Fatalf("EncodeTradingAction: %v", err)
	}
	input := engine.InputEnvelope{
		InputID:              engine.IDFromSequence(engine.ID{}, 955),
		SchemaVersion:        engine.CurrentSchemaVersion,
		ShardID:              8,
		Kind:                 engine.InputKindTimer,
		SourceID:             "liquidation-timer",
		SourceSequence:       1,
		StreamSequence:       1,
		LogicalTime:          engine.NewLogicalTime(time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)),
		ConfigurationVersion: 1,
		InstrumentVersion:    1,
		Payload:              payload,
	}
	store := platformpostgres.NewEngineStore(pool)
	halted, _, _, err := store.ApplyTrading(
		context.Background(),
		engine.NewState(8),
		input,
		action,
		platformpostgres.ApplyOptions{},
	)
	if !errors.Is(err, platformpostgres.ErrAccountShardConflict) ||
		!errors.Is(err, engine.ErrDurableInputConflict) {
		t.Fatalf(
			"missing mapping timer error = %v, want account-shard and durable conflicts",
			err,
		)
	}
	if halted.Ready() || halted.NextStreamSequence() != 1 {
		t.Fatalf("missing mapping timer state = %+v", halted)
	}
	for relation, query := range map[string]string{
		"account mappings": "SELECT count(*) FROM engine.account_shards",
		"accounts":         "SELECT count(*) FROM trading.accounts",
		"balances":         "SELECT count(*) FROM ledger.balances",
		"ledger":           "SELECT count(*) FROM ledger.transactions",
		"orders":           "SELECT count(*) FROM trading.orders",
		"positions":        "SELECT count(*) FROM trading.positions",
		"receipts":         "SELECT count(*) FROM engine.input_receipts",
		"outbox":           "SELECT count(*) FROM messaging.outbox",
	} {
		var count int
		if err := pool.QueryRow(context.Background(), query).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", relation, err)
		}
		if count != 0 {
			t.Fatalf("%s rows = %d, want 0", relation, count)
		}
	}
	assertRowCount(t, pool, "engine.shard_faults", 1)
	assertRowCount(t, pool, "engine.shard_checkpoints", 1)
	recovered, err := store.RecoverTradingState(context.Background(), 8)
	if err != nil {
		t.Fatalf("RecoverTradingState: %v", err)
	}
	if recovered.Ready() || recovered.Hash() != halted.Hash() {
		t.Fatalf(
			"recovered missing mapping timer = ready %t hash %s, want false %s",
			recovered.Ready(),
			recovered.Hash(),
			halted.Hash(),
		)
	}
}

func TestEngineStoreFailsClosedOnInvalidCommandShardAssignment(t *testing.T) {
	for _, test := range []struct {
		name             string
		mappingShard     *engine.ShardID
		deliveryShard    engine.ShardID
		wantMappingCount int
	}{
		{
			name:          "missing mapping",
			deliveryShard: 7,
		},
		{
			name: "mismatched mapping",
			mappingShard: func() *engine.ShardID {
				shardID := engine.ShardID(7)
				return &shardID
			}(),
			deliveryShard:    8,
			wantMappingCount: 1,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			pool := postgresPool(t)
			resetDurableSchemas(t, pool)
			if err := platformpostgres.NewMigrator(
				pool,
				os.DirFS(filepath.Join("..", "..", "..", "migrations")),
			).Migrate(context.Background()); err != nil {
				t.Fatalf("Migrate: %v", err)
			}
			now := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
			commandID := engine.IDFromSequence(engine.ID{}, 961)
			action := engine.TradingAction{
				Kind: engine.TradingActionConfigureAccount,
				ConfigureAccount: &engine.ConfigureAccount{
					AccountID: "account-1",
					OmsMode:   engine.OmsModeNetting,
				},
			}
			payload, err := engine.EncodeTradingAction(action)
			if err != nil {
				t.Fatalf("EncodeTradingAction: %v", err)
			}
			storedInput := engine.InputEnvelope{
				InputID:              commandID,
				SchemaVersion:        engine.CurrentSchemaVersion,
				ShardID:              7,
				Kind:                 engine.InputKindCommand,
				SourceID:             "command-journal",
				SourceSequence:       1,
				LogicalTime:          engine.NewLogicalTime(now),
				ConfigurationVersion: 1,
				InstrumentVersion:    1,
				Payload:              payload,
			}
			outboxPayload, err := engine.EncodeInputMessage(storedInput)
			if err != nil {
				t.Fatalf("EncodeInputMessage: %v", err)
			}
			if test.mappingShard != nil {
				if _, err := pool.Exec(context.Background(), `
					INSERT INTO engine.account_shards (account_id, shard_id)
					VALUES ('account-1', $1)`,
					int64(*test.mappingShard),
				); err != nil {
					t.Fatalf("seed mismatched account shard: %v", err)
				}
			}
			if _, err := pool.Exec(context.Background(), `
				INSERT INTO trading.commands (
					command_id, account_id, account_sequence, command_type,
					schema_version, canonical_payload, status, logical_time
				) VALUES ($1, 'account-1', 1, $2, $3, $4, 'pending', $5)`,
				commandID.String(),
				string(action.Kind),
				storedInput.SchemaVersion,
				payload.Bytes(),
				now,
			); err != nil {
				t.Fatalf("seed durable command without valid mapping: %v", err)
			}
			if _, err := pool.Exec(context.Background(), `
				INSERT INTO messaging.outbox (
					message_id, subject, schema_version, payload
				) VALUES ($1, 'engine.input.7.command.v1', $2, $3)`,
				commandID.String(),
				storedInput.SchemaVersion,
				outboxPayload,
			); err != nil {
				t.Fatalf("seed durable command outbox without valid mapping: %v", err)
			}

			delivered := storedInput
			delivered.ShardID = test.deliveryShard
			delivered.StreamSequence = 1
			halted, _, _, err := platformpostgres.NewEngineStore(pool).ApplyTrading(
				context.Background(),
				engine.NewState(test.deliveryShard),
				delivered,
				action,
				platformpostgres.ApplyOptions{},
			)
			if !errors.Is(err, platformpostgres.ErrAccountShardConflict) ||
				!errors.Is(err, engine.ErrDurableInputConflict) {
				t.Fatalf(
					"ApplyTrading error = %v, want account-shard and durable conflicts",
					err,
				)
			}
			if halted.Ready() || halted.NextStreamSequence() != 1 {
				t.Fatalf("invalid mapping state = %+v", halted)
			}

			var commandStatus string
			var subject string
			var payloadUnchanged bool
			var attempts int
			var claimUnset bool
			var publicationUnset bool
			if err := pool.QueryRow(context.Background(), `
				SELECT c.status, o.subject, o.payload = $2::jsonb, o.attempts,
				       o.claimed_at IS NULL,
				       o.published_at IS NULL AND o.publish_sequence IS NULL
				  FROM trading.commands AS c
				  JOIN messaging.outbox AS o ON o.message_id = c.command_id
				 WHERE c.command_id = $1`,
				commandID.String(),
				outboxPayload,
			).Scan(
				&commandStatus,
				&subject,
				&payloadUnchanged,
				&attempts,
				&claimUnset,
				&publicationUnset,
			); err != nil {
				t.Fatalf("read command/outbox after mapping conflict: %v", err)
			}
			if commandStatus != "pending" ||
				subject != "engine.input.7.command.v1" ||
				!payloadUnchanged ||
				attempts != 0 ||
				!claimUnset ||
				!publicationUnset {
				t.Fatalf(
					"mapping conflict changed command/outbox: status=%s subject=%s payload=%t attempts=%d claimUnset=%t publicationUnset=%t",
					commandStatus,
					subject,
					payloadUnchanged,
					attempts,
					claimUnset,
					publicationUnset,
				)
			}
			for relation, query := range map[string]string{
				"accounts":     "SELECT count(*) FROM trading.accounts",
				"balances":     "SELECT count(*) FROM ledger.balances",
				"transactions": "SELECT count(*) FROM ledger.transactions",
				"entries":      "SELECT count(*) FROM ledger.entries",
				"orders":       "SELECT count(*) FROM trading.orders",
				"fills":        "SELECT count(*) FROM trading.fills",
				"positions":    "SELECT count(*) FROM trading.positions",
				"receipts":     "SELECT count(*) FROM engine.input_receipts",
				"domain outbox": `SELECT count(*) FROM messaging.outbox
				                  WHERE subject LIKE 'domain.v1.%'`,
			} {
				var count int
				if err := pool.QueryRow(context.Background(), query).Scan(&count); err != nil {
					t.Fatalf("count %s after mapping conflict: %v", relation, err)
				}
				if count != 0 {
					t.Fatalf("%s rows after mapping conflict = %d, want 0", relation, count)
				}
			}
			var mappingCount int
			if err := pool.QueryRow(
				context.Background(),
				"SELECT count(*) FROM engine.account_shards",
			).Scan(&mappingCount); err != nil {
				t.Fatalf("count account mappings: %v", err)
			}
			if mappingCount != test.wantMappingCount {
				t.Fatalf(
					"mapping rows = %d, want %d",
					mappingCount,
					test.wantMappingCount,
				)
			}
			assertRowCount(t, pool, "engine.shard_faults", 1)
			assertRowCount(t, pool, "engine.shard_checkpoints", 1)
			recovered, err := platformpostgres.NewEngineStore(pool).
				RecoverTradingState(context.Background(), test.deliveryShard)
			if err != nil {
				t.Fatalf("RecoverTradingState: %v", err)
			}
			if recovered.Ready() || recovered.Hash() != halted.Hash() {
				t.Fatalf(
					"recovered mapping conflict = ready %t hash %s, want false %s",
					recovered.Ready(),
					recovered.Hash(),
					halted.Hash(),
				)
			}
		})
	}
}

func TestEngineStoreBindsCompleteCommandEnvelope(t *testing.T) {
	baseTime := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	action := engine.TradingAction{
		Kind: engine.TradingActionConfigureAccount,
		ConfigureAccount: &engine.ConfigureAccount{
			AccountID: "account-1",
			OmsMode:   engine.OmsModeNetting,
		},
	}
	payload, err := engine.EncodeTradingAction(action)
	if err != nil {
		t.Fatalf("EncodeTradingAction: %v", err)
	}
	baseInput := engine.InputEnvelope{
		InputID:              engine.IDFromSequence(engine.ID{}, 401),
		SchemaVersion:        engine.CurrentSchemaVersion,
		ShardID:              7,
		Kind:                 engine.InputKindCommand,
		SourceID:             "command-journal",
		SourceSequence:       1,
		StreamSequence:       1,
		MarketSequence:       2,
		LogicalTime:          engine.NewLogicalTime(baseTime),
		ConfigurationVersion: 3,
		InstrumentVersion:    4,
		Payload:              payload,
	}

	mutations := []struct {
		name      string
		mutate    func(*engine.InputEnvelope)
		wantError error
	}{
		{
			name:      "schema version",
			mutate:    func(input *engine.InputEnvelope) { input.SchemaVersion++ },
			wantError: engine.ErrUnknownSchema,
		},
		{
			name:      "shard",
			mutate:    func(input *engine.InputEnvelope) { input.ShardID++ },
			wantError: platformpostgres.ErrAccountShardConflict,
		},
		{
			name:      "kind",
			mutate:    func(input *engine.InputEnvelope) { input.Kind = engine.InputKindMarket },
			wantError: engine.ErrInvalidEnvelope,
		},
		{
			name:   "source ID",
			mutate: func(input *engine.InputEnvelope) { input.SourceID = "other-source" },
		},
		{
			name:   "source sequence",
			mutate: func(input *engine.InputEnvelope) { input.SourceSequence++ },
		},
		{
			name:   "market sequence",
			mutate: func(input *engine.InputEnvelope) { input.MarketSequence++ },
		},
		{
			name:   "logical time",
			mutate: func(input *engine.InputEnvelope) { input.LogicalTime++ },
		},
		{
			name: "configuration version",
			mutate: func(input *engine.InputEnvelope) {
				input.ConfigurationVersion++
			},
		},
		{
			name:   "instrument version",
			mutate: func(input *engine.InputEnvelope) { input.InstrumentVersion++ },
		},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			pool := postgresPool(t)
			resetDurableSchemas(t, pool)
			if err := platformpostgres.NewMigrator(
				pool,
				os.DirFS(filepath.Join("..", "..", "..", "migrations")),
			).Migrate(context.Background()); err != nil {
				t.Fatalf("Migrate: %v", err)
			}
			outboxPayload, err := platformnats.EncodeEngineInputMessage(baseInput)
			if err != nil {
				t.Fatalf("EncodeEngineInputMessage: %v", err)
			}
			if _, err := platformpostgres.NewCommandJournal(pool).Begin(
				context.Background(),
				platformpostgres.BeginCommandRequest{
					Scope:            "account:account-1",
					IdempotencyKey:   "configure-account",
					RequestHash:      [32]byte{1},
					CommandID:        baseInput.InputID,
					AccountID:        "account-1",
					AccountSequence:  1,
					CommandType:      string(action.Kind),
					SchemaVersion:    baseInput.SchemaVersion,
					CanonicalPayload: payload.Bytes(),
					OutboxSubject:    "engine.input.7.command.v1",
					OutboxPayload:    outboxPayload,
					LogicalTime:      baseTime,
					ExpiresAt:        baseTime.Add(24 * time.Hour),
				},
			); err != nil {
				t.Fatalf("Begin: %v", err)
			}

			delivered := baseInput
			mutation.mutate(&delivered)
			next, _, _, err := platformpostgres.NewEngineStore(pool).ApplyTrading(
				context.Background(),
				engine.NewState(delivered.ShardID),
				delivered,
				action,
				platformpostgres.ApplyOptions{},
			)
			wantError := mutation.wantError
			if wantError == nil {
				wantError = platformpostgres.ErrCommandInputConflict
			}
			if !errors.Is(err, wantError) {
				t.Fatalf("ApplyTrading error = %v, want %v", err, wantError)
			}
			if next.Ready() {
				t.Fatal("invalid command input did not halt the shard")
			}
			if next.NextStreamSequence() != 1 {
				t.Fatalf(
					"failed command advanced state to sequence %d",
					next.NextStreamSequence(),
				)
			}
			assertRowCount(t, pool, "trading.accounts", 0)
			assertRowCount(t, pool, "engine.input_receipts", 0)
		})
	}
}

func TestEngineStoreRejectsAccountCommandBeforePredecessorCommits(t *testing.T) {
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := platformpostgres.NewMigrator(
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	logicalTime := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	actions := []engine.TradingAction{
		{
			Kind: engine.TradingActionConfigureAccount,
			ConfigureAccount: &engine.ConfigureAccount{
				AccountID: "account-1",
				OmsMode:   engine.OmsModeNetting,
			},
		},
		{
			Kind: engine.TradingActionConfigureAccount,
			ConfigureAccount: &engine.ConfigureAccount{
				AccountID: "account-1",
				OmsMode:   engine.OmsModeHedging,
			},
		},
	}
	inputs := make([]engine.InputEnvelope, len(actions))
	journal := platformpostgres.NewCommandJournal(pool)
	for index, action := range actions {
		payload, err := engine.EncodeTradingAction(action)
		if err != nil {
			t.Fatalf("EncodeTradingAction %d: %v", index+1, err)
		}
		inputs[index] = engine.InputEnvelope{
			InputID:              engine.IDFromSequence(engine.ID{}, uint64(501+index)),
			SchemaVersion:        engine.CurrentSchemaVersion,
			ShardID:              7,
			Kind:                 engine.InputKindCommand,
			SourceID:             "command-journal",
			SourceSequence:       uint64(index + 1),
			MarketSequence:       uint64(index + 1),
			LogicalTime:          engine.NewLogicalTime(logicalTime.Add(time.Duration(index) * time.Second)),
			ConfigurationVersion: 1,
			InstrumentVersion:    1,
			Payload:              payload,
		}
		outboxPayload, err := engine.EncodeInputMessage(inputs[index])
		if err != nil {
			t.Fatalf("EncodeInputMessage %d: %v", index+1, err)
		}
		if _, err := journal.Begin(
			context.Background(),
			platformpostgres.BeginCommandRequest{
				Scope:            "account:account-1",
				IdempotencyKey:   fmt.Sprintf("configure-account-%d", index+1),
				RequestHash:      [32]byte{byte(index + 1)},
				CommandID:        inputs[index].InputID,
				AccountID:        "account-1",
				AccountSequence:  uint64(index + 1),
				CommandType:      string(action.Kind),
				SchemaVersion:    inputs[index].SchemaVersion,
				CanonicalPayload: payload.Bytes(),
				OutboxSubject:    "engine.input.7.command.v1",
				OutboxPayload:    outboxPayload,
				LogicalTime:      logicalTime.Add(time.Duration(index) * time.Second),
				ExpiresAt:        logicalTime.Add(24 * time.Hour),
			},
		); err != nil {
			t.Fatalf("Begin command %d: %v", index+1, err)
		}
	}

	store := platformpostgres.NewEngineStore(pool)
	secondFirst := inputs[1]
	secondFirst.StreamSequence = 1
	state := engine.NewState(7)
	next, _, _, err := store.ApplyTrading(
		context.Background(),
		state,
		secondFirst,
		actions[1],
		platformpostgres.ApplyOptions{},
	)
	if !errors.Is(err, platformpostgres.ErrCommandPredecessorPending) {
		t.Fatalf("early second command error = %v, want ErrCommandPredecessorPending", err)
	}
	if next.Ready() || next.Hash() == state.Hash() {
		t.Fatal("early second command did not durably halt state")
	}
	assertRowCount(t, pool, "engine.input_receipts", 0)
	assertRowCount(t, pool, "engine.shard_faults", 1)
	assertRowCount(t, pool, "engine.shard_checkpoints", 1)
	recovered, err := store.RecoverTradingState(context.Background(), 7)
	if err != nil {
		t.Fatalf("RecoverTradingState: %v", err)
	}
	if recovered.Ready() || recovered.Hash() != next.Hash() {
		t.Fatalf("recovered predecessor state = %+v, want halted hash %s", recovered, next.Hash())
	}
}

func TestEngineStoreBindsRedundantCommandMetadata(t *testing.T) {
	now := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		mutate func(context.Context, *pgxpool.Pool, engine.ID) error
	}{
		{
			name: "command type",
			mutate: func(ctx context.Context, pool *pgxpool.Pool, id engine.ID) error {
				_, err := pool.Exec(ctx, `
					UPDATE trading.commands
					   SET command_type = 'submit_order'
					 WHERE command_id = $1`,
					id.String(),
				)
				return err
			},
		},
		{
			name: "logical time",
			mutate: func(ctx context.Context, pool *pgxpool.Pool, id engine.ID) error {
				_, err := pool.Exec(ctx, `
					UPDATE trading.commands
					   SET logical_time = logical_time + interval '1 second'
					 WHERE command_id = $1`,
					id.String(),
				)
				return err
			},
		},
		{
			name: "outbox schema",
			mutate: func(ctx context.Context, pool *pgxpool.Pool, id engine.ID) error {
				_, err := pool.Exec(ctx, `
					UPDATE messaging.outbox
					   SET schema_version = schema_version + 1
					 WHERE message_id = $1`,
					id.String(),
				)
				return err
			},
		},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			pool := postgresPool(t)
			resetDurableSchemas(t, pool)
			if err := platformpostgres.NewMigrator(
				pool,
				os.DirFS(filepath.Join("..", "..", "..", "migrations")),
			).Migrate(ctx); err != nil {
				t.Fatalf("Migrate: %v", err)
			}
			commandID := engine.IDFromSequence(engine.ID{}, uint64(801+index))
			request := validCommandRequest(
				t,
				commandID,
				"account-1",
				1,
				7,
				now,
			)
			if _, err := platformpostgres.NewCommandJournal(pool).Begin(
				ctx,
				request,
			); err != nil {
				t.Fatalf("Begin: %v", err)
			}
			if err := test.mutate(ctx, pool, commandID); err != nil {
				t.Fatalf("mutate durable metadata: %v", err)
			}
			input, action, err := engine.DecodeInputMessage(request.OutboxPayload)
			if err != nil {
				t.Fatalf("DecodeInputMessage: %v", err)
			}
			input.StreamSequence = 1
			next, _, _, err := platformpostgres.NewEngineStore(pool).ApplyTrading(
				ctx,
				engine.NewState(7),
				input,
				action,
				platformpostgres.ApplyOptions{},
			)
			if !errors.Is(err, platformpostgres.ErrCommandInputConflict) {
				t.Fatalf("ApplyTrading error = %v, want ErrCommandInputConflict", err)
			}
			if next.Ready() {
				t.Fatal("durable metadata conflict did not halt shard")
			}
			assertRowCount(t, pool, "ledger.transactions", 0)
			assertRowCount(t, pool, "ledger.entries", 0)
			assertRowCount(t, pool, "engine.input_receipts", 0)
			assertRowCount(t, pool, "engine.shard_faults", 1)
		})
	}
}

func TestEngineStoreDurablyHaltsNonCommandReuseOfCommandID(t *testing.T) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := platformpostgres.NewMigrator(
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	now := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	commandID := engine.IDFromSequence(engine.ID{}, 851)
	request := validCommandRequest(t, commandID, "account-1", 1, 7, now)
	if _, err := platformpostgres.NewCommandJournal(pool).Begin(
		ctx,
		request,
	); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	action := engine.TradingAction{
		Kind: engine.TradingActionConfigureAccount,
		ConfigureAccount: &engine.ConfigureAccount{
			AccountID: "account-1",
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
		ShardID:              7,
		Kind:                 engine.InputKindConfiguration,
		SourceID:             "configuration",
		SourceSequence:       1,
		StreamSequence:       1,
		LogicalTime:          engine.NewLogicalTime(now),
		ConfigurationVersion: 1,
		InstrumentVersion:    1,
		Payload:              payload,
	}
	state := engine.NewState(7)
	next, _, _, err := platformpostgres.NewEngineStore(pool).ApplyTrading(
		ctx,
		state,
		input,
		action,
		platformpostgres.ApplyOptions{},
	)
	if !errors.Is(err, platformpostgres.ErrCommandInputConflict) ||
		!errors.Is(err, engine.ErrDurableInputConflict) {
		t.Fatalf(
			"ApplyTrading error = %v, want command and durable input conflicts",
			err,
		)
	}
	if next.Ready() || next.Hash() == state.Hash() {
		t.Fatal("non-command command-ID collision did not halt shard")
	}
	for _, relation := range []string{
		"trading.accounts",
		"ledger.transactions",
		"ledger.entries",
		"engine.input_receipts",
		"messaging.domain_outbox",
	} {
		assertRowCount(t, pool, relation, 0)
	}
	assertRowCount(t, pool, "engine.shard_faults", 1)
	assertRowCount(t, pool, "engine.shard_checkpoints", 1)
	recovered, err := platformpostgres.NewEngineStore(pool).RecoverTradingState(ctx, 7)
	if err != nil {
		t.Fatalf("RecoverTradingState: %v", err)
	}
	if recovered.Ready() || recovered.Hash() != next.Hash() {
		t.Fatalf("recovered state = %+v, want halted hash %s", recovered, next.Hash())
	}
}

func applyStoredTrading(
	t *testing.T,
	pool *pgxpool.Pool,
	store *platformpostgres.EngineStore,
	state engine.State,
	ids *testkit.IDSequence,
	clock *testkit.ManualClock,
	action engine.TradingAction,
	options platformpostgres.ApplyOptions,
) (engine.State, engine.Decision, engine.InputEnvelope, bool) {
	t.Helper()
	input := nextStoredInput(t, state, ids, clock, action)
	return applyStoredInput(t, pool, store, state, input, action, options)
}

func nextStoredInput(
	t *testing.T,
	state engine.State,
	ids *testkit.IDSequence,
	clock *testkit.ManualClock,
	action engine.TradingAction,
) engine.InputEnvelope {
	t.Helper()
	sequence := state.NextStreamSequence()
	input, err := (testkit.TradingInput{
		InputID:              ids.Next(),
		ShardID:              state.ShardID(),
		SourceID:             "postgres-integration",
		SourceSequence:       sequence,
		StreamSequence:       sequence,
		MarketSequence:       sequence,
		LogicalTime:          clock.Now(),
		ConfigurationVersion: 1,
		InstrumentVersion:    1,
		Action:               action,
	}).CanonicalEnvelope()
	if err != nil {
		t.Fatalf("CanonicalEnvelope: %v", err)
	}
	switch action.Kind {
	case engine.TradingActionUpdateBook:
		input.Kind = engine.InputKindMarket
	case engine.TradingActionSettleFunding,
		engine.TradingActionLiquidateAccount:
		input.Kind = engine.InputKindTimer
	}
	clock.Advance(time.Second)
	return input
}

func applyStoredInput(
	t *testing.T,
	pool *pgxpool.Pool,
	store *platformpostgres.EngineStore,
	state engine.State,
	input engine.InputEnvelope,
	action engine.TradingAction,
	options platformpostgres.ApplyOptions,
) (engine.State, engine.Decision, engine.InputEnvelope, bool) {
	t.Helper()
	seedPendingCommand(t, pool, input, action)
	next, decision, duplicate, err := store.ApplyTrading(
		context.Background(),
		state,
		input,
		action,
		options,
	)
	if err != nil {
		t.Fatalf("ApplyTrading sequence %d: %v", input.StreamSequence, err)
	}
	return next, decision, input, duplicate
}

func seedPendingCommand(
	t *testing.T,
	pool *pgxpool.Pool,
	input engine.InputEnvelope,
	action engine.TradingAction,
) {
	t.Helper()
	if input.Kind != engine.InputKindCommand {
		return
	}
	accountID := fmt.Sprintf("test-shard-%d", input.ShardID)
	if actionAccountID, scoped := engine.TradingActionAccountID(action); scoped {
		accountID = actionAccountID
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO engine.account_shards (account_id, shard_id)
		VALUES ($1, $2)
		ON CONFLICT (account_id) DO NOTHING`,
		accountID,
		int64(input.ShardID),
	); err != nil {
		t.Fatalf("seed command %s account shard: %v", input.InputID, err)
	}
	outboxPayload, err := engine.EncodeInputMessage(input)
	if err != nil {
		t.Fatalf("encode pending command %s outbox: %v", input.InputID, err)
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO trading.commands (
			command_id, account_id, account_sequence, command_type,
			schema_version, canonical_payload, status, logical_time
		) VALUES ($1,$2,$3,$4,$5,$6,'pending',$7)
		ON CONFLICT (command_id) DO NOTHING`,
		input.InputID.String(),
		accountID,
		input.SourceSequence,
		string(action.Kind),
		input.SchemaVersion,
		input.Payload.Bytes(),
		time.Unix(0, input.LogicalTime.UnixNano()).UTC(),
	); err != nil {
		t.Fatalf("seed pending command %s: %v", input.InputID, err)
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO messaging.outbox (
			message_id, subject, schema_version, payload
		) VALUES ($1,$2,$3,$4)
		ON CONFLICT (message_id) DO NOTHING`,
		input.InputID.String(),
		fmt.Sprintf(
			"engine.input.%d.command.v%d",
			input.ShardID,
			input.SchemaVersion,
		),
		input.SchemaVersion,
		outboxPayload,
	); err != nil {
		t.Fatalf("seed pending command %s outbox: %v", input.InputID, err)
	}
}

func assertRowCount(t *testing.T, pool queryRower, relation string, want int) {
	t.Helper()
	query := ""
	switch relation {
	case "engine.input_receipts":
		query = "SELECT count(*) FROM engine.input_receipts"
	case "engine.shard_checkpoints":
		query = "SELECT count(*) FROM engine.shard_checkpoints"
	case "engine.shard_faults":
		query = "SELECT count(*) FROM engine.shard_faults"
	case "engine.duplicate_delivery_receipts":
		query = "SELECT count(*) FROM engine.duplicate_delivery_receipts"
	case "ledger.transactions":
		query = "SELECT count(*) FROM ledger.transactions"
	case "ledger.entries":
		query = "SELECT count(*) FROM ledger.entries"
	case "trading.orders":
		query = "SELECT count(*) FROM trading.orders"
	case "trading.fills":
		query = "SELECT count(*) FROM trading.fills"
	case "trading.positions":
		query = "SELECT count(*) FROM trading.positions"
	case "trading.accounts":
		query = "SELECT count(*) FROM trading.accounts"
	case "messaging.domain_outbox":
		query = "SELECT count(*) FROM messaging.outbox WHERE subject LIKE 'domain.v1.%'"
	default:
		t.Fatalf("unsupported row-count relation %q", relation)
	}
	var got int
	if err := pool.QueryRow(
		context.Background(),
		query,
	).Scan(&got); err != nil {
		t.Fatalf("count %s: %v", relation, err)
	}
	if got != want {
		t.Fatalf("%s row count = %d, want %d", relation, got, want)
	}
}

type queryRower interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}
