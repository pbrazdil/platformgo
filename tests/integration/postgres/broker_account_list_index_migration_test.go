package postgres_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
)

const (
	brokerAccountListPreviousMigration = "20260730000200_phase3_currency_scale_authority_fence.up.sql"
	brokerAccountListIndexMigration    = "20260730000300_phase3_broker_account_list_index.up.sql"
	brokerAccountListIndexName         = "user_accounts_broker_list_idx"
)

func TestBrokerAccountListIndexMigrationUpgradesPopulatedCurrentMain(
	t *testing.T,
) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	previousFiles := migrationFilesThrough(
		t,
		brokerAccountListPreviousMigration,
	)
	if err := platformpostgres.NewMigrator(
		pool,
		previousFiles,
	).MigrateAndProvision(ctx, 7); err != nil {
		t.Fatalf("apply 38-file current-main schema: %v", err)
	}
	seedBrokerAccountListIndexFixture(t, ctx, pool)
	beforeState := brokerAccountListOwnershipState(
		t,
		ctx,
		pool,
	)
	if plan := brokerAccountListOwnershipPlan(t, ctx, pool); !strings.Contains(
		plan,
		`"Node Type": "Seq Scan"`,
	) {
		t.Fatalf("pre-migration ownership plan did not expose global scan:\n%s", plan)
	}

	current := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, brokerAccountListIndexMigration),
	)
	if err := current.Migrate(ctx); err != nil {
		t.Fatalf("apply broker account-list index migration: %v", err)
	}
	afterState := brokerAccountListOwnershipState(
		t,
		ctx,
		pool,
	)
	if beforeState != afterState {
		t.Fatalf(
			"index migration changed ownership state:\nbefore=%+v\nafter=%+v",
			beforeState,
			afterState,
		)
	}
	assertBrokerAccountListIndexCatalog(t, ctx, pool)
	assertBrokerAccountListExecutionPlans(t, ctx, pool)
	assertBrokerAccountListMigrationChecksum(t, ctx, pool)
	if err := current.VerifyCurrent(ctx); err != nil {
		t.Fatalf("verify 39-file schema: %v", err)
	}
	if err := platformpostgres.NewMigrator(
		pool,
		previousFiles,
	).VerifyCurrent(ctx); !errors.Is(
		err,
		platformpostgres.ErrDatabaseSchemaAhead,
	) {
		t.Fatalf("38-file binary verification = %v, want schema-ahead", err)
	}
	if err := current.Migrate(ctx); err != nil {
		t.Fatalf("idempotent index migration retry: %v", err)
	}
}

func TestBrokerAccountListIndexMigrationLockTimeoutRollsBackAndRetries(
	t *testing.T,
) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, brokerAccountListPreviousMigration),
	).MigrateAndProvision(ctx, 7); err != nil {
		t.Fatalf("apply 38-file current-main schema: %v", err)
	}
	beforeState := brokerAccountListOwnershipState(
		t,
		ctx,
		pool,
	)
	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := blocker.Exec(
		ctx,
		"LOCK TABLE identity.user_accounts IN ROW EXCLUSIVE MODE",
	); err != nil {
		_ = blocker.Rollback(ctx)
		t.Fatal(err)
	}
	current := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, brokerAccountListIndexMigration),
	)
	started := time.Now()
	migrateErr := current.Migrate(ctx)
	elapsed := time.Since(started)
	if migrateErr == nil {
		_ = blocker.Rollback(ctx)
		t.Fatal("index migration succeeded through conflicting writer lock")
	}
	var postgresErr *pgconn.PgError
	if !errors.As(migrateErr, &postgresErr) ||
		postgresErr.Code != "55P03" {
		_ = blocker.Rollback(ctx)
		t.Fatalf("lock conflict error=%v, want SQLSTATE 55P03", migrateErr)
	}
	if elapsed < 4*time.Second || elapsed > 8*time.Second {
		_ = blocker.Rollback(ctx)
		t.Fatalf("lock conflict elapsed=%s, want bounded near five seconds", elapsed)
	}
	if err := blocker.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	var (
		indexExists    bool
		migrationCount int
	)
	if err := pool.QueryRow(ctx, `
		SELECT
			to_regclass('identity.user_accounts_broker_list_idx') IS NOT NULL,
			(
				SELECT count(*)
				  FROM engine.schema_migrations
				 WHERE filename = $1
			)`,
		brokerAccountListIndexMigration,
	).Scan(&indexExists, &migrationCount); err != nil {
		t.Fatal(err)
	}
	if indexExists || migrationCount != 0 {
		t.Fatalf(
			"failed migration left index=%v journal rows=%d",
			indexExists,
			migrationCount,
		)
	}
	afterState := brokerAccountListOwnershipState(
		t,
		ctx,
		pool,
	)
	if beforeState != afterState {
		t.Fatalf(
			"failed index migration changed ownership state:\nbefore=%+v\nafter=%+v",
			beforeState,
			afterState,
		)
	}
	if err := current.Migrate(ctx); err != nil {
		t.Fatalf("retry index migration after drain: %v", err)
	}
	assertBrokerAccountListIndexCatalog(t, ctx, pool)
}

