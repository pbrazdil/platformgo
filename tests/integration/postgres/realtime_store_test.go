package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
	"github.com/upcomers-org/platformgo/internal/edge"
	"github.com/upcomers-org/platformgo/internal/engine"
	"github.com/upcomers-org/platformgo/testkit"
)

func TestCommittedRealtimeOutboxPreservesChannelOrderAndBusinessIdentity(t *testing.T) {
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := platformpostgres.NewMigrator(
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).MigrateAndProvision(context.Background(), 19); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	store := platformpostgres.NewEngineStore(pool)
	state := engine.NewState(19)
	ids := testkit.NewShardIDSequence(19)
	clock := testkit.NewManualClock(
		engine.NewLogicalTime(time.Date(2026, time.July, 25, 18, 0, 0, 0, time.UTC)),
	)
	const (
		accountID = "urn:xb:account:acct-realtime"
		userID    = "urn:xb:user:user-realtime"
	)
	state = configureRealtimeAccount(t, pool, store, state, ids, clock, accountID)
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO identity.users (user_id, login, normalized_login)
		VALUES ($1, 'realtime-user', 'realtime-user')`,
		userID,
	); err != nil {
		t.Fatalf("seed realtime user: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO identity.user_accounts (user_id, account_id)
		VALUES ($1, $2)`,
		userID,
		accountID,
	); err != nil {
		t.Fatalf("seed realtime account mapping: %v", err)
	}

	orderID := engine.IDFromSequence(engine.ID{}, 1901)
	var firstInput engine.InputEnvelope
	state, firstDecision, firstInput, _ := applyStoredTrading(
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
				AccountID:    accountID,
				InstrumentID: "BTC-PERP",
				Side:         engine.SideBuy,
				Type:         engine.OrderTypeLimit,
				TimeInForce:  engine.TimeInForceGTC,
				Quantity:     "1",
				Price:        "99",
			},
		},
		platformpostgres.ApplyOptions{},
	)
	if len(firstDecision.Events) != 1 {
		t.Fatalf("first decision events = %d, want 1", len(firstDecision.Events))
	}
	state, secondDecision, _, _ := applyStoredTrading(
		t,
		pool,
		store,
		state,
		ids,
		clock,
		engine.TradingAction{
			Kind: engine.TradingActionCancelOrder,
			CancelOrder: &engine.CancelOrder{
				AccountID: accountID,
				OrderID:   orderID,
			},
		},
		platformpostgres.ApplyOptions{},
	)
	if len(secondDecision.Events) != 1 {
		t.Fatalf("second decision events = %d, want 1", len(secondDecision.Events))
	}

	duplicateState, _, duplicate, err := store.ApplyTrading(
		context.Background(),
		state,
		firstInput,
		engine.TradingAction{
			Kind: engine.TradingActionSubmitOrder,
			SubmitOrder: &engine.SubmitOrder{
				OrderID:      orderID,
				AccountID:    accountID,
				InstrumentID: "BTC-PERP",
				Side:         engine.SideBuy,
				Type:         engine.OrderTypeLimit,
				TimeInForce:  engine.TimeInForceGTC,
				Quantity:     "1",
				Price:        "99",
			},
		},
		platformpostgres.ApplyOptions{},
	)
	if err != nil || !duplicate || duplicateState.Hash() != state.Hash() {
		t.Fatalf("duplicate replay = duplicate %t error %v", duplicate, err)
	}

	faultedOrder := engine.TradingAction{
		Kind: engine.TradingActionSubmitOrder,
		SubmitOrder: &engine.SubmitOrder{
			OrderID:      engine.IDFromSequence(engine.ID{}, 1902),
			AccountID:    accountID,
			InstrumentID: "BTC-PERP",
			Side:         engine.SideBuy,
			Type:         engine.OrderTypeLimit,
			TimeInForce:  engine.TimeInForceGTC,
			Quantity:     "1",
			Price:        "98",
		},
	}
	faultedInput := nextStoredInput(t, state, ids, clock, faultedOrder)
	seedPendingCommand(t, pool, faultedInput, faultedOrder)
	faultedState, _, _, err := store.ApplyTrading(
		context.Background(),
		state,
		faultedInput,
		faultedOrder,
		platformpostgres.ApplyOptions{
			Faults: testkit.NewFaults(
				platformpostgres.FailpointAfterPersistBeforeCommit,
			),
		},
	)
	if !errors.Is(err, platformpostgres.ErrInjectedFault) ||
		faultedState.Hash() != state.Hash() {
		t.Fatalf("faulted realtime commit state=%s error=%v", faultedState.Hash(), err)
	}

	var publicationCount int
	if err := pool.QueryRow(
		context.Background(),
		"SELECT count(*) FROM realtime.publications",
	).Scan(&publicationCount); err != nil {
		t.Fatalf("count realtime publications: %v", err)
	}
	if publicationCount != 2 {
		t.Fatalf("realtime publication count = %d, want 2", publicationCount)
	}

	realtime := platformpostgres.NewRealtimeStore(pool)
	first := claimOneRealtime(t, realtime)
	if first.Channel != "user:user-realtime" ||
		first.EventID != firstDecision.Events[0].EventID.String() ||
		first.EventType != "order.created" ||
		first.AccountID != accountID ||
		first.Sequence != 1 ||
		first.Attempts != 1 {
		t.Fatalf("first realtime publication = %+v", first)
	}
	var firstData edge.OrderView
	if err := json.Unmarshal(first.Data, &firstData); err != nil {
		t.Fatalf("decode first realtime data: %v", err)
	}
	wantFirstData := edge.OrderView{
		OrderID:        "urn:xb:order:" + orderID.String(),
		IntentID:       "",
		Symbol:         "BTC-PERP",
		Side:           "BUY",
		Type:           "LIMIT",
		Quantity:       "1",
		Status:         "working",
		FilledQuantity: "0",
		LimitPrice:     realtimeStringPointer("99"),
		TimeInForce:    realtimeStringPointer("GTC"),
		AccountID:      accountID,
	}
	if !reflect.DeepEqual(firstData, wantFirstData) {
		t.Fatalf("first realtime data = %+v, want %+v", firstData, wantFirstData)
	}

	blocked, err := realtime.ClaimRealtimeBatch(
		context.Background(),
		10,
		30*time.Second,
	)
	if err != nil {
		t.Fatalf("claim while first is leased: %v", err)
	}
	if len(blocked) != 0 {
		t.Fatalf("later channel event bypassed first claim: %+v", blocked)
	}

	if _, err := pool.Exec(context.Background(), `
		UPDATE realtime.publications
		   SET claimed_at = clock_timestamp() - interval '31 seconds'
		 WHERE channel = $1
		   AND event_id = $2`,
		first.Channel,
		first.EventID,
	); err != nil {
		t.Fatalf("age realtime claim: %v", err)
	}
	reclaimed := claimOneRealtime(t, realtime)
	if reclaimed.EventID != first.EventID ||
		reclaimed.Sequence != first.Sequence ||
		reclaimed.Attempts != 2 {
		t.Fatalf("reclaimed publication changed identity: %+v", reclaimed)
	}
	if err := realtime.MarkRealtimePublished(
		context.Background(),
		first,
	); !errors.Is(err, platformpostgres.ErrRealtimeClaimLost) {
		t.Fatalf("stale realtime claimant mark error = %v", err)
	}
	if err := realtime.MarkRealtimePublished(
		context.Background(),
		reclaimed,
	); err != nil {
		t.Fatalf("mark first realtime publication: %v", err)
	}

	second := claimRealtimeConcurrently(t, realtime)
	if second.EventID != secondDecision.Events[0].EventID.String() ||
		second.EventType != "order.cancelled" ||
		second.Sequence != 2 {
		t.Fatalf("second realtime publication = %+v", second)
	}
	var realtimeOrder edge.OrderView
	if err := json.Unmarshal(second.Data, &realtimeOrder); err != nil {
		t.Fatalf("decode cancelled realtime order: %v", err)
	}
	restOrders, err := platformpostgres.NewCompatibilityStore(pool).Orders(
		context.Background(),
		accountID,
	)
	if err != nil {
		t.Fatalf("read authoritative REST order projection: %v", err)
	}
	if len(restOrders) != 1 || !reflect.DeepEqual(realtimeOrder, restOrders[0]) {
		t.Fatalf(
			"realtime order = %+v, authoritative REST order = %+v",
			realtimeOrder,
			restOrders,
		)
	}
	for _, invalid := range []struct {
		class      platformpostgres.RealtimeFailureClass
		quarantine bool
	}{
		{class: platformpostgres.RealtimeFailureTransient, quarantine: true},
		{class: platformpostgres.RealtimeFailurePermanent, quarantine: false},
		{class: platformpostgres.RealtimeFailureRetryExhausted, quarantine: false},
	} {
		if err := realtime.MarkRealtimeFailed(
			context.Background(),
			second,
			time.Second,
			invalid.class,
			invalid.quarantine,
			context.DeadlineExceeded,
		); err == nil {
			t.Fatalf("accepted invalid failure state %+v", invalid)
		}
	}
	if err := realtime.MarkRealtimeFailed(
		context.Background(),
		second,
		7*time.Second,
		platformpostgres.RealtimeFailureTransient,
		false,
		context.DeadlineExceeded,
	); err != nil {
		t.Fatalf("mark second realtime failure: %v", err)
	}
	if early, claimErr := realtime.ClaimRealtimeBatch(
		context.Background(),
		10,
		30*time.Second,
	); claimErr != nil {
		t.Fatalf("claim before retry: %v", claimErr)
	} else if len(early) != 0 {
		t.Fatalf("publication retried before deadline: %+v", early)
	}
	if _, err := pool.Exec(context.Background(), `
		UPDATE realtime.publications
		   SET next_attempt_at = clock_timestamp()
		 WHERE channel = $1
		   AND event_id = $2`,
		second.Channel,
		second.EventID,
	); err != nil {
		t.Fatalf("make realtime retry eligible: %v", err)
	}
	retried := claimOneRealtime(t, realtime)
	if retried.EventID != second.EventID ||
		retried.Sequence != second.Sequence ||
		retried.Attempts != 2 {
		t.Fatalf("retry changed publication identity: %+v", retried)
	}
	if err := realtime.MarkRealtimeFailed(
		context.Background(),
		retried,
		0,
		platformpostgres.RealtimeFailureRetryExhausted,
		true,
		context.DeadlineExceeded,
	); err != nil {
		t.Fatalf("quarantine exhausted realtime publication: %v", err)
	}
	if quarantined, err := realtime.ClaimRealtimeBatch(
		context.Background(),
		10,
		30*time.Second,
	); err != nil {
		t.Fatalf("claim quarantined realtime publication: %v", err)
	} else if len(quarantined) != 0 {
		t.Fatalf("quarantined publication was claimed: %+v", quarantined)
	}
	requeue := platformpostgres.RealtimeRequeue{
		RequestID: "019f9460-4b36-4e9b-8f44-682611f71901",
		Channel:   retried.Channel,
		EventID:   retried.EventID,
		Actor:     "operator@example.test",
		Reason:    "Centrifugo recovered and delivery was verified",
	}
	if err := realtime.RequeueRealtimePublication(
		context.Background(),
		requeue,
	); err != nil {
		t.Fatalf("requeue quarantined realtime publication: %v", err)
	}
	if err := realtime.RequeueRealtimePublication(
		context.Background(),
		requeue,
	); err != nil {
		t.Fatalf("replay identical realtime requeue: %v", err)
	}
	requeued := claimOneRealtime(t, realtime)
	if requeued.EventID != second.EventID ||
		requeued.Sequence != second.Sequence ||
		requeued.Attempts != 3 ||
		requeued.RetryAttemptBase != 2 {
		t.Fatalf("requeued publication changed identity/budget: %+v", requeued)
	}
	if err := realtime.MarkRealtimePublished(
		context.Background(),
		requeued,
	); err != nil {
		t.Fatalf("publish requeued realtime publication: %v", err)
	}

}

