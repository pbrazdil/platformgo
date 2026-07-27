package runtime

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
	"github.com/upcomers-org/platformgo/internal/testsupport/postgresfixture"
	"github.com/upcomers-org/platformgo/migrations"
)

func TestReplayCleanupDrainsBoundedBrokerEchoBatchesOnPostgres19(
	t *testing.T,
) {
	databaseURL := os.Getenv("PLATFORMGO_TEST_POSTGRES_DSN")
	if databaseURL == "" {
		t.Log("PLATFORMGO_TEST_POSTGRES_DSN is not configured")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin, openAdminErr := pgxpool.New(ctx, databaseURL)
	if openAdminErr != nil {
		t.Fatal(openAdminErr)
	}
	defer admin.Close()
	if resetErr := postgresfixture.ResetDurableSchemas(ctx, admin); resetErr != nil {
		t.Fatal(resetErr)
	}
	if roleErr := ensureReplayCleanupRuntimeRoles(ctx, admin); roleErr != nil {
		t.Fatal(roleErr)
	}
	if migrationErr := platformpostgres.NewMigrator(
		admin,
		migrations.Files,
	).MigrateAndProvision(ctx, 73); migrationErr != nil {
		t.Fatal(migrationErr)
	}
	var postgresMajor int
	if versionErr := admin.QueryRow(ctx, `
		SELECT current_setting('server_version_num')::integer / 10000`,
	).Scan(&postgresMajor); versionErr != nil {
		t.Fatal(versionErr)
	}
	if postgresMajor != 19 {
		t.Fatalf("PostgreSQL major = %d, want 19", postgresMajor)
	}

	const (
		expiredRows = 205
		liveRows    = 3
	)
	if _, seedExpiredErr := admin.Exec(ctx, `
		INSERT INTO identity.broker_echo_replays (
			scope,
			idempotency_key_hash,
			request_hash,
			response_status,
			response_headers,
			response_body,
			created_at,
			expires_at
		)
		SELECT
			'broker-echo' || chr(31) ||
				'urn:xb:apikey:runtime-cleanup-expired-' || item,
			sha256(convert_to('expired-' || item, 'UTF8')),
			sha256(convert_to('request-' || item, 'UTF8')),
			200,
			'{"Content-Type":["application/json"]}'::jsonb,
			convert_to(
				'{"id":"019fa562-2c4f-4b7e-8db3-' ||
				lpad(item::text, 12, '0') || E'"}\n',
				'UTF8'
			),
			statement_timestamp() - interval '48 hours',
			statement_timestamp() - interval '24 hours'
		  FROM generate_series(1, $1::integer) AS item`,
		expiredRows,
	); seedExpiredErr != nil {
		t.Fatal(seedExpiredErr)
	}
	if _, seedLiveErr := admin.Exec(ctx, `
		INSERT INTO identity.broker_echo_replays (
			scope,
			idempotency_key_hash,
			request_hash,
			response_status,
			response_headers,
			response_body,
			created_at,
			expires_at
		)
		SELECT
			'broker-echo' || chr(31) ||
				'urn:xb:apikey:runtime-cleanup-live-' || item,
			sha256(convert_to('live-' || item, 'UTF8')),
			sha256(convert_to('live-request-' || item, 'UTF8')),
			200,
			jsonb_build_object(
				'Content-Type',
				jsonb_build_array('application/json'),
				'X-Live',
				jsonb_build_array(item::text)
			),
			convert_to(
				'{"id":"019fa562-2c4f-4b7e-8db3-' ||
				lpad((900000 + item)::text, 12, '0') || E'"}\n',
				'UTF8'
			),
			statement_timestamp(),
			statement_timestamp() + interval '24 hours'
		  FROM generate_series(1, $1::integer) AS item`,
		liveRows,
	); seedLiveErr != nil {
		t.Fatal(seedLiveErr)
	}
	liveBefore := readLiveBrokerEchoRows(t, ctx, admin)

	apiConfig, parseConfigErr := pgxpool.ParseConfig(databaseURL)
	if parseConfigErr != nil {
		t.Fatal(parseConfigErr)
	}
	apiConfig.AfterConnect = func(
		connectContext context.Context,
		connection *pgx.Conn,
	) error {
		_, setRoleErr := connection.Exec(
			connectContext,
			"SET ROLE platformgo_api",
		)
		return setRoleErr
	}
	api, openAPIErr := pgxpool.NewWithConfig(ctx, apiConfig)
	if openAPIErr != nil {
		t.Fatal(openAPIErr)
	}
	defer api.Close()
	store := platformpostgres.NewCompatibilityStore(api)
	policy, coverageErr := store.BrokerEchoReplayCoverage(ctx)
	if coverageErr != nil {
		t.Fatal(coverageErr)
	}

	cleanupContext, cancelCleanup := context.WithCancel(ctx)
	ticks := make(chan time.Time)
	result := make(chan error, 1)
	completed := make(chan struct{}, 3)
	var (
		countsMu         sync.Mutex
		apiKeyCounts     []int64
		brokerEchoCounts []int64
	)
	go func() {
		result <- runReplayCleanup(
			cleanupContext,
			ticks,
			func(callContext context.Context) (int64, error) {
				deleted, purgeErr := store.PurgeExpiredAPIKeyReplays(
					callContext,
					apiKeyReplayCleanupBatch,
				)
				countsMu.Lock()
				apiKeyCounts = append(apiKeyCounts, deleted)
				countsMu.Unlock()
				return deleted, purgeErr
			},
			func(callContext context.Context) (int64, error) {
				return drainExpiredBrokerEchoReplays(
					callContext,
					func(
						batchContext context.Context,
						batchLimit int,
					) (int64, error) {
						deleted, purgeErr := store.PurgeExpiredBrokerEchoReplays(
							batchContext,
							batchLimit,
						)
						countsMu.Lock()
						brokerEchoCounts = append(
							brokerEchoCounts,
							deleted,
						)
						countsMu.Unlock()
						return deleted, purgeErr
					},
					policy.PurgeBatchSize,
					policy.MaxBatchesPerCycle,
				)
			},
			func(callContext context.Context) error {
				_, apiCoverageErr :=
					store.APIKeyReplayCoverage(callContext)
				brokerCoverage, brokerCoverageErr :=
					store.BrokerEchoReplayCoverage(callContext)
				completed <- struct{}{}
				if apiCoverageErr != nil {
					return apiCoverageErr
				}
				if brokerCoverageErr != nil {
					return brokerCoverageErr
				}
				return validateBrokerEchoReplayCoverage(
					brokerCoverage,
					true,
					false,
				)
			},
		)
	}()
	ticks <- time.Time{}
	select {
	case <-completed:
	case err := <-result:
		t.Fatalf("cleanup stopped before draining batches: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	cancelCleanup()
	if err := <-result; err != nil {
		t.Fatalf("canceled cleanup = %v", err)
	}
	countsMu.Lock()
	gotAPIKeyCounts := slices.Clone(apiKeyCounts)
	gotBrokerEchoCounts := slices.Clone(brokerEchoCounts)
	countsMu.Unlock()
	if !slices.Equal(gotAPIKeyCounts, []int64{0}) {
		t.Fatalf("API-key deleted counts = %v", gotAPIKeyCounts)
	}
	if !slices.Equal(
		gotBrokerEchoCounts,
		[]int64{
			int64(policy.PurgeBatchSize),
			int64(policy.PurgeBatchSize),
			5,
		},
	) {
		t.Fatalf("broker-echo deleted counts = %v", gotBrokerEchoCounts)
	}

	var remainingExpired int
	if countErr := admin.QueryRow(ctx, `
		SELECT count(*)
		  FROM identity.broker_echo_replays
		 WHERE expires_at <= statement_timestamp()`,
	).Scan(&remainingExpired); countErr != nil {
		t.Fatal(countErr)
	}
	if remainingExpired != 0 {
		t.Fatalf("remaining expired broker-echo rows = %d", remainingExpired)
	}
	liveAfter := readLiveBrokerEchoRows(t, ctx, admin)
	if len(liveAfter) != liveRows || !slices.EqualFunc(
		liveAfter,
		liveBefore,
		func(left, right brokerEchoLiveRow) bool {
			return left.scope == right.scope &&
				bytes.Equal(left.keyHash, right.keyHash) &&
				bytes.Equal(left.requestHash, right.requestHash) &&
				left.status == right.status &&
				left.headers == right.headers &&
				bytes.Equal(left.body, right.body) &&
				left.createdAt.Equal(right.createdAt) &&
				left.expiresAt.Equal(right.expiresAt)
		},
	) {
		t.Fatalf(
			"live broker-echo rows changed during cleanup: before=%#v after=%#v",
			liveBefore,
			liveAfter,
		)
	}
}

type brokerEchoLiveRow struct {
	scope       string
	keyHash     []byte
	requestHash []byte
	status      int
	headers     string
	body        []byte
	createdAt   time.Time
	expiresAt   time.Time
}

func readLiveBrokerEchoRows(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
) []brokerEchoLiveRow {
	t.Helper()
	rows, err := pool.Query(ctx, `
		SELECT
			scope,
			idempotency_key_hash,
			request_hash,
			response_status,
			response_headers::text,
			response_body,
			created_at,
			expires_at
		  FROM identity.broker_echo_replays
		 WHERE expires_at > statement_timestamp()
		 ORDER BY scope COLLATE "C", idempotency_key_hash`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var result []brokerEchoLiveRow
	for rows.Next() {
		var row brokerEchoLiveRow
		if err := rows.Scan(
			&row.scope,
			&row.keyHash,
			&row.requestHash,
			&row.status,
			&row.headers,
			&row.body,
			&row.createdAt,
			&row.expiresAt,
		); err != nil {
			t.Fatal(err)
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func ensureReplayCleanupRuntimeRoles(
	ctx context.Context,
	pool *pgxpool.Pool,
) error {
	for _, role := range []string{
		"platformgo_api",
		"platformgo_engine",
		"platformgo_outbox",
		"platformgo_projector",
		"platformgo_realtime",
		"platformgo_realtime_repair",
	} {
		var exists bool
		if err := pool.QueryRow(
			ctx,
			"SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1)",
			role,
		).Scan(&exists); err != nil {
			return fmt.Errorf("inspect runtime role %s: %w", role, err)
		}
		if exists {
			continue
		}
		if _, err := pool.Exec(
			ctx,
			"CREATE ROLE "+pgx.Identifier{role}.Sanitize()+" NOLOGIN",
		); err != nil {
			return fmt.Errorf("provision runtime role %s: %w", role, err)
		}
	}
	return nil
}