func seedBrokerAccountListIndexFixture(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
		WITH fixture AS (
			SELECT
				sequence,
				CASE
					WHEN sequence <= 200
					THEN 'urn:xb:user:00000000-0000-4000-8000-' ||
						lpad(sequence::text, 12, '0')
					ELSE 'urn:xb:user:00000000-0000-4000-8000-999999999998'
				END AS user_id,
				'urn:xb:account:00000000-0000-4000-8000-' ||
					lpad((sequence + 100000)::text, 12, '0') AS account_id,
				CASE
					WHEN sequence <= 200
					THEN 'urn:xb:tenant:index-target'
					ELSE 'urn:xb:tenant:index-foreign'
				END AS broker_subject
			  FROM generate_series(1, 3200) AS sequence
		)
		INSERT INTO identity.users (
			user_id, login, normalized_login, broker_subject
		)
		SELECT
			user_id,
			'index-user-' || min(sequence),
			'index-user-' || min(sequence),
			broker_subject
		  FROM fixture
		 GROUP BY user_id, broker_subject;

		WITH fixture AS (
			SELECT
				sequence,
				'urn:xb:account:00000000-0000-4000-8000-' ||
					lpad((sequence + 100000)::text, 12, '0') AS account_id
			  FROM generate_series(1, 3200) AS sequence
		)
		INSERT INTO trading.accounts (account_id, oms_mode)
		SELECT account_id, 'NETTING'
		  FROM fixture;

		WITH fixture AS (
			SELECT
				sequence,
				CASE
					WHEN sequence <= 200
					THEN 'urn:xb:user:00000000-0000-4000-8000-' ||
						lpad(sequence::text, 12, '0')
					ELSE 'urn:xb:user:00000000-0000-4000-8000-999999999998'
				END AS user_id,
				'urn:xb:account:00000000-0000-4000-8000-' ||
					lpad((sequence + 100000)::text, 12, '0') AS account_id,
				CASE
					WHEN sequence <= 200
					THEN 'urn:xb:tenant:index-target'
					ELSE 'urn:xb:tenant:index-foreign'
				END AS broker_subject
			  FROM generate_series(1, 3200) AS sequence
		)
		INSERT INTO identity.user_accounts (
			user_id, account_id, broker_subject
		)
		SELECT user_id, account_id, broker_subject
		  FROM fixture;

		WITH fixture AS (
			SELECT
				sequence,
				'urn:xb:account:00000000-0000-4000-8000-' ||
					lpad((sequence + 100000)::text, 12, '0') AS account_id,
				CASE
					WHEN sequence <= 200
					THEN 'urn:xb:tenant:index-target'
					ELSE 'urn:xb:tenant:index-foreign'
				END AS broker_subject
			  FROM generate_series(1, 3200) AS sequence
		)
		INSERT INTO identity.account_profiles (
			account_id, login, base_currency, market_venue,
			permitted_classes, broker_subject, created_at
		)
		SELECT
			account_id,
			80000000 + sequence,
			'USDC',
			'HYPERLIQUID',
			ARRAY['CRYPTOCURRENCY'],
			broker_subject,
			'2026-07-30T08:09:10Z'
		  FROM fixture;

		ANALYZE identity.user_accounts;
		ANALYZE identity.account_profiles;
		ANALYZE trading.accounts`,
	); err != nil {
		t.Fatalf("seed broker account-list index fixture: %v", err)
	}
}

type brokerAccountListState struct {
	RowDigest       string
	FileNode        uint32
	SecurityCatalog string
	PriorJournal    string
}

func brokerAccountListOwnershipState(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
) brokerAccountListState {
	t.Helper()
	var state brokerAccountListState
	if err := pool.QueryRow(ctx, `
		SELECT
			encode(sha256(convert_to(COALESCE((
				SELECT string_agg(
					jsonb_build_array(
						user_id,
						account_id,
						to_char(
							created_at AT TIME ZONE 'UTC',
							'YYYY-MM-DD"T"HH24:MI:SS.US"Z"'
						),
						broker_subject
					)::text,
					E'\n' ORDER BY user_id, account_id
				)
				  FROM identity.user_accounts
			), ''), 'UTF8')), 'hex'),
			pg_relation_filenode('identity.user_accounts'::regclass),
			jsonb_build_object(
				'owner', relowner::regrole::text,
				'table_acl', relacl,
				'column_acls', (
					SELECT jsonb_agg(
						jsonb_build_array(attnum, attname, attacl)
						ORDER BY attnum
					)
					  FROM pg_catalog.pg_attribute
					 WHERE attrelid = class.oid
					   AND attnum > 0
					   AND NOT attisdropped
				),
				'owner_defaults', (
					SELECT jsonb_agg(
						jsonb_build_array(
							defaclnamespace,
							defaclobjtype,
							defaclacl
						)
						ORDER BY defaclnamespace, defaclobjtype
					)
					  FROM pg_catalog.pg_default_acl
					 WHERE defaclrole = class.relowner
					   AND defaclnamespace IN (
							0,
							'identity'::regnamespace
					   )
				)
			)::text,
			encode(sha256(convert_to(COALESCE((
				SELECT string_agg(
					filename || ':' || encode(checksum, 'hex'),
					E'\n' ORDER BY filename
				)
				  FROM engine.schema_migrations
				 WHERE filename <= $1
			), ''), 'UTF8')), 'hex')
		  FROM pg_catalog.pg_class AS class
		 WHERE class.oid = 'identity.user_accounts'::regclass`,
		brokerAccountListPreviousMigration,
	).Scan(
		&state.RowDigest,
		&state.FileNode,
		&state.SecurityCatalog,
		&state.PriorJournal,
	); err != nil {
		t.Fatal(err)
	}
	return state
}

func brokerAccountListOwnershipPlan(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
) string {
	t.Helper()
	var plan string
	if err := pool.QueryRow(ctx, `
		EXPLAIN (FORMAT JSON)
		SELECT account_id, user_id
		  FROM identity.user_accounts
		 WHERE broker_subject = 'urn:xb:tenant:index-target'`,
	).Scan(&plan); err != nil {
		t.Fatal(err)
	}
	return plan
}

func assertBrokerAccountListIndexCatalog(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
) {
	t.Helper()
	var (
		ready      bool
		valid      bool
		definition string
		predicate  string
	)
	if err := pool.QueryRow(ctx, `
		SELECT
			index.indisready,
			index.indisvalid,
			pg_catalog.pg_get_indexdef(index.indexrelid),
			pg_catalog.pg_get_expr(index.indpred, index.indrelid)
		  FROM pg_catalog.pg_index AS index
		 WHERE index.indexrelid =
			'identity.user_accounts_broker_list_idx'::regclass`,
	).Scan(&ready, &valid, &definition, &predicate); err != nil {
		t.Fatal(err)
	}
	if !ready ||
		!valid ||
		!strings.Contains(
			definition,
			"USING btree (broker_subject, user_id, account_id)",
		) ||
		predicate != "(broker_subject IS NOT NULL)" {
		t.Fatalf(
			"index catalog ready=%v valid=%v definition=%q predicate=%q",
			ready,
			valid,
			definition,
			predicate,
		)
	}
}

func assertBrokerAccountListExecutionPlans(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
) {
	t.Helper()
	connection, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Release()
	if _, err := connection.Exec(ctx, `
		SET plan_cache_mode = force_custom_plan;
		PREPARE broker_account_list_filtered(text, text) AS
		WITH ownership AS MATERIALIZED (
			SELECT account_id, user_id
			  FROM identity.user_accounts
			 WHERE broker_subject = $1
			   AND user_id = $2
			   AND EXISTS (
					SELECT 1
					  FROM identity.users
					 WHERE user_id = $2
					   AND broker_subject = $1
			   )
		)
		SELECT
			ownership.account_id,
			profile.login,
			ownership.user_id,
			profile.base_currency,
			account.margin_mode,
			account.oms_mode,
			profile.market_venue,
			profile.permitted_classes,
			account.status,
			profile.created_at
		  FROM ownership
		  LEFT JOIN LATERAL (
			SELECT
				login, base_currency, market_venue,
				permitted_classes, created_at
			  FROM identity.account_profiles
			 WHERE account_id = ownership.account_id
			   AND broker_subject = $1
			 OFFSET 0
		  ) AS profile ON true
		  LEFT JOIN LATERAL (
			SELECT margin_mode, oms_mode, status
			  FROM trading.accounts
			 WHERE account_id = ownership.account_id
			 OFFSET 0
		  ) AS account ON true
		 ORDER BY profile.login, ownership.account_id COLLATE "C";
		PREPARE broker_account_list_unfiltered(text) AS
		WITH ownership AS MATERIALIZED (
			SELECT account_id, user_id
			  FROM identity.user_accounts
			 WHERE broker_subject = $1
		)
		SELECT
			ownership.account_id,
			profile.login,
			ownership.user_id,
			profile.base_currency,
			account.margin_mode,
			account.oms_mode,
			profile.market_venue,
			profile.permitted_classes,
			account.status,
			profile.created_at
		  FROM ownership
		  LEFT JOIN LATERAL (
			SELECT
				login, base_currency, market_venue,
				permitted_classes, created_at
			  FROM identity.account_profiles
			 WHERE account_id = ownership.account_id
			   AND broker_subject = $1
			 OFFSET 0
		  ) AS profile ON true
		  LEFT JOIN LATERAL (
			SELECT margin_mode, oms_mode, status
			  FROM trading.accounts
			 WHERE account_id = ownership.account_id
			 OFFSET 0
		  ) AS account ON true
		 ORDER BY profile.login, ownership.account_id COLLATE "C"`,
	); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = connection.Exec(
			context.Background(),
			"DEALLOCATE broker_account_list_filtered; "+
				"DEALLOCATE broker_account_list_unfiltered",
		)
	}()
	for _, test := range []struct {
		name     string
		explain  string
		validate func(*testing.T, brokerAccountExplainNode)
	}{
		{
			name: "filtered foreign user",
			explain: `
				EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)
				EXECUTE broker_account_list_filtered(
					'urn:xb:tenant:index-target',
					'urn:xb:user:00000000-0000-4000-8000-999999999998'
				)`,
			validate: func(t *testing.T, plan brokerAccountExplainNode) {
				user := requireBrokerAccountPlanNode(
					t,
					plan,
					"identity.users authority lookup",
					func(node brokerAccountExplainNode) bool {
						return node.RelationName == "users" &&
							node.IndexName == "users_id_broker_subject_key"
					},
				)
				if user.ActualLoops != 1 || user.ActualRows != 0 {
					t.Fatalf("foreign authority lookup loops=%v rows=%v, want 1/0", user.ActualLoops, user.ActualRows)
				}
				ownership := requireBrokerAccountPlanNode(
					t,
					plan,
					"identity.user_accounts ownership lookup",
					func(node brokerAccountExplainNode) bool {
						return node.RelationName == "user_accounts"
					},
				)
				if ownership.ActualLoops != 0 ||
					ownership.ActualRows != 0 ||
					ownership.SharedHitBlocks != 0 ||
					ownership.SharedReadBlocks != 0 {
					t.Fatalf(
						"foreign ownership lookup loops=%v rows=%v hit=%d read=%d, want all zero",
						ownership.ActualLoops,
						ownership.ActualRows,
						ownership.SharedHitBlocks,
						ownership.SharedReadBlocks,
					)
				}
			},
		},
		{
			name: "unfiltered tenant",
			explain: `
				EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)
				EXECUTE broker_account_list_unfiltered(
					'urn:xb:tenant:index-target'
				)`,
			validate: func(t *testing.T, plan brokerAccountExplainNode) {
				for _, index := range []string{
					"user_accounts_broker_list_idx",
					"account_profiles_pkey",
					"accounts_pkey",
				} {
					requireBrokerAccountPlanNode(
						t,
						plan,
						index,
						func(node brokerAccountExplainNode) bool {
							return node.IndexName == index
						},
					)
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var plan string
			if err := connection.QueryRow(ctx, test.explain).Scan(&plan); err != nil {
				t.Fatal(err)
			}
			var document []struct {
				Plan brokerAccountExplainNode `json:"Plan"`
			}
			if err := json.Unmarshal([]byte(plan), &document); err != nil {
				t.Fatalf("decode execution plan: %v\n%s", err, plan)
			}
			if len(document) != 1 {
				t.Fatalf("execution plan document count=%d, want 1", len(document))
			}
			if findBrokerAccountPlanNode(
				document[0].Plan,
				func(node brokerAccountExplainNode) bool {
					return node.NodeType == "Seq Scan"
				},
			) != nil {
				t.Fatalf("execution plan uses sequential scan:\n%s", plan)
			}
			test.validate(t, document[0].Plan)
		})
	}
}

type brokerAccountExplainNode struct {
	NodeType         string                     `json:"Node Type"`
	RelationName     string                     `json:"Relation Name"`
	IndexName        string                     `json:"Index Name"`
	ActualLoops      float64                    `json:"Actual Loops"`
	ActualRows       float64                    `json:"Actual Rows"`
	SharedHitBlocks  int                        `json:"Shared Hit Blocks"`
	SharedReadBlocks int                        `json:"Shared Read Blocks"`
	Plans            []brokerAccountExplainNode `json:"Plans"`
}

func findBrokerAccountPlanNode(
	node brokerAccountExplainNode,
	matches func(brokerAccountExplainNode) bool,
) *brokerAccountExplainNode {
	if matches(node) {
		return &node
	}
	for _, child := range node.Plans {
		if found := findBrokerAccountPlanNode(child, matches); found != nil {
			return found
		}
	}
	return nil
}

func requireBrokerAccountPlanNode(
	t *testing.T,
	plan brokerAccountExplainNode,
	name string,
	matches func(brokerAccountExplainNode) bool,
) brokerAccountExplainNode {
	t.Helper()
	found := findBrokerAccountPlanNode(plan, matches)
	if found == nil {
		t.Fatalf("execution plan omits %s", name)
	}
	return *found
}

func assertBrokerAccountListMigrationChecksum(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(
		"..",
		"..",
		"..",
		"migrations",
		brokerAccountListIndexMigration,
	))
	if err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256(raw)
	var stored []byte
	if err := pool.QueryRow(ctx, `
		SELECT checksum
		  FROM engine.schema_migrations
		 WHERE filename = $1`,
		brokerAccountListIndexMigration,
	).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if !equalBytes(stored, want[:]) {
		t.Fatalf("migration checksum=%x, want %x", stored, want)
	}
}