func TestImmediateFillRealtimeSequenceMatchesAuthoritativeViews(t *testing.T) {
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := platformpostgres.NewMigrator(
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).MigrateAndProvision(context.Background(), 29); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	store := platformpostgres.NewEngineStore(pool)
	state := engine.NewState(29)
	ids := testkit.NewShardIDSequence(29)
	clock := testkit.NewManualClock(
		engine.NewLogicalTime(time.Date(2026, time.July, 25, 19, 0, 0, 0, time.UTC)),
	)
	const (
		accountID = "urn:xb:account:immediate-realtime"
		userID    = "urn:xb:user:immediate-realtime"
	)
	state = configureRealtimeAccount(t, pool, store, state, ids, clock, accountID)
	if _, err := pool.Exec(context.Background(), `
		WITH inserted_user AS (
			INSERT INTO identity.users (user_id, login, normalized_login)
			VALUES ($1, 'immediate-realtime', 'immediate-realtime')
			RETURNING user_id
		)
		INSERT INTO identity.user_accounts (user_id, account_id)
		SELECT user_id, $2 FROM inserted_user`,
		userID,
		accountID,
	); err != nil {
		t.Fatal(err)
	}
	orderID := engine.IDFromSequence(engine.ID{}, 2_903)
	_, _, _, _ = applyStoredTrading(
		t,
		pool,
		store,
		state,
		ids,
		clock,
		engine.TradingAction{
			Kind: engine.TradingActionSubmitOrder,
			SubmitOrder: &engine.SubmitOrder{
				OrderID: orderID, AccountID: accountID,
				InstrumentID: "BTC-PERP", Side: engine.SideBuy,
				Type: engine.OrderTypeMarket, TimeInForce: engine.TimeInForceIOC,
				Quantity: "1",
			},
		},
		platformpostgres.ApplyOptions{},
	)
	var immediateTypes []string
	if err := pool.QueryRow(context.Background(), `
		SELECT array_agg(event_type ORDER BY sequence)
		  FROM realtime.publications
		 WHERE channel = 'user:immediate-realtime'`,
	).Scan(&immediateTypes); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(immediateTypes, []string{
		"order.created", "order.filled", "position.opened",
	}) {
		t.Fatalf("immediate-fill realtime sequence = %v", immediateTypes)
	}
	var orderData, positionData []byte
	if err := pool.QueryRow(context.Background(), `
		SELECT data
		  FROM realtime.publications
		 WHERE channel = 'user:immediate-realtime'
		   AND event_type = 'order.filled'`,
	).Scan(&orderData); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(context.Background(), `
		SELECT data
		  FROM realtime.publications
		 WHERE channel = 'user:immediate-realtime'
		   AND event_type = 'position.opened'`,
	).Scan(&positionData); err != nil {
		t.Fatal(err)
	}
	var realtimeOrder edge.OrderView
	var realtimePosition edge.PositionView
	if err := json.Unmarshal(orderData, &realtimeOrder); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(positionData, &realtimePosition); err != nil {
		t.Fatal(err)
	}
	views := platformpostgres.NewCompatibilityStore(pool)
	restOrders, err := views.Orders(context.Background(), accountID)
	if err != nil {
		t.Fatal(err)
	}
	restPositions, err := views.Positions(context.Background(), accountID)
	if err != nil {
		t.Fatal(err)
	}
	if len(restOrders) != 1 || !reflect.DeepEqual(realtimeOrder, restOrders[0]) ||
		len(restPositions) != 1 ||
		!reflect.DeepEqual(realtimePosition, restPositions[0]) {
		t.Fatalf(
			"realtime order/position = %+v/%+v REST = %+v/%+v",
			realtimeOrder,
			realtimePosition,
			restOrders,
			restPositions,
		)
	}
	if report, err := store.ReconcileShard(context.Background(), 29); err != nil ||
		report.RealtimeMismatchCount != 0 {
		t.Fatalf("clean realtime reconciliation report=%+v error=%v", report, err)
	}
	if _, err := pool.Exec(context.Background(), `
		UPDATE realtime.channel_sequences
		   SET last_sequence = last_sequence + 1
		 WHERE channel = 'user:immediate-realtime'`,
	); err != nil {
		t.Fatal(err)
	}
	report, err := store.ReconcileShard(context.Background(), 29)
	if !errors.Is(err, platformpostgres.ErrReconciliationMismatch) ||
		report.RealtimeMismatchCount == 0 {
		t.Fatalf("corrupt realtime reconciliation report=%+v error=%v", report, err)
	}
}

