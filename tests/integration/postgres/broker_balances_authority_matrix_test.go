package postgres_test

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestBrokerBalancesUnauthorizedAuthorityProjectionMatrix(t *testing.T) {
	ctx := context.Background()
	pool := postgresPool(t)
	assertBrokerBalancesACLPostgres19Beta2(t, pool)
	resetDurableSchemas(t, pool)
	migrateBrokerBalancesCurrent(t, ctx, pool)

	const (
		requestingTenant = brokerBalancesTenant
		foreignTenantA   = "urn:xb:tenant:broker-balances-foreign-a"
		foreignTenantB   = "urn:xb:tenant:broker-balances-foreign-b"
		requestingUser   = "urn:xb:user:broker-balances-requesting"
		foreignUserA     = "urn:xb:user:broker-balances-foreign-a"
		foreignUserB     = "urn:xb:user:broker-balances-foreign-b"
		requestID        = "broker-balances-authority-matrix"
	)
	if _, err := pool.Exec(ctx, `
		INSERT INTO identity.users (
			user_id, login, normalized_login, broker_subject
		) VALUES
			($1, 'broker-balances-requesting', 'broker-balances-requesting', $2),
			($3, 'broker-balances-foreign-a', 'broker-balances-foreign-a', $4),
			($5, 'broker-balances-foreign-b', 'broker-balances-foreign-b', $6)`,
		requestingUser,
		requestingTenant,
		foreignUserA,
		foreignTenantA,
		foreignUserB,
		foreignTenantB,
	); err != nil {
		t.Fatalf("seed broker-balances authority-matrix principals: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO trading.currency_scales (currency, scale)
		VALUES ('USDC', 2)`); err != nil {
		t.Fatalf("seed broker-balances authority-matrix scale: %v", err)
	}

	authorityShapes := []struct {
		name            string
		ownershipUser   string
		ownershipTenant string
		profileTenant   string
	}{
		{name: "unknown account"},
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
	projections := []struct {
		name     string
		currency string
	}{
		{name: "empty"},
		{name: "valid", currency: "USDC"},
		{name: "corrupt", currency: "bad!"},
	}
	if got := len(authorityShapes) * len(projections); got != 18 {
		t.Fatalf("broker balances authority/projection matrix has %d cases, want 18", got)
	}

	caseNumber := 0
	for _, authority := range authorityShapes {
		for _, projection := range projections {
			caseNumber++
			t.Run(authority.name+"/"+projection.name, func(t *testing.T) {
				accountID := fmt.Sprintf(
					"urn:xb:account:00000000-0000-4000-8000-%012d",
					800+caseNumber,
				)
				seedBrokerBalancesAuthorityMatrixCase(
					t,
					ctx,
					pool,
					caseNumber,
					accountID,
					authority.ownershipUser,
					authority.ownershipTenant,
					authority.profileTenant,
					projection.currency,
				)
				before := brokerBalancesDurableSnapshot(t, ctx, pool)
				harness := newBrokerBalancesHarness(
					t,
					pool,
					fmt.Sprintf("platformgo_balance_matrix_%02d", caseNumber),
					requestID,
				)
				response := harness.get(
					t,
					"/broker/v1/accounts/"+accountID+"/balances",
				)
				want := []byte(
					`{"code":"forbidden","message":"forbidden","requestId":"` +
						requestID + `"}` + "\n",
				)
				if response.Code != http.StatusForbidden ||
					!bytes.Equal(response.Body.Bytes(), want) {
					t.Fatalf(
						"status/body = %d/%q, want byte-identical 403/%q",
						response.Code,
						response.Body.Bytes(),
						want,
					)
				}
				for _, leaked := range []string{
					accountID,
					`"currency"`,
					`"total"`,
					`"locked"`,
					`"free"`,
					`"equity"`,
					"USDC",
					"bad!",
					"123.45",
				} {
					if strings.Contains(response.Body.String(), leaked) {
						t.Fatalf(
							"generic forbidden response leaked %q: %q",
							leaked,
							response.Body.String(),
						)
					}
				}
				after := brokerBalancesDurableSnapshot(t, ctx, pool)
				if after != before {
					t.Fatalf(
						"denied broker balance read mutated durable state:\nbefore=%s\nafter=%s",
						before,
						after,
					)
				}
			})
		}
	}
}

func seedBrokerBalancesAuthorityMatrixCase(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	caseNumber int,
	accountID string,
	ownershipUser string,
	ownershipTenant string,
	profileTenant string,
	currency string,
) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
		INSERT INTO trading.accounts (account_id, oms_mode)
		VALUES ($1, 'NETTING')`,
		accountID,
	); err != nil {
		t.Fatalf("seed broker balance matrix account: %v", err)
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
			t.Fatalf("seed broker balance matrix ownership: %v", err)
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
			9800+caseNumber,
			profileTenant,
		); err != nil {
			t.Fatalf("seed broker balance matrix profile: %v", err)
		}
	}
	if currency == "" {
		return
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO ledger.balances (
			account_id, currency, total, used, free, equity, ledger_sequence,
			updated_at
		) VALUES (
			$1, $2, 123.45, 0, 123.45, 123.45, $3,
			'2026-07-30T00:00:00Z'
		)`,
		accountID,
		currency,
		caseNumber,
	); err != nil {
		t.Fatalf("seed broker balance matrix projection: %v", err)
	}
	seedBrokerBalancesLedgerFold(
		t,
		ctx,
		pool,
		accountID,
		currency,
		"123.45",
		900+caseNumber,
	)
	assertBrokerBalancesLedgerFold(t, ctx, pool, accountID)
}
