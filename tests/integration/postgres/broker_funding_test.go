package postgres_test

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
	"github.com/upcomers-org/platformgo/internal/edge"
	"github.com/upcomers-org/platformgo/internal/engine"
)

const (
	brokerFundingAccount = "urn:xb:account:funding-one"
	brokerFundingTenant  = "urn:xb:tenant:funding-proof"
)

type brokerFundingProvenanceKind string

const (
	brokerFundingProvenanceValid    brokerFundingProvenanceKind = "valid"
	brokerFundingProvenanceMissing  brokerFundingProvenanceKind = "missing"
	brokerFundingProvenanceMismatch brokerFundingProvenanceKind = "mismatch"
)

type brokerFundingQuery struct {
	sql  string
	args []any
}

type brokerFundingQueryTrace struct {
	count   atomic.Int64
	mu      sync.Mutex
	queries []brokerFundingQuery
}

func (trace *brokerFundingQueryTrace) TraceQueryStart(
	ctx context.Context,
	_ *pgx.Conn,
	data pgx.TraceQueryStartData,
) context.Context {
	trace.count.Add(1)
	trace.mu.Lock()
	trace.queries = append(trace.queries, brokerFundingQuery{
		sql:  data.SQL,
		args: append([]any(nil), data.Args...),
	})
	trace.mu.Unlock()
	return ctx
}

func (*brokerFundingQueryTrace) TraceQueryEnd(
	context.Context,
	*pgx.Conn,
	pgx.TraceQueryEndData,
) {
}

func (trace *brokerFundingQueryTrace) lastBrokerRead(
	t *testing.T,
) brokerFundingQuery {
	t.Helper()
	trace.mu.Lock()
	defer trace.mu.Unlock()
	for index := len(trace.queries) - 1; index >= 0; index-- {
		if strings.Contains(
			trace.queries[index].sql,
			"WITH authority AS MATERIALIZED",
		) {
			return trace.queries[index]
		}
	}
	t.Fatal("broker funding did not execute its authority/page statement")
	return brokerFundingQuery{}
}

