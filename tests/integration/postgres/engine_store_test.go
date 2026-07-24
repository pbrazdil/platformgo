package postgres_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
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
		t, store, state, ids, clock, configureInstrument, platformpostgres.ApplyOptions{},
	)

	configureAccount := engine.TradingAction{
		Kind: engine.TradingActionConfigureAccount,
		ConfigureAccount: &engine.ConfigureAccount{
			AccountID: "account-1",
			OmsMode:   engine.OmsModeNetting,
		},
	}
	state, _, _, _ = applyStoredTrading(
		t, store, state, ids, clock, configureAccount, platformpostgres.ApplyOptions{},
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
		t, store, state, ids, clock, deposit, platformpostgres.ApplyOptions{},
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
		t, store, state, ids, clock, book, platformpostgres.ApplyOptions{},
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
}

func applyStoredTrading(
	t *testing.T,
	store *platformpostgres.EngineStore,
	state engine.State,
	ids *testkit.IDSequence,
	clock *testkit.ManualClock,
	action engine.TradingAction,
	options platformpostgres.ApplyOptions,
) (engine.State, engine.Decision, engine.InputEnvelope, bool) {
	t.Helper()
	input := nextStoredInput(t, state, ids, clock, action)
	return applyStoredInput(t, store, state, input, action, options)
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
	store *platformpostgres.EngineStore,
	state engine.State,
	input engine.InputEnvelope,
	action engine.TradingAction,
	options platformpostgres.ApplyOptions,
) (engine.State, engine.Decision, engine.InputEnvelope, bool) {
	t.Helper()
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

func assertRowCount(t *testing.T, pool queryRower, relation string, want int) {
	t.Helper()
	query := ""
	switch relation {
	case "engine.input_receipts":
		query = "SELECT count(*) FROM engine.input_receipts"
	case "engine.shard_faults":
		query = "SELECT count(*) FROM engine.shard_faults"
	case "ledger.transactions":
		query = "SELECT count(*) FROM ledger.transactions"
	case "ledger.entries":
		query = "SELECT count(*) FROM ledger.entries"
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
