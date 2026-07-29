package postgres_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
	"github.com/upcomers-org/platformgo/internal/application"
	"github.com/upcomers-org/platformgo/internal/edge"
	"github.com/upcomers-org/platformgo/internal/engine"
	"github.com/upcomers-org/platformgo/testkit"
)

// Ported from:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/trading/e2e_fills.rs:207
//	test: fill_filled_at_is_engine_execution_time_not_insert_now
//
// Adaptations:
//   - The native durable fill and its logical time replace the legacy mirror row.
//   - A fixed nanosecond timestamp replaces insert-time now().
//   - The PostgreSQL compatibility reader replaces the Rust query dispatcher.
//
// Assertions preserved:
//   - filledAt is the engine execution time, not database insertion time.
func TestFillFilledAtIsEngineExecutionTimeNotInsertNow(t *testing.T) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := platformpostgres.NewMigrator(
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).Migrate(ctx); err != nil {
		t.Fatalf("migrate fill history database: %v", err)
	}

	const (
		accountID  = "urn:xb:account:fill-time"
		userID     = "urn:xb:user:fill-time"
		orderID    = "019fa844-26c0-7000-8000-000000000001"
		fillID     = "019fa844-26c0-7000-8000-000000000002"
		inputID    = "019fa844-26c0-7000-8000-000000000003"
		positionID = "019fa844-26c0-7000-8000-000000000004"
	)
	engineTime := time.Date(2020, time.September, 13, 12, 26, 40, 123456789, time.UTC)
	if _, err := pool.Exec(ctx, `
		INSERT INTO engine.deployment_shard (shard_id) VALUES (41);
		INSERT INTO trading.instruments (
			instrument_id, revision, price_scale, quantity_scale,
			settlement_currency, settlement_currency_scale,
			initial_margin_rate, maintenance_margin_rate, max_leverage,
			maker_fee_rate, taker_fee_rate
		) VALUES (
			'BTC-PERP', 1, 2, 3, 'USDC', 2,
			0.1, 0.05, 10, -0.0001, 0.0005
		);
		INSERT INTO trading.accounts (account_id, oms_mode)
		VALUES ('urn:xb:account:fill-time', 'NETTING');
		INSERT INTO engine.account_shards (account_id, shard_id)
		VALUES ('urn:xb:account:fill-time', 41);
		INSERT INTO identity.users (
			user_id, login, normalized_login, broker_subject
		) VALUES (
			'urn:xb:user:fill-time', 'fill-time', 'fill-time',
			'urn:xb:tenant:fill-proof'
		);
		INSERT INTO identity.user_accounts (
			user_id, account_id, broker_subject
		) VALUES (
			'urn:xb:user:fill-time', 'urn:xb:account:fill-time',
			'urn:xb:tenant:fill-proof'
		)`); err != nil {
		t.Fatalf("seed fill identities: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO identity.account_profiles (
			account_id, login, base_currency, market_venue,
			permitted_classes, created_at, broker_subject
		) VALUES (
			'urn:xb:account:fill-time', 1001, 'USDC', 'HYPERLIQUID',
			ARRAY['CRYPTOCURRENCY'], $1,
			'urn:xb:tenant:fill-proof'
		)`,
		engineTime,
	); err != nil {
		t.Fatalf("seed fill profile: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO trading.orders (
			order_id, account_id, instrument_id, side, order_type,
			time_in_force, status, quantity, filled_quantity,
			average_fill_price, triggered, reduce_only, has_rested,
			version
		) VALUES (
			'019fa844-26c0-7000-8000-000000000001',
			'urn:xb:account:fill-time', 'BTC-PERP', 'BUY', 'MARKET',
			'IOC', 'FILLED', 0.01, 0.01, 60000,
			false, false, false, 1
		)`); err != nil {
		t.Fatalf("seed fill order: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO trading.fills (
			fill_id, order_id, input_id, account_id, instrument_id,
			side, price, quantity, position_id, position_effect,
			realized_pnl, settlement_currency, liquidity_side,
			fee, fee_currency, logical_time
		) VALUES (
			'019fa844-26c0-7000-8000-000000000002',
			'019fa844-26c0-7000-8000-000000000001',
			'019fa844-26c0-7000-8000-000000000003',
			'urn:xb:account:fill-time', 'BTC-PERP',
			'BUY', 60000, 0.01,
			'019fa844-26c0-7000-8000-000000000004', 'open',
			NULL, NULL, 'TAKER', 0.5, 'USDC', $1
		)`,
		engineTime.UnixNano(),
	); err != nil {
		t.Fatalf("seed durable fill: %v", err)
	}

	apiPool := runtimeRoleLoginPool(
		t,
		pool,
		"platformgo_fill_history_api_login",
		"platformgo_api",
	)
	latest, err := platformpostgres.NewCompatibilityStore(apiPool).LatestFillExecution(
		ctx,
		accountID,
	)
	if err != nil {
		t.Fatalf("read latest fill execution: %v", err)
	}
	if latest.FilledAt != engineTime.Format(time.RFC3339Nano) {
		t.Fatalf(
			"filledAt = %q, want engine time %q",
			latest.FilledAt,
			engineTime.Format(time.RFC3339Nano),
		)
	}
}

func TestFilterFillExecutionsRejectsRealizedPnLBeyondRegisteredCurrencyScale(
	t *testing.T,
) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := platformpostgres.NewMigrator(
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).Migrate(ctx); err != nil {
		t.Fatalf("migrate fill money-scale database: %v", err)
	}

	const accountID = "urn:xb:account:fill-money-scale"
	if _, err := pool.Exec(ctx, `
		INSERT INTO identity.users (
			user_id, login, normalized_login
		) VALUES (
			'urn:xb:user:fill-money-scale',
			'fill-money-scale',
			'fill-money-scale'
		);
		INSERT INTO trading.instruments (
			instrument_id, revision, price_scale, quantity_scale,
			settlement_currency, settlement_currency_scale,
			initial_margin_rate, maintenance_margin_rate, max_leverage,
			maker_fee_rate, taker_fee_rate
		) VALUES (
			'BTC-PERP', 1, 2, 3, 'USDC', 2,
			0.1, 0.05, 10, -0.0001, 0.0005
		);
		INSERT INTO trading.accounts (account_id, oms_mode)
		VALUES ('urn:xb:account:fill-money-scale', 'NETTING');
		INSERT INTO trading.orders (
			order_id, account_id, instrument_id, side, order_type,
			time_in_force, status, quantity, filled_quantity,
			average_fill_price, triggered, reduce_only, has_rested,
			version
		) VALUES (
			'019fa844-26c0-7000-8000-000000000201',
			'urn:xb:account:fill-money-scale',
			'BTC-PERP', 'SELL', 'MARKET', 'IOC', 'FILLED',
			0.01, 0.01, 60000, false, true, false, 1
		);
		INSERT INTO trading.fills (
			fill_id, order_id, input_id, account_id, instrument_id,
			side, price, quantity, position_id, position_effect,
			realized_pnl, settlement_currency, liquidity_side,
			logical_time
		) VALUES (
			'019fa844-26c0-7000-8000-000000000202',
			'019fa844-26c0-7000-8000-000000000201',
			'019fa844-26c0-7000-8000-000000000203',
			'urn:xb:account:fill-money-scale',
			'BTC-PERP', 'SELL', 60000, 0.01,
			'019fa844-26c0-7000-8000-000000000204',
			'close', 1.234, 'USDC', 'TAKER',
			1784901600000000202
		)`); err != nil {
		t.Fatalf("seed over-scale realized PnL: %v", err)
	}

	apiPool := runtimeRoleLoginPool(
		t,
		pool,
		"platformgo_fill_money_scale_api_login",
		"platformgo_api",
	)
	page, err := platformpostgres.NewCompatibilityStore(apiPool).
		FilterFillExecutions(
			ctx,
			accountID,
			platformpostgres.FillExecutionFilter{Limit: 10},
		)
	if err == nil || len(page.Items) != 0 || page.Total != 0 {
		t.Fatalf(
			"over-scale fill page = %#v, err=%v; want fail-closed zero page",
			page,
			err,
		)
	}

	authenticator, err := edge.NewHMACAuthenticator(
		edge.HMACAuthenticatorConfig{
			ClientTokenSecret: []byte(
				"phase3-fill-http-money-secret-0123456789abcdef",
			),
		},
	)
	if err != nil {
		t.Fatalf("create fill HTTP authenticator: %v", err)
	}
	identity, err := application.NewIdentity(
		platformpostgres.NewCompatibilityStore(apiPool),
		authenticator,
		application.IdentityConfig{
			Entropy: bytes.NewReader(bytes.Repeat([]byte{29}, 64)),
		},
	)
	if err != nil {
		t.Fatalf("create fill HTTP identity: %v", err)
	}
	token, err := authenticator.SignClientToken(edge.ClientClaims{
		Subject:  "urn:xb:user:fill-money-scale",
		Audience: string(edge.AudienceClient),
		Expires:  4_102_444_800,
		Accounts: []string{accountID},
	})
	if err != nil {
		t.Fatalf("sign fill HTTP token: %v", err)
	}
	server := edge.NewServer(edge.ServerConfig{
		Authenticator: authenticator,
		Identity:      identity,
		Trading:       platformpostgres.NewCompatibilityStore(apiPool),
	}).Handler()
	request := httptest.NewRequest(
		http.MethodGet,
		"/v1/accounts/"+accountID+"/fills",
		nil,
	)
	request.Header.Set("authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable ||
		strings.Contains(response.Body.String(), "1.234") ||
		strings.Contains(response.Body.String(), `"items"`) {
		t.Fatalf(
			"over-scale HTTP response status=%d body=%s; want opaque 503",
			response.Code,
			response.Body.String(),
		)
	}
}

// Ported from:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/trading/e2e_fills.rs:63
//	test: fills_history_reads_and_paginates
//
// Adaptations:
//   - Deterministic UUID fill IDs and immutable engine fills replace the
//     legacy mirror's free-form fill IDs.
//   - The current narrow Go fill projection is exposed through its reviewed
//     owner-scoped HTTP route without inventing unavailable catalog metadata.
//   - An opaque keyset cursor replaces the source query dispatcher's cursor.
//
// Assertions preserved:
//   - The requested limit is honored.
//   - The first page reports the complete account-scoped total and a cursor
//     when older rows remain.
//   - Following that cursor returns the final row.
//   - Every fill is returned exactly once across the two pages.
//
// Strengthening:
//   - All three fills share one logical time, proving the immutable fill ID is
//     a deterministic tie-break rather than relying on insertion order.
func TestFillsHistoryReadsAndPaginates(t *testing.T) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := platformpostgres.NewMigrator(
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).Migrate(ctx); err != nil {
		t.Fatalf("migrate fill pagination database: %v", err)
	}

	const (
		accountID = "urn:xb:account:fill-pagination"
	)
	fillIDs := []string{
		"019fa844-26c0-7000-8000-000000000101",
		"019fa844-26c0-7000-8000-000000000102",
		"019fa844-26c0-7000-8000-000000000103",
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO identity.users (
			user_id, login, normalized_login
		) VALUES (
			'urn:xb:user:fill-pagination',
			'fill-pagination',
			'fill-pagination'
		);
		INSERT INTO trading.instruments (
			instrument_id, revision, price_scale, quantity_scale,
			settlement_currency, settlement_currency_scale,
			initial_margin_rate, maintenance_margin_rate, max_leverage,
			maker_fee_rate, taker_fee_rate
		) VALUES (
			'BTC-PERP', 1, 2, 3, 'USDC', 2,
			0.1, 0.05, 10, -0.0001, 0.0005
		);
		INSERT INTO trading.accounts (account_id, oms_mode)
		VALUES ('urn:xb:account:fill-pagination', 'NETTING');
		INSERT INTO trading.orders (
			order_id, account_id, instrument_id, side, order_type,
			time_in_force, status, quantity, filled_quantity,
			average_fill_price, triggered, reduce_only, has_rested,
			version
		) VALUES (
			'019fa844-26c0-7000-8000-000000000111',
			'urn:xb:account:fill-pagination', 'BTC-PERP', 'BUY', 'MARKET',
			'IOC', 'FILLED', 0.03, 0.03, 60000,
			false, false, false, 1
		);
		INSERT INTO trading.fills (
			fill_id, order_id, input_id, account_id, instrument_id,
			side, price, quantity, position_id, position_effect,
			liquidity_side, logical_time
		) VALUES
			(
				'019fa844-26c0-7000-8000-000000000101',
				'019fa844-26c0-7000-8000-000000000111',
				'019fa844-26c0-7000-8000-000000000121',
				'urn:xb:account:fill-pagination', 'BTC-PERP',
				'BUY', 60000, 0.01,
				'019fa844-26c0-7000-8000-000000000131',
				'open', 'TAKER', 1784901600000000000
			),
			(
				'019fa844-26c0-7000-8000-000000000102',
				'019fa844-26c0-7000-8000-000000000111',
				'019fa844-26c0-7000-8000-000000000122',
				'urn:xb:account:fill-pagination', 'BTC-PERP',
				'BUY', 60000, 0.01,
				'019fa844-26c0-7000-8000-000000000131',
				'increase', 'TAKER', 1784901600000000000
			),
			(
				'019fa844-26c0-7000-8000-000000000103',
				'019fa844-26c0-7000-8000-000000000111',
				'019fa844-26c0-7000-8000-000000000123',
				'urn:xb:account:fill-pagination', 'BTC-PERP',
				'BUY', 60000, 0.01,
				'019fa844-26c0-7000-8000-000000000131',
				'increase', 'TAKER', 1784901600000000000
			)`); err != nil {
		t.Fatalf("seed durable fill pagination: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO trading.accounts (account_id, oms_mode)
		VALUES ('urn:xb:account:fill-pagination-other', 'NETTING');
		INSERT INTO trading.orders (
			order_id, account_id, instrument_id, side, order_type,
			time_in_force, status, quantity, filled_quantity,
			average_fill_price, triggered, reduce_only, has_rested,
			version
		) VALUES (
			'019fa844-26c0-7000-8000-000000000141',
			'urn:xb:account:fill-pagination-other',
			'BTC-PERP', 'BUY', 'MARKET', 'IOC', 'FILLED',
			0.01, 0.01, 60000, false, false, false, 1
		);
		INSERT INTO trading.fills (
			fill_id, order_id, input_id, account_id, instrument_id,
			side, price, quantity, position_id, position_effect,
			liquidity_side, logical_time
		) VALUES (
			'019fa844-26c0-7000-8000-000000000142',
			'019fa844-26c0-7000-8000-000000000141',
			'019fa844-26c0-7000-8000-000000000143',
			'urn:xb:account:fill-pagination-other',
			'BTC-PERP', 'BUY', 60000, 0.01,
			'019fa844-26c0-7000-8000-000000000144',
			'open', 'TAKER', 1784901600000000000
		)`); err != nil {
		t.Fatalf("seed foreign fill pagination authority: %v", err)
	}

	apiPool := runtimeRoleLoginPool(
		t,
		pool,
		"platformgo_fill_pagination_api_login",
		"platformgo_api",
	)
	store := platformpostgres.NewCompatibilityStore(apiPool)
	pageOne, err := store.FilterFillExecutions(
		ctx,
		accountID,
		platformpostgres.FillExecutionFilter{Side: "buy", Limit: 2},
	)
	if err != nil {
		t.Fatalf("read first fill page: %v", err)
	}
	if len(pageOne.Items) != 2 ||
		pageOne.Total != 3 ||
		pageOne.NextCursor == nil {
		t.Fatalf("first fill page = %#v", pageOne)
	}
	if pageOne.Items[0].FillID != fillIDs[2] ||
		pageOne.Items[1].FillID != fillIDs[1] {
		t.Fatalf("first fill page order = %#v", pageOne.Items)
	}
	canonicalFirst, err := store.FilterFillExecutions(
		ctx,
		accountID,
		platformpostgres.FillExecutionFilter{
			Side:      "buy",
			Limit:     2,
			Direction: "prev",
		},
	)
	if err != nil {
		t.Fatalf("read cursorless previous fill page: %v", err)
	}
	if len(canonicalFirst.Items) != 2 ||
		canonicalFirst.Items[0].FillID != fillIDs[2] ||
		canonicalFirst.Items[1].FillID != fillIDs[1] ||
		canonicalFirst.Total != 3 ||
		canonicalFirst.NextCursor == nil ||
		canonicalFirst.PrevCursor != nil {
		t.Fatalf("cursorless previous fill page = %#v", canonicalFirst)
	}

	pageTwo, err := store.FilterFillExecutions(
		ctx,
		accountID,
		platformpostgres.FillExecutionFilter{
			Side:   "buy",
			Limit:  2,
			Cursor: *pageOne.NextCursor,
		},
	)
	if err != nil {
		t.Fatalf("read second fill page: %v", err)
	}
	if len(pageTwo.Items) != 1 ||
		pageTwo.Items[0].FillID != fillIDs[0] ||
		pageTwo.PrevCursor == nil ||
		pageTwo.Total != 3 {
		t.Fatalf("second fill page = %#v", pageTwo)
	}
	emptyForward, err := store.FilterFillExecutions(
		ctx,
		accountID,
		platformpostgres.FillExecutionFilter{
			Side:   "buy",
			Limit:  2,
			Cursor: *pageTwo.PrevCursor,
		},
	)
	if err != nil {
		t.Fatalf("read empty forward fill page: %v", err)
	}
	if len(emptyForward.Items) != 0 ||
		emptyForward.Total != 3 ||
		emptyForward.NextCursor != nil ||
		emptyForward.PrevCursor != nil {
		t.Fatalf("empty forward fill page = %#v", emptyForward)
	}
	seen := make(map[string]int, len(fillIDs))
	for _, fill := range append(pageOne.Items, pageTwo.Items...) {
		seen[fill.FillID]++
	}
	for _, fillID := range fillIDs {
		if seen[fillID] != 1 {
			t.Fatalf("fill %s returned %d times across pages", fillID, seen[fillID])
		}
	}

	backward, err := store.FilterFillExecutions(
		ctx,
		accountID,
		platformpostgres.FillExecutionFilter{
			Side:      "buy",
			Limit:     2,
			Cursor:    *pageTwo.PrevCursor,
			Direction: "prev",
		},
	)
	if err != nil {
		t.Fatalf("read previous fill page: %v", err)
	}
	if len(backward.Items) != 2 ||
		backward.Items[0].FillID != fillIDs[2] ||
		backward.Items[1].FillID != fillIDs[1] ||
		backward.Total != 3 {
		t.Fatalf("previous fill page = %#v", backward)
	}
	newest, err := store.FilterFillExecutions(
		ctx,
		accountID,
		platformpostgres.FillExecutionFilter{
			Side:      "buy",
			Limit:     2,
			Cursor:    *backward.NextCursor,
			Direction: "backward",
		},
	)
	if err != nil {
		t.Fatalf("read newest backward fill page: %v", err)
	}
	if len(newest.Items) != 1 ||
		newest.Items[0].FillID != fillIDs[2] ||
		newest.Total != 3 ||
		newest.NextCursor == nil {
		t.Fatalf("newest backward fill page = %#v", newest)
	}
	emptyBackward, err := store.FilterFillExecutions(
		ctx,
		accountID,
		platformpostgres.FillExecutionFilter{
			Side:      "buy",
			Limit:     2,
			Cursor:    *newest.NextCursor,
			Direction: "backward",
		},
	)
	if err != nil {
		t.Fatalf("read empty backward fill page: %v", err)
	}
	if len(emptyBackward.Items) != 0 ||
		emptyBackward.Total != 3 ||
		emptyBackward.NextCursor != nil ||
		emptyBackward.PrevCursor != nil {
		t.Fatalf("empty backward fill page = %#v", emptyBackward)
	}

	invalid, err := store.FilterFillExecutions(
		ctx,
		accountID,
		platformpostgres.FillExecutionFilter{
			Limit:  2,
			Cursor: "not-a-fill-cursor",
		},
	)
	if err == nil || len(invalid.Items) != 0 || invalid.Total != 0 {
		t.Fatalf("invalid fill cursor page = %#v, error %v", invalid, err)
	}

	authenticator, err := edge.NewHMACAuthenticator(
		edge.HMACAuthenticatorConfig{
			ClientTokenSecret: []byte(
				"phase3-fill-http-page-secret-0123456789abcdef",
			),
		},
	)
	if err != nil {
		t.Fatalf("create fill page authenticator: %v", err)
	}
	identity, err := application.NewIdentity(
		store,
		authenticator,
		application.IdentityConfig{
			Entropy: bytes.NewReader(bytes.Repeat([]byte{31}, 64)),
		},
	)
	if err != nil {
		t.Fatalf("create fill page identity: %v", err)
	}
	token, err := authenticator.SignClientToken(edge.ClientClaims{
		Subject:  "urn:xb:user:fill-pagination",
		Audience: string(edge.AudienceClient),
		Expires:  4_102_444_800,
		Accounts: []string{accountID},
	})
	if err != nil {
		t.Fatalf("sign fill page token: %v", err)
	}
	server := edge.NewServer(edge.ServerConfig{
		Authenticator: authenticator,
		Identity:      identity,
		Trading:       platformpostgres.NewCompatibilityStore(apiPool),
	}).Handler()
	request := httptest.NewRequest(
		http.MethodGet,
		"/v1/accounts/"+accountID+"/fills?side=buy&limit=2",
		nil,
	)
	request.Header.Set("authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf(
			"fill page HTTP status=%d body=%s",
			response.Code,
			response.Body.String(),
		)
	}
	var httpPage edge.FillExecutionPage
	if err := json.Unmarshal(response.Body.Bytes(), &httpPage); err != nil {
		t.Fatalf("decode fill page HTTP body: %v", err)
	}
	if len(httpPage.Items) != 2 ||
		httpPage.Total != 3 ||
		httpPage.NextCursor == nil ||
		httpPage.Items[0].FillID != fillIDs[2] ||
		httpPage.Items[1].FillID != fillIDs[1] {
		t.Fatalf("fill page HTTP body = %#v", httpPage)
	}
	secondRequest := httptest.NewRequest(
		http.MethodGet,
		"/v1/accounts/"+accountID+"/fills?side=buy&limit=2&cursor="+
			*httpPage.NextCursor,
		nil,
	)
	secondRequest.Header.Set("authorization", "Bearer "+token)
	secondResponse := httptest.NewRecorder()
	server.ServeHTTP(secondResponse, secondRequest)
	if secondResponse.Code != http.StatusOK {
		t.Fatalf(
			"second fill page HTTP status=%d body=%s",
			secondResponse.Code,
			secondResponse.Body.String(),
		)
	}
	var secondHTTPPage edge.FillExecutionPage
	if err := json.Unmarshal(
		secondResponse.Body.Bytes(),
		&secondHTTPPage,
	); err != nil {
		t.Fatalf("decode second fill page HTTP body: %v", err)
	}
	if len(secondHTTPPage.Items) != 1 ||
		secondHTTPPage.Items[0].FillID != fillIDs[0] ||
		secondHTTPPage.Total != 3 ||
		secondHTTPPage.PrevCursor == nil {
		t.Fatalf("second fill page HTTP body = %#v", secondHTTPPage)
	}

	foreign := httptest.NewRequest(
		http.MethodGet,
		"/v1/accounts/urn:xb:account:fill-pagination-other/fills",
		nil,
	)
	foreign.Header.Set("authorization", "Bearer "+token)
	foreignResponse := httptest.NewRecorder()
	server.ServeHTTP(foreignResponse, foreign)
	if foreignResponse.Code != http.StatusForbidden ||
		strings.Contains(foreignResponse.Body.String(), "000000000142") {
		t.Fatalf(
			"foreign fill HTTP status=%d body=%s",
			foreignResponse.Code,
			foreignResponse.Body.String(),
		)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO trading.fills (
			fill_id, order_id, input_id, account_id, instrument_id,
			side, price, quantity, position_id, position_effect,
			liquidity_side, logical_time
		) VALUES (
			'019fa844-26c0-7000-8000-000000000100',
			'019fa844-26c0-7000-8000-000000000111',
			'019fa844-26c0-7000-8000-000000000120',
			'urn:xb:account:fill-pagination',
			'BTC-PERP', 'BUY', 60000, 0.01,
			'019fa844-26c0-7000-8000-000000000131',
			'increase', 'TAKER', 1784901600000000000
		)`); err != nil {
		t.Fatalf("commit fill below existing cursor: %v", err)
	}
	movingContinuation, err := store.FilterFillExecutions(
		ctx,
		accountID,
		platformpostgres.FillExecutionFilter{
			Side:   "buy",
			Limit:  10,
			Cursor: *pageOne.NextCursor,
		},
	)
	if err != nil {
		t.Fatalf("continue moving fill page: %v", err)
	}
	if movingContinuation.Total != 4 ||
		len(movingContinuation.Items) != 2 ||
		movingContinuation.Items[0].FillID != fillIDs[0] ||
		movingContinuation.Items[1].FillID !=
			"019fa844-26c0-7000-8000-000000000100" {
		t.Fatalf("moving continuation after older commit = %#v", movingContinuation)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO trading.fills (
			fill_id, order_id, input_id, account_id, instrument_id,
			side, price, quantity, position_id, position_effect,
			liquidity_side, logical_time
		) VALUES (
			'019fa844-26c0-7000-8000-000000000104',
			'019fa844-26c0-7000-8000-000000000111',
			'019fa844-26c0-7000-8000-000000000124',
			'urn:xb:account:fill-pagination',
			'BTC-PERP', 'BUY', 60000, 0.01,
			'019fa844-26c0-7000-8000-000000000131',
			'increase', 'TAKER', 1784901600000000000
		)`); err != nil {
		t.Fatalf("commit fill above existing cursor: %v", err)
	}
	movingContinuation, err = store.FilterFillExecutions(
		ctx,
		accountID,
		platformpostgres.FillExecutionFilter{
			Side:   "buy",
			Limit:  10,
			Cursor: *pageOne.NextCursor,
		},
	)
	if err != nil {
		t.Fatalf("continue moving page after newer commit: %v", err)
	}
	if movingContinuation.Total != 5 ||
		len(movingContinuation.Items) != 2 {
		t.Fatalf("moving continuation after newer commit = %#v", movingContinuation)
	}
	for _, fill := range movingContinuation.Items {
		if fill.FillID == "019fa844-26c0-7000-8000-000000000104" {
			t.Fatalf("newer fill crossed prior cursor: %#v", movingContinuation)
		}
	}
	freshMovingPage, err := store.FilterFillExecutions(
		ctx,
		accountID,
		platformpostgres.FillExecutionFilter{Side: "buy", Limit: 1},
	)
	if err != nil {
		t.Fatalf("reload moving first page: %v", err)
	}
	if freshMovingPage.Total != 5 ||
		len(freshMovingPage.Items) != 1 ||
		freshMovingPage.Items[0].FillID !=
			"019fa844-26c0-7000-8000-000000000104" {
		t.Fatalf("fresh moving page = %#v", freshMovingPage)
	}
}

// Ported from:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/trading/e2e_fills.rs:849
//	test: fill_realized_isolates_hedged_legs_by_position
//
// Adaptations:
//   - The deterministic Go engine creates and closes two real hedged legs;
//     direct writes to the legacy fill mirror are removed.
//   - Current Go position UUIDs remain the authoritative internal projection.
//   - Opening fills retain the current Go absence of realized PnL rather than
//     manufacturing a numeric zero.
//
// Assertions preserved:
//   - The long close realizes its own exact positive PnL.
//   - The short close realizes its own exact positive PnL.
//   - Opening fills do not report realized profit from another leg.
//   - Long and short fills retain distinct, correlatable position identities.
//
// Strengthening:
//   - The same deterministic engine decisions are committed atomically to
//     PostgreSQL and read through the least-privilege compatibility role.
func TestFillRealizedIsolatesHedgedLegsByPosition(t *testing.T) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := platformpostgres.NewMigrator(
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).MigrateAndProvision(ctx, 7); err != nil {
		t.Fatalf("migrate hedged fill realization database: %v", err)
	}

	store := platformpostgres.NewEngineStore(pool)
	state := engine.NewState(7)
	ids := testkit.NewShardIDSequence(7)
	clock := testkit.NewManualClock(engine.NewLogicalTime(
		time.Date(2026, time.July, 26, 18, 0, 0, 0, time.UTC),
	))
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
			AccountID: "urn:xb:account:hedged-fill-realization",
			OmsMode:   engine.OmsModeHedging,
		},
	})
	apply(engine.TradingAction{
		Kind: engine.TradingActionAdjustBalance,
		AdjustBalance: &engine.AdjustBalance{
			AccountID:     "urn:xb:account:hedged-fill-realization",
			Currency:      "USDC",
			CurrencyScale: 2,
			Operation:     engine.BalanceOperationDeposit,
			Amount:        "1000",
		},
	})
	updateBook := func(mark, bid, ask string) {
		apply(engine.TradingAction{
			Kind: engine.TradingActionUpdateBook,
			UpdateBook: &engine.UpdateBook{
				InstrumentID: "BTC-PERP",
				MarkPrice:    mark,
				Bids: []engine.BookLevel{{
					Price: bid, Quantity: "10",
				}},
				Asks: []engine.BookLevel{{
					Price: ask, Quantity: "10",
				}},
			},
		})
	}
	submit := func(
		orderID engine.ID,
		side engine.Side,
		positionID engine.ID,
		reduceOnly bool,
	) engine.FillSnapshot {
		decision := apply(engine.TradingAction{
			Kind: engine.TradingActionSubmitOrder,
			SubmitOrder: &engine.SubmitOrder{
				OrderID:      orderID,
				AccountID:    "urn:xb:account:hedged-fill-realization",
				InstrumentID: "BTC-PERP",
				Side:         side,
				Type:         engine.OrderTypeMarket,
				TimeInForce:  engine.TimeInForceGTC,
				Quantity:     "1",
				ReduceOnly:   reduceOnly,
				PositionID:   positionID,
			},
		})
		if len(decision.Fills) != 1 {
			t.Fatalf(
				"order %s decision = %#v, want one fill",
				orderID,
				decision,
			)
		}
		return decision.Fills[0]
	}

	updateBook("100", "100", "101")
	longOpen := submit(
		engine.IDFromSequence(engine.ID{}, 4101),
		engine.SideBuy,
		engine.ID{},
		false,
	)
	shortOpen := submit(
		engine.IDFromSequence(engine.ID{}, 4102),
		engine.SideSell,
		engine.ID{},
		false,
	)
	if longOpen.PositionID == shortOpen.PositionID {
		t.Fatalf(
			"hedged opening fills share position %s",
			longOpen.PositionID,
		)
	}

	updateBook("151", "151", "152")
	longClose := submit(
		engine.IDFromSequence(engine.ID{}, 4103),
		engine.SideSell,
		longOpen.PositionID,
		true,
	)
	updateBook("70", "69", "70")
	shortClose := submit(
		engine.IDFromSequence(engine.ID{}, 4104),
		engine.SideBuy,
		shortOpen.PositionID,
		true,
	)

	apiPool := runtimeRoleLoginPool(
		t,
		pool,
		"platformgo_fill_hedged_realization_api_login",
		"platformgo_api",
	)
	page, err := platformpostgres.NewCompatibilityStore(apiPool).
		FilterFillExecutions(
			ctx,
			"urn:xb:account:hedged-fill-realization",
			platformpostgres.FillExecutionFilter{Limit: 10},
		)
	if err != nil {
		t.Fatalf("read hedged fill realization: %v", err)
	}
	if len(page.Items) != 4 || page.Total != 4 {
		t.Fatalf("hedged fill page = %#v, want four fills", page)
	}
	byID := make(map[string]struct {
		positionID         string
		realizedPnL        *string
		settlementCurrency *string
	}, len(page.Items))
	for _, fill := range page.Items {
		byID[fill.FillID] = struct {
			positionID         string
			realizedPnL        *string
			settlementCurrency *string
		}{
			positionID:         fill.PositionID,
			realizedPnL:        fill.RealizedPnL,
			settlementCurrency: fill.SettlementCurrency,
		}
	}
	assertFill := func(
		fill engine.FillSnapshot,
		wantPositionID string,
		wantRealizedPnL *string,
		wantCurrency *string,
	) {
		t.Helper()
		got, ok := byID[fill.FillID.String()]
		if !ok {
			t.Fatalf("fill %s missing from compatibility page", fill.FillID)
		}
		if got.positionID != wantPositionID {
			t.Fatalf(
				"fill %s position = %q, want %q",
				fill.FillID,
				got.positionID,
				wantPositionID,
			)
		}
		if (got.realizedPnL == nil) != (wantRealizedPnL == nil) ||
			(got.realizedPnL != nil &&
				*got.realizedPnL != *wantRealizedPnL) {
			t.Fatalf(
				"fill %s realized PnL = %v, want %v",
				fill.FillID,
				got.realizedPnL,
				wantRealizedPnL,
			)
		}
		if (got.settlementCurrency == nil) != (wantCurrency == nil) ||
			(got.settlementCurrency != nil &&
				*got.settlementCurrency != *wantCurrency) {
			t.Fatalf(
				"fill %s settlement currency = %v, want %v",
				fill.FillID,
				got.settlementCurrency,
				wantCurrency,
			)
		}
	}
	longProfit := "50"
	shortProfit := "30"
	settlementCurrency := "USDC"
	assertFill(longOpen, longOpen.PositionID.String(), nil, nil)
	assertFill(shortOpen, shortOpen.PositionID.String(), nil, nil)
	assertFill(
		longClose,
		longOpen.PositionID.String(),
		&longProfit,
		&settlementCurrency,
	)
	assertFill(
		shortClose,
		shortOpen.PositionID.String(),
		&shortProfit,
		&settlementCurrency,
	)
}

// Ported from:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/trading/e2e_fills.rs:328
//	test: fills_history_filters_by_side_and_trade_id
//
// Adaptations:
//   - Deterministic UUID fill IDs replace the legacy mirror's free-form IDs.
//   - Durable immutable fills replace legacy mirror rows.
//   - The PostgreSQL compatibility reader replaces the Rust query dispatcher.
//
// Assertions preserved:
//   - A lowercase side filter returns only the matching fill and filtered total.
//   - A trade ID filter returns exactly the requested fill.
func TestFillsHistoryFiltersBySideAndTradeID(t *testing.T) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := platformpostgres.NewMigrator(
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).Migrate(ctx); err != nil {
		t.Fatalf("migrate fill filtering database: %v", err)
	}

	const (
		accountID  = "urn:xb:account:fill-filter"
		buyFillID  = "019fa844-26c0-7000-8000-000000000011"
		sellFillID = "019fa844-26c0-7000-8000-000000000012"
	)
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
		INSERT INTO trading.accounts (account_id, oms_mode)
		VALUES ('urn:xb:account:fill-filter', 'NETTING');
		INSERT INTO trading.orders (
			order_id, account_id, instrument_id, side, order_type,
			time_in_force, status, quantity, filled_quantity,
			average_fill_price, triggered, reduce_only, has_rested,
			version
		) VALUES
			(
				'019fa844-26c0-7000-8000-000000000021',
				'urn:xb:account:fill-filter', 'BTC-PERP', 'BUY', 'MARKET',
				'IOC', 'FILLED', 0.01, 0.01, 60000,
				false, false, false, 1
			),
			(
				'019fa844-26c0-7000-8000-000000000022',
				'urn:xb:account:fill-filter', 'BTC-PERP', 'SELL', 'MARKET',
				'IOC', 'FILLED', 0.01, 0.01, 61000,
				false, false, false, 1
			);
		INSERT INTO trading.fills (
			fill_id, order_id, input_id, account_id, instrument_id,
			side, price, quantity, position_id, position_effect,
			liquidity_side, logical_time
		) VALUES
			(
				'019fa844-26c0-7000-8000-000000000011',
				'019fa844-26c0-7000-8000-000000000021',
				'019fa844-26c0-7000-8000-000000000031',
				'urn:xb:account:fill-filter', 'BTC-PERP',
				'BUY', 60000, 0.01,
				'019fa844-26c0-7000-8000-000000000041',
				'open', 'TAKER', 1784901600000000001
			),
			(
				'019fa844-26c0-7000-8000-000000000012',
				'019fa844-26c0-7000-8000-000000000022',
				'019fa844-26c0-7000-8000-000000000032',
				'urn:xb:account:fill-filter', 'BTC-PERP',
				'SELL', 61000, 0.01,
				'019fa844-26c0-7000-8000-000000000042',
				'close', 'TAKER', 1784901600000000002
			)`); err != nil {
		t.Fatalf("seed durable fill filters: %v", err)
	}

	apiPool := runtimeRoleLoginPool(
		t,
		pool,
		"platformgo_fill_filter_api_login",
		"platformgo_api",
	)
	store := platformpostgres.NewCompatibilityStore(apiPool)
	buys, err := store.FilterFillExecutions(
		ctx,
		accountID,
		platformpostgres.FillExecutionFilter{Side: "buy", Limit: 10},
	)
	if err != nil {
		t.Fatalf("filter fills by side: %v", err)
	}
	if len(buys.Items) != 1 || buys.Items[0].FillID != buyFillID {
		t.Fatalf("buy fills = %#v, want only %s", buys.Items, buyFillID)
	}
	if buys.Total != 1 {
		t.Fatalf("buy filtered total = %d, want 1", buys.Total)
	}

	one, err := store.FilterFillExecutions(
		ctx,
		accountID,
		platformpostgres.FillExecutionFilter{TradeID: sellFillID, Limit: 10},
	)
	if err != nil {
		t.Fatalf("filter fills by trade ID: %v", err)
	}
	if len(one.Items) != 1 || one.Items[0].FillID != sellFillID {
		t.Fatalf("trade-ID fills = %#v, want only %s", one.Items, sellFillID)
	}
}