func TestBrokerFundingReadsOneExactRestartSafePage(t *testing.T) {
	ctx := context.Background()
	admin := postgresPool(t)
	requireBrokerFundingPostgres19Beta2(t, admin)
	resetDurableSchemas(t, admin)
	migrateBrokerFundingTestSchema(t, ctx, admin)
	baseTime := time.Date(2026, 7, 26, 1, 0, 0, 0, time.UTC)
	seedFundingHistory(t, admin, baseTime)

	api, trace := newTracedBrokerFundingAPIPool(
		t,
		admin,
		"platformgo_broker_funding_api_login",
	)
	store := platformpostgres.NewCompatibilityStore(api)
	before := trace.count.Load()
	first, err := store.BrokerFunding(
		ctx,
		brokerFundingTenant,
		brokerFundingAccount,
		edge.PageParams{Limit: 2},
	)
	if err != nil {
		t.Fatalf("read first broker funding page: %v", err)
	}
	if trace.count.Load()-before != 1 {
		t.Fatalf(
			"first broker funding page executed %d statements, want one",
			trace.count.Load()-before,
		)
	}
	if len(first.Items) != 2 ||
		first.Total == nil ||
		*first.Total != 3 ||
		first.NextCursor == nil ||
		first.PrevCursor != nil {
		t.Fatalf("first broker funding page = %#v", first)
	}
	for index, item := range first.Items {
		if item.AccountLogin == nil || *item.AccountLogin != 1001 {
			t.Fatalf(
				"first broker funding item %d account login = %#v, want 1001",
				index,
				item.AccountLogin,
			)
		}
	}
	newest := first.Items[0]
	if newest.FundingID != "019f9b6d-3154-4db1-b639-57c246e92403" ||
		newest.FundingAmount != "-2" ||
		newest.FundingRate != "0.0000125" ||
		newest.OraclePrice != "1000" ||
		newest.PositionSignedQuantity != "1" ||
		newest.Currency != "USDC" ||
		newest.Symbol != "BTC-PERP" ||
		newest.PositionID != hex.EncodeToString(
			[]byte("019f9b6d-3154-4db1-b639-57c246e92201"),
		) ||
		newest.FundingTime != baseTime.
			Add(-100*time.Second).
			Format(time.RFC3339) {
		t.Fatalf("newest broker funding item = %#v", newest)
	}
	if first.Items[1].FundingAmount != "5" {
		t.Fatalf("second broker funding item = %#v", first.Items[1])
	}
	before = trace.count.Load()
	reverseWithoutCursor, err := store.BrokerFunding(
		ctx,
		brokerFundingTenant,
		brokerFundingAccount,
		edge.PageParams{Limit: 2, Direction: "prev"},
	)
	if err != nil {
		t.Fatalf("read reverse broker funding without cursor: %v", err)
	}
	if trace.count.Load()-before != 1 {
		t.Fatalf(
			"reverse broker funding without cursor executed %d statements, want one",
			trace.count.Load()-before,
		)
	}
	if !reflect.DeepEqual(reverseWithoutCursor, first) {
		t.Fatalf(
			"reverse broker funding without cursor = %#v, want newest page %#v",
			reverseWithoutCursor,
			first,
		)
	}

	var preparedStatements int
	if err := api.QueryRow(
		ctx,
		"SELECT count(*) FROM pg_catalog.pg_prepared_statements",
		pgx.QueryExecModeSimpleProtocol,
	).Scan(&preparedStatements); err != nil {
		t.Fatalf("inspect broker funding prepared statements: %v", err)
	}
	if preparedStatements != 0 {
		t.Fatalf(
			"broker funding left %d prepared statements, want custom unnamed execution",
			preparedStatements,
		)
	}

	restartedAPI, restartedTrace := newTracedBrokerFundingAPIPool(
		t,
		admin,
		"platformgo_broker_funding_restart_api_login",
	)
	restartedStore := platformpostgres.NewCompatibilityStore(restartedAPI)
	second, err := restartedStore.BrokerFunding(
		ctx,
		brokerFundingTenant,
		brokerFundingAccount,
		edge.PageParams{
			Limit:  2,
			Cursor: *first.NextCursor,
		},
	)
	if err != nil {
		t.Fatalf("read restarted broker funding page: %v", err)
	}
	if restartedTrace.count.Load() != 1 {
		t.Fatalf(
			"restarted broker funding page executed %d statements, want one",
			restartedTrace.count.Load(),
		)
	}
	if len(second.Items) != 1 ||
		second.Items[0].FundingAmount != "-10" ||
		second.Items[0].AccountLogin == nil ||
		*second.Items[0].AccountLogin != 1001 ||
		second.Total != nil ||
		second.NextCursor != nil ||
		second.PrevCursor == nil {
		t.Fatalf("restarted broker funding page = %#v", second)
	}
	backward, err := restartedStore.BrokerFunding(
		ctx,
		brokerFundingTenant,
		brokerFundingAccount,
		edge.PageParams{
			Limit:     2,
			Cursor:    *second.PrevCursor,
			Direction: "prev",
		},
	)
	if err != nil {
		t.Fatalf("read reverse broker funding page: %v", err)
	}
	if len(backward.Items) != 2 ||
		backward.Items[0].FundingAmount != "-2" ||
		backward.Items[1].FundingAmount != "5" ||
		backward.Total != nil {
		t.Fatalf("reverse broker funding page = %#v", backward)
	}

	if _, err := admin.Exec(ctx, `
		UPDATE identity.account_profiles
		   SET broker_subject = 'urn:xb:tenant:foreign'
		 WHERE account_id = $1`,
		brokerFundingAccount,
	); err != nil {
		t.Fatalf("make broker funding profile foreign: %v", err)
	}
	before = restartedTrace.count.Load()
	denied, err := restartedStore.BrokerFunding(
		ctx,
		brokerFundingTenant,
		brokerFundingAccount,
		edge.PageParams{
			Limit:     2,
			Cursor:    *second.PrevCursor,
			Direction: "prev",
		},
	)
	if !errors.Is(err, edge.ErrForbidden) ||
		!reflect.DeepEqual(denied, edge.FundingPage{}) {
		t.Fatalf(
			"reverse foreign broker funding result=%#v error=%v, want zero-page forbidden",
			denied,
			err,
		)
	}
	if restartedTrace.count.Load()-before != 1 {
		t.Fatalf(
			"reverse foreign broker funding executed %d statements, want one",
			restartedTrace.count.Load()-before,
		)
	}
}

