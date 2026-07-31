package postgres_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
	"github.com/upcomers-org/platformgo/internal/application"
	"github.com/upcomers-org/platformgo/internal/edge"
	"github.com/upcomers-org/platformgo/internal/engine"
)

type orderIntentIdempotencyClock struct {
	value time.Time
}

func (clock orderIntentIdempotencyClock) Now() time.Time {
	return clock.value
}

type orderIntentIdempotencyCounts struct {
	idempotencyRecords int
	commands           int
	replayResponses    int
	orderIntents       int
	outbox             int
	accountShards      int
	inputReceipts      int
	duplicateReceipts  int
	ledgerTransactions int
	ledgerEntries      int
	orders             int
	fills              int
	positions          int
	fundingSettlements int
	fundingHistory     int
	engineOutboxFacts  int
	realtimeSequences  int
	realtimeFacts      int
	realtimeRequeues   int
}

type orderIntentBalance struct {
	total          string
	used           string
	free           string
	equity         string
	ledgerSequence uint64
}

// Ported from:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/trading/e2e_orders.rs:154
//	test: a_reused_intent_id_replays_only_an_identical_payload
//
// Adaptations:
//
//   - The source application fixture and (account_id, intent_id) order insert
//     are replaced by current-Go application.OrderSubmission and the real
//     PostgreSQL 19 Beta 2 CommandJournal using key "intent:idem-1".
//   - Generated source identities and runtime time are replaced by the current
//     Go stable-ID derivation and explicit clocks.
//   - The identical retry is reconstructed from persisted state through a
//     fresh journal/application instance, then repeated after readiness drain.
//   - Concurrent first-flight callers replace implementation-specific async
//     plumbing and synchronize without sleeps.
//
// Assertions preserved:
//
//   - Reusing intent "idem-1" for the identical LIMIT BUY quantity 0.010 at
//     50000 replays the original order identity.
//   - Reusing that mapped key for a different LIMIT SELL quantity 0.020 at
//     80000 returns the typed idempotency conflict.
//
// Additional current-Go assertions:
//
//   - Admission persists one canonical command/replay/order-intent/outbox graph
//     and one account-shard binding, with exact response and ordered metadata.
//   - Admission alone leaves funded balance and all engine/economic/realtime
//     facts unchanged.
//   - Persisted identical replay and changed-payload conflict precede runtime
//     readiness, while a new key fails closed after drain without mutation.
func TestOrderIntentIdempotencyPersistsOneReplayableAdmissionGraph(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	rootPool := postgresPool(t)
	resetDurableSchemas(t, rootPool)
	t.Cleanup(func() {
		dropDurableSchemas(t, rootPool)
	})

	var serverVersion string
	if err := rootPool.QueryRow(
		ctx,
		"SELECT current_setting('server_version')",
	).Scan(&serverVersion); err != nil {
		t.Fatalf("read PostgreSQL server version: %v", err)
	}
	if serverVersion != "19beta2" &&
		!strings.HasPrefix(serverVersion, "19beta2 ") {
		t.Fatalf(
			"PostgreSQL server version = %q, want PostgreSQL 19 Beta 2",
			serverVersion,
		)
	}
	t.Logf("PostgreSQL server version: %s", serverVersion)

	const shardID engine.ShardID = 7
	if err := newCurrentTestMigrator(
		t,
		rootPool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).MigrateAndProvision(ctx, shardID); err != nil {
		t.Fatalf("migrate and provision PostgreSQL: %v", err)
	}

	const (
		accountID    = "urn:xb:account:idem-1"
		userID       = "urn:xb:user:idem-1"
		instrumentID = "BTC-PERP"
	)
	if _, err := rootPool.Exec(ctx, `
		INSERT INTO trading.accounts (account_id, oms_mode)
		VALUES ($1, 'NETTING')`,
		accountID,
	); err != nil {
		t.Fatalf("seed idempotency account: %v", err)
	}
	if _, err := rootPool.Exec(ctx, `
		INSERT INTO identity.users (
			user_id, login, normalized_login, email, normalized_email
		) VALUES (
			$1, 'idem-1', 'idem-1', 'idem-1@example.com',
			'idem-1@example.com'
		)`,
		userID,
	); err != nil {
		t.Fatalf("seed idempotency user: %v", err)
	}
	if _, err := rootPool.Exec(ctx, `
		INSERT INTO identity.user_accounts (user_id, account_id)
		VALUES ($1, $2)`,
		userID,
		accountID,
	); err != nil {
		t.Fatalf("seed idempotency account ownership: %v", err)
	}
	if _, err := rootPool.Exec(ctx, `
		INSERT INTO trading.instruments (
			instrument_id, revision, price_scale, quantity_scale,
			settlement_currency, settlement_currency_scale,
			initial_margin_rate, maintenance_margin_rate, max_leverage,
			maker_fee_rate, taker_fee_rate
		) VALUES (
			$1, 3, 2, 3, 'USDC', 2,
			0.1, 0.05, 10, -0.0001, 0.0005
		)`,
		instrumentID,
	); err != nil {
		t.Fatalf("seed idempotency instrument: %v", err)
	}
	if _, err := rootPool.Exec(ctx, `
		INSERT INTO ledger.balances (
			account_id, currency, total, used, free, equity, ledger_sequence
		) VALUES (
			$1, 'USDC', 1000000000, 0, 1000000000, 1000000000, 0
		)`,
		accountID,
	); err != nil {
		t.Fatalf("seed idempotency funded balance: %v", err)
	}

	engineStore := platformpostgres.NewEngineStore(rootPool)
	ownership, err := engineStore.AcquireShardOwnership(ctx, shardID)
	if err != nil {
		t.Fatalf("acquire engine shard ownership: %v", err)
	}
	t.Cleanup(func() {
		if ownership != nil {
			_ = ownership.Close(context.Background())
		}
	})
	engineReady, err := engineStore.AcquireEngineReady(ctx, shardID)
	if err != nil {
		t.Fatalf("acquire engine readiness: %v", err)
	}
	t.Cleanup(func() {
		if engineReady != nil {
			_ = engineReady.Close(context.Background())
		}
	})
	messagingStore := platformpostgres.NewMessagingStore(rootPool)
	outboxOwnership, err := messagingStore.AcquireOutboxPublisher(ctx)
	if err != nil {
		t.Fatalf("acquire outbox ownership: %v", err)
	}
	t.Cleanup(func() {
		_ = outboxOwnership.Close(context.Background())
	})
	outboxReady, err := messagingStore.AcquireOutboxReady(ctx)
	if err != nil {
		t.Fatalf("acquire outbox readiness: %v", err)
	}
	t.Cleanup(func() {
		_ = outboxReady.Close(context.Background())
	})

	apiPool := postgresRolePool(t, "platformgo_api")
	assertCurrentRole(t, apiPool, "platformgo_api")
	readinessStore := platformpostgres.NewCompatibilityStore(apiPool)
	readiness := func(checkContext context.Context) error {
		return readinessStore.RuntimeCommandReady(checkContext, shardID)
	}
	if err := readiness(ctx); err != nil {
		t.Fatalf("seeded runtime is not ready: %v", err)
	}

	beforeBalance := readOrderIntentBalance(t, rootPool, accountID)
	wantBalance := orderIntentBalance{
		total:          "1000000000.000000000000000000",
		used:           "0.000000000000000000",
		free:           "1000000000.000000000000000000",
		equity:         "1000000000.000000000000000000",
		ledgerSequence: 0,
	}
	if beforeBalance != wantBalance {
		t.Fatalf("seeded funded balance = %#v, want %#v", beforeBalance, wantBalance)
	}

	firstClock := time.Date(
		2026,
		time.July,
		29,
		13,
		14,
		15,
		123456000,
		time.UTC,
	)
	firstSubmission := newOrderIntentSubmission(
		t,
		apiPool,
		shardID,
		firstClock,
		readiness,
	)
	principal := edge.Principal{
		Subject:  userID,
		Audience: edge.AudienceClient,
		Accounts: []string{accountID},
	}
	firstRequest := orderIntentRequest("idem-1", "buy", "0.010", "50000")
	const idempotencyKey = "intent:idem-1"

	type submitResult struct {
		admission edge.OrderAdmission
		err       error
	}
	const callers = 8
	start := make(chan struct{})
	results := make(chan submitResult, callers)
	var group sync.WaitGroup
	group.Add(callers)
	for range callers {
		go func() {
			defer group.Done()
			<-start
			admission, submitErr := firstSubmission.SubmitOrder(
				ctx,
				principal,
				accountID,
				idempotencyKey,
				firstRequest,
			)
			results <- submitResult{admission: admission, err: submitErr}
		}()
	}
	close(start)
	group.Wait()
	close(results)

	var first edge.OrderAdmission
	for result := range results {
		if result.err != nil {
			t.Fatalf("concurrent identical first flight: %v", result.err)
		}
		if first.OrderID == "" {
			first = result.admission
			continue
		}
		if !reflect.DeepEqual(result.admission, first) {
			t.Fatalf(
				"concurrent admission = %#v, want exact %#v",
				result.admission,
				first,
			)
		}
	}

	scope := strings.Join(
		[]string{
			string(principal.Audience),
			principal.Subject,
			"POST",
			"/v1/accounts/" + accountID + "/orders",
		},
		"\x1f",
	)
	canonicalRequest := []byte(
		`{"intentId":"idem-1","symbol":"BTC-PERP","side":"BUY","type":"LIMIT","quantity":"0.01","price":"50000","reduceOnly":false,"timeInForce":"GTC"}`,
	)
	requestHash := sha256.Sum256(canonicalRequest)
	wantCommandID := orderIntentStableID(
		"platformgo.command.v1",
		scope,
		idempotencyKey,
		requestHash,
	)
	wantOrderID := orderIntentStableID(
		"platformgo.order.v1",
		scope,
		idempotencyKey,
		requestHash,
	)
	wantBody := []byte(fmt.Sprintf(
		"{\"orderId\":\"urn:xb:order:%s\",\"intentId\":\"idem-1\"}\n",
		wantOrderID,
	))
	wantAdmission := edge.OrderAdmission{
		OrderAccepted: edge.OrderAccepted{
			OrderID:  "urn:xb:order:" + wantOrderID.String(),
			IntentID: "idem-1",
		},
		Response: edge.StoredResponse{
			Status:  202,
			Headers: []byte(`{"content-type":["application/json"]}`),
			Body:    wantBody,
		},
	}
	if !reflect.DeepEqual(first, wantAdmission) {
		t.Fatalf("first admission = %#v, want exact %#v", first, wantAdmission)
	}

	assertOrderIntentAdmissionGraph(
		t,
		rootPool,
		scope,
		idempotencyKey,
		requestHash,
		wantCommandID,
		wantOrderID,
		accountID,
		firstClock,
		firstClock.Add(24*time.Hour),
		wantAdmission,
	)
	afterFirst := readOrderIntentCounts(t, rootPool)
	wantAfterFirst := orderIntentIdempotencyCounts{
		idempotencyRecords: 1,
		commands:           1,
		replayResponses:    1,
		orderIntents:       1,
		outbox:             1,
		accountShards:      1,
	}
	if afterFirst != wantAfterFirst {
		t.Fatalf("first admission counts = %#v, want %#v", afterFirst, wantAfterFirst)
	}
	proveOrderIntentForbiddenEffectDetection(
		t,
		rootPool,
		afterFirst,
		accountID,
	)
	if afterBalance := readOrderIntentBalance(t, rootPool, accountID); afterBalance != beforeBalance {
		t.Fatalf(
			"admission changed funded balance from %#v to %#v",
			beforeBalance,
			afterBalance,
		)
	}

	restartClock := time.Date(2031, time.January, 2, 3, 4, 5, 6, time.UTC)
	restartedSubmission := newOrderIntentSubmission(
		t,
		apiPool,
		shardID,
		restartClock,
		readiness,
	)
	replayed, err := restartedSubmission.SubmitOrder(
		ctx,
		principal,
		accountID,
		idempotencyKey,
		firstRequest,
	)
	if err != nil {
		t.Fatalf("persisted retry through fresh application: %v", err)
	}
	if !reflect.DeepEqual(replayed, wantAdmission) {
		t.Fatalf("persisted replay = %#v, want exact %#v", replayed, wantAdmission)
	}
	requireOrderIntentCounts(
		t,
		rootPool,
		afterFirst,
		"fresh-instance identical replay",
	)

	changedRequest := orderIntentRequest("idem-1", "sell", "0.020", "80000")
	if _, err := restartedSubmission.SubmitOrder(
		ctx,
		principal,
		accountID,
		idempotencyKey,
		changedRequest,
	); !errors.Is(err, edge.ErrIdempotencyConflict) {
		t.Fatalf(
			"changed LIMIT SELL error = %v, want edge.ErrIdempotencyConflict",
			err,
		)
	}
	requireOrderIntentCounts(t, rootPool, afterFirst, "changed-payload conflict")

	if err := engineReady.Close(ctx); err != nil {
		t.Fatalf("drain engine readiness: %v", err)
	}
	engineReady = nil
	if err := readiness(ctx); !errors.Is(err, platformpostgres.ErrRuntimeNotReady) {
		t.Fatalf("drained readiness error = %v, want ErrRuntimeNotReady", err)
	}

	drainedReplay, err := restartedSubmission.SubmitOrder(
		ctx,
		principal,
		accountID,
		idempotencyKey,
		firstRequest,
	)
	if err != nil || !reflect.DeepEqual(drainedReplay, wantAdmission) {
		t.Fatalf(
			"drained exact replay = %#v, error = %v, want %#v",
			drainedReplay,
			err,
			wantAdmission,
		)
	}
	if _, err := restartedSubmission.SubmitOrder(
		ctx,
		principal,
		accountID,
		idempotencyKey,
		changedRequest,
	); !errors.Is(err, edge.ErrIdempotencyConflict) {
		t.Fatalf(
			"drained changed request error = %v, want edge.ErrIdempotencyConflict",
			err,
		)
	}
	newRequest := orderIntentRequest("new-intent", "buy", "0.010", "50000")
	if _, err := restartedSubmission.SubmitOrder(
		ctx,
		principal,
		accountID,
		"intent:new-intent",
		newRequest,
	); !errors.Is(err, platformpostgres.ErrRuntimeNotReady) {
		t.Fatalf("drained new-key error = %v, want ErrRuntimeNotReady", err)
	}
	requireOrderIntentCounts(t, rootPool, afterFirst, "drained replay/conflict/new key")
	if finalBalance := readOrderIntentBalance(t, rootPool, accountID); finalBalance != beforeBalance {
		t.Fatalf(
			"idempotency flow changed funded balance from %#v to %#v",
			beforeBalance,
			finalBalance,
		)
	}
}

