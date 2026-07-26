package postgres_test

import (
	"context"
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
//	source: apps/app/tests/it/trading/e2e_fills.rs:1029
//	test: rejected_order_persists_reason
//
// Adaptations:
//   - The accepted Go engine's working state replaces the legacy pending
//     mirror-row state after admission.
//   - Deterministic stop-order activation and the exact slippage_exceeded
//     classification replace the unrestricted legacy mark_rejected(reason)
//     persistence helper.
//   - Engine recovery plus a later ordered market input replaces a direct
//     second mark_rejected call; current Go exposes no arbitrary reason writer.
//
// Assertions preserved:
//   - The admitted order has no rejection reason before rejection.
//   - Rejection makes the order terminal and persists the exact reason.
//   - A later rejection opportunity cannot re-reject the terminal order,
//     or rewrite its reason.
//
// Strengthening:
//   - Restart recovery must reproduce the ready canonical engine state before
//     terminal no-rewrite behavior is checked.
//   - The durable order version must also remain unchanged after the later
//     market input.
//   - The frozen external order contract remains unchanged.
func TestRejectedOrderPersistsReason(t *testing.T) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := platformpostgres.NewMigrator(
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).MigrateAndProvision(ctx, 8); err != nil {
		t.Fatalf("migrate order rejection database: %v", err)
	}

	store := platformpostgres.NewEngineStore(pool)
	state := engine.NewState(8)
	ids := testkit.NewShardIDSequence(8)
	clock := testkit.NewManualClock(
		engine.NewLogicalTime(time.Date(2026, time.July, 26, 12, 0, 0, 0, time.UTC)),
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
			AccountID: "account-rejection",
			OmsMode:   engine.OmsModeNetting,
		},
	})
	apply(engine.TradingAction{
		Kind: engine.TradingActionAdjustBalance,
		AdjustBalance: &engine.AdjustBalance{
			AccountID:     "account-rejection",
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

	maxSlippageBPS := uint32(50)
	orderID := engine.IDFromSequence(engine.ID{}, 1029)
	submitted := apply(engine.TradingAction{
		Kind: engine.TradingActionSubmitOrder,
		SubmitOrder: &engine.SubmitOrder{
			OrderID:        orderID,
			AccountID:      "account-rejection",
			InstrumentID:   "BTC-PERP",
			Side:           engine.SideBuy,
			Type:           engine.OrderTypeStopMarket,
			TimeInForce:    engine.TimeInForceGTC,
			Quantity:       "1",
			TriggerPrice:   "110",
			MaxSlippageBPS: &maxSlippageBPS,
		},
	})
	if submitted.CommandResult.Status != engine.CommandStatusAccepted {
		t.Fatalf("submit result = %+v, want accepted pending order", submitted.CommandResult)
	}

	before := singlePersistedOrder(t, pool, orderID)
	if before.status != string(engine.OrderStatusWorking) || before.rejectReason != "" {
		t.Fatalf("before rejection = %#v, want working without reason", before)
	}

	rejected := apply(engine.TradingAction{
		Kind: engine.TradingActionUpdateBook,
		UpdateBook: &engine.UpdateBook{
			InstrumentID: "BTC-PERP",
			MarkPrice:    "200",
			Bids:         []engine.BookLevel{{Price: "109", Quantity: "10"}},
			Asks:         []engine.BookLevel{{Price: "110", Quantity: "10"}},
		},
	})
	if len(rejected.OrderChanges) != 1 ||
		rejected.OrderChanges[0].Status != engine.OrderStatusRejected ||
		rejected.OrderChanges[0].RejectReason != engine.RejectionSlippageExceeded {
		t.Fatalf("rejection decision = %+v", rejected)
	}
	after := singlePersistedOrder(t, pool, orderID)
	if after.status != string(engine.OrderStatusRejected) ||
		after.rejectReason != string(engine.RejectionSlippageExceeded) {
		t.Fatalf("after rejection = %#v", after)
	}

	var persistedReason string
	var persistedVersion uint64
	if err := pool.QueryRow(ctx, `
		SELECT reject_reason, version
		  FROM trading.orders
		 WHERE order_id = $1`,
		orderID.String(),
	).Scan(&persistedReason, &persistedVersion); err != nil {
		t.Fatalf("read persisted rejection: %v", err)
	}
	recoveredStore := platformpostgres.NewEngineStore(pool)
	recoveredState, err := recoveredStore.RecoverTradingState(ctx, 8)
	if err != nil {
		t.Fatalf("recover trading state: %v", err)
	}
	if !recoveredState.Ready() || recoveredState.Hash() != state.Hash() {
		t.Fatalf(
			"recovered state ready=%t hash=%s, want ready hash=%s",
			recoveredState.Ready(),
			recoveredState.Hash(),
			state.Hash(),
		)
	}
	state = recoveredState
	store = recoveredStore

	replayedMarket := apply(engine.TradingAction{
		Kind: engine.TradingActionUpdateBook,
		UpdateBook: &engine.UpdateBook{
			InstrumentID: "BTC-PERP",
			MarkPrice:    "201",
			Bids:         []engine.BookLevel{{Price: "109", Quantity: "10"}},
			Asks:         []engine.BookLevel{{Price: "110", Quantity: "10"}},
		},
	})
	if len(replayedMarket.OrderChanges) != 0 {
		t.Fatalf("terminal order changed again: %+v", replayedMarket.OrderChanges)
	}
	still := singlePersistedOrder(t, pool, orderID)
	if still.rejectReason != persistedReason {
		t.Fatalf("repeated market input changed rejection = %#v", still)
	}
	var stillReason string
	var stillVersion uint64
	if err := pool.QueryRow(ctx, `
		SELECT reject_reason, version
		  FROM trading.orders
		 WHERE order_id = $1`,
		orderID.String(),
	).Scan(&stillReason, &stillVersion); err != nil {
		t.Fatalf("read stable rejection: %v", err)
	}
	if stillReason != persistedReason || stillVersion != persistedVersion {
		t.Fatalf(
			"durable rejection changed from (%q,%d) to (%q,%d)",
			persistedReason,
			persistedVersion,
			stillReason,
			stillVersion,
		)
	}
}

type persistedOrder struct {
	status       string
	rejectReason string
}

func singlePersistedOrder(
	t *testing.T,
	pool *pgxpool.Pool,
	orderID engine.ID,
) persistedOrder {
	t.Helper()
	var order persistedOrder
	if err := pool.QueryRow(context.Background(), `
		SELECT status, COALESCE(reject_reason, '')
		  FROM trading.orders
		 WHERE order_id = $1`,
		orderID.String(),
	).Scan(&order.status, &order.rejectReason); err != nil {
		t.Fatalf("read persisted order: %v", err)
	}
	return order
}
