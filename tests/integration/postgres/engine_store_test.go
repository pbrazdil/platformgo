package postgres_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
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
	if err := migrator.MigrateAndProvision(context.Background(), 7); err != nil {
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
	tamperedReport, err := store.ReconcileShard(
		context.Background(),
		7,
	)
	if !errors.Is(err, platformpostgres.ErrReconciliationMismatch) {
		t.Fatalf(
			"tampered reconciliation error = %v, want ErrReconciliationMismatch",
			err,
		)
	}
	if tamperedReport.Ready {
		t.Fatal("tampered balance reconciliation left shard ready")
	}
	recoveredMismatch, err := store.RecoverTradingState(context.Background(), 7)
	if err != nil {
		t.Fatalf("RecoverTradingState after reconciliation mismatch: %v", err)
	}
	if recoveredMismatch.Ready() {
		t.Fatal("reconciliation mismatch became ready after recovery")
	}
}

func TestEngineStorePersistsOrdersFillsPositionsAndEvents(t *testing.T) {
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := platformpostgres.NewMigrator(
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).MigrateAndProvision(context.Background(), 8); err != nil {
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

func TestReconcileShardFailsClosedOnTradingProjectionCorruption(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(*testing.T, *pgxpool.Pool, reconciliationFixture)
		reportKind func(platformpostgres.ReconciliationReport) uint64
	}{
		{
			name: "order fill sum",
			mutate: func(t *testing.T, pool *pgxpool.Pool, fixture reconciliationFixture) {
				_, err := pool.Exec(context.Background(), `
					UPDATE trading.orders
					   SET filled_quantity = 0, average_fill_price = 0
					 WHERE order_id = $1`,
					fixture.orderID.String(),
				)
				if err != nil {
					t.Fatalf("corrupt order fill sum: %v", err)
				}
			},
			reportKind: func(report platformpostgres.ReconciliationReport) uint64 {
				return report.OrderFillMismatchCount
			},
		},
		{
			name: "terminal order status",
			mutate: func(t *testing.T, pool *pgxpool.Pool, fixture reconciliationFixture) {
				_, err := pool.Exec(context.Background(), `
					UPDATE trading.orders SET status = 'OPEN' WHERE order_id = $1`,
					fixture.orderID.String(),
				)
				if err != nil {
					t.Fatalf("corrupt terminal order status: %v", err)
				}
			},
			reportKind: func(report platformpostgres.ReconciliationReport) uint64 {
				return report.OrderFillMismatchCount
			},
		},
		{
			name: "order side",
			mutate: func(t *testing.T, pool *pgxpool.Pool, fixture reconciliationFixture) {
				_, err := pool.Exec(context.Background(), `
					UPDATE trading.orders SET side = 'SELL' WHERE order_id = $1`,
					fixture.orderID.String(),
				)
				if err != nil {
					t.Fatalf("corrupt order side: %v", err)
				}
			},
			reportKind: func(report platformpostgres.ReconciliationReport) uint64 {
				return report.OrderFillMismatchCount
			},
		},
		{
			name: "position fold",
			mutate: func(t *testing.T, pool *pgxpool.Pool, _ reconciliationFixture) {
				_, err := pool.Exec(context.Background(), `
					UPDATE trading.positions
					   SET signed_quantity = signed_quantity + 1,
					       version = version + 1
					 WHERE account_id = 'account-1'
					   AND instrument_id = 'BTC-PERP'`,
				)
				if err != nil {
					t.Fatalf("corrupt position fold: %v", err)
				}
			},
			reportKind: func(report platformpostgres.ReconciliationReport) uint64 {
				return report.PositionMismatchCount
			},
		},
		{
			name: "position average entry",
			mutate: func(t *testing.T, pool *pgxpool.Pool, _ reconciliationFixture) {
				_, err := pool.Exec(context.Background(), `
					UPDATE trading.positions
					   SET average_open_price = average_open_price + 1,
					       version = version + 1
					 WHERE account_id = 'account-1'
					   AND instrument_id = 'BTC-PERP'`,
				)
				if err != nil {
					t.Fatalf("corrupt position average entry: %v", err)
				}
			},
			reportKind: func(report platformpostgres.ReconciliationReport) uint64 {
				return report.PositionMismatchCount
			},
		},
		{
			name: "command result",
			mutate: func(t *testing.T, pool *pgxpool.Pool, fixture reconciliationFixture) {
				_, err := pool.Exec(context.Background(), `
					UPDATE trading.commands SET result = NULL WHERE command_id = $1`,
					fixture.orderInputID.String(),
				)
				if err != nil {
					t.Fatalf("corrupt command result: %v", err)
				}
			},
			reportKind: func(report platformpostgres.ReconciliationReport) uint64 {
				return report.CommandMismatchCount
			},
		},
		{
			name: "command canonical payload",
			mutate: func(t *testing.T, pool *pgxpool.Pool, fixture reconciliationFixture) {
				corruptAsReplicationAuthority(t, pool, `
					UPDATE trading.commands
					   SET canonical_payload = '{}'::jsonb
					 WHERE command_id = $1`,
					fixture.orderInputID.String(),
				)
			},
			reportKind: func(report platformpostgres.ReconciliationReport) uint64 {
				return report.CommandMismatchCount
			},
		},
		{
			name: "command outbox producer authority",
			mutate: func(t *testing.T, pool *pgxpool.Pool, fixture reconciliationFixture) {
				if _, err := pool.Exec(context.Background(), `
					ALTER TABLE messaging.outbox
						DROP CONSTRAINT outbox_producer_authority_check`,
				); err != nil {
					t.Fatalf("remove producer authority constraint for corruption: %v", err)
				}
				_, err := pool.Exec(context.Background(), `
					UPDATE messaging.outbox
					   SET producer_class = 'engine'
					 WHERE message_id = $1`,
					fixture.orderInputID.String(),
				)
				if err != nil {
					t.Fatalf("corrupt command outbox producer authority: %v", err)
				}
			},
			reportKind: func(report platformpostgres.ReconciliationReport) uint64 {
				return report.CommandMismatchCount
			},
		},
		{
			name: "balance reservation",
			mutate: func(t *testing.T, pool *pgxpool.Pool, _ reconciliationFixture) {
				_, err := pool.Exec(context.Background(), `
					UPDATE ledger.balances
					   SET used = used + 1,
					       free = free - 1,
					       ledger_sequence = ledger_sequence + 1
					 WHERE account_id = 'account-1'
					   AND currency = 'USDC'`,
				)
				if err != nil {
					t.Fatalf("corrupt balance reservation: %v", err)
				}
			},
			reportKind: func(report platformpostgres.ReconciliationReport) uint64 {
				return report.LedgerMismatchCount
			},
		},
		{
			name: "risk leverage",
			mutate: func(t *testing.T, pool *pgxpool.Pool, _ reconciliationFixture) {
				_, err := pool.Exec(context.Background(), `
					UPDATE trading.risk_configs
					   SET leverage = leverage + 1,
					       version = version + 1
					 WHERE account_id = 'account-1'
					   AND instrument_id = 'BTC-PERP'`,
				)
				if err != nil {
					t.Fatalf("corrupt risk leverage: %v", err)
				}
			},
			reportKind: func(report platformpostgres.ReconciliationReport) uint64 {
				return report.ConfigurationMismatchCount
			},
		},
		{
			name: "market book",
			mutate: func(t *testing.T, pool *pgxpool.Pool, _ reconciliationFixture) {
				_, err := pool.Exec(context.Background(), `
					UPDATE market.books
					   SET mark_price = mark_price + 1,
					       stream_sequence = stream_sequence + 1
					 WHERE instrument_id = 'BTC-PERP'`,
				)
				if err != nil {
					t.Fatalf("corrupt market book: %v", err)
				}
			},
			reportKind: func(report platformpostgres.ReconciliationReport) uint64 {
				return report.MarketMismatchCount
			},
		},
		{
			name: "funding amount",
			mutate: func(t *testing.T, pool *pgxpool.Pool, _ reconciliationFixture) {
				_, err := pool.Exec(context.Background(), `
					ALTER TABLE trading.funding_settlements
						DISABLE TRIGGER funding_settlements_are_immutable;
					UPDATE trading.funding_settlements
					   SET amount = amount + 1
					 WHERE account_id = 'account-1'
					   AND instrument_id = 'BTC-PERP';
					ALTER TABLE trading.funding_settlements
						ENABLE TRIGGER funding_settlements_are_immutable`,
				)
				if err != nil {
					t.Fatalf("corrupt funding amount: %v", err)
				}
			},
			reportKind: func(report platformpostgres.ReconciliationReport) uint64 {
				return report.FundingMismatchCount
			},
		},
		{
			name: "ledger business identity",
			mutate: func(t *testing.T, pool *pgxpool.Pool, _ reconciliationFixture) {
				_, err := pool.Exec(context.Background(), `
					ALTER TABLE ledger.transactions
						DISABLE TRIGGER ledger_transactions_are_immutable;
					UPDATE ledger.transactions
					   SET business_key = business_key || ':corrupt'
					 WHERE transaction_id = (
						SELECT transaction_id
						  FROM ledger.transactions
						 ORDER BY transaction_id
						 LIMIT 1
					 );
					ALTER TABLE ledger.transactions
						ENABLE TRIGGER ledger_transactions_are_immutable`,
				)
				if err != nil {
					t.Fatalf("corrupt ledger business identity: %v", err)
				}
			},
			reportKind: func(report platformpostgres.ReconciliationReport) uint64 {
				return report.LedgerMismatchCount
			},
		},
		{
			name: "unmapped ledger and balance",
			mutate: func(t *testing.T, pool *pgxpool.Pool, _ reconciliationFixture) {
				_, err := pool.Exec(context.Background(), `
					INSERT INTO ledger.transactions (
						transaction_id, business_key, input_id, logical_time
					) VALUES (
						'019f9460-4b36-4e9b-8f44-682611f78901',
						'orphan-ledger',
						'019f9460-4b36-4e9b-8f44-682611f78902',
						1784894400000000000
					);
					INSERT INTO ledger.entries (
						entry_id, transaction_id, account_id, currency, amount
					) VALUES
						(
							'019f9460-4b36-4e9b-8f44-682611f78903',
							'019f9460-4b36-4e9b-8f44-682611f78901',
							'orphan-account', 'USDC', 10
						),
						(
							'019f9460-4b36-4e9b-8f44-682611f78904',
							'019f9460-4b36-4e9b-8f44-682611f78901',
							'system:clearing', 'USDC', -10
						);
					INSERT INTO ledger.balances (
						account_id, currency, total, used, free, equity,
						ledger_sequence
					) VALUES ('orphan-account', 'USDC', 10, 0, 10, 10, 99)`)
				if err != nil {
					t.Fatalf("insert unmapped ledger and balance: %v", err)
				}
			},
			reportKind: func(report platformpostgres.ReconciliationReport) uint64 {
				return report.LedgerMismatchCount
			},
		},
		{
			name: "domain event payload",
			mutate: func(t *testing.T, pool *pgxpool.Pool, _ reconciliationFixture) {
				_, err := pool.Exec(context.Background(), `
					UPDATE messaging.outbox
					   SET payload = jsonb_set(payload, '{kind}', '"corrupt"')
					 WHERE subject LIKE 'domain.v1.%'
					   AND message_id = (
						SELECT message_id
						  FROM messaging.outbox
						 WHERE subject LIKE 'domain.v1.%'
						 ORDER BY message_id
						 LIMIT 1
					   )`,
				)
				if err != nil {
					t.Fatalf("corrupt domain event payload: %v", err)
				}
			},
			reportKind: func(report platformpostgres.ReconciliationReport) uint64 {
				return report.MessagingMismatchCount
			},
		},
		{
			name: "domain event receipt binding",
			mutate: func(t *testing.T, pool *pgxpool.Pool, _ reconciliationFixture) {
				_, err := pool.Exec(context.Background(), `
					UPDATE messaging.outbox AS outbox
					   SET engine_input_id = (
							SELECT receipt.input_id
							  FROM engine.input_receipts AS receipt
							 WHERE receipt.shard_id = outbox.engine_shard_id
							   AND receipt.input_id <> outbox.engine_input_id
							 ORDER BY receipt.stream_sequence
							 LIMIT 1
					   )
					 WHERE outbox.subject LIKE 'domain.v1.%'
					   AND outbox.message_id = (
							SELECT message_id
							  FROM messaging.outbox
							 WHERE subject LIKE 'domain.v1.%'
							 ORDER BY message_id
							 LIMIT 1
					   )`)
				if err != nil {
					t.Fatalf("rebind domain event receipt: %v", err)
				}
			},
			reportKind: func(report platformpostgres.ReconciliationReport) uint64 {
				return report.MessagingMismatchCount
			},
		},
		{
			name: "unexpected engine domain event",
			mutate: func(t *testing.T, pool *pgxpool.Pool, _ reconciliationFixture) {
				messageID := engine.IDFromSequence(engine.ID{}, 8990)
				_, err := pool.Exec(context.Background(), `
					INSERT INTO messaging.outbox (
						message_id, subject, schema_version, payload,
						producer_class, engine_shard_id, engine_input_id
					)
					SELECT
						$1, 'domain.v1.order.filled', 1,
						jsonb_build_object(
							'messageId', $1::uuid::text,
							'correlationId', receipt.input_id::text
						),
						'engine', receipt.shard_id, receipt.input_id
					  FROM engine.input_receipts AS receipt
					 WHERE receipt.shard_id = 8
					 ORDER BY receipt.stream_sequence
					 LIMIT 1`,
					messageID.String(),
				)
				if err != nil {
					t.Fatalf("insert unexpected engine domain event: %v", err)
				}
			},
			reportKind: func(report platformpostgres.ReconciliationReport) uint64 {
				return report.MessagingMismatchCount
			},
		},
		{
			name: "unbound engine domain event",
			mutate: func(t *testing.T, pool *pgxpool.Pool, _ reconciliationFixture) {
				if _, err := pool.Exec(context.Background(), `
					ALTER TABLE messaging.outbox
						DROP CONSTRAINT outbox_engine_receipt_fkey`,
				); err != nil {
					t.Fatalf("remove engine receipt constraint for corruption: %v", err)
				}
				messageID := engine.IDFromSequence(engine.ID{}, 8996)
				unknownInputID := engine.IDFromSequence(engine.ID{}, 8997)
				_, err := pool.Exec(context.Background(), `
					INSERT INTO messaging.outbox (
						message_id, subject, schema_version, payload,
						producer_class, engine_shard_id, engine_input_id
					) VALUES (
						$1, 'domain.v1.order.filled', 1,
						jsonb_build_object(
							'messageId', $1::uuid::text,
							'correlationId', $2::uuid::text
						),
						'engine', 8, $2
					)`,
					messageID.String(),
					unknownInputID.String(),
				)
				if err != nil {
					t.Fatalf("insert unbound engine domain event: %v", err)
				}
			},
			reportKind: func(report platformpostgres.ReconciliationReport) uint64 {
				return report.MessagingMismatchCount
			},
		},
		{
			name: "unexplained fill",
			mutate: func(t *testing.T, pool *pgxpool.Pool, fixture reconciliationFixture) {
				fillID := engine.IDFromSequence(engine.ID{}, 8991)
				inputID := engine.IDFromSequence(engine.ID{}, 8992)
				_, err := pool.Exec(context.Background(), `
					INSERT INTO trading.fills (
						fill_id, order_id, input_id, account_id, instrument_id,
						side, price, quantity, position_id, position_effect,
						realized_pnl, settlement_currency, liquidity_side,
						fee, fee_currency, logical_time
					)
					SELECT
						$1, order_id, $2, account_id, instrument_id,
						side, price, 0.5, position_id, position_effect,
						realized_pnl, settlement_currency, liquidity_side,
						fee, fee_currency, logical_time
					  FROM trading.fills
					 WHERE order_id = $3
					 LIMIT 1`,
					fillID.String(),
					inputID.String(),
					fixture.orderID.String(),
				)
				if err != nil {
					t.Fatalf("insert unexplained fill: %v", err)
				}
			},
			reportKind: func(report platformpostgres.ReconciliationReport) uint64 {
				return report.OrderFillMismatchCount + report.PositionMismatchCount
			},
		},
		{
			name: "orphan protection",
			mutate: func(t *testing.T, pool *pgxpool.Pool, fixture reconciliationFixture) {
				_, err := pool.Exec(context.Background(), `
					UPDATE trading.orders
					   SET status = 'held',
					       bracket_leg = 'stop_loss',
					       reduce_only = false
					 WHERE order_id = $1`,
					fixture.orderID.String(),
				)
				if err != nil {
					t.Fatalf("corrupt position protection: %v", err)
				}
			},
			reportKind: func(report platformpostgres.ReconciliationReport) uint64 {
				return report.ProtectionMismatchCount
			},
		},
		{
			name: "unexplained funding",
			mutate: func(t *testing.T, pool *pgxpool.Pool, _ reconciliationFixture) {
				fundingID := engine.IDFromSequence(engine.ID{}, 8993)
				settlementID := engine.IDFromSequence(engine.ID{}, 8994)
				inputID := engine.IDFromSequence(engine.ID{}, 8995)
				corruptAsReplicationAuthority(t, pool, `
					INSERT INTO trading.funding_settlements (
						funding_id, settlement_id, position_id, input_id,
						account_id, instrument_id, signed_quantity, oracle_price,
						rate, amount, settlement_currency
					)
					SELECT
						$1, $2, position_id, $3,
						account_id, instrument_id, signed_quantity, 100,
						0.01, 1, settlement_currency
					  FROM trading.positions
					 WHERE account_id = 'account-1'
					   AND instrument_id = 'BTC-PERP'
					 LIMIT 1`,
					fundingID.String(),
					settlementID.String(),
					inputID.String(),
				)
			},
			reportKind: func(report platformpostgres.ReconciliationReport) uint64 {
				return report.FundingMismatchCount
			},
		},
		{
			name: "missing funding history projection",
			mutate: func(t *testing.T, pool *pgxpool.Pool, _ reconciliationFixture) {
				corruptAsReplicationAuthority(t, pool, `
					DELETE FROM trading.funding_history_projection
					 WHERE account_id = 'account-1'`)
			},
			reportKind: func(report platformpostgres.ReconciliationReport) uint64 {
				return report.FundingMismatchCount
			},
		},
		{
			name: "wrong funding history account",
			mutate: func(t *testing.T, pool *pgxpool.Pool, _ reconciliationFixture) {
				corruptAsReplicationAuthority(t, pool, `
					UPDATE trading.funding_history_projection
					   SET account_id = 'wrong-account'
					 WHERE account_id = 'account-1'`)
			},
			reportKind: func(report platformpostgres.ReconciliationReport) uint64 {
				return report.FundingMismatchCount
			},
		},
		{
			name: "wrong funding history position",
			mutate: func(t *testing.T, pool *pgxpool.Pool, _ reconciliationFixture) {
				corruptAsReplicationAuthority(t, pool, `
					UPDATE trading.funding_history_projection
					   SET position_id = $1`,
					engine.IDFromSequence(engine.ID{}, 8996).String(),
				)
			},
			reportKind: func(report platformpostgres.ReconciliationReport) uint64 {
				return report.FundingMismatchCount
			},
		},
		{
			name: "wrong funding history logical time",
			mutate: func(t *testing.T, pool *pgxpool.Pool, _ reconciliationFixture) {
				corruptAsReplicationAuthority(t, pool, `
					UPDATE trading.funding_history_projection
					   SET logical_time = logical_time + 1`)
			},
			reportKind: func(report platformpostgres.ReconciliationReport) uint64 {
				return report.FundingMismatchCount
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pool := postgresPool(t)
			fixture := seedReconciliationFixture(t, pool, 8)
			report, err := fixture.store.ReconcileShard(context.Background(), 8)
			if err != nil || !report.Ready {
				t.Fatalf("baseline reconciliation = %+v, error %v", report, err)
			}
			test.mutate(t, pool, fixture)

			report, err = fixture.store.ReconcileShard(context.Background(), 8)
			if !errors.Is(err, platformpostgres.ErrReconciliationMismatch) {
				t.Fatalf(
					"corrupt reconciliation error = %v, want ErrReconciliationMismatch",
					err,
				)
			}
			if report.Ready || test.reportKind(report) == 0 {
				t.Fatalf("corrupt reconciliation report = %+v", report)
			}
			recovered, err := fixture.store.RecoverTradingState(
				context.Background(),
				8,
			)
			if err != nil {
				t.Fatalf("RecoverTradingState after reconciliation halt: %v", err)
			}
			if recovered.Ready() {
				t.Fatal("corrupt projection became ready after recovery")
			}
			var faults int
			if err := pool.QueryRow(context.Background(), `
				SELECT count(*) FROM engine.shard_faults WHERE shard_id = 8`,
			).Scan(&faults); err != nil {
				t.Fatalf("count reconciliation faults: %v", err)
			}
			if faults != 1 {
				t.Fatalf("reconciliation faults = %d, want 1", faults)
			}
		})
	}
}

func TestReconcileShardRejectsCorruptPendingCommandJournal(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, context.Context, *pgxpool.Pool, engine.ID) error
	}{
		{
			name: "missing outbox",
			mutate: func(_ *testing.T, ctx context.Context, pool *pgxpool.Pool, id engine.ID) error {
				_, err := pool.Exec(
					ctx,
					"DELETE FROM messaging.outbox WHERE message_id = $1",
					id.String(),
				)
				return err
			},
		},
		{
			name: "changed canonical envelope",
			mutate: func(_ *testing.T, ctx context.Context, pool *pgxpool.Pool, id engine.ID) error {
				_, err := pool.Exec(ctx, `
					UPDATE messaging.outbox
					   SET payload = jsonb_set(payload, '{sourceSequence}', '2')
					 WHERE message_id = $1`,
					id.String(),
				)
				return err
			},
		},
		{
			name: "changed subject",
			mutate: func(_ *testing.T, ctx context.Context, pool *pgxpool.Pool, id engine.ID) error {
				_, err := pool.Exec(ctx, `
					UPDATE messaging.outbox
					   SET subject = 'engine.input.8.command.v2'
					 WHERE message_id = $1`,
					id.String(),
				)
				return err
			},
		},
		{
			name: "changed schema",
			mutate: func(_ *testing.T, ctx context.Context, pool *pgxpool.Pool, id engine.ID) error {
				_, err := pool.Exec(ctx, `
					UPDATE messaging.outbox
					   SET schema_version = schema_version + 1
					 WHERE message_id = $1`,
					id.String(),
				)
				return err
			},
		},
		{
			name: "missing idempotency",
			mutate: func(t *testing.T, _ context.Context, pool *pgxpool.Pool, id engine.ID) error {
				corruptAsReplicationAuthority(
					t,
					pool,
					"DELETE FROM trading.idempotency_records WHERE command_id = $1",
					id.String(),
				)
				return nil
			},
		},
		{
			name: "changed command payload",
			mutate: func(t *testing.T, _ context.Context, pool *pgxpool.Pool, id engine.ID) error {
				corruptAsReplicationAuthority(t, pool, `
					UPDATE trading.commands
					   SET canonical_payload = '{}'::jsonb
					 WHERE command_id = $1`,
					id.String(),
				)
				return nil
			},
		},
	}

	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			pool := postgresPool(t)
			fixture := seedReconciliationFixture(t, pool, 8)
			commandID := engine.IDFromSequence(engine.ID{}, uint64(8900+index))
			action := engine.TradingAction{
				Kind: engine.TradingActionConfigureAccount,
				ConfigureAccount: &engine.ConfigureAccount{
					AccountID: "pending-account",
					OmsMode:   engine.OmsModeNetting,
				},
			}
			payload, err := engine.EncodeTradingAction(action)
			if err != nil {
				t.Fatalf("EncodeTradingAction: %v", err)
			}
			logicalTime := time.Date(
				2026,
				time.July,
				24,
				16,
				0,
				0,
				index,
				time.UTC,
			)
			input := engine.InputEnvelope{
				InputID:              commandID,
				SchemaVersion:        engine.CurrentSchemaVersion,
				ShardID:              8,
				Kind:                 engine.InputKindCommand,
				SourceID:             "command-journal",
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
			if _, err := platformpostgres.NewCommandJournal(pool).Begin(
				ctx,
				platformpostgres.BeginCommandRequest{
					Scope:            "account:pending-account",
					IdempotencyKey:   fmt.Sprintf("pending-%d", index),
					RequestHash:      sha256.Sum256(outboxPayload),
					CommandID:        commandID,
					AccountID:        "pending-account",
					AccountSequence:  1,
					CommandType:      string(action.Kind),
					SchemaVersion:    engine.CurrentSchemaVersion,
					CanonicalPayload: payload.Bytes(),
					OutboxSubject:    "engine.input.8.command.v1",
					OutboxPayload:    outboxPayload,
					LogicalTime:      logicalTime,
					ExpiresAt:        logicalTime.Add(24 * time.Hour),
				},
			); err != nil {
				t.Fatalf("Begin pending command: %v", err)
			}
			if report, err := fixture.store.ReconcileShard(ctx, 8); err != nil ||
				!report.Ready {
				t.Fatalf("valid pending journal reconciliation = %+v, error %v", report, err)
			}
			if err := test.mutate(t, ctx, pool, commandID); err != nil {
				t.Fatalf("mutate pending command journal: %v", err)
			}
			report, err := fixture.store.ReconcileShard(ctx, 8)
			if !errors.Is(err, platformpostgres.ErrReconciliationMismatch) ||
				report.Ready ||
				report.CommandMismatchCount == 0 {
				t.Fatalf("corrupt pending journal reconciliation = %+v, error %v", report, err)
			}
			assertRowCount(t, pool, "engine.shard_faults", 1)
			recovered, err := fixture.store.RecoverTradingState(ctx, 8)
			if err != nil {
				t.Fatalf("RecoverTradingState: %v", err)
			}
			if recovered.Ready() {
				t.Fatal("corrupt pending journal restart became ready")
			}
		})
	}
}