func newOrderIntentSubmission(
	t *testing.T,
	pool *pgxpool.Pool,
	shardID engine.ShardID,
	now time.Time,
	readiness func(context.Context) error,
) *application.OrderSubmission {
	t.Helper()
	submission, err := application.NewOrderSubmission(
		platformpostgres.NewCommandJournal(pool),
		application.OrderSubmissionConfig{
			ShardID:        shardID,
			IdempotencyTTL: 24 * time.Hour,
			Clock:          orderIntentIdempotencyClock{value: now},
			Readiness:      readiness,
		},
	)
	if err != nil {
		t.Fatalf("construct order submission: %v", err)
	}
	return submission
}

func orderIntentRequest(
	intentID string,
	side string,
	quantity string,
	price string,
) edge.SubmitOrderRequest {
	return edge.SubmitOrderRequest{
		IntentID: intentID,
		Symbol:   "BTC-PERP",
		Side:     side,
		Type:     "LIMIT",
		Quantity: quantity,
		Price:    &price,
	}
}

func orderIntentStableID(
	label string,
	scope string,
	key string,
	requestHash [sha256.Size]byte,
) engine.ID {
	hasher := sha256.New()
	_, _ = hasher.Write([]byte(label))
	_, _ = hasher.Write([]byte{0})
	_, _ = hasher.Write([]byte(scope))
	_, _ = hasher.Write([]byte{0})
	_, _ = hasher.Write([]byte(key))
	_, _ = hasher.Write([]byte{0})
	_, _ = hasher.Write(requestHash[:])
	sum := hasher.Sum(nil)
	var id engine.ID
	copy(id[:], sum[:len(id)])
	id[6] = (id[6] & 0x0f) | 0x40
	id[8] = (id[8] & 0x3f) | 0x80
	return id
}

