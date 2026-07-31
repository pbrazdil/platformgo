package postgres_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
	"github.com/upcomers-org/platformgo/internal/edge"
)

type brokerFillsQueryCounter struct {
	count atomic.Int64
}

func (counter *brokerFillsQueryCounter) TraceQueryStart(
	ctx context.Context,
	_ *pgx.Conn,
	_ pgx.TraceQueryStartData,
) context.Context {
	counter.count.Add(1)
	return ctx
}

func (*brokerFillsQueryCounter) TraceQueryEnd(
	context.Context,
	*pgx.Conn,
	pgx.TraceQueryEndData,
) {
}

func TestBrokerFillsRequiresBothTenantAuthoritiesInThePageStatement(t *testing.T) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := newCurrentTestMigrator(
		t,
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).Migrate(ctx); err != nil {
		t.Fatalf("migrate broker fills database: %v", err)
	}

	const (
		accountID = "urn:xb:account:00000000-0000-4000-8000-000000000009"
		userID    = "urn:xb:user:broker-fills"
		tenant    = "urn:xb:tenant:broker-fills"
	)
	if _, err := pool.Exec(ctx, `
		INSERT INTO trading.accounts (account_id, oms_mode)
		VALUES ($1, 'NETTING')`,
		accountID,
	); err != nil {
		t.Fatalf("seed broker fills account: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO identity.users (
			user_id, login, normalized_login, broker_subject
		) VALUES (
			$1, 'broker-fills', 'broker-fills', $2
		)`,
		userID,
		tenant,
	); err != nil {
		t.Fatalf("seed broker fills user: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO identity.users (
			user_id, login, normalized_login, broker_subject
		) VALUES (
			'urn:xb:user:broker-fills-other',
			'broker-fills-other',
			'broker-fills-other',
			'urn:xb:tenant:other'
		)`,
	); err != nil {
		t.Fatalf("seed foreign broker fills user: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO identity.user_accounts (
			user_id, account_id, broker_subject
		) VALUES ($2, $1, $3)`,
		accountID,
		userID,
		tenant,
	); err != nil {
		t.Fatalf("seed broker fills ownership: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO identity.account_profiles (
			account_id, login, base_currency, market_venue,
			permitted_classes, broker_subject, created_at
		) VALUES (
			$1, 9009, 'USDC', 'HYPERLIQUID',
			ARRAY['CRYPTOCURRENCY'], $2, '2026-07-30T00:00:00Z'
		)`,
		accountID,
		tenant,
	); err != nil {
		t.Fatalf("seed broker fills profile: %v", err)
	}

	apiPool := runtimeRoleLoginPool(
		t,
		pool,
		"platformgo_broker_fills_api_login",
		"platformgo_api",
	)
	queryCounter := &brokerFillsQueryCounter{}
	tracedConfig := apiPool.Config().Copy()
	tracedConfig.ConnConfig.Tracer = queryCounter
	tracedPool, err := pgxpool.NewWithConfig(ctx, tracedConfig)
	if err != nil {
		t.Fatalf("open traced broker fills API pool: %v", err)
	}
	t.Cleanup(tracedPool.Close)
	store := platformpostgres.NewCompatibilityStore(tracedPool)
	page, err := store.BrokerFills(
		ctx,
		tenant,
		accountID,
		edge.FillExecutionFilter{},
	)
	if err != nil {
		t.Fatalf("read authorized empty broker fills: %v", err)
	}
	if page.Items == nil || len(page.Items) != 0 || page.Total != 0 {
		t.Fatalf("authorized empty page = %#v", page)
	}

	for _, deniedTenant := range []string{
		"urn:xb:tenant:other",
		"urn:xb:apikey:broker-fills",
	} {
		denied, deniedErr := store.BrokerFills(
			ctx,
			deniedTenant,
			accountID,
			edge.FillExecutionFilter{},
		)
		if !errors.Is(deniedErr, edge.ErrForbidden) {
			t.Fatalf("tenant %q error = %v, want forbidden", deniedTenant, deniedErr)
		}
		if denied.Items != nil || denied.Total != 0 ||
			denied.NextCursor != nil || denied.PrevCursor != nil {
			t.Fatalf("tenant %q returned partial page %#v", deniedTenant, denied)
		}
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO trading.instruments (
			instrument_id, revision, price_scale, quantity_scale,
			settlement_currency, settlement_currency_scale,
			initial_margin_rate, maintenance_margin_rate, max_leverage,
			maker_fee_rate, taker_fee_rate
		) VALUES (
			'BTC-PERP', 1, 2, 3, 'USDC', 2,
			0.1, 0.05, 10, -0.0001, 0.0005
		);
		INSERT INTO trading.orders (
			order_id, account_id, instrument_id, side, order_type,
			time_in_force, status, quantity, filled_quantity,
			average_fill_price, triggered, reduce_only, has_rested, version
		) VALUES (
			'00000000-0000-4000-8000-000000000601',
			'urn:xb:account:00000000-0000-4000-8000-000000000009',
			'BTC-PERP', 'SELL', 'MARKET', 'IOC', 'FILLED',
			1, 1, 100, false, true, false, 1
		);
		INSERT INTO trading.fills (
			fill_id, order_id, input_id, account_id, instrument_id,
			side, price, quantity, position_id, position_effect,
			realized_pnl, settlement_currency, liquidity_side,
			fee, fee_currency, logical_time, effective_leverage
		) VALUES (
			'00000000-0000-4000-8000-000000000602',
			'00000000-0000-4000-8000-000000000601',
			'00000000-0000-4000-8000-000000000603',
			'urn:xb:account:00000000-0000-4000-8000-000000000009',
			'BTC-PERP', 'SELL', 100, 1,
			'00000000-0000-4000-8000-000000000604', 'close',
			12.30, 'USDC', 'TAKER', 0, 'USDC',
			1784901600000000000, 5.000
		), (
			'00000000-0000-4000-8000-000000000605',
			'00000000-0000-4000-8000-000000000601',
			'00000000-0000-4000-8000-000000000606',
			'urn:xb:account:00000000-0000-4000-8000-000000000009',
			'BTC-PERP', 'SELL', 99, 1,
			'00000000-0000-4000-8000-000000000604', 'open',
			NULL, NULL, 'TAKER', 0, 'USDC', 50, NULL
		)`,
	); err != nil {
		t.Fatalf("seed exact broker fill projection: %v", err)
	}
	authenticator, err := edge.NewHMACAuthenticator(edge.HMACAuthenticatorConfig{
		ClientTokenSecret: []byte("broker-fills-client-secret-0123456789"),
		BrokerCredentials: []edge.BrokerCredential{{
			Prefix:     "xbk_brokerfills",
			SecretHash: edge.HashBrokerSecret("secret"),
			Subject:    "urn:xb:apikey:not-the-tenant",
			Tenant:     tenant,
			Scopes:     []string{"accounts:read"},
		}},
	})
	if err != nil {
		t.Fatalf("create exact broker fills authenticator: %v", err)
	}
	server := edge.NewServer(edge.ServerConfig{
		Authenticator: authenticator,
		BrokerFills:   store,
		RequestID:     func() string { return "broker-fills-exact" },
	}).Handler()
	request := httptest.NewRequest(
		http.MethodGet,
		"/broker/v1/accounts/"+accountID+"/fills?limit=1",
		nil,
	)
	request.Header.Set("x-api-key", "xbk_brokerfills.secret")
	response := httptest.NewRecorder()
	queriesBefore := queryCounter.count.Load()
	server.ServeHTTP(response, request)
	wantBody := `{"items":[{"fillId":"00000000-0000-4000-8000-000000000602","orderId":"urn:xb:order:00000000-0000-4000-8000-000000000601","positionId":"00000000-0000-4000-8000-000000000604","side":"SELL","tradeType":"close","reason":"manual","realizedPnl":"12.3","settlementCurrency":"USDC","leverage":"5","filledAt":"2026-07-24T14:00:00Z"}],"nextCursor":"MTc4NDkwMTYwMDAwMDAwMDAwMDowMDAwMDAwMC0wMDAwLTQwMDAtODAwMC0wMDAwMDAwMDA2MDI","total":2}` + "\n"
	if response.Code != http.StatusOK || response.Body.String() != wantBody {
		t.Fatalf(
			"exact broker fills response status=%d body=%q, want status=200 body=%q",
			response.Code,
			response.Body.String(),
			wantBody,
		)
	}
	if queryCounter.count.Load()-queriesBefore != 1 {
		t.Fatalf(
			"authorized cursorless broker fills executed %d queries, want one",
			queryCounter.count.Load()-queriesBefore,
		)
	}
	restartedPool := runtimeRoleLoginPool(
		t,
		pool,
		"platformgo_broker_fills_restart_api_login",
		"platformgo_api",
	)
	restartedCounter := &brokerFillsQueryCounter{}
	restartedConfig := restartedPool.Config().Copy()
	restartedConfig.ConnConfig.Tracer = restartedCounter
	restartedTracedPool, err := pgxpool.NewWithConfig(ctx, restartedConfig)
	if err != nil {
		t.Fatalf("open traced restarted broker fills API pool: %v", err)
	}
	t.Cleanup(restartedTracedPool.Close)
	restartedAuthenticator, err := edge.NewHMACAuthenticator(
		edge.HMACAuthenticatorConfig{
			ClientTokenSecret: []byte(
				"broker-fills-client-secret-0123456789",
			),
			BrokerCredentials: []edge.BrokerCredential{{
				Prefix:     "xbk_brokerfills",
				SecretHash: edge.HashBrokerSecret("secret"),
				Subject:    "urn:xb:apikey:not-the-tenant",
				Tenant:     tenant,
				Scopes:     []string{"accounts:read"},
			}},
		},
	)
	if err != nil {
		t.Fatalf("recreate exact broker fills authenticator: %v", err)
	}
	restartedServer := edge.NewServer(edge.ServerConfig{
		Authenticator: restartedAuthenticator,
		BrokerFills: platformpostgres.NewCompatibilityStore(
			restartedTracedPool,
		),
		RequestID: func() string { return "broker-fills-exact" },
	}).Handler()
	restartedResponse := httptest.NewRecorder()
	restartedServer.ServeHTTP(restartedResponse, request.Clone(ctx))
	if restartedResponse.Code != http.StatusOK ||
		restartedResponse.Body.String() != wantBody {
		t.Fatalf(
			"restarted broker fills response status=%d body=%q, want exact success",
			restartedResponse.Code,
			restartedResponse.Body.String(),
		)
	}
	if restartedCounter.count.Load() != 1 {
		t.Fatalf(
			"restarted broker fills executed %d queries, want exactly one",
			restartedCounter.count.Load(),
		)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO trading.fills (
			fill_id, order_id, input_id, account_id, instrument_id,
			side, price, quantity, position_id, position_effect,
			realized_pnl, settlement_currency, liquidity_side,
			fee, fee_currency, logical_time, effective_leverage
		) VALUES (
			'00000000-0000-4000-8000-000000000607',
			'00000000-0000-4000-8000-000000000601',
			'00000000-0000-4000-8000-000000000608',
			'urn:xb:account:00000000-0000-4000-8000-000000000009',
			'BTC-PERP', 'SELL', 98, 1,
			'00000000-0000-4000-8000-000000000604', 'close',
			2, 'USDC', 'TAKER', 0, 'USDC', 25, 5
		)`,
	); err != nil {
		t.Fatalf("commit older fill after broker cursor: %v", err)
	}
	movingRequest := httptest.NewRequest(
		http.MethodGet,
		"/broker/v1/accounts/"+accountID+"/fills?limit=10&cursor="+
			"MTc4NDkwMTYwMDAwMDAwMDAwMDowMDAwMDAwMC0wMDAwLTQwMDAtODAwMC0wMDAwMDAwMDA2MDI",
		nil,
	)
	movingRequest.Header.Set("x-api-key", "xbk_brokerfills.secret")
	movingResponse := httptest.NewRecorder()
	restartedQueriesBefore := restartedCounter.count.Load()
	restartedServer.ServeHTTP(movingResponse, movingRequest)
	var movingPage edge.FillExecutionPage
	if movingResponse.Code != http.StatusOK ||
		json.Unmarshal(movingResponse.Body.Bytes(), &movingPage) != nil ||
		movingPage.Total != 3 ||
		len(movingPage.Items) != 2 ||
		movingPage.Items[0].FillID !=
			"00000000-0000-4000-8000-000000000605" ||
		movingPage.Items[0].Leverage != nil ||
		movingPage.Items[1].FillID !=
			"00000000-0000-4000-8000-000000000607" ||
		strings.Contains(movingResponse.Body.String(), `"leverage":null`) ||
		!strings.Contains(
			movingResponse.Body.String(),
			`"realizedPnl":null,"settlementCurrency":null`,
		) {
		t.Fatalf(
			"moving broker history status=%d page=%#v body=%s",
			movingResponse.Code,
			movingPage,
			movingResponse.Body.String(),
		)
	}
	if restartedCounter.count.Load()-restartedQueriesBefore != 1 {
		t.Fatalf(
			"restarted forward broker fills executed %d queries, want one",
			restartedCounter.count.Load()-restartedQueriesBefore,
		)
	}

	assertDenied := func(name string) {
		t.Helper()
		denied, deniedErr := store.BrokerFills(
			ctx,
			tenant,
			accountID,
			edge.FillExecutionFilter{},
		)
		if !errors.Is(deniedErr, edge.ErrForbidden) ||
			denied.Items != nil ||
			denied.Total != 0 ||
			denied.NextCursor != nil ||
			denied.PrevCursor != nil {
			t.Fatalf("%s result=%#v error=%v, want zero-page forbidden", name, denied, deniedErr)
		}
	}
	if _, err := pool.Exec(
		ctx,
		`DELETE FROM identity.account_profiles WHERE account_id = $1`,
		accountID,
	); err != nil {
		t.Fatalf("remove broker fills profile: %v", err)
	}
	assertDenied("ownership only")
	if _, err := pool.Exec(ctx, `
		INSERT INTO identity.account_profiles (
			account_id, login, base_currency, market_venue,
			permitted_classes, broker_subject, created_at
		) VALUES (
			$1, 9009, 'USDC', 'HYPERLIQUID',
			ARRAY['CRYPTOCURRENCY'], $2, '2026-07-30T00:00:00Z'
		)`,
		accountID,
		tenant,
	); err != nil {
		t.Fatalf("restore broker fills profile: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE identity.user_accounts
		   SET user_id = 'urn:xb:user:broker-fills-other',
		       broker_subject = 'urn:xb:tenant:other'
		 WHERE account_id = $1`,
		accountID,
	); err != nil {
		t.Fatalf("make broker fills ownership foreign: %v", err)
	}
	assertDenied("matching profile only")
	if _, err := pool.Exec(ctx, `
		UPDATE identity.user_accounts
		   SET user_id = $2,
		       broker_subject = $3
		 WHERE account_id = $1`,
		accountID,
		userID,
		tenant,
	); err != nil {
		t.Fatalf("restore broker fills ownership: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE identity.account_profiles
		   SET broker_subject = 'urn:xb:tenant:other'
		 WHERE account_id = $1`,
		accountID,
	); err != nil {
		t.Fatalf("make broker fills profile foreign: %v", err)
	}
	assertDenied("matching ownership only")
	if _, err := pool.Exec(ctx, `
		UPDATE identity.user_accounts
		   SET user_id = 'urn:xb:user:broker-fills-other',
		       broker_subject = 'urn:xb:tenant:other'
		 WHERE account_id = $1`,
		accountID,
	); err != nil {
		t.Fatalf("make both broker fills authorities foreign: %v", err)
	}
	assertDenied("both authorities foreign")

	if _, err := pool.Exec(ctx, `
		UPDATE identity.user_accounts
		   SET user_id = $2,
		       broker_subject = $3
		 WHERE account_id = $1`,
		accountID,
		userID,
		tenant,
	); err != nil {
		t.Fatalf("restore authorized broker fills ownership: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE identity.account_profiles SET broker_subject = $2 WHERE account_id = $1`,
		accountID,
		tenant,
	); err != nil {
		t.Fatalf("restore authorized broker fills profile: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO trading.accounts (account_id, oms_mode)
		VALUES (
			'urn:xb:account:00000000-0000-4000-8000-000000000010',
			'NETTING'
		);
		INSERT INTO trading.orders (
			order_id, account_id, instrument_id, side, order_type,
			time_in_force, status, quantity, filled_quantity,
			average_fill_price, triggered, reduce_only, has_rested, version
		) VALUES (
			'00000000-0000-4000-8000-000000000611',
			'urn:xb:account:00000000-0000-4000-8000-000000000010',
			'BTC-PERP', 'SELL', 'MARKET', 'IOC', 'FILLED',
			1, 1, 100, false, true, false, 1
		);
		INSERT INTO trading.fills (
			fill_id, order_id, input_id, account_id, instrument_id,
			side, price, quantity, position_id, position_effect,
			realized_pnl, settlement_currency, liquidity_side,
			fee, fee_currency, logical_time, effective_leverage
		) VALUES (
			'00000000-0000-4000-8000-000000000612',
			'00000000-0000-4000-8000-000000000611',
			'00000000-0000-4000-8000-000000000613',
			'urn:xb:account:00000000-0000-4000-8000-000000000009',
			'BTC-PERP', 'SELL', 100, 1,
			'00000000-0000-4000-8000-000000000614', 'close',
			1, 'USDC', 'TAKER', 0, 'USDC', 100, 5
		)`,
	); err != nil {
		t.Fatalf("seed late authorized broker fill corruption: %v", err)
	}
	response = httptest.NewRecorder()
	queriesBefore = queryCounter.count.Load()
	server.ServeHTTP(response, request.Clone(ctx))
	wantUnavailable := `{"code":"unavailable","message":"trading views unavailable","requestId":"broker-fills-exact"}` +
		"\n"
	if response.Code != http.StatusServiceUnavailable ||
		response.Body.String() != wantUnavailable {
		t.Fatalf(
			"corrupt broker fills response status=%d body=%q, want opaque 503",
			response.Code,
			response.Body.String(),
		)
	}
	if queryCounter.count.Load()-queriesBefore != 1 {
		t.Fatalf(
			"authorized corrupt broker fills executed %d queries, want one",
			queryCounter.count.Load()-queriesBefore,
		)
	}
	corruptRestartPool := runtimeRoleLoginPool(
		t,
		pool,
		"platformgo_broker_fills_corrupt_restart_api_login",
		"platformgo_api",
	)
	corruptRestartCounter := &brokerFillsQueryCounter{}
	corruptRestartConfig := corruptRestartPool.Config().Copy()
	corruptRestartConfig.ConnConfig.Tracer = corruptRestartCounter
	corruptRestartTracedPool, err := pgxpool.NewWithConfig(
		ctx,
		corruptRestartConfig,
	)
	if err != nil {
		t.Fatalf("open traced corrupt-restart broker fills API pool: %v", err)
	}
	t.Cleanup(corruptRestartTracedPool.Close)
	corruptRestartAuthenticator, err := edge.NewHMACAuthenticator(
		edge.HMACAuthenticatorConfig{
			ClientTokenSecret: []byte(
				"broker-fills-client-secret-0123456789",
			),
			BrokerCredentials: []edge.BrokerCredential{{
				Prefix:     "xbk_brokerfills",
				SecretHash: edge.HashBrokerSecret("secret"),
				Subject:    "urn:xb:apikey:not-the-tenant",
				Tenant:     tenant,
				Scopes:     []string{"accounts:read"},
			}},
		},
	)
	if err != nil {
		t.Fatalf("recreate corrupt broker fills authenticator: %v", err)
	}
	corruptRestartServer := edge.NewServer(edge.ServerConfig{
		Authenticator: corruptRestartAuthenticator,
		BrokerFills: platformpostgres.NewCompatibilityStore(
			corruptRestartTracedPool,
		),
		RequestID: func() string { return "broker-fills-exact" },
	}).Handler()
	corruptRestartResponse := httptest.NewRecorder()
	corruptRestartServer.ServeHTTP(corruptRestartResponse, request.Clone(ctx))
	if corruptRestartResponse.Code != http.StatusServiceUnavailable ||
		corruptRestartResponse.Body.String() != wantUnavailable {
		t.Fatalf(
			"restarted corrupt broker fills status=%d body=%q, want opaque 503",
			corruptRestartResponse.Code,
			corruptRestartResponse.Body.String(),
		)
	}
	if corruptRestartCounter.count.Load() != 1 {
		t.Fatalf(
			"restarted corrupt broker fills executed %d queries, want exactly one",
			corruptRestartCounter.count.Load(),
		)
	}
}

func TestBrokerFillsReverseCursorsCannotBypassTenantAuthority(t *testing.T) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := newCurrentTestMigrator(
		t,
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).Migrate(ctx); err != nil {
		t.Fatalf("migrate broker reverse-cursor database: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO trading.instruments (
			instrument_id, revision, price_scale, quantity_scale,
			settlement_currency, settlement_currency_scale,
			initial_margin_rate, maintenance_margin_rate, max_leverage,
			maker_fee_rate, taker_fee_rate
		) VALUES (
			'BTC-PERP', 1, 2, 3, 'USDC', 2,
			0.1, 0.05, 10, -0.0001, 0.0005
		);
		INSERT INTO trading.accounts (account_id, oms_mode) VALUES
			('urn:xb:account:00000000-0000-4000-8000-000000000101', 'NETTING'),
			('urn:xb:account:00000000-0000-4000-8000-000000000102', 'NETTING'),
			('urn:xb:account:00000000-0000-4000-8000-000000000103', 'NETTING');
		INSERT INTO identity.users (
			user_id, login, normalized_login, broker_subject
		) VALUES
			('urn:xb:user:broker-fill-valid', 'broker-fill-valid',
			 'broker-fill-valid', 'urn:xb:tenant:other'),
			('urn:xb:user:broker-fill-corrupt', 'broker-fill-corrupt',
			 'broker-fill-corrupt', 'urn:xb:tenant:other');
		INSERT INTO identity.user_accounts (
			user_id, account_id, broker_subject
		) VALUES
			('urn:xb:user:broker-fill-valid',
			 'urn:xb:account:00000000-0000-4000-8000-000000000101',
			 'urn:xb:tenant:other'),
			('urn:xb:user:broker-fill-corrupt',
			 'urn:xb:account:00000000-0000-4000-8000-000000000102',
			 'urn:xb:tenant:other');
		INSERT INTO identity.account_profiles (
			account_id, login, base_currency, market_venue,
			permitted_classes, broker_subject, created_at
		) VALUES
			('urn:xb:account:00000000-0000-4000-8000-000000000101',
			 9101, 'USDC', 'HYPERLIQUID', ARRAY['CRYPTOCURRENCY'],
			 'urn:xb:tenant:other', '2026-07-30T00:00:00Z'),
			('urn:xb:account:00000000-0000-4000-8000-000000000102',
			 9102, 'USDC', 'HYPERLIQUID', ARRAY['CRYPTOCURRENCY'],
			 'urn:xb:tenant:other', '2026-07-30T00:00:00Z');
		INSERT INTO trading.orders (
			order_id, account_id, instrument_id, side, order_type,
			time_in_force, status, quantity, filled_quantity,
			average_fill_price, triggered, reduce_only, has_rested, version
		) VALUES
			('00000000-0000-4000-8000-000000000201',
			 'urn:xb:account:00000000-0000-4000-8000-000000000101',
			 'BTC-PERP', 'BUY', 'MARKET', 'IOC', 'FILLED',
			 1, 1, 100, false, false, false, 1),
			('00000000-0000-4000-8000-000000000202',
			 'urn:xb:account:00000000-0000-4000-8000-000000000103',
			 'BTC-PERP', 'BUY', 'MARKET', 'IOC', 'FILLED',
			 1, 1, 100, false, false, false, 1);
		INSERT INTO trading.fills (
			fill_id, order_id, input_id, account_id, instrument_id,
			side, price, quantity, position_id, position_effect,
			realized_pnl, settlement_currency, liquidity_side,
			fee, fee_currency, logical_time
		) VALUES
			('00000000-0000-4000-8000-000000000301',
			 '00000000-0000-4000-8000-000000000201',
			 '00000000-0000-4000-8000-000000000401',
			 'urn:xb:account:00000000-0000-4000-8000-000000000101',
			 'BTC-PERP', 'BUY', 100, 1,
			 '00000000-0000-4000-8000-000000000501', 'open',
			 NULL, NULL, 'TAKER', 0, 'USDC', 200),
			('00000000-0000-4000-8000-000000000302',
			 '00000000-0000-4000-8000-000000000202',
			 '00000000-0000-4000-8000-000000000402',
			 'urn:xb:account:00000000-0000-4000-8000-000000000102',
			 'BTC-PERP', 'BUY', 100, 1,
			 '00000000-0000-4000-8000-000000000502', 'open',
			 NULL, NULL, 'TAKER', 0, 'USDC', 200)`,
	); err != nil {
		t.Fatalf("seed foreign broker fill histories: %v", err)
	}

	apiPool := runtimeRoleLoginPool(
		t,
		pool,
		"platformgo_broker_fills_reverse_api_login",
		"platformgo_api",
	)
	counter := &brokerFillsQueryCounter{}
	tracedConfig := apiPool.Config().Copy()
	tracedConfig.ConnConfig.Tracer = counter
	tracedPool, err := pgxpool.NewWithConfig(ctx, tracedConfig)
	if err != nil {
		t.Fatalf("open traced broker fills API pool: %v", err)
	}
	t.Cleanup(tracedPool.Close)
	store := platformpostgres.NewCompatibilityStore(tracedPool)
	authenticator, err := edge.NewHMACAuthenticator(edge.HMACAuthenticatorConfig{
		ClientTokenSecret: []byte("broker-fills-client-secret-0123456789"),
		BrokerCredentials: []edge.BrokerCredential{{
			Prefix:     "xbk_brokerfills",
			SecretHash: edge.HashBrokerSecret("secret"),
			Subject:    "urn:xb:apikey:broker-fills",
			Tenant:     "urn:xb:tenant:requesting",
			Scopes:     []string{"accounts:read"},
		}},
	})
	if err != nil {
		t.Fatalf("create broker fills authenticator: %v", err)
	}
	server := edge.NewServer(edge.ServerConfig{
		Authenticator: authenticator,
		BrokerFills:   store,
		RequestID:     func() string { return "broker-fills-reverse" },
	}).Handler()
	cursor := base64.RawURLEncoding.EncodeToString([]byte(
		"100:00000000-0000-4000-8000-000000000001",
	))
	wantBody := `{"code":"forbidden","message":"forbidden","requestId":"broker-fills-reverse"}` +
		"\n"
	for _, accountID := range []string{
		"urn:xb:account:00000000-0000-4000-8000-000000000101",
		"urn:xb:account:00000000-0000-4000-8000-000000000102",
	} {
		for _, direction := range []string{"prev", "backward"} {
			request := httptest.NewRequest(
				http.MethodGet,
				"/broker/v1/accounts/"+accountID+"/fills?cursor="+
					cursor+"&direction="+direction,
				nil,
			)
			request.Header.Set("x-api-key", "xbk_brokerfills.secret")
			response := httptest.NewRecorder()
			server.ServeHTTP(response, request)
			if response.Code != http.StatusForbidden ||
				response.Body.String() != wantBody {
				t.Fatalf(
					"account=%s direction=%s status=%d body=%q, want generic 403",
					accountID,
					direction,
					response.Code,
					response.Body.String(),
				)
			}
		}
	}
	if counter.count.Load() != 4 {
		t.Fatalf(
			"broker reverse-cursor PostgreSQL statements = %d, want exactly 4 for 4 requests",
			counter.count.Load(),
		)
	}
}
