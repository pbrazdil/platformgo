package postgres_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
	"github.com/upcomers-org/platformgo/internal/engine"
	"github.com/upcomers-org/platformgo/testkit"
)

// Ported from:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/trading/e2e_balances.rs:83
//	test: a_working_order_locks_its_reserved_margin
//
// Adaptations:
//   - Current Go reserves initial margin plus the worst non-negative
//     prospective fee at the maximum authoritative price. The approved exact
//     source-vector result is therefore locked 45 and free 9955, rather than
//     the source's initial-margin-only 20 and 9980.
//   - One atomic deterministic submit creates the working order; there is no
//     separately committed submitted-to-working helper transition.
//   - The order and complete balance projection commit together in PostgreSQL.
//
// Assertions preserved:
//   - Before any working order, the account's USDC locked balance is zero.
//   - A source-shaped non-reduce-only GTC BUY LIMIT reserves exact margin and
//     reduces free balance by the same amount. Under the owner-approved
//     current-Go authority decision, the accepted exact result is locked 45
//     and free 9955 instead of the source's locked 20 and free 9980.
//
// Strengthening:
//   - Pre-commit rollback, exact retry, both duplicate-delivery paths, stable
//     decision hashes, restart recovery, reconciliation, exact cancel release,
//     and zero ledger effects are proved against the production store.
func TestAWorkingOrderLocksItsReservedMargin(t *testing.T) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	const shardID = engine.ShardID(9)
	if err := newCurrentTestMigrator(
		t,
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).MigrateAndProvision(ctx, shardID); err != nil {
		t.Fatalf("migrate working-order balance database: %v", err)
	}

	store := platformpostgres.NewEngineStore(pool)
	state := engine.NewState(shardID)
	ids := testkit.NewShardIDSequence(shardID)
	clock := testkit.NewManualClock(
		engine.NewLogicalTime(time.Date(2026, time.July, 28, 13, 0, 0, 0, time.UTC)),
	)
	nextInput := func(action engine.TradingAction) engine.InputEnvelope {
		t.Helper()
		sequence := state.NextStreamSequence()
		input, inputErr := (testkit.TradingInput{
			InputID:              ids.Next(),
			ShardID:              state.ShardID(),
			SourceID:             "working-order-balance-source-port",
			SourceSequence:       sequence,
			StreamSequence:       sequence,
			MarketSequence:       0,
			LogicalTime:          clock.Now(),
			ConfigurationVersion: 1,
			InstrumentVersion:    1,
			Action:               action,
		}).CanonicalEnvelope()
		if inputErr != nil {
			t.Fatalf("canonical input sequence %d: %v", sequence, inputErr)
		}
		if action.Kind == engine.TradingActionUpdateBook {
			input.Kind = engine.InputKindMarket
			input.MarketSequence = sequence
		}
		clock.Advance(time.Second)
		return input
	}
	apply := func(action engine.TradingAction) (engine.Decision, engine.InputEnvelope) {
		t.Helper()
		input := nextInput(action)
		seedPendingCommand(t, pool, input, action)
		next, decision, duplicate, applyErr := store.ApplyTrading(
			ctx,
			state,
			input,
			action,
			platformpostgres.ApplyOptions{},
		)
		if applyErr != nil || duplicate {
			t.Fatalf(
				"new input %s = duplicate %t error %v",
				input.InputID,
				duplicate,
				applyErr,
			)
		}
		state = next
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
			InitialMarginRate:       "0.02",
			MaintenanceMarginRate:   "0.01",
			MaxLeverage:             "50",
			MakerFeeRate:            "0.0002",
			TakerFeeRate:            "0.0005",
		},
	})
	const accountID = "urn:xb:account:working-order-balance"
	apply(engine.TradingAction{
		Kind: engine.TradingActionConfigureAccount,
		ConfigureAccount: &engine.ConfigureAccount{
			AccountID: accountID,
			OmsMode:   engine.OmsModeNetting,
		},
	})
	apply(engine.TradingAction{
		Kind: engine.TradingActionConfigureRisk,
		ConfigureRisk: &engine.ConfigureRisk{
			AccountID:    accountID,
			InstrumentID: "BTC-PERP",
			MarginMode:   engine.MarginModeCross,
			Leverage:     "50",
		},
	})
	apply(engine.TradingAction{
		Kind: engine.TradingActionAdjustBalance,
		AdjustBalance: &engine.AdjustBalance{
			AccountID:     accountID,
			Currency:      "USDC",
			CurrencyScale: 2,
			Operation:     engine.BalanceOperationDeposit,
			Amount:        "10000",
		},
	})
	_, bookInput := apply(engine.TradingAction{
		Kind: engine.TradingActionUpdateBook,
		UpdateBook: &engine.UpdateBook{
			InstrumentID: "BTC-PERP",
			MarkPrice:    "49999",
			Bids:         []engine.BookLevel{{Price: "49998", Quantity: "10"}},
			Asks:         []engine.BookLevel{{Price: "50001", Quantity: "10"}},
		},
	})
	if _, err := pool.Exec(ctx, `
		INSERT INTO identity.users (user_id, login, normalized_login)
		VALUES (
			'urn:xb:user:working-order-balance',
			'working-order-balance',
			'working-order-balance'
		)`,
	); err != nil {
		t.Fatalf("seed realtime user: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO identity.user_accounts (user_id, account_id)
		VALUES ('urn:xb:user:working-order-balance', $1)`,
		accountID,
	); err != nil {
		t.Fatalf("seed realtime subscriber: %v", err)
	}

	initialBalance := requireWorkingOrderBalance(
		t,
		state,
		accountID,
		"10000",
		"0",
		"10000",
		"10000",
	)
	assertPersistedBalanceMatches(t, pool, initialBalance)
	assertRowCount(t, pool, "ledger.transactions", 1)
	assertRowCount(t, pool, "ledger.entries", 2)

	orderID := ids.Next()
	submitAction := engine.TradingAction{
		Kind: engine.TradingActionSubmitOrder,
		SubmitOrder: &engine.SubmitOrder{
			OrderID:      orderID,
			AccountID:    accountID,
			InstrumentID: "BTC-PERP",
			Side:         engine.SideBuy,
			Type:         engine.OrderTypeLimit,
			TimeInForce:  engine.TimeInForceGTC,
			Quantity:     "1",
			Price:        "50000",
		},
	}
	submitInput := nextInput(submitAction)
	seedPendingCommand(t, pool, submitInput, submitAction)

	beforeSubmitHash := state.Hash()
	var (
		publicationsBeforeSubmit int
		channelsBeforeSubmit     int
		domainOutboxBeforeSubmit int
	)
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM realtime.publications),
			(SELECT count(*) FROM realtime.channel_sequences),
			(SELECT count(*) FROM messaging.outbox
			  WHERE producer_class = 'engine')`,
	).Scan(
		&publicationsBeforeSubmit,
		&channelsBeforeSubmit,
		&domainOutboxBeforeSubmit,
	); err != nil {
		t.Fatalf("read outbox baselines before submit: %v", err)
	}
	if publicationsBeforeSubmit != 0 ||
		channelsBeforeSubmit != 0 ||
		domainOutboxBeforeSubmit != 0 {
		t.Fatalf(
			"outbox baselines = publications %d channels %d domain %d, want zero",
			publicationsBeforeSubmit,
			channelsBeforeSubmit,
			domainOutboxBeforeSubmit,
		)
	}
	faultedState, _, duplicate, err := store.ApplyTrading(
		ctx,
		state,
		submitInput,
		submitAction,
		platformpostgres.ApplyOptions{
			Faults: testkit.NewFaults(
				platformpostgres.FailpointAfterPersistBeforeCommit,
			),
		},
	)
	if !errors.Is(err, platformpostgres.ErrInjectedFault) {
		t.Fatalf("faulted submit error = %v, want ErrInjectedFault", err)
	}
	if duplicate || faultedState.Hash() != beforeSubmitHash {
		t.Fatal("faulted submit escaped the PostgreSQL transaction")
	}
	assertRowCount(t, pool, "trading.orders", 0)
	assertRowCount(t, pool, "engine.input_receipts", 5)
	assertRowCount(t, pool, "ledger.transactions", 1)
	assertRowCount(t, pool, "ledger.entries", 2)
	assertPersistedBalanceMatches(t, pool, initialBalance)
	var (
		publicationsAfterFault int
		channelsAfterFault     int
		domainOutboxAfterFault int
	)
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM realtime.publications),
			(SELECT count(*) FROM realtime.channel_sequences),
			(SELECT count(*) FROM messaging.outbox
			  WHERE producer_class = 'engine')`,
	).Scan(
		&publicationsAfterFault,
		&channelsAfterFault,
		&domainOutboxAfterFault,
	); err != nil {
		t.Fatalf("read outbox state after fault: %v", err)
	}
	if publicationsAfterFault != publicationsBeforeSubmit ||
		channelsAfterFault != channelsBeforeSubmit ||
		domainOutboxAfterFault != domainOutboxBeforeSubmit {
		t.Fatalf(
			"faulted outbox = publications %d/%d channels %d/%d domain %d/%d",
			publicationsAfterFault,
			publicationsBeforeSubmit,
			channelsAfterFault,
			channelsBeforeSubmit,
			domainOutboxAfterFault,
			domainOutboxBeforeSubmit,
		)
	}

	state, submitted, duplicate, err := store.ApplyTrading(
		ctx,
		state,
		submitInput,
		submitAction,
		platformpostgres.ApplyOptions{},
	)
	if err != nil || duplicate ||
		submitted.CommandResult.Status != engine.CommandStatusAccepted {
		t.Fatalf(
			"retried submit = status %s duplicate %t error %v",
			submitted.CommandResult.Status,
			duplicate,
			err,
		)
	}
	if submitted.MarketSequence != bookInput.MarketSequence ||
		len(submitted.BalanceChanges) != 1 ||
		len(submitted.LedgerChanges) != 0 ||
		len(submitted.OrderChanges) != 1 ||
		submitted.OrderChanges[0].Status != engine.OrderStatusWorking {
		t.Fatalf("working-order decision = %+v", submitted)
	}
	workingBalance := requireWorkingOrderBalance(
		t,
		state,
		accountID,
		"10000",
		"45",
		"9955",
		"10000",
	)
	if submitted.BalanceChanges[0] != workingBalance {
		t.Fatalf(
			"submitted balance = %#v, want %#v",
			submitted.BalanceChanges[0],
			workingBalance,
		)
	}
	assertPersistedBalanceMatches(t, pool, workingBalance)
	assertRowCount(t, pool, "trading.orders", 1)
	assertRowCount(t, pool, "ledger.transactions", 1)
	assertRowCount(t, pool, "ledger.entries", 2)
	var (
		submitRealtimeType     string
		submitRealtimeSequence uint64
		submitChannelSequence  uint64
		submitDomainOutbox     int
	)
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT event_type
			   FROM realtime.publications
			  WHERE channel = 'user:working-order-balance'),
			(SELECT sequence
			   FROM realtime.publications
			  WHERE channel = 'user:working-order-balance'),
			(SELECT last_sequence
			   FROM realtime.channel_sequences
			  WHERE channel = 'user:working-order-balance'),
			(SELECT count(*)
			   FROM messaging.outbox
			  WHERE producer_class = 'engine')`,
	).Scan(
		&submitRealtimeType,
		&submitRealtimeSequence,
		&submitChannelSequence,
		&submitDomainOutbox,
	); err != nil {
		t.Fatalf("read committed submit outboxes: %v", err)
	}
	if submitRealtimeType != "order.created" ||
		submitRealtimeSequence != 1 ||
		submitChannelSequence != 1 ||
		submitDomainOutbox != 1 {
		t.Fatalf(
			"submit outboxes = realtime %s/%d channel %d domain %d",
			submitRealtimeType,
			submitRealtimeSequence,
			submitChannelSequence,
			submitDomainOutbox,
		)
	}

	workingHash := state.Hash()
	sameState, sameDecision, duplicate, err := store.ApplyTrading(
		ctx,
		state,
		submitInput,
		submitAction,
		platformpostgres.ApplyOptions{},
	)
	if err != nil || !duplicate ||
		sameState.Hash() != workingHash ||
		sameDecision.DecisionHash != submitted.DecisionHash {
		t.Fatalf(
			"same-sequence replay = duplicate %t state %s decision %s error %v",
			duplicate,
			sameState.Hash(),
			sameDecision.DecisionHash,
			err,
		)
	}

	republishedInput := submitInput
	republishedInput.StreamSequence = state.NextStreamSequence()
	republishedState, republishedDecision, duplicate, err := store.ApplyTrading(
		ctx,
		state,
		republishedInput,
		submitAction,
		platformpostgres.ApplyOptions{},
	)
	if err != nil || !duplicate ||
		republishedDecision.DuplicateOfDecisionHash != submitted.DecisionHash ||
		republishedState.NextStreamSequence() != state.NextStreamSequence()+1 {
		t.Fatalf(
			"later-sequence replay = duplicate %t decision %+v next %d error %v",
			duplicate,
			republishedDecision,
			republishedState.NextStreamSequence(),
			err,
		)
	}
	state = republishedState
	assertRowCount(t, pool, "engine.duplicate_delivery_receipts", 1)
	assertRowCount(t, pool, "trading.orders", 1)
	assertRowCount(t, pool, "ledger.transactions", 1)
	assertRowCount(t, pool, "ledger.entries", 2)
	assertPersistedBalanceMatches(t, pool, workingBalance)

	recovered, err := platformpostgres.NewEngineStore(pool).
		RecoverTradingState(ctx, shardID)
	if err != nil {
		t.Fatalf("recover working order: %v", err)
	}
	if !recovered.Ready() ||
		recovered.Hash() != state.Hash() ||
		recovered.NextStreamSequence() != state.NextStreamSequence() {
		t.Fatalf(
			"recovered state = ready %t hash %s next %d, want %s/%d",
			recovered.Ready(),
			recovered.Hash(),
			recovered.NextStreamSequence(),
			state.Hash(),
			state.NextStreamSequence(),
		)
	}
	recoveredBalance := requireWorkingOrderBalance(
		t,
		recovered,
		accountID,
		"10000",
		"45",
		"9955",
		"10000",
	)
	if recoveredBalance != workingBalance {
		t.Fatalf(
			"recovered balance = %#v, want %#v",
			recoveredBalance,
			workingBalance,
		)
	}
	report, err := platformpostgres.NewEngineStore(pool).
		ReconcileShard(ctx, shardID)
	if err != nil || !report.Ready ||
		report.DuplicateDeliveryCount != 1 ||
		report.LedgerMismatchCount != 0 ||
		report.OrderFillMismatchCount != 0 ||
		report.CommandMismatchCount != 0 ||
		report.MarketMismatchCount != 0 {
		t.Fatalf("working-order reconciliation = %+v, error %v", report, err)
	}

	state = recovered
	store = platformpostgres.NewEngineStore(pool)
	cancelAction := engine.TradingAction{
		Kind: engine.TradingActionCancelOrder,
		CancelOrder: &engine.CancelOrder{
			OrderID:   orderID,
			AccountID: accountID,
		},
	}
	cancelled, cancelInput := apply(cancelAction)
	if len(cancelled.BalanceChanges) != 1 ||
		len(cancelled.LedgerChanges) != 0 ||
		len(cancelled.OrderChanges) != 1 ||
		cancelled.MarketSequence != bookInput.MarketSequence ||
		cancelled.OrderChanges[0].Status != engine.OrderStatusCancelled {
		t.Fatalf("cancel decision = %+v", cancelled)
	}
	var (
		persistedCancelEnvelopeMarket uint64
		persistedCancelDecisionMarket uint64
	)
	if err := pool.QueryRow(ctx, `
		SELECT
			(envelope ->> 'MarketSequence')::bigint,
			(decision ->> 'MarketSequence')::bigint
		  FROM engine.input_receipts
		 WHERE shard_id = $1 AND input_id = $2`,
		int64(shardID),
		cancelInput.InputID.String(),
	).Scan(
		&persistedCancelEnvelopeMarket,
		&persistedCancelDecisionMarket,
	); err != nil {
		t.Fatalf("read cancel market watermark: %v", err)
	}
	if persistedCancelEnvelopeMarket != bookInput.MarketSequence ||
		persistedCancelDecisionMarket != bookInput.MarketSequence {
		t.Fatalf(
			"cancel watermark envelope/decision = %d/%d, want %d",
			persistedCancelEnvelopeMarket,
			persistedCancelDecisionMarket,
			bookInput.MarketSequence,
		)
	}
	releasedBalance := requireWorkingOrderBalance(
		t,
		state,
		accountID,
		"10000",
		"0",
		"10000",
		"10000",
	)
	if cancelled.BalanceChanges[0] != releasedBalance {
		t.Fatalf(
			"cancel balance = %#v, want %#v",
			cancelled.BalanceChanges[0],
			releasedBalance,
		)
	}
	assertPersistedBalanceMatches(t, pool, releasedBalance)
	assertRowCount(t, pool, "trading.orders", 1)
	assertRowCount(t, pool, "ledger.transactions", 1)
	assertRowCount(t, pool, "ledger.entries", 2)
	var (
		persistedOrderStatus  string
		persistedOrderVersion uint64
	)
	if err := pool.QueryRow(ctx, `
		SELECT status, version
		  FROM trading.orders
		 WHERE order_id = $1`,
		orderID.String(),
	).Scan(&persistedOrderStatus, &persistedOrderVersion); err != nil {
		t.Fatalf("read cancelled order: %v", err)
	}
	if persistedOrderStatus != string(engine.OrderStatusCancelled) ||
		persistedOrderVersion != 2 {
		t.Fatalf(
			"persisted cancelled order = %s version %d, want cancelled/2",
			persistedOrderStatus,
			persistedOrderVersion,
		)
	}
	var (
		realtimeTypes         string
		cancelMaxSequence     uint64
		cancelChannelSequence uint64
		cancelDomainOutbox    int
	)
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT string_agg(event_type, ',' ORDER BY sequence)
			   FROM realtime.publications
			  WHERE channel = 'user:working-order-balance'),
			(SELECT max(sequence)
			   FROM realtime.publications
			  WHERE channel = 'user:working-order-balance'),
			(SELECT last_sequence
			   FROM realtime.channel_sequences
			  WHERE channel = 'user:working-order-balance'),
			(SELECT count(*)
			   FROM messaging.outbox
			  WHERE producer_class = 'engine')`,
	).Scan(
		&realtimeTypes,
		&cancelMaxSequence,
		&cancelChannelSequence,
		&cancelDomainOutbox,
	); err != nil {
		t.Fatalf("read committed cancel outboxes: %v", err)
	}
	if realtimeTypes != "order.created,order.cancelled" ||
		cancelMaxSequence != 2 ||
		cancelChannelSequence != 2 ||
		cancelDomainOutbox != 2 {
		t.Fatalf(
			"cancel outboxes = realtime %q/%d channel %d domain %d",
			realtimeTypes,
			cancelMaxSequence,
			cancelChannelSequence,
			cancelDomainOutbox,
		)
	}

	cancelledHash := state.Hash()
	sameCancelledState, sameCancelDecision, duplicate, err := store.ApplyTrading(
		ctx,
		state,
		cancelInput,
		cancelAction,
		platformpostgres.ApplyOptions{},
	)
	if err != nil || !duplicate ||
		sameCancelledState.Hash() != cancelledHash ||
		sameCancelDecision.DecisionHash != cancelled.DecisionHash {
		t.Fatalf(
			"same-sequence cancel replay = duplicate %t state %s decision %s error %v",
			duplicate,
			sameCancelledState.Hash(),
			sameCancelDecision.DecisionHash,
			err,
		)
	}
	republishedCancelInput := cancelInput
	republishedCancelInput.StreamSequence = state.NextStreamSequence()
	republishedCancelledState, republishedCancelDecision, duplicate, err :=
		store.ApplyTrading(
			ctx,
			state,
			republishedCancelInput,
			cancelAction,
			platformpostgres.ApplyOptions{},
		)
	if err != nil || !duplicate ||
		republishedCancelDecision.DuplicateOfDecisionHash !=
			cancelled.DecisionHash ||
		republishedCancelledState.NextStreamSequence() !=
			state.NextStreamSequence()+1 {
		t.Fatalf(
			"later-sequence cancel replay = duplicate %t decision %+v next %d error %v",
			duplicate,
			republishedCancelDecision,
			republishedCancelledState.NextStreamSequence(),
			err,
		)
	}
	state = republishedCancelledState
	assertRowCount(t, pool, "engine.duplicate_delivery_receipts", 2)
	assertPersistedBalanceMatches(t, pool, releasedBalance)
	assertRowCount(t, pool, "ledger.transactions", 1)
	assertRowCount(t, pool, "ledger.entries", 2)
	var (
		realtimeAfterCancelReplay int
		domainAfterCancelReplay   int
	)
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM realtime.publications),
			(SELECT count(*) FROM messaging.outbox
			  WHERE producer_class = 'engine')`,
	).Scan(
		&realtimeAfterCancelReplay,
		&domainAfterCancelReplay,
	); err != nil {
		t.Fatalf("read cancel replay outboxes: %v", err)
	}
	if realtimeAfterCancelReplay != 2 || domainAfterCancelReplay != 2 {
		t.Fatalf(
			"cancel replay outboxes = realtime %d domain %d, want 2/2",
			realtimeAfterCancelReplay,
			domainAfterCancelReplay,
		)
	}
	recoveredAfterCancel, err := platformpostgres.NewEngineStore(pool).
		RecoverTradingState(ctx, shardID)
	if err != nil ||
		!recoveredAfterCancel.Ready() ||
		recoveredAfterCancel.Hash() != state.Hash() ||
		recoveredAfterCancel.NextStreamSequence() != state.NextStreamSequence() {
		t.Fatalf(
			"cancel recovery = ready %t hash %s/%s next %d/%d error %v",
			recoveredAfterCancel.Ready(),
			recoveredAfterCancel.Hash(),
			state.Hash(),
			recoveredAfterCancel.NextStreamSequence(),
			state.NextStreamSequence(),
			err,
		)
	}
	requireWorkingOrderBalance(
		t,
		recoveredAfterCancel,
		accountID,
		"10000",
		"0",
		"10000",
		"10000",
	)
	cancelReport, err := platformpostgres.NewEngineStore(pool).
		ReconcileShard(ctx, shardID)
	if err != nil || !cancelReport.Ready ||
		cancelReport.DuplicateDeliveryCount != 2 ||
		cancelReport.LedgerMismatchCount != 0 ||
		cancelReport.OrderFillMismatchCount != 0 ||
		cancelReport.CommandMismatchCount != 0 ||
		cancelReport.MarketMismatchCount != 0 ||
		cancelReport.RealtimeMismatchCount != 0 {
		t.Fatalf("cancel reconciliation = %+v, error %v", cancelReport, err)
	}
	state = recoveredAfterCancel
	store = platformpostgres.NewEngineStore(pool)

	for _, test := range []struct {
		name                  string
		limit, mark, bid, ask string
		wantUsed, wantFree    string
	}{
		{
			name:     "higher mark controls margin and fee",
			limit:    "50000",
			mark:     "60000",
			bid:      "59999",
			ask:      "60001",
			wantUsed: "54",
			wantFree: "9946",
		},
		{
			name:     "margin half even down",
			limit:    "2512.50",
			mark:     "2512.49",
			bid:      "2512.48",
			ask:      "2512.51",
			wantUsed: "2.26",
			wantFree: "9997.74",
		},
		{
			name:     "margin half even up",
			limit:    "2537.50",
			mark:     "2537.49",
			bid:      "2537.48",
			ask:      "2537.51",
			wantUsed: "2.29",
			wantFree: "9997.71",
		},
		{
			name:     "fee half even down",
			limit:    "2010.00",
			mark:     "2009.99",
			bid:      "2009.98",
			ask:      "2010.01",
			wantUsed: "1.8",
			wantFree: "9998.2",
		},
		{
			name:     "fee half even up",
			limit:    "2030.00",
			mark:     "2029.99",
			bid:      "2029.98",
			ask:      "2030.01",
			wantUsed: "1.83",
			wantFree: "9998.17",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, controlBookInput := apply(engine.TradingAction{
				Kind: engine.TradingActionUpdateBook,
				UpdateBook: &engine.UpdateBook{
					InstrumentID: "BTC-PERP",
					MarkPrice:    test.mark,
					Bids: []engine.BookLevel{{
						Price: test.bid, Quantity: "10",
					}},
					Asks: []engine.BookLevel{{
						Price: test.ask, Quantity: "10",
					}},
				},
			})
			controlOrderID := ids.Next()
			controlDecision, _ := apply(engine.TradingAction{
				Kind: engine.TradingActionSubmitOrder,
				SubmitOrder: &engine.SubmitOrder{
					OrderID:      controlOrderID,
					AccountID:    accountID,
					InstrumentID: "BTC-PERP",
					Side:         engine.SideBuy,
					Type:         engine.OrderTypeLimit,
					TimeInForce:  engine.TimeInForceGTC,
					Quantity:     "1",
					Price:        test.limit,
				},
			})
			if controlDecision.MarketSequence != controlBookInput.MarketSequence ||
				len(controlDecision.BalanceChanges) != 1 ||
				len(controlDecision.LedgerChanges) != 0 ||
				len(controlDecision.OrderChanges) != 1 ||
				controlDecision.OrderChanges[0].Status != engine.OrderStatusWorking {
				t.Fatalf("control submit decision = %+v", controlDecision)
			}
			controlBalance := requireWorkingOrderBalance(
				t,
				state,
				accountID,
				"10000",
				test.wantUsed,
				test.wantFree,
				"10000",
			)
			if controlDecision.BalanceChanges[0] != controlBalance {
				t.Fatalf(
					"control decision balance = %#v, want %#v",
					controlDecision.BalanceChanges[0],
					controlBalance,
				)
			}
			assertPersistedBalanceMatches(t, pool, controlBalance)
			assertRowCount(t, pool, "ledger.transactions", 1)
			assertRowCount(t, pool, "ledger.entries", 2)

			controlCancel, _ := apply(engine.TradingAction{
				Kind: engine.TradingActionCancelOrder,
				CancelOrder: &engine.CancelOrder{
					OrderID:   controlOrderID,
					AccountID: accountID,
				},
			})
			if len(controlCancel.BalanceChanges) != 1 ||
				len(controlCancel.LedgerChanges) != 0 ||
				len(controlCancel.OrderChanges) != 1 ||
				controlCancel.MarketSequence !=
					controlBookInput.MarketSequence ||
				controlCancel.OrderChanges[0].Status != engine.OrderStatusCancelled {
				t.Fatalf("control cancel decision = %+v", controlCancel)
			}
			zeroBalance := requireWorkingOrderBalance(
				t,
				state,
				accountID,
				"10000",
				"0",
				"10000",
				"10000",
			)
			if controlCancel.BalanceChanges[0] != zeroBalance {
				t.Fatalf(
					"control cancel balance = %#v, want %#v",
					controlCancel.BalanceChanges[0],
					zeroBalance,
				)
			}
			assertPersistedBalanceMatches(t, pool, zeroBalance)
			assertRowCount(t, pool, "ledger.transactions", 1)
			assertRowCount(t, pool, "ledger.entries", 2)
		})
	}

	cancelAfterLaterMarkets := cancelInput
	cancelAfterLaterMarkets.StreamSequence = state.NextStreamSequence()
	cancelAfterLaterState, cancelAfterLaterDecision, duplicate, err :=
		store.ApplyTrading(
			ctx,
			state,
			cancelAfterLaterMarkets,
			cancelAction,
			platformpostgres.ApplyOptions{},
		)
	if err != nil || !duplicate ||
		cancelAfterLaterDecision.MarketSequence != bookInput.MarketSequence ||
		cancelAfterLaterDecision.DuplicateOfDecisionHash !=
			cancelled.DecisionHash ||
		cancelAfterLaterState.NextStreamSequence() !=
			state.NextStreamSequence()+1 {
		t.Fatalf(
			"cancel replay after later markets = duplicate %t decision %+v next %d error %v",
			duplicate,
			cancelAfterLaterDecision,
			cancelAfterLaterState.NextStreamSequence(),
			err,
		)
	}
	state = cancelAfterLaterState
	assertRowCount(t, pool, "engine.duplicate_delivery_receipts", 3)
	requireWorkingOrderBalance(
		t,
		state,
		accountID,
		"10000",
		"0",
		"10000",
		"10000",
	)
	assertRowCount(t, pool, "ledger.transactions", 1)
	assertRowCount(t, pool, "ledger.entries", 2)

	finalRecovered, err := platformpostgres.NewEngineStore(pool).
		RecoverTradingState(ctx, shardID)
	if err != nil ||
		!finalRecovered.Ready() ||
		finalRecovered.Hash() != state.Hash() ||
		finalRecovered.NextStreamSequence() != state.NextStreamSequence() {
		t.Fatalf(
			"final recovery = ready %t hash %s/%s next %d/%d error %v",
			finalRecovered.Ready(),
			finalRecovered.Hash(),
			state.Hash(),
			finalRecovered.NextStreamSequence(),
			state.NextStreamSequence(),
			err,
		)
	}
	requireWorkingOrderBalance(
		t,
		finalRecovered,
		accountID,
		"10000",
		"0",
		"10000",
		"10000",
	)
	finalReport, err := platformpostgres.NewEngineStore(pool).
		ReconcileShard(ctx, shardID)
	if err != nil || !finalReport.Ready ||
		finalReport.DuplicateDeliveryCount != 3 ||
		finalReport.LedgerMismatchCount != 0 ||
		finalReport.OrderFillMismatchCount != 0 ||
		finalReport.CommandMismatchCount != 0 ||
		finalReport.MarketMismatchCount != 0 ||
		finalReport.RealtimeMismatchCount != 0 {
		t.Fatalf("final working-order reconciliation = %+v, error %v", finalReport, err)
	}

	corruptAsReplicationAuthority(
		t,
		pool,
		`UPDATE messaging.outbox
		    SET payload = jsonb_set(
				payload,
				'{marketSequence}',
				to_jsonb($2::bigint)
			)
		  WHERE message_id = $1`,
		submitInput.InputID.String(),
		int64(bookInput.MarketSequence),
	)
	corruptReport, err := platformpostgres.NewEngineStore(pool).
		ReconcileShard(ctx, shardID)
	if err == nil || corruptReport.Ready ||
		corruptReport.CommandMismatchCount != 1 {
		t.Fatalf(
			"corrupt command outbox reconciliation = %+v, error %v",
			corruptReport,
			err,
		)
	}
}