func TestRestingTriggerThenSameTimeAmendPublishesTriggeredOnce(t *testing.T) {
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := platformpostgres.NewMigrator(
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).MigrateAndProvision(context.Background(), 30); err != nil {
		t.Fatal(err)
	}
	store := platformpostgres.NewEngineStore(pool)
	state := engine.NewState(30)
	ids := testkit.NewShardIDSequence(30)
	clock := testkit.NewManualClock(
		engine.NewLogicalTime(time.Date(2026, time.July, 25, 20, 0, 0, 0, time.UTC)),
	)
	const accountID = "urn:xb:account:trigger-realtime"
	state = configureRealtimeAccount(t, pool, store, state, ids, clock, accountID)
	if _, err := pool.Exec(context.Background(), `
		WITH inserted_user AS (
			INSERT INTO identity.users (user_id, login, normalized_login)
			VALUES (
				'urn:xb:user:trigger-realtime',
				'trigger-realtime',
				'trigger-realtime'
			)
			RETURNING user_id
		)
		INSERT INTO identity.user_accounts (user_id, account_id)
		SELECT user_id, $1 FROM inserted_user`,
		accountID,
	); err != nil {
		t.Fatal(err)
	}
	orderID := engine.IDFromSequence(engine.ID{}, 3_001)
	state, _, _, _ = applyStoredTrading(
		t, pool, store, state, ids, clock,
		engine.TradingAction{
			Kind: engine.TradingActionSubmitOrder,
			SubmitOrder: &engine.SubmitOrder{
				OrderID: orderID, AccountID: accountID,
				InstrumentID: "BTC-PERP", Side: engine.SideBuy,
				Type: engine.OrderTypeStopLimit, TimeInForce: engine.TimeInForceGTC,
				Quantity: "1", Price: "101", TriggerPrice: "105",
			},
		},
		platformpostgres.ApplyOptions{},
	)
	triggerTime := clock.Now()
	state, _, _, _ = applyStoredTrading(
		t, pool, store, state, ids, clock,
		engine.TradingAction{
			Kind: engine.TradingActionUpdateBook,
			UpdateBook: &engine.UpdateBook{
				InstrumentID: "BTC-PERP", MarkPrice: "105",
				Bids: []engine.BookLevel{{Price: "104", Quantity: "10"}},
				Asks: []engine.BookLevel{{Price: "105", Quantity: "10"}},
			},
		},
		platformpostgres.ApplyOptions{},
	)
	clock.Set(triggerTime)
	_, _, _, _ = applyStoredTrading(
		t, pool, store, state, ids, clock,
		engine.TradingAction{
			Kind: engine.TradingActionAmendOrder,
			AmendOrder: &engine.AmendOrder{
				AccountID: accountID, OrderID: orderID,
				Quantity: "1", Price: "100",
			},
		},
		platformpostgres.ApplyOptions{},
	)
	var eventTypes []string
	if err := pool.QueryRow(context.Background(), `
		SELECT array_agg(event_type ORDER BY sequence)
		  FROM realtime.publications
		 WHERE channel = 'user:trigger-realtime'`,
	).Scan(&eventTypes); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(eventTypes, []string{
		"order.created", "order.triggered", "order.updated",
	}) {
		t.Fatalf("trigger/amend realtime sequence = %v", eventTypes)
	}
}

