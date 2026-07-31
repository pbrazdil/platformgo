package postgres_test

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
	"github.com/upcomers-org/platformgo/internal/edge"
)

func TestBrokerFillsUnauthorizedAuthorityHistoryMatrix(t *testing.T) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := newCurrentTestMigrator(
		t,
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).Migrate(ctx); err != nil {
		t.Fatalf("migrate broker fills authority-matrix database: %v", err)
	}

	const (
		requestingTenant = "urn:xb:tenant:broker-fills-requesting"
		foreignTenantA   = "urn:xb:tenant:broker-fills-foreign-a"
		foreignTenantB   = "urn:xb:tenant:broker-fills-foreign-b"
		requestingUser   = "urn:xb:user:broker-fills-requesting"
		foreignUserA     = "urn:xb:user:broker-fills-foreign-a"
		foreignUserB     = "urn:xb:user:broker-fills-foreign-b"
		principalSubject = "urn:xb:apikey:broker-fills-authority-matrix"
		requestID        = "broker-fills-authority-matrix"
	)
	if principalSubject == requestingTenant {
		t.Fatal("broker API-key subject must not be used as tenant authority")
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
		)`,
	); err != nil {
		t.Fatalf("seed broker fills authority-matrix instrument: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO identity.users (
			user_id, login, normalized_login, broker_subject
		) VALUES
			($1, 'broker-fills-requesting', 'broker-fills-requesting', $2),
			($3, 'broker-fills-foreign-a', 'broker-fills-foreign-a', $4),
			($5, 'broker-fills-foreign-b', 'broker-fills-foreign-b', $6)`,
		requestingUser,
		requestingTenant,
		foreignUserA,
		foreignTenantA,
		foreignUserB,
		foreignTenantB,
	); err != nil {
		t.Fatalf("seed broker fills authority-matrix users: %v", err)
	}

	authorityShapes := []struct {
		name            string
		ownershipUser   string
		ownershipTenant string
		profileTenant   string
	}{
		{name: "no identity authority"},
		{
			name:            "ownership match profile absent",
			ownershipUser:   requestingUser,
			ownershipTenant: requestingTenant,
		},
		{
			name:            "ownership foreign profile match",
			ownershipUser:   foreignUserA,
			ownershipTenant: foreignTenantA,
			profileTenant:   requestingTenant,
		},
		{
			name:            "ownership match profile foreign",
			ownershipUser:   requestingUser,
			ownershipTenant: requestingTenant,
			profileTenant:   foreignTenantA,
		},
		{
			name:            "both foreign",
			ownershipUser:   foreignUserA,
			ownershipTenant: foreignTenantA,
			profileTenant:   foreignTenantA,
		},
		{
			name:            "conflicting tenant rows",
			ownershipUser:   foreignUserA,
			ownershipTenant: foreignTenantA,
			profileTenant:   foreignTenantB,
		},
	}
	histories := []struct {
		name          string
		seedFill      bool
		corruptReason bool
	}{
		{name: "empty"},
		{name: "valid", seedFill: true},
		{name: "reason corrupt", seedFill: true, corruptReason: true},
	}
	if got := len(authorityShapes) * len(histories); got != 18 {
		t.Fatalf("broker fills authority/history matrix has %d cases, want 18", got)
	}

	caseNumber := 0
	for _, authority := range authorityShapes {
		for _, history := range histories {
			caseNumber++
			name := authority.name + "/" + history.name
			t.Run(name, func(t *testing.T) {
				accountID := fmt.Sprintf(
					"urn:xb:account:00000000-0000-4000-8000-%012d",
					caseNumber,
				)
				orderID := fmt.Sprintf(
					"00000000-0000-4000-8001-%012d",
					caseNumber,
				)
				fillID := fmt.Sprintf(
					"00000000-0000-4000-8002-%012d",
					caseNumber,
				)
				inputID := fmt.Sprintf(
					"00000000-0000-4000-8003-%012d",
					caseNumber,
				)
				positionID := fmt.Sprintf(
					"00000000-0000-4000-8004-%012d",
					caseNumber,
				)
				seedBrokerFillsAuthorityMatrixCase(
					t,
					ctx,
					pool,
					caseNumber,
					accountID,
					orderID,
					fillID,
					inputID,
					positionID,
					authority.ownershipUser,
					authority.ownershipTenant,
					authority.profileTenant,
					history.seedFill,
					history.corruptReason,
				)
				before := brokerFillsDurableProjectionSnapshot(t, ctx, pool)

				baseAPIPool := runtimeRoleLoginPool(
					t,
					pool,
					fmt.Sprintf("platformgo_broker_fill_matrix_%02d", caseNumber),
					"platformgo_api",
				)
				queryCounter := &brokerFillsQueryCounter{}
				tracedConfig := baseAPIPool.Config().Copy()
				tracedConfig.ConnConfig.Tracer = queryCounter
				apiPool, err := pgxpool.NewWithConfig(ctx, tracedConfig)
				if err != nil {
					t.Fatalf("open traced least-privilege API pool: %v", err)
				}
				t.Cleanup(apiPool.Close)
				store := platformpostgres.NewCompatibilityStore(apiPool)
				authenticator, err := edge.NewHMACAuthenticator(
					edge.HMACAuthenticatorConfig{
						ClientTokenSecret: []byte(
							"broker-fills-matrix-client-secret-0123456789",
						),
						BrokerCredentials: []edge.BrokerCredential{{
							Prefix:     "xbk_fillmatrix",
							SecretHash: edge.HashBrokerSecret("secret"),
							Subject:    principalSubject,
							Tenant:     requestingTenant,
							Scopes:     []string{"accounts:read"},
						}},
					},
				)
				if err != nil {
					t.Fatalf("create fresh broker fills authenticator: %v", err)
				}
				server := edge.NewServer(edge.ServerConfig{
					Authenticator: authenticator,
					BrokerFills:   store,
					RequestID:     func() string { return requestID },
				}).Handler()

				request := httptest.NewRequest(
					http.MethodGet,
					"/broker/v1/accounts/"+accountID+"/fills?limit=200",
					nil,
				)
				request.Header.Set("x-api-key", "xbk_fillmatrix.secret")
				response := httptest.NewRecorder()
				server.ServeHTTP(response, request)

				wantBody := []byte(
					`{"code":"forbidden","message":"forbidden","requestId":"` +
						requestID + `"}` + "\n",
				)
				if response.Code != http.StatusForbidden ||
					!bytes.Equal(response.Body.Bytes(), wantBody) {
					t.Fatalf(
						"status/body = %d/%q, want byte-identical 403/%q",
						response.Code,
						response.Body.Bytes(),
						wantBody,
					)
				}
				if got := queryCounter.count.Load(); got != 1 {
					t.Fatalf(
						"PostgreSQL statements = %d, want exactly one authority/page statement",
						got,
					)
				}
				for _, leaked := range []string{
					accountID,
					orderID,
					fillID,
					positionID,
					`"items"`,
					`"total"`,
					`"nextCursor"`,
					`"prevCursor"`,
					`"fillId"`,
					`"orderId"`,
					`"positionId"`,
					`"reason"`,
					`"realizedPnl"`,
					`"settlementCurrency"`,
					"12.3",
					"manual",
					"authority_matrix_corrupt",
				} {
					if strings.Contains(response.Body.String(), leaked) {
						t.Fatalf(
							"generic forbidden response leaked %q: %q",
							leaked,
							response.Body.String(),
						)
					}
				}

				after := brokerFillsDurableProjectionSnapshot(t, ctx, pool)
				if after != before {
					t.Fatalf(
						"denied broker fills read mutated durable fill/projection rows:\nbefore=%s\nafter=%s",
						before,
						after,
					)
				}
			})
		}
	}
}