func assertOrderIntentAdmissionGraph(
	t *testing.T,
	pool *pgxpool.Pool,
	scope string,
	idempotencyKey string,
	requestHash [sha256.Size]byte,
	commandID engine.ID,
	orderID engine.ID,
	accountID string,
	logicalTime time.Time,
	expiresAt time.Time,
	admission edge.OrderAdmission,
) {
	t.Helper()
	var (
		gotScope             string
		gotKey               string
		gotRequestHash       string
		idempotencyState     string
		gotExpiresAt         time.Time
		commandIDText        string
		commandAccount       string
		accountSequence      uint64
		commandType          string
		commandSchemaVersion uint32
		canonicalPayload     []byte
		commandStatus        string
		commandLogicalTime   int64
		marketBinding        string
		responseStatus       int
		responseHeaders      []byte
		responseBody         []byte
		intentOrderID        string
		intentCommandID      string
		intentAccountID      string
		intentID             string
		outboxMessageID      string
		outboxSubject        string
		outboxSchemaVersion  uint32
		outboxPayload        []byte
		outboxProducerClass  string
		outboxEngineShardID  *int64
		outboxEngineInputID  *string
		outboxAttempts       int
		outboxUnpublished    bool
		shardAccountID       string
		gotShardID           uint32
	)
	if err := pool.QueryRow(context.Background(), `
		SELECT
			idempotency.scope,
			idempotency.idempotency_key,
			encode(idempotency.request_hash, 'hex'),
			idempotency.state,
			idempotency.expires_at,
			command.command_id::text,
			command.account_id,
			command.account_sequence,
			command.command_type,
			command.schema_version,
			command.canonical_payload,
			command.status,
			command.logical_time,
			command.market_sequence_binding,
			replay.response_status,
			replay.response_headers,
			replay.response_body,
			intent.order_id::text,
			intent.command_id::text,
			intent.account_id,
			intent.intent_id,
			outbox.message_id::text,
			outbox.subject,
			outbox.schema_version,
			outbox.payload,
			outbox.producer_class,
			outbox.engine_shard_id,
			outbox.engine_input_id::text,
			outbox.attempts,
			outbox.published_at IS NULL,
			shard.account_id,
			shard.shard_id
		  FROM trading.idempotency_records AS idempotency
		  JOIN trading.commands AS command
		    ON command.command_id = idempotency.command_id
		  JOIN trading.command_replay_responses AS replay
		    ON replay.command_id = command.command_id
		  JOIN trading.order_intents AS intent
		    ON intent.command_id = command.command_id
		  JOIN messaging.outbox AS outbox
		    ON outbox.message_id = command.command_id
		  JOIN engine.account_shards AS shard
		    ON shard.account_id = command.account_id
		 WHERE idempotency.scope = $1
		   AND idempotency.idempotency_key = $2`,
		scope,
		idempotencyKey,
	).Scan(
		&gotScope,
		&gotKey,
		&gotRequestHash,
		&idempotencyState,
		&gotExpiresAt,
		&commandIDText,
		&commandAccount,
		&accountSequence,
		&commandType,
		&commandSchemaVersion,
		&canonicalPayload,
		&commandStatus,
		&commandLogicalTime,
		&marketBinding,
		&responseStatus,
		&responseHeaders,
		&responseBody,
		&intentOrderID,
		&intentCommandID,
		&intentAccountID,
		&intentID,
		&outboxMessageID,
		&outboxSubject,
		&outboxSchemaVersion,
		&outboxPayload,
		&outboxProducerClass,
		&outboxEngineShardID,
		&outboxEngineInputID,
		&outboxAttempts,
		&outboxUnpublished,
		&shardAccountID,
		&gotShardID,
	); err != nil {
		t.Fatalf("read joined order-intent admission graph: %v", err)
	}

	if gotScope != scope ||
		gotKey != idempotencyKey ||
		gotRequestHash != hex.EncodeToString(requestHash[:]) ||
		idempotencyState != "in_progress" ||
		!gotExpiresAt.Equal(expiresAt) {
		t.Fatalf(
			"idempotency record = scope %q key %q hash %q state %q expires %s",
			gotScope,
			gotKey,
			gotRequestHash,
			idempotencyState,
			gotExpiresAt,
		)
	}
	if commandIDText != commandID.String() ||
		commandAccount != accountID ||
		accountSequence != 1 ||
		commandType != string(engine.TradingActionSubmitOrder) ||
		commandSchemaVersion != engine.CurrentSchemaVersion ||
		commandStatus != "pending" ||
		commandLogicalTime != logicalTime.UnixNano() ||
		marketBinding != "ordered" {
		t.Fatalf(
			"command = id %q account %q sequence %d type %q schema %d status %q logical %d binding %q",
			commandIDText,
			commandAccount,
			accountSequence,
			commandType,
			commandSchemaVersion,
			commandStatus,
			commandLogicalTime,
			marketBinding,
		)
	}
	wantCanonicalPayload, err := engine.EncodeTradingAction(engine.TradingAction{
		Kind: engine.TradingActionSubmitOrder,
		SubmitOrder: &engine.SubmitOrder{
			OrderID:      orderID,
			AccountID:    accountID,
			InstrumentID: "BTC-PERP",
			Side:         engine.SideBuy,
			Type:         engine.OrderTypeLimit,
			TimeInForce:  engine.TimeInForceGTC,
			Quantity:     "0.01",
			Price:        "50000",
		},
	})
	if err != nil {
		t.Fatalf("encode expected canonical command payload: %v", err)
	}
	requireOrderIntentJSON(
		t,
		canonicalPayload,
		wantCanonicalPayload.Bytes(),
		"canonical command payload",
	)

	if responseStatus != admission.Response.Status ||
		!bytes.Equal(responseBody, admission.Response.Body) {
		t.Fatalf(
			"persisted replay response = status %d body %q, want %d %q",
			responseStatus,
			responseBody,
			admission.Response.Status,
			admission.Response.Body,
		)
	}
	requireOrderIntentJSON(
		t,
		responseHeaders,
		admission.Response.Headers,
		"canonical response headers",
	)
	if intentOrderID != orderID.String() ||
		intentCommandID != commandID.String() ||
		intentAccountID != accountID ||
		intentID != "idem-1" {
		t.Fatalf(
			"order intent = order %q command %q account %q intent %q",
			intentOrderID,
			intentCommandID,
			intentAccountID,
			intentID,
		)
	}
	if outboxMessageID != commandID.String() ||
		outboxSubject != fmt.Sprintf(
			"engine.input.%d.command.v%d",
			gotShardID,
			engine.CurrentSchemaVersion,
		) ||
		outboxSchemaVersion != engine.CurrentSchemaVersion ||
		outboxProducerClass != "api" ||
		outboxEngineShardID != nil ||
		outboxEngineInputID != nil ||
		outboxAttempts != 0 ||
		!outboxUnpublished ||
		shardAccountID != accountID ||
		gotShardID != 7 {
		t.Fatalf(
			"outbox/shard = message %q subject %q schema %d producer %q engine shard/input %v/%v attempts %d unpublished %t account %q shard %d",
			outboxMessageID,
			outboxSubject,
			outboxSchemaVersion,
			outboxProducerClass,
			outboxEngineShardID,
			outboxEngineInputID,
			outboxAttempts,
			outboxUnpublished,
			shardAccountID,
			gotShardID,
		)
	}
	input, action, err := engine.DecodeInputMessage(outboxPayload)
	if err != nil {
		t.Fatalf("decode persisted outbox input: %v", err)
	}
	if input.InputID != commandID ||
		input.SchemaVersion != engine.CurrentSchemaVersion ||
		input.ShardID != 7 ||
		input.Kind != engine.InputKindCommand ||
		input.SourceID != "urn:xb:user:idem-1" ||
		input.SourceSequence != 1 ||
		input.StreamSequence != 0 ||
		input.MarketSequence != 0 ||
		input.LogicalTime.UnixNano() != logicalTime.UnixNano() ||
		input.ConfigurationVersion != 1 ||
		input.InstrumentVersion != 3 {
		t.Fatalf("persisted outbox input = %#v", input)
	}
	if action.Kind != engine.TradingActionSubmitOrder ||
		action.SubmitOrder == nil ||
		action.SubmitOrder.OrderID != orderID ||
		action.SubmitOrder.AccountID != accountID ||
		action.SubmitOrder.InstrumentID != "BTC-PERP" ||
		action.SubmitOrder.Side != engine.SideBuy ||
		action.SubmitOrder.Type != engine.OrderTypeLimit ||
		action.SubmitOrder.TimeInForce != engine.TimeInForceGTC ||
		action.SubmitOrder.Quantity != "0.01" ||
		action.SubmitOrder.Price != "50000" ||
		action.SubmitOrder.TriggerPrice != "" ||
		action.SubmitOrder.ReduceOnly ||
		!action.SubmitOrder.PositionID.IsZero() ||
		action.SubmitOrder.MaxSlippageBPS != nil {
		t.Fatalf("persisted canonical order action = %#v", action.SubmitOrder)
	}
}