func realtimeStringPointer(value string) *string {
	return &value
}

func claimRealtimeConcurrently(
	t *testing.T,
	store *platformpostgres.RealtimeStore,
) platformpostgres.RealtimePublication {
	t.Helper()
	type result struct {
		publications []platformpostgres.RealtimePublication
		err          error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	for range 2 {
		go func() {
			<-start
			publications, err := store.ClaimRealtimeBatch(
				context.Background(),
				10,
				30*time.Second,
			)
			results <- result{publications: publications, err: err}
		}()
	}
	close(start)
	var claimed []platformpostgres.RealtimePublication
	for range 2 {
		got := <-results
		if got.err != nil {
			t.Fatalf("concurrent realtime claim: %v", got.err)
		}
		claimed = append(claimed, got.publications...)
	}
	if len(claimed) != 1 {
		t.Fatalf("concurrent claims returned %d publications, want 1", len(claimed))
	}
	return claimed[0]
}

func configureRealtimeAccount(
	t *testing.T,
	pool *pgxpool.Pool,
	store *platformpostgres.EngineStore,
	state engine.State,
	ids *testkit.IDSequence,
	clock *testkit.ManualClock,
	accountID string,
) engine.State {
	t.Helper()
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
			AccountID: accountID,
			OmsMode:   engine.OmsModeNetting,
		},
	}, platformpostgres.ApplyOptions{})
	state, _, _, _ = applyStoredTrading(t, pool, store, state, ids, clock, engine.TradingAction{
		Kind: engine.TradingActionAdjustBalance,
		AdjustBalance: &engine.AdjustBalance{
			AccountID:     accountID,
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
	return state
}

func claimOneRealtime(
	t *testing.T,
	store *platformpostgres.RealtimeStore,
) platformpostgres.RealtimePublication {
	t.Helper()
	publications, err := store.ClaimRealtimeBatch(
		context.Background(),
		10,
		30*time.Second,
	)
	if err != nil {
		t.Fatalf("claim realtime publication: %v", err)
	}
	if len(publications) != 1 {
		t.Fatalf("claimed realtime publications = %d, want 1", len(publications))
	}
	return publications[0]
}
