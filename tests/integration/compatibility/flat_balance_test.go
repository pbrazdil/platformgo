package compatibility_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
	"github.com/upcomers-org/platformgo/internal/application"
	"github.com/upcomers-org/platformgo/internal/edge"
	"github.com/upcomers-org/platformgo/internal/engine"
	"github.com/upcomers-org/platformgo/migrations"
	"github.com/upcomers-org/platformgo/testkit"
)

// Ported from:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/trading/e2e_balances.rs:18
//	test: flat_account_balance_is_pure_cash_and_scale_stripped
//
// Adaptations:
//   - Current Go's accepted five-field projection is authoritative; the source
//     query's additional computed margin fields are intentionally absent.
//   - The source's scale-15 fixture becomes canonical engine input "1000"
//     because USDC scale 2 rejects excess lexical scale before persistence.
//   - The balance is produced by the deterministic engine and committed to
//     PostgreSQL instead of being inserted as a positive-path SQL fixture.
//   - Malformed authoritative projection rows prove whole-response fail-closed
//     behavior at the PostgreSQL compatibility boundary.
//
// Assertions preserved:
//   - A flat account returns total 1000, locked 0, free 1000, and equity 1000.
//   - PostgreSQL numeric storage scale is stripped on the wire.
//
// Strengthening:
//   - Fault rollback, exact retry, both duplicate-delivery paths, restart,
//     reconciliation, balanced ledger entries, auth, ownership, and the
//     least-privilege read role are exercised in one deterministic fixture.
func TestFlatAccountBalanceIsPureCashAndScaleStripped(t *testing.T) {
	databaseURL := os.Getenv("PLATFORMGO_TEST_POSTGRES_DSN")
	if databaseURL == "" {
		t.Skip("PLATFORMGO_TEST_POSTGRES_DSN is not configured")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	if err := resetCompatibilityDatabase(ctx, admin); err != nil {
		t.Fatal(err)
	}
	if err := platformpostgres.NewMigrator(
		admin,
		migrations.Files,
	).MigrateAndProvision(ctx, 7); err != nil {
		t.Fatal(err)
	}

	const (
		shardID       = engine.ShardID(7)
		accountID     = "urn:xb:account:flat-balance"
		marginAccount = "urn:xb:account:flat-balance-margin"
		ownerID       = "urn:xb:user:flat-balance-owner"
		otherID       = "urn:xb:user:flat-balance-other"
	)
	logicalTime := time.Date(2026, time.July, 28, 10, 0, 0, 0, time.UTC)
	store := platformpostgres.NewEngineStore(admin)
	journal := platformpostgres.NewCommandJournal(admin)
	state := engine.NewState(shardID)
	var latestMarketSequence uint64
	apply := func(
		commandIDText string,
		accountLane string,
		accountSequence uint64,
		action engine.TradingAction,
		faults *testkit.Faults,
	) (engine.State, engine.Decision, engine.InputEnvelope, bool, error) {
		t.Helper()
		commandID, parseErr := engine.ParseID(commandIDText)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		payload, encodeErr := engine.EncodeTradingAction(action)
		if encodeErr != nil {
			t.Fatal(encodeErr)
		}
		input := engine.InputEnvelope{
			InputID:              commandID,
			SchemaVersion:        engine.CurrentSchemaVersion,
			ShardID:              shardID,
			Kind:                 engine.InputKindCommand,
			SourceID:             "phase3-flat-balance-source-port",
			SourceSequence:       accountSequence,
			StreamSequence:       state.NextStreamSequence(),
			MarketSequence:       latestMarketSequence,
			LogicalTime:          engine.NewLogicalTime(logicalTime),
			ConfigurationVersion: 1,
			InstrumentVersion:    1,
			Payload:              payload,
		}
		outboxPayload, encodeErr := engine.EncodeInputMessage(input)
		if encodeErr != nil {
			t.Fatal(encodeErr)
		}
		requestHash := sha256.Sum256(payload.Bytes())
		begin, beginErr := journal.Begin(ctx, application.BeginCommandRequest{
			Scope:            "phase3-flat-balance:" + accountLane,
			IdempotencyKey:   commandID.String(),
			RequestHash:      requestHash,
			CommandID:        commandID,
			AccountID:        accountLane,
			AccountSequence:  accountSequence,
			CommandType:      string(action.Kind),
			SchemaVersion:    engine.CurrentSchemaVersion,
			CanonicalPayload: payload.Bytes(),
			OutboxSubject: fmt.Sprintf(
				"engine.input.%d.command.v%d",
				shardID,
				engine.CurrentSchemaVersion,
			),
			OutboxPayload: outboxPayload,
			LogicalTime:   logicalTime,
			ExpiresAt:     logicalTime.Add(24 * time.Hour),
		})
		if beginErr != nil {
			t.Fatal(beginErr)
		}
		if !begin.Created || begin.CommandID != commandID {
			t.Fatalf("begin command = %+v, want newly created %s", begin, commandID)
		}
		next, decision, duplicate, applyErr := store.ApplyTrading(
			ctx,
			state,
			input,
			action,
			platformpostgres.ApplyOptions{Faults: faults},
		)
		return next, decision, input, duplicate, applyErr
	}

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
			MakerFeeRate:            "0",
			TakerFeeRate:            "0",
		},
	}
	state, _, _, _, err = apply(
		"019f9550-0001-7000-8000-000000000001",
		"urn:xb:account:flat-balance-catalog",
		1,
		configureInstrument,
		nil,
	)
	if err != nil {
		t.Fatalf("configure instrument: %v", err)
	}
	logicalTime = logicalTime.Add(time.Second)

	configureAccount := engine.TradingAction{
		Kind: engine.TradingActionConfigureAccount,
		ConfigureAccount: &engine.ConfigureAccount{
			AccountID: accountID,
			OmsMode:   engine.OmsModeNetting,
		},
	}
	state, _, _, _, err = apply(
		"019f9550-0002-7000-8000-000000000002",
		accountID,
		1,
		configureAccount,
		nil,
	)
	if err != nil {
		t.Fatalf("configure account: %v", err)
	}
	logicalTime = logicalTime.Add(time.Second)

	deposit := engine.TradingAction{
		Kind: engine.TradingActionAdjustBalance,
		AdjustBalance: &engine.AdjustBalance{
			AccountID:     accountID,
			Currency:      "USDC",
			CurrencyScale: 2,
			Operation:     engine.BalanceOperationDeposit,
			Amount:        "1000",
		},
	}
	beforeDepositHash := state.Hash()
	faults := testkit.NewFaults(platformpostgres.FailpointAfterPersistBeforeCommit)
	rolledBack, _, depositInput, duplicate, err := apply(
		"019f9550-0003-7000-8000-000000000003",
		accountID,
		2,
		deposit,
		faults,
	)
	if !errors.Is(err, platformpostgres.ErrInjectedFault) {
		t.Fatalf("faulted deposit error = %v, want injected fault", err)
	}
	if duplicate || rolledBack.Hash() != beforeDepositHash {
		t.Fatal("faulted deposit escaped its PostgreSQL transaction")
	}
	requireFlatBalanceRowCounts(t, admin, 2, 0, 0)
	var (
		faultedBalanceCount int
		faultedReceiptCount int
		faultedCommandState string
		checkpointSequence  uint64
		checkpointHash      []byte
	)
	if err := admin.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM ledger.balances
			  WHERE account_id = $1 AND currency = 'USDC'),
			(SELECT count(*) FROM engine.input_receipts
			  WHERE shard_id = $2 AND input_id = $3),
			(SELECT status FROM trading.commands WHERE command_id = $3),
			(SELECT next_stream_sequence FROM engine.shard_checkpoints
			  WHERE shard_id = $2),
			(SELECT state_hash FROM engine.shard_checkpoints
			  WHERE shard_id = $2)`,
		accountID,
		int64(shardID),
		depositInput.InputID.String(),
	).Scan(
		&faultedBalanceCount,
		&faultedReceiptCount,
		&faultedCommandState,
		&checkpointSequence,
		&checkpointHash,
	); err != nil {
		t.Fatal(err)
	}
	if faultedBalanceCount != 0 ||
		faultedReceiptCount != 0 ||
		faultedCommandState != "pending" ||
		checkpointSequence != state.NextStreamSequence() ||
		!bytes.Equal(checkpointHash, beforeDepositHash[:]) {
		t.Fatalf(
			"fault rollback balance/receipt/command/checkpoint = %d/%d/%s/%d/%x",
			faultedBalanceCount,
			faultedReceiptCount,
			faultedCommandState,
			checkpointSequence,
			checkpointHash,
		)
	}

	state, depositDecision, duplicate, err := store.ApplyTrading(
		ctx,
		state,
		depositInput,
		deposit,
		platformpostgres.ApplyOptions{},
	)
	if err != nil || duplicate ||
		depositDecision.CommandResult.Status != engine.CommandStatusAccepted {
		t.Fatalf(
			"retried deposit = status %s duplicate %t error %v",
			depositDecision.CommandResult.Status,
			duplicate,
			err,
		)
	}
	requireFlatBalanceRowCounts(t, admin, 3, 1, 2)

	beforeDuplicateHash := state.Hash()
	sameState, sameDecision, duplicate, err := store.ApplyTrading(
		ctx,
		state,
		depositInput,
		deposit,
		platformpostgres.ApplyOptions{},
	)
	if err != nil || !duplicate ||
		sameState.Hash() != beforeDuplicateHash ||
		sameDecision.DecisionHash != depositDecision.DecisionHash {
		t.Fatalf("same-sequence duplicate mutated state or decision")
	}

	republished := depositInput
	republished.StreamSequence = state.NextStreamSequence()
	state, republishedDecision, duplicate, err := store.ApplyTrading(
		ctx,
		state,
		republished,
		deposit,
		platformpostgres.ApplyOptions{},
	)
	if err != nil || !duplicate ||
		republishedDecision.DuplicateOfDecisionHash != depositDecision.DecisionHash {
		t.Fatalf("later-sequence duplicate = %+v duplicate %t error %v", republishedDecision, duplicate, err)
	}
	requireFlatBalanceRowCounts(t, admin, 3, 1, 2)

	// A second engine-created account supplies a causally valid non-flat
	// projection so the HTTP test can distinguish committed used/free columns
	// from source-style edge recomputation. It does not accept the adjacent
	// pinned working-order balance behavior or its formula.
	logicalTime = logicalTime.Add(time.Second)
	state, _, _, _, err = apply(
		"019f9550-0004-7000-8000-000000000004",
		marginAccount,
		1,
		engine.TradingAction{
			Kind: engine.TradingActionConfigureAccount,
			ConfigureAccount: &engine.ConfigureAccount{
				AccountID: marginAccount,
				OmsMode:   engine.OmsModeNetting,
			},
		},
		nil,
	)
	if err != nil {
		t.Fatalf("configure margin fixture account: %v", err)
	}
	logicalTime = logicalTime.Add(time.Second)
	state, _, _, _, err = apply(
		"019f9550-0005-7000-8000-000000000005",
		marginAccount,
		2,
		engine.TradingAction{
			Kind: engine.TradingActionAdjustBalance,
			AdjustBalance: &engine.AdjustBalance{
				AccountID:     marginAccount,
				Currency:      "USDC",
				CurrencyScale: 2,
				Operation:     engine.BalanceOperationDeposit,
				Amount:        "1000",
			},
		},
		nil,
	)
	if err != nil {
		t.Fatalf("fund margin fixture account: %v", err)
	}
	logicalTime = logicalTime.Add(time.Second)
	bookAction := engine.TradingAction{
		Kind: engine.TradingActionUpdateBook,
		UpdateBook: &engine.UpdateBook{
			InstrumentID: "BTC-PERP",
			MarkPrice:    "100",
			Bids:         []engine.BookLevel{{Price: "99", Quantity: "10"}},
			Asks:         []engine.BookLevel{{Price: "110", Quantity: "10"}},
		},
	}
	bookPayload, err := engine.EncodeTradingAction(bookAction)
	if err != nil {
		t.Fatal(err)
	}
	bookID, err := engine.ParseID("019f9550-0006-7000-8000-000000000006")
	if err != nil {
		t.Fatal(err)
	}
	bookInput := engine.InputEnvelope{
		InputID:              bookID,
		SchemaVersion:        engine.CurrentSchemaVersion,
		ShardID:              shardID,
		Kind:                 engine.InputKindMarket,
		SourceID:             "phase3-flat-balance-market",
		SourceSequence:       1,
		StreamSequence:       state.NextStreamSequence(),
		MarketSequence:       state.NextStreamSequence(),
		LogicalTime:          engine.NewLogicalTime(logicalTime),
		ConfigurationVersion: 1,
		InstrumentVersion:    1,
		Payload:              bookPayload,
	}
	state, _, duplicate, err = store.ApplyTrading(
		ctx,
		state,
		bookInput,
		bookAction,
		platformpostgres.ApplyOptions{},
	)
	if err != nil || duplicate {
		t.Fatalf("apply margin fixture book = duplicate %t error %v", duplicate, err)
	}
	latestMarketSequence = bookInput.MarketSequence
	logicalTime = logicalTime.Add(time.Second)
	orderID, err := engine.ParseID("019f9550-0007-7000-8000-000000000007")
	if err != nil {
		t.Fatal(err)
	}
	var marginDecision engine.Decision
	state, marginDecision, _, _, err = apply(
		"019f9550-0008-7000-8000-000000000008",
		marginAccount,
		3,
		engine.TradingAction{
			Kind: engine.TradingActionSubmitOrder,
			SubmitOrder: &engine.SubmitOrder{
				OrderID:      orderID,
				AccountID:    marginAccount,
				InstrumentID: "BTC-PERP",
				Side:         engine.SideBuy,
				Type:         engine.OrderTypeLimit,
				TimeInForce:  engine.TimeInForceGTC,
				Quantity:     "1",
				Price:        "90",
			},
		},
		nil,
	)
	if err != nil ||
		marginDecision.CommandResult.Status != engine.CommandStatusAccepted ||
		len(marginDecision.BalanceChanges) != 1 ||
		marginDecision.BalanceChanges[0].Total != "1000" ||
		marginDecision.BalanceChanges[0].Used != "1" ||
		marginDecision.BalanceChanges[0].Free != "999" ||
		marginDecision.BalanceChanges[0].Equity != "1000" {
		t.Fatalf("margin projection decision = %+v, error %v", marginDecision, err)
	}
	if marginDecision.MarketSequence != bookInput.MarketSequence {
		t.Fatalf(
			"margin decision market sequence = %d, want relevant book %d",
			marginDecision.MarketSequence,
			bookInput.MarketSequence,
		)
	}
	var persistedMarketSequence int64
	if err := admin.QueryRow(ctx, `
		SELECT (decision ->> 'MarketSequence')::bigint
		  FROM engine.input_receipts
		 WHERE shard_id = $1 AND input_id = $2`,
		int64(shardID),
		"019f9550-0008-7000-8000-000000000008",
	).Scan(&persistedMarketSequence); err != nil {
		t.Fatalf("load durable margin decision market sequence: %v", err)
	}
	if persistedMarketSequence != int64(bookInput.MarketSequence) {
		t.Fatalf(
			"durable margin decision market sequence = %d, want relevant book %d",
			persistedMarketSequence,
			bookInput.MarketSequence,
		)
	}
	requireFlatBalanceRowCounts(t, admin, 7, 2, 4)

	recovered, err := platformpostgres.NewEngineStore(admin).
		RecoverTradingState(ctx, shardID)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Hash() != state.Hash() ||
		recovered.NextStreamSequence() != state.NextStreamSequence() {
		t.Fatalf("recovered state differs from committed state")
	}
	balance, ok := recovered.Balance(accountID, "USDC")
	if !ok || balance.Total != "1000" || balance.Used != "0" ||
		balance.Free != "1000" || balance.Equity != "1000" {
		t.Fatalf("recovered balance = %+v, found %t", balance, ok)
	}
	marginBalance, ok := recovered.Balance(marginAccount, "USDC")
	if !ok ||
		marginBalance.Total != "1000" ||
		marginBalance.Used != "1" ||
		marginBalance.Free != "999" ||
		marginBalance.Equity != "1000" {
		t.Fatalf("recovered margin projection = %+v, found %t", marginBalance, ok)
	}

	publisher := &flatBalancePublisher{}
	messaging := platformpostgres.NewMessagingStore(admin)
	publishTime := logicalTime.Add(7 * 24 * time.Hour)
	for {
		published, publishErr := messaging.PublishOutboxBatch(
			ctx,
			publisher,
			publishTime,
			100,
			time.Minute,
			time.Second,
		)
		if publishErr != nil {
			t.Fatal(publishErr)
		}
		if published == 0 {
			break
		}
		publishTime = publishTime.Add(time.Second)
	}
	report, err := platformpostgres.NewEngineStore(admin).
		ReconcileShard(ctx, shardID)
	if err != nil || !report.Ready ||
		report.ReceiptCount != 7 ||
		report.DuplicateDeliveryCount != 1 ||
		report.LedgerMismatchCount != 0 ||
		report.UnbalancedGroupCount != 0 ||
		report.CommandMismatchCount != 0 ||
		report.MessagingMismatchCount != 0 ||
		report.PendingOutboxMessages != 0 {
		t.Fatalf("flat-balance reconciliation = %+v, error %v", report, err)
	}
	requireFlatBalanceLedger(t, admin, accountID, marginAccount)

	passwordHash, err := application.HashPassword(
		"flat balance password",
		bytes.NewReader(bytes.Repeat([]byte{31}, 16)),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `
		INSERT INTO identity.users (
			user_id, login, normalized_login, email, normalized_email,
			password_hash
			) VALUES
				($1, 'flat-owner', 'flat-owner', 'flat-owner@example.com',
					'flat-owner@example.com', $3),
				($2, 'flat-other', 'flat-other', 'flat-other@example.com',
					'flat-other@example.com', $3)`,
		ownerID,
		otherID,
		passwordHash,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `
		INSERT INTO identity.user_accounts (user_id, account_id)
		VALUES ($1, $2), ($1, $3)`,
		ownerID,
		accountID,
		marginAccount,
	); err != nil {
		t.Fatal(err)
	}
	apiDatabaseURL := provisionRuntimeLogin(
		t,
		ctx,
		admin,
		databaseURL,
		"platformgo_api",
	)
	apiPool, err := pgxpool.New(ctx, apiDatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer apiPool.Close()
	requireFlatBalanceACL(t, apiPool)

	now := time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC)
	clock := ownershipGateClock{value: now}
	authenticator, err := edge.NewHMACAuthenticator(edge.HMACAuthenticatorConfig{
		ClientTokenSecret: []byte("phase3-flat-balance-client-secret"),
		Clock:             clock,
	})
	if err != nil {
		t.Fatal(err)
	}
	apiStore := platformpostgres.NewCompatibilityStore(apiPool)
	identity, err := application.NewIdentity(
		apiStore,
		authenticator,
		application.IdentityConfig{
			Clock: clock,
			Entropy: bytes.NewReader(append(
				bytes.Repeat([]byte{43}, 32),
				bytes.Repeat([]byte{47}, 32)...,
			)),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(edge.NewServer(edge.ServerConfig{
		Authenticator: authenticator,
		Identity:      identity,
		Trading:       apiStore,
		RequestID:     func() string { return "flat-balance-request" },
	}).Handler())
	defer server.Close()
	ownerToken := flatBalanceLogin(t, server.URL, "flat-owner")
	otherToken := flatBalanceLogin(t, server.URL, "flat-other")
	path := "/v1/accounts/" + accountID + "/balances"

	requireFlatBalanceHTTP(
		t,
		server.URL+path,
		ownerToken,
		http.StatusOK,
		`[{"currency":"USDC","total":"1000","locked":"0","free":"1000","equity":"1000"}]`+"\n",
	)
	requireFlatBalanceHTTP(
		t,
		server.URL+"/v1/accounts/"+marginAccount+"/balances",
		ownerToken,
		http.StatusOK,
		`[{"currency":"USDC","total":"1000","locked":"1","free":"999","equity":"1000"}]`+"\n",
	)
	requireFlatBalanceHTTP(
		t,
		server.URL+path,
		"",
		http.StatusUnauthorized,
		`{"code":"unauthorized","message":"unauthorized","requestId":"flat-balance-request"}`+"\n",
	)
	requireFlatBalanceHTTP(
		t,
		server.URL+path,
		otherToken,
		http.StatusForbidden,
		`{"code":"forbidden","message":"forbidden","requestId":"flat-balance-request"}`+"\n",
	)
	requireFlatBalanceHTTP(
		t,
		server.URL+"/v1/accounts/not-a-urn/balances",
		ownerToken,
		http.StatusBadRequest,
		`{"code":"invalid_request","message":"invalid account id","requestId":"flat-balance-request"}`+"\n",
	)
	var ownerRate, otherRate int64
	if err := admin.QueryRow(ctx, `
		SELECT
			(SELECT request_count FROM identity.client_rate_limits
			  WHERE owner_user_id = $1),
			(SELECT request_count FROM identity.client_rate_limits
			  WHERE owner_user_id = $2)`,
		ownerID,
		otherID,
	).Scan(&ownerRate, &otherRate); err != nil {
		t.Fatal(err)
	}
	if ownerRate != 3 || otherRate != 1 {
		t.Fatalf("rate claims owner/foreign = %d/%d, want 3/1", ownerRate, otherRate)
	}
	requireFlatBalanceRowCounts(t, admin, 7, 2, 4)

	freshPool, err := pgxpool.New(ctx, apiDatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer freshPool.Close()
	freshStore := platformpostgres.NewCompatibilityStore(freshPool)
	freshIdentity, err := application.NewIdentity(
		freshStore,
		authenticator,
		application.IdentityConfig{
			Clock:   clock,
			Entropy: bytes.NewReader(bytes.Repeat([]byte{47}, 64)),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	freshServer := httptest.NewServer(edge.NewServer(edge.ServerConfig{
		Authenticator: authenticator,
		Identity:      freshIdentity,
		Trading:       freshStore,
	}).Handler())
	defer freshServer.Close()
	requireFlatBalanceHTTP(
		t,
		freshServer.URL+path,
		ownerToken,
		http.StatusOK,
		`[{"currency":"USDC","total":"1000","locked":"0","free":"1000","equity":"1000"}]`+"\n",
	)

	if _, err := admin.Exec(ctx, `REVOKE SELECT ON ledger.balances FROM platformgo_api`); err != nil {
		t.Fatal(err)
	}
	selectRestored := false
	t.Cleanup(func() {
		if selectRestored {
			return
		}
		if _, err := admin.Exec(
			context.Background(),
			`GRANT SELECT ON ledger.balances TO platformgo_api`,
		); err != nil {
			t.Errorf("restore platformgo_api balance SELECT: %v", err)
		}
	})
	requireFlatBalanceUnavailable(t, server.URL+path, ownerToken)
	if _, err := admin.Exec(ctx, `GRANT SELECT ON ledger.balances TO platformgo_api`); err != nil {
		t.Fatal(err)
	}
	selectRestored = true

	for _, test := range []struct {
		name, currency, total, used, free, equity string
	}{
		{"unknown currency scale", "ZZY", "1", "0", "1", "1"},
		{"invalid currency", "bad!", "1", "0", "1", "1"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := admin.Exec(ctx, `
				INSERT INTO ledger.balances (
					account_id, currency, total, used, free, equity,
					ledger_sequence
				) VALUES ($1,$2,$3,$4,$5,$6,4)`,
				accountID,
				test.currency,
				test.total,
				test.used,
				test.free,
				test.equity,
			); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if _, err := admin.Exec(context.Background(), `
					DELETE FROM ledger.balances
					 WHERE account_id = $1 AND currency = $2`,
					accountID,
					test.currency,
				); err != nil {
					t.Errorf("delete malformed balance fixture: %v", err)
				}
			})
			requireFlatBalanceUnavailable(t, server.URL+path, ownerToken)
		})
	}
	t.Run("known currency excess scale", func(t *testing.T) {
		if _, err := admin.Exec(ctx, `
			UPDATE ledger.balances
			   SET total = 1000.001, free = 1000.001, equity = 1000.001
			 WHERE account_id = $1 AND currency = 'USDC'`,
			accountID,
		); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if _, err := admin.Exec(context.Background(), `
				UPDATE ledger.balances
				   SET total = 1000, free = 1000, equity = 1000
				 WHERE account_id = $1 AND currency = 'USDC'`,
				accountID,
			); err != nil {
				t.Errorf("restore excess-scale balance fixture: %v", err)
			}
		})
		requireFlatBalanceUnavailable(t, server.URL+path, ownerToken)
	})
	t.Run("known currency locked excess scale", func(t *testing.T) {
		if _, err := admin.Exec(ctx, `
			UPDATE ledger.balances
			   SET used = 0.001, free = 999.999
			 WHERE account_id = $1 AND currency = 'USDC'`,
			accountID,
		); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if _, err := admin.Exec(context.Background(), `
				UPDATE ledger.balances
				   SET used = 0, free = 1000
				 WHERE account_id = $1 AND currency = 'USDC'`,
				accountID,
			); err != nil {
				t.Errorf("restore locked excess-scale fixture: %v", err)
			}
		})
		requireFlatBalanceUnavailable(t, server.URL+path, ownerToken)
	})
	t.Run("registered currency non-finite money", func(t *testing.T) {
		if _, err := admin.Exec(ctx, `
			UPDATE ledger.balances
			   SET total = 'NaN', used = 0, free = 'NaN', equity = 'NaN'
			 WHERE account_id = $1 AND currency = 'USDC'`,
			accountID,
		); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if _, err := admin.Exec(context.Background(), `
				UPDATE ledger.balances
				   SET total = 1000, used = 0, free = 1000, equity = 1000
				 WHERE account_id = $1 AND currency = 'USDC'`,
				accountID,
			); err != nil {
				t.Errorf("restore non-finite balance fixture: %v", err)
			}
		})
		requireFlatBalanceUnavailable(t, server.URL+path, ownerToken)
	})

	if _, err := admin.Exec(ctx, `
		UPDATE ledger.balances
		   SET used = 1, free = 999
		 WHERE account_id = $1 AND currency = 'USDC'`,
		accountID,
	); err != nil {
		t.Fatal(err)
	}
	corruptReport, err := platformpostgres.NewEngineStore(admin).
		ReconcileShard(ctx, shardID)
	if !errors.Is(err, platformpostgres.ErrReconciliationMismatch) ||
		corruptReport.Ready ||
		corruptReport.LedgerMismatchCount == 0 ||
		corruptReport.ConfigurationMismatchCount != 0 {
		t.Fatalf(
			"corrupt direct projection reconciliation = %+v, error %v",
			corruptReport,
			err,
		)
	}
}

type flatBalancePublisher struct {
	sequence uint64
}

func (publisher *flatBalancePublisher) Publish(
	_ context.Context,
	_ platformpostgres.OutboxMessage,
) (uint64, error) {
	publisher.sequence++
	return publisher.sequence, nil
}

func flatBalanceLogin(t *testing.T, serverURL string, login string) string {
	t.Helper()
	response := requestJSON(
		t,
		http.MethodPost,
		serverURL+"/v1/auth/login",
		fmt.Sprintf(
			`{"login":%q,"password":"flat balance password"}`,
			login,
		),
		nil,
	)
	var authenticated edge.LoginResponse
	decodeAndClose(t, response, &authenticated)
	if response.StatusCode != http.StatusOK || authenticated.AccessToken == "" {
		t.Fatalf(
			"login %q = status %d body %#v",
			login,
			response.StatusCode,
			authenticated,
		)
	}
	return authenticated.AccessToken
}

func requireFlatBalanceHTTP(
	t *testing.T,
	url string,
	token string,
	status int,
	wantBody string,
) {
	t.Helper()
	headers := map[string]string(nil)
	if token != "" {
		headers = map[string]string{"authorization": "Bearer " + token}
	}
	response := requestJSON(t, http.MethodGet, url, "", headers)
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != status || string(body) != wantBody {
		t.Fatalf(
			"GET %s = status %d body %q, want %d %q",
			url,
			response.StatusCode,
			body,
			status,
			wantBody,
		)
	}
}

func requireFlatBalanceUnavailable(t *testing.T, url string, token string) {
	t.Helper()
	response := requestJSON(
		t,
		http.MethodGet,
		url,
		"",
		map[string]string{"authorization": "Bearer " + token},
	)
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	wantBody := `{"code":"unavailable","message":"trading views unavailable","requestId":"flat-balance-request"}` + "\n"
	if response.StatusCode != http.StatusServiceUnavailable ||
		string(body) != wantBody {
		t.Fatalf(
			"unavailable response = status %d body %q, want 503 %q",
			response.StatusCode,
			body,
			wantBody,
		)
	}
}

func requireFlatBalanceRowCounts(
	t *testing.T,
	pool *pgxpool.Pool,
	receipts int,
	transactions int,
	entries int,
) {
	t.Helper()
	var gotReceipts, gotTransactions, gotEntries int
	if err := pool.QueryRow(context.Background(), `
		SELECT
			(SELECT count(*) FROM engine.input_receipts),
			(SELECT count(*) FROM ledger.transactions),
			(SELECT count(*) FROM ledger.entries)`,
	).Scan(&gotReceipts, &gotTransactions, &gotEntries); err != nil {
		t.Fatal(err)
	}
	if gotReceipts != receipts ||
		gotTransactions != transactions ||
		gotEntries != entries {
		t.Fatalf(
			"durable row counts = receipts %d transactions %d entries %d, want %d/%d/%d",
			gotReceipts,
			gotTransactions,
			gotEntries,
			receipts,
			transactions,
			entries,
		)
	}
}

func requireFlatBalanceLedger(
	t *testing.T,
	pool *pgxpool.Pool,
	accountID string,
	marginAccount string,
) {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		SELECT account_id, currency, trim_scale(amount)::text
		  FROM ledger.entries
		 ORDER BY account_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var account, currency, amount string
		if err := rows.Scan(&account, &currency, &amount); err != nil {
			t.Fatal(err)
		}
		got = append(got, account+"|"+currency+"|"+amount)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"system:clearing|USDC|-1000",
		"system:clearing|USDC|-1000",
		accountID + "|USDC|1000",
		marginAccount + "|USDC|1000",
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("ledger entries = %v, want %v", got, want)
	}
	var sum string
	if err := pool.QueryRow(context.Background(), `
		SELECT trim_scale(sum(amount))::text FROM ledger.entries`,
	).Scan(&sum); err != nil {
		t.Fatal(err)
	}
	if sum != "0" {
		t.Fatalf("ledger sum = %q, want zero", sum)
	}
}

func requireFlatBalanceACL(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	var (
		selectOK, insertOK, updateOK, deleteOK, truncateOK                bool
		scaleSelect, scaleInsert, scaleUpdate, scaleDelete, scaleTruncate bool
	)
	if err := pool.QueryRow(context.Background(), `
		SELECT
			has_table_privilege(current_user, 'ledger.balances', 'SELECT'),
			has_table_privilege(current_user, 'ledger.balances', 'INSERT'),
			has_table_privilege(current_user, 'ledger.balances', 'UPDATE'),
			has_table_privilege(current_user, 'ledger.balances', 'DELETE'),
			has_table_privilege(current_user, 'ledger.balances', 'TRUNCATE'),
			has_table_privilege(current_user, 'trading.currency_scales', 'SELECT'),
			has_table_privilege(current_user, 'trading.currency_scales', 'INSERT'),
			has_table_privilege(current_user, 'trading.currency_scales', 'UPDATE'),
			has_table_privilege(current_user, 'trading.currency_scales', 'DELETE'),
			has_table_privilege(current_user, 'trading.currency_scales', 'TRUNCATE')`,
	).Scan(
		&selectOK,
		&insertOK,
		&updateOK,
		&deleteOK,
		&truncateOK,
		&scaleSelect,
		&scaleInsert,
		&scaleUpdate,
		&scaleDelete,
		&scaleTruncate,
	); err != nil {
		t.Fatal(err)
	}
	if !selectOK || insertOK || updateOK || deleteOK || truncateOK ||
		!scaleSelect || scaleInsert || scaleUpdate || scaleDelete ||
		scaleTruncate {
		t.Fatalf(
			"API balance/scale privileges = %t/%t/%t/%t/%t %t/%t/%t/%t/%t",
			selectOK,
			insertOK,
			updateOK,
			deleteOK,
			truncateOK,
			scaleSelect,
			scaleInsert,
			scaleUpdate,
			scaleDelete,
			scaleTruncate,
		)
	}
	if _, err := pool.Exec(context.Background(), `
		UPDATE ledger.balances SET total = total`); err == nil {
		t.Fatal("API role unexpectedly mutated ledger.balances")
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO trading.currency_scales (currency, scale)
		VALUES ('ZZX', 2)`); err == nil {
		t.Fatal("API role unexpectedly mutated trading.currency_scales")
	}
}
