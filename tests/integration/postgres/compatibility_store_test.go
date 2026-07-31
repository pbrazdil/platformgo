package postgres_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	platformnats "github.com/upcomers-org/platformgo/internal/adapters/nats"
	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
	"github.com/upcomers-org/platformgo/internal/application"
	"github.com/upcomers-org/platformgo/internal/edge"
	"github.com/upcomers-org/platformgo/internal/engine"
	"github.com/upcomers-org/platformgo/testkit"
)

type compatibilityClock struct{ value time.Time }

func (clock compatibilityClock) Now() time.Time { return clock.value }

func TestPhase3IdentityCatalogAndDurableOrderIntentUsePostgreSQL(t *testing.T) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := newCurrentTestMigrator(
		t,
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).MigrateAndProvision(ctx, 7); err != nil {
		t.Fatal(err)
	}
	passwordHash, err := application.HashPassword(
		"correct horse battery staple",
		bytes.NewReader(bytes.Repeat([]byte{5}, 16)),
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `
		INSERT INTO identity.users (
			user_id, login, normalized_login, email, normalized_email,
			password_hash
		) VALUES (
			'urn:xb:user:trader-1', 'trader1', 'trader1',
			'trader1@example.com', 'trader1@example.com', $1
		), (
			'urn:xb:user:trader-2', 'trader2', 'trader2',
			'trader2@example.com', 'trader2@example.com', $1
		)`,
		passwordHash,
	)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.July, 25, 16, 0, 0, 0, time.UTC)
	engineStore := platformpostgres.NewEngineStore(pool)
	state := engine.NewState(7)
	ids := testkit.NewShardIDSequence(7)
	logicalClock := testkit.NewManualClock(engine.NewLogicalTime(now))
	state, _, _, _ = applyStoredTrading(
		t,
		pool,
		engineStore,
		state,
		ids,
		logicalClock,
		engine.TradingAction{
			Kind: engine.TradingActionConfigureInstrument,
			ConfigureInstrument: &engine.ConfigureInstrument{
				InstrumentID:            "BTC-PERP",
				Revision:                3,
				PriceScale:              2,
				QuantityScale:           3,
				SettlementCurrency:      "USDC",
				SettlementCurrencyScale: 2,
				InitialMarginRate:       "0.1",
				MaintenanceMarginRate:   "0.05",
				MaxLeverage:             "10",
				MakerFeeRate:            "-0.0001",
				TakerFeeRate:            "0.0005",
			},
		},
		platformpostgres.ApplyOptions{},
	)
	state, _, _, _ = applyStoredTrading(
		t,
		pool,
		engineStore,
		state,
		ids,
		logicalClock,
		engine.TradingAction{
			Kind: engine.TradingActionConfigureAccount,
			ConfigureAccount: &engine.ConfigureAccount{
				AccountID: "urn:xb:account:acct-1",
				OmsMode:   engine.OmsModeNetting,
			},
		},
		platformpostgres.ApplyOptions{},
	)
	state, _, _, _ = applyStoredTrading(
		t,
		pool,
		engineStore,
		state,
		ids,
		logicalClock,
		engine.TradingAction{
			Kind: engine.TradingActionAdjustBalance,
			AdjustBalance: &engine.AdjustBalance{
				AccountID:     "urn:xb:account:acct-1",
				Currency:      "USDC",
				CurrencyScale: 2,
				Operation:     engine.BalanceOperationSet,
				Amount:        "1000",
			},
		},
		platformpostgres.ApplyOptions{},
	)
	nextProcessorSequence := state.NextStreamSequence()
	_, err = pool.Exec(ctx, `
		INSERT INTO identity.user_accounts (user_id, account_id)
		VALUES ('urn:xb:user:trader-1', 'urn:xb:account:acct-1')`)
	if err != nil {
		t.Fatal(err)
	}
	authenticator, err := edge.NewHMACAuthenticator(edge.HMACAuthenticatorConfig{
		ClientTokenSecret: []byte("0123456789abcdef0123456789abcdef"),
		Clock:             compatibilityClock{value: now},
	})
	if err != nil {
		t.Fatal(err)
	}
	store := platformpostgres.NewCompatibilityStore(pool)
	identity, err := application.NewIdentity(
		store,
		authenticator,
		application.IdentityConfig{
			Clock:   compatibilityClock{value: now},
			Entropy: bytes.NewReader(bytes.Repeat([]byte{8}, 32)),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	login, err := identity.Login(ctx, edge.LoginRequest{
		Login: "trader1", Password: "correct horse battery staple",
	})
	if err != nil {
		t.Fatal(err)
	}
	principal, err := authenticator.AuthenticateClient(ctx, login.AccessToken)
	if err != nil || !principal.OwnsAccount("urn:xb:account:acct-1") {
		t.Fatalf("principal=%#v err=%v", principal, err)
	}
	instruments, err := store.Instruments(ctx)
	if err != nil || len(instruments) != 1 ||
		instruments[0].Symbol != "BTC-PERP" ||
		instruments[0].PriceIncrement != "0.01" ||
		instruments[0].SizeIncrement != "0.001" ||
		instruments[0].MaxLeverage != "10" ||
		instruments[0].MakerFee != "-0.0001" ||
		instruments[0].TakerFee != "0.0005" {
		t.Fatalf("instruments=%#v err=%v", instruments, err)
	}
	balances, err := store.Balances(ctx, "urn:xb:account:acct-1")
	if err != nil || len(balances) != 1 ||
		balances[0].Total != "1000" ||
		balances[0].Locked != "0" ||
		balances[0].Free != "1000" ||
		balances[0].Equity != "1000" {
		t.Fatalf("balances=%#v err=%v", balances, err)
	}
	submission, err := application.NewOrderSubmission(
		platformpostgres.NewCommandJournal(pool),
		application.OrderSubmissionConfig{
			ShardID:        7,
			IdempotencyTTL: 24 * time.Hour,
			Clock:          compatibilityClock{value: now},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := submission.SubmitOrder(
		ctx,
		principal,
		"urn:xb:account:acct-1",
		"idem-1",
		edge.SubmitOrderRequest{
			IntentID: "intent-1", Symbol: "BTC-PERP", Side: "buy",
			Type: "MARKET", Quantity: "0.001",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	var intentID string
	var commandCount int
	if err := pool.QueryRow(ctx, `
		SELECT intent.intent_id, count(command.command_id) OVER ()
		  FROM trading.order_intents AS intent
		  JOIN trading.commands AS command
		    ON command.command_id = intent.command_id
		 WHERE intent.order_id = $1`,
		accepted.OrderID[len("urn:xb:order:"):],
	).Scan(&intentID, &commandCount); err != nil {
		t.Fatal(err)
	}
	if intentID != "intent-1" || commandCount != 1 {
		t.Fatalf("intent=%q commands=%d", intentID, commandCount)
	}
	orders, err := store.Orders(ctx, "urn:xb:account:acct-1")
	if err != nil || len(orders) != 1 ||
		orders[0].Quantity != "0.001" ||
		orders[0].FilledQuantity != "0" {
		t.Fatalf("pending orders=%#v err=%v", orders, err)
	}

	broker := edge.Principal{
		Subject:  "urn:xb:apikey:partner-1",
		Tenant:   "urn:xb:tenant:partner-1",
		Audience: edge.AudienceBroker,
	}
	echo, err := identity.BrokerEcho(ctx, broker, "echo-key")
	if err != nil {
		t.Fatal(err)
	}
	echoReplay, err := identity.BrokerEcho(ctx, broker, "echo-key")
	if err != nil || !reflect.DeepEqual(echoReplay, echo) {
		t.Fatalf("echo replay=%#v first=%#v err=%v", echoReplay, echo, err)
	}
	otherEcho, err := identity.BrokerEcho(
		ctx,
		edge.Principal{
			Subject:  "urn:xb:apikey:partner-2",
			Tenant:   "urn:xb:tenant:partner-2",
			Audience: edge.AudienceBroker,
		},
		"echo-key",
	)
	if err != nil || reflect.DeepEqual(otherEcho, echo) {
		t.Fatalf("other principal echo=%#v first=%#v err=%v", otherEcho, echo, err)
	}
	brokerUser, err := identity.CreateBrokerUser(
		ctx,
		broker,
		"broker-user-1",
		edge.BrokerUserRequest{
			Login: "partner-user-1",
			Email: "partner-user-1@example.com",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	otherBrokerUser, err := identity.CreateBrokerUser(
		ctx,
		broker,
		"broker-user-2",
		edge.BrokerUserRequest{
			Login: "partner-user-2",
			Email: "partner-user-2@example.com",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	type accountResult struct {
		account edge.BrokerAccountAdmission
		err     error
	}
	accountResultChannel := make(chan accountResult, 1)
	go func() {
		account, createErr := identity.CreateBrokerAccount(
			ctx,
			broker,
			"account-key",
			edge.BrokerAccountRequest{UserID: brokerUser.ID},
		)
		accountResultChannel <- accountResult{account: account, err: createErr}
	}()
	var messages []platformnats.InboundMessage
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	for len(messages) < 2 {
		rows, queryErr := pool.Query(ctx, `
			SELECT outbox.message_id::text, outbox.subject, outbox.payload
			  FROM messaging.outbox AS outbox
			 WHERE outbox.producer_class = 'api'
			   AND NOT EXISTS (
				   SELECT 1
				     FROM engine.input_receipts AS receipt
				    WHERE receipt.input_id = outbox.message_id
			   )
			   AND NOT EXISTS (
				   SELECT 1
				     FROM engine.duplicate_delivery_receipts AS duplicate
				    WHERE duplicate.input_id = outbox.message_id
			   )
			 ORDER BY outbox.created_at, outbox.message_id`)
		if queryErr != nil {
			t.Fatal(queryErr)
		}
		messages = messages[:0]
		for rows.Next() {
			var messageIDText string
			var message platformnats.InboundMessage
			if scanErr := rows.Scan(
				&messageIDText,
				&message.Subject,
				&message.Data,
			); scanErr != nil {
				rows.Close()
				t.Fatal(scanErr)
			}
			message.MessageID, queryErr = engine.ParseID(messageIDText)
			if queryErr != nil {
				rows.Close()
				t.Fatal(queryErr)
			}
			message.StreamSequence = nextProcessorSequence + uint64(len(messages))
			messages = append(messages, message)
		}
		rows.Close()
		if len(messages) >= 2 {
			break
		}
		select {
		case premature := <-accountResultChannel:
			t.Fatalf("account returned before engine commit: %#v", premature)
		case <-deadline.C:
			t.Fatal("account command admission timed out")
		case <-time.After(10 * time.Millisecond):
		}
	}
	select {
	case premature := <-accountResultChannel:
		t.Fatalf("account returned before engine commit: %#v", premature)
	default:
	}
	processor, err := platformnats.NewEngineProcessor(
		ctx,
		platformpostgres.NewEngineStore(pool),
		7,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = processor.Close(context.Background()) }()
	for _, message := range messages {
		if err := processor.Handle(ctx, message); err != nil {
			t.Fatal(err)
		}
	}
	accountOutcome := <-accountResultChannel
	account, err := accountOutcome.account, accountOutcome.err
	if err != nil {
		t.Fatal(err)
	}
	replay, err := identity.CreateBrokerAccount(
		ctx,
		broker,
		"account-key",
		edge.BrokerAccountRequest{UserID: brokerUser.ID},
	)
	if err != nil || replay.ID != account.ID || replay.CreatedAt != account.CreatedAt {
		t.Fatalf("account replay=%#v first=%#v err=%v", replay, account, err)
	}
	if _, err := identity.CreateBrokerAccount(
		ctx,
		broker,
		"account-key",
		edge.BrokerAccountRequest{UserID: otherBrokerUser.ID},
	); !errors.Is(err, edge.ErrIdempotencyConflict) {
		t.Fatalf("account key conflict error = %v", err)
	}
	var admittedCount int
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM trading.commands WHERE account_id = $1)
			+ (SELECT count(*) FROM engine.account_shards WHERE account_id = $1)
			+ (SELECT count(*) FROM identity.account_provisioning_intents WHERE account_id = $1)
			+ (SELECT count(*) FROM messaging.outbox WHERE message_id = (
				SELECT command_id
				  FROM identity.account_provisioning_intents
				 WHERE account_id = $1
			))`,
		account.ID,
	).Scan(&admittedCount); err != nil {
		t.Fatal(err)
	}
	if admittedCount != 4 {
		t.Fatalf("admitted account command graph = %d, want 4", admittedCount)
	}
	var economicProjectionCount int
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM trading.accounts WHERE account_id = $1)
			+ (SELECT count(*) FROM identity.user_accounts WHERE account_id = $1)
			+ (SELECT count(*) FROM identity.account_profiles WHERE account_id = $1)`,
		account.ID,
	).Scan(&economicProjectionCount); err != nil {
		t.Fatal(err)
	}
	if economicProjectionCount != 3 {
		t.Fatalf(
			"account engine projection rows = %d, want 3",
			economicProjectionCount,
		)
	}
}

func TestBrokerEchoClaimConvergesUnderConcurrentDelivery(t *testing.T) {
	ctx := context.Background()
	adminPool := postgresPool(t)
	resetDurableSchemas(t, adminPool)
	if err := newCurrentTestMigrator(
		t,
		adminPool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).MigrateAndProvision(ctx, 7); err != nil {
		t.Fatal(err)
	}
	apiPool := runtimeRoleLoginPool(
		t,
		adminPool,
		"platformgo_broker_echo_race_api_login",
		"platformgo_api",
	)
	store := platformpostgres.NewCompatibilityStore(apiPool)

	t.Run("same request converges on one exact wire response", func(t *testing.T) {
		const principal = "urn:xb:apikey:echo-race-same-request"
		idempotencyHash := [32]byte{0x11, 0x01}
		requestHash := [32]byte{0x21, 0x01}
		candidates := []edge.StoredResponse{
			brokerEchoStoredResponse(
				"019fa9e1-0001-4000-8000-000000000001",
				"wire-a",
			),
			brokerEchoStoredResponse(
				"019fa9e1-0001-4000-8000-000000000002",
				"wire-b",
			),
			brokerEchoStoredResponse(
				"019fa9e1-0001-4000-8000-000000000003",
				"wire-c",
			),
			brokerEchoStoredResponse(
				"019fa9e1-0001-4000-8000-000000000004",
				"wire-d",
			),
		}
		results := concurrentBrokerEchoClaims(
			t,
			store,
			principal,
			idempotencyHash,
			32,
			func(worker int) ([32]byte, edge.StoredResponse) {
				return requestHash, candidates[worker%len(candidates)]
			},
		)
		var winner edge.StoredResponse
		for worker, result := range results {
			if result.err != nil {
				t.Fatalf("worker %d claim: %v", worker, result.err)
			}
			if worker == 0 {
				winner = result.response
			} else if !reflect.DeepEqual(result.response, winner) {
				t.Fatalf(
					"worker %d response = %#v, want exact winner %#v",
					worker,
					result.response,
					winner,
				)
			}
		}
		if !containsBrokerEchoResponse(candidates, winner) {
			t.Fatalf("winner %#v is not one of the submitted wire responses", winner)
		}
		assertBrokerEchoReplayRow(
			t,
			adminPool,
			principal,
			idempotencyHash,
			requestHash,
			winner,
		)
	})

	t.Run("different request hashes yield one winner and deterministic conflicts", func(t *testing.T) {
		const principal = "urn:xb:apikey:echo-race-conflict"
		idempotencyHash := [32]byte{0x12, 0x02}
		requestHashes := [2][32]byte{
			{0x22, 0x01},
			{0x22, 0x02},
		}
		responses := [2]edge.StoredResponse{
			brokerEchoStoredResponse(
				"019fa9e1-0002-4000-8000-000000000001",
				"request-a",
			),
			brokerEchoStoredResponse(
				"019fa9e1-0002-4000-8000-000000000002",
				"request-b",
			),
		}
		const claimsPerRequest = 16
		results := concurrentBrokerEchoClaims(
			t,
			store,
			principal,
			idempotencyHash,
			claimsPerRequest*len(requestHashes),
			func(worker int) ([32]byte, edge.StoredResponse) {
				group := worker % len(requestHashes)
				return requestHashes[group], responses[group]
			},
		)

		winnerGroup := -1
		var winnerResponse edge.StoredResponse
		successes := [2]int{}
		conflicts := [2]int{}
		for worker, result := range results {
			group := worker % len(requestHashes)
			switch {
			case result.err == nil:
				successes[group]++
				if winnerGroup == -1 {
					winnerGroup = group
					winnerResponse = result.response
				}
				if winnerGroup != group {
					t.Fatalf(
						"request groups %d and %d both claimed the same key",
						winnerGroup,
						group,
					)
				}
				if !brokerEchoResponseMatchesCandidate(
					result.response,
					responses[group],
				) {
					t.Fatalf(
						"worker %d response = %#v, want %#v",
						worker,
						result.response,
						responses[group],
					)
				}
				if !reflect.DeepEqual(result.response, winnerResponse) {
					t.Fatalf(
						"worker %d response = %#v, want exact winner %#v",
						worker,
						result.response,
						winnerResponse,
					)
				}
			case result.err == edge.ErrIdempotencyConflict:
				conflicts[group]++
			default:
				t.Fatalf(
					"worker %d error = %v, want exact ErrIdempotencyConflict",
					worker,
					result.err,
				)
			}
		}
		if winnerGroup == -1 {
			t.Fatal("no request hash won the concurrent claim")
		}
		loserGroup := 1 - winnerGroup
		if successes[winnerGroup] != claimsPerRequest ||
			conflicts[winnerGroup] != 0 ||
			successes[loserGroup] != 0 ||
			conflicts[loserGroup] != claimsPerRequest {
			t.Fatalf(
				"successes=%v conflicts=%v winner=%d",
				successes,
				conflicts,
				winnerGroup,
			)
		}
		assertBrokerEchoReplayRow(
			t,
			adminPool,
			principal,
			idempotencyHash,
			requestHashes[winnerGroup],
			winnerResponse,
		)
	})

	t.Run("expired response is replaced by one new generation", func(t *testing.T) {
		const principal = "urn:xb:apikey:echo-race-expired"
		idempotencyHash := [32]byte{0x13, 0x03}
		expiredRequestHash := [32]byte{0x23, 0x01}
		requestHash := [32]byte{0x23, 0x02}
		expiredResponse := brokerEchoStoredResponse(
			"019fa9e1-0003-4000-8000-000000000001",
			"expired",
		)
		var databaseNow time.Time
		if err := adminPool.QueryRow(ctx, `
			SELECT clock_timestamp()
		`).Scan(&databaseNow); err != nil {
			t.Fatal(err)
		}
		if _, err := adminPool.Exec(ctx, `
			INSERT INTO identity.broker_echo_replays (
				scope,
				idempotency_key_hash,
				request_hash,
				response_status,
				response_headers,
				response_body,
				created_at,
				expires_at
			) VALUES (
				'broker-echo' || chr(31) || $1,
				$2,
				$3,
				$4,
				$5,
				$6,
				$7::timestamptz - interval '48 hours',
				$7::timestamptz - interval '24 hours'
			)`,
			principal,
			idempotencyHash[:],
			expiredRequestHash[:],
			expiredResponse.Status,
			expiredResponse.Headers,
			expiredResponse.Body,
			databaseNow,
		); err != nil {
			t.Fatal(err)
		}

		candidates := []edge.StoredResponse{
			brokerEchoStoredResponse(
				"019fa9e1-0003-4000-8000-000000000002",
				"replacement-a",
			),
			brokerEchoStoredResponse(
				"019fa9e1-0003-4000-8000-000000000003",
				"replacement-b",
			),
		}
		results := concurrentBrokerEchoClaims(
			t,
			store,
			principal,
			idempotencyHash,
			24,
			func(worker int) ([32]byte, edge.StoredResponse) {
				return requestHash, candidates[worker%len(candidates)]
			},
		)
		var winner edge.StoredResponse
		for worker, result := range results {
			if result.err != nil {
				t.Fatalf("worker %d replacement: %v", worker, result.err)
			}
			if worker == 0 {
				winner = result.response
			} else if !reflect.DeepEqual(result.response, winner) {
				t.Fatalf(
					"worker %d replacement = %#v, want exact winner %#v",
					worker,
					result.response,
					winner,
				)
			}
		}
		if reflect.DeepEqual(winner, expiredResponse) ||
			!containsBrokerEchoResponse(candidates, winner) {
			t.Fatalf("replacement winner = %#v", winner)
		}
		createdAt, expiresAt := assertBrokerEchoReplayRow(
			t,
			adminPool,
			principal,
			idempotencyHash,
			requestHash,
			winner,
		)
		if createdAt.Before(databaseNow) {
			t.Fatalf(
				"replacement created_at = %s, want at or after %s",
				createdAt,
				databaseNow,
			)
		}
		if expiresAt.Sub(createdAt) != 24*time.Hour {
			t.Fatalf(
				"replacement lifetime = %s, want 24h",
				expiresAt.Sub(createdAt),
			)
		}
	})
}

func TestBrokerEchoCapacityIsSharedAndReplayPrecedesAdmission(t *testing.T) {
	ctx := context.Background()
	adminPool := postgresPool(t)
	resetDurableSchemas(t, adminPool)
	if err := newCurrentTestMigrator(
		t,
		adminPool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).MigrateAndProvision(ctx, 7); err != nil {
		t.Fatal(err)
	}
	apiPoolA := runtimeRoleLoginPool(
		t,
		adminPool,
		"platformgo_broker_echo_capacity_api_a",
		"platformgo_api",
	)
	apiPoolB := runtimeRoleLoginPool(
		t,
		adminPool,
		"platformgo_broker_echo_capacity_api_b",
		"platformgo_api",
	)
	storeA := platformpostgres.NewCompatibilityStore(apiPoolA)
	storeB := platformpostgres.NewCompatibilityStore(apiPoolB)
	const (
		principalA = "urn:xb:apikey:capacity-a"
		principalC = "urn:xb:apikey:capacity-c"
	)
	expiredKey := [32]byte{0x31}
	expiredRequest := [32]byte{0x41}
	if _, err := adminPool.Exec(ctx, `
		INSERT INTO identity.broker_echo_replays (
			scope,
			idempotency_key_hash,
			request_hash,
			response_status,
			response_headers,
			response_body,
			created_at,
			expires_at
		) VALUES (
			'broker-echo' || chr(31) || $1,
			$2,
			$3,
			200,
			'{"Content-Type":["application/json"]}',
			convert_to(
				'{"id":"019fa9e1-0004-4000-8000-000000000001"}' ||
					chr(10),
				'UTF8'
			),
			statement_timestamp() - interval '48 hours',
			statement_timestamp() - interval '24 hours'
		)`,
		principalA,
		expiredKey[:],
		expiredRequest[:],
	); err != nil {
		t.Fatal(err)
	}

	keyA := [32]byte{0x32}
	requestA := [32]byte{0x42}
	responseA := brokerEchoStoredResponse(
		"019fa9e1-0004-4000-8000-000000000002",
		"capacity-a",
	)
	if _, err := storeA.BrokerEcho(
		ctx,
		principalA,
		keyA,
		requestA,
		responseA,
	); err != nil {
		t.Fatal(err)
	}
	for index := range 98 {
		key := sha256.Sum256([]byte(fmt.Sprintf("principal-a-%d", index)))
		requestHash := sha256.Sum256(
			[]byte(fmt.Sprintf("principal-a-request-%d", index)),
		)
		if _, err := storeB.BrokerEcho(
			ctx,
			principalA,
			key,
			requestHash,
			brokerEchoStoredResponse(
				fmt.Sprintf(
					"019fa9e1-0004-4000-8000-%012d",
					index+3,
				),
				"capacity-a",
			),
		); err != nil {
			t.Fatal(err)
		}
	}

	replayed, err := storeB.BrokerEcho(
		ctx,
		principalA,
		keyA,
		requestA,
		brokerEchoStoredResponse(
			"019fa9e1-0004-4000-8000-000000000099",
			"changed-candidate",
		),
	)
	if err != nil {
		t.Fatalf("exact replay at capacity: %v", err)
	}
	if !brokerEchoResponseMatchesCandidate(replayed, responseA) {
		t.Fatalf("exact replay at capacity = %#v, want %#v", replayed, responseA)
	}
	if _, err := storeA.BrokerEcho(
		ctx,
		principalA,
		keyA,
		[32]byte{0x44},
		responseA,
	); !errors.Is(err, edge.ErrIdempotencyConflict) {
		t.Fatalf("conflict at capacity = %v", err)
	}

	assertCapacityLimited := func(
		name string,
		store *platformpostgres.CompatibilityStore,
		principal string,
		key [32]byte,
	) {
		t.Helper()
		before := brokerEchoReplayCount(t, adminPool)
		_, claimErr := store.BrokerEcho(
			ctx,
			principal,
			key,
			[32]byte{0x45},
			brokerEchoStoredResponse(
				"019fa9e1-0004-4000-8000-000000000005",
				name,
			),
		)
		var rateLimit edge.RateLimitError
		if !errors.As(claimErr, &rateLimit) ||
			rateLimit.RetryAfterSeconds == 0 {
			t.Fatalf("%s capacity error = %v", name, claimErr)
		}
		if after := brokerEchoReplayCount(t, adminPool); after != before {
			t.Fatalf("%s capacity rejection changed rows %d -> %d", name, before, after)
		}
	}
	assertCapacityLimited(
		"principal",
		storeA,
		principalA,
		[32]byte{0x35},
	)
	for principalIndex := range 9 {
		principal := fmt.Sprintf(
			"urn:xb:apikey:capacity-global-%d",
			principalIndex,
		)
		for keyIndex := range 100 {
			key := sha256.Sum256(
				[]byte(fmt.Sprintf("global-%d-%d", principalIndex, keyIndex)),
			)
			requestHash := sha256.Sum256(
				[]byte(
					fmt.Sprintf(
						"global-request-%d-%d",
						principalIndex,
						keyIndex,
					),
				),
			)
			if _, err := storeB.BrokerEcho(
				ctx,
				principal,
				key,
				requestHash,
				brokerEchoStoredResponse(
					fmt.Sprintf(
						"019fa9e1-0004-4000-%04x-%012d",
						0x8000+principalIndex,
						keyIndex+1,
					),
					"capacity-global",
				),
			); err != nil {
				t.Fatal(err)
			}
		}
	}
	assertCapacityLimited(
		"global",
		storeB,
		principalC,
		[32]byte{0x36},
	)

	replacement := brokerEchoStoredResponse(
		"019fa9e1-0004-4000-8000-000000000006",
		"expired-replacement",
	)
	replaced, err := storeA.BrokerEcho(
		ctx,
		principalA,
		expiredKey,
		[32]byte{0x46},
		replacement,
	)
	if err != nil || !brokerEchoResponseMatchesCandidate(replaced, replacement) {
		t.Fatalf("expired replacement at capacity = %#v, %v", replaced, err)
	}
	if total := brokerEchoReplayCount(t, adminPool); total != 1000 {
		t.Fatalf("replacement changed total rows to %d", total)
	}

	reopenedPool := runtimeRoleLoginPool(
		t,
		adminPool,
		"platformgo_broker_echo_capacity_api_reopened",
		"platformgo_api",
	)
	reopened := platformpostgres.NewCompatibilityStore(reopenedPool)
	replayed, err = reopened.BrokerEcho(
		ctx,
		principalA,
		keyA,
		requestA,
		responseA,
	)
	if err != nil || !brokerEchoResponseMatchesCandidate(replayed, responseA) {
		t.Fatalf("reconstructed replay at capacity = %#v, %v", replayed, err)
	}
}

func TestBrokerEchoCapacitySerializesFinalPrincipalAndGlobalSlots(
	t *testing.T,
) {
	ctx := context.Background()
	adminPool := postgresPool(t)
	resetDurableSchemas(t, adminPool)
	if err := newCurrentTestMigrator(
		t,
		adminPool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).MigrateAndProvision(ctx, 7); err != nil {
		t.Fatal(err)
	}
	apiPoolA := runtimeRoleLoginPool(
		t,
		adminPool,
		"platformgo_broker_echo_final_slot_api_a",
		"platformgo_api",
	)
	apiPoolB := runtimeRoleLoginPool(
		t,
		adminPool,
		"platformgo_broker_echo_final_slot_api_b",
		"platformgo_api",
	)
	stores := [2]*platformpostgres.CompatibilityStore{
		platformpostgres.NewCompatibilityStore(apiPoolA),
		platformpostgres.NewCompatibilityStore(apiPoolB),
	}
	const principal = "urn:xb:apikey:capacity-final-principal"
	if _, err := adminPool.Exec(ctx, `
		INSERT INTO identity.broker_echo_replays (
			scope,
			idempotency_key_hash,
			request_hash,
			response_status,
			response_headers,
			response_body,
			created_at,
			expires_at
		)
		SELECT
			'broker-echo' || chr(31) || $1,
			sha256(convert_to('principal-' || item, 'UTF8')),
			sha256(convert_to('principal-request-' || item, 'UTF8')),
			200,
			'{"Content-Type":["application/json"]}',
			convert_to(
				'{"id":"019fa9e1-0005-4000-8000-' ||
					lpad(item::text, 12, '0') || '"}' || chr(10),
				'UTF8'
			),
			statement_timestamp(),
			statement_timestamp() + interval '24 hours'
		  FROM generate_series(1, 99) AS item`,
		principal,
	); err != nil {
		t.Fatal(err)
	}
	requireOneBrokerEchoCapacityWinner(
		t,
		stores,
		[2]string{principal, principal},
		0x51,
	)
	if total := brokerEchoReplayCount(t, adminPool); total != 100 {
		t.Fatalf("principal final-slot total = %d", total)
	}
	if _, err := adminPool.Exec(ctx, `
		INSERT INTO identity.broker_echo_replays (
			scope,
			idempotency_key_hash,
			request_hash,
			response_status,
			response_headers,
			response_body,
			created_at,
			expires_at
		)
		SELECT
			'broker-echo' || chr(31) ||
				'urn:xb:apikey:capacity-global-final-' ||
				((item - 1) / 100)::text,
			sha256(convert_to('global-' || item, 'UTF8')),
			sha256(convert_to('global-request-' || item, 'UTF8')),
			200,
			'{"Content-Type":["application/json"]}',
			convert_to(
				'{"id":"019fa9e1-0005-4000-9000-' ||
					lpad(item::text, 12, '0') || '"}' || chr(10),
				'UTF8'
			),
			statement_timestamp(),
			statement_timestamp() + interval '24 hours'
		  FROM generate_series(1, 899) AS item`); err != nil {
		t.Fatal(err)
	}
	if total := brokerEchoReplayCount(t, adminPool); total != 999 {
		t.Fatalf("global pre-final-slot total = %d", total)
	}
	requireOneBrokerEchoCapacityWinner(
		t,
		stores,
		[2]string{
			"urn:xb:apikey:capacity-global-candidate-a",
			"urn:xb:apikey:capacity-global-candidate-b",
		},
		0x61,
	)
	if total := brokerEchoReplayCount(t, adminPool); total != 1000 {
		t.Fatalf("global final-slot total = %d", total)
	}
}

func TestBrokerEchoClaimAndPurgeShareOneLockOrder(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	adminPool := postgresPool(t)
	resetDurableSchemas(t, adminPool)
	if err := newCurrentTestMigrator(
		t,
		adminPool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).MigrateAndProvision(ctx, 7); err != nil {
		t.Fatal(err)
	}
	apiPoolA := runtimeRoleLoginPool(
		t,
		adminPool,
		"platformgo_broker_echo_purge_order_api_a",
		"platformgo_api",
	)
	apiPoolB := runtimeRoleLoginPool(
		t,
		adminPool,
		"platformgo_broker_echo_purge_order_api_b",
		"platformgo_api",
	)
	storeA := platformpostgres.NewCompatibilityStore(apiPoolA)
	storeB := platformpostgres.NewCompatibilityStore(apiPoolB)

	seedExpired := func(
		principal string,
		key [32]byte,
		request [32]byte,
		id string,
	) {
		t.Helper()
		if _, err := adminPool.Exec(ctx, `
			INSERT INTO identity.broker_echo_replays (
				scope,
				idempotency_key_hash,
				request_hash,
				response_status,
				response_headers,
				response_body,
				created_at,
				expires_at
			) VALUES (
				'broker-echo' || chr(31) || $1,
				$2,
				$3,
				200,
				'{"Content-Type":["application/json"]}',
				convert_to(
					'{"id":"' || $4 || '"}' || chr(10),
					'UTF8'
				),
				statement_timestamp() - interval '48 hours',
				statement_timestamp() - interval '24 hours'
			)`,
			principal,
			key[:],
			request[:],
			id,
		); err != nil {
			t.Fatal(err)
		}
	}
	runConcurrent := func(claim func() error) int64 {
		t.Helper()
		start := make(chan struct{})
		purgeResult := make(chan struct {
			deleted int64
			err     error
		}, 1)
		claimResult := make(chan error, 1)
		go func() {
			<-start
			deleted, err := storeA.PurgeExpiredBrokerEchoReplays(ctx, 100)
			purgeResult <- struct {
				deleted int64
				err     error
			}{deleted: deleted, err: err}
		}()
		go func() {
			<-start
			claimResult <- claim()
		}()
		close(start)
		purged := <-purgeResult
		if purged.err != nil {
			t.Fatalf("concurrent purge: %v", purged.err)
		}
		if err := <-claimResult; err != nil {
			t.Fatalf("concurrent claim: %v", err)
		}
		return purged.deleted
	}

	seedExpired(
		"urn:xb:apikey:purge-order-unrelated",
		[32]byte{0x71},
		[32]byte{0x72},
		"019fa9e1-0006-4000-8000-000000000001",
	)
	missingPrincipal := "urn:xb:apikey:purge-order-missing"
	missingKey := [32]byte{0x73}
	missingRequest := [32]byte{0x74}
	missingResponse := brokerEchoStoredResponse(
		"019fa9e1-0006-4000-8000-000000000002",
		"purge-order-missing",
	)
	if deleted := runConcurrent(func() error {
		response, err := storeB.BrokerEcho(
			ctx,
			missingPrincipal,
			missingKey,
			missingRequest,
			missingResponse,
		)
		if err == nil &&
			!brokerEchoResponseMatchesCandidate(response, missingResponse) {
			return fmt.Errorf("missing claim response = %#v", response)
		}
		return err
	}); deleted != 1 {
		t.Fatalf("unrelated expired purge deleted %d, want 1", deleted)
	}
	if total := brokerEchoReplayCount(t, adminPool); total != 1 {
		t.Fatalf("missing-claim interleaving left %d rows, want 1", total)
	}

	expiredPrincipal := "urn:xb:apikey:purge-order-replacement"
	expiredKey := [32]byte{0x75}
	seedExpired(
		expiredPrincipal,
		expiredKey,
		[32]byte{0x76},
		"019fa9e1-0006-4000-8000-000000000003",
	)
	replacementRequest := [32]byte{0x77}
	replacement := brokerEchoStoredResponse(
		"019fa9e1-0006-4000-8000-000000000004",
		"purge-order-replacement",
	)
	deleted := runConcurrent(func() error {
		response, err := storeB.BrokerEcho(
			ctx,
			expiredPrincipal,
			expiredKey,
			replacementRequest,
			replacement,
		)
		if err == nil &&
			!brokerEchoResponseMatchesCandidate(response, replacement) {
			return fmt.Errorf("replacement response = %#v", response)
		}
		return err
	})
	if deleted != 0 && deleted != 1 {
		t.Fatalf("replacement interleaving purge deleted %d, want 0 or 1", deleted)
	}
	if total := brokerEchoReplayCount(t, adminPool); total != 2 {
		t.Fatalf("replacement interleaving left %d rows, want 2", total)
	}
	replayed, err := storeA.BrokerEcho(
		ctx,
		expiredPrincipal,
		expiredKey,
		replacementRequest,
		replacement,
	)
	if err != nil ||
		!brokerEchoResponseMatchesCandidate(replayed, replacement) {
		t.Fatalf("replacement replay = %#v, %v", replayed, err)
	}
	assertBrokerEchoReplayRow(
		t,
		adminPool,
		expiredPrincipal,
		expiredKey,
		replacementRequest,
		replayed,
	)
	coverage, err := storeA.BrokerEchoReplayCoverage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if coverage.TotalRows != 2 ||
		coverage.LiveRows != 2 ||
		coverage.ExpiredRows != 0 ||
		coverage.MaximumPrincipalRows != 1 {
		t.Fatalf("post-interleaving coverage = %#v", coverage)
	}
}

func requireOneBrokerEchoCapacityWinner(
	t *testing.T,
	stores [2]*platformpostgres.CompatibilityStore,
	principals [2]string,
	seed byte,
) {
	t.Helper()
	type result struct {
		err error
	}
	start := make(chan struct{})
	results := make(chan result, len(stores))
	var claims sync.WaitGroup
	for index, store := range stores {
		claims.Add(1)
		go func() {
			defer claims.Done()
			<-start
			_, claimErr := store.BrokerEcho(
				context.Background(),
				principals[index],
				[32]byte{seed, byte(index)},
				[32]byte{seed + 1, byte(index)},
				brokerEchoStoredResponse(
					fmt.Sprintf(
						"019fa9e1-0005-4000-a000-%012d",
						int(seed)+index,
					),
					"final-slot",
				),
			)
			results <- result{err: claimErr}
		}()
	}
	close(start)
	claims.Wait()
	close(results)
	successes := 0
	limited := 0
	for candidate := range results {
		switch {
		case candidate.err == nil:
			successes++
		case errors.Is(candidate.err, edge.ErrRateLimited):
			limited++
		default:
			t.Fatalf("final-slot error = %v", candidate.err)
		}
	}
	if successes != 1 || limited != 1 {
		t.Fatalf("final-slot successes=%d limited=%d", successes, limited)
	}
}

func brokerEchoReplayCount(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var total int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM identity.broker_echo_replays`,
	).Scan(&total); err != nil {
		t.Fatal(err)
	}
	return total
}

type brokerEchoClaimResult struct {
	response edge.StoredResponse
	err      error
}

func concurrentBrokerEchoClaims(
	t *testing.T,
	store *platformpostgres.CompatibilityStore,
	principal string,
	idempotencyHash [32]byte,
	workers int,
	input func(int) ([32]byte, edge.StoredResponse),
) []brokerEchoClaimResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	start := make(chan struct{})
	results := make([]brokerEchoClaimResult, workers)
	var ready sync.WaitGroup
	ready.Add(workers)
	var claims sync.WaitGroup
	claims.Add(workers)
	for worker := range workers {
		requestHash, response := input(worker)
		go func() {
			defer claims.Done()
			ready.Done()
			<-start
			results[worker].response, results[worker].err = store.BrokerEcho(
				ctx,
				principal,
				idempotencyHash,
				requestHash,
				response,
			)
		}()
	}
	ready.Wait()
	close(start)
	claims.Wait()
	return results
}

func brokerEchoStoredResponse(id string, wireVersion string) edge.StoredResponse {
	return edge.StoredResponse{
		Status: 200,
		Headers: []byte(
			`{"Content-Type":["application/json"],` +
				`"X-Echo-Wire-Version":["` + wireVersion + `"]}`,
		),
		Body: []byte(`{"id":"` + id + `","wireVersion":"` +
			wireVersion + `"}` + "\n"),
	}
}

func containsBrokerEchoResponse(
	candidates []edge.StoredResponse,
	response edge.StoredResponse,
) bool {
	for _, candidate := range candidates {
		if brokerEchoResponseMatchesCandidate(response, candidate) {
			return true
		}
	}
	return false
}

func brokerEchoResponseMatchesCandidate(
	response edge.StoredResponse,
	candidate edge.StoredResponse,
) bool {
	if response.Status != candidate.Status ||
		!bytes.Equal(response.Body, candidate.Body) {
		return false
	}
	var responseHeaders any
	if err := json.Unmarshal(response.Headers, &responseHeaders); err != nil {
		return false
	}
	var candidateHeaders any
	if err := json.Unmarshal(candidate.Headers, &candidateHeaders); err != nil {
		return false
	}
	return reflect.DeepEqual(responseHeaders, candidateHeaders)
}

func assertBrokerEchoReplayRow(
	t *testing.T,
	pool interface {
		QueryRow(context.Context, string, ...any) pgx.Row
	},
	principal string,
	idempotencyHash [32]byte,
	wantRequestHash [32]byte,
	wantResponse edge.StoredResponse,
) (time.Time, time.Time) {
	t.Helper()
	var (
		count       int
		requestHash []byte
		response    edge.StoredResponse
		createdAt   time.Time
		expiresAt   time.Time
	)
	if err := pool.QueryRow(context.Background(), `
		SELECT
			count(*) OVER (),
			request_hash,
			response_status,
			response_headers,
			response_body,
			created_at,
			expires_at
		  FROM identity.broker_echo_replays
		 WHERE scope = 'broker-echo' || chr(31) || $1
		   AND idempotency_key_hash = $2`,
		principal,
		idempotencyHash[:],
	).Scan(
		&count,
		&requestHash,
		&response.Status,
		&response.Headers,
		&response.Body,
		&createdAt,
		&expiresAt,
	); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("durable replay rows = %d, want 1", count)
	}
	if !bytes.Equal(requestHash, wantRequestHash[:]) {
		t.Fatalf(
			"durable request hash = %x, want %x",
			requestHash,
			wantRequestHash,
		)
	}
	if !reflect.DeepEqual(response, wantResponse) {
		t.Fatalf(
			"durable response = %#v, want exact %#v",
			response,
			wantResponse,
		)
	}
	return createdAt, expiresAt
}