func TestEngineStoreRejectsCommandOutsideCommittedMarketHighWatermark(
	t *testing.T,
) {
	for _, test := range []struct {
		name           string
		marketSequence uint64
	}{
		{name: "stale", marketSequence: 2},
		{name: "future", marketSequence: 4},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			pool := postgresPool(t)
			resetDurableSchemas(t, pool)
			const shardID = engine.ShardID(12)
			if err := newCurrentTestMigrator(
				t,
				pool,
				os.DirFS(filepath.Join("..", "..", "..", "migrations")),
			).MigrateAndProvision(ctx, shardID); err != nil {
				t.Fatalf("migrate market-fence database: %v", err)
			}
			store := platformpostgres.NewEngineStore(pool)
			state := engine.NewState(shardID)
			ids := testkit.NewShardIDSequence(shardID)
			clock := testkit.NewManualClock(
				engine.NewLogicalTime(time.Date(
					2026,
					time.July,
					28,
					16,
					0,
					0,
					0,
					time.UTC,
				)),
			)
			state, _, _, _ = applyStoredTrading(
				t,
				pool,
				store,
				state,
				ids,
				clock,
				engine.TradingAction{
					Kind: engine.TradingActionConfigureInstrument,
					ConfigureInstrument: &engine.ConfigureInstrument{
						InstrumentID:            "BTC-PERP",
						Revision:                1,
						PriceScale:              2,
						QuantityScale:           3,
						SettlementCurrency:      "USDC",
						SettlementCurrencyScale: 2,
						InitialMarginRate:       "0.02",
						MaintenanceMarginRate:   "0.01",
						MaxLeverage:             "50",
						MakerFeeRate:            "0.0002",
						TakerFeeRate:            "0.0005",
					},
				},
				platformpostgres.ApplyOptions{},
			)
			const accountID = "urn:xb:account:market-fence"
			state, _, _, _ = applyStoredTrading(
				t,
				pool,
				store,
				state,
				ids,
				clock,
				engine.TradingAction{
					Kind: engine.TradingActionConfigureAccount,
					ConfigureAccount: &engine.ConfigureAccount{
						AccountID: accountID,
						OmsMode:   engine.OmsModeNetting,
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
					Kind: engine.TradingActionUpdateBook,
					UpdateBook: &engine.UpdateBook{
						InstrumentID: "BTC-PERP",
						MarkPrice:    "49999",
						Bids: []engine.BookLevel{{
							Price: "49998", Quantity: "10",
						}},
						Asks: []engine.BookLevel{{
							Price: "50001", Quantity: "10",
						}},
					},
				},
				platformpostgres.ApplyOptions{},
			)
			if marketSequence, found := state.MarketSequence(); !found ||
				marketSequence != 3 {
				t.Fatalf(
					"committed market high-watermark = %d found %t, want 3",
					marketSequence,
					found,
				)
			}
			action := engine.TradingAction{
				Kind: engine.TradingActionAdjustBalance,
				AdjustBalance: &engine.AdjustBalance{
					AccountID:     accountID,
					Currency:      "USDC",
					CurrencyScale: 2,
					Operation:     engine.BalanceOperationDeposit,
					Amount:        "10000",
				},
			}
			input := nextStoredInput(t, state, ids, clock, action)
			input.MarketSequence = test.marketSequence
			seedPendingCommand(t, pool, input, action)
			halted, _, duplicate, err := store.ApplyTrading(
				ctx,
				state,
				input,
				action,
				platformpostgres.ApplyOptions{},
			)
			if !errors.Is(err, platformpostgres.ErrCommandInputConflict) ||
				duplicate ||
				halted.Ready() {
				t.Fatalf(
					"invalid market fence = ready %t duplicate %t error %v",
					halted.Ready(),
					duplicate,
					err,
				)
			}
			if _, found := halted.Balance(accountID, "USDC"); found {
				t.Fatal("invalid market fence committed a balance")
			}
			var balanceRows int
			if err := pool.QueryRow(ctx, `
				SELECT count(*) FROM ledger.balances`,
			).Scan(&balanceRows); err != nil {
				t.Fatalf("count invalid-fence balances: %v", err)
			}
			if balanceRows != 0 {
				t.Fatalf("invalid market fence committed %d balance rows", balanceRows)
			}
			assertRowCount(t, pool, "ledger.transactions", 0)
			assertRowCount(t, pool, "ledger.entries", 0)
			assertRowCount(t, pool, "engine.input_receipts", 3)
			assertRowCount(t, pool, "engine.shard_faults", 1)
			var publicationRows int
			if err := pool.QueryRow(ctx, `
				SELECT count(*) FROM realtime.publications`,
			).Scan(&publicationRows); err != nil {
				t.Fatalf("count invalid-fence publications: %v", err)
			}
			if publicationRows != 0 {
				t.Fatalf(
					"invalid market fence committed %d realtime publications",
					publicationRows,
				)
			}
		})
	}
}