func TestReconcileShardRejectsBrokenTerminalCommandIdempotencyAuthority(t *testing.T) {
	t.Run("missing terminal idempotency record", func(t *testing.T) {
		ctx := context.Background()
		pool := postgresPool(t)
		fixture := seedReconciliationFixture(t, pool, 8)

		var commandIDText string
		var scope string
		var idempotencyKey string
		var requestHash []byte
		if err := pool.QueryRow(ctx, `
			SELECT command.command_id::text, idempotency.scope,
			       idempotency.idempotency_key, idempotency.request_hash
			  FROM trading.commands AS command
			  JOIN trading.idempotency_records AS idempotency
			    ON idempotency.command_id = command.command_id
			 WHERE command.command_type = 'adjust_balance'
			   AND command.status <> 'pending'
			 ORDER BY command.account_sequence
			 LIMIT 1`,
		).Scan(
			&commandIDText,
			&scope,
			&idempotencyKey,
			&requestHash,
		); err != nil {
			t.Fatalf("load terminal monetary command: %v", err)
		}
		commandID, err := engine.ParseID(commandIDText)
		if err != nil {
			t.Fatalf("parse terminal command ID: %v", err)
		}
		var balanceBefore string
		var ledgerEntriesBefore int
		if err := pool.QueryRow(ctx, `
			SELECT trim_scale(total)::text
			  FROM ledger.balances
			 WHERE account_id = 'account-1' AND currency = 'USDC'`,
		).Scan(&balanceBefore); err != nil {
			t.Fatalf("read balance before corruption: %v", err)
		}
		if err := pool.QueryRow(
			ctx,
			"SELECT count(*) FROM ledger.entries",
		).Scan(&ledgerEntriesBefore); err != nil {
			t.Fatalf("count ledger entries before corruption: %v", err)
		}

		corruptAsReplicationAuthority(
			t,
			pool,
			"DELETE FROM trading.idempotency_records WHERE command_id = $1",
			commandID.String(),
		)
		report, err := fixture.store.ReconcileShard(ctx, 8)
		if !errors.Is(err, platformpostgres.ErrReconciliationMismatch) ||
			report.Ready ||
			report.CommandMismatchCount == 0 {
			t.Fatalf("missing terminal idempotency reconciliation = %+v, error %v", report, err)
		}
		assertRowCount(t, pool, "engine.shard_faults", 1)
		recovered, err := fixture.store.RecoverTradingState(ctx, 8)
		if err != nil {
			t.Fatalf("RecoverTradingState: %v", err)
		}
		if recovered.Ready() {
			t.Fatal("missing terminal idempotency restart became ready")
		}

		retryAction := engine.TradingAction{
			Kind: engine.TradingActionAdjustBalance,
			AdjustBalance: &engine.AdjustBalance{
				AccountID:     "account-1",
				Currency:      "USDC",
				CurrencyScale: 2,
				Operation:     engine.BalanceOperationDeposit,
				Amount:        "1000",
			},
		}
		retryInput := nextStoredInput(
			t,
			recovered,
			fixture.ids,
			fixture.clock,
			retryAction,
		)
		var nextAccountSequence uint64
		if err := pool.QueryRow(ctx, `
			SELECT max(account_sequence) + 1
			  FROM trading.commands
			 WHERE account_id = 'account-1'`,
		).Scan(&nextAccountSequence); err != nil {
			t.Fatalf("read retry account sequence: %v", err)
		}
		retryInput.SourceSequence = nextAccountSequence
		outboxPayload, err := engine.EncodeInputMessage(retryInput)
		if err != nil {
			t.Fatalf("EncodeInputMessage retry: %v", err)
		}
		if _, err := platformpostgres.NewCommandJournal(pool).Begin(
			ctx,
			platformpostgres.BeginCommandRequest{
				Scope:            scope,
				IdempotencyKey:   idempotencyKey,
				RequestHash:      [sha256.Size]byte(requestHash),
				CommandID:        retryInput.InputID,
				AccountID:        "account-1",
				AccountSequence:  nextAccountSequence,
				CommandType:      string(retryAction.Kind),
				SchemaVersion:    retryInput.SchemaVersion,
				CanonicalPayload: retryInput.Payload.Bytes(),
				OutboxSubject:    "engine.input.8.command.v1",
				OutboxPayload:    outboxPayload,
				LogicalTime:      time.Unix(0, retryInput.LogicalTime.UnixNano()).UTC(),
				ExpiresAt: time.Unix(
					0,
					fixture.clock.Now().UnixNano(),
				).UTC().Add(24 * time.Hour),
			},
		); err != nil {
			t.Fatalf("Begin retry after corruption: %v", err)
		}
		next, _, _, err := fixture.store.ApplyTrading(
			ctx,
			recovered,
			retryInput,
			retryAction,
			platformpostgres.ApplyOptions{},
		)
		var engineErr *engine.Error
		if !errors.As(err, &engineErr) ||
			engineErr.Kind != engine.ErrShardNotReady ||
			next.Ready() {
			t.Fatalf("retry after corruption state = %+v, error %v", next, err)
		}
		var balanceAfter string
		var ledgerEntriesAfter int
		if err := pool.QueryRow(ctx, `
			SELECT trim_scale(total)::text
			  FROM ledger.balances
			 WHERE account_id = 'account-1' AND currency = 'USDC'`,
		).Scan(&balanceAfter); err != nil {
			t.Fatalf("read balance after retry: %v", err)
		}
		if err := pool.QueryRow(
			ctx,
			"SELECT count(*) FROM ledger.entries",
		).Scan(&ledgerEntriesAfter); err != nil {
			t.Fatalf("count ledger entries after retry: %v", err)
		}
		if balanceAfter != balanceBefore || ledgerEntriesAfter != ledgerEntriesBefore {
			t.Fatalf(
				"retry duplicated money: balance %s -> %s, ledger entries %d -> %d",
				balanceBefore,
				balanceAfter,
				ledgerEntriesBefore,
				ledgerEntriesAfter,
			)
		}
	})

	t.Run("orphan idempotency record", func(t *testing.T) {
		ctx := context.Background()
		pool := postgresPool(t)
		fixture := seedReconciliationFixture(t, pool, 8)
		orphanID := engine.IDFromSequence(engine.ID{}, 8999)
		corruptAsReplicationAuthority(t, pool, `
			INSERT INTO trading.idempotency_records (
				scope, idempotency_key, request_hash, command_id, state, expires_at
			) VALUES (
				'account:orphan', 'orphan',
				decode(repeat('8f', 32), 'hex'), $1, 'in_progress',
				'2026-07-26T00:00:00Z'
			)`,
			orphanID.String(),
		)
		report, err := fixture.store.ReconcileShard(ctx, 8)
		if !errors.Is(err, platformpostgres.ErrReconciliationMismatch) ||
			report.Ready ||
			report.CommandMismatchCount == 0 {
			t.Fatalf("orphan idempotency reconciliation = %+v, error %v", report, err)
		}
		recovered, err := fixture.store.RecoverTradingState(ctx, 8)
		if err != nil {
			t.Fatalf("RecoverTradingState: %v", err)
		}
		if recovered.Ready() {
			t.Fatal("orphan idempotency restart became ready")
		}
	})
}

