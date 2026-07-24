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
	assertRowCount(t, pool, "messaging.outbox", len(orderDecision.Events))
	var eventSubject string
	var envelopeMessageID string
	if err := pool.QueryRow(context.Background(), `
		SELECT subject, payload ->> 'messageId'
		  FROM messaging.outbox
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
	assertRowCount(t, pool, "messaging.outbox", len(orderDecision.Events))

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
			OutboxPayload:    []byte(`{"kind":"mismatched"}`),
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
	if next.Hash() != state.Hash() || next.NextStreamSequence() != 1 {
		t.Fatalf("failed command advanced state to %+v", next)
	}
	assertRowCount(t, pool, "trading.accounts", 0)
	assertRowCount(t, pool, "engine.input_receipts", 0)
	assertRowCount(t, pool, "engine.shard_checkpoints", 0)

	if _, err := pool.Exec(context.Background(), `
		UPDATE trading.commands
		   SET status = 'rejected',
		       result = '{"Status":"rejected","Reason":"invalid_order"}',
		       completed_at = clock_timestamp()
		 WHERE command_id = $1`,
		commandID.String(),
	); err != nil {
		t.Fatalf("preterminalize command: %v", err)
	}
	input.Payload = storedPayload
	next, _, _, err = platformpostgres.NewEngineStore(pool).ApplyTrading(
		context.Background(),
		state,
		input,
		storedAction,
		platformpostgres.ApplyOptions{},
	)
	if !errors.Is(err, platformpostgres.ErrCommandCompletionConflict) {
		t.Fatalf(
			"preterminalized ApplyTrading error = %v, want ErrCommandCompletionConflict",
			err,
		)
	}
	if next.Hash() != state.Hash() || next.NextStreamSequence() != 1 {
		t.Fatalf("preterminalized command advanced state to %+v", next)
	}
	assertRowCount(t, pool, "trading.accounts", 0)
	assertRowCount(t, pool, "engine.input_receipts", 0)
	assertRowCount(t, pool, "engine.shard_checkpoints", 0)
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
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO trading.commands (
			command_id, account_id, account_sequence, command_type,
			schema_version, canonical_payload, status, logical_time
		) VALUES ($1,$2,$3,$4,$5,$6,'pending',$7)
		ON CONFLICT (command_id) DO NOTHING`,
		input.InputID.String(),
		fmt.Sprintf("test-shard-%d", input.ShardID),
		input.SourceSequence,
		string(action.Kind),
		input.SchemaVersion,
		input.Payload.Bytes(),
		time.Unix(0, input.LogicalTime.UnixNano()).UTC(),
	); err != nil {
		t.Fatalf("seed pending command %s: %v", input.InputID, err)
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
	case "messaging.outbox":
		query = "SELECT count(*) FROM messaging.outbox"
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