// Ported from:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/trading/e2e_fills.rs:252
//	test: fill_history_returns_side_and_trade_type
//
// Adaptations:
//   - Durable immutable fills replace legacy mirror rows.
//   - The PostgreSQL compatibility reader replaces the Rust query dispatcher.
//   - Current Go behavior remains authoritative: every engine-produced durable
//     fill has a required position effect, so the legacy unclassified fixture
//     is not imported as a nullable trade type.
//
// Assertions preserved:
//   - BUY/open and SELL/close sides retain their source spellings.
//   - Open, increase, reduce, flip, and close trade types project exactly.
//
// Strengthening:
//   - Unknown durable effects fail closed instead of becoming client values.
func TestFillHistoryReturnsSideAndTradeType(t *testing.T) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := platformpostgres.NewMigrator(
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).Migrate(ctx); err != nil {
		t.Fatalf("migrate fill side/trade-type database: %v", err)
	}

	const accountID = "urn:xb:account:fill-side-trade-type"
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
		INSERT INTO trading.accounts (account_id, oms_mode)
		VALUES ('urn:xb:account:fill-side-trade-type', 'NETTING');
		INSERT INTO trading.orders (
			order_id, account_id, instrument_id, side, order_type,
			time_in_force, status, quantity, filled_quantity,
			average_fill_price, triggered, reduce_only, has_rested,
			version
		) VALUES
			('019fa844-26c0-7000-8000-000000000081',
			 'urn:xb:account:fill-side-trade-type', 'BTC-PERP', 'BUY',
			 'MARKET', 'IOC', 'FILLED', 0.01, 0.01, 60000,
			 false, false, false, 1),
			('019fa844-26c0-7000-8000-000000000082',
			 'urn:xb:account:fill-side-trade-type', 'BTC-PERP', 'BUY',
			 'MARKET', 'IOC', 'FILLED', 0.01, 0.01, 60000,
			 false, false, false, 1),
			('019fa844-26c0-7000-8000-000000000083',
			 'urn:xb:account:fill-side-trade-type', 'BTC-PERP', 'SELL',
			 'MARKET', 'IOC', 'FILLED', 0.01, 0.01, 60000,
			 false, false, false, 1),
			('019fa844-26c0-7000-8000-000000000084',
			 'urn:xb:account:fill-side-trade-type', 'BTC-PERP', 'SELL',
			 'MARKET', 'IOC', 'FILLED', 0.01, 0.01, 60000,
			 false, false, false, 1),
			('019fa844-26c0-7000-8000-000000000085',
			 'urn:xb:account:fill-side-trade-type', 'BTC-PERP', 'SELL',
			 'MARKET', 'IOC', 'FILLED', 0.01, 0.01, 60000,
			 false, false, false, 1);
		INSERT INTO trading.fills (
			fill_id, order_id, input_id, account_id, instrument_id,
			side, price, quantity, position_id, position_effect,
			liquidity_side, logical_time
		) VALUES
			('019fa844-26c0-7000-8000-000000000071',
			 '019fa844-26c0-7000-8000-000000000081',
			 '019fa844-26c0-7000-8000-000000000091',
			 'urn:xb:account:fill-side-trade-type', 'BTC-PERP',
			 'BUY', 60000, 0.01,
			 '019fa844-26c0-7000-8000-0000000000a1',
			 'open', 'TAKER', 1784901600000000071),
			('019fa844-26c0-7000-8000-000000000072',
			 '019fa844-26c0-7000-8000-000000000082',
			 '019fa844-26c0-7000-8000-000000000092',
			 'urn:xb:account:fill-side-trade-type', 'BTC-PERP',
			 'BUY', 60000, 0.01,
			 '019fa844-26c0-7000-8000-0000000000a1',
			 'increase', 'TAKER', 1784901600000000072),
			('019fa844-26c0-7000-8000-000000000073',
			 '019fa844-26c0-7000-8000-000000000083',
			 '019fa844-26c0-7000-8000-000000000093',
			 'urn:xb:account:fill-side-trade-type', 'BTC-PERP',
			 'SELL', 60000, 0.01,
			 '019fa844-26c0-7000-8000-0000000000a1',
			 'reduce', 'TAKER', 1784901600000000073),
			('019fa844-26c0-7000-8000-000000000074',
			 '019fa844-26c0-7000-8000-000000000084',
			 '019fa844-26c0-7000-8000-000000000094',
			 'urn:xb:account:fill-side-trade-type', 'BTC-PERP',
			 'SELL', 60000, 0.01,
			 '019fa844-26c0-7000-8000-0000000000a1',
			 'flip', 'TAKER', 1784901600000000074),
			('019fa844-26c0-7000-8000-000000000075',
			 '019fa844-26c0-7000-8000-000000000085',
			 '019fa844-26c0-7000-8000-000000000095',
			 'urn:xb:account:fill-side-trade-type', 'BTC-PERP',
			 'SELL', 60000, 0.01,
			 '019fa844-26c0-7000-8000-0000000000a1',
			 'close', 'TAKER', 1784901600000000075)`); err != nil {
		t.Fatalf("seed durable fill side/trade types: %v", err)
	}

	apiPool := runtimeRoleLoginPool(
		t,
		pool,
		"platformgo_fill_side_trade_type_api_login",
		"platformgo_api",
	)
	store := platformpostgres.NewCompatibilityStore(apiPool)
	page, err := store.FilterFillExecutions(
		ctx,
		accountID,
		platformpostgres.FillExecutionFilter{Limit: 10},
	)
	if err != nil {
		t.Fatalf("read fill side/trade types: %v", err)
	}
	if len(page.Items) != 5 {
		t.Fatalf("fills = %#v, want five classified fills", page.Items)
	}
	want := map[string]struct {
		side      string
		tradeType string
	}{
		"019fa844-26c0-7000-8000-000000000071": {"BUY", "open"},
		"019fa844-26c0-7000-8000-000000000072": {"BUY", "increase"},
		"019fa844-26c0-7000-8000-000000000073": {"SELL", "reduce"},
		"019fa844-26c0-7000-8000-000000000074": {"SELL", "flip"},
		"019fa844-26c0-7000-8000-000000000075": {"SELL", "close"},
	}
	for _, fill := range page.Items {
		expected, ok := want[fill.FillID]
		if !ok {
			t.Fatalf("unexpected fill = %#v", fill)
		}
		if fill.Side != expected.side || fill.TradeType != expected.tradeType {
			t.Fatalf(
				"fill %s = (%q, %q), want (%q, %q)",
				fill.FillID,
				fill.Side,
				fill.TradeType,
				expected.side,
				expected.tradeType,
			)
		}
	}
	latest, err := store.LatestFillExecution(ctx, accountID)
	if err != nil {
		t.Fatalf("read latest classified fill: %v", err)
	}
	if latest.FillID != "019fa844-26c0-7000-8000-000000000075" ||
		latest.Side != "SELL" ||
		latest.TradeType != "close" {
		t.Fatalf("latest classified fill = %#v", latest)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO trading.accounts (account_id, oms_mode) VALUES
			('urn:xb:account:fill-effect-upper', 'NETTING'),
			('urn:xb:account:fill-effect-mixed', 'NETTING'),
			('urn:xb:account:fill-effect-whitespace', 'NETTING'),
			('urn:xb:account:fill-effect-unknown', 'NETTING');
		INSERT INTO trading.orders (
			order_id, account_id, instrument_id, side, order_type,
			time_in_force, status, quantity, filled_quantity,
			average_fill_price, triggered, reduce_only, has_rested,
			version
		) VALUES
			('019fa844-26c0-7000-8000-0000000000b1',
			 'urn:xb:account:fill-effect-upper', 'BTC-PERP', 'BUY',
			 'MARKET', 'IOC', 'FILLED', 0.01, 0.01, 60000,
			 false, false, false, 1),
			('019fa844-26c0-7000-8000-0000000000b2',
			 'urn:xb:account:fill-effect-mixed', 'BTC-PERP', 'BUY',
			 'MARKET', 'IOC', 'FILLED', 0.01, 0.01, 60000,
			 false, false, false, 1),
			('019fa844-26c0-7000-8000-0000000000b3',
			 'urn:xb:account:fill-effect-whitespace', 'BTC-PERP', 'BUY',
			 'MARKET', 'IOC', 'FILLED', 0.01, 0.01, 60000,
			 false, false, false, 1),
			('019fa844-26c0-7000-8000-0000000000b4',
			 'urn:xb:account:fill-effect-unknown', 'BTC-PERP', 'BUY',
			 'MARKET', 'IOC', 'FILLED', 0.01, 0.01, 60000,
			 false, false, false, 1);
		INSERT INTO trading.fills (
			fill_id, order_id, input_id, account_id, instrument_id,
			side, price, quantity, position_id, position_effect,
			liquidity_side, logical_time
		) VALUES
			('019fa844-26c0-7000-8000-0000000000c1',
			 '019fa844-26c0-7000-8000-0000000000b1',
			 '019fa844-26c0-7000-8000-0000000000d1',
			 'urn:xb:account:fill-effect-upper', 'BTC-PERP',
			 'BUY', 60000, 0.01,
			 '019fa844-26c0-7000-8000-0000000000e1',
			 'OPEN', 'TAKER', 1784901600000000081),
			('019fa844-26c0-7000-8000-0000000000c2',
			 '019fa844-26c0-7000-8000-0000000000b2',
			 '019fa844-26c0-7000-8000-0000000000d2',
			 'urn:xb:account:fill-effect-mixed', 'BTC-PERP',
			 'BUY', 60000, 0.01,
			 '019fa844-26c0-7000-8000-0000000000e2',
			 'Open', 'TAKER', 1784901600000000082),
			('019fa844-26c0-7000-8000-0000000000c3',
			 '019fa844-26c0-7000-8000-0000000000b3',
			 '019fa844-26c0-7000-8000-0000000000d3',
			 'urn:xb:account:fill-effect-whitespace', 'BTC-PERP',
			 'BUY', 60000, 0.01,
			 '019fa844-26c0-7000-8000-0000000000e3',
			 ' open ', 'TAKER', 1784901600000000083),
			('019fa844-26c0-7000-8000-0000000000c4',
			 '019fa844-26c0-7000-8000-0000000000b4',
			 '019fa844-26c0-7000-8000-0000000000d4',
			 'urn:xb:account:fill-effect-unknown', 'BTC-PERP',
			 'BUY', 60000, 0.01,
			 '019fa844-26c0-7000-8000-0000000000e4',
			 'unknown', 'TAKER', 1784901600000000084)`); err != nil {
		t.Fatalf("seed noncanonical durable fill trade types: %v", err)
	}

	restartedPool := runtimeRoleLoginPool(
		t,
		pool,
		"platformgo_fill_side_trade_type_restart_api_login",
		"platformgo_api",
	)
	restartedStore := platformpostgres.NewCompatibilityStore(restartedPool)
	invalidEffects := []struct {
		label     string
		accountID string
		raw       string
	}{
		{
			"uppercase",
			"urn:xb:account:fill-effect-upper",
			"OPEN",
		},
		{
			"mixed-case",
			"urn:xb:account:fill-effect-mixed",
			"Open",
		},
		{
			"whitespace",
			"urn:xb:account:fill-effect-whitespace",
			" open ",
		},
		{
			"unknown",
			"urn:xb:account:fill-effect-unknown",
			"unknown",
		},
	}
	stores := []struct {
		label string
		store *platformpostgres.CompatibilityStore
	}{
		{"current", store},
		{"restarted", restartedStore},
	}
	for _, invalidEffect := range invalidEffects {
		for _, candidateStore := range stores {
			invalidLatest, err := candidateStore.store.LatestFillExecution(
				ctx,
				invalidEffect.accountID,
			)
			if err == nil || invalidLatest.FillID != "" {
				t.Fatalf(
					"%s %s latest = %#v, err=%v; want fail-closed zero view",
					candidateStore.label,
					invalidEffect.label,
					invalidLatest,
					err,
				)
			}
			invalidPage, err := candidateStore.store.FilterFillExecutions(
				ctx,
				invalidEffect.accountID,
				platformpostgres.FillExecutionFilter{Limit: 10},
			)
			if err == nil ||
				len(invalidPage.Items) != 0 ||
				invalidPage.Total != 0 {
				t.Fatalf(
					"%s %s page = %#v, err=%v; want fail-closed zero page",
					candidateStore.label,
					invalidEffect.label,
					invalidPage,
					err,
				)
			}
		}
		var rawPositionEffect string
		if err := pool.QueryRow(ctx, `
			SELECT position_effect
			  FROM trading.fills
			 WHERE account_id = $1`,
			invalidEffect.accountID,
		).Scan(&rawPositionEffect); err != nil {
			t.Fatalf(
				"read %s raw durable effect: %v",
				invalidEffect.label,
				err,
			)
		}
		if rawPositionEffect != invalidEffect.raw {
			t.Fatalf(
				"%s raw durable effect = %q, want immutable %q",
				invalidEffect.label,
				rawPositionEffect,
				invalidEffect.raw,
			)
		}
	}
}