func TestTimerInputRetainsCommittedMarketHighWatermarkAcrossRecoveryAndRedelivery(
	t *testing.T,
) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	const shardID engine.ShardID = 13
	if err := newCurrentTestMigrator(
		t,
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).MigrateAndProvision(ctx, shardID); err != nil {
		t.Fatalf("migrate timer market-binding database: %v", err)
	}

	store := platformpostgres.NewEngineStore(pool)
	state := engine.NewState(shardID)
	ids := testkit.NewShardIDSequence(shardID)
	clock := testkit.NewManualClock(engine.NewLogicalTime(time.Date(
		2026,
		time.July,
		28,
		12,
		0,
		0,
		0,
		time.UTC,
	)))
	state, _, _, _ = applyStoredTrading(
		t,
		pool,
		store,
		state,
		ids,
		clock,
		engine.TradingAction{
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
		platformpostgres.ApplyOptions{},
	)
	state, _, marketInput, _ := applyStoredTrading(
		t,
		pool,
		store,
		state,
		ids,
		clock,
		engine.TradingAction{
			Kind: engine.TradingActionUpdateBook,
			UpdateBook: &engine.UpdateBook{
				InstrumentID: "BTC-PERP",
				MarkPrice:    "50000",
				Bids: []engine.BookLevel{{
					Price: "49999", Quantity: "10",
				}},
				Asks: []engine.BookLevel{{
					Price: "50001", Quantity: "10",
				}},
			},
		},
		platformpostgres.ApplyOptions{},
	)
	timerAction := engine.TradingAction{
		Kind: engine.TradingActionSettleFunding,
		SettleFunding: &engine.SettleFunding{
			SettlementID: ids.Next(),
			InstrumentID: "BTC-PERP",
			OraclePrice:  "50000",
			Rate:         "0.0001",
		},
	}
	state, timerDecision, timerInput, _ := applyStoredTrading(
		t,
		pool,
		store,
		state,
		ids,
		clock,
		timerAction,
		platformpostgres.ApplyOptions{},
	)
	if timerInput.Kind != engine.InputKindTimer ||
		timerInput.MarketSequence != 0 ||
		timerDecision.MarketSequence != marketInput.MarketSequence {
		t.Fatalf(
			"timer binding input=%+v decision market=%d, want %d",
			timerInput,
			timerDecision.MarketSequence,
			marketInput.MarketSequence,
		)
	}
	var (
		persistedEnvelopeMarket uint64
		persistedDecisionMarket uint64
	)
	if err := pool.QueryRow(ctx, `
		SELECT
			(envelope ->> 'MarketSequence')::bigint,
			(decision ->> 'MarketSequence')::bigint
		  FROM engine.input_receipts
		 WHERE shard_id = $1
		   AND input_id = $2`,
		int64(shardID),
		timerInput.InputID.String(),
	).Scan(
		&persistedEnvelopeMarket,
		&persistedDecisionMarket,
	); err != nil {
		t.Fatalf("read persisted timer market binding: %v", err)
	}
	if persistedEnvelopeMarket != marketInput.MarketSequence ||
		persistedDecisionMarket != marketInput.MarketSequence {
		t.Fatalf(
			"persisted timer market envelope=%d decision=%d, want %d",
			persistedEnvelopeMarket,
			persistedDecisionMarket,
			marketInput.MarketSequence,
		)
	}

	state, _, _, _ = applyStoredTrading(
		t,
		pool,
		store,
		state,
		ids,
		clock,
		engine.TradingAction{
			Kind: engine.TradingActionUpdateBook,
			UpdateBook: &engine.UpdateBook{
				InstrumentID: "BTC-PERP",
				MarkPrice:    "51000",
				Bids: []engine.BookLevel{{
					Price: "50999", Quantity: "10",
				}},
				Asks: []engine.BookLevel{{
					Price: "51001", Quantity: "10",
				}},
			},
		},
		platformpostgres.ApplyOptions{},
	)
	replay := timerInput
	replay.StreamSequence = state.NextStreamSequence()
	replayed, replayDecision, duplicate, err := store.ApplyTrading(
		ctx,
		state,
		replay,
		timerAction,
		platformpostgres.ApplyOptions{},
	)
	if err != nil || !duplicate ||
		replayDecision.MarketSequence != marketInput.MarketSequence ||
		replayDecision.DuplicateOfDecisionHash != timerDecision.DecisionHash {
		t.Fatalf(
			"timer replay duplicate=%t decision=%+v error=%v",
			duplicate,
			replayDecision,
			err,
		)
	}
	recovered, err := store.RecoverTradingState(ctx, shardID)
	if err != nil || !recovered.Ready() ||
		recovered.Hash() != replayed.Hash() {
		t.Fatalf(
			"recover timer replay ready=%t hash=%s want=%s error=%v",
			recovered.Ready(),
			recovered.Hash(),
			replayed.Hash(),
			err,
		)
	}
	if marketSequence, found := recovered.MarketSequence(); !found ||
		marketSequence != 4 {
		t.Fatalf(
			"recovered market high-watermark=%d found=%t, want 4",
			marketSequence,
			found,
		)
	}
	if report, err := store.ReconcileShard(ctx, shardID); err != nil ||
		!report.Ready {
		t.Fatalf("timer market-binding reconciliation=%+v error=%v", report, err)
	}
}