func TestBrokerFundingEqualTimeTieBreaksAcrossBothCursorDirections(t *testing.T) {
	ctx := context.Background()
	admin := postgresPool(t)
	requireBrokerFundingPostgres19Beta2(t, admin)
	resetDurableSchemas(t, admin)
	migrateBrokerFundingTestSchema(t, ctx, admin)
	baseTime := time.Date(2026, 7, 26, 2, 0, 0, 0, time.UTC)
	seedFundingHistory(t, admin, baseTime)
	seedBrokerFundingEqualTimeRow(t, admin, baseTime.Add(-100*time.Second))

	api, _ := newTracedBrokerFundingAPIPool(
		t,
		admin,
		"platformgo_broker_funding_tie_api_login",
	)
	store := platformpostgres.NewCompatibilityStore(api)
	first, err := store.BrokerFunding(
		ctx,
		brokerFundingTenant,
		brokerFundingAccount,
		edge.PageParams{Limit: 1},
	)
	if err != nil {
		t.Fatalf("read first equal-time broker funding page: %v", err)
	}
	if len(first.Items) != 1 ||
		first.Items[0].FundingID != "019f9b6d-3154-4db1-b639-57c246e92405" ||
		first.NextCursor == nil {
		t.Fatalf("first equal-time broker funding page = %#v", first)
	}
	second, err := store.BrokerFunding(
		ctx,
		brokerFundingTenant,
		brokerFundingAccount,
		edge.PageParams{Limit: 1, Cursor: *first.NextCursor},
	)
	if err != nil {
		t.Fatalf("read second equal-time broker funding page: %v", err)
	}
	if len(second.Items) != 1 ||
		second.Items[0].FundingID != "019f9b6d-3154-4db1-b639-57c246e92403" ||
		second.PrevCursor == nil ||
		second.Items[0].FundingID == first.Items[0].FundingID {
		t.Fatalf("second equal-time broker funding page = %#v", second)
	}
	backward, err := store.BrokerFunding(
		ctx,
		brokerFundingTenant,
		brokerFundingAccount,
		edge.PageParams{
			Limit:     1,
			Cursor:    *second.PrevCursor,
			Direction: "backward",
		},
	)
	if err != nil {
		t.Fatalf("read reverse equal-time broker funding page: %v", err)
	}
	if len(backward.Items) != 1 ||
		backward.Items[0].FundingID != first.Items[0].FundingID {
		t.Fatalf("reverse equal-time broker funding page = %#v", backward)
	}
}

func TestBrokerFundingCancellationReleasesSingleConnectionPool(t *testing.T) {
	ctx := context.Background()
	admin := postgresPool(t)
	requireBrokerFundingPostgres19Beta2(t, admin)
	resetDurableSchemas(t, admin)
	migrateBrokerFundingTestSchema(t, ctx, admin)
	seedFundingHistory(
		t,
		admin,
		time.Date(2026, 7, 26, 3, 0, 0, 0, time.UTC),
	)
	basePool := runtimeRoleLoginPool(
		t,
		admin,
		"platformgo_broker_funding_cancellation",
		"platformgo_api",
	)
	config := basePool.Config().Copy()
	config.MaxConns = 1
	singlePool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatalf("open single-connection broker funding pool: %v", err)
	}
	t.Cleanup(singlePool.Close)
	store := platformpostgres.NewCompatibilityStore(singlePool)

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	page, err := store.BrokerFunding(
		canceled,
		brokerFundingTenant,
		brokerFundingAccount,
		edge.PageParams{Limit: 2},
	)
	if err == nil || !reflect.DeepEqual(page, edge.FundingPage{}) {
		t.Fatalf(
			"canceled broker funding page=%#v error=%v, want zero page/error",
			page,
			err,
		)
	}
	page, err = store.BrokerFunding(
		ctx,
		brokerFundingTenant,
		brokerFundingAccount,
		edge.PageParams{Limit: 2},
	)
	if err != nil || len(page.Items) != 2 {
		t.Fatalf("post-cancellation broker funding page=%#v error=%v", page, err)
	}
}