func TestReconcileShardAcceptsRoundedMultiFillAndClosedPosition(t *testing.T) {
	pool := postgresPool(t)
	fixture := seedReconciliationFixture(t, pool, 8)
	baseline, err := fixture.store.ReconcileShard(context.Background(), 8)
	if err != nil || !baseline.Ready {
		t.Fatalf("rounded multi-fill reconciliation = %+v, error %v", baseline, err)
	}
	closeOrderID := engine.IDFromSequence(engine.ID{}, 9810)
	state, _, _, _ := applyStoredTrading(
		t,
		pool,
		fixture.store,
		fixture.state,
		fixture.ids,
		fixture.clock,
		engine.TradingAction{
			Kind: engine.TradingActionSubmitOrder,
			SubmitOrder: &engine.SubmitOrder{
				OrderID:      closeOrderID,
				AccountID:    "account-1",
				InstrumentID: "BTC-PERP",
				Side:         engine.SideSell,
				Type:         engine.OrderTypeMarket,
				TimeInForce:  engine.TimeInForceIOC,
				Quantity:     "3",
				ReduceOnly:   true,
			},
		},
		platformpostgres.ApplyOptions{},
	)
	if !state.Ready() {
		t.Fatal("healthy close halted deterministic state")
	}
	report, err := fixture.store.ReconcileShard(context.Background(), 8)
	if err != nil || !report.Ready {
		t.Fatalf("closed-position reconciliation = %+v, error %v", report, err)
	}
}

