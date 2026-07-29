package postgres_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/upcomers-org/platformgo/internal/adapters/postgres"
	"github.com/upcomers-org/platformgo/internal/application"
	"github.com/upcomers-org/platformgo/internal/edge"
)

func TestFillHistoryRejectsNaNLeverageWithoutPartialPageAfterRestart(
	t *testing.T,
) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := postgres.NewMigrator(
		pool,
		migrationFilesThrough(t, commandAdmissionACLMigration),
	).MigrateAndProvision(ctx, 7); err != nil {
		t.Fatalf("migrate pre-hardening fill database: %v", err)
	}

	const accountID = "urn:xb:account:fill-leverage-corruption"
	if _, err := pool.Exec(ctx, `
		INSERT INTO trading.instruments (
			instrument_id, revision, price_scale, quantity_scale,
			settlement_currency, settlement_currency_scale,
			initial_margin_rate, maintenance_margin_rate, max_leverage,
			maker_fee_rate, taker_fee_rate
		) VALUES (
			'BTC-PERP', 1, 2, 3, 'USDC', 2,
			0.1, 0.05, 10, 0, 0
		);
		INSERT INTO trading.accounts (account_id, oms_mode)
		VALUES ('urn:xb:account:fill-leverage-corruption', 'NETTING');
		INSERT INTO trading.orders (
			order_id, account_id, instrument_id, side, order_type,
			time_in_force, status, quantity, filled_quantity,
			average_fill_price, triggered, reduce_only, has_rested,
			version
		) VALUES (
			'019fae0d-7425-7000-8000-000000000001',
			'urn:xb:account:fill-leverage-corruption',
			'BTC-PERP', 'BUY', 'MARKET', 'IOC', 'FILLED',
			0.02, 0.02, 60000, false, false, false, 1
		);
		INSERT INTO trading.fills (
			fill_id, order_id, input_id, account_id, instrument_id,
			side, price, quantity, position_id, position_effect,
			liquidity_side, logical_time, effective_leverage
		) VALUES
			(
				'019fae0d-7425-7000-8000-000000000002',
				'019fae0d-7425-7000-8000-000000000001',
				'019fae0d-7425-7000-8000-000000000003',
				'urn:xb:account:fill-leverage-corruption',
				'BTC-PERP', 'BUY', 60000, 0.01,
				'019fae0d-7425-7000-8000-000000000004',
				'open', 'TAKER', 1785330000000000000, 5
			),
			(
				'019fae0d-7425-7000-8000-000000000005',
				'019fae0d-7425-7000-8000-000000000001',
				'019fae0d-7425-7000-8000-000000000006',
				'urn:xb:account:fill-leverage-corruption',
				'BTC-PERP', 'BUY', 60000, 0.01,
				'019fae0d-7425-7000-8000-000000000004',
				'increase', 'TAKER', 1785330000000000001, 'NaN'::numeric
			)`); err != nil {
		t.Fatalf("seed valid and NaN fill leverage: %v", err)
	}

	apiPool := runtimeRoleLoginPool(
		t,
		pool,
		"platformgo_fill_leverage_corruption_api_login",
		"platformgo_api",
	)
	store := postgres.NewCompatibilityStore(apiPool)
	page, err := store.FilterFillExecutions(
		ctx,
		accountID,
		postgres.FillExecutionFilter{Limit: 10},
	)
	if err == nil || len(page.Items) != 0 || page.Total != 0 {
		t.Fatalf(
			"NaN fill page = %#v, err=%v; want fail-closed zero page",
			page,
			err,
		)
	}
	latest, err := store.LatestFillExecution(ctx, accountID)
	if err == nil || latest.FillID != "" {
		t.Fatalf(
			"NaN latest fill = %#v, err=%v; want fail-closed zero view",
			latest,
			err,
		)
	}

	restartedPool := runtimeRoleLoginPool(
		t,
		pool,
		"platformgo_fill_leverage_corruption_restart_api_login",
		"platformgo_api",
	)
	authenticator, err := edge.NewHMACAuthenticator(
		edge.HMACAuthenticatorConfig{
			ClientTokenSecret: []byte(
				"phase3-fill-leverage-corruption-secret-0123456789abcdef",
			),
		},
	)
	if err != nil {
		t.Fatalf("create restarted fill authenticator: %v", err)
	}
	identity, err := application.NewIdentity(
		postgres.NewCompatibilityStore(restartedPool),
		authenticator,
		application.IdentityConfig{
			Entropy: bytes.NewReader(bytes.Repeat([]byte{37}, 64)),
		},
	)
	if err != nil {
		t.Fatalf("create restarted fill identity: %v", err)
	}
	token, err := authenticator.SignClientToken(edge.ClientClaims{
		Subject:  "urn:xb:user:fill-leverage-corruption",
		Audience: string(edge.AudienceClient),
		Expires:  4_102_444_800,
		Accounts: []string{accountID},
	})
	if err != nil {
		t.Fatalf("sign restarted fill token: %v", err)
	}
	server := edge.NewServer(edge.ServerConfig{
		Authenticator: authenticator,
		Identity:      identity,
		Trading:       postgres.NewCompatibilityStore(restartedPool),
	}).Handler()
	request := httptest.NewRequest(
		http.MethodGet,
		"/v1/accounts/"+accountID+"/fills?limit=10",
		nil,
	)
	request.Header.Set("authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	body := response.Body.String()
	if response.Code != http.StatusServiceUnavailable ||
		strings.Contains(body, `"items"`) ||
		strings.Contains(body, `"total"`) ||
		strings.Contains(body, "NaN") ||
		strings.Contains(body, "019fae0d-7425-7000-8000-000000000002") ||
		strings.Contains(body, "019fae0d-7425-7000-8000-000000000005") {
		t.Fatalf(
			"restarted NaN fill response status=%d body=%s; want opaque 503",
			response.Code,
			body,
		)
	}

	var (
		validCount int
		nanCount   int
	)
	if err := pool.QueryRow(ctx, `
		SELECT
			count(*) FILTER (WHERE effective_leverage = 5),
			count(*) FILTER (WHERE effective_leverage = 'NaN'::numeric)
		  FROM trading.fills
		 WHERE account_id = $1`,
		accountID,
	).Scan(&validCount, &nanCount); err != nil {
		t.Fatalf("read immutable leverage corruption fixture: %v", err)
	}
	if validCount != 1 || nanCount != 1 {
		t.Fatalf(
			"fill reader mutated corruption fixture: valid=%d NaN=%d",
			validCount,
			nanCount,
		)
	}
}
