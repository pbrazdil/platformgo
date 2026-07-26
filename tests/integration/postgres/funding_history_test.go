package postgres_test

import (
	"context"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
	"github.com/upcomers-org/platformgo/internal/edge"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Ported from:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/trading/e2e_funding.rs:60
//	test: funding_history_reads_paginates_and_aggregates
//
// Adaptations:
//   - Old mirror rows are represented by authoritative funding settlements and
//     their immutable engine receipts.
//   - Fixed logical timestamps replace wall-clock-relative source fixtures.
//   - The PostgreSQL compatibility reader replaces the Rust query dispatcher.
//
// Assertions preserved:
//   - Account history is newest-first, limited, cursor-paginated, and counted.
//   - All economic values are canonical exact decimal strings.
//   - Position funding totals are exact for all time and the current cycle.
//   - Symbol-wide history contains both accounts and preserves their logins.
func TestFundingHistoryReadsPaginatesAndAggregates(t *testing.T) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := platformpostgres.NewMigrator(
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).Migrate(ctx); err != nil {
		t.Fatalf("migrate funding history database: %v", err)
	}
	baseTime := time.Date(2026, 7, 26, 1, 0, 0, 0, time.UTC)
	const (
		accountOne  = "urn:xb:account:funding-one"
		accountTwo  = "urn:xb:account:funding-two"
		positionOne = "019f9b6d-3154-4db1-b639-57c246e92201"
		positionTwo = "019f9b6d-3154-4db1-b639-57c246e92202"
	)
	seedFundingHistory(t, pool, baseTime)

	apiPool := runtimeRoleLoginPool(
		t,
		pool,
		"platformgo_funding_history_api_login",
		"platformgo_api",
	)
	store := platformpostgres.NewCompatibilityStore(apiPool)
	pageOne, err := store.Funding(
		ctx,
		accountOne,
		edge.PageParams{Limit: 2},
	)
	if err != nil {
		t.Fatalf("read first funding page: %v", err)
	}
	if len(pageOne.Items) != 2 ||
		pageOne.Total == nil ||
		*pageOne.Total != 3 ||
		pageOne.NextCursor == nil {
		t.Fatalf("first funding page = %#v", pageOne)
	}
	newest := pageOne.Items[0]
	if newest.FundingAmount != "-2" ||
		newest.FundingRate != "0.0000125" ||
		newest.OraclePrice != "1000" ||
		newest.PositionSignedQuantity != "1" ||
		newest.Currency != "USDC" ||
		newest.Symbol != "BTC-PERP" ||
		newest.PositionID != hex.EncodeToString([]byte(positionOne)) ||
		newest.FundingTime != baseTime.Add(-100*time.Second).Format(time.RFC3339) {
		t.Fatalf("newest funding = %#v", newest)
	}
	if pageOne.Items[1].FundingAmount != "5" {
		t.Fatalf("second-newest funding = %#v", pageOne.Items[1])
	}
	pageTwo, err := store.Funding(
		ctx,
		accountOne,
		edge.PageParams{Limit: 2, Cursor: *pageOne.NextCursor},
	)
	if err != nil {
		t.Fatalf("read second funding page: %v", err)
	}
	if len(pageTwo.Items) != 1 || pageTwo.Items[0].FundingAmount != "-10" {
		t.Fatalf("second funding page = %#v", pageTwo)
	}
	if pageTwo.Total != nil || pageTwo.PrevCursor == nil {
		t.Fatalf("second funding page cursors = %#v", pageTwo)
	}
	backward, err := store.Funding(
		ctx,
		accountOne,
		edge.PageParams{
			Limit:     2,
			Cursor:    *pageTwo.PrevCursor,
			Direction: "prev",
		},
	)
	if err != nil {
		t.Fatalf("read previous funding page: %v", err)
	}
	if len(backward.Items) != 2 ||
		backward.Items[0].FundingAmount != "-2" ||
		backward.Items[1].FundingAmount != "5" {
		t.Fatalf("previous funding page = %#v", backward)
	}

	allTime, err := store.FundingPaidByPosition(
		ctx,
		accountOne,
		positionOne,
		nil,
	)
	if err != nil || allTime != "-7" {
		t.Fatalf("all-time funding = %q, error %v", allTime, err)
	}
	cycleStart := baseTime.Add(-250 * time.Second)
	currentCycle, err := store.FundingPaidByPosition(
		ctx,
		accountOne,
		positionOne,
		&cycleStart,
	)
	if err != nil || currentCycle != "3" {
		t.Fatalf("current-cycle funding = %q, error %v", currentCycle, err)
	}

	fleet, err := store.FundingBySymbol(
		ctx,
		"BTC-PERP",
		edge.PageParams{Limit: 10},
	)
	if err != nil {
		t.Fatalf("read fleet funding: %v", err)
	}
	if len(fleet.Items) != 4 {
		t.Fatalf("fleet funding rows = %d, want 4", len(fleet.Items))
	}
	logins := make(map[int64]bool)
	for _, item := range fleet.Items {
		if item.AccountLogin == nil {
			t.Fatalf("fleet funding omitted account login: %#v", item)
		}
		logins[*item.AccountLogin] = true
	}
	if !logins[1001] || !logins[1002] {
		t.Fatalf("fleet funding logins = %v", logins)
	}
}