func seedBrokerFillsAuthorityMatrixCase(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	caseNumber int,
	accountID string,
	orderID string,
	fillID string,
	inputID string,
	positionID string,
	ownershipUser string,
	ownershipTenant string,
	profileTenant string,
	seedFill bool,
	corruptReason bool,
) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
		INSERT INTO trading.accounts (account_id, oms_mode)
		VALUES ($1, 'NETTING')`,
		accountID,
	); err != nil {
		t.Fatalf("seed authority-matrix trading account: %v", err)
	}
	if ownershipTenant != "" {
		if _, err := pool.Exec(ctx, `
			INSERT INTO identity.user_accounts (
				user_id, account_id, broker_subject
			) VALUES ($1, $2, $3)`,
			ownershipUser,
			accountID,
			ownershipTenant,
		); err != nil {
			t.Fatalf("seed authority-matrix ownership: %v", err)
		}
	}
	if profileTenant != "" {
		if _, err := pool.Exec(ctx, `
			INSERT INTO identity.account_profiles (
				account_id, login, base_currency, market_venue,
				permitted_classes, broker_subject, created_at
			) VALUES (
				$1, $2, 'USDC', 'HYPERLIQUID',
				ARRAY['CRYPTOCURRENCY'], $3, '2026-07-30T00:00:00Z'
			)`,
			accountID,
			10000+caseNumber,
			profileTenant,
		); err != nil {
			t.Fatalf("seed authority-matrix profile: %v", err)
		}
	}
	if !seedFill {
		return
	}
	var bracketLeg any
	if corruptReason {
		bracketLeg = "authority_matrix_corrupt"
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO trading.orders (
			order_id, account_id, instrument_id, side, order_type,
			time_in_force, status, quantity, filled_quantity,
			average_fill_price, triggered, reduce_only, bracket_leg,
			has_rested, version
		) VALUES (
			$1, $2, 'BTC-PERP', 'SELL', 'MARKET', 'IOC', 'FILLED',
			1, 1, 100, false, true, $3, false, 1
		)`,
		orderID,
		accountID,
		bracketLeg,
	); err != nil {
		t.Fatalf("seed authority-matrix order history: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO trading.fills (
			fill_id, order_id, input_id, account_id, instrument_id,
			side, price, quantity, position_id, position_effect,
			realized_pnl, settlement_currency, liquidity_side,
			fee, fee_currency, logical_time, effective_leverage
		) VALUES (
			$1, $2, $3, $4, 'BTC-PERP', 'SELL', 100, 1, $5, 'close',
			12.30, 'USDC', 'TAKER', 0, 'USDC', $6, 5
		)`,
		fillID,
		orderID,
		inputID,
		accountID,
		positionID,
		1785427200000000000+caseNumber,
	); err != nil {
		t.Fatalf("seed authority-matrix fill history: %v", err)
	}
}

func brokerFillsDurableProjectionSnapshot(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
) string {
	t.Helper()
	var snapshot string
	if err := pool.QueryRow(ctx, `
		SELECT jsonb_build_object(
			'accounts', (
				SELECT COALESCE(
					jsonb_agg(to_jsonb(account) ORDER BY account.account_id),
					'[]'::jsonb
				)
				  FROM trading.accounts AS account
			),
			'orders', (
				SELECT COALESCE(
					jsonb_agg(to_jsonb(orders) ORDER BY orders.order_id),
					'[]'::jsonb
				)
				  FROM trading.orders
			),
			'fills', (
				SELECT COALESCE(
					jsonb_agg(to_jsonb(fills) ORDER BY fills.fill_id),
					'[]'::jsonb
				)
				  FROM trading.fills
			),
			'positions', (
				SELECT COALESCE(
					jsonb_agg(to_jsonb(positions) ORDER BY positions.position_id),
					'[]'::jsonb
				)
				  FROM trading.positions
			),
			'fundingSettlements', (
				SELECT COALESCE(
					jsonb_agg(
						to_jsonb(funding)
						ORDER BY funding.funding_id
					),
					'[]'::jsonb
				)
				  FROM trading.funding_settlements AS funding
			),
			'ledgerTransactions', (
				SELECT COALESCE(
					jsonb_agg(
						to_jsonb(transactions)
						ORDER BY transactions.transaction_id
					),
					'[]'::jsonb
				)
				  FROM ledger.transactions
			),
			'ledgerEntries', (
				SELECT COALESCE(
					jsonb_agg(to_jsonb(entries) ORDER BY entries.entry_id),
					'[]'::jsonb
				)
				  FROM ledger.entries
			),
			'balances', (
				SELECT COALESCE(
					jsonb_agg(
						to_jsonb(balances)
						ORDER BY balances.account_id, balances.currency
					),
					'[]'::jsonb
				)
				  FROM ledger.balances
			),
			'realtimeSequences', (
				SELECT COALESCE(
					jsonb_agg(
						to_jsonb(sequences)
						ORDER BY sequences.channel
					),
					'[]'::jsonb
				)
				  FROM realtime.channel_sequences AS sequences
			),
			'realtimePublications', (
				SELECT COALESCE(
					jsonb_agg(
						to_jsonb(publications)
						ORDER BY publications.channel, publications.sequence
					),
					'[]'::jsonb
				)
				  FROM realtime.publications
			)
		)::text`).Scan(&snapshot); err != nil {
		t.Fatalf("snapshot broker fills durable projections: %v", err)
	}
	return snapshot
}