func TestBrokerFundingRequiresBothTenantAuthoritiesBeforeHistory(t *testing.T) {
	ctx := context.Background()
	admin := postgresPool(t)
	requireBrokerFundingPostgres19Beta2(t, admin)
	resetDurableSchemas(t, admin)
	migrateBrokerFundingTestSchema(t, ctx, admin)
	seedFundingHistory(
		t,
		admin,
		time.Date(2026, 7, 26, 1, 0, 0, 0, time.UTC),
	)
	seedBrokerFundingCorruptHistory(
		t,
		admin,
		"019f9b6d-3154-4db1-b639-57c246e92601",
		"019f9b6d-3154-4db1-b639-57c246e92701",
		"019f9b6d-3154-4db1-b639-57c246e92801",
		"1",
		"NaN",
		"USDC",
		brokerFundingProvenanceValid,
		time.Date(2026, 7, 26, 0, 55, 0, 0, time.UTC),
	)
	if _, err := admin.Exec(ctx, `
		INSERT INTO identity.users (
			user_id, login, normalized_login, broker_subject
		) VALUES (
			'urn:xb:user:broker-funding-foreign',
			'broker-funding-foreign',
			'broker-funding-foreign',
			'urn:xb:tenant:foreign'
		)`,
	); err != nil {
		t.Fatalf("seed foreign broker funding user: %v", err)
	}

	api, trace := newTracedBrokerFundingAPIPool(
		t,
		admin,
		"platformgo_broker_funding_authority_api_login",
	)
	store := platformpostgres.NewCompatibilityStore(api)
	cases := []struct {
		name            string
		accountID       string
		ownershipUser   string
		ownershipTenant string
		profileTenant   string
	}{
		{
			name:      "unknown durable account",
			accountID: "urn:xb:account:unknown",
		},
		{name: "missing both authorities"},
		{
			name:            "matching ownership only",
			ownershipUser:   "urn:xb:user:funding-one",
			ownershipTenant: brokerFundingTenant,
		},
		{
			name:            "matching ownership foreign profile",
			ownershipUser:   "urn:xb:user:funding-one",
			ownershipTenant: brokerFundingTenant,
			profileTenant:   "urn:xb:tenant:foreign",
		},
		{
			name:            "foreign ownership matching profile",
			ownershipUser:   "urn:xb:user:broker-funding-foreign",
			ownershipTenant: "urn:xb:tenant:foreign",
			profileTenant:   brokerFundingTenant,
		},
		{
			name:            "both authorities foreign",
			ownershipUser:   "urn:xb:user:broker-funding-foreign",
			ownershipTenant: "urn:xb:tenant:foreign",
			profileTenant:   "urn:xb:tenant:foreign",
		},
	}
	var deniedError string
	var explained brokerFundingQuery
	for index, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			setBrokerFundingAuthorities(
				t,
				admin,
				test.ownershipUser,
				test.ownershipTenant,
				test.profileTenant,
			)
			before := trace.count.Load()
			accountID := test.accountID
			if accountID == "" {
				accountID = brokerFundingAccount
			}
			page, err := store.BrokerFunding(
				ctx,
				brokerFundingTenant,
				accountID,
				edge.PageParams{
					Limit:     200,
					Cursor:    "MTc4NTAwMDAwMDAwMDAwMDAwMDpmZmZmZmZmZi1mZmZmLWZmZmYtZmZmZi1mZmZmZmZmZmZmZmY",
					Direction: "prev",
				},
			)
			if !errors.Is(err, edge.ErrForbidden) ||
				!reflect.DeepEqual(page, edge.FundingPage{}) {
				t.Fatalf(
					"denied broker funding result=%#v error=%v, want zero-page forbidden",
					page,
					err,
				)
			}
			if trace.count.Load()-before != 1 {
				t.Fatalf(
					"denied broker funding executed %d statements, want one",
					trace.count.Load()-before,
				)
			}
			if index == 0 {
				deniedError = err.Error()
				explained = trace.lastBrokerRead(t)
			} else if err.Error() != deniedError {
				t.Fatalf(
					"denied error = %q, want byte-identical %q",
					err,
					deniedError,
				)
			}
		})
	}
	assertBrokerFundingHistoryFunctionNotExecuted(
		t,
		admin,
		explained,
	)
}