func TestFundingHistoryReadsPersistedEngineLogicalTime(t *testing.T) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := platformpostgres.NewMigrator(
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).MigrateAndProvision(ctx, 41); err != nil {
		t.Fatalf("migrate engine-role funding database: %v", err)
	}
	enginePool := postgresRolePool(t, "platformgo_engine")
	seedReconciliationFixtureWithStorePool(t, pool, enginePool, 41)

	var (
		logicalTimeType string
		hasLegacyKey    bool
	)
	if err := pool.QueryRow(ctx, `
		SELECT
			jsonb_typeof(receipt.envelope -> 'LogicalTime'),
			receipt.envelope ? 'logicalTime'
		  FROM trading.funding_settlements AS funding
		  JOIN engine.input_receipts AS receipt
		    ON receipt.input_id = funding.input_id
		   AND receipt.shard_id = 41
		 LIMIT 1`,
	).Scan(&logicalTimeType, &hasLegacyKey); err != nil {
		t.Fatalf("inspect persisted funding receipt: %v", err)
	}
	if logicalTimeType != "number" || hasLegacyKey {
		t.Fatalf(
			"persisted logical time shape = type %q legacy-key %t",
			logicalTimeType,
			hasLegacyKey,
		)
	}

	page, err := platformpostgres.NewCompatibilityStore(pool).Funding(
		ctx,
		"account-1",
		edge.PageParams{Limit: 1},
	)
	if err != nil {
		t.Fatalf("read EngineStore-backed funding: %v", err)
	}
	wantTime := time.Date(
		2026,
		time.July,
		24,
		14,
		0,
		6,
		123456789,
		time.UTC,
	).Format(time.RFC3339Nano)
	if len(page.Items) != 1 || page.Items[0].FundingTime != wantTime {
		t.Fatalf("EngineStore-backed funding page = %#v, want time %q", page, wantTime)
	}
}

func TestFundingHistoryRejectsSettlementWithoutProjection(t *testing.T) {
	pool := postgresPool(t)
	seedReconciliationFixture(t, pool, 41)
	_, err := pool.Exec(context.Background(), `
		INSERT INTO trading.funding_settlements (
			funding_id, settlement_id, position_id, input_id,
			account_id, instrument_id, signed_quantity, oracle_price,
			rate, amount, settlement_currency
		)
		SELECT
			'40000000-0000-0000-0000-000000000001',
			'40000000-0000-0000-0000-000000000002',
			position_id,
			input_id,
			account_id,
			instrument_id,
			signed_quantity,
			oracle_price,
			rate,
			amount,
			settlement_currency
		  FROM trading.funding_settlements
		 LIMIT 1`)
	var pgError *pgconn.PgError
	if !errors.As(err, &pgError) || pgError.Code != "55000" {
		t.Fatalf("settlement without projection error = %v, want SQLSTATE 55000", err)
	}
}