func TestReconcileShardPreservesNanosecondTimesAndCanonicalSlippage(t *testing.T) {
	pool := postgresPool(t)
	fixture := seedReconciliationFixture(t, pool, 8)
	report, err := fixture.store.ReconcileShard(context.Background(), 8)
	if err != nil || !report.Ready {
		t.Fatalf("nanosecond baseline reconciliation = %+v, error %v", report, err)
	}

	stopOrderID := engine.IDFromSequence(engine.ID{}, 9815)
	state, _, _, _ := applyStoredTrading(
		t,
		pool,
		fixture.store,
		fixture.state,
		fixture.ids,
		fixture.clock,
		engine.TradingAction{
			Kind: engine.TradingActionSubmitOrder,
			SubmitOrder: &engine.SubmitOrder{
				OrderID:        stopOrderID,
				AccountID:      "account-1",
				InstrumentID:   "BTC-PERP",
				Side:           engine.SideBuy,
				Type:           engine.OrderTypeStopMarket,
				TimeInForce:    engine.TimeInForceGTC,
				Quantity:       "1",
				TriggerPrice:   "110",
				MaxSlippageBPS: uint32Pointer(50),
			},
		},
		platformpostgres.ApplyOptions{},
	)
	state, _, _, _ = applyStoredTrading(
		t,
		pool,
		fixture.store,
		state,
		fixture.ids,
		fixture.clock,
		engine.TradingAction{
			Kind: engine.TradingActionUpdateBook,
			UpdateBook: &engine.UpdateBook{
				InstrumentID: "BTC-PERP",
				MarkPrice:    "110",
				Bids:         []engine.BookLevel{{Price: "109", Quantity: "10"}},
				Asks:         []engine.BookLevel{{Price: "110", Quantity: "10"}},
			},
		},
		platformpostgres.ApplyOptions{},
	)
	if !state.Ready() {
		t.Fatal("valid nanosecond trigger halted deterministic state")
	}
	report, err = fixture.store.ReconcileShard(context.Background(), 8)
	if err != nil || !report.Ready {
		t.Fatalf("nanosecond trigger reconciliation = %+v, error %v", report, err)
	}

	var triggeredAt int64
	if err := pool.QueryRow(context.Background(), `
		SELECT triggered_at
		  FROM trading.orders
		 WHERE order_id = $1`,
		stopOrderID.String(),
	).Scan(&triggeredAt); err != nil {
		t.Fatalf("read exact triggered time: %v", err)
	}
	if triggeredAt%int64(time.Second) != 123456789 {
		t.Fatalf("triggered nanoseconds = %d, want 123456789", triggeredAt%int64(time.Second))
	}
	var nonExactTimes int
	if err := pool.QueryRow(context.Background(), `
		SELECT
			(SELECT count(*) FROM trading.fills
			  WHERE logical_time % 1000000000 <> 123456789)
			+
			(SELECT count(*) FROM ledger.transactions
			  WHERE logical_time % 1000000000 <> 123456789)`,
	).Scan(&nonExactTimes); err != nil {
		t.Fatalf("check exact durable logical times: %v", err)
	}
	if nonExactTimes != 0 {
		t.Fatalf("non-exact durable logical times = %d", nonExactTimes)
	}
}

func TestEngineStoreEnforcesInitialSingleShardDeployment(t *testing.T) {
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := platformpostgres.NewMigrator(
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).MigrateAndProvision(context.Background(), 8); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	store := platformpostgres.NewEngineStore(pool)
	owner, err := store.AcquireShardOwnership(context.Background(), 8)
	if err != nil {
		t.Fatalf("AcquireShardOwnership shard 8: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := owner.Close(context.Background()); closeErr != nil {
			t.Fatalf("close shard 8 owner: %v", closeErr)
		}
	})
	other, err := store.AcquireShardOwnership(context.Background(), 9)
	if other != nil {
		_ = other.Close(context.Background())
		t.Fatal("second deployment shard acquired ownership")
	}
	if !errors.Is(err, platformpostgres.ErrDeploymentShardConflict) {
		t.Fatalf(
			"AcquireShardOwnership shard 9 error = %v, want ErrDeploymentShardConflict",
			err,
		)
	}
	var shardID uint64
	if err := pool.QueryRow(context.Background(), `
		SELECT shard_id FROM engine.deployment_shard WHERE singleton`,
	).Scan(&shardID); err != nil {
		t.Fatalf("read deployment shard: %v", err)
	}
	if shardID != 8 {
		t.Fatalf("deployment shard = %d, want 8", shardID)
	}
}

