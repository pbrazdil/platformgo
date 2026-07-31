package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
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
//	source: apps/app/tests/it/trading/e2e_fills.rs:952
//	test: fill_surfaces_frozen_effective_leverage
//
// Adaptations:
//   - Direct writes to the legacy fill mirror are replaced by deterministic
//     executions through the real EngineStore and the PostgreSQL compatibility
//     reader.
//   - A historical pre-v4 row is inserted only to preserve the source's
//     nullable-fill assertion; current engine executions must always freeze a
//     canonical positive leverage.
//   - Same-sequence replay, later-sequence duplicate delivery, fresh-owner
//     recovery, and reconciliation strengthen the source assertion at the
//     durable exactly-once boundary.
//
// Assertions preserved:
//   - Frozen leverage 10 surfaces as exactly 10.
//   - Frozen leverage stored from 5.00 surfaces canonically as 5.
//   - A historical fill with no frozen leverage surfaces no leverage.
//
// Strengthening:
//   - A risk-config change after the first execution does not rewrite its
//     frozen leverage, while the next execution freezes the new value.
//   - Decision, immutable receipt, fill row, compatibility read model, replay,
//     recovery, and reconciliation agree on the frozen execution-time value.
func TestFillSurfacesFrozenEffectiveLeverage(t *testing.T) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)

	const shardID engine.ShardID = 63
	if err := newCurrentTestMigrator(
		t,
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).MigrateAndProvision(ctx, shardID); err != nil {
		t.Fatalf("migrate frozen leverage database: %v", err)
	}

	store := platformpostgres.NewEngineStore(pool)
	ownership, err := store.AcquireShardOwnership(ctx, shardID)
	if err != nil {
		t.Fatalf("acquire initial shard ownership: %v", err)
	}
	t.Cleanup(func() {
		if ownership != nil {
			_ = ownership.Close(context.Background())
		}
	})
	options := func() platformpostgres.ApplyOptions {
		return platformpostgres.ApplyOptions{Ownership: ownership}
	}

	state := engine.NewState(shardID)
	ids := testkit.NewShardIDSequence(shardID)
	clock := testkit.NewManualClock(engine.NewLogicalTime(
		time.Date(2026, time.July, 26, 20, 0, 0, 123456789, time.UTC),
	))
	apply := func(action engine.TradingAction) (engine.Decision, engine.InputEnvelope) {
		t.Helper()
		var (
			decision engine.Decision
			input    engine.InputEnvelope
		)
		state, decision, input, _ = applyStoredTrading(
			t,
			pool,
			store,
			state,
			ids,
			clock,
			action,
			options(),
		)
		return decision, input
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
	const accountID = "urn:xb:account:frozen-effective-leverage"
	apply(engine.TradingAction{
		Kind: engine.TradingActionConfigureAccount,
		ConfigureAccount: &engine.ConfigureAccount{
			AccountID: accountID,
			OmsMode:   engine.OmsModeNetting,
		},
	})
	apply(engine.TradingAction{
		Kind: engine.TradingActionAdjustBalance,
		AdjustBalance: &engine.AdjustBalance{
			AccountID:     accountID,
			Currency:      "USDC",
			CurrencyScale: 2,
			Operation:     engine.BalanceOperationDeposit,
			Amount:        "1000",
		},
	})
	apply(engine.TradingAction{
		Kind: engine.TradingActionConfigureRisk,
		ConfigureRisk: &engine.ConfigureRisk{
			AccountID:    accountID,
			InstrumentID: "BTC-PERP",
			MarginMode:   engine.MarginModeCross,
			Leverage:     "10",
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

	firstOrderID := engine.IDFromSequence(engine.ID{}, 6301)
	firstOrder := engine.TradingAction{
		Kind: engine.TradingActionSubmitOrder,
		SubmitOrder: &engine.SubmitOrder{
			OrderID:      firstOrderID,
			AccountID:    accountID,
			InstrumentID: "BTC-PERP",
			Side:         engine.SideBuy,
			Type:         engine.OrderTypeMarket,
			TimeInForce:  engine.TimeInForceIOC,
			Quantity:     "1",
		},
	}
	firstDecision, firstInput := apply(firstOrder)
	firstFill := requireFrozenLeverageFill(t, firstDecision, "10")
	assertPersistedFrozenLeverage(
		t,
		pool,
		firstInput.InputID,
		firstFill.FillID,
		"10",
	)

	sameState, sameDecision, duplicate, err := store.ApplyTrading(
		ctx,
		state,
		firstInput,
		firstOrder,
		options(),
	)
	if err != nil {
		t.Fatalf("same-sequence fill replay: %v", err)
	}
	if !duplicate ||
		sameState.Hash() != state.Hash() ||
		sameState.NextStreamSequence() != state.NextStreamSequence() ||
		sameDecision.DecisionHash != firstDecision.DecisionHash {
		t.Fatalf(
			"same-sequence fill replay duplicate=%t hash=%s next=%d decision=%s",
			duplicate,
			sameState.Hash(),
			sameState.NextStreamSequence(),
			sameDecision.DecisionHash,
		)
	}
	requireFrozenLeverageFill(t, sameDecision, "10")
	assertRowCount(t, pool, "trading.fills", 1)

	republishedInput := firstInput
	republishedInput.StreamSequence = state.NextStreamSequence()
	republishedState, republishedDecision, duplicate, err := store.ApplyTrading(
		ctx,
		state,
		republishedInput,
		firstOrder,
		options(),
	)
	if err != nil {
		t.Fatalf("later-sequence fill replay: %v", err)
	}
	if !duplicate ||
		republishedDecision.DuplicateOfDecisionHash != firstDecision.DecisionHash ||
		republishedDecision.StreamSequence != republishedInput.StreamSequence ||
		republishedState.NextStreamSequence() != republishedInput.StreamSequence+1 ||
		republishedState.Hash() == state.Hash() ||
		len(republishedDecision.Fills) != 0 {
		t.Fatalf(
			"later-sequence fill replay duplicate=%t duplicateOf=%s fills=%d next=%d",
			duplicate,
			republishedDecision.DuplicateOfDecisionHash,
			len(republishedDecision.Fills),
			republishedState.NextStreamSequence(),
		)
	}
	state = republishedState
	assertRowCount(t, pool, "trading.fills", 1)

	closeOrderID := engine.IDFromSequence(engine.ID{}, 6302)
	closeDecision, _ := apply(engine.TradingAction{
		Kind: engine.TradingActionSubmitOrder,
		SubmitOrder: &engine.SubmitOrder{
			OrderID:      closeOrderID,
			AccountID:    accountID,
			InstrumentID: "BTC-PERP",
			Side:         engine.SideSell,
			Type:         engine.OrderTypeMarket,
			TimeInForce:  engine.TimeInForceIOC,
			Quantity:     "1",
			ReduceOnly:   true,
			PositionID:   firstFill.PositionID,
		},
	})
	requireFrozenLeverageFill(t, closeDecision, "10")

	lowerMaximumDecision, _ := apply(engine.TradingAction{
		Kind: engine.TradingActionConfigureInstrument,
		ConfigureInstrument: &engine.ConfigureInstrument{
			InstrumentID:            "BTC-PERP",
			Revision:                2,
			PriceScale:              2,
			QuantityScale:           3,
			SettlementCurrency:      "USDC",
			SettlementCurrencyScale: 2,
			InitialMarginRate:       "0.1",
			MaintenanceMarginRate:   "0.05",
			MaxLeverage:             "5",
			MakerFeeRate:            "0",
			TakerFeeRate:            "0",
		},
	})
	if lowerMaximumDecision.CommandResult.Status != engine.CommandStatusRejected ||
		lowerMaximumDecision.CommandResult.Reason !=
			engine.RejectionRiskConfigLocked ||
		len(lowerMaximumDecision.InstrumentChanges) != 0 {
		t.Fatalf(
			"lower maximum with explicit risk = result %+v changes %+v, want risk-config-locked rejection",
			lowerMaximumDecision.CommandResult,
			lowerMaximumDecision.InstrumentChanges,
		)
	}

	riskDecision, _ := apply(engine.TradingAction{
		Kind: engine.TradingActionConfigureRisk,
		ConfigureRisk: &engine.ConfigureRisk{
			AccountID:    accountID,
			InstrumentID: "BTC-PERP",
			MarginMode:   engine.MarginModeCross,
			Leverage:     "5.00",
		},
	})
	if riskDecision.CommandResult.Status != engine.CommandStatusAccepted ||
		len(riskDecision.RiskChanges) != 1 ||
		riskDecision.RiskChanges[0].Leverage != "5" {
		t.Fatalf(
			"canonical risk change = result %+v changes %+v, want accepted leverage 5",
			riskDecision.CommandResult,
			riskDecision.RiskChanges,
		)
	}

	secondOrderID := engine.IDFromSequence(engine.ID{}, 6303)
	secondDecision, secondInput := apply(engine.TradingAction{
		Kind: engine.TradingActionSubmitOrder,
		SubmitOrder: &engine.SubmitOrder{
			OrderID:      secondOrderID,
			AccountID:    accountID,
			InstrumentID: "BTC-PERP",
			Side:         engine.SideBuy,
			Type:         engine.OrderTypeMarket,
			TimeInForce:  engine.TimeInForceIOC,
			Quantity:     "1",
		},
	})
	secondFill := requireFrozenLeverageFill(t, secondDecision, "5")
	assertPersistedFrozenLeverage(
		t,
		pool,
		secondInput.InputID,
		secondFill.FillID,
		"5",
	)
	assertPersistedFrozenLeverage(
		t,
		pool,
		firstInput.InputID,
		firstFill.FillID,
		"10",
	)

	if err := ownership.Close(ctx); err != nil {
		t.Fatalf("close initial shard ownership: %v", err)
	}
	ownership = nil

	freshStore := platformpostgres.NewEngineStore(pool)
	freshOwnership, err := freshStore.AcquireShardOwnership(ctx, shardID)
	if err != nil {
		t.Fatalf("acquire fresh shard ownership: %v", err)
	}
	recovered, err := freshStore.RecoverTradingState(ctx, shardID)
	if err != nil {
		_ = freshOwnership.Close(context.Background())
		t.Fatalf("recover frozen leverage state: %v", err)
	}
	if !recovered.Ready() ||
		recovered.Hash() != state.Hash() ||
		recovered.NextStreamSequence() != state.NextStreamSequence() {
		_ = freshOwnership.Close(context.Background())
		t.Fatalf(
			"recovered frozen leverage ready=%t hash=%s next=%d, want true/%s/%d",
			recovered.Ready(),
			recovered.Hash(),
			recovered.NextStreamSequence(),
			state.Hash(),
			state.NextStreamSequence(),
		)
	}
	recoveredFirst := recovered.FillsForOrder(firstOrderID)
	recoveredSecond := recovered.FillsForOrder(secondOrderID)
	if len(recoveredFirst) != 1 ||
		recoveredFirst[0].EffectiveLeverage != "10" ||
		len(recoveredSecond) != 1 ||
		recoveredSecond[0].EffectiveLeverage != "5" {
		_ = freshOwnership.Close(context.Background())
		t.Fatalf(
			"recovered frozen leverage first=%+v second=%+v",
			recoveredFirst,
			recoveredSecond,
		)
	}
	if err := freshOwnership.Close(ctx); err != nil {
		t.Fatalf("close fresh shard ownership: %v", err)
	}

	report, err := freshStore.ReconcileShard(ctx, shardID)
	if err != nil {
		t.Fatalf("reconcile frozen leverage shard: %v", err)
	}
	if !report.Ready ||
		report.DuplicateDeliveryCount != 1 ||
		report.NextStreamSequence != state.NextStreamSequence() ||
		report.DeliveryMismatchCount != 0 ||
		report.OrderFillMismatchCount != 0 ||
		report.ConfigurationMismatchCount != 0 {
		t.Fatalf("frozen leverage reconciliation = %+v", report)
	}

	if _, err := pool.Exec(
		ctx,
		"ALTER TABLE trading.fills DISABLE TRIGGER USER",
	); err != nil {
		t.Fatalf("disable immutable fill trigger for corruption proof: %v", err)
	}
	defer func() {
		_, _ = pool.Exec(
			context.Background(),
			"ALTER TABLE trading.fills ENABLE TRIGGER USER",
		)
	}()
	if _, err := pool.Exec(
		ctx,
		"ALTER TABLE engine.input_receipts DISABLE TRIGGER USER",
	); err != nil {
		t.Fatalf("disable immutable receipt trigger for corruption proof: %v", err)
	}
	defer func() {
		_, _ = pool.Exec(
			context.Background(),
			"ALTER TABLE engine.input_receipts ENABLE TRIGGER USER",
		)
	}()
	if _, err := pool.Exec(ctx, `
		UPDATE engine.input_receipts
		   SET decision = jsonb_set(
		       decision,
		       '{Fills,0,EffectiveLeverage}',
		       to_jsonb('9'::text),
		       false
		   )
		 WHERE input_id = $1`,
		firstInput.InputID.String(),
	); err != nil {
		t.Fatalf("corrupt frozen leverage receipt: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE trading.fills
		   SET effective_leverage = 9
		 WHERE fill_id = $1`,
		firstFill.FillID.String(),
	); err != nil {
		t.Fatalf("corrupt frozen leverage projection: %v", err)
	}
	if _, err := pool.Exec(
		ctx,
		"ALTER TABLE trading.fills ENABLE TRIGGER USER",
	); err != nil {
		t.Fatalf("reenable immutable fill trigger after corruption: %v", err)
	}
	if _, err := pool.Exec(
		ctx,
		"ALTER TABLE engine.input_receipts ENABLE TRIGGER USER",
	); err != nil {
		t.Fatalf("reenable immutable receipt trigger after corruption: %v", err)
	}
	if _, err := freshStore.RecoverTradingState(ctx, shardID); !errors.Is(
		err,
		platformpostgres.ErrCheckpointMismatch,
	) {
		t.Fatalf(
			"correlated receipt and fill corruption recovery error = %v, want checkpoint mismatch",
			err,
		)
	}

	if _, err := pool.Exec(
		ctx,
		"ALTER TABLE engine.input_receipts DISABLE TRIGGER USER",
	); err != nil {
		t.Fatalf("disable immutable receipt trigger for repair: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE engine.input_receipts
		   SET decision = jsonb_set(
		       decision,
		       '{Fills,0,EffectiveLeverage}',
		       to_jsonb('10'::text),
		       false
		   )
		 WHERE input_id = $1`,
		firstInput.InputID.String(),
	); err != nil {
		t.Fatalf("repair frozen leverage receipt: %v", err)
	}
	if _, err := pool.Exec(
		ctx,
		"ALTER TABLE engine.input_receipts ENABLE TRIGGER USER",
	); err != nil {
		t.Fatalf("reenable immutable receipt trigger after repair: %v", err)
	}
	corruptReport, err := freshStore.ReconcileShard(ctx, shardID)
	if !errors.Is(err, platformpostgres.ErrReconciliationMismatch) ||
		corruptReport.Ready ||
		corruptReport.OrderFillMismatchCount != 1 {
		t.Fatalf(
			"corrupted leverage reconciliation = %+v error %v, want one fill mismatch",
			corruptReport,
			err,
		)
	}

	if _, err := pool.Exec(
		ctx,
		"ALTER TABLE trading.fills DISABLE TRIGGER USER",
	); err != nil {
		t.Fatalf("disable immutable fill trigger for repair: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE trading.fills
		   SET effective_leverage = 10
		 WHERE fill_id = $1`,
		firstFill.FillID.String(),
	); err != nil {
		t.Fatalf("repair frozen leverage projection: %v", err)
	}
	if _, err := pool.Exec(
		ctx,
		"ALTER TABLE trading.fills ENABLE TRIGGER USER",
	); err != nil {
		t.Fatalf("reenable immutable fill trigger after repair: %v", err)
	}
	repairedReport, err := freshStore.ReconcileShard(ctx, shardID)
	if err != nil {
		t.Fatalf("reconcile repaired frozen leverage: %v", err)
	}
	if err != nil ||
		repairedReport.Ready ||
		repairedReport.OrderFillMismatchCount != 0 {
		t.Fatalf(
			"repaired frozen leverage reconciliation = %+v error %v, want mismatch cleared with readiness still latched false",
			repairedReport,
			err,
		)
	}

	legacyFillID := engine.IDFromSequence(engine.ID{}, 6399)
	if _, err := pool.Exec(ctx, `
		INSERT INTO trading.fills (
			fill_id, order_id, input_id, account_id, instrument_id,
			side, price, quantity, position_id, position_effect,
			realized_pnl, settlement_currency, liquidity_side,
			fee, fee_currency, logical_time, effective_leverage
		) VALUES (
			$1, $2, $3, $4, 'BTC-PERP',
			'BUY', 100, 1, $5, 'open',
			NULL, NULL, 'TAKER',
			NULL, NULL, 0, NULL
		)`,
		legacyFillID.String(),
		firstOrderID.String(),
		engine.IDFromSequence(engine.ID{}, 6398).String(),
		accountID,
		engine.IDFromSequence(engine.ID{}, 6397).String(),
	); err != nil {
		t.Fatalf("insert nullable pre-v4 fill: %v", err)
	}

	apiPool := runtimeRoleLoginPool(
		t,
		pool,
		"platformgo_frozen_leverage_api_login",
		"platformgo_api",
	)
	page, err := platformpostgres.NewCompatibilityStore(apiPool).
		FilterFillExecutions(
			ctx,
			accountID,
			platformpostgres.FillExecutionFilter{Limit: 10},
		)
	if err != nil {
		t.Fatalf("read frozen leverage fills: %v", err)
	}
	byID := make(map[string]*string, len(page.Items))
	for _, fill := range page.Items {
		byID[fill.FillID] = fill.Leverage
	}
	requireReadLeverage(t, byID, firstFill.FillID.String(), "10")
	requireReadLeverage(t, byID, secondFill.FillID.String(), "5")
	if leverage, ok := byID[legacyFillID.String()]; !ok {
		t.Fatalf("nullable pre-v4 fill %s absent from read model", legacyFillID)
	} else if leverage != nil {
		t.Fatalf(
			"nullable pre-v4 fill leverage = %q, want absent",
			*leverage,
		)
	}
	for _, fill := range page.Items {
		if fill.FillID != legacyFillID.String() {
			continue
		}
		encoded, err := json.Marshal(fill)
		if err != nil {
			t.Fatalf("encode nullable pre-v4 fill: %v", err)
		}
		var object map[string]json.RawMessage
		if err := json.Unmarshal(encoded, &object); err != nil {
			t.Fatalf("decode nullable pre-v4 fill JSON: %v", err)
		}
		if _, present := object["leverage"]; present {
			t.Fatalf(
				"nullable pre-v4 fill JSON contains leverage: %s",
				encoded,
			)
		}
		return
	}
	t.Fatalf("nullable pre-v4 fill %s absent from JSON proof", legacyFillID)
}

func requireFrozenLeverageFill(
	t *testing.T,
	decision engine.Decision,
	want string,
) engine.FillSnapshot {
	t.Helper()
	if decision.CommandResult.Status != engine.CommandStatusAccepted ||
		len(decision.Fills) != 1 {
		t.Fatalf(
			"fill decision = result %+v fills %+v, want one accepted fill",
			decision.CommandResult,
			decision.Fills,
		)
	}
	fill := decision.Fills[0]
	if fill.EffectiveLeverage != want {
		t.Fatalf(
			"decision fill %s effective leverage = %q, want %q",
			fill.FillID,
			fill.EffectiveLeverage,
			want,
		)
	}
	return fill
}

func assertPersistedFrozenLeverage(
	t *testing.T,
	pool *pgxpool.Pool,
	inputID engine.ID,
	fillID engine.ID,
	want string,
) {
	t.Helper()
	var (
		decisionJSON []byte
		fillLeverage string
	)
	if err := pool.QueryRow(context.Background(), `
		SELECT decision
		  FROM engine.input_receipts
		 WHERE input_id = $1`,
		inputID.String(),
	).Scan(&decisionJSON); err != nil {
		t.Fatalf("read persisted receipt %s: %v", inputID, err)
	}
	var persisted engine.Decision
	if err := json.Unmarshal(decisionJSON, &persisted); err != nil {
		t.Fatalf("decode persisted receipt %s: %v", inputID, err)
	}
	if len(persisted.Fills) != 1 ||
		persisted.Fills[0].FillID != fillID ||
		persisted.Fills[0].EffectiveLeverage != want {
		t.Fatalf(
			"persisted receipt fill = %+v, want %s leverage %q",
			persisted.Fills,
			fillID,
			want,
		)
	}
	if err := pool.QueryRow(context.Background(), `
		SELECT trim_scale(effective_leverage)::text
		  FROM trading.fills
		 WHERE fill_id = $1`,
		fillID.String(),
	).Scan(&fillLeverage); err != nil {
		t.Fatalf("read persisted fill %s: %v", fillID, err)
	}
	if fillLeverage != want {
		t.Fatalf(
			"persisted fill %s effective leverage = %q, want %q",
			fillID,
			fillLeverage,
			want,
		)
	}
}

func requireReadLeverage(
	t *testing.T,
	byID map[string]*string,
	fillID string,
	want string,
) {
	t.Helper()
	leverage, ok := byID[fillID]
	if !ok {
		t.Fatalf("fill %s absent from read model", fillID)
	}
	if leverage == nil || *leverage != want {
		t.Fatalf("fill %s read leverage = %v, want %q", fillID, leverage, want)
	}
}