func requireOrderIntentJSON(
	t *testing.T,
	got []byte,
	want []byte,
	label string,
) {
	t.Helper()
	var gotValue any
	if err := json.Unmarshal(got, &gotValue); err != nil {
		t.Fatalf("decode %s %q: %v", label, got, err)
	}
	var wantValue any
	if err := json.Unmarshal(want, &wantValue); err != nil {
		t.Fatalf("decode expected %s %q: %v", label, want, err)
	}
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Fatalf("%s = %s, want %s", label, got, want)
	}
}

type orderIntentQueryRower interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func readOrderIntentCounts(
	t *testing.T,
	queryer orderIntentQueryRower,
) orderIntentIdempotencyCounts {
	t.Helper()
	var counts orderIntentIdempotencyCounts
	if err := queryer.QueryRow(context.Background(), `
		SELECT
			(SELECT count(*) FROM trading.idempotency_records),
			(SELECT count(*) FROM trading.commands),
			(SELECT count(*) FROM trading.command_replay_responses),
			(SELECT count(*) FROM trading.order_intents),
			(SELECT count(*) FROM messaging.outbox),
			(SELECT count(*) FROM engine.account_shards),
			(SELECT count(*) FROM engine.input_receipts),
			(SELECT count(*) FROM engine.duplicate_delivery_receipts),
			(SELECT count(*) FROM ledger.transactions),
			(SELECT count(*) FROM ledger.entries),
			(SELECT count(*) FROM trading.orders),
			(SELECT count(*) FROM trading.fills),
			(SELECT count(*) FROM trading.positions),
			(SELECT count(*) FROM trading.funding_settlements),
			(SELECT count(*) FROM trading.funding_history_projection),
			(SELECT count(*)
			   FROM messaging.outbox
			  WHERE producer_class = 'engine'),
			(SELECT count(*) FROM realtime.channel_sequences),
			(SELECT count(*) FROM realtime.publications),
			(SELECT count(*) FROM realtime.publication_requeues)`,
	).Scan(
		&counts.idempotencyRecords,
		&counts.commands,
		&counts.replayResponses,
		&counts.orderIntents,
		&counts.outbox,
		&counts.accountShards,
		&counts.inputReceipts,
		&counts.duplicateReceipts,
		&counts.ledgerTransactions,
		&counts.ledgerEntries,
		&counts.orders,
		&counts.fills,
		&counts.positions,
		&counts.fundingSettlements,
		&counts.fundingHistory,
		&counts.engineOutboxFacts,
		&counts.realtimeSequences,
		&counts.realtimeFacts,
		&counts.realtimeRequeues,
	); err != nil {
		t.Fatalf("count order-intent admission facts: %v", err)
	}
	return counts
}

