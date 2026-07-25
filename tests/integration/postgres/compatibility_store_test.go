package postgres_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	platformnats "github.com/upcomers-org/platformgo/internal/adapters/nats"
	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
	"github.com/upcomers-org/platformgo/internal/application"
	"github.com/upcomers-org/platformgo/internal/edge"
	"github.com/upcomers-org/platformgo/internal/engine"
)

type compatibilityClock struct{ value time.Time }

func (clock compatibilityClock) Now() time.Time { return clock.value }

func TestPhase3IdentityCatalogAndDurableOrderIntentUsePostgreSQL(t *testing.T) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := platformpostgres.NewMigrator(
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
	_, err = pool.Exec(ctx, `
		INSERT INTO trading.accounts (account_id, oms_mode)
		VALUES ('urn:xb:account:acct-1', 'NETTING');
		INSERT INTO identity.user_accounts (user_id, account_id)
		VALUES ('urn:xb:user:trader-1', 'urn:xb:account:acct-1');
		INSERT INTO trading.instruments (
			instrument_id, revision, price_scale, quantity_scale,
			settlement_currency, settlement_currency_scale,
			initial_margin_rate, maintenance_margin_rate, max_leverage,
			maker_fee_rate, taker_fee_rate
		) VALUES (
			'BTC-PERP', 3, 2, 3, 'USDC', 2,
			0.1, 0.05, 10, -0.0001, 0.0005
		);
		INSERT INTO ledger.balances (
			account_id, currency, total, used, free, equity, ledger_sequence
		) VALUES (
			'urn:xb:account:acct-1', 'USDC',
			1000.000000000000000000,
			0.000000000000000000,
			1000.000000000000000000,
			1000.000000000000000000,
			0
		)`,
	)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.July, 25, 16, 0, 0, 0, time.UTC)
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
	if err != nil || echoReplay != echo {
		t.Fatalf("echo replay=%q first=%q err=%v", echoReplay, echo, err)
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
	if err != nil || otherEcho == echo {
		t.Fatalf("other principal echo=%q first=%q err=%v", otherEcho, echo, err)
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
			SELECT message_id::text, subject, payload
			  FROM messaging.outbox
			 WHERE producer_class = 'api'
			 ORDER BY created_at, message_id`)
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
			message.StreamSequence = uint64(len(messages) + 1)
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