func seedFundingHistory(
	t *testing.T,
	pool *pgxpool.Pool,
	baseTime time.Time,
) {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin funding history seed: %v", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(ctx, `
		SELECT
			set_config(
				'platformgo.runtime_schema_revision',
				'20260725001100_phase3_committed_realtime_outbox',
				true
			),
			set_config(
				'platformgo.engine_decision_hash_version',
				'4',
				true
			);
		INSERT INTO engine.deployment_shard (shard_id)
		VALUES (41)
		ON CONFLICT DO NOTHING;
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
			('urn:xb:account:funding-one', 'NETTING'),
			('urn:xb:account:funding-two', 'NETTING');
		INSERT INTO engine.account_shards (account_id, shard_id) VALUES
			('urn:xb:account:funding-one', 41),
			('urn:xb:account:funding-two', 41);
		INSERT INTO identity.users (
			user_id, login, normalized_login, broker_subject
		) VALUES
			(
				'urn:xb:user:funding-one', 'funding-one', 'funding-one',
				'urn:xb:tenant:funding-proof'
			),
			(
				'urn:xb:user:funding-two', 'funding-two', 'funding-two',
				'urn:xb:tenant:funding-proof'
			);
		INSERT INTO identity.user_accounts (
			user_id, account_id, broker_subject
		) VALUES
			(
				'urn:xb:user:funding-one', 'urn:xb:account:funding-one',
				'urn:xb:tenant:funding-proof'
			),
			(
				'urn:xb:user:funding-two', 'urn:xb:account:funding-two',
				'urn:xb:tenant:funding-proof'
			)`); err != nil {
		t.Fatalf("seed funding identities: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO identity.account_profiles (
			account_id, login, base_currency, market_venue,
			permitted_classes, created_at, broker_subject
		) VALUES
			(
				'urn:xb:account:funding-one', 1001, 'USDC',
				'HYPERLIQUID', ARRAY['CRYPTOCURRENCY'], $1,
				'urn:xb:tenant:funding-proof'
			),
			(
				'urn:xb:account:funding-two', 1002, 'USDC',
				'HYPERLIQUID', ARRAY['CRYPTOCURRENCY'], $1,
				'urn:xb:tenant:funding-proof'
			)`,
		baseTime,
	); err != nil {
		t.Fatalf("seed funding profiles: %v", err)
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
			input_id,
			stream_sequence,
			1,
			1,
			decode(repeat(lpad(stream_sequence::text, 2, '0'), 32), 'hex'),
			4,
			decode(repeat(lpad((stream_sequence + 10)::text, 2, '0'), 32), 'hex'),
			decode(repeat(lpad((stream_sequence + 20)::text, 2, '0'), 32), 'hex'),
			jsonb_build_object('LogicalTime', logical_time),
			'{"DecisionHashVersion":4}'::jsonb,
			decode(repeat(lpad((stream_sequence + 30)::text, 2, '0'), 32), 'hex'),
			1
		  FROM (
			VALUES
				(
					'019f9b6d-3154-4db1-b639-57c246e92301'::uuid,
					1::bigint,
					$1::bigint
				),
				(
					'019f9b6d-3154-4db1-b639-57c246e92302'::uuid,
					2::bigint,
					$2::bigint
				),
				(
					'019f9b6d-3154-4db1-b639-57c246e92303'::uuid,
					3::bigint,
					$3::bigint
				),
				(
					'019f9b6d-3154-4db1-b639-57c246e92304'::uuid,
					4::bigint,
					$3::bigint
				)
		  ) AS receipts(input_id, stream_sequence, logical_time)`,
		baseTime.Add(-300*time.Second).UnixNano(),
		baseTime.Add(-200*time.Second).UnixNano(),
		baseTime.Add(-100*time.Second).UnixNano(),
	); err != nil {
		t.Fatalf("seed funding receipts: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO trading.funding_settlements (
			funding_id, settlement_id, position_id, input_id,
			account_id, instrument_id, signed_quantity, oracle_price,
			rate, amount, settlement_currency
		) VALUES
			(
				'019f9b6d-3154-4db1-b639-57c246e92401',
				'019f9b6d-3154-4db1-b639-57c246e92501',
				'019f9b6d-3154-4db1-b639-57c246e92201',
				'019f9b6d-3154-4db1-b639-57c246e92301',
				'urn:xb:account:funding-one', 'BTC-PERP',
				1, 1000, 0.0000125, -10, 'USDC'
			),
			(
				'019f9b6d-3154-4db1-b639-57c246e92402',
				'019f9b6d-3154-4db1-b639-57c246e92502',
				'019f9b6d-3154-4db1-b639-57c246e92201',
				'019f9b6d-3154-4db1-b639-57c246e92302',
				'urn:xb:account:funding-one', 'BTC-PERP',
				1, 1000, 0.0000125, 5, 'USDC'
			),
			(
				'019f9b6d-3154-4db1-b639-57c246e92403',
				'019f9b6d-3154-4db1-b639-57c246e92503',
				'019f9b6d-3154-4db1-b639-57c246e92201',
				'019f9b6d-3154-4db1-b639-57c246e92303',
				'urn:xb:account:funding-one', 'BTC-PERP',
				1, 1000, 0.0000125, -2, 'USDC'
			),
			(
				'019f9b6d-3154-4db1-b639-57c246e92404',
				'019f9b6d-3154-4db1-b639-57c246e92504',
				'019f9b6d-3154-4db1-b639-57c246e92202',
				'019f9b6d-3154-4db1-b639-57c246e92304',
				'urn:xb:account:funding-two', 'BTC-PERP',
				1, 1000, 0.0000125, -3, 'USDC'
			)`); err != nil {
		t.Fatalf("seed funding settlements: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO trading.funding_history_projection (
			funding_id, account_id, instrument_id, position_id, logical_time
		) VALUES
			(
				'019f9b6d-3154-4db1-b639-57c246e92401',
				'urn:xb:account:funding-one',
				'BTC-PERP',
				'019f9b6d-3154-4db1-b639-57c246e92201',
				$1
			),
			(
				'019f9b6d-3154-4db1-b639-57c246e92402',
				'urn:xb:account:funding-one',
				'BTC-PERP',
				'019f9b6d-3154-4db1-b639-57c246e92201',
				$2
			),
			(
				'019f9b6d-3154-4db1-b639-57c246e92403',
				'urn:xb:account:funding-one',
				'BTC-PERP',
				'019f9b6d-3154-4db1-b639-57c246e92201',
				$3
			),
			(
				'019f9b6d-3154-4db1-b639-57c246e92404',
				'urn:xb:account:funding-two',
				'BTC-PERP',
				'019f9b6d-3154-4db1-b639-57c246e92202',
				$3
			)`,
		baseTime.Add(-300*time.Second).UnixNano(),
		baseTime.Add(-200*time.Second).UnixNano(),
		baseTime.Add(-100*time.Second).UnixNano(),
	); err != nil {
		t.Fatalf("seed funding history projection: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit funding history seed: %v", err)
	}
}