func TestEngineStorePersistsAmendedOrderQuantity(t *testing.T) {
	pool := postgresPool(t)
	fixture := seedReconciliationFixture(t, pool, 8)
	orderID := engine.IDFromSequence(engine.ID{}, 9820)
	state, _, _, _ := applyStoredTrading(
		t,
		pool,
		fixture.store,
		fixture.state,
		fixture.ids,
		fixture.clock,
		engine.TradingAction{
			Kind: engine.TradingActionSubmitOrder,
			SubmitOrder: &engine.SubmitOrder{
				OrderID:      orderID,
				AccountID:    "account-1",
				InstrumentID: "BTC-PERP",
				Side:         engine.SideBuy,
				Type:         engine.OrderTypeLimit,
				TimeInForce:  engine.TimeInForceGTC,
				Quantity:     "1",
				Price:        "90",
			},
		},
		platformpostgres.ApplyOptions{},
	)
	state, _, _, _ = applyStoredTrading(
		t,
		pool,
		fixture.store,
		state,
		fixture.ids,
		fixture.clock,
		engine.TradingAction{
			Kind: engine.TradingActionAmendOrder,
			AmendOrder: &engine.AmendOrder{
				AccountID: "account-1",
				OrderID:   orderID,
				Quantity:  "2",
				Price:     "91",
			},
		},
		platformpostgres.ApplyOptions{},
	)
	var quantity string
	var price string
	if err := pool.QueryRow(context.Background(), `
		SELECT trim_scale(quantity)::text, trim_scale(limit_price)::text
		  FROM trading.orders
		 WHERE order_id = $1`,
		orderID.String(),
	).Scan(&quantity, &price); err != nil {
		t.Fatalf("read amended durable order: %v", err)
	}
	if quantity != "2" || price != "91" {
		t.Fatalf("amended durable order = quantity %s price %s", quantity, price)
	}
	report, err := fixture.store.ReconcileShard(context.Background(), 8)
	if err != nil || !report.Ready {
		t.Fatalf("amended-order reconciliation = %+v, error %v", report, err)
	}
}

type reconciliationFixture struct {
	store        *platformpostgres.EngineStore
	state        engine.State
	ids          *testkit.IDSequence
	clock        *testkit.ManualClock
	orderID      engine.ID
	orderInputID engine.ID
}

func seedReconciliationFixture(
	t *testing.T,
	pool *pgxpool.Pool,
	shardID engine.ShardID,
) reconciliationFixture {
	t.Helper()
	resetDurableSchemas(t, pool)
	if err := platformpostgres.NewMigrator(
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).MigrateAndProvision(context.Background(), shardID); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return seedReconciliationFixtureWithStorePool(t, pool, pool, shardID)
}

func seedReconciliationFixtureWithStorePool(
	t *testing.T,
	pool *pgxpool.Pool,
	storePool *pgxpool.Pool,
	shardID engine.ShardID,
) reconciliationFixture {
	t.Helper()
	store := platformpostgres.NewEngineStore(storePool)
	state := engine.NewState(shardID)
	ids := testkit.NewShardIDSequence(shardID)
	clock := testkit.NewManualClock(
		engine.NewLogicalTime(time.Date(
			2026,
			time.July,
			24,
			14,
			0,
			0,
			123456789,
			time.UTC,
		)),
	)
	actions := []engine.TradingAction{
		{
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
		},
		{
			Kind: engine.TradingActionConfigureAccount,
			ConfigureAccount: &engine.ConfigureAccount{
				AccountID: "account-1",
				OmsMode:   engine.OmsModeNetting,
			},
		},
		{
			Kind: engine.TradingActionAdjustBalance,
			AdjustBalance: &engine.AdjustBalance{
				AccountID:     "account-1",
				Currency:      "USDC",
				CurrencyScale: 2,
				Operation:     engine.BalanceOperationDeposit,
				Amount:        "1000",
			},
		},
		{
			Kind: engine.TradingActionConfigureRisk,
			ConfigureRisk: &engine.ConfigureRisk{
				AccountID:    "account-1",
				InstrumentID: "BTC-PERP",
				MarginMode:   engine.MarginModeCross,
				Leverage:     "5",
			},
		},
		{
			Kind: engine.TradingActionUpdateBook,
			UpdateBook: &engine.UpdateBook{
				InstrumentID: "BTC-PERP",
				MarkPrice:    "100",
				Bids:         []engine.BookLevel{{Price: "99", Quantity: "10"}},
				Asks: []engine.BookLevel{
					{Price: "100", Quantity: "1"},
					{Price: "105", Quantity: "2"},
				},
			},
		},
	}
	for _, action := range actions {
		state, _, _, _ = applyStoredTrading(
			t,
			pool,
			store,
			state,
			ids,
			clock,
			action,
			platformpostgres.ApplyOptions{},
		)
	}
	orderID := engine.IDFromSequence(engine.ID{}, 8801)
	state, _, input, _ := applyStoredTrading(
		t,
		pool,
		store,
		state,
		ids,
		clock,
		engine.TradingAction{
			Kind: engine.TradingActionSubmitOrder,
			SubmitOrder: &engine.SubmitOrder{
				OrderID:        orderID,
				AccountID:      "account-1",
				InstrumentID:   "BTC-PERP",
				Side:           engine.SideBuy,
				Type:           engine.OrderTypeMarket,
				TimeInForce:    engine.TimeInForceIOC,
				Quantity:       "3",
				MaxSlippageBPS: uint32Pointer(50),
			},
		},
		platformpostgres.ApplyOptions{},
	)
	state, _, _, _ = applyStoredTrading(
		t,
		pool,
		store,
		state,
		ids,
		clock,
		engine.TradingAction{
			Kind: engine.TradingActionSettleFunding,
			SettleFunding: &engine.SettleFunding{
				SettlementID: engine.IDFromSequence(engine.ID{}, 8802),
				InstrumentID: "BTC-PERP",
				OraclePrice:  "100",
				Rate:         "0.01",
			},
		},
		platformpostgres.ApplyOptions{},
	)
	if !state.Ready() {
		t.Fatal("seed reconciliation fixture halted")
	}
	return reconciliationFixture{
		store:        store,
		state:        state,
		ids:          ids,
		clock:        clock,
		orderID:      orderID,
		orderInputID: input.InputID,
	}
}