func proveOrderIntentForbiddenEffectDetection(
	t *testing.T,
	pool *pgxpool.Pool,
	baseline orderIntentIdempotencyCounts,
	accountID string,
) {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin forbidden-effect detector fixture: %v", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()
	if _, err := tx.Exec(ctx, `
		INSERT INTO trading.funding_settlements (
			funding_id,
			settlement_id,
			position_id,
			input_id,
			account_id,
			instrument_id,
			signed_quantity,
			oracle_price,
			rate,
			amount,
			settlement_currency
		) VALUES (
			'00000000-0000-4000-8000-00000000f001',
			'00000000-0000-4000-8000-00000000f002',
			'00000000-0000-4000-8000-00000000f003',
			'00000000-0000-4000-8000-00000000f004',
			$1,
			'BTC-PERP',
			0.01,
			50000,
			0.0001,
			0.05,
			'USDC'
		)`,
		accountID,
	); err != nil {
		t.Fatalf("seed forbidden funding effect: %v", err)
	}
	got := readOrderIntentCounts(t, tx)
	if got == baseline ||
		got.fundingSettlements != baseline.fundingSettlements+1 {
		t.Fatalf(
			"forbidden-effect detector did not observe funding row: got %#v baseline %#v",
			got,
			baseline,
		)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("roll back forbidden-effect detector fixture: %v", err)
	}
	requireOrderIntentCounts(
		t,
		pool,
		baseline,
		"forbidden-effect detector rollback",
	)
}

func requireOrderIntentCounts(
	t *testing.T,
	pool *pgxpool.Pool,
	want orderIntentIdempotencyCounts,
	operation string,
) {
	t.Helper()
	if got := readOrderIntentCounts(t, pool); got != want {
		t.Fatalf("%s counts = %#v, want unchanged %#v", operation, got, want)
	}
}

func readOrderIntentBalance(
	t *testing.T,
	pool *pgxpool.Pool,
	accountID string,
) orderIntentBalance {
	t.Helper()
	var balance orderIntentBalance
	if err := pool.QueryRow(context.Background(), `
		SELECT
			total::text,
			used::text,
			free::text,
			equity::text,
			ledger_sequence
		  FROM ledger.balances
		 WHERE account_id = $1
		   AND currency = 'USDC'`,
		accountID,
	).Scan(
		&balance.total,
		&balance.used,
		&balance.free,
		&balance.equity,
		&balance.ledgerSequence,
	); err != nil {
		t.Fatalf("read funded balance: %v", err)
	}
	return balance
}