// Ported from:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/trading/e2e_fills.rs:410
//	test: fill_order_id_is_the_correlatable_order_urn
//
// Adaptations:
//   - The immutable durable fill replaces the legacy mirror row.
//   - The PostgreSQL compatibility reader replaces the Rust query dispatcher.
//   - The accepted Go order surface retains the UUID body inside the stable
//     urn:xb:order: namespace.
//
// Assertions preserved:
//   - Fill orderId is the same typed order URN exposed by the order surface.
func TestFillOrderIDIsTheCorrelatableOrderURN(t *testing.T) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := platformpostgres.NewMigrator(
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).Migrate(ctx); err != nil {
		t.Fatalf("migrate fill order-correlation database: %v", err)
	}

	const (
		accountID = "urn:xb:account:fill-order-correlation"
		fillID    = "019fa844-26c0-7000-8000-000000000062"
	)
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
		INSERT INTO trading.accounts (account_id, oms_mode)
		VALUES ('urn:xb:account:fill-order-correlation', 'NETTING');
		INSERT INTO trading.orders (
			order_id, account_id, instrument_id, side, order_type,
			time_in_force, status, quantity, filled_quantity,
			average_fill_price, triggered, reduce_only, has_rested,
			version
		) VALUES (
			'019fa844-26c0-7000-8000-000000000061',
			'urn:xb:account:fill-order-correlation',
			'BTC-PERP', 'BUY', 'MARKET', 'IOC', 'FILLED',
			0.01, 0.01, 60000, false, false, false, 1
		);
		INSERT INTO trading.fills (
			fill_id, order_id, input_id, account_id, instrument_id,
			side, price, quantity, position_id, position_effect,
			liquidity_side, logical_time
		) VALUES (
			'019fa844-26c0-7000-8000-000000000062',
			'019fa844-26c0-7000-8000-000000000061',
			'019fa844-26c0-7000-8000-000000000063',
			'urn:xb:account:fill-order-correlation',
			'BTC-PERP', 'BUY', 60000, 0.01,
			'019fa844-26c0-7000-8000-000000000064',
			'open', 'TAKER', 1784901600000000062
		)`); err != nil {
		t.Fatalf("seed correlatable fill order: %v", err)
	}

	apiPool := runtimeRoleLoginPool(
		t,
		pool,
		"platformgo_fill_order_correlation_api_login",
		"platformgo_api",
	)
	store := platformpostgres.NewCompatibilityStore(apiPool)
	page, err := store.FilterFillExecutions(
		ctx,
		accountID,
		platformpostgres.FillExecutionFilter{
			TradeID: fillID,
			Limit:   10,
		},
	)
	if err != nil {
		t.Fatalf("read correlatable fill order: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("fill page = %#v, want one item", page)
	}
	orders, err := store.Orders(ctx, accountID)
	if err != nil {
		t.Fatalf("read correlatable order surface: %v", err)
	}
	if len(orders) != 1 {
		t.Fatalf("orders = %#v, want one item", orders)
	}
	wantOrderID := orders[0].OrderID
	if page.Items[0].OrderID != wantOrderID {
		t.Fatalf(
			"fill orderId = %q, want correlatable %q",
			page.Items[0].OrderID,
			wantOrderID,
		)
	}
}

func TestFillHistoryQueriesUseKeysetIndex(t *testing.T) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := platformpostgres.NewMigrator(
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).Migrate(ctx); err != nil {
		t.Fatalf("migrate fill query-plan database: %v", err)
	}
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
		VALUES ('account-fill-plan', 'NETTING');
		INSERT INTO trading.accounts (account_id, oms_mode)
		SELECT format('account-fill-plan-other-%s', account_number), 'NETTING'
		  FROM generate_series(1, 9) AS accounts(account_number);
		INSERT INTO trading.orders (
			order_id, account_id, instrument_id, side, order_type,
			time_in_force, status, quantity, filled_quantity,
			average_fill_price, triggered, reduce_only, has_rested,
			version
		) VALUES (
			'019fa844-26c0-7000-8000-000000000001',
			'account-fill-plan', 'BTC-PERP', 'BUY', 'MARKET',
			'IOC', 'FILLED', 100, 100, 100,
			false, false, false, 1
		);
		INSERT INTO trading.fills (
			fill_id, order_id, input_id, account_id, instrument_id,
			side, price, quantity, position_id, position_effect,
			liquidity_side, logical_time
		)
		SELECT
			format(
				'10000000-0000-0000-0000-%s',
				lpad(to_hex(sequence_number), 12, '0')
			)::uuid,
			'019fa844-26c0-7000-8000-000000000001'::uuid,
			format(
				'20000000-0000-0000-0000-%s',
				lpad(to_hex(sequence_number), 12, '0')
			)::uuid,
			CASE
				WHEN sequence_number <= 10000 THEN 'account-fill-plan'
				ELSE format(
					'account-fill-plan-other-%s',
					1 + ((sequence_number - 10001) / 10000)
				)
			END,
			'BTC-PERP',
			CASE
				WHEN sequence_number % 2 = 0 THEN 'BUY'
				ELSE 'SELL'
			END,
			100,
			0.01,
			'30000000-0000-0000-0000-000000000001'::uuid,
			'open',
			'TAKER',
			1784901600000000000 + sequence_number
		  FROM generate_series(1, 100000) AS sequence(sequence_number);
		ANALYZE trading.fills`); err != nil {
		t.Fatalf("seed representative fill history: %v", err)
	}

	assertFillPlanUsesIndex(
		t,
		pool,
		"fills_account_history_idx",
		`SELECT fill_id
		   FROM trading.fills
		  WHERE account_id = 'account-fill-plan'
		    AND (logical_time, fill_id) <
		        (1784901600000010001, 'ffffffff-ffff-ffff-ffff-ffffffffffff')
		  ORDER BY logical_time DESC, fill_id DESC
		  LIMIT 51`,
	)
	assertFillPlanUsesIndex(
		t,
		pool,
		"fills_account_history_idx",
		`WITH page AS (
			SELECT
				fill.fill_id,
				fill.order_id,
				fill.position_id,
				fill.side,
				fill.position_effect,
				fill.realized_pnl,
				fill.settlement_currency,
				fill.logical_time
			  FROM trading.fills AS fill
			 WHERE fill.account_id = $1
			   AND ($2::text IS NULL OR fill.side = $2)
			   AND ($3::uuid IS NULL OR fill.fill_id = $3)
			   AND (fill.logical_time, fill.fill_id) < ($4, $5)
			 ORDER BY fill.logical_time DESC, fill.fill_id DESC
			 LIMIT $6
		),
		filtered_total AS (
			SELECT count(*) AS total
			  FROM trading.fills AS counted
			 WHERE counted.account_id = $1
			   AND ($2::text IS NULL OR counted.side = $2)
			   AND ($3::uuid IS NULL OR counted.fill_id = $3)
		)
		SELECT
			page.fill_id::text,
			page.order_id::text,
			page.position_id::text,
			page.side,
			page.position_effect,
			trim_scale(page.realized_pnl)::text,
			page.settlement_currency,
			page.logical_time,
			filtered_total.total
		  FROM filtered_total
		  LEFT JOIN page ON true
		 ORDER BY page.logical_time DESC NULLS LAST,
		          page.fill_id DESC NULLS LAST`,
		"account-fill-plan",
		nil,
		nil,
		int64(1784901600000010001),
		"ffffffff-ffff-ffff-ffff-ffffffffffff",
		51,
	)
	assertFillPlanUsesIndex(
		t,
		pool,
		"fills_account_history_idx",
		`WITH page AS (
			SELECT
				fill.fill_id,
				fill.order_id,
				fill.position_id,
				fill.side,
				fill.position_effect,
				fill.realized_pnl,
				fill.settlement_currency,
				fill.logical_time
			  FROM trading.fills AS fill
			 WHERE fill.account_id = $1
			   AND ($2::text IS NULL OR fill.side = $2)
			   AND ($3::uuid IS NULL OR fill.fill_id = $3)
			   AND (fill.logical_time, fill.fill_id) > ($4, $5)
			 ORDER BY fill.logical_time ASC, fill.fill_id ASC
			 LIMIT $6
		),
		filtered_total AS (
			SELECT count(*) AS total
			  FROM trading.fills AS counted
			 WHERE counted.account_id = $1
			   AND ($2::text IS NULL OR counted.side = $2)
			   AND ($3::uuid IS NULL OR counted.fill_id = $3)
		)
		SELECT
			page.fill_id::text,
			page.order_id::text,
			page.position_id::text,
			page.side,
			page.position_effect,
			trim_scale(page.realized_pnl)::text,
			page.settlement_currency,
			page.logical_time,
			filtered_total.total
		  FROM filtered_total
		  LEFT JOIN page ON true
		 ORDER BY page.logical_time ASC NULLS LAST,
		          page.fill_id ASC NULLS LAST`,
		"account-fill-plan",
		nil,
		nil,
		int64(1784901600000009950),
		"00000000-0000-0000-0000-000000000000",
		51,
	)
	assertFillPlanUsesIndex(
		t,
		pool,
		"fills_account_history_idx",
		`SELECT fill_id
		   FROM trading.fills
		  WHERE account_id = 'account-fill-plan'
		    AND (logical_time, fill_id) >
		        (1784901600000009950, '00000000-0000-0000-0000-000000000000')
		  ORDER BY logical_time ASC, fill_id ASC
		  LIMIT 51`,
	)
	assertFillPlanUsesIndex(
		t,
		pool,
		"fills_account_side_history_idx",
		`WITH page AS (
			SELECT
				fill.fill_id,
				fill.order_id,
				fill.position_id,
				fill.side,
				fill.position_effect,
				fill.realized_pnl,
				fill.settlement_currency,
				fill.logical_time
			  FROM trading.fills AS fill
			 WHERE fill.account_id = $1
			   AND ($2::text IS NULL OR fill.side = $2)
			   AND ($3::uuid IS NULL OR fill.fill_id = $3)
			 ORDER BY fill.logical_time DESC, fill.fill_id DESC
			 LIMIT $4
		),
		filtered_total AS (
			SELECT count(*) AS total
			  FROM trading.fills AS counted
			 WHERE counted.account_id = $1
			   AND ($2::text IS NULL OR counted.side = $2)
			   AND ($3::uuid IS NULL OR counted.fill_id = $3)
		)
		SELECT
			page.fill_id::text,
			page.order_id::text,
			page.position_id::text,
			page.side,
			page.position_effect,
			trim_scale(page.realized_pnl)::text,
			page.settlement_currency,
			page.logical_time,
			filtered_total.total
		  FROM filtered_total
		  LEFT JOIN page ON true
		 ORDER BY page.logical_time DESC NULLS LAST,
		          page.fill_id DESC NULLS LAST`,
		"account-fill-plan",
		"BUY",
		nil,
		10,
	)
}