func seedBrokerFundingEqualTimeRow(
	t *testing.T,
	pool *pgxpool.Pool,
	logicalTime time.Time,
) {
	t.Helper()
	ctx := context.Background()
	decision := brokerFundingDecisionJSON(
		t,
		"019f9b6d-3154-4db1-b639-57c246e92405",
		"019f9b6d-3154-4db1-b639-57c246e92505",
		"019f9b6d-3154-4db1-b639-57c246e92201",
		"1",
		"1000",
		"7",
		"USDC",
	)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin equal-time broker funding seed: %v", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(ctx, `
		SELECT
			set_config(
				'platformgo.runtime_schema_revision',
				'20260730000400_phase3_broker_funding_acl',
				true
			),
			set_config(
				'platformgo.engine_decision_hash_version',
				'4',
				true
			);
		INSERT INTO engine.input_receipts (
			shard_id, input_id, stream_sequence, schema_version,
			input_hash_version, input_hash, decision_hash_version,
			decision_hash, resulting_state_hash, envelope, decision,
			business_input_hash, business_input_hash_version
		) VALUES (
			41,
			'019f9b6d-3154-4db1-b639-57c246e92305',
			6,
			1,
			1,
			decode(repeat('45', 32), 'hex'),
			4,
			decode(repeat('46', 32), 'hex'),
			decode(repeat('47', 32), 'hex'),
			jsonb_build_object('LogicalTime', $1::bigint),
			$2::jsonb,
			decode(repeat('48', 32), 'hex'),
			1
		);
		INSERT INTO trading.funding_settlements (
			funding_id, settlement_id, position_id, input_id,
			account_id, instrument_id, signed_quantity, oracle_price,
			rate, amount, settlement_currency
		) VALUES (
			'019f9b6d-3154-4db1-b639-57c246e92405',
			'019f9b6d-3154-4db1-b639-57c246e92505',
			'019f9b6d-3154-4db1-b639-57c246e92201',
			'019f9b6d-3154-4db1-b639-57c246e92305',
			'urn:xb:account:funding-one',
			'BTC-PERP',
			1,
			1000,
			0.0000125,
			7,
			'USDC'
		);
		INSERT INTO trading.funding_history_projection (
			funding_id, account_id, instrument_id, position_id, logical_time
		) VALUES (
			'019f9b6d-3154-4db1-b639-57c246e92405',
			'urn:xb:account:funding-one',
			'BTC-PERP',
			'019f9b6d-3154-4db1-b639-57c246e92201',
			$1
		);
		INSERT INTO trading.funding_instrument_provenance (
			funding_id, instrument_id, revision, price_scale, quantity_scale
		) VALUES (
			'019f9b6d-3154-4db1-b639-57c246e92405',
			'BTC-PERP', 1, 2, 3
		)`,
		pgx.QueryExecModeSimpleProtocol,
		logicalTime.UnixNano(),
		string(decision),
	); err != nil {
		t.Fatalf("seed equal-time broker funding row: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit equal-time broker funding row: %v", err)
	}
}

func TestBrokerFundingRejectsLateEconomicCorruptionWithoutPrefix(
	t *testing.T,
) {
	cases := []struct {
		name           string
		signedQuantity string
		oracle         string
		currency       string
		provenance     brokerFundingProvenanceKind
	}{
		{
			name:           "NaN oracle",
			signedQuantity: "1",
			oracle:         "NaN",
			currency:       "USDC",
			provenance:     brokerFundingProvenanceValid,
		},
		{
			name:           "off-tick oracle",
			signedQuantity: "1",
			oracle:         "1000.001",
			currency:       "USDC",
			provenance:     brokerFundingProvenanceValid,
		},
		{
			name:           "positive off-step quantity",
			signedQuantity: "1.0001",
			oracle:         "1000",
			currency:       "USDC",
			provenance:     brokerFundingProvenanceValid,
		},
		{
			name:           "negative off-step quantity",
			signedQuantity: "-1.0001",
			oracle:         "1000",
			currency:       "USDC",
			provenance:     brokerFundingProvenanceValid,
		},
		{
			name:           "unregistered currency",
			signedQuantity: "1",
			oracle:         "1000",
			currency:       "ZZZ",
			provenance:     brokerFundingProvenanceValid,
		},
		{
			name:           "missing provenance",
			signedQuantity: "1",
			oracle:         "1000",
			currency:       "USDC",
			provenance:     brokerFundingProvenanceMissing,
		},
		{
			name:           "mismatched provenance",
			signedQuantity: "1",
			oracle:         "1000",
			currency:       "USDC",
			provenance:     brokerFundingProvenanceMismatch,
		},
	}
	for index, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			admin := postgresPool(t)
			requireBrokerFundingPostgres19Beta2(t, admin)
			resetDurableSchemas(t, admin)
			migrateBrokerFundingTestSchema(t, ctx, admin)
			seedFundingHistory(
				t,
				admin,
				time.Date(2026, 7, 26, 1, 0, 0, 0, time.UTC),
			)
			seedBrokerFundingCorruptHistory(
				t,
				admin,
				"019f9b6d-3154-4db1-b639-57c246e9290"+string(rune('1'+index)),
				"019f9b6d-3154-4db1-b639-57c246e9300"+string(rune('1'+index)),
				"019f9b6d-3154-4db1-b639-57c246e9310"+string(rune('1'+index)),
				test.signedQuantity,
				test.oracle,
				test.currency,
				test.provenance,
				time.Date(2026, 7, 26, 0, 55, 0, 0, time.UTC),
			)
			api, trace := newTracedBrokerFundingAPIPool(
				t,
				admin,
				"platformgo_broker_funding_corrupt_api_"+string(rune('a'+index)),
			)
			page, err := platformpostgres.NewCompatibilityStore(api).
				BrokerFunding(
					ctx,
					brokerFundingTenant,
					brokerFundingAccount,
					edge.PageParams{Limit: 10},
				)
			if err == nil || !reflect.DeepEqual(page, edge.FundingPage{}) {
				t.Fatalf(
					"corrupt broker funding result=%#v error=%v, want zero page and error",
					page,
					err,
				)
			}
			if errors.Is(err, edge.ErrForbidden) {
				t.Fatalf("authorized corrupt broker funding was reported forbidden: %v", err)
			}
			if trace.count.Load() != 1 {
				t.Fatalf(
					"corrupt broker funding executed %d statements, want one",
					trace.count.Load(),
				)
			}
		})
	}
}

func TestBrokerFundingCursorPagesRejectLateCorruptionWithoutPrefix(
	t *testing.T,
) {
	baseTime := time.Date(2026, 7, 26, 1, 0, 0, 0, time.UTC)
	cases := []struct {
		name         string
		direction    string
		forward      bool
		cursorTime   time.Time
		cursorID     string
		corruptTime  time.Time
		fundingID    string
		settlementID string
		inputID      string
		wantPrefixID string
	}{
		{
			name:         "forward cursor",
			forward:      true,
			cursorTime:   baseTime,
			cursorID:     "ffffffff-ffff-ffff-ffff-ffffffffffff",
			corruptTime:  baseTime.Add(-150 * time.Second),
			fundingID:    "019f9b6d-3154-4db1-b639-57c246e94001",
			settlementID: "019f9b6d-3154-4db1-b639-57c246e94101",
			inputID:      "019f9b6d-3154-4db1-b639-57c246e94201",
			wantPrefixID: "019f9b6d-3154-4db1-b639-57c246e92403",
		},
		{
			name:         "backward cursor",
			direction:    "prev",
			cursorTime:   baseTime.Add(-400 * time.Second),
			cursorID:     "00000000-0000-0000-0000-000000000000",
			corruptTime:  baseTime.Add(-250 * time.Second),
			fundingID:    "019f9b6d-3154-4db1-b639-57c246e94002",
			settlementID: "019f9b6d-3154-4db1-b639-57c246e94102",
			inputID:      "019f9b6d-3154-4db1-b639-57c246e94202",
			wantPrefixID: "019f9b6d-3154-4db1-b639-57c246e92401",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			admin := postgresPool(t)
			requireBrokerFundingPostgres19Beta2(t, admin)
			resetDurableSchemas(t, admin)
			migrateBrokerFundingTestSchema(t, ctx, admin)
			seedFundingHistory(t, admin, baseTime)
			seedBrokerFundingCorruptHistory(
				t,
				admin,
				test.fundingID,
				test.settlementID,
				test.inputID,
				"1",
				"1000",
				"USDC",
				brokerFundingProvenanceMissing,
				test.corruptTime,
			)
			assertBrokerFundingCorruptionOrder(
				t,
				admin,
				test.cursorTime,
				test.cursorID,
				test.forward,
				test.wantPrefixID,
				test.fundingID,
			)

			api, trace := newTracedBrokerFundingAPIPool(
				t,
				admin,
				"platformgo_broker_funding_cursor_"+
					strings.ReplaceAll(test.name, " ", "_"),
			)
			cursor := brokerFundingCursor(test.cursorTime, test.cursorID)
			page, err := platformpostgres.NewCompatibilityStore(api).
				BrokerFunding(
					ctx,
					brokerFundingTenant,
					brokerFundingAccount,
					edge.PageParams{
						Limit:     10,
						Cursor:    cursor,
						Direction: test.direction,
					},
				)
			if err == nil ||
				errors.Is(err, edge.ErrForbidden) ||
				!reflect.DeepEqual(page, edge.FundingPage{}) ||
				page.Total != nil {
				t.Fatalf(
					"cursor corruption result=%#v error=%v, want empty page, nil total, non-forbidden error",
					page,
					err,
				)
			}
			if trace.count.Load() != 1 {
				t.Fatalf(
					"cursor corruption executed %d statements, want one",
					trace.count.Load(),
				)
			}
			query := trace.lastBrokerRead(t)
			if len(query.args) != 8 ||
				query.args[5] != true ||
				query.args[7] != test.forward {
				t.Fatalf(
					"cursor query args = %#v, want cursor-present and forward=%t",
					query.args,
					test.forward,
				)
			}
		})
	}
}

func brokerFundingCursor(logicalTime time.Time, fundingID string) string {
	raw := strconv.FormatInt(logicalTime.UnixNano(), 10) + ":" + fundingID
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func assertBrokerFundingCorruptionOrder(
	t *testing.T,
	admin *pgxpool.Pool,
	cursorTime time.Time,
	cursorID string,
	forward bool,
	wantPrefixID string,
	wantCorruptID string,
) {
	t.Helper()
	rows, err := admin.Query(context.Background(), `
		SELECT funding_id::text, instrument_revision
		  FROM trading.read_broker_account_funding_history(
			$1, $2, $3, true, 10, $4
		  )`,
		brokerFundingAccount,
		cursorTime.UnixNano(),
		cursorID,
		forward,
	)
	if err != nil {
		t.Fatalf("read ordered broker funding corruption fixture: %v", err)
	}
	defer rows.Close()
	type orderedRow struct {
		fundingID string
		revision  *int64
	}
	var ordered []orderedRow
	for rows.Next() {
		var row orderedRow
		if err := rows.Scan(&row.fundingID, &row.revision); err != nil {
			t.Fatalf("scan ordered broker funding corruption fixture: %v", err)
		}
		ordered = append(ordered, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate ordered broker funding corruption fixture: %v", err)
	}
	if len(ordered) < 2 ||
		ordered[0].fundingID != wantPrefixID ||
		ordered[0].revision == nil ||
		ordered[1].fundingID != wantCorruptID ||
		ordered[1].revision != nil {
		t.Fatalf(
			"ordered broker funding corruption fixture = %#v, want valid %s then corrupt %s",
			ordered,
			wantPrefixID,
			wantCorruptID,
		)
	}
}

func migrateBrokerFundingTestSchema(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
) {
	t.Helper()
	if err := newCurrentTestMigrator(
		t,
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).Migrate(ctx); err != nil {
		t.Fatalf("migrate broker funding database: %v", err)
	}
}

func requireBrokerFundingPostgres19Beta2(
	t *testing.T,
	pool *pgxpool.Pool,
) {
	t.Helper()
	var version string
	if err := pool.QueryRow(
		context.Background(),
		"SELECT current_setting('server_version')",
	).Scan(&version); err != nil {
		t.Fatalf("read PostgreSQL version: %v", err)
	}
	if !strings.HasPrefix(version, "19beta2") {
		t.Fatalf("PostgreSQL version = %q, want 19beta2", version)
	}
}

func newTracedBrokerFundingAPIPool(
	t *testing.T,
	admin *pgxpool.Pool,
	login string,
) (*pgxpool.Pool, *brokerFundingQueryTrace) {
	t.Helper()
	base := runtimeRoleLoginPool(t, admin, login, "platformgo_api")
	trace := &brokerFundingQueryTrace{}
	config := base.Config().Copy()
	config.MaxConns = 1
	config.MinConns = 0
	config.ConnConfig.Tracer = trace
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("open traced broker funding API pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool, trace
}

func setBrokerFundingAuthorities(
	t *testing.T,
	admin *pgxpool.Pool,
	ownershipUser string,
	ownershipTenant string,
	profileTenant string,
) {
	t.Helper()
	ctx := context.Background()
	if _, err := admin.Exec(
		ctx,
		"DELETE FROM identity.account_profiles WHERE account_id = $1",
		brokerFundingAccount,
	); err != nil {
		t.Fatalf("clear broker funding profile: %v", err)
	}
	if _, err := admin.Exec(
		ctx,
		"DELETE FROM identity.user_accounts WHERE account_id = $1",
		brokerFundingAccount,
	); err != nil {
		t.Fatalf("clear broker funding ownership: %v", err)
	}
	if ownershipTenant != "" {
		if _, err := admin.Exec(ctx, `
			INSERT INTO identity.user_accounts (
				user_id, account_id, broker_subject
			) VALUES ($1, $2, $3)`,
			ownershipUser,
			brokerFundingAccount,
			ownershipTenant,
		); err != nil {
			t.Fatalf("seed broker funding ownership: %v", err)
		}
	}
	if profileTenant != "" {
		if _, err := admin.Exec(ctx, `
			INSERT INTO identity.account_profiles (
				account_id, login, base_currency, market_venue,
				permitted_classes, broker_subject, created_at
			) VALUES (
				$1, 1001, 'USDC', 'HYPERLIQUID',
				ARRAY['CRYPTOCURRENCY'], $2, '2026-07-26T01:00:00Z'
			)`,
			brokerFundingAccount,
			profileTenant,
		); err != nil {
			t.Fatalf("seed broker funding profile: %v", err)
		}
	}
}

func seedBrokerFundingCorruptHistory(
	t *testing.T,
	admin *pgxpool.Pool,
	fundingID string,
	settlementID string,
	inputID string,
	signedQuantity string,
	oracle string,
	currency string,
	provenance brokerFundingProvenanceKind,
	logicalTime time.Time,
) {
	t.Helper()
	ctx := context.Background()
	decision := brokerFundingDecisionJSON(
		t,
		fundingID,
		settlementID,
		"019f9b6d-3154-4db1-b639-57c246e92201",
		signedQuantity,
		oracle,
		"-7",
		currency,
	)
	tx, err := admin.Begin(ctx)
	if err != nil {
		t.Fatalf("begin corrupt broker funding seed: %v", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(ctx, `
		SELECT
			set_config(
				'platformgo.runtime_schema_revision',
				'20260730000400_phase3_broker_funding_acl',
				true
			),
			set_config(
				'platformgo.engine_decision_hash_version',
				'4',
				true
			);
		SET LOCAL session_replication_role = replica`); err != nil {
		t.Fatalf("set corrupt broker funding writer fence: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO engine.input_receipts (
			shard_id, input_id, stream_sequence, schema_version,
			input_hash_version, input_hash, decision_hash_version,
			decision_hash, resulting_state_hash, envelope, decision,
			business_input_hash, business_input_hash_version
		)
		SELECT
			41,
			$1::uuid,
			COALESCE(max(stream_sequence), 0) + 1,
			1,
			1,
			decode(repeat('51', 32), 'hex'),
			4,
			decode(repeat('52', 32), 'hex'),
			decode(repeat('53', 32), 'hex'),
			jsonb_build_object('LogicalTime', $2::bigint),
			$3::jsonb,
			decode(repeat('54', 32), 'hex'),
			1
		  FROM engine.input_receipts
		 WHERE shard_id = 41`,
		inputID,
		logicalTime.UnixNano(),
		string(decision),
	); err != nil {
		t.Fatalf("seed corrupt broker funding receipt: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO trading.funding_settlements (
			funding_id, settlement_id, position_id, input_id,
			account_id, instrument_id, signed_quantity, oracle_price,
			rate, amount, settlement_currency
		) VALUES (
			$1, $2, '019f9b6d-3154-4db1-b639-57c246e92201', $3,
			$4, 'BTC-PERP', $5::numeric, $6::numeric, 0.0000125, -7, $7
		)`,
		fundingID,
		settlementID,
		inputID,
		brokerFundingAccount,
		signedQuantity,
		oracle,
		currency,
	); err != nil {
		t.Fatalf("seed corrupt broker funding settlement: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO trading.funding_history_projection (
			funding_id, account_id, instrument_id, position_id, logical_time
		) VALUES (
			$1, $2, 'BTC-PERP',
			'019f9b6d-3154-4db1-b639-57c246e92201', $3
		)`,
		fundingID,
		brokerFundingAccount,
		logicalTime.UnixNano(),
	); err != nil {
		t.Fatalf("seed corrupt broker funding projection: %v", err)
	}
	switch provenance {
	case brokerFundingProvenanceMissing:
	case brokerFundingProvenanceValid, brokerFundingProvenanceMismatch:
		instrumentID := "BTC-PERP"
		if provenance == brokerFundingProvenanceMismatch {
			instrumentID = "ETH-PERP"
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO trading.funding_instrument_provenance (
				funding_id, instrument_id, revision, price_scale, quantity_scale
			) VALUES ($1, $2, 1, 2, 3)`,
			fundingID,
			instrumentID,
		); err != nil {
			t.Fatalf("seed corrupt broker funding provenance: %v", err)
		}
	default:
		t.Fatalf("unknown broker funding provenance fixture %q", provenance)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit corrupt broker funding row: %v", err)
	}
}

func brokerFundingDecisionJSON(
	t *testing.T,
	fundingID string,
	settlementID string,
	positionID string,
	signedQuantity string,
	oracle string,
	amount string,
	currency string,
) []byte {
	t.Helper()
	parseID := func(value string) engine.ID {
		t.Helper()
		id, err := engine.ParseID(value)
		if err != nil {
			t.Fatalf("parse broker funding fixture ID %q: %v", value, err)
		}
		return id
	}
	encoded, err := json.Marshal(engine.Decision{
		DecisionHashVersion: engine.CurrentDecisionHashVersion,
		CommandResult: engine.CommandResult{
			Status: engine.CommandStatusAccepted,
		},
		FundingChanges: []engine.FundingSnapshot{{
			FundingID:          parseID(fundingID),
			SettlementID:       parseID(settlementID),
			PositionID:         parseID(positionID),
			AccountID:          brokerFundingAccount,
			InstrumentID:       "BTC-PERP",
			SignedQuantity:     signedQuantity,
			OraclePrice:        oracle,
			Rate:               "0.0000125",
			Amount:             amount,
			SettlementCurrency: currency,
		}},
	})
	if err != nil {
		t.Fatalf("encode broker funding fixture decision: %v", err)
	}
	return encoded
}

type brokerFundingExplainNode struct {
	NodeType     string                     `json:"Node Type"`
	FunctionName string                     `json:"Function Name"`
	ActualLoops  float64                    `json:"Actual Loops"`
	Plans        []brokerFundingExplainNode `json:"Plans"`
}

func assertBrokerFundingHistoryFunctionNotExecuted(
	t *testing.T,
	admin *pgxpool.Pool,
	query brokerFundingQuery,
) {
	t.Helper()
	var mode any
	if len(query.args) != 0 {
		mode = query.args[0]
	}
	if query.sql == "" || len(query.args) != 8 ||
		mode != pgx.QueryExecModeExec {
		t.Fatalf(
			"captured broker funding query shape = SQL-present:%t args:%d mode:%v",
			query.sql != "",
			len(query.args),
			mode,
		)
	}
	args := []any{pgx.QueryExecModeExec}
	args = append(args, query.args[1:]...)
	var raw []byte
	if err := admin.QueryRow(
		context.Background(),
		"EXPLAIN (ANALYZE, FORMAT JSON, COSTS OFF, TIMING OFF, SUMMARY OFF) "+
			query.sql,
		args...,
	).Scan(&raw); err != nil {
		t.Fatalf("explain denied broker funding statement: %v", err)
	}
	var document []struct {
		Plan brokerFundingExplainNode `json:"Plan"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatalf("decode denied broker funding plan: %v", err)
	}
	if len(document) != 1 {
		t.Fatalf("denied broker funding plans = %d, want one", len(document))
	}
	function := findBrokerFundingFunctionNode(
		document[0].Plan,
		"read_broker_account_funding_history",
	)
	if function == nil {
		t.Fatalf(
			"denied broker funding plan omits read_broker_account_funding_history: %s",
			raw,
		)
	}
	if function.ActualLoops != 0 {
		t.Fatalf(
			"foreign account funding function loops = %v, want 0: %s",
			function.ActualLoops,
			raw,
		)
	}
}

func findBrokerFundingFunctionNode(
	node brokerFundingExplainNode,
	name string,
) *brokerFundingExplainNode {
	if node.FunctionName == name {
		return &node
	}
	for _, child := range node.Plans {
		if found := findBrokerFundingFunctionNode(child, name); found != nil {
			return found
		}
	}
	return nil
}