func TestEngineStoreRejectsCommandInputThatDiffersFromJournal(t *testing.T) {
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := platformpostgres.NewMigrator(
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).MigrateAndProvision(context.Background(), 7); err != nil {
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
			).MigrateAndProvision(context.Background(), 7); err != nil {
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
	).MigrateAndProvision(context.Background(), 7); err != nil {
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
	if !errors.Is(err, platformpostgres.ErrDeploymentShardConflict) {
		t.Fatalf(
			"cross-shard configuration error = %v, want deployment-shard conflict",
			err,
		)
	}
	if !halted.Ready() || halted.NextStreamSequence() != 1 {
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
	assertRowCount(t, pool, "engine.shard_faults", 0)
	recovered, err := store.RecoverTradingState(context.Background(), 7)
	if err != nil {
		t.Fatalf("RecoverTradingState configured shard: %v", err)
	}
	if !recovered.Ready() || recovered.Hash() != firstState.Hash() {
		t.Fatalf(
			"recovered configured shard = ready %t hash %s, want true %s",
			recovered.Ready(),
			recovered.Hash(),
			firstState.Hash(),
		)
	}
}

func TestEngineStoreMissingAccountShardFailsClosedWithoutBinding(t *testing.T) {
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := platformpostgres.NewMigrator(
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).MigrateAndProvision(context.Background(), 8); err != nil {
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
	} {
		t.Run(test.name, func(t *testing.T) {
			pool := postgresPool(t)
			resetDurableSchemas(t, pool)
			if err := platformpostgres.NewMigrator(
				pool,
				os.DirFS(filepath.Join("..", "..", "..", "migrations")),
			).MigrateAndProvision(context.Background(), 7); err != nil {
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
				WITH idempotency AS (
					INSERT INTO trading.idempotency_records (
						scope, idempotency_key, request_hash, command_id,
						state, expires_at
					) VALUES (
						'account:account-1', 'missing-mapping',
						decode(repeat('61', 32), 'hex'), $1,
						'in_progress', $6
					)
					RETURNING command_id
				)
				INSERT INTO trading.commands (
					command_id, account_id, account_sequence, command_type,
					schema_version, canonical_payload, status, logical_time
				)
				SELECT command_id, 'account-1', 1, $2, $3, $4, 'pending', $5
				  FROM idempotency`,
				commandID.String(),
				string(action.Kind),
				storedInput.SchemaVersion,
				payload.Bytes(),
				now.UnixNano(),
				now.Add(24*time.Hour),
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
		name         string
		mutate       func(*engine.InputEnvelope)
		wantError    error
		remainsReady bool
	}{
		{
			name:      "schema version",
			mutate:    func(input *engine.InputEnvelope) { input.SchemaVersion++ },
			wantError: engine.ErrUnknownSchema,
		},
		{
			name:         "shard",
			mutate:       func(input *engine.InputEnvelope) { input.ShardID++ },
			wantError:    platformpostgres.ErrDeploymentShardConflict,
			remainsReady: true,
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
			).MigrateAndProvision(context.Background(), 7); err != nil {
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
			if next.Ready() != mutation.remainsReady {
				t.Fatalf(
					"invalid command readiness = %t, want %t",
					next.Ready(),
					mutation.remainsReady,
				)
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
	).MigrateAndProvision(context.Background(), 7); err != nil {
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
		mutate func(*testing.T, context.Context, *pgxpool.Pool, engine.ID) error
	}{
		{
			name: "command type",
			mutate: func(t *testing.T, _ context.Context, pool *pgxpool.Pool, id engine.ID) error {
				corruptAsReplicationAuthority(t, pool, `
					UPDATE trading.commands
					   SET command_type = 'submit_order'
					 WHERE command_id = $1`,
					id.String(),
				)
				return nil
			},
		},
		{
			name: "logical time",
			mutate: func(t *testing.T, _ context.Context, pool *pgxpool.Pool, id engine.ID) error {
				corruptAsReplicationAuthority(t, pool, `
					UPDATE trading.commands
					   SET logical_time = logical_time + 1000000000
					 WHERE command_id = $1`,
					id.String(),
				)
				return nil
			},
		},
		{
			name: "outbox schema",
			mutate: func(_ *testing.T, ctx context.Context, pool *pgxpool.Pool, id engine.ID) error {
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
			).MigrateAndProvision(ctx, 7); err != nil {
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
			if err := test.mutate(t, ctx, pool, commandID); err != nil {
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
	).MigrateAndProvision(ctx, 7); err != nil {
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

func TestEngineStoreRecoversDecisionHashV2AndExtendsTheChainWithV3(
	t *testing.T,
) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := platformpostgres.NewMigrator(
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).MigrateAndProvision(ctx, 8); err != nil {
		t.Fatalf("migrate mixed decision history database: %v", err)
	}

	store := platformpostgres.NewEngineStore(pool)
	initial := engine.NewState(8)
	ids := testkit.NewShardIDSequence(8)
	clock := testkit.NewManualClock(
		engine.NewLogicalTime(time.Date(2026, time.July, 26, 15, 0, 0, 0, time.UTC)),
	)
	legacyAction := engine.TradingAction{
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
	}
	legacyInput := nextStoredInput(t, initial, ids, clock, legacyAction)
	_, currentDecision, _, _ := applyStoredInput(
		t,
		pool,
		store,
		initial,
		legacyInput,
		legacyAction,
		platformpostgres.ApplyOptions{},
	)
	if currentDecision.DecisionHashVersion != 3 {
		t.Fatalf(
			"initial persisted decision version = %d, want 3",
			currentDecision.DecisionHashVersion,
		)
	}
	legacyState, legacyDecision, err :=
		engine.ApplyTradingWithReceiptsAtDecisionHashVersion(
			initial,
			legacyInput,
			legacyAction,
			nil,
			2,
		)
	if err != nil {
		t.Fatalf("derive legacy decision: %v", err)
	}
	legacyDecisionJSON, err := json.Marshal(legacyDecision)
	if err != nil {
		t.Fatalf("encode legacy decision: %v", err)
	}
	legacyDecisionHash := legacyDecision.DecisionHash
	legacyStateHash := legacyState.Hash()

	connection, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire legacy-history connection: %v", err)
	}
	if _, err := connection.Exec(
		ctx,
		"ALTER TABLE engine.input_receipts DISABLE TRIGGER USER",
	); err != nil {
		connection.Release()
		t.Fatalf("disable immutable receipt triggers: %v", err)
	}
	defer func() {
		_, _ = pool.Exec(
			context.Background(),
			"ALTER TABLE engine.input_receipts ENABLE TRIGGER USER",
		)
	}()
	if _, err := connection.Exec(ctx, `
		UPDATE engine.input_receipts
		   SET decision_hash_version = 2,
		       decision_hash = $1,
		       resulting_state_hash = $2,
		       decision = $3
		 WHERE shard_id = 8
		   AND input_id = $4`,
		legacyDecisionHash[:],
		legacyStateHash[:],
		legacyDecisionJSON,
		legacyInput.InputID.String(),
	); err != nil {
		connection.Release()
		t.Fatalf("install immutable legacy receipt fixture: %v", err)
	}
	if _, err := connection.Exec(ctx, `
		UPDATE engine.shard_checkpoints
		   SET state_hash = $1
		 WHERE shard_id = 8`,
		legacyStateHash[:],
	); err != nil {
		connection.Release()
		t.Fatalf("install legacy checkpoint fixture: %v", err)
	}
	if _, err := connection.Exec(
		ctx,
		"ALTER TABLE engine.input_receipts ENABLE TRIGGER USER",
	); err != nil {
		connection.Release()
		t.Fatalf("reenable immutable receipt triggers: %v", err)
	}
	connection.Release()

	recovered, err := store.RecoverTradingState(ctx, 8)
	if err != nil {
		t.Fatalf("recover v2 history: %v", err)
	}
	if recovered.Hash() != legacyState.Hash() || !recovered.Ready() {
		t.Fatalf(
			"recovered v2 history ready=%t hash=%s, want %s",
			recovered.Ready(),
			recovered.Hash(),
			legacyState.Hash(),
		)
	}

	duplicateInput := legacyInput
	duplicateInput.StreamSequence = recovered.NextStreamSequence()
	_, currentDuplicateDecision, duplicate, err :=
		store.ApplyTrading(
			ctx,
			recovered,
			duplicateInput,
			legacyAction,
			platformpostgres.ApplyOptions{},
		)
	if err != nil || !duplicate {
		t.Fatalf(
			"persist current duplicate = duplicate %t error %v",
			duplicate,
			err,
		)
	}
	if currentDuplicateDecision.DecisionHashVersion != 3 {
		t.Fatalf(
			"persisted duplicate version = %d, want 3",
			currentDuplicateDecision.DecisionHashVersion,
		)
	}
	legacyDuplicateState, legacyDuplicateDecision, err :=
		engine.ApplyDuplicateDeliveryAtDecisionHashVersion(
			legacyState,
			duplicateInput,
			engine.NewReceipt(legacyInput, legacyDecision),
			2,
		)
	if err != nil {
		t.Fatalf("derive legacy duplicate decision: %v", err)
	}
	legacyDuplicateStateHash := legacyDuplicateState.Hash()
	legacyDuplicateJSON, err := json.Marshal(legacyDuplicateDecision)
	if err != nil {
		t.Fatalf("encode legacy duplicate decision: %v", err)
	}
	duplicateConnection, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire legacy duplicate connection: %v", err)
	}
	if _, err := duplicateConnection.Exec(
		ctx,
		"ALTER TABLE engine.duplicate_delivery_receipts DISABLE TRIGGER USER",
	); err != nil {
		duplicateConnection.Release()
		t.Fatalf("disable immutable duplicate triggers: %v", err)
	}
	defer func() {
		_, _ = pool.Exec(
			context.Background(),
			"ALTER TABLE engine.duplicate_delivery_receipts ENABLE TRIGGER USER",
		)
	}()
	if _, err := duplicateConnection.Exec(ctx, `
		UPDATE engine.duplicate_delivery_receipts
		   SET decision_hash = $1,
		       resulting_state_hash = $2,
		       decision = $3
		 WHERE shard_id = 8
		   AND stream_sequence = $4`,
		legacyDuplicateDecision.DecisionHash[:],
		legacyDuplicateStateHash[:],
		legacyDuplicateJSON,
		duplicateInput.StreamSequence,
	); err != nil {
		duplicateConnection.Release()
		t.Fatalf("install immutable legacy duplicate fixture: %v", err)
	}
	if _, err := duplicateConnection.Exec(ctx, `
		UPDATE engine.shard_checkpoints
		   SET state_hash = $1,
		       next_stream_sequence = $2
		 WHERE shard_id = 8`,
		legacyDuplicateStateHash[:],
		legacyDuplicateState.NextStreamSequence(),
	); err != nil {
		duplicateConnection.Release()
		t.Fatalf("install legacy duplicate checkpoint fixture: %v", err)
	}
	if _, err := duplicateConnection.Exec(
		ctx,
		"ALTER TABLE engine.duplicate_delivery_receipts ENABLE TRIGGER USER",
	); err != nil {
		duplicateConnection.Release()
		t.Fatalf("reenable immutable duplicate triggers: %v", err)
	}
	duplicateConnection.Release()

	recoveredDuplicate, err := store.RecoverTradingState(ctx, 8)
	if err != nil {
		t.Fatalf("recover v2 business and duplicate history: %v", err)
	}
	if recoveredDuplicate.Hash() != legacyDuplicateState.Hash() ||
		recoveredDuplicate.NextStreamSequence() !=
			legacyDuplicateState.NextStreamSequence() ||
		!recoveredDuplicate.Ready() {
		t.Fatalf(
			"recovered v2 duplicate ready=%t hash=%s next=%d, want %s/%d",
			recoveredDuplicate.Ready(),
			recoveredDuplicate.Hash(),
			recoveredDuplicate.NextStreamSequence(),
			legacyDuplicateState.Hash(),
			legacyDuplicateState.NextStreamSequence(),
		)
	}
	recovered = recoveredDuplicate

	next, v3Decision, _, _ := applyStoredTrading(
		t,
		pool,
		store,
		recovered,
		ids,
		clock,
		engine.TradingAction{
			Kind: engine.TradingActionConfigureAccount,
			ConfigureAccount: &engine.ConfigureAccount{
				AccountID: "mixed-version-account",
				OmsMode:   engine.OmsModeNetting,
			},
		},
		platformpostgres.ApplyOptions{},
	)
	if v3Decision.DecisionHashVersion != 3 {
		t.Fatalf(
			"new decision version = %d, want 3",
			v3Decision.DecisionHashVersion,
		)
	}
	recoveredMixed, err := store.RecoverTradingState(ctx, 8)
	if err != nil {
		t.Fatalf("recover mixed v2/v3 history: %v", err)
	}
	if recoveredMixed.Hash() != next.Hash() || !recoveredMixed.Ready() {
		t.Fatalf(
			"mixed recovery ready=%t hash=%s, want %s",
			recoveredMixed.Ready(),
			recoveredMixed.Hash(),
			next.Hash(),
		)
	}
}

func TestEngineStoreRestoredMarkRepairsProjectionWithoutLedgerDelta(
	t *testing.T,
) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := platformpostgres.NewMigrator(
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).MigrateAndProvision(ctx, 8); err != nil {
		t.Fatalf("migrate restored-mark database: %v", err)
	}

	store := platformpostgres.NewEngineStore(pool)
	state := engine.NewState(8)
	ids := testkit.NewShardIDSequence(8)
	clock := testkit.NewManualClock(
		engine.NewLogicalTime(time.Date(2026, time.July, 26, 16, 0, 0, 0, time.UTC)),
	)
	apply := func(action engine.TradingAction) engine.Decision {
		var decision engine.Decision
		state, decision, _, _ = applyStoredTrading(
			t,
			pool,
			store,
			state,
			ids,
			clock,
			action,
			platformpostgres.ApplyOptions{},
		)
		return decision
	}
	apply(engine.TradingAction{
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
	})
	apply(engine.TradingAction{
		Kind: engine.TradingActionConfigureAccount,
		ConfigureAccount: &engine.ConfigureAccount{
			AccountID: "account-restored-mark",
			OmsMode:   engine.OmsModeNetting,
		},
	})
	apply(engine.TradingAction{
		Kind: engine.TradingActionAdjustBalance,
		AdjustBalance: &engine.AdjustBalance{
			AccountID:     "account-restored-mark",
			Currency:      "USDC",
			CurrencyScale: 2,
			Operation:     engine.BalanceOperationDeposit,
			Amount:        "1000",
		},
	})
	apply(engine.TradingAction{
		Kind: engine.TradingActionUpdateBook,
		UpdateBook: &engine.UpdateBook{
			InstrumentID: "BTC-PERP",
			MarkPrice:    "100",
			Bids:         []engine.BookLevel{{Price: "99", Quantity: "10"}},
			Asks:         []engine.BookLevel{{Price: "100", Quantity: "10"}},
		},
	})
	apply(engine.TradingAction{
		Kind: engine.TradingActionSubmitOrder,
		SubmitOrder: &engine.SubmitOrder{
			OrderID:      ids.Next(),
			AccountID:    "account-restored-mark",
			InstrumentID: "BTC-PERP",
			Side:         engine.SideBuy,
			Type:         engine.OrderTypeMarket,
			TimeInForce:  engine.TimeInForceGTC,
			Quantity:     "1",
		},
	})

	var (
		ledgerTransactionsBefore int
		ledgerEntriesBefore      int
	)
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM ledger.transactions),
			(SELECT count(*) FROM ledger.entries)`,
	).Scan(&ledgerTransactionsBefore, &ledgerEntriesBefore); err != nil {
		t.Fatalf("count baseline ledger rows: %v", err)
	}

	markless := apply(engine.TradingAction{
		Kind: engine.TradingActionUpdateBook,
		UpdateBook: &engine.UpdateBook{
			InstrumentID: "BTC-PERP",
			Bids:         []engine.BookLevel{{Price: "99", Quantity: "10"}},
			Asks:         []engine.BookLevel{{Price: "100", Quantity: "10"}},
		},
	})
	if len(markless.BalanceChanges) != 0 || len(markless.LedgerChanges) != 0 {
		t.Fatalf(
			"markless decision = balances %#v ledger %#v",
			markless.BalanceChanges,
			markless.LedgerChanges,
		)
	}
	var markIsNull bool
	if err := pool.QueryRow(ctx, `
		SELECT mark_price IS NULL
		  FROM market.books
		 WHERE instrument_id = 'BTC-PERP'`,
	).Scan(&markIsNull); err != nil || !markIsNull {
		t.Fatalf("persisted markless book null=%t, error %v", markIsNull, err)
	}
	recoveredMarkless, err := store.RecoverTradingState(ctx, 8)
	if err != nil {
		t.Fatalf("recover markless state: %v", err)
	}
	if recoveredMarkless.Hash() != state.Hash() || !recoveredMarkless.Ready() {
		t.Fatalf(
			"recovered markless ready=%t hash=%s, want %s",
			recoveredMarkless.Ready(),
			recoveredMarkless.Hash(),
			state.Hash(),
		)
	}
	if report, err := store.ReconcileShard(ctx, 8); err != nil || !report.Ready {
		t.Fatalf("markless reconciliation = %+v, error %v", report, err)
	}
	assertLedgerRowCounts(
		t,
		pool,
		ledgerTransactionsBefore,
		ledgerEntriesBefore,
	)

	restoredAction := engine.TradingAction{
		Kind: engine.TradingActionUpdateBook,
		UpdateBook: &engine.UpdateBook{
			InstrumentID: "BTC-PERP",
			MarkPrice:    "110",
			Bids:         []engine.BookLevel{{Price: "109", Quantity: "10"}},
			Asks:         []engine.BookLevel{{Price: "110", Quantity: "10"}},
		},
	}
	var restoredInput engine.InputEnvelope
	var restored engine.Decision
	state, restored, restoredInput, _ = applyStoredTrading(
		t,
		pool,
		store,
		state,
		ids,
		clock,
		restoredAction,
		platformpostgres.ApplyOptions{},
	)
	if len(restored.BalanceChanges) != 1 ||
		restored.BalanceChanges[0].Total != "1000" ||
		restored.BalanceChanges[0].Used != "1" ||
		restored.BalanceChanges[0].Equity != "1010" ||
		restored.BalanceChanges[0].Free != "1009" {
		t.Fatalf("restored projection = %#v", restored.BalanceChanges)
	}
	if len(restored.LedgerChanges) != 0 {
		t.Fatalf("restored mark minted ledger money = %#v", restored.LedgerChanges)
	}
	var (
		persistedTotal  string
		persistedUsed   string
		persistedFree   string
		persistedEquity string
	)
	if err := pool.QueryRow(ctx, `
		SELECT
			trim_scale(total)::text,
			trim_scale(used)::text,
			trim_scale(free)::text,
			trim_scale(equity)::text
		  FROM ledger.balances
		 WHERE account_id = 'account-restored-mark'
		   AND currency = 'USDC'`,
	).Scan(
		&persistedTotal,
		&persistedUsed,
		&persistedFree,
		&persistedEquity,
	); err != nil {
		t.Fatalf("read persisted restored projection: %v", err)
	}
	if persistedTotal != "1000" ||
		persistedUsed != "1" ||
		persistedFree != "1009" ||
		persistedEquity != "1010" {
		t.Fatalf(
			"persisted restored projection = %s/%s/%s/%s",
			persistedTotal,
			persistedUsed,
			persistedFree,
			persistedEquity,
		)
	}
	assertLedgerRowCounts(
		t,
		pool,
		ledgerTransactionsBefore,
		ledgerEntriesBefore,
	)

	recovered, err := store.RecoverTradingState(ctx, 8)
	if err != nil {
		t.Fatalf("recover restored projection: %v", err)
	}
	if recovered.Hash() != state.Hash() || !recovered.Ready() {
		t.Fatalf(
			"recovered restored state ready=%t hash=%s, want %s",
			recovered.Ready(),
			recovered.Hash(),
			state.Hash(),
		)
	}
	if report, err := store.ReconcileShard(ctx, 8); err != nil || !report.Ready {
		t.Fatalf("restored projection reconciliation = %+v, error %v", report, err)
	}

	duplicateInput := restoredInput
	duplicateInput.StreamSequence = state.NextStreamSequence()
	duplicateState, duplicateDecision, duplicate, err := store.ApplyTrading(
		ctx,
		state,
		duplicateInput,
		restoredAction,
		platformpostgres.ApplyOptions{},
	)
	if err != nil || !duplicate {
		t.Fatalf(
			"restored-mark duplicate = duplicate %t error %v",
			duplicate,
			err,
		)
	}
	if len(duplicateDecision.LedgerChanges) != 0 {
		t.Fatalf("restored-mark duplicate ledger = %#v", duplicateDecision.LedgerChanges)
	}
	assertLedgerRowCounts(
		t,
		pool,
		ledgerTransactionsBefore,
		ledgerEntriesBefore,
	)
	recoveredDuplicate, err := store.RecoverTradingState(ctx, 8)
	if err != nil {
		t.Fatalf("recover restored-mark duplicate: %v", err)
	}
	if recoveredDuplicate.Hash() != duplicateState.Hash() ||
		!recoveredDuplicate.Ready() {
		t.Fatalf(
			"recovered duplicate ready=%t hash=%s, want %s",
			recoveredDuplicate.Ready(),
			recoveredDuplicate.Hash(),
			duplicateState.Hash(),
		)
	}
	if report, err := store.ReconcileShard(ctx, 8); err != nil || !report.Ready {
		t.Fatalf("duplicate reconciliation = %+v, error %v", report, err)
	}
}

func TestEngineStoreRejectsCurrencyScaleAliasesAndRecoversExactly(
	t *testing.T,
) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := platformpostgres.NewMigrator(
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).MigrateAndProvision(ctx, 8); err != nil {
		t.Fatalf("migrate currency-scale database: %v", err)
	}

	store := platformpostgres.NewEngineStore(pool)
	state := engine.NewState(8)
	ids := testkit.NewShardIDSequence(8)
	clock := testkit.NewManualClock(
		engine.NewLogicalTime(time.Date(2026, time.July, 26, 17, 0, 0, 0, time.UTC)),
	)
	instrument := func(
		instrumentID string,
		revision uint64,
		currency string,
		scale uint8,
	) engine.TradingAction {
		return engine.TradingAction{
			Kind: engine.TradingActionConfigureInstrument,
			ConfigureInstrument: &engine.ConfigureInstrument{
				InstrumentID:            instrumentID,
				Revision:                revision,
				PriceScale:              2,
				QuantityScale:           3,
				SettlementCurrency:      currency,
				SettlementCurrencyScale: scale,
				InitialMarginRate:       "0.1",
				MaintenanceMarginRate:   "0.05",
				MaxLeverage:             "10",
				MakerFeeRate:            "0",
				TakerFeeRate:            "0",
			},
		}
	}
	state, _, _, _ = applyStoredTrading(
		t,
		pool,
		store,
		state,
		ids,
		clock,
		instrument("BTC-PERP", 1, "USDC", 2),
		platformpostgres.ApplyOptions{},
	)
	state, _, _, _ = applyStoredTrading(
		t,
		pool,
		store,
		state,
		ids,
		clock,
		engine.TradingAction{
			Kind: engine.TradingActionAdjustBalance,
			AdjustBalance: &engine.AdjustBalance{
				AccountID:     "account-1",
				Currency:      "USDC",
				CurrencyScale: 2,
				Operation:     engine.BalanceOperationSet,
				Amount:        "10",
			},
		},
		platformpostgres.ApplyOptions{},
	)
	state, _, _, _ = applyStoredTrading(
		t,
		pool,
		store,
		state,
		ids,
		clock,
		instrument("BTC-PERP", 2, "EUR", 2),
		platformpostgres.ApplyOptions{},
	)
	conflictingAction := instrument("ETH-PERP", 1, "USDC", 8)
	var (
		conflictingDecision engine.Decision
		conflictingInput    engine.InputEnvelope
	)
	state, conflictingDecision, conflictingInput, _ = applyStoredTrading(
		t,
		pool,
		store,
		state,
		ids,
		clock,
		conflictingAction,
		platformpostgres.ApplyOptions{},
	)
	if conflictingDecision.CommandResult.Status != engine.CommandStatusRejected ||
		conflictingDecision.CommandResult.Reason != engine.RejectionInvalidInstrument ||
		len(conflictingDecision.InstrumentChanges) != 0 ||
		len(conflictingDecision.BalanceChanges) != 0 ||
		len(conflictingDecision.LedgerChanges) != 0 {
		t.Fatalf("conflicting currency-scale decision = %+v", conflictingDecision)
	}
	assertRowCount(t, pool, "trading.instruments", 1)

	duplicateInput := conflictingInput
	duplicateInput.StreamSequence = state.NextStreamSequence()
	duplicateState, duplicateDecision, duplicate, err := store.ApplyTrading(
		ctx,
		state,
		duplicateInput,
		conflictingAction,
		platformpostgres.ApplyOptions{},
	)
	if err != nil || !duplicate {
		t.Fatalf(
			"conflicting scale duplicate = duplicate %t error %v",
			duplicate,
			err,
		)
	}
	if len(duplicateDecision.InstrumentChanges) != 0 ||
		len(duplicateDecision.BalanceChanges) != 0 ||
		len(duplicateDecision.LedgerChanges) != 0 {
		t.Fatalf("conflicting scale duplicate effects = %+v", duplicateDecision)
	}
	assertRowCount(t, pool, "trading.instruments", 1)

	recovered, err := store.RecoverTradingState(ctx, 8)
	if err != nil {
		t.Fatalf("recover currency-scale rejection: %v", err)
	}
	if recovered.Hash() != duplicateState.Hash() || !recovered.Ready() {
		t.Fatalf(
			"recovered scale rejection ready=%t hash=%s, want %s",
			recovered.Ready(),
			recovered.Hash(),
			duplicateState.Hash(),
		)
	}
	if report, err := store.ReconcileShard(ctx, 8); err != nil || !report.Ready {
		t.Fatalf("currency-scale rejection reconciliation = %+v, error %v", report, err)
	}

	restartConflictAction := instrument("SOL-PERP", 1, "USDC", 8)
	restartConflictInput := nextStoredInput(
		t,
		duplicateState,
		ids,
		clock,
		restartConflictAction,
	)
	liveConflictState, liveConflict, err := engine.ApplyTrading(
		duplicateState,
		restartConflictInput,
		restartConflictAction,
	)
	if err != nil {
		t.Fatalf("live post-reconfiguration conflict: %v", err)
	}
	restartedConflictState, restartedConflict, err := engine.ApplyTrading(
		recovered,
		restartConflictInput,
		restartConflictAction,
	)
	if err != nil {
		t.Fatalf("restarted post-reconfiguration conflict: %v", err)
	}
	if liveConflict.CommandResult.Status != engine.CommandStatusRejected ||
		liveConflict.CommandResult.Reason != engine.RejectionInvalidInstrument ||
		restartedConflict.CommandResult != liveConflict.CommandResult ||
		restartedConflict.DecisionHash != liveConflict.DecisionHash ||
		restartedConflictState.Hash() != liveConflictState.Hash() ||
		len(liveConflict.InstrumentChanges) != 0 ||
		len(liveConflict.BalanceChanges) != 0 ||
		len(liveConflict.LedgerChanges) != 0 ||
		len(liveConflict.Events) != 0 ||
		len(restartedConflict.InstrumentChanges) != 0 ||
		len(restartedConflict.BalanceChanges) != 0 ||
		len(restartedConflict.LedgerChanges) != 0 ||
		len(restartedConflict.Events) != 0 {
		t.Fatalf(
			"post-restart currency conflict live=%+v restarted=%+v",
			liveConflict,
			restartedConflict,
		)
	}

	finalState, sameScale, _, _ := applyStoredTrading(
		t,
		pool,
		store,
		duplicateState,
		ids,
		clock,
		instrument("ETH-PERP", 1, "USDC", 2),
		platformpostgres.ApplyOptions{},
	)
	if sameScale.CommandResult.Status != engine.CommandStatusAccepted ||
		len(sameScale.InstrumentChanges) != 1 {
		t.Fatalf("same currency-scale decision = %+v", sameScale)
	}
	assertRowCount(t, pool, "trading.instruments", 2)
	recoveredFinal, err := store.RecoverTradingState(ctx, 8)
	if err != nil {
		t.Fatalf("recover same-scale instruments: %v", err)
	}
	if recoveredFinal.Hash() != finalState.Hash() || !recoveredFinal.Ready() {
		t.Fatalf(
			"recovered same-scale ready=%t hash=%s, want %s",
			recoveredFinal.Ready(),
			recoveredFinal.Hash(),
			finalState.Hash(),
		)
	}
	if report, err := store.ReconcileShard(ctx, 8); err != nil || !report.Ready {
		t.Fatalf("same-scale reconciliation = %+v, error %v", report, err)
	}

	if _, err := pool.Exec(ctx, `
		UPDATE trading.currency_scales
		   SET scale = 8
		 WHERE currency = 'USDC'`); err != nil {
		t.Fatalf("corrupt retained currency scale: %v", err)
	}
	if _, err := store.RecoverTradingState(ctx, 8); !errors.Is(
		err,
		platformpostgres.ErrCheckpointMismatch,
	) {
		t.Fatalf("corrupt registry recovery error = %v, want checkpoint mismatch", err)
	}
	report, err := store.ReconcileShard(ctx, 8)
	if !errors.Is(err, platformpostgres.ErrReconciliationMismatch) ||
		report.Ready ||
		report.ConfigurationMismatchCount != 1 {
		t.Fatalf(
			"corrupt registry reconciliation = %+v, error %v",
			report,
			err,
		)
	}
	var checkpointReady bool
	if err := pool.QueryRow(ctx, `
		SELECT ready
		  FROM engine.shard_checkpoints
		 WHERE shard_id = 8`).Scan(&checkpointReady); err != nil ||
		checkpointReady {
		t.Fatalf(
			"currency registry mismatch checkpoint ready=%t error=%v",
			checkpointReady,
			err,
		)
	}
}

func assertLedgerRowCounts(
	t *testing.T,
	pool *pgxpool.Pool,
	transactions int,
	entries int,
) {
	t.Helper()
	var (
		actualTransactions int
		actualEntries      int
	)
	if err := pool.QueryRow(context.Background(), `
		SELECT
			(SELECT count(*) FROM ledger.transactions),
			(SELECT count(*) FROM ledger.entries)`,
	).Scan(&actualTransactions, &actualEntries); err != nil {
		t.Fatalf("count ledger rows: %v", err)
	}
	if actualTransactions != transactions || actualEntries != entries {
		t.Fatalf(
			"ledger rows = transactions %d entries %d, want %d/%d",
			actualTransactions,
			actualEntries,
			transactions,
			entries,
		)
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
	requestHash := sha256.Sum256(input.Payload.Bytes())
	if _, err := pool.Exec(context.Background(), `
		WITH idempotency AS (
			INSERT INTO trading.idempotency_records (
				scope, idempotency_key, request_hash, command_id, state, expires_at
			) VALUES ($1,$2,$3,$4,'in_progress',$5)
			ON CONFLICT (scope, idempotency_key) DO NOTHING
			RETURNING command_id
		)
		INSERT INTO trading.commands (
			command_id, account_id, account_sequence, command_type,
			schema_version, canonical_payload, status, logical_time
		)
		SELECT command_id,$6,$7,$8,$9,$10,'pending',$11
		  FROM idempotency
		ON CONFLICT (command_id) DO NOTHING`,
		"test:"+accountID,
		input.InputID.String(),
		requestHash[:],
		input.InputID.String(),
		time.Unix(0, input.LogicalTime.UnixNano()).UTC().Add(24*time.Hour),
		accountID,
		input.SourceSequence,
		string(action.Kind),
		input.SchemaVersion,
		input.Payload.Bytes(),
		input.LogicalTime.UnixNano(),
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

func corruptAsReplicationAuthority(
	t *testing.T,
	pool *pgxpool.Pool,
	statement string,
	arguments ...any,
) {
	t.Helper()
	connection, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatalf("acquire corruption connection: %v", err)
	}
	defer connection.Release()
	if _, err := connection.Exec(
		context.Background(),
		"SET session_replication_role = replica",
	); err != nil {
		t.Fatalf("enable corruption authority: %v", err)
	}
	defer func() {
		if _, resetErr := connection.Exec(
			context.Background(),
			"SET session_replication_role = origin",
		); resetErr != nil {
			t.Fatalf("restore replication authority: %v", resetErr)
		}
	}()
	if _, err := connection.Exec(
		context.Background(),
		statement,
		arguments...,
	); err != nil {
		t.Fatalf("apply corruption: %v", err)
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
	case "trading.instruments":
		query = "SELECT count(*) FROM trading.instruments"
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

func uint32Pointer(value uint32) *uint32 {
	return &value
}