func assertFillPlanUsesIndex(
	t *testing.T,
	pool *pgxpool.Pool,
	indexName string,
	query string,
	args ...any,
) {
	t.Helper()
	var rawPlan []byte
	if err := pool.QueryRow(
		context.Background(),
		"EXPLAIN (FORMAT JSON, COSTS OFF) "+query,
		args...,
	).Scan(&rawPlan); err != nil {
		t.Fatalf("explain fill query: %v", err)
	}
	var explained []struct {
		Plan postgresExplainPlan `json:"Plan"`
	}
	if err := json.Unmarshal(rawPlan, &explained); err != nil {
		t.Fatalf("decode fill plan: %v", err)
	}
	if len(explained) != 1 {
		t.Fatalf("fill plans = %d, want 1", len(explained))
	}
	var (
		indexFound  bool
		fillSeqScan bool
	)
	walkPostgresPlan(explained[0].Plan, func(plan postgresExplainPlan) {
		indexFound = indexFound || plan.IndexName == indexName
		fillSeqScan = fillSeqScan ||
			(plan.NodeType == "Seq Scan" && plan.RelationName == "fills")
	})
	if !indexFound || fillSeqScan {
		t.Fatalf(
			"fill plan required index found=%t fill-seq=%t: %s",
			indexFound,
			fillSeqScan,
			rawPlan,
		)
	}
}