func TestEngineStoreRejectsTimerOutsideCommittedMarketHighWatermark(
	t *testing.T,
) {
	for _, test := range []struct {
		name           string
		marketSequence uint64
	}{
		{name: "stale", marketSequence: 2},
		{name: "future", marketSequence: 4},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			pool := postgresPool(t)
			resetDurableSchemas(t, pool)
			const shardID engine.ShardID = 14
			if err := newCurrentTestMigrator(
				t,
				pool,
				os.DirFS(filepath.Join("..", "..", "..", "migrations")),
			).MigrateAndProvision(ctx, shardID); err != nil {
				t.Fatalf("migrate timer-fence database: %v", err)
			}
			store := platformpostgres.NewEngineStore(pool)
			state := engine.NewState(shardID)
			ids := testkit.NewShardIDSequence(shardID)
			clock := testkit.NewManualClock(engine.NewLogicalTime(time.Date(
				2026,
				time.July,
				28,
				13,
				0,
				0,
				0,
				time.UTC,
			)))
			for _, action := range []engine.TradingAction{
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
						AccountID: "urn:xb:account:timer-fence",
						OmsMode:   engine.OmsModeNetting,
					},
				},
				{
					Kind: engine.TradingActionUpdateBook,
					UpdateBook: &engine.UpdateBook{
						InstrumentID: "BTC-PERP",
						MarkPrice:    "50000",
						Bids: []engine.BookLevel{{
							Price: "49999", Quantity: "10",
						}},
						Asks: []engine.BookLevel{{
							Price: "50001", Quantity: "10",
						}},
					},
				},
			} {
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
			timerAction := engine.TradingAction{
				Kind: engine.TradingActionSettleFunding,
				SettleFunding: &engine.SettleFunding{
					SettlementID: ids.Next(),
					InstrumentID: "BTC-PERP",
					OraclePrice:  "50000",
					Rate:         "0.0001",
				},
			}
			timerInput := nextStoredInput(t, state, ids, clock, timerAction)
			timerInput.MarketSequence = test.marketSequence
			halted, _, duplicate, err := store.ApplyTrading(
				ctx,
				state,
				timerInput,
				timerAction,
				platformpostgres.ApplyOptions{},
			)
			if !errors.Is(err, engine.ErrDurableInputConflict) ||
				duplicate ||
				halted.Ready() {
				t.Fatalf(
					"invalid timer fence ready=%t duplicate=%t error=%v",
					halted.Ready(),
					duplicate,
					err,
				)
			}
			var (
				inputRows       int
				fundingRows     int
				ledgerRows      int
				publicationRows int
				outboxRows      int
				faultRows       int
			)
			if err := pool.QueryRow(ctx, `
				SELECT
					(SELECT count(*) FROM engine.input_receipts
					  WHERE input_id = $1),
					(SELECT count(*) FROM trading.funding_settlements),
					(SELECT count(*) FROM ledger.transactions),
					(SELECT count(*) FROM realtime.publications),
					(SELECT count(*) FROM messaging.outbox
					  WHERE engine_input_id = $1),
					(SELECT count(*) FROM engine.shard_faults
					  WHERE input_id = $1)`,
				timerInput.InputID.String(),
			).Scan(
				&inputRows,
				&fundingRows,
				&ledgerRows,
				&publicationRows,
				&outboxRows,
				&faultRows,
			); err != nil {
				t.Fatalf("inspect invalid timer fence effects: %v", err)
			}
			if inputRows != 0 ||
				fundingRows != 0 ||
				ledgerRows != 0 ||
				publicationRows != 0 ||
				outboxRows != 0 ||
				faultRows != 1 {
				t.Fatalf(
					"invalid timer fence effects receipts=%d funding=%d ledger=%d publications=%d outbox=%d faults=%d",
					inputRows,
					fundingRows,
					ledgerRows,
					publicationRows,
					outboxRows,
					faultRows,
				)
			}
			recovered, err := store.RecoverTradingState(ctx, shardID)
			if err != nil || recovered.Ready() {
				t.Fatalf(
					"recover invalid timer fence ready=%t error=%v",
					recovered.Ready(),
					err,
				)
			}
		})
	}
}

func requireWorkingOrderBalance(
	t *testing.T,
	state engine.State,
	accountID string,
	total string,
	used string,
	free string,
	equity string,
) engine.BalanceSnapshot {
	t.Helper()
	balance, ok := state.Balance(accountID, "USDC")
	if !ok ||
		balance.Total != total ||
		balance.Used != used ||
		balance.Free != free ||
		balance.Equity != equity {
		t.Fatalf(
			"balance = %#v found %t, want total/used/free/equity %s/%s/%s/%s",
			balance,
			ok,
			total,
			used,
			free,
			equity,
		)
	}
	return balance
}
