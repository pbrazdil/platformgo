package postgres_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	brokerEchoPreviousMigration   = "20260726001000_phase3_validate_fill_effective_leverage.up.sql"
	brokerEchoReplayMigration     = "20260727000100_phase3_broker_echo_exact_replay.up.sql"
	brokerEchoCapacityMigration   = "20260727000200_phase3_broker_echo_capacity_authority.up.sql"
	brokerEchoIntegrityMigration  = "20260727000300_phase3_broker_echo_coverage_integrity.up.sql"
	brokerEchoFinalGuardMigration = "20260727000400_phase3_broker_echo_replay_guards.up.sql"
)

func TestBrokerEchoReplayMigrationUpgradesExactLiveLegacyResponse(t *testing.T) {
	ctx := context.Background()
	pool := postgresPool(t)
	applyBrokerEchoPreviousSchema(t, pool)

	const (
		principal      = "urn:xb:apikey:migration-upgrade"
		idempotencyKey = "žluťoučký-klíč-🔑"
		responseID     = "019fa562-2c4f-4b7e-8db3-ec1fc8d53901"
	)
	scope := "broker-echo\x1f" + principal
	var databaseNow time.Time
	if err := pool.QueryRow(ctx, "SELECT statement_timestamp()").Scan(
		&databaseNow,
	); err != nil {
		t.Fatalf("read PostgreSQL time before legacy claim: %v", err)
	}
	applicationNow := databaseNow.Add(-5 * time.Second)
	requestedExpiresAt := applicationNow.Add(24 * time.Hour)
	var claimedID string
	if err := pool.QueryRow(ctx, `
		SELECT identity.claim_broker_echo(
			$1,
			$2,
			decode(repeat('a1', 32), 'hex'),
			$3,
			$4
		)`,
		principal,
		idempotencyKey,
		responseID,
		requestedExpiresAt,
	).Scan(&claimedID); err != nil {
		t.Fatalf("claim live replay through prior authority: %v", err)
	}
	if claimedID != responseID {
		t.Fatalf("prior broker-echo claim id = %q, want %q", claimedID, responseID)
	}
	var createdAt time.Time
	var expiresAt time.Time
	if err := pool.QueryRow(ctx, `
		SELECT created_at, expires_at
		  FROM identity.idempotency_responses
		 WHERE scope = $1
		   AND idempotency_key = $2`,
		scope,
		idempotencyKey,
	).Scan(&createdAt, &expiresAt); err != nil {
		t.Fatalf("read prior-authority live replay: %v", err)
	}
	legacyLifetime := expiresAt.Sub(createdAt)
	if legacyLifetime >= 24*time.Hour ||
		legacyLifetime <= 23*time.Hour+59*time.Minute {
		t.Fatalf(
			"prior-authority replay lifetime = %s, want deliberately below 24h",
			legacyLifetime,
		)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO identity.idempotency_responses (
			scope,
			idempotency_key,
			request_hash,
			response_status,
			response_body,
			created_at,
			expires_at
		) VALUES
		(
			'broker-echo' || chr(31) || 'urn:xb:apikey:expired-invalid',
			'expired-invalid',
			decode(repeat('b2', 32), 'hex'),
			599,
			'["not-an-object"]',
			transaction_timestamp() - interval '2 days',
			transaction_timestamp() - interval '1 day'
		),
		(
			'unrelated-scope',
			'live-unrelated-invalid',
			decode(repeat('c3', 32), 'hex'),
			599,
			'null',
			transaction_timestamp() - interval '1 hour',
			transaction_timestamp() + interval '1 day'
		)`); err != nil {
		t.Fatalf("seed ignored legacy rows: %v", err)
	}

	if err := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, brokerEchoReplayMigration),
	).Migrate(ctx); err != nil {
		t.Fatalf("apply broker-echo exact replay migration: %v", err)
	}

	expectedKeyHash := sha256.Sum256([]byte(idempotencyKey))
	var (
		storedScope   string
		storedKeyHash []byte
		requestHash   []byte
		status        int
		headers       []byte
		body          []byte
		storedCreated time.Time
		storedExpires time.Time
	)
	if err := pool.QueryRow(ctx, `
		SELECT
			scope,
			idempotency_key_hash,
			request_hash,
			response_status,
			response_headers,
			response_body,
			created_at,
			expires_at
		  FROM identity.broker_echo_replays
		 WHERE scope = $1`,
		scope,
	).Scan(
		&storedScope,
		&storedKeyHash,
		&requestHash,
		&status,
		&headers,
		&body,
		&storedCreated,
		&storedExpires,
	); err != nil {
		t.Fatalf("read upgraded replay: %v", err)
	}
	if storedScope != scope ||
		!slices.Equal(storedKeyHash, expectedKeyHash[:]) ||
		!slices.Equal(requestHash, slices.Repeat([]byte{0xa1}, 32)) ||
		status != 200 ||
		string(headers) != `{"Content-Type": ["application/json"]}` ||
		string(body) != `{"id":"`+responseID+"\"}\n" ||
		!storedCreated.Equal(createdAt) ||
		!storedExpires.Equal(expiresAt) {
		t.Fatalf(
			"upgraded replay scope=%q key_hash=%x request_hash=%x "+
				"status=%d headers=%s body=%q created=%s expires=%s",
			storedScope,
			storedKeyHash,
			requestHash,
			status,
			headers,
			body,
			storedCreated,
			storedExpires,
		)
	}
	assertBrokerEchoCapacityIntermediateTip(t, pool)

	if err := migrateBrokerEchoCapacitySchema(t, pool); err != nil {
		t.Fatalf("apply broker-echo capacity migration: %v", err)
	}
	assertBrokerEchoReplaySnapshot(
		t,
		pool,
		scope,
		storedKeyHash,
		requestHash,
		status,
		headers,
		body,
		storedCreated,
		storedExpires,
		nil,
	)
	assertBrokerEchoCapacityFinalCatalog(t, pool)

	if err := migrateBrokerEchoIntegritySchema(t, pool); err != nil {
		t.Fatalf("apply broker-echo integrity migration: %v", err)
	}
	legacyMarker := false
	assertBrokerEchoReplaySnapshot(
		t,
		pool,
		scope,
		storedKeyHash,
		requestHash,
		status,
		headers,
		body,
		storedCreated,
		storedExpires,
		&legacyMarker,
	)
	assertBrokerEchoIntegrityFinalCatalog(t, pool)
	if err := migrateBrokerEchoFinalGuardSchema(t, pool); err != nil {
		t.Fatalf("apply broker-echo final guard migration: %v", err)
	}
	assertBrokerEchoFinalGuardCatalog(t, pool)

	var (
		legacyKey     string
		legacyStatus  int
		legacyBody    []byte
		legacyHash    []byte
		legacyCreated time.Time
		legacyExpires time.Time
		legacyRows    int
		upgradedRows  int
	)
	if err := pool.QueryRow(ctx, `
		SELECT
			idempotency_key,
			response_status,
			response_body::text,
			request_hash,
			created_at,
			expires_at,
			count(*) OVER ()
		  FROM identity.idempotency_responses
		 WHERE scope = $1
		   AND idempotency_key = $2`,
		scope,
		idempotencyKey,
	).Scan(
		&legacyKey,
		&legacyStatus,
		&legacyBody,
		&legacyHash,
		&legacyCreated,
		&legacyExpires,
		&legacyRows,
	); err != nil {
		t.Fatalf("read preserved legacy replay: %v", err)
	}
	if err := pool.QueryRow(
		ctx,
		"SELECT count(*) FROM identity.broker_echo_replays",
	).Scan(&upgradedRows); err != nil {
		t.Fatal(err)
	}
	if legacyKey != idempotencyKey ||
		legacyStatus != 200 ||
		string(legacyBody) != `{"id": "`+responseID+`"}` ||
		!slices.Equal(legacyHash, slices.Repeat([]byte{0xa1}, 32)) ||
		!legacyCreated.Equal(createdAt) ||
		!legacyExpires.Equal(expiresAt) ||
		legacyRows != 1 ||
		upgradedRows != 1 {
		t.Fatalf(
			"legacy key=%q status=%d body=%s hash=%x created=%s expires=%s "+
				"rows=%d upgraded_rows=%d",
			legacyKey,
			legacyStatus,
			legacyBody,
			legacyHash,
			legacyCreated,
			legacyExpires,
			legacyRows,
			upgradedRows,
		)
	}

	var (
		replayOutcome  string
		replayRetry    int64
		replayCapacity string
		replayStatus   int
		replayHeaders  []byte
		replayBody     []byte
	)
	if err := pool.QueryRow(ctx, `
		SELECT
			outcome,
			retry_after_seconds,
			capacity_scope,
			response_status,
			response_headers,
			response_body
		  FROM identity.claim_broker_echo_response(
			$1,
			$2,
			decode(repeat('a1', 32), 'hex'),
			200,
			'{"Content-Type":["application/json"]}',
			convert_to(
				'{"id":"019fa562-2c4f-4b7e-8db3-ec1fc8d53902"}' ||
					chr(10),
				'UTF8'
			)
		  )`,
		principal,
		expectedKeyHash[:],
	).Scan(
		&replayOutcome,
		&replayRetry,
		&replayCapacity,
		&replayStatus,
		&replayHeaders,
		&replayBody,
	); err != nil {
		t.Fatalf("replay upgraded prior response: %v", err)
	}
	if replayOutcome != "stored" || replayRetry != 0 || replayCapacity != "" ||
		replayStatus != status || !slices.Equal(replayHeaders, headers) ||
		!slices.Equal(replayBody, body) {
		t.Fatalf(
			"upgraded replay outcome=%q retry=%d capacity=%q status=%d "+
				"headers=%s body=%q",
			replayOutcome,
			replayRetry,
			replayCapacity,
			replayStatus,
			replayHeaders,
			replayBody,
		)
	}

	const currentPrincipal = "urn:xb:apikey:current-time-authority"
	currentKeyHash := sha256.Sum256([]byte("current-time-authority-key"))
	var (
		currentOutcome  string
		currentRetry    int64
		currentCapacity string
		currentStatus   int
		currentHeaders  []byte
		currentBody     []byte
	)
	if err := pool.QueryRow(ctx, `
		SELECT
			outcome,
			retry_after_seconds,
			capacity_scope,
			response_status,
			response_headers,
			response_body
		  FROM identity.claim_broker_echo_response(
			$1,
			$2,
			decode(repeat('a2', 32), 'hex'),
			200,
			'{"Content-Type":["application/json"]}',
			convert_to(
				'{"id":"019fa562-2c4f-4b7e-8db3-ec1fc8d53903"}' ||
					chr(10),
				'UTF8'
			)
		  )`,
		currentPrincipal,
		currentKeyHash[:],
	).Scan(
		&currentOutcome,
		&currentRetry,
		&currentCapacity,
		&currentStatus,
		&currentHeaders,
		&currentBody,
	); err != nil {
		t.Fatalf("claim current PostgreSQL-time response: %v", err)
	}
	if currentOutcome != "stored" || currentRetry != 0 ||
		currentCapacity != "" || currentStatus != 200 ||
		string(currentHeaders) != `{"Content-Type": ["application/json"]}` ||
		string(currentBody) !=
			"{\"id\":\"019fa562-2c4f-4b7e-8db3-ec1fc8d53903\"}\n" {
		t.Fatalf(
			"current claim outcome=%q retry=%d capacity=%q status=%d "+
				"headers=%s body=%q",
			currentOutcome,
			currentRetry,
			currentCapacity,
			currentStatus,
			currentHeaders,
			currentBody,
		)
	}
	var (
		currentMarker        bool
		currentExactLifetime bool
	)
	if err := pool.QueryRow(ctx, `
		SELECT
			postgres_time_authority,
			expires_at = created_at + interval '24 hours'
		  FROM identity.broker_echo_replays
		 WHERE scope = 'broker-echo' || chr(31) || $1
		   AND idempotency_key_hash = $2`,
		currentPrincipal,
		currentKeyHash[:],
	).Scan(&currentMarker, &currentExactLifetime); err != nil {
		t.Fatalf("inspect current PostgreSQL-time response: %v", err)
	}
	if !currentMarker || !currentExactLifetime {
		t.Fatalf(
			"current response marker=%t exact_lifetime=%t, want true/true",
			currentMarker,
			currentExactLifetime,
		)
	}

	for index, lifetime := range []string{"23 hours", "24 hours"} {
		_, err := pool.Exec(ctx, `
			INSERT INTO identity.broker_echo_replays (
				scope,
				idempotency_key_hash,
				request_hash,
				response_status,
				response_headers,
				response_body,
				created_at,
				expires_at,
				postgres_time_authority
			) VALUES (
				'broker-echo' || chr(31) ||
					'urn:xb:apikey:owner-false-insert-' || $1::text,
				sha256(convert_to('owner-false-insert-' || $1::text, 'UTF8')),
				decode(repeat('a4', 32), 'hex'),
				200,
				'{"Content-Type":["application/json"]}',
				convert_to(
					'{"id":"019fa562-2c4f-4b7e-8db3-ec1fc8d5390' ||
						(4 + $1::integer)::text || '"}' || chr(10),
					'UTF8'
				),
				statement_timestamp(),
				statement_timestamp() + $2::interval,
				false
			)`,
			strconv.Itoa(index),
			lifetime,
		)
		requireBrokerEchoPostgresCode(t, err, "55000")
	}

	assertBrokerEchoReplayCatalogAndACL(t, pool)
}

func TestBrokerEchoCapacityMigrationWaitsForSkewedApplicationExpiryAuthority(
	t *testing.T,
) {
	ctx := context.Background()
	pool := postgresPool(t)
	applyBrokerEchoPreviousSchema(t, pool)

	const (
		principal      = "urn:xb:apikey:ahead-of-postgres"
		idempotencyKey = "ahead-of-postgres-key"
		responseID     = "019fa562-2c4f-4b7e-8db3-ec1fc8d53909"
	)
	var databaseNow time.Time
	if err := pool.QueryRow(ctx, "SELECT statement_timestamp()").Scan(
		&databaseNow,
	); err != nil {
		t.Fatalf("read PostgreSQL time before skewed claim: %v", err)
	}
	applicationNow := databaseNow.Add(3 * time.Second)
	requestedExpiresAt := applicationNow.Add(24 * time.Hour)
	var claimedID string
	if err := pool.QueryRow(ctx, `
		SELECT identity.claim_broker_echo(
			$1,
			$2,
			decode(repeat('a5', 32), 'hex'),
			$3,
			$4
		)`,
		principal,
		idempotencyKey,
		responseID,
		requestedExpiresAt,
	).Scan(&claimedID); err != nil {
		t.Fatalf("claim skewed legacy replay: %v", err)
	}
	if claimedID != responseID {
		t.Fatalf("skewed legacy claim id = %q, want %q", claimedID, responseID)
	}

	var legacyCreatedAt time.Time
	var legacyExpiresAt time.Time
	if err := pool.QueryRow(ctx, `
		SELECT created_at, expires_at
		  FROM identity.idempotency_responses
		 WHERE scope = 'broker-echo' || chr(31) || $1
		   AND idempotency_key = $2`,
		principal,
		idempotencyKey,
	).Scan(&legacyCreatedAt, &legacyExpiresAt); err != nil {
		t.Fatalf("read skewed legacy replay: %v", err)
	}
	if !legacyExpiresAt.Equal(requestedExpiresAt) {
		t.Fatalf(
			"skewed requested expiry = %s, stored %s",
			requestedExpiresAt,
			legacyExpiresAt,
		)
	}

	if err := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, brokerEchoReplayMigration),
	).Migrate(ctx); err != nil {
		t.Fatalf("apply exact replay migration before skew bound: %v", err)
	}
	keyHash := sha256.Sum256([]byte(idempotencyKey))
	expectedHeaders := []byte(`{"Content-Type": ["application/json"]}`)
	expectedBody := []byte(`{"id":"` + responseID + "\"}\n")
	assertBrokerEchoReplaySnapshot(
		t,
		pool,
		"broker-echo\x1f"+principal,
		keyHash[:],
		slices.Repeat([]byte{0xa5}, 32),
		200,
		expectedHeaders,
		expectedBody,
		legacyCreatedAt,
		legacyExpiresAt,
		nil,
	)
	journalBefore := brokerEchoMigrationJournalSnapshot(t, pool)
	replaysBefore := brokerEchoReplayRowsSnapshot(t, pool, false)

	err := migrateBrokerEchoCapacitySchema(t, pool)
	requireBrokerEchoPostgresCode(t, err, "55000")
	assertBrokerEchoCapacityIntermediateTip(t, pool)
	assertBrokerEchoSnapshotsEqual(
		t,
		"skew-bound migration journal",
		journalBefore,
		brokerEchoMigrationJournalSnapshot(t, pool),
	)
	assertBrokerEchoSnapshotsEqual(
		t,
		"skew-bound replay authority",
		replaysBefore,
		brokerEchoReplayRowsSnapshot(t, pool, false),
	)

	if _, err := pool.Exec(ctx, "SELECT pg_sleep_until($1)", applicationNow); err != nil {
		t.Fatalf("wait until PostgreSQL statement time catches application clock: %v", err)
	}
	var databaseCaughtUp bool
	if err := pool.QueryRow(
		ctx,
		"SELECT statement_timestamp() >= $1",
		applicationNow,
	).Scan(&databaseCaughtUp); err != nil {
		t.Fatalf("verify PostgreSQL caught application clock: %v", err)
	}
	if !databaseCaughtUp {
		t.Fatal("PostgreSQL statement time did not catch the injected application clock")
	}
	assertBrokerEchoSnapshotsEqual(
		t,
		"wait-only replay authority",
		replaysBefore,
		brokerEchoReplayRowsSnapshot(t, pool, false),
	)

	if err := migrateBrokerEchoCurrentSchema(pool); err != nil {
		t.Fatalf("retry skewed broker-echo migration after time catch-up: %v", err)
	}
	legacyMarker := false
	assertBrokerEchoReplaySnapshot(
		t,
		pool,
		"broker-echo\x1f"+principal,
		keyHash[:],
		slices.Repeat([]byte{0xa5}, 32),
		200,
		expectedHeaders,
		expectedBody,
		legacyCreatedAt,
		legacyExpiresAt,
		&legacyMarker,
	)
	assertBrokerEchoFinalGuardCatalog(t, pool)
}

func TestBrokerEchoIntegrityMigrationFencesLegacyMarkerAtCommittedTip(
	t *testing.T,
) {
	ctx := context.Background()
	pool := postgresPool(t)
	applyBrokerEchoPreviousSchema(t, pool)

	const (
		principal      = "urn:xb:apikey:integrity-marker-fence"
		idempotencyKey = "integrity-marker-fence-key"
		responseID     = "019fa562-2c4f-4b7e-8db3-ec1fc8d53910"
	)
	var databaseNow time.Time
	if err := pool.QueryRow(ctx, "SELECT statement_timestamp()").Scan(
		&databaseNow,
	); err != nil {
		t.Fatalf("read PostgreSQL time before legacy marker claim: %v", err)
	}
	requestedExpiresAt := databaseNow.Add(-5 * time.Second).Add(24 * time.Hour)
	var claimedID string
	if err := pool.QueryRow(ctx, `
		SELECT identity.claim_broker_echo(
			$1,
			$2,
			decode(repeat('a6', 32), 'hex'),
			$3,
			$4
		)`,
		principal,
		idempotencyKey,
		responseID,
		requestedExpiresAt,
	).Scan(&claimedID); err != nil {
		t.Fatalf("claim genuine legacy marker replay: %v", err)
	}
	if claimedID != responseID {
		t.Fatalf("legacy marker claim id = %q, want %q", claimedID, responseID)
	}

	if err := migrateBrokerEchoIntegritySchema(t, pool); err != nil {
		t.Fatalf("apply exact broker-echo integrity tip: %v", err)
	}
	legacyKeyHash := sha256.Sum256([]byte(idempotencyKey))
	var (
		legacyBody      []byte
		legacyCreatedAt time.Time
		legacyExpiresAt time.Time
	)
	if err := pool.QueryRow(ctx, `
		SELECT response_body, created_at, expires_at
		  FROM identity.broker_echo_replays
		 WHERE scope = 'broker-echo' || chr(31) || $1
		   AND idempotency_key_hash = $2
		   AND NOT postgres_time_authority`,
		principal,
		legacyKeyHash[:],
	).Scan(&legacyBody, &legacyCreatedAt, &legacyExpiresAt); err != nil {
		t.Fatalf("read genuine migrated legacy marker replay: %v", err)
	}
	journalBefore := brokerEchoMigrationJournalSnapshot(t, pool)
	replaysBefore := brokerEchoReplayRowsSnapshot(t, pool, true)

	for index, test := range []struct {
		name          string
		createdOffset string
		lifetime      string
	}{
		{
			name:          "backdated exact lifetime",
			createdOffset: "-23 hours",
			lifetime:      "24 hours",
		},
		{
			name:          "equal cutover short lifetime",
			createdOffset: "0 seconds",
			lifetime:      "23 hours",
		},
		{
			name:          "future exact lifetime",
			createdOffset: "1 second",
			lifetime:      "24 hours",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := pool.Exec(ctx, `
				INSERT INTO identity.broker_echo_replays (
					scope,
					idempotency_key_hash,
					request_hash,
					response_status,
					response_headers,
					response_body,
					created_at,
					expires_at,
					postgres_time_authority
				)
				SELECT
					'broker-echo' || chr(31) ||
						'urn:xb:apikey:forged-marker-' || $1::text,
					sha256(convert_to('forged-marker-' || $1::text, 'UTF8')),
					decode(repeat('a7', 32), 'hex'),
					200,
					'{"Content-Type":["application/json"]}',
					convert_to(
						'{"id":"019fa562-2c4f-4b7e-8db3-ec1fc8d5391' ||
							(1 + $1::integer)::text || '"}' || chr(10),
						'UTF8'
					),
					migration.applied_at + $2::interval,
					migration.applied_at + $2::interval + $3::interval,
					false
				  FROM engine.schema_migrations AS migration
				 WHERE migration.filename = $4`,
				strconv.Itoa(index),
				test.createdOffset,
				test.lifetime,
				brokerEchoIntegrityMigration,
			)
			requireBrokerEchoPostgresCode(t, err, "55000")
		})
	}

	assertBrokerEchoSnapshotsEqual(
		t,
		"003 journal after rejected forged legacy markers",
		journalBefore,
		brokerEchoMigrationJournalSnapshot(t, pool),
	)
	assertBrokerEchoSnapshotsEqual(
		t,
		"003 replay rows after rejected forged legacy markers",
		replaysBefore,
		brokerEchoReplayRowsSnapshot(t, pool, true),
	)
	legacyMarker := false
	assertBrokerEchoReplaySnapshot(
		t,
		pool,
		"broker-echo\x1f"+principal,
		legacyKeyHash[:],
		slices.Repeat([]byte{0xa6}, 32),
		200,
		[]byte(`{"Content-Type": ["application/json"]}`),
		legacyBody,
		legacyCreatedAt,
		legacyExpiresAt,
		&legacyMarker,
	)

	if err := migrateBrokerEchoFinalGuardSchema(t, pool); err != nil {
		t.Fatalf("advance fenced integrity tip through final guards: %v", err)
	}
	assertBrokerEchoFinalGuardCatalog(t, pool)
}

func TestBrokerEchoMigrationsOverrideHostileDefaultFunctionACL(t *testing.T) {
	ctx := context.Background()
	pool := postgresPool(t)
	applyBrokerEchoPreviousSchema(t, pool)

	var migrationOwner string
	if err := pool.QueryRow(ctx, "SELECT current_user").Scan(&migrationOwner); err != nil {
		t.Fatalf("read migration owner: %v", err)
	}
	hostileRole := fmt.Sprintf("broker_echo_hostile_%d", os.Getpid())
	ownerIdentifier := quoteBrokerEchoIdentifier(migrationOwner)
	hostileIdentifier := quoteBrokerEchoIdentifier(hostileRole)
	if _, err := pool.Exec(ctx, "CREATE ROLE "+hostileIdentifier+" NOLOGIN"); err != nil {
		t.Fatalf("create unlisted hostile role: %v", err)
	}
	hostileDefaults := fmt.Sprintf(`
		GRANT USAGE ON SCHEMA identity TO %[2]s;
		ALTER DEFAULT PRIVILEGES FOR ROLE %[1]s IN SCHEMA identity
			GRANT EXECUTE ON FUNCTIONS TO %[2]s;
		ALTER DEFAULT PRIVILEGES FOR ROLE %[1]s IN SCHEMA identity
			GRANT ALL PRIVILEGES ON TABLES TO %[2]s;
		GRANT ALL PRIVILEGES ON TABLE identity.idempotency_responses TO %[2]s`,
		ownerIdentifier,
		hostileIdentifier,
	)
	if _, err := pool.Exec(ctx, hostileDefaults); err != nil {
		_, _ = pool.Exec(ctx, "DROP ROLE "+hostileIdentifier)
		t.Fatalf("install hostile owner ACLs: %v", err)
	}
	restored := false
	t.Cleanup(func() {
		if !restored {
			if err := restoreBrokerEchoHostileRole(
				context.Background(),
				pool,
				ownerIdentifier,
				hostileIdentifier,
			); err != nil {
				t.Errorf("restore unlisted hostile role after failure: %v", err)
			}
		}
	})

	if err := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, brokerEchoReplayMigration),
	).Migrate(ctx); err != nil {
		t.Fatalf("apply exact replay migration under hostile defaults: %v", err)
	}
	assertBrokerEchoCapacityIntermediateTip(t, pool)
	assertBrokerEchoRawACLAllowlist(t, pool, hostileRole, 2, 4)
	assertBrokerEchoHostileAccessDenied(t, pool, hostileRole, false, false)

	if err := migrateBrokerEchoCapacitySchema(t, pool); err != nil {
		t.Fatalf("apply capacity migration under hostile defaults: %v", err)
	}
	assertBrokerEchoCapacityFinalCatalog(t, pool)
	assertBrokerEchoRawACLAllowlist(t, pool, hostileRole, 3, 7)
	assertBrokerEchoHostileAccessDenied(t, pool, hostileRole, true, true)

	if err := migrateBrokerEchoIntegritySchema(t, pool); err != nil {
		t.Fatalf("apply integrity migration under hostile defaults: %v", err)
	}
	assertBrokerEchoIntegrityFinalCatalog(t, pool)
	assertBrokerEchoRawACLAllowlist(t, pool, hostileRole, 3, 8)
	assertBrokerEchoHostileAccessDenied(t, pool, hostileRole, true, true)

	if err := migrateBrokerEchoFinalGuardSchema(t, pool); err != nil {
		t.Fatalf("apply final guards under hostile defaults: %v", err)
	}
	assertBrokerEchoFinalGuardCatalog(t, pool)
	assertBrokerEchoRawACLAllowlist(t, pool, hostileRole, 3, 9)
	assertBrokerEchoHostileAccessDenied(t, pool, hostileRole, true, true)

	if err := restoreBrokerEchoHostileRole(
		ctx,
		pool,
		ownerIdentifier,
		hostileIdentifier,
	); err != nil {
		t.Fatalf("restore unlisted hostile role and owner defaults: %v", err)
	}
	restored = true
}

func TestBrokerEchoReplayMigrationRejectsInvalidLiveLegacyShapesAtomically(
	t *testing.T,
) {
	for _, test := range []struct {
		name   string
		scope  string
		status int
		body   string
	}{
		{
			name:   "non-200 status",
			status: 201,
			body:   `{"id":"019fa562-2c4f-4b7e-8db3-ec1fc8d53911"}`,
		},
		{
			name:   "non-object body",
			status: 200,
			body:   `["019fa562-2c4f-4b7e-8db3-ec1fc8d53912"]`,
		},
		{
			name:   "extra body field",
			status: 200,
			body:   `{"id":"019fa562-2c4f-4b7e-8db3-ec1fc8d53913","extra":true}`,
		},
		{
			name:   "non-string id",
			status: 200,
			body:   `{"id":17}`,
		},
		{
			name:   "non-canonical id",
			status: 200,
			body:   `{"id":"NOT-A-UUID"}`,
		},
		{
			name:   "empty principal suffix",
			scope:  "broker-echo\x1furn:xb:apikey:",
			status: 200,
			body:   `{"id":"019fa562-2c4f-4b7e-8db3-ec1fc8d53914"}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			pool := postgresPool(t)
			applyBrokerEchoPreviousSchema(t, pool)
			scope := test.scope
			if scope == "" {
				scope = "broker-echo\x1furn:xb:apikey:invalid-upgrade"
			}
			if _, err := pool.Exec(ctx, `
				INSERT INTO identity.idempotency_responses (
					scope,
					idempotency_key,
					request_hash,
					response_status,
					response_body,
					expires_at
				) VALUES (
					$1,
					'invalid-upgrade',
					decode(repeat('d4', 32), 'hex'),
					$2,
					$3::jsonb,
					transaction_timestamp() + interval '1 day'
				)`,
				scope,
				test.status,
				test.body,
			); err != nil {
				t.Fatalf("seed invalid live legacy replay: %v", err)
			}

			err := migrateBrokerEchoCurrentSchema(pool)
			requireBrokerEchoPostgresCode(t, err, "55000")
			assertBrokerEchoMigrationAbsent(t, pool)

			var legacyRows int
			if err := pool.QueryRow(ctx, `
				SELECT count(*)
				  FROM identity.idempotency_responses
				 WHERE idempotency_key = 'invalid-upgrade'`,
			).Scan(&legacyRows); err != nil {
				t.Fatal(err)
			}
			if legacyRows != 1 {
				t.Fatalf("refused upgrade changed legacy row count to %d", legacyRows)
			}
			if _, err := pool.Exec(
				ctx,
				"TRUNCATE identity.idempotency_responses",
			); err != nil {
				t.Fatalf("owner-reviewed reset of invalid row: %v", err)
			}
			applyBrokerEchoCurrentSchema(t, pool)
		})
	}
}

func TestBrokerEchoReplayMigrationUsesBoundedLockAndRetriesAtomically(
	t *testing.T,
) {
	ctx := context.Background()
	pool := postgresPool(t)
	applyBrokerEchoPreviousSchema(t, pool)

	lockingTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lockingTx.Rollback(context.Background()) }()
	if _, err := lockingTx.Exec(ctx, `
		LOCK TABLE identity.idempotency_responses IN ROW EXCLUSIVE MODE`); err != nil {
		t.Fatalf("hold legacy writer lock: %v", err)
	}

	startedAt := time.Now()
	err = migrateBrokerEchoCurrentSchema(pool)
	elapsed := time.Since(startedAt)
	requireBrokerEchoPostgresCode(t, err, "55P03")
	if elapsed < 4*time.Second || elapsed > 8*time.Second {
		t.Fatalf("bounded migration lock wait = %s, want approximately 5s", elapsed)
	}
	assertBrokerEchoMigrationAbsent(t, pool)

	if err := lockingTx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	applyBrokerEchoCurrentSchema(t, pool)
}

func TestBrokerEchoReplayMigrationEnforcesLiveRowBackfillBound(t *testing.T) {
	for _, test := range []struct {
		name           string
		rows           int
		wantCode       string
		wantBackfilled int
	}{
		{
			name:           "maximum",
			rows:           1000,
			wantBackfilled: 1000,
		},
		{
			name:     "maximum plus one",
			rows:     1001,
			wantCode: "55000",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			pool := postgresPool(t)
			applyBrokerEchoPreviousSchema(t, pool)
			if _, err := pool.Exec(ctx, `
				INSERT INTO identity.idempotency_responses (
					scope,
					idempotency_key,
					request_hash,
					response_status,
					response_body,
					created_at,
					expires_at
				)
				SELECT
					'broker-echo' || chr(31) ||
						'urn:xb:apikey:bounded-upgrade-' ||
						((candidate - 1) / 100)::text,
					'bounded-' || candidate::text,
					decode(repeat('b7', 32), 'hex'),
					200,
					'{"id":"019fa562-2c4f-4b7e-8db3-ec1fc8d53941"}',
					transaction_timestamp() - interval '1 hour',
					transaction_timestamp() + interval '23 hours'
				  FROM generate_series(1, $1::integer) AS candidate`,
				test.rows,
			); err != nil {
				t.Fatalf("seed %d bounded legacy rows: %v", test.rows, err)
			}

			err := migrateBrokerEchoCurrentSchema(pool)
			if test.wantCode != "" {
				requireBrokerEchoPostgresCode(t, err, test.wantCode)
				assertBrokerEchoMigrationAbsent(t, pool)
				var legacyRows int
				if countErr := pool.QueryRow(ctx, `
					SELECT count(*)
					  FROM identity.idempotency_responses`,
				).Scan(&legacyRows); countErr != nil {
					t.Fatal(countErr)
				}
				if legacyRows != test.rows {
					t.Fatalf(
						"refused bounded upgrade changed legacy rows to %d, want %d",
						legacyRows,
						test.rows,
					)
				}
				return
			}
			if err != nil {
				t.Fatalf("upgrade at reviewed row bound: %v", err)
			}
			var backfilled int
			if err := pool.QueryRow(ctx, `
				SELECT count(*) FROM identity.broker_echo_replays`,
			).Scan(&backfilled); err != nil {
				t.Fatal(err)
			}
			if backfilled != test.wantBackfilled {
				t.Fatalf(
					"backfilled rows = %d, want %d",
					backfilled,
					test.wantBackfilled,
				)
			}
		})
	}
}

func TestBrokerEchoReplayMigrationEnforcesPerPrincipalBackfillBound(
	t *testing.T,
) {
	for _, test := range []struct {
		name           string
		rows           int
		wantCode       string
		wantBackfilled int
	}{
		{
			name:           "maximum",
			rows:           100,
			wantBackfilled: 100,
		},
		{
			name:     "maximum plus one",
			rows:     101,
			wantCode: "55000",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			pool := postgresPool(t)
			applyBrokerEchoPreviousSchema(t, pool)
			if _, err := pool.Exec(ctx, `
				INSERT INTO identity.idempotency_responses (
					scope,
					idempotency_key,
					request_hash,
					response_status,
					response_body,
					created_at,
					expires_at
				)
				SELECT
					'broker-echo' || chr(31) ||
						'urn:xb:apikey:principal-bound',
					'principal-bound-' || candidate::text,
					decode(repeat('b8', 32), 'hex'),
					200,
					'{"id":"019fa562-2c4f-4b7e-8db3-ec1fc8d53942"}',
					transaction_timestamp() - interval '1 hour',
					transaction_timestamp() + interval '23 hours'
				  FROM generate_series(1, $1::integer) AS candidate`,
				test.rows,
			); err != nil {
				t.Fatalf(
					"seed %d per-principal legacy rows: %v",
					test.rows,
					err,
				)
			}

			err := migrateBrokerEchoCurrentSchema(pool)
			if test.wantCode != "" {
				requireBrokerEchoPostgresCode(t, err, test.wantCode)
				assertBrokerEchoCapacityIntermediateTip(t, pool)
				var (
					legacyRows     int
					backfilledRows int
				)
				if countErr := pool.QueryRow(ctx, `
					SELECT
						(SELECT count(*)
						   FROM identity.idempotency_responses),
						(SELECT count(*)
						   FROM identity.broker_echo_replays)`,
				).Scan(
					&legacyRows,
					&backfilledRows,
				); countErr != nil {
					t.Fatal(countErr)
				}
				if legacyRows != test.rows || backfilledRows != test.rows {
					t.Fatalf(
						"refused principal-bound upgrade rows legacy=%d backfilled=%d, want %d each",
						legacyRows,
						backfilledRows,
						test.rows,
					)
				}
				return
			}
			if err != nil {
				t.Fatalf("upgrade at principal row bound: %v", err)
			}
			var backfilled int
			if err := pool.QueryRow(ctx, `
				SELECT count(*) FROM identity.broker_echo_replays`,
			).Scan(&backfilled); err != nil {
				t.Fatal(err)
			}
			if backfilled != test.wantBackfilled {
				t.Fatalf(
					"backfilled rows = %d, want %d",
					backfilled,
					test.wantBackfilled,
				)
			}
		})
	}
}

func assertBrokerEchoCapacityIntermediateTip(
	t *testing.T,
	pool *pgxpool.Pool,
) {
	t.Helper()
	assertBrokerEchoCapacityIntermediateCatalog(t, pool, false)
}

func assertBrokerEchoCapacityIntermediateCatalog(
	t *testing.T,
	pool *pgxpool.Pool,
	expectCoverageCollision bool,
) {
	t.Helper()
	var (
		replayApplied   bool
		capacityApplied bool
		policyExists    bool
		policyGuard     bool
		validatorExists bool
		coverageExists  bool
	)
	if err := pool.QueryRow(context.Background(), `
		SELECT
			EXISTS (
				SELECT 1
				  FROM engine.schema_migrations
				 WHERE filename = $1
			),
			EXISTS (
				SELECT 1
				  FROM engine.schema_migrations
				 WHERE filename = $2
			),
			to_regclass('identity.broker_echo_replay_policy') IS NOT NULL,
			to_regprocedure(
				'identity.guard_broker_echo_replay_policy()'
			) IS NOT NULL,
			to_regprocedure(
				'identity.valid_broker_echo_response(bytea,integer,jsonb,bytea,timestamptz,timestamptz)'
			) IS NOT NULL,
			to_regprocedure(
				'identity.broker_echo_replay_coverage()'
			) IS NOT NULL`,
		brokerEchoReplayMigration,
		brokerEchoCapacityMigration,
	).Scan(
		&replayApplied,
		&capacityApplied,
		&policyExists,
		&policyGuard,
		&validatorExists,
		&coverageExists,
	); err != nil {
		t.Fatal(err)
	}
	if !replayApplied || capacityApplied || policyExists || policyGuard ||
		validatorExists || coverageExists != expectCoverageCollision {
		t.Fatalf(
			"capacity intermediate tip replay=%t capacity=%t policy=%t "+
				"policy_guard=%t validator=%t coverage=%t",
			replayApplied,
			capacityApplied,
			policyExists,
			policyGuard,
			validatorExists,
			coverageExists,
		)
	}

	assertBrokerEchoClaimResult(
		t,
		pool,
		[]string{"response_status", "response_headers", "response_body"},
		[]string{"integer", "jsonb", "bytea"},
	)
	assertBrokerEchoFunctionAuthority(
		t,
		pool,
		"identity.claim_broker_echo_response(text,bytea,bytea,integer,jsonb,bytea)",
		true,
		[]string{"search_path=pg_catalog", "lock_timeout=5s"},
		map[string]bool{"platformgo_api": true},
	)
	assertBrokerEchoFunctionAuthority(
		t,
		pool,
		"identity.purge_expired_broker_echo_replays(integer)",
		true,
		[]string{"search_path=pg_catalog", "lock_timeout=5s"},
		map[string]bool{"platformgo_api": true},
	)
	assertBrokerEchoPurgeDefinition(t, pool, false)

	if expectCoverageCollision {
		var (
			result string
			source string
		)
		if err := pool.QueryRow(context.Background(), `
			SELECT
				pg_get_function_result(oid),
				prosrc
			  FROM pg_proc
			 WHERE oid =
			       'identity.broker_echo_replay_coverage()'::regprocedure`,
		).Scan(&result, &source); err != nil {
			t.Fatalf("inspect preserved coverage collision: %v", err)
		}
		if result != "integer" ||
			strings.Join(strings.Fields(source), " ") != "SELECT 7" {
			t.Fatalf(
				"preserved coverage collision result=%q source=%q",
				result,
				source,
			)
		}
	}
}

func TestBrokerEchoCapacityMigrationRetriesFromIntermediateTipAfterExpiry(
	t *testing.T,
) {
	ctx := context.Background()
	pool := postgresPool(t)
	applyBrokerEchoPreviousSchema(t, pool)
	if err := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, brokerEchoReplayMigration),
	).Migrate(ctx); err != nil {
		t.Fatalf("apply exact-replay intermediate tip: %v", err)
	}
	if _, err := pool.Exec(ctx, `
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
				'urn:xb:apikey:retry-after-expiry',
			sha256(convert_to('retry-' || candidate::text, 'UTF8')),
			decode(repeat('d8', 32), 'hex'),
			200,
			'{"Content-Type":["application/json"]}'::jsonb,
			convert_to(
				'{"id":"019fa562-2c4f-4b7e-8db3-' ||
				lpad(candidate::text, 12, '0') || '"}' || chr(10),
				'UTF8'
			),
			statement_timestamp() - interval '23 hours',
			statement_timestamp() + interval '3 seconds'
		  FROM generate_series(1, 101) AS candidate`); err != nil {
		t.Fatalf("seed intermediate-tip capacity blocker: %v", err)
	}

	err := migrateBrokerEchoCapacitySchema(t, pool)
	requireBrokerEchoPostgresCode(t, err, "55000")
	assertBrokerEchoCapacityIntermediateTip(t, pool)
	var preservedRows int
	if countErr := pool.QueryRow(ctx, `
		SELECT count(*) FROM identity.broker_echo_replays`,
	).Scan(&preservedRows); countErr != nil {
		t.Fatal(countErr)
	}
	if preservedRows != 101 {
		t.Fatalf("failed capacity migration preserved %d rows, want 101", preservedRows)
	}

	if _, err := pool.Exec(ctx, "SELECT pg_sleep(3.1)"); err != nil {
		t.Fatalf("wait for PostgreSQL-authoritative expiry: %v", err)
	}
	if err := migrateBrokerEchoCapacitySchema(t, pool); err != nil {
		t.Fatalf("retry broker-echo capacity migration: %v", err)
	}
	var (
		remainingRows   int
		capacityApplied bool
	)
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM identity.broker_echo_replays),
			EXISTS (
				SELECT 1
				  FROM engine.schema_migrations
				 WHERE filename = $1
			)`,
		brokerEchoCapacityMigration,
	).Scan(&remainingRows, &capacityApplied); err != nil {
		t.Fatal(err)
	}
	if remainingRows != 0 || !capacityApplied {
		t.Fatalf(
			"capacity retry remaining=%d applied=%t, want 0/true",
			remainingRows,
			capacityApplied,
		)
	}
}

func TestBrokerEchoCapacityMigrationRejectsCorruptIntermediateAuthority(
	t *testing.T,
) {
	ctx := context.Background()
	pool := postgresPool(t)
	applyBrokerEchoPreviousSchema(t, pool)
	if err := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, brokerEchoReplayMigration),
	).Migrate(ctx); err != nil {
		t.Fatalf("apply exact-replay intermediate tip: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO identity.broker_echo_replays (
			scope,
			idempotency_key_hash,
			request_hash,
			response_status,
			response_headers,
			response_body,
			created_at,
			expires_at
		) VALUES (
			'broker-echo' || chr(31) ||
				'urn:xb:apikey:corrupt-intermediate',
			decode(repeat('e1', 32), 'hex'),
			decode(repeat('e2', 32), 'hex'),
			200,
			'{"Content-Type":["application/json"]}',
			convert_to('{}', 'UTF8'),
			statement_timestamp(),
			statement_timestamp() + interval '24 hours'
		)`); err != nil {
		t.Fatalf("seed corrupt intermediate authority: %v", err)
	}

	err := migrateBrokerEchoCapacitySchema(t, pool)
	requireBrokerEchoPostgresCode(t, err, "55000")
	assertBrokerEchoCapacityIntermediateTip(t, pool)
	var (
		status int
		body   []byte
	)
	if err := pool.QueryRow(ctx, `
		SELECT response_status, response_body
		  FROM identity.broker_echo_replays
		 WHERE scope = 'broker-echo' || chr(31) ||
		       'urn:xb:apikey:corrupt-intermediate'`,
	).Scan(&status, &body); err != nil {
		t.Fatal(err)
	}
	if status != 200 || string(body) != "{}" {
		t.Fatalf("failed capacity migration changed status=%d body=%q", status, body)
	}
}

func TestBrokerEchoCapacityMigrationRollsBackAfterClaimReplacementAndRetries(
	t *testing.T,
) {
	ctx := context.Background()
	pool := postgresPool(t)
	applyBrokerEchoPreviousSchema(t, pool)
	if err := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, brokerEchoReplayMigration),
	).Migrate(ctx); err != nil {
		t.Fatalf("apply exact-replay intermediate tip: %v", err)
	}
	const (
		scope = "broker-echo\x1furn:xb:apikey:coverage-collision"
		id    = "019fa562-2c4f-4b7e-8db3-ec1fc8d53981"
	)
	body, createdAt, expiresAt := seedBrokerEchoIntermediateReplay(
		t,
		pool,
		scope,
		id,
	)
	if _, err := pool.Exec(ctx, `
		CREATE FUNCTION identity.broker_echo_replay_coverage()
		RETURNS integer
		LANGUAGE sql
		SET search_path = pg_catalog
		AS 'SELECT 7'`); err != nil {
		t.Fatalf("install deterministic post-claim collision: %v", err)
	}

	err := migrateBrokerEchoCapacitySchema(t, pool)
	requireBrokerEchoPostgresCode(t, err, "42723")
	assertBrokerEchoCapacityIntermediateCatalog(t, pool, true)
	assertBrokerEchoIntermediateReplayPreserved(
		t,
		pool,
		scope,
		body,
		createdAt,
		expiresAt,
	)

	if _, err := pool.Exec(
		ctx,
		"DROP FUNCTION identity.broker_echo_replay_coverage()",
	); err != nil {
		t.Fatalf("remove deterministic post-claim collision: %v", err)
	}
	if err := migrateBrokerEchoCapacitySchema(t, pool); err != nil {
		t.Fatalf("retry broker-echo capacity migration: %v", err)
	}
	assertBrokerEchoCapacityFinalCatalog(t, pool)
	assertBrokerEchoIntermediateReplayPreserved(
		t,
		pool,
		scope,
		body,
		createdAt,
		expiresAt,
	)
}

func TestBrokerEchoCapacityMigrationUsesBoundedReplayLockAndRetriesAtomically(
	t *testing.T,
) {
	ctx := context.Background()
	pool := postgresPool(t)
	applyBrokerEchoPreviousSchema(t, pool)
	if err := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, brokerEchoReplayMigration),
	).Migrate(ctx); err != nil {
		t.Fatalf("apply exact-replay intermediate tip: %v", err)
	}
	const (
		scope = "broker-echo\x1furn:xb:apikey:capacity-lock"
		id    = "019fa562-2c4f-4b7e-8db3-ec1fc8d53982"
	)
	body, createdAt, expiresAt := seedBrokerEchoIntermediateReplay(
		t,
		pool,
		scope,
		id,
	)

	lockingTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lockingTx.Rollback(context.Background()) }()
	if _, err := lockingTx.Exec(ctx, `
		LOCK TABLE identity.broker_echo_replays IN ROW EXCLUSIVE MODE`); err != nil {
		t.Fatalf("hold broker-echo replay writer lock: %v", err)
	}

	startedAt := time.Now()
	err = migrateBrokerEchoCapacitySchema(t, pool)
	elapsed := time.Since(startedAt)
	requireBrokerEchoPostgresCode(t, err, "55P03")
	if elapsed < 4*time.Second || elapsed > 8*time.Second {
		t.Fatalf("bounded capacity migration lock wait = %s, want approximately 5s", elapsed)
	}
	assertBrokerEchoCapacityIntermediateCatalog(t, pool, false)
	assertBrokerEchoIntermediateReplayPreserved(
		t,
		pool,
		scope,
		body,
		createdAt,
		expiresAt,
	)

	if err := lockingTx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := migrateBrokerEchoCapacitySchema(t, pool); err != nil {
		t.Fatalf("retry broker-echo capacity migration: %v", err)
	}
	assertBrokerEchoCapacityFinalCatalog(t, pool)
	assertBrokerEchoIntermediateReplayPreserved(
		t,
		pool,
		scope,
		body,
		createdAt,
		expiresAt,
	)
}

func TestBrokerEchoIntegrityMigrationRejectsInvalidIntermediateAuthorityAtomically(
	t *testing.T,
) {
	for _, test := range []struct {
		name     string
		scope    string
		body     []byte
		lifetime string
	}{
		{
			name:     "malformed response",
			scope:    "broker-echo\x1furn:xb:apikey:integrity-malformed",
			body:     []byte("x"),
			lifetime: "24 hours",
		},
		{
			name:  "overlong lifetime",
			scope: "broker-echo\x1furn:xb:apikey:integrity-overlong",
			body: []byte(
				"{\"id\":\"019fa562-2c4f-4b7e-8db3-ec1fc8d53984\"}\n",
			),
			lifetime: "26 hours",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			pool := postgresPool(t)
			applyBrokerEchoPreviousSchema(t, pool)
			if err := migrateBrokerEchoCapacitySchema(t, pool); err != nil {
				t.Fatalf("apply broker-echo capacity tip: %v", err)
			}
			body, createdAt, expiresAt := seedBrokerEchoIntermediateReplayShape(
				t,
				pool,
				test.scope,
				test.body,
				test.lifetime,
			)

			err := migrateBrokerEchoIntegritySchema(t, pool)
			requireBrokerEchoPostgresCode(t, err, "55000")
			assertBrokerEchoCapacityFinalCatalog(t, pool)
			assertBrokerEchoIntermediateReplayPreserved(
				t,
				pool,
				test.scope,
				body,
				createdAt,
				expiresAt,
			)

			if _, err := pool.Exec(
				ctx,
				"TRUNCATE identity.broker_echo_replays",
			); err != nil {
				t.Fatalf("remove rejected integrity blocker: %v", err)
			}
			if err := migrateBrokerEchoIntegritySchema(t, pool); err != nil {
				t.Fatalf("retry broker-echo integrity migration: %v", err)
			}
			assertBrokerEchoIntegrityFinalCatalog(t, pool)
		})
	}
}

func TestBrokerEchoIntegrityMigrationUsesBoundedReplayLockAndRetriesAtomically(
	t *testing.T,
) {
	ctx := context.Background()
	pool := postgresPool(t)
	applyBrokerEchoPreviousSchema(t, pool)
	if err := migrateBrokerEchoCapacitySchema(t, pool); err != nil {
		t.Fatalf("apply broker-echo capacity tip: %v", err)
	}
	const (
		scope = "broker-echo\x1furn:xb:apikey:integrity-lock"
		id    = "019fa562-2c4f-4b7e-8db3-ec1fc8d53985"
	)
	body, createdAt, expiresAt := seedBrokerEchoIntermediateReplay(
		t,
		pool,
		scope,
		id,
	)

	lockingTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lockingTx.Rollback(context.Background()) }()
	if _, err := lockingTx.Exec(ctx, `
		LOCK TABLE identity.broker_echo_replays IN ACCESS SHARE MODE`); err != nil {
		t.Fatalf("hold broker-echo replay reader lock for integrity migration: %v", err)
	}
	journalBefore := brokerEchoMigrationJournalSnapshot(t, pool)
	replaysBefore := brokerEchoReplayRowsSnapshot(t, pool, false)

	startedAt := time.Now()
	err = migrateBrokerEchoIntegritySchema(t, pool)
	elapsed := time.Since(startedAt)
	requireBrokerEchoPostgresCode(t, err, "55P03")
	if elapsed < 4*time.Second || elapsed > 8*time.Second {
		t.Fatalf("bounded integrity migration lock wait = %s, want approximately 5s", elapsed)
	}
	assertBrokerEchoCapacityFinalCatalog(t, pool)
	assertBrokerEchoSnapshotsEqual(
		t,
		"002 journal after bounded 003 lock refusal",
		journalBefore,
		brokerEchoMigrationJournalSnapshot(t, pool),
	)
	assertBrokerEchoSnapshotsEqual(
		t,
		"002 replay rows after bounded 003 lock refusal",
		replaysBefore,
		brokerEchoReplayRowsSnapshot(t, pool, false),
	)
	assertBrokerEchoIntermediateReplayPreserved(
		t,
		pool,
		scope,
		body,
		createdAt,
		expiresAt,
	)

	if err := lockingTx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := migrateBrokerEchoIntegritySchema(t, pool); err != nil {
		t.Fatalf("retry broker-echo integrity migration: %v", err)
	}
	assertBrokerEchoIntegrityFinalCatalog(t, pool)
	var finalGuardApplied bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			  FROM engine.schema_migrations
			 WHERE filename = $1
		)`,
		brokerEchoFinalGuardMigration,
	).Scan(&finalGuardApplied); err != nil {
		t.Fatalf("inspect exact integrity-tip journal: %v", err)
	}
	if finalGuardApplied {
		t.Fatal("integrity-only retry advanced unexpectedly through final guard")
	}
	assertBrokerEchoIntermediateReplayPreserved(
		t,
		pool,
		scope,
		body,
		createdAt,
		expiresAt,
	)
}

func TestBrokerEchoIntegrityMigrationRollsBackPostDropCoverageCollision(
	t *testing.T,
) {
	ctx := context.Background()
	pool := postgresPool(t)
	applyBrokerEchoPreviousSchema(t, pool)
	if err := migrateBrokerEchoCapacitySchema(t, pool); err != nil {
		t.Fatalf("apply broker-echo capacity tip: %v", err)
	}
	const (
		scope = "broker-echo\x1furn:xb:apikey:integrity-collision"
		id    = "019fa562-2c4f-4b7e-8db3-ec1fc8d53986"
	)
	body, createdAt, expiresAt := seedBrokerEchoIntermediateReplay(
		t,
		pool,
		scope,
		id,
	)

	files := migrationFilesThrough(t, brokerEchoIntegrityMigration)
	original := string(files[brokerEchoIntegrityMigration].Data)
	const collisionPoint = `DROP FUNCTION identity.broker_echo_replay_coverage();

CREATE FUNCTION identity.broker_echo_replay_coverage()`
	const injectedCollision = `DROP FUNCTION identity.broker_echo_replay_coverage();

CREATE FUNCTION identity.broker_echo_replay_coverage()
RETURNS integer
LANGUAGE sql
SET search_path = pg_catalog
AS 'SELECT 7';

CREATE FUNCTION identity.broker_echo_replay_coverage()`
	injected := strings.Replace(
		original,
		collisionPoint,
		injectedCollision,
		1,
	)
	if injected == original {
		t.Fatal("coverage recreation collision point was not found")
	}
	files[brokerEchoIntegrityMigration].Data = []byte(injected)

	err := platformpostgres.NewMigrator(pool, files).Migrate(ctx)
	requireBrokerEchoPostgresCode(t, err, "42723")
	assertBrokerEchoCapacityFinalCatalog(t, pool)
	assertBrokerEchoIntermediateReplayPreserved(
		t,
		pool,
		scope,
		body,
		createdAt,
		expiresAt,
	)

	if err := migrateBrokerEchoIntegritySchema(t, pool); err != nil {
		t.Fatalf("retry broker-echo integrity migration: %v", err)
	}
	assertBrokerEchoIntegrityFinalCatalog(t, pool)
	assertBrokerEchoIntermediateReplayPreserved(
		t,
		pool,
		scope,
		body,
		createdAt,
		expiresAt,
	)
}

func TestBrokerEchoFinalGuardMigrationUsesBoundedReplayLockAndRetriesAtomically(
	t *testing.T,
) {
	ctx := context.Background()
	pool := postgresPool(t)
	applyBrokerEchoPreviousSchema(t, pool)
	if err := migrateBrokerEchoIntegritySchema(t, pool); err != nil {
		t.Fatalf("apply exact broker-echo integrity tip: %v", err)
	}
	const (
		scope = "broker-echo\x1furn:xb:apikey:final-guard-lock"
		id    = "019fa562-2c4f-4b7e-8db3-ec1fc8d53987"
	)
	body, createdAt, expiresAt := seedBrokerEchoIntermediateReplay(
		t,
		pool,
		scope,
		id,
	)
	journalBefore := brokerEchoMigrationJournalSnapshot(t, pool)
	replaysBefore := brokerEchoReplayRowsSnapshot(t, pool, true)

	lockingTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lockingTx.Rollback(context.Background()) }()
	if _, err := lockingTx.Exec(ctx, `
		LOCK TABLE identity.broker_echo_replays IN ACCESS SHARE MODE`); err != nil {
		t.Fatalf("hold broker-echo reader lock for final guard migration: %v", err)
	}

	startedAt := time.Now()
	err = migrateBrokerEchoFinalGuardSchema(t, pool)
	elapsed := time.Since(startedAt)
	requireBrokerEchoPostgresCode(t, err, "55P03")
	if elapsed < 4*time.Second || elapsed > 8*time.Second {
		t.Fatalf(
			"bounded final guard migration lock wait = %s, want approximately 5s",
			elapsed,
		)
	}
	assertBrokerEchoIntegrityFinalCatalog(t, pool)
	assertBrokerEchoSnapshotsEqual(
		t,
		"003 journal after bounded 004 lock refusal",
		journalBefore,
		brokerEchoMigrationJournalSnapshot(t, pool),
	)
	assertBrokerEchoSnapshotsEqual(
		t,
		"003 replay rows after bounded 004 lock refusal",
		replaysBefore,
		brokerEchoReplayRowsSnapshot(t, pool, true),
	)
	assertBrokerEchoIntermediateReplayPreserved(
		t,
		pool,
		scope,
		body,
		createdAt,
		expiresAt,
	)

	if err := lockingTx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := migrateBrokerEchoFinalGuardSchema(t, pool); err != nil {
		t.Fatalf("retry broker-echo final guard migration: %v", err)
	}
	assertBrokerEchoFinalGuardCatalog(t, pool)
	assertBrokerEchoSnapshotsEqual(
		t,
		"final guard retry replay rows",
		replaysBefore,
		brokerEchoReplayRowsSnapshot(t, pool, true),
	)
}

func TestBrokerEchoFinalGuardMigrationRejectsDivergentInsertFenceAtomically(
	t *testing.T,
) {
	ctx := context.Background()
	pool := postgresPool(t)
	applyBrokerEchoPreviousSchema(t, pool)
	if err := migrateBrokerEchoIntegritySchema(t, pool); err != nil {
		t.Fatalf("apply exact broker-echo integrity tip: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		CREATE OR REPLACE FUNCTION identity.guard_broker_echo_replay_insert()
		RETURNS trigger
		LANGUAGE plpgsql
		SET search_path = pg_catalog
		AS $$
		BEGIN
			RETURN NEW;
		END;
		$$`); err != nil {
		t.Fatalf("replace insert fence with divergent no-op body: %v", err)
	}
	journalBefore := brokerEchoMigrationJournalSnapshot(t, pool)
	replaysBefore := brokerEchoReplayRowsSnapshot(t, pool, true)
	catalogBefore := brokerEchoInsertGuardCatalogSnapshot(t, pool)

	err := migrateBrokerEchoFinalGuardSchema(t, pool)
	requireBrokerEchoPostgresCode(t, err, "55000")
	assertBrokerEchoSnapshotsEqual(
		t,
		"003 journal after divergent insert fence refusal",
		journalBefore,
		brokerEchoMigrationJournalSnapshot(t, pool),
	)
	assertBrokerEchoSnapshotsEqual(
		t,
		"003 replay rows after divergent insert fence refusal",
		replaysBefore,
		brokerEchoReplayRowsSnapshot(t, pool, true),
	)
	assertBrokerEchoSnapshotsEqual(
		t,
		"003 insert fence catalog after divergent fence refusal",
		catalogBefore,
		brokerEchoInsertGuardCatalogSnapshot(t, pool),
	)
	var finalGuardApplied bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			  FROM engine.schema_migrations
			 WHERE filename = $1
		)`,
		brokerEchoFinalGuardMigration,
	).Scan(&finalGuardApplied); err != nil {
		t.Fatalf("inspect final guard journal after divergent fence: %v", err)
	}
	if finalGuardApplied {
		t.Fatal("divergent insert fence advanced unexpectedly through final guard")
	}
}

func TestBrokerEchoFinalGuardMigrationRejectsConditionalInsertFenceAtomically(
	t *testing.T,
) {
	ctx := context.Background()
	pool := postgresPool(t)
	applyBrokerEchoPreviousSchema(t, pool)
	if err := migrateBrokerEchoIntegritySchema(t, pool); err != nil {
		t.Fatalf("apply exact broker-echo integrity tip: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		DROP TRIGGER broker_echo_replays_require_postgres_time_authority
		ON identity.broker_echo_replays;

		CREATE TRIGGER broker_echo_replays_require_postgres_time_authority
		BEFORE INSERT ON identity.broker_echo_replays
		FOR EACH ROW
		WHEN (NEW.postgres_time_authority)
		EXECUTE FUNCTION identity.guard_broker_echo_replay_insert()`); err != nil {
		t.Fatalf("replace insert fence with conditional trigger: %v", err)
	}
	journalBefore := brokerEchoMigrationJournalSnapshot(t, pool)
	replaysBefore := brokerEchoReplayRowsSnapshot(t, pool, true)
	catalogBefore := brokerEchoInsertGuardCatalogSnapshot(t, pool)

	err := migrateBrokerEchoFinalGuardSchema(t, pool)
	requireBrokerEchoPostgresCode(t, err, "55000")
	assertBrokerEchoSnapshotsEqual(
		t,
		"003 journal after conditional insert fence refusal",
		journalBefore,
		brokerEchoMigrationJournalSnapshot(t, pool),
	)
	assertBrokerEchoSnapshotsEqual(
		t,
		"003 replay rows after conditional insert fence refusal",
		replaysBefore,
		brokerEchoReplayRowsSnapshot(t, pool, true),
	)
	assertBrokerEchoSnapshotsEqual(
		t,
		"003 insert fence catalog after conditional fence refusal",
		catalogBefore,
		brokerEchoInsertGuardCatalogSnapshot(t, pool),
	)
	var finalGuardApplied bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			  FROM engine.schema_migrations
			 WHERE filename = $1
		)`,
		brokerEchoFinalGuardMigration,
	).Scan(&finalGuardApplied); err != nil {
		t.Fatalf("inspect final guard journal after conditional fence: %v", err)
	}
	if finalGuardApplied {
		t.Fatal("conditional insert fence advanced unexpectedly through final guard")
	}
}

func TestBrokerEchoFinalGuardMigrationRejectsInsertSidecarAtomically(
	t *testing.T,
) {
	ctx := context.Background()
	pool := postgresPool(t)
	applyBrokerEchoPreviousSchema(t, pool)
	if err := migrateBrokerEchoIntegritySchema(t, pool); err != nil {
		t.Fatalf("apply exact broker-echo integrity tip: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		CREATE FUNCTION identity.zzz_broker_echo_force_legacy()
		RETURNS trigger
		LANGUAGE plpgsql
		SET search_path = pg_catalog
		AS $$
		BEGIN
			NEW.postgres_time_authority := false;
			RETURN NEW;
		END;
		$$;

		CREATE TRIGGER zzz_broker_echo_force_legacy
		BEFORE INSERT ON identity.broker_echo_replays
		FOR EACH ROW
		EXECUTE FUNCTION identity.zzz_broker_echo_force_legacy()`); err != nil {
		t.Fatalf("install marker-changing insert sidecar: %v", err)
	}
	journalBefore := brokerEchoMigrationJournalSnapshot(t, pool)
	replaysBefore := brokerEchoReplayRowsSnapshot(t, pool, true)
	catalogBefore := brokerEchoReplayTriggerCatalogSnapshot(t, pool)

	err := migrateBrokerEchoFinalGuardSchema(t, pool)
	requireBrokerEchoPostgresCode(t, err, "55000")
	assertBrokerEchoSnapshotsEqual(
		t,
		"003 journal after insert sidecar refusal",
		journalBefore,
		brokerEchoMigrationJournalSnapshot(t, pool),
	)
	assertBrokerEchoSnapshotsEqual(
		t,
		"003 replay rows after insert sidecar refusal",
		replaysBefore,
		brokerEchoReplayRowsSnapshot(t, pool, true),
	)
	assertBrokerEchoSnapshotsEqual(
		t,
		"003 trigger catalog after insert sidecar refusal",
		catalogBefore,
		brokerEchoReplayTriggerCatalogSnapshot(t, pool),
	)
	var finalGuardApplied bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			  FROM engine.schema_migrations
			 WHERE filename = $1
		)`,
		brokerEchoFinalGuardMigration,
	).Scan(&finalGuardApplied); err != nil {
		t.Fatalf("inspect final guard journal after insert sidecar: %v", err)
	}
	if finalGuardApplied {
		t.Fatal("insert sidecar advanced unexpectedly through final guard")
	}
}

func TestBrokerEchoReplayPurgeIsBoundedAndCannotDeleteLiveResponse(
	t *testing.T,
) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	applyBrokerEchoCurrentSchema(t, pool)

	for i := 1; i <= 3; i++ {
		if _, err := pool.Exec(ctx, `
			INSERT INTO identity.broker_echo_replays (
				scope,
				idempotency_key_hash,
				request_hash,
				response_status,
				response_headers,
				response_body,
				created_at,
				expires_at
			) VALUES (
				'broker-echo' || chr(31) || 'urn:xb:apikey:purge',
				sha256(convert_to($1::text, 'UTF8')),
				decode(repeat('e5', 32), 'hex'),
				200,
				'{"Content-Type":["application/json"]}',
				convert_to(
					'{"id":"019fa562-2c4f-4b7e-8db3-ec1fc8d539' ||
					lpad($1::text, 2, '0') || '"}' || chr(10),
					'UTF8'
				),
				transaction_timestamp() - interval '2 days',
				transaction_timestamp() - interval '1 day'
			)`,
			strconv.Itoa(i),
		); err != nil {
			t.Fatalf("seed expired replay %d: %v", i, err)
		}
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO identity.broker_echo_replays (
			scope,
			idempotency_key_hash,
			request_hash,
			response_status,
			response_headers,
			response_body,
			created_at,
			expires_at
		) VALUES (
			'broker-echo' || chr(31) || 'urn:xb:apikey:purge',
			sha256(convert_to('live', 'UTF8')),
			decode(repeat('f6', 32), 'hex'),
			200,
			'{"Content-Type":["application/json"]}',
			convert_to(
				'{"id":"019fa562-2c4f-4b7e-8db3-ec1fc8d53904"}' || chr(10),
				'UTF8'
			),
			transaction_timestamp() - interval '1 hour',
			transaction_timestamp() + interval '23 hours'
	)`); err != nil {
		t.Fatalf("seed live replay: %v", err)
	}
	journalBeforeTruncate := brokerEchoMigrationJournalSnapshot(t, pool)
	replaysBeforeTruncate := brokerEchoReplayRowsSnapshot(t, pool, true)

	t.Run("owner cannot truncate replay authority", func(t *testing.T) {
		if _, err := pool.Exec(
			ctx,
			"TRUNCATE identity.broker_echo_replays",
		); err == nil {
			t.Fatal("owner truncated exact replay authority")
		} else {
			requireBrokerEchoPostgresCode(t, err, "55000")
		}
		assertBrokerEchoSnapshotsEqual(
			t,
			"final replay rows after rejected truncate",
			replaysBeforeTruncate,
			brokerEchoReplayRowsSnapshot(t, pool, true),
		)
		assertBrokerEchoSnapshotsEqual(
			t,
			"migration journal after rejected replay truncate",
			journalBeforeTruncate,
			brokerEchoMigrationJournalSnapshot(t, pool),
		)
	})

	t.Run("owner cannot delete live replay", func(t *testing.T) {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(context.Background()) }()
		if _, err := tx.Exec(ctx, `
			DELETE FROM identity.broker_echo_replays
			 WHERE idempotency_key_hash = sha256(convert_to('live', 'UTF8'))`,
		); err == nil {
			t.Fatal("owner direct delete removed a live exact replay")
		} else {
			requireBrokerEchoPostgresCode(t, err, "55000")
		}
	})
	t.Run("owner cannot update replay", func(t *testing.T) {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(context.Background()) }()
		if _, err := tx.Exec(ctx, `
			UPDATE identity.broker_echo_replays
			   SET response_body = convert_to('{}', 'UTF8')
			 WHERE idempotency_key_hash = sha256(convert_to('live', 'UTF8'))`,
		); err == nil {
			t.Fatal("owner direct update changed a live exact replay")
		} else {
			requireBrokerEchoPostgresCode(t, err, "55000")
		}
	})

	api := runtimeRoleLoginPool(
		t,
		pool,
		"platformgo_api_broker_echo_purge_test",
		"platformgo_api",
	)
	if _, err := api.Exec(ctx, "SELECT * FROM identity.broker_echo_replays"); err == nil {
		t.Fatal("API role read replay table directly")
	} else {
		requireBrokerEchoPostgresCode(t, err, "42501")
	}
	if _, err := api.Exec(ctx, "DELETE FROM identity.broker_echo_replays"); err == nil {
		t.Fatal("API role deleted replay rows directly")
	} else {
		requireBrokerEchoPostgresCode(t, err, "42501")
	}
	if _, err := api.Exec(
		ctx,
		"SELECT * FROM identity.broker_echo_replay_policy",
	); err == nil {
		t.Fatal("API role read immutable replay policy directly")
	} else {
		requireBrokerEchoPostgresCode(t, err, "42501")
	}

	for _, mutation := range []struct {
		name string
		sql  string
	}{
		{
			name: "owner cannot update immutable policy",
			sql: `UPDATE identity.broker_echo_replay_policy
			         SET max_total_rows = max_total_rows`,
		},
		{
			name: "owner cannot delete immutable policy",
			sql:  `DELETE FROM identity.broker_echo_replay_policy`,
		},
		{
			name: "owner cannot truncate immutable policy",
			sql:  `TRUNCATE identity.broker_echo_replay_policy`,
		},
	} {
		t.Run(mutation.name, func(t *testing.T) {
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(context.Background()) }()
			if _, err := tx.Exec(ctx, mutation.sql); err == nil {
				t.Fatal("immutable replay policy mutation succeeded")
			} else {
				requireBrokerEchoPostgresCode(t, err, "55000")
			}
		})
	}

	t.Run("NULL purge limit is invalid", func(t *testing.T) {
		tx, err := api.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(context.Background()) }()
		var deleted int
		err = tx.QueryRow(
			ctx,
			"SELECT identity.purge_expired_broker_echo_replays(NULL)",
		).Scan(&deleted)
		requireBrokerEchoPostgresCode(t, err, "22023")
	})
	for _, invalidLimit := range []int{0, 101, 1001} {
		if _, err := api.Exec(
			ctx,
			"SELECT identity.purge_expired_broker_echo_replays($1)",
			invalidLimit,
		); err == nil {
			t.Fatalf("purge limit %d was accepted", invalidLimit)
		} else {
			requireBrokerEchoPostgresCode(t, err, "22023")
		}
	}
	for call, expected := range []int{2, 1, 0} {
		var deleted int
		if err := api.QueryRow(ctx, `
			SELECT identity.purge_expired_broker_echo_replays(2)`,
		).Scan(&deleted); err != nil {
			t.Fatalf("purge call %d: %v", call+1, err)
		}
		if deleted != expected {
			t.Fatalf("purge call %d deleted %d, want %d", call+1, deleted, expected)
		}
	}
	var (
		expiredRows int
		liveRows    int
	)
	if err := pool.QueryRow(ctx, `
		SELECT
			count(*) FILTER (
				WHERE expires_at <= transaction_timestamp()
			),
			count(*) FILTER (
				WHERE expires_at > transaction_timestamp()
			)
		  FROM identity.broker_echo_replays`,
	).Scan(&expiredRows, &liveRows); err != nil {
		t.Fatal(err)
	}
	if expiredRows != 0 || liveRows != 1 {
		t.Fatalf("purge left expired=%d live=%d, want expired=0 live=1", expiredRows, liveRows)
	}
	var coverage platformpostgres.BrokerEchoReplayCoverage
	if err := api.QueryRow(ctx, `
		SELECT
			max_total_rows,
			max_rows_per_principal,
			purge_batch_size,
			max_batches_per_cycle,
			cleanup_interval_seconds,
			cleanup_cycle_timeout_seconds,
			expired_readiness_slo_seconds,
			max_retry_after_seconds,
			total_rows,
			live_rows,
			expired_rows,
			maximum_principal_rows,
			oldest_live_expires_at,
			oldest_expired_at,
			oldest_expired_age_seconds
		  FROM identity.broker_echo_replay_coverage()`,
	).Scan(
		&coverage.MaxTotalRows,
		&coverage.MaxRowsPerPrincipal,
		&coverage.PurgeBatchSize,
		&coverage.MaxBatchesPerCycle,
		&coverage.CleanupIntervalSeconds,
		&coverage.CleanupCycleTimeoutSeconds,
		&coverage.ExpiredReadinessSLOSeconds,
		&coverage.MaxRetryAfterSeconds,
		&coverage.TotalRows,
		&coverage.LiveRows,
		&coverage.ExpiredRows,
		&coverage.MaximumPrincipalRows,
		&coverage.OldestLiveExpiresAt,
		&coverage.OldestExpiredAt,
		&coverage.OldestExpiredAgeSeconds,
	); err != nil {
		t.Fatalf("load least-privilege replay coverage: %v", err)
	}
	if coverage.MaxTotalRows != 1000 ||
		coverage.MaxRowsPerPrincipal != 100 ||
		coverage.PurgeBatchSize != 100 ||
		coverage.MaxBatchesPerCycle != 10 ||
		coverage.CleanupIntervalSeconds != 60 ||
		coverage.CleanupCycleTimeoutSeconds != 10 ||
		coverage.ExpiredReadinessSLOSeconds != 120 ||
		coverage.MaxRetryAfterSeconds != 86460 ||
		coverage.TotalRows != 1 ||
		coverage.LiveRows != 1 ||
		coverage.ExpiredRows != 0 ||
		coverage.MaximumPrincipalRows != 1 ||
		coverage.OldestLiveExpiresAt == "" ||
		coverage.OldestExpiredAt != "" ||
		coverage.OldestExpiredAgeSeconds != 0 {
		t.Fatalf("broker-echo replay coverage = %#v", coverage)
	}
}

func TestBrokerEchoReplayClaimRejectsInvalidInputs(t *testing.T) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	applyBrokerEchoCurrentSchema(t, pool)
	api := runtimeRoleLoginPool(
		t,
		pool,
		"platformgo_api_broker_echo_claim_test",
		"platformgo_api",
	)

	validPrincipal := "urn:xb:apikey:claim-inputs"
	validKeyHash := slices.Repeat([]byte{0x11}, 32)
	validRequestHash := slices.Repeat([]byte{0x22}, 32)
	validHeaders := `{"Content-Type":["application/json"]}`
	validBody := []byte(
		"{\"id\":\"019fa562-2c4f-4b7e-8db3-ec1fc8d53931\"}\n",
	)
	type claimInput struct {
		principal   any
		keyHash     any
		requestHash any
		status      any
		headers     any
		body        any
	}
	for _, test := range []struct {
		name  string
		input claimInput
	}{
		{
			name:  "NULL principal",
			input: claimInput{nil, validKeyHash, validRequestHash, 200, validHeaders, validBody},
		},
		{
			name:  "empty principal suffix",
			input: claimInput{"urn:xb:apikey:", validKeyHash, validRequestHash, 200, validHeaders, validBody},
		},
		{
			name:  "wrong principal prefix",
			input: claimInput{"urn:xb:user:wrong", validKeyHash, validRequestHash, 200, validHeaders, validBody},
		},
		{
			name:  "NULL key hash",
			input: claimInput{validPrincipal, nil, validRequestHash, 200, validHeaders, validBody},
		},
		{
			name:  "short key hash",
			input: claimInput{validPrincipal, []byte{0x11}, validRequestHash, 200, validHeaders, validBody},
		},
		{
			name:  "NULL request hash",
			input: claimInput{validPrincipal, validKeyHash, nil, 200, validHeaders, validBody},
		},
		{
			name:  "short request hash",
			input: claimInput{validPrincipal, validKeyHash, []byte{0x22}, 200, validHeaders, validBody},
		},
		{
			name:  "NULL status",
			input: claimInput{validPrincipal, validKeyHash, validRequestHash, nil, validHeaders, validBody},
		},
		{
			name:  "wrong status",
			input: claimInput{validPrincipal, validKeyHash, validRequestHash, 201, validHeaders, validBody},
		},
		{
			name:  "NULL headers",
			input: claimInput{validPrincipal, validKeyHash, validRequestHash, 200, nil, validBody},
		},
		{
			name:  "non-object headers",
			input: claimInput{validPrincipal, validKeyHash, validRequestHash, 200, `[]`, validBody},
		},
		{
			name:  "missing content type",
			input: claimInput{validPrincipal, validKeyHash, validRequestHash, 200, `{}`, validBody},
		},
		{
			name:  "wrong content type",
			input: claimInput{validPrincipal, validKeyHash, validRequestHash, 200, `{"Content-Type":["text/plain"]}`, validBody},
		},
		{
			name:  "NULL body",
			input: claimInput{validPrincipal, validKeyHash, validRequestHash, 200, validHeaders, nil},
		},
		{
			name:  "empty body",
			input: claimInput{validPrincipal, validKeyHash, validRequestHash, 200, validHeaders, []byte{}},
		},
		{
			name:  "malformed JSON body",
			input: claimInput{validPrincipal, validKeyHash, validRequestHash, 200, validHeaders, []byte(`{`)},
		},
		{
			name:  "body without id",
			input: claimInput{validPrincipal, validKeyHash, validRequestHash, 200, validHeaders, []byte(`{}`)},
		},
		{
			name:  "body without final newline",
			input: claimInput{validPrincipal, validKeyHash, validRequestHash, 200, validHeaders, []byte(`{"id":"019fa562-2c4f-4b7e-8db3-ec1fc8d53931"}`)},
		},
		{
			name:  "body with non-canonical id",
			input: claimInput{validPrincipal, validKeyHash, validRequestHash, 200, validHeaders, []byte(`{"id":"bad"}`)},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := test.input
			tx, err := api.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(context.Background()) }()
			var status int
			var headers []byte
			var body []byte
			err = tx.QueryRow(ctx, `
				SELECT response_status, response_headers, response_body
				  FROM identity.claim_broker_echo_response(
					$1::text,
					$2::bytea,
					$3::bytea,
					$4::integer,
					$5::jsonb,
					$6::bytea
				  )`,
				input.principal,
				input.keyHash,
				input.requestHash,
				input.status,
				input.headers,
				input.body,
			).Scan(&status, &headers, &body)
			requireBrokerEchoPostgresCode(t, err, "22023")
		})
	}
	var rows int
	if err := pool.QueryRow(
		ctx,
		"SELECT count(*) FROM identity.broker_echo_replays",
	).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("invalid claims persisted %d replay rows", rows)
	}
}

func assertBrokerEchoReplayCatalogAndACL(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	for _, function := range []string{
		"identity.claim_broker_echo_response(text,bytea,bytea,integer,jsonb,bytea)",
		"identity.purge_expired_broker_echo_replays(integer)",
	} {
		var (
			securityDefiner bool
			config          []string
			owner           string
			publicExecute   bool
			apiExecute      bool
		)
		if err := pool.QueryRow(ctx, `
			SELECT
				prosecdef,
				COALESCE(proconfig, ARRAY[]::text[]),
				pg_get_userbyid(proowner),
				has_function_privilege('public', oid, 'EXECUTE'),
				has_function_privilege('platformgo_api', oid, 'EXECUTE')
			  FROM pg_proc
			 WHERE oid = $1::regprocedure`,
			function,
		).Scan(
			&securityDefiner,
			&config,
			&owner,
			&publicExecute,
			&apiExecute,
		); err != nil {
			t.Fatalf("inspect %s: %v", function, err)
		}
		if !securityDefiner ||
			!slices.Equal(
				config,
				[]string{"search_path=pg_catalog", "lock_timeout=5s"},
			) ||
			slices.Contains(
				[]string{
					"public",
					"platformgo_api",
					"platformgo_engine",
					"platformgo_outbox",
					"platformgo_projector",
					"platformgo_realtime",
					"platformgo_realtime_repair",
				},
				owner,
			) ||
			publicExecute ||
			!apiExecute {
			t.Fatalf(
				"%s security definer=%t config=%v owner=%q public=%t api=%t",
				function,
				securityDefiner,
				config,
				owner,
				publicExecute,
				apiExecute,
			)
		}
	}
	var (
		coverageSecurityDefiner bool
		coverageConfig          []string
		coverageOwner           string
		publicCoverageExecute   bool
		apiCoverageExecute      bool
	)
	if err := pool.QueryRow(ctx, `
		SELECT
			prosecdef,
			COALESCE(proconfig, ARRAY[]::text[]),
			pg_get_userbyid(proowner),
			has_function_privilege('public', oid, 'EXECUTE'),
			has_function_privilege('platformgo_api', oid, 'EXECUTE')
		  FROM pg_proc
		 WHERE oid =
		       'identity.broker_echo_replay_coverage()'::regprocedure`,
	).Scan(
		&coverageSecurityDefiner,
		&coverageConfig,
		&coverageOwner,
		&publicCoverageExecute,
		&apiCoverageExecute,
	); err != nil {
		t.Fatalf("inspect broker-echo coverage authority: %v", err)
	}
	if !coverageSecurityDefiner ||
		!slices.Equal(
			coverageConfig,
			[]string{"search_path=pg_catalog"},
		) ||
		slices.Contains(
			[]string{
				"public",
				"platformgo_api",
				"platformgo_engine",
				"platformgo_outbox",
				"platformgo_projector",
				"platformgo_realtime",
				"platformgo_realtime_repair",
			},
			coverageOwner,
		) ||
		publicCoverageExecute ||
		!apiCoverageExecute {
		t.Fatalf(
			"coverage definer=%t config=%v owner=%q public=%t api=%t",
			coverageSecurityDefiner,
			coverageConfig,
			coverageOwner,
			publicCoverageExecute,
			apiCoverageExecute,
		)
	}

	var (
		oldFunctionExecute bool
		tableSelect        bool
		tableInsert        bool
		tableUpdate        bool
		tableDelete        bool
		tableTruncate      bool
		legacyTableSelect  bool
		updateTrigger      bool
	)
	if err := pool.QueryRow(ctx, `
		SELECT
			has_function_privilege(
				'platformgo_api',
				'identity.claim_broker_echo(text,text,bytea,text,timestamp with time zone)',
				'EXECUTE'
			),
			has_table_privilege(
				'platformgo_api',
				'identity.broker_echo_replays',
				'SELECT'
			),
			has_table_privilege(
				'platformgo_api',
				'identity.broker_echo_replays',
				'INSERT'
			),
			has_table_privilege(
				'platformgo_api',
				'identity.broker_echo_replays',
				'UPDATE'
			),
			has_table_privilege(
				'platformgo_api',
				'identity.broker_echo_replays',
				'DELETE'
			),
			has_table_privilege(
				'platformgo_api',
				'identity.broker_echo_replays',
				'TRUNCATE'
			),
			has_table_privilege(
				'platformgo_api',
				'identity.idempotency_responses',
				'SELECT'
			),
			EXISTS (
				SELECT 1
				  FROM pg_trigger
				 WHERE tgrelid = 'identity.broker_echo_replays'::regclass
				   AND tgname = 'broker_echo_replays_guard_mutation'
				   AND tgenabled = 'O'
				   AND NOT tgisinternal
			)`,
	).Scan(
		&oldFunctionExecute,
		&tableSelect,
		&tableInsert,
		&tableUpdate,
		&tableDelete,
		&tableTruncate,
		&legacyTableSelect,
		&updateTrigger,
	); err != nil {
		t.Fatal(err)
	}
	if oldFunctionExecute ||
		tableSelect ||
		tableInsert ||
		tableUpdate ||
		tableDelete ||
		tableTruncate ||
		legacyTableSelect ||
		!updateTrigger {
		t.Fatalf(
			"catalog old_execute=%t table(select=%t insert=%t update=%t "+
				"delete=%t truncate=%t) "+
				"legacy_select=%t update_trigger=%t",
			oldFunctionExecute,
			tableSelect,
			tableInsert,
			tableUpdate,
			tableDelete,
			tableTruncate,
			legacyTableSelect,
			updateTrigger,
		)
	}

	for _, role := range []string{
		"public",
		"platformgo_engine",
		"platformgo_outbox",
		"platformgo_projector",
		"platformgo_realtime",
		"platformgo_realtime_repair",
	} {
		var (
			canSelect   bool
			canInsert   bool
			canUpdate   bool
			canDelete   bool
			canTruncate bool
			canClaim    bool
			canPurge    bool
			canCoverage bool
			canPolicy   bool
		)
		if err := pool.QueryRow(ctx, `
			SELECT
				has_table_privilege($1, 'identity.broker_echo_replays', 'SELECT'),
				has_table_privilege($1, 'identity.broker_echo_replays', 'INSERT'),
				has_table_privilege($1, 'identity.broker_echo_replays', 'UPDATE'),
				has_table_privilege($1, 'identity.broker_echo_replays', 'DELETE'),
				has_table_privilege($1, 'identity.broker_echo_replays', 'TRUNCATE'),
				has_function_privilege(
					$1,
					'identity.claim_broker_echo_response(text,bytea,bytea,integer,jsonb,bytea)',
					'EXECUTE'
				),
				has_function_privilege(
					$1,
					'identity.purge_expired_broker_echo_replays(integer)',
					'EXECUTE'
				),
				has_function_privilege(
					$1,
					'identity.broker_echo_replay_coverage()',
					'EXECUTE'
				),
				has_table_privilege(
					$1,
					'identity.broker_echo_replay_policy',
					'SELECT'
				)`,
			role,
		).Scan(
			&canSelect,
			&canInsert,
			&canUpdate,
			&canDelete,
			&canTruncate,
			&canClaim,
			&canPurge,
			&canCoverage,
			&canPolicy,
		); err != nil {
			t.Fatalf("inspect broker echo privileges for %s: %v", role, err)
		}
		if canSelect || canInsert || canUpdate || canDelete ||
			canTruncate || canClaim || canPurge || canCoverage || canPolicy {
			t.Fatalf(
				"%s broker echo privileges = table(%t,%t,%t,%t,%t) "+
					"functions(%t,%t,%t) policy=%t",
				role,
				canSelect,
				canInsert,
				canUpdate,
				canDelete,
				canTruncate,
				canClaim,
				canPurge,
				canCoverage,
				canPolicy,
			)
		}
	}

	api := runtimeRoleLoginPool(
		t,
		pool,
		fmt.Sprintf("platformgo_api_broker_echo_poison_%d", os.Getpid()),
		"platformgo_api",
	)
	if _, err := api.Exec(ctx, `
		CREATE TEMP TABLE broker_echo_replays (response_body bytea);
		SET search_path = pg_temp, public`); err != nil {
		t.Fatalf("poison API search path: %v", err)
	}
	var poisonedStatus int
	if err := api.QueryRow(ctx, `
		SELECT response_status
		  FROM identity.claim_broker_echo_response(
			'urn:xb:apikey:poisoned-path',
			decode(repeat('71', 32), 'hex'),
			decode(repeat('72', 32), 'hex'),
			200,
			'{"Content-Type":["application/json"]}',
			convert_to(
				'{"id":"019fa562-2c4f-4b7e-8db3-ec1fc8d53971"}' ||
					chr(10),
				'UTF8'
			)
		  )`,
	).Scan(&poisonedStatus); err != nil {
		t.Fatalf("claim through poisoned search path: %v", err)
	}
	if poisonedStatus != 200 {
		t.Fatalf("poisoned-path claim status = %d, want 200", poisonedStatus)
	}
}

func assertBrokerEchoCapacityFinalCatalog(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	var (
		replayApplied       bool
		capacityApplied     bool
		integrityApplied    bool
		policyExists        bool
		policyGuard         bool
		validatorExists     bool
		coverageExists      bool
		integrityConstraint bool
	)
	if err := pool.QueryRow(ctx, `
		SELECT
			EXISTS (
				SELECT 1
				  FROM engine.schema_migrations
				 WHERE filename = $1
			),
			EXISTS (
				SELECT 1
				  FROM engine.schema_migrations
				 WHERE filename = $2
			),
			EXISTS (
				SELECT 1
				  FROM engine.schema_migrations
				 WHERE filename = $3
			),
			to_regclass('identity.broker_echo_replay_policy') IS NOT NULL,
			to_regprocedure(
				'identity.guard_broker_echo_replay_policy()'
			) IS NOT NULL,
			to_regprocedure(
				'identity.valid_broker_echo_response(bytea,integer,jsonb,bytea,timestamptz,timestamptz)'
			) IS NOT NULL,
			to_regprocedure(
				'identity.broker_echo_replay_coverage()'
			) IS NOT NULL,
			EXISTS (
				SELECT 1
				  FROM pg_constraint
				 WHERE conrelid =
				       'identity.broker_echo_replays'::regclass
				   AND conname =
				       'broker_echo_replays_have_valid_exact_response'
			)`,
		brokerEchoReplayMigration,
		brokerEchoCapacityMigration,
		brokerEchoIntegrityMigration,
	).Scan(
		&replayApplied,
		&capacityApplied,
		&integrityApplied,
		&policyExists,
		&policyGuard,
		&validatorExists,
		&coverageExists,
		&integrityConstraint,
	); err != nil {
		t.Fatal(err)
	}
	if !replayApplied || !capacityApplied || integrityApplied ||
		!policyExists || !policyGuard || !validatorExists || !coverageExists ||
		integrityConstraint {
		t.Fatalf(
			"capacity final tip replay=%t capacity=%t integrity=%t policy=%t "+
				"policy_guard=%t validator=%t coverage=%t constraint=%t",
			replayApplied,
			capacityApplied,
			integrityApplied,
			policyExists,
			policyGuard,
			validatorExists,
			coverageExists,
			integrityConstraint,
		)
	}

	assertBrokerEchoPolicyCatalog(t, pool)
	assertBrokerEchoClaimResult(
		t,
		pool,
		[]string{
			"outcome",
			"retry_after_seconds",
			"capacity_scope",
			"response_status",
			"response_headers",
			"response_body",
		},
		[]string{"text", "bigint", "text", "integer", "jsonb", "bytea"},
	)
	assertBrokerEchoFunctionAuthority(
		t,
		pool,
		"identity.valid_broker_echo_response(bytea,integer,jsonb,bytea,timestamptz,timestamptz)",
		false,
		[]string{"search_path=pg_catalog"},
		nil,
	)
	assertBrokerEchoFunctionAuthority(
		t,
		pool,
		"identity.claim_broker_echo_response(text,bytea,bytea,integer,jsonb,bytea)",
		true,
		[]string{"search_path=pg_catalog", "lock_timeout=5s"},
		map[string]bool{"platformgo_api": true},
	)
	assertBrokerEchoFunctionAuthority(
		t,
		pool,
		"identity.purge_expired_broker_echo_replays(integer)",
		true,
		[]string{"search_path=pg_catalog", "lock_timeout=5s"},
		map[string]bool{"platformgo_api": true},
	)
	assertBrokerEchoFunctionAuthority(
		t,
		pool,
		"identity.broker_echo_replay_coverage()",
		true,
		[]string{"search_path=pg_catalog"},
		map[string]bool{"platformgo_api": true},
	)
	assertBrokerEchoFunctionTableResult(
		t,
		pool,
		"identity.broker_echo_replay_coverage()",
		[]string{
			"max_total_rows",
			"max_rows_per_principal",
			"purge_batch_size",
			"max_batches_per_cycle",
			"cleanup_interval_seconds",
			"cleanup_cycle_timeout_seconds",
			"expired_readiness_slo_seconds",
			"max_retry_after_seconds",
			"total_rows",
			"live_rows",
			"expired_rows",
			"maximum_principal_rows",
			"oldest_live_expires_at",
			"oldest_expired_at",
			"oldest_expired_age_seconds",
		},
		[]string{
			"integer",
			"integer",
			"integer",
			"integer",
			"integer",
			"integer",
			"integer",
			"integer",
			"bigint",
			"bigint",
			"bigint",
			"bigint",
			"text",
			"text",
			"bigint",
		},
	)
	assertBrokerEchoFunctionVolatility(
		t,
		pool,
		"identity.valid_broker_echo_response(bytea,integer,jsonb,bytea,timestamptz,timestamptz)",
		"i",
	)
	assertBrokerEchoFunctionVolatility(
		t,
		pool,
		"identity.broker_echo_replay_coverage()",
		"s",
	)
	assertBrokerEchoValidatorLifetime(t, pool, false)
	assertBrokerEchoPurgeDefinition(t, pool, true)
	assertBrokerEchoPolicyPrivileges(t, pool)
}

func assertBrokerEchoIntegrityFinalCatalog(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	var (
		replayApplied        bool
		capacityApplied      bool
		integrityApplied     bool
		policyExists         bool
		validatorExists      bool
		coverageExists       bool
		constraintValid      bool
		constraintType       string
		constraintDefinition string
	)
	if err := pool.QueryRow(ctx, `
		SELECT
			EXISTS (
				SELECT 1
				  FROM engine.schema_migrations
				 WHERE filename = $1
			),
			EXISTS (
				SELECT 1
				  FROM engine.schema_migrations
				 WHERE filename = $2
			),
			EXISTS (
				SELECT 1
				  FROM engine.schema_migrations
				 WHERE filename = $3
			),
			to_regclass('identity.broker_echo_replay_policy') IS NOT NULL,
			to_regprocedure(
				'identity.valid_broker_echo_response(bytea,integer,jsonb,bytea,timestamptz,timestamptz)'
			) IS NOT NULL,
			to_regprocedure(
				'identity.broker_echo_replay_coverage()'
			) IS NOT NULL,
			catalog_constraint.convalidated,
			catalog_constraint.contype::text,
			pg_get_constraintdef(catalog_constraint.oid, true)
		  FROM pg_constraint AS catalog_constraint
		 WHERE catalog_constraint.conrelid =
		       'identity.broker_echo_replays'::regclass
		   AND catalog_constraint.conname =
		       'broker_echo_replays_have_valid_exact_response'`,
		brokerEchoReplayMigration,
		brokerEchoCapacityMigration,
		brokerEchoIntegrityMigration,
	).Scan(
		&replayApplied,
		&capacityApplied,
		&integrityApplied,
		&policyExists,
		&validatorExists,
		&coverageExists,
		&constraintValid,
		&constraintType,
		&constraintDefinition,
	); err != nil {
		t.Fatalf("inspect broker-echo integrity final tip: %v", err)
	}
	const expectedConstraint = "CHECK (identity.valid_broker_echo_response(" +
		"request_hash, response_status, response_headers, response_body, " +
		"created_at, expires_at) AND (NOT postgres_time_authority OR " +
		"expires_at = (created_at + '24:00:00'::interval)))"
	if !replayApplied || !capacityApplied || !integrityApplied ||
		!policyExists || !validatorExists || !coverageExists ||
		!constraintValid || constraintType != "c" ||
		constraintDefinition != expectedConstraint {
		t.Fatalf(
			"integrity final tip replay=%t capacity=%t integrity=%t "+
				"policy=%t validator=%t coverage=%t constraint(valid=%t "+
				"type=%q definition=%q)",
			replayApplied,
			capacityApplied,
			integrityApplied,
			policyExists,
			validatorExists,
			coverageExists,
			constraintValid,
			constraintType,
			constraintDefinition,
		)
	}

	assertBrokerEchoPolicyCatalog(t, pool)
	assertBrokerEchoPostgresTimeAuthorityColumn(t, pool)
	assertBrokerEchoInsertGuardCatalog(t, pool)
	assertBrokerEchoClaimResult(
		t,
		pool,
		[]string{
			"outcome",
			"retry_after_seconds",
			"capacity_scope",
			"response_status",
			"response_headers",
			"response_body",
		},
		[]string{"text", "bigint", "text", "integer", "jsonb", "bytea"},
	)
	assertBrokerEchoFunctionAuthority(
		t,
		pool,
		"identity.valid_broker_echo_response(bytea,integer,jsonb,bytea,timestamptz,timestamptz)",
		false,
		[]string{"search_path=pg_catalog"},
		nil,
	)
	assertBrokerEchoFunctionAuthority(
		t,
		pool,
		"identity.claim_broker_echo_response(text,bytea,bytea,integer,jsonb,bytea)",
		true,
		[]string{"search_path=pg_catalog", "lock_timeout=5s"},
		map[string]bool{"platformgo_api": true},
	)
	assertBrokerEchoFunctionAuthority(
		t,
		pool,
		"identity.purge_expired_broker_echo_replays(integer)",
		true,
		[]string{"search_path=pg_catalog", "lock_timeout=5s"},
		map[string]bool{"platformgo_api": true},
	)
	assertBrokerEchoFunctionAuthority(
		t,
		pool,
		"identity.broker_echo_replay_coverage()",
		true,
		[]string{"search_path=pg_catalog"},
		map[string]bool{"platformgo_api": true},
	)
	assertBrokerEchoFunctionTableResult(
		t,
		pool,
		"identity.broker_echo_replay_coverage()",
		[]string{
			"max_total_rows",
			"max_rows_per_principal",
			"purge_batch_size",
			"max_batches_per_cycle",
			"cleanup_interval_seconds",
			"cleanup_cycle_timeout_seconds",
			"expired_readiness_slo_seconds",
			"max_retry_after_seconds",
			"total_rows",
			"live_rows",
			"invalid_live_rows",
			"overlong_live_rows",
			"expired_rows",
			"maximum_principal_rows",
			"oldest_live_expires_at",
			"oldest_expired_at",
			"oldest_expired_age_seconds",
		},
		[]string{
			"integer",
			"integer",
			"integer",
			"integer",
			"integer",
			"integer",
			"integer",
			"integer",
			"bigint",
			"bigint",
			"bigint",
			"bigint",
			"bigint",
			"bigint",
			"text",
			"text",
			"bigint",
		},
	)
	assertBrokerEchoFunctionVolatility(
		t,
		pool,
		"identity.valid_broker_echo_response(bytea,integer,jsonb,bytea,timestamptz,timestamptz)",
		"i",
	)
	assertBrokerEchoFunctionVolatility(
		t,
		pool,
		"identity.broker_echo_replay_coverage()",
		"s",
	)
	assertBrokerEchoValidatorLifetime(t, pool, false)
	assertBrokerEchoCoverageMarkerDefinition(t, pool)
	assertBrokerEchoPurgeDefinition(t, pool, true)
	assertBrokerEchoPolicyPrivileges(t, pool)
}

func assertBrokerEchoFinalGuardCatalog(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	var (
		finalGuardApplied bool
		brokerMigrations  int
		insertGuard       bool
		truncateGuard     bool
		triggerCount      int
		mutationTrigger   string
		insertTrigger     string
		truncateTrigger   string
	)
	if err := pool.QueryRow(ctx, `
		SELECT
			EXISTS (
				SELECT 1
				  FROM engine.schema_migrations
				 WHERE filename = $1
			),
			(
				SELECT count(*)
				  FROM engine.schema_migrations
				 WHERE filename = ANY($2::text[])
			),
			to_regprocedure(
				'identity.guard_broker_echo_replay_insert()'
			) IS NOT NULL,
			to_regprocedure(
				'identity.guard_broker_echo_replay_truncate()'
			) IS NOT NULL,
			(
				SELECT count(*)
				  FROM pg_trigger
				 WHERE tgrelid = 'identity.broker_echo_replays'::regclass
				   AND NOT tgisinternal
			),
			(
				SELECT pg_get_triggerdef(oid, true)
				  FROM pg_trigger
				 WHERE tgrelid = 'identity.broker_echo_replays'::regclass
				   AND tgname = 'broker_echo_replays_guard_mutation'
				   AND tgenabled = 'O'
				   AND NOT tgisinternal
			),
			(
				SELECT pg_get_triggerdef(oid, true)
				  FROM pg_trigger
				 WHERE tgrelid = 'identity.broker_echo_replays'::regclass
				   AND tgname =
				       'broker_echo_replays_require_postgres_time_authority'
				   AND tgenabled = 'O'
				   AND NOT tgisinternal
			),
			(
				SELECT pg_get_triggerdef(oid, true)
				  FROM pg_trigger
				 WHERE tgrelid = 'identity.broker_echo_replays'::regclass
				   AND tgname = 'broker_echo_replays_reject_truncate'
				   AND tgenabled = 'O'
				   AND NOT tgisinternal
			)`,
		brokerEchoFinalGuardMigration,
		[]string{
			brokerEchoReplayMigration,
			brokerEchoCapacityMigration,
			brokerEchoIntegrityMigration,
			brokerEchoFinalGuardMigration,
		},
	).Scan(
		&finalGuardApplied,
		&brokerMigrations,
		&insertGuard,
		&truncateGuard,
		&triggerCount,
		&mutationTrigger,
		&insertTrigger,
		&truncateTrigger,
	); err != nil {
		t.Fatalf("inspect broker-echo final guard catalog: %v", err)
	}
	if !finalGuardApplied || brokerMigrations != 4 || !insertGuard ||
		!truncateGuard || triggerCount != 3 {
		t.Fatalf(
			"final guard applied=%t migrations=%d insert_guard=%t "+
				"truncate_guard=%t trigger_count=%d",
			finalGuardApplied,
			brokerMigrations,
			insertGuard,
			truncateGuard,
			triggerCount,
		)
	}
	expectedMutation := "CREATE TRIGGER broker_echo_replays_guard_mutation " +
		"BEFORE DELETE OR UPDATE ON identity.broker_echo_replays " +
		"FOR EACH ROW EXECUTE FUNCTION identity.guard_broker_echo_replay_mutation()"
	expectedInsert := "CREATE TRIGGER " +
		"broker_echo_replays_require_postgres_time_authority " +
		"BEFORE INSERT ON identity.broker_echo_replays FOR EACH ROW " +
		"EXECUTE FUNCTION identity.guard_broker_echo_replay_insert()"
	expectedTruncate := "CREATE TRIGGER broker_echo_replays_reject_truncate " +
		"BEFORE TRUNCATE ON identity.broker_echo_replays FOR EACH STATEMENT " +
		"EXECUTE FUNCTION identity.guard_broker_echo_replay_truncate()"
	if mutationTrigger != expectedMutation ||
		insertTrigger != expectedInsert ||
		truncateTrigger != expectedTruncate {
		t.Fatalf(
			"final replay triggers mutation=%q insert=%q truncate=%q",
			mutationTrigger,
			insertTrigger,
			truncateTrigger,
		)
	}
	for _, signature := range []string{
		"identity.guard_broker_echo_replay_insert()",
		"identity.guard_broker_echo_replay_truncate()",
	} {
		assertBrokerEchoFunctionAuthority(
			t,
			pool,
			signature,
			false,
			[]string{"search_path=pg_catalog"},
			nil,
		)
		assertBrokerEchoFunctionExecuteACL(t, pool, signature, false)
	}
	assertBrokerEchoIntegrityFinalCatalog(t, pool)
}

func assertBrokerEchoInsertGuardCatalog(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	var (
		insertGuard         bool
		insertGuardSource   string
		insertGuardLanguage string
		insertTrigger       string
	)
	if err := pool.QueryRow(context.Background(), `
		SELECT
			to_regprocedure(
				'identity.guard_broker_echo_replay_insert()'
			) IS NOT NULL,
			procedure.prosrc,
			language.lanname,
			(
				SELECT pg_get_triggerdef(oid, true)
				  FROM pg_trigger
				 WHERE tgrelid = 'identity.broker_echo_replays'::regclass
				   AND tgname =
				       'broker_echo_replays_require_postgres_time_authority'
				   AND tgenabled = 'O'
				   AND NOT tgisinternal
			)
		  FROM pg_proc AS procedure
		  JOIN pg_language AS language
		    ON language.oid = procedure.prolang
		 WHERE procedure.oid =
		       'identity.guard_broker_echo_replay_insert()'::regprocedure`,
	).Scan(
		&insertGuard,
		&insertGuardSource,
		&insertGuardLanguage,
		&insertTrigger,
	); err != nil {
		t.Fatalf("inspect broker-echo insert fence: %v", err)
	}
	const expectedSource = `
BEGIN
    IF NEW.postgres_time_authority IS DISTINCT FROM true THEN
        RAISE EXCEPTION USING
            ERRCODE = '55000',
            MESSAGE =
                'broker echo legacy time authority is migration-only';
    END IF;
    RETURN NEW;
END;
`
	const expectedTrigger = "CREATE TRIGGER " +
		"broker_echo_replays_require_postgres_time_authority " +
		"BEFORE INSERT ON identity.broker_echo_replays FOR EACH ROW " +
		"EXECUTE FUNCTION identity.guard_broker_echo_replay_insert()"
	if !insertGuard || insertGuardLanguage != "plpgsql" ||
		insertGuardSource != expectedSource ||
		insertTrigger != expectedTrigger {
		t.Fatalf(
			"broker-echo insert guard=%t language=%q source=%q trigger=%q",
			insertGuard,
			insertGuardLanguage,
			insertGuardSource,
			insertTrigger,
		)
	}
	assertBrokerEchoFunctionAuthority(
		t,
		pool,
		"identity.guard_broker_echo_replay_insert()",
		false,
		[]string{"search_path=pg_catalog"},
		nil,
	)
	assertBrokerEchoFunctionExecuteACL(
		t,
		pool,
		"identity.guard_broker_echo_replay_insert()",
		false,
	)
}

func assertBrokerEchoPostgresTimeAuthorityColumn(
	t *testing.T,
	pool *pgxpool.Pool,
) {
	t.Helper()
	var (
		dataType   string
		notNull    bool
		defaultSQL string
	)
	if err := pool.QueryRow(context.Background(), `
		SELECT
			format_type(attribute.atttypid, attribute.atttypmod),
			attribute.attnotnull,
			COALESCE(pg_get_expr(defaults.adbin, defaults.adrelid), '')
		  FROM pg_attribute AS attribute
		  LEFT JOIN pg_attrdef AS defaults
		    ON defaults.adrelid = attribute.attrelid
		   AND defaults.adnum = attribute.attnum
		 WHERE attribute.attrelid =
		       'identity.broker_echo_replays'::regclass
		   AND attribute.attname = 'postgres_time_authority'
		   AND NOT attribute.attisdropped`,
	).Scan(&dataType, &notNull, &defaultSQL); err != nil {
		t.Fatalf("inspect PostgreSQL-time authority column: %v", err)
	}
	if dataType != "boolean" || !notNull || defaultSQL != "true" {
		t.Fatalf(
			"PostgreSQL-time authority column type=%q not_null=%t default=%q",
			dataType,
			notNull,
			defaultSQL,
		)
	}
}

func assertBrokerEchoPolicyPrivileges(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	for _, role := range brokerEchoPrivilegeRoles() {
		var (
			canSelect   bool
			canInsert   bool
			canUpdate   bool
			canDelete   bool
			canTruncate bool
		)
		if err := pool.QueryRow(ctx, `
			SELECT
				has_table_privilege(
					$1,
					'identity.broker_echo_replay_policy',
					'SELECT'
				),
				has_table_privilege(
					$1,
					'identity.broker_echo_replay_policy',
					'INSERT'
				),
				has_table_privilege(
					$1,
					'identity.broker_echo_replay_policy',
					'UPDATE'
				),
				has_table_privilege(
					$1,
					'identity.broker_echo_replay_policy',
					'DELETE'
				),
				has_table_privilege(
					$1,
					'identity.broker_echo_replay_policy',
					'TRUNCATE'
				)`,
			role,
		).Scan(
			&canSelect,
			&canInsert,
			&canUpdate,
			&canDelete,
			&canTruncate,
		); err != nil {
			t.Fatalf("inspect policy privileges for %s: %v", role, err)
		}
		if canSelect || canInsert || canUpdate || canDelete || canTruncate {
			t.Fatalf(
				"%s policy privileges select=%t insert=%t update=%t "+
					"delete=%t truncate=%t",
				role,
				canSelect,
				canInsert,
				canUpdate,
				canDelete,
				canTruncate,
			)
		}
	}
}

func assertBrokerEchoPolicyCatalog(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	rows, err := pool.Query(ctx, `
		SELECT
			attname,
			format_type(atttypid, atttypmod),
			attnotnull,
			COALESCE(pg_get_expr(defaults.adbin, defaults.adrelid), '')
		  FROM pg_attribute AS attributes
		  LEFT JOIN pg_attrdef AS defaults
		    ON defaults.adrelid = attributes.attrelid
		   AND defaults.adnum = attributes.attnum
		 WHERE attributes.attrelid =
		       'identity.broker_echo_replay_policy'::regclass
		   AND attributes.attnum > 0
		   AND NOT attributes.attisdropped
		 ORDER BY attributes.attnum`)
	if err != nil {
		t.Fatal(err)
	}
	var columns []string
	for rows.Next() {
		var (
			name       string
			dataType   string
			notNull    bool
			defaultSQL string
		)
		if err := rows.Scan(&name, &dataType, &notNull, &defaultSQL); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		columns = append(
			columns,
			fmt.Sprintf("%s|%s|%t|%s", name, dataType, notNull, defaultSQL),
		)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatal(err)
	}
	rows.Close()
	expectedColumns := []string{
		"singleton|boolean|true|true",
		"max_total_rows|integer|true|",
		"max_rows_per_principal|integer|true|",
		"purge_batch_size|integer|true|",
		"max_batches_per_cycle|integer|true|",
		"cleanup_interval_seconds|integer|true|",
		"cleanup_cycle_timeout_seconds|integer|true|",
		"expired_readiness_slo_seconds|integer|true|",
		"max_retry_after_seconds|integer|true|",
	}
	if !slices.Equal(columns, expectedColumns) {
		t.Fatalf("policy columns = %v, want %v", columns, expectedColumns)
	}

	rows, err = pool.Query(ctx, `
		SELECT pg_get_constraintdef(oid, true)
		  FROM pg_constraint
		 WHERE conrelid =
		       'identity.broker_echo_replay_policy'::regclass
		   AND contype = 'c'
		 ORDER BY pg_get_constraintdef(oid, true)`)
	if err != nil {
		t.Fatal(err)
	}
	var checks []string
	for rows.Next() {
		var check string
		if err := rows.Scan(&check); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		checks = append(checks, check)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatal(err)
	}
	rows.Close()
	expectedChecks := []string{
		"CHECK ((purge_batch_size::bigint * max_batches_per_cycle::bigint) >= max_total_rows::bigint)",
		"CHECK (cleanup_cycle_timeout_seconds = 10)",
		"CHECK (cleanup_interval_seconds = 60)",
		"CHECK (expired_readiness_slo_seconds = 120)",
		"CHECK (max_batches_per_cycle = 10)",
		"CHECK (max_retry_after_seconds = 86460)",
		"CHECK (max_retry_after_seconds >= (86400 + cleanup_interval_seconds))",
		"CHECK (max_rows_per_principal = 100)",
		"CHECK (max_rows_per_principal <= max_total_rows)",
		"CHECK (max_total_rows = 1000)",
		"CHECK (purge_batch_size = 100)",
		"CHECK (singleton)",
	}
	slices.Sort(expectedChecks)
	if !slices.Equal(checks, expectedChecks) {
		t.Fatalf("policy checks = %v, want %v", checks, expectedChecks)
	}

	var (
		singleton                  bool
		maxTotalRows               int
		maxRowsPerPrincipal        int
		purgeBatchSize             int
		maxBatchesPerCycle         int
		cleanupIntervalSeconds     int
		cleanupTimeoutSeconds      int
		expiredReadinessSLOSeconds int
		maxRetryAfterSeconds       int
	)
	if err := pool.QueryRow(ctx, `
		SELECT
			singleton,
			max_total_rows,
			max_rows_per_principal,
			purge_batch_size,
			max_batches_per_cycle,
			cleanup_interval_seconds,
			cleanup_cycle_timeout_seconds,
			expired_readiness_slo_seconds,
			max_retry_after_seconds
		  FROM identity.broker_echo_replay_policy`,
	).Scan(
		&singleton,
		&maxTotalRows,
		&maxRowsPerPrincipal,
		&purgeBatchSize,
		&maxBatchesPerCycle,
		&cleanupIntervalSeconds,
		&cleanupTimeoutSeconds,
		&expiredReadinessSLOSeconds,
		&maxRetryAfterSeconds,
	); err != nil {
		t.Fatal(err)
	}
	if !singleton ||
		maxTotalRows != 1000 ||
		maxRowsPerPrincipal != 100 ||
		purgeBatchSize != 100 ||
		maxBatchesPerCycle != 10 ||
		cleanupIntervalSeconds != 60 ||
		cleanupTimeoutSeconds != 10 ||
		expiredReadinessSLOSeconds != 120 ||
		maxRetryAfterSeconds != 86460 {
		t.Fatalf(
			"policy values singleton=%t total=%d principal=%d batch=%d "+
				"cycles=%d interval=%d timeout=%d slo=%d retry=%d",
			singleton,
			maxTotalRows,
			maxRowsPerPrincipal,
			purgeBatchSize,
			maxBatchesPerCycle,
			cleanupIntervalSeconds,
			cleanupTimeoutSeconds,
			expiredReadinessSLOSeconds,
			maxRetryAfterSeconds,
		)
	}

	rows, err = pool.Query(ctx, `
		SELECT tgname, pg_get_triggerdef(oid, true)
		  FROM pg_trigger
		 WHERE tgrelid =
		       'identity.broker_echo_replay_policy'::regclass
		   AND NOT tgisinternal
		 ORDER BY tgname`)
	if err != nil {
		t.Fatal(err)
	}
	var triggers []string
	for rows.Next() {
		var (
			name       string
			definition string
		)
		if err := rows.Scan(&name, &definition); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		triggers = append(triggers, name+"|"+definition)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatal(err)
	}
	rows.Close()
	expectedTriggers := []string{
		"broker_echo_replay_policy_is_immutable|" +
			"CREATE TRIGGER broker_echo_replay_policy_is_immutable " +
			"BEFORE DELETE OR UPDATE ON identity.broker_echo_replay_policy " +
			"FOR EACH ROW EXECUTE FUNCTION identity.guard_broker_echo_replay_policy()",
		"broker_echo_replay_policy_rejects_truncate|" +
			"CREATE TRIGGER broker_echo_replay_policy_rejects_truncate " +
			"BEFORE TRUNCATE ON identity.broker_echo_replay_policy " +
			"FOR EACH STATEMENT EXECUTE FUNCTION identity.guard_broker_echo_replay_policy()",
	}
	if !slices.Equal(triggers, expectedTriggers) {
		t.Fatalf("policy triggers = %v, want %v", triggers, expectedTriggers)
	}
}

func assertBrokerEchoClaimResult(
	t *testing.T,
	pool *pgxpool.Pool,
	expectedNames []string,
	expectedTypes []string,
) {
	t.Helper()
	assertBrokerEchoFunctionTableResult(
		t,
		pool,
		"identity.claim_broker_echo_response(text,bytea,bytea,integer,jsonb,bytea)",
		expectedNames,
		expectedTypes,
	)
}

func assertBrokerEchoFunctionTableResult(
	t *testing.T,
	pool *pgxpool.Pool,
	signature string,
	expectedNames []string,
	expectedTypes []string,
) {
	t.Helper()
	var (
		names []string
		types []string
	)
	if err := pool.QueryRow(context.Background(), `
		SELECT
			COALESCE(
				array_agg(argument.name ORDER BY argument.ordinality)
					FILTER (WHERE argument.mode = 't'),
				ARRAY[]::text[]
			),
			COALESCE(
				array_agg(
					format_type(argument.type_oid, NULL)
					ORDER BY argument.ordinality
				) FILTER (WHERE argument.mode = 't'),
				ARRAY[]::text[]
			)
		  FROM pg_proc AS function
		  CROSS JOIN LATERAL unnest(
			function.proallargtypes,
			function.proargmodes,
			function.proargnames
		  ) WITH ORDINALITY AS argument(
			type_oid,
			mode,
			name,
			ordinality
		 )
		 WHERE function.oid = $1::regprocedure
		 GROUP BY function.oid`,
		signature,
	).Scan(&names, &types); err != nil {
		t.Fatalf("inspect %s table result: %v", signature, err)
	}
	if !slices.Equal(names, expectedNames) ||
		!slices.Equal(types, expectedTypes) {
		t.Fatalf(
			"%s table result names=%v types=%v, want names=%v types=%v",
			signature,
			names,
			types,
			expectedNames,
			expectedTypes,
		)
	}
}

func assertBrokerEchoFunctionVolatility(
	t *testing.T,
	pool *pgxpool.Pool,
	signature string,
	expected string,
) {
	t.Helper()
	var volatility string
	if err := pool.QueryRow(context.Background(), `
		SELECT provolatile::text
		  FROM pg_proc
		 WHERE oid = $1::regprocedure`,
		signature,
	).Scan(&volatility); err != nil {
		t.Fatalf("inspect %s volatility: %v", signature, err)
	}
	if volatility != expected {
		t.Fatalf("%s volatility = %q, want %q", signature, volatility, expected)
	}
}

func assertBrokerEchoValidatorLifetime(
	t *testing.T,
	pool *pgxpool.Pool,
	expectExact bool,
) {
	t.Helper()
	var source string
	if err := pool.QueryRow(context.Background(), `
		SELECT prosrc
		  FROM pg_proc
		 WHERE oid =
		       $1::regprocedure`,
		"identity.valid_broker_echo_response(bytea,integer,jsonb,bytea,timestamptz,timestamptz)",
	).Scan(&source); err != nil {
		t.Fatalf("inspect broker-echo validator lifetime: %v", err)
	}
	compact := strings.Join(strings.Fields(source), " ")
	hasExact := strings.Contains(
		compact,
		"stored_expires_at = stored_created_at + interval '24 hours'",
	)
	hasLoose := strings.Contains(
		compact,
		"stored_expires_at > stored_created_at",
	)
	if hasExact != expectExact || hasLoose == expectExact {
		t.Fatalf(
			"broker-echo validator exact=%t loose=%t source=%q",
			hasExact,
			hasLoose,
			compact,
		)
	}
}

func assertBrokerEchoCoverageMarkerDefinition(
	t *testing.T,
	pool *pgxpool.Pool,
) {
	t.Helper()
	var source string
	if err := pool.QueryRow(context.Background(), `
		SELECT prosrc
		  FROM pg_proc
		 WHERE oid =
		       'identity.broker_echo_replay_coverage()'::regprocedure`,
	).Scan(&source); err != nil {
		t.Fatalf("inspect broker-echo coverage marker definition: %v", err)
	}
	compact := strings.Join(strings.Fields(source), " ")
	if !strings.Contains(
		compact,
		"replay.postgres_time_authority AND replay.expires_at <> "+
			"replay.created_at + interval '24 hours'",
	) || !strings.Contains(
		compact,
		"replay.expires_at > pg_catalog.statement_timestamp() + "+
			"interval '24 hours'",
	) {
		t.Fatalf("broker-echo coverage marker definition = %q", compact)
	}
}

func assertBrokerEchoFunctionAuthority(
	t *testing.T,
	pool *pgxpool.Pool,
	signature string,
	expectedSecurityDefiner bool,
	expectedConfig []string,
	expectedExecute map[string]bool,
) {
	t.Helper()
	ctx := context.Background()
	var (
		securityDefiner bool
		config          []string
		owner           string
		currentUser     string
	)
	if err := pool.QueryRow(ctx, `
		SELECT
			prosecdef,
			COALESCE(proconfig, ARRAY[]::text[]),
			pg_get_userbyid(proowner),
			current_user
		  FROM pg_proc
		 WHERE oid = $1::regprocedure`,
		signature,
	).Scan(
		&securityDefiner,
		&config,
		&owner,
		&currentUser,
	); err != nil {
		t.Fatalf("inspect %s authority: %v", signature, err)
	}
	if securityDefiner != expectedSecurityDefiner ||
		!slices.Equal(config, expectedConfig) ||
		owner != currentUser {
		t.Fatalf(
			"%s definer=%t config=%v owner=%q current_user=%q",
			signature,
			securityDefiner,
			config,
			owner,
			currentUser,
		)
	}
	for _, role := range brokerEchoPrivilegeRoles() {
		var canExecute bool
		if err := pool.QueryRow(ctx, `
			SELECT has_function_privilege($1, $2::regprocedure, 'EXECUTE')`,
			role,
			signature,
		).Scan(&canExecute); err != nil {
			t.Fatalf("inspect %s execute for %s: %v", signature, role, err)
		}
		if canExecute != expectedExecute[role] {
			t.Fatalf(
				"%s execute for %s = %t, want %t",
				signature,
				role,
				canExecute,
				expectedExecute[role],
			)
		}
	}
}

func assertBrokerEchoFunctionExecuteACL(
	t *testing.T,
	pool *pgxpool.Pool,
	signature string,
	expectAPI bool,
) {
	t.Helper()
	for _, role := range brokerEchoPrivilegeRoles() {
		var canExecute bool
		if err := pool.QueryRow(context.Background(), `
			SELECT has_function_privilege($1, $2::regprocedure, 'EXECUTE')`,
			role,
			signature,
		).Scan(&canExecute); err != nil {
			t.Fatalf("inspect %s execute for %s: %v", signature, role, err)
		}
		expected := role == "platformgo_api" && expectAPI
		if canExecute != expected {
			t.Fatalf(
				"%s execute for %s = %t, want %t",
				signature,
				role,
				canExecute,
				expected,
			)
		}
	}
}

func assertBrokerEchoPurgeDefinition(
	t *testing.T,
	pool *pgxpool.Pool,
	expectPolicyBound bool,
) {
	t.Helper()
	var source string
	if err := pool.QueryRow(context.Background(), `
		SELECT prosrc
		  FROM pg_proc
		 WHERE oid =
		       'identity.purge_expired_broker_echo_replays(integer)'::regprocedure`,
	).Scan(&source); err != nil {
		t.Fatalf("inspect broker-echo purge definition: %v", err)
	}
	compact := strings.Join(strings.Fields(source), " ")
	hasPolicyLock := strings.Contains(
		compact,
		"FROM identity.broker_echo_replay_policy AS policy",
	) && strings.Contains(compact, "FOR UPDATE") &&
		strings.Contains(compact, "requested_limit > policy_batch_size")
	hasOriginalBound := strings.Contains(compact, "requested_limit > 1000")
	if hasPolicyLock != expectPolicyBound ||
		hasOriginalBound == expectPolicyBound {
		t.Fatalf(
			"broker-echo purge policy_bound=%t original_bound=%t source=%q",
			hasPolicyLock,
			hasOriginalBound,
			compact,
		)
	}
}

func brokerEchoPrivilegeRoles() []string {
	return []string{
		"public",
		"platformgo_api",
		"platformgo_engine",
		"platformgo_outbox",
		"platformgo_projector",
		"platformgo_realtime",
		"platformgo_realtime_repair",
	}
}

func assertBrokerEchoRawACLAllowlist(
	t *testing.T,
	pool *pgxpool.Pool,
	hostileRole string,
	expectedTables int,
	expectedFunctions int,
) {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		WITH broker_objects AS (
			SELECT
				'table'::text AS object_kind,
				relation.oid AS object_oid,
				relation.relname AS object_name,
				relation.relowner AS owner_oid,
				relation.relacl AS object_acl
			  FROM pg_class AS relation
			  JOIN pg_namespace AS namespace
			    ON namespace.oid = relation.relnamespace
			 WHERE namespace.nspname = 'identity'
			   AND relation.relkind IN ('r', 'p')
			   AND relation.relname IN (
			       'idempotency_responses',
			       'broker_echo_replays',
			       'broker_echo_replay_policy'
			   )
		),
		broker_functions AS (
			SELECT
				'function'::text AS object_kind,
				procedure.oid AS object_oid,
				procedure.proname || '(' ||
					oidvectortypes(procedure.proargtypes) || ')' AS object_name,
				procedure.proowner AS owner_oid,
				procedure.proacl AS object_acl
			  FROM pg_proc AS procedure
			  JOIN pg_namespace AS namespace
			    ON namespace.oid = procedure.pronamespace
			 WHERE namespace.nspname = 'identity'
			   AND procedure.proname LIKE '%broker_echo%'
		),
		all_objects AS (
			SELECT object_kind, object_oid, object_name, owner_oid, object_acl
			  FROM broker_objects
			UNION ALL
			SELECT object_kind, object_oid, object_name, owner_oid, object_acl
			  FROM broker_functions
		)
		SELECT
			object.object_kind,
			object.object_name,
			pg_get_userbyid(object.owner_oid),
			CASE
				WHEN privilege.grantee = 0 THEN 'PUBLIC'
				ELSE pg_get_userbyid(privilege.grantee)
			END,
			privilege.privilege_type,
			privilege.is_grantable
		  FROM all_objects AS object
		  CROSS JOIN LATERAL aclexplode(
		      COALESCE(
		          object.object_acl,
		          acldefault(
		              CASE object.object_kind
		                  WHEN 'table' THEN 'r'::"char"
		                  ELSE 'f'::"char"
		              END,
		              object.owner_oid
		          )
		      )
		  ) AS privilege
		 ORDER BY
		       object.object_kind,
		       object.object_name,
		       privilege.grantee,
		       privilege.privilege_type`,
	)
	if err != nil {
		t.Fatalf("inspect complete raw broker-echo ACLs: %v", err)
	}
	defer rows.Close()

	tableObjects := make(map[string]bool)
	functionObjects := make(map[string]bool)
	ownerEntries := make(map[string]bool)
	for rows.Next() {
		var (
			objectKind string
			objectName string
			owner      string
			grantee    string
			privilege  string
			grantable  bool
		)
		if err := rows.Scan(
			&objectKind,
			&objectName,
			&owner,
			&grantee,
			&privilege,
			&grantable,
		); err != nil {
			t.Fatalf("scan raw broker-echo ACL: %v", err)
		}
		objectKey := objectKind + ":" + objectName
		if objectKind == "table" {
			tableObjects[objectName] = true
			if grantee != owner {
				t.Fatalf(
					"raw table ACL %s grants %s to %s (hostile=%s)",
					objectName,
					privilege,
					grantee,
					hostileRole,
				)
			}
		} else {
			functionObjects[objectName] = true
			apiEndpoint := strings.HasPrefix(
				objectName,
				"claim_broker_echo_response(",
			) || strings.HasPrefix(
				objectName,
				"purge_expired_broker_echo_replays(",
			) || objectName == "broker_echo_replay_coverage()"
			if privilege != "EXECUTE" ||
				(grantee != owner &&
					!(apiEndpoint && grantee == "platformgo_api")) {
				t.Fatalf(
					"raw function ACL %s grants %s to %s (hostile=%s)",
					objectName,
					privilege,
					grantee,
					hostileRole,
				)
			}
			if grantee == "platformgo_api" && grantable {
				t.Fatalf("API can grant EXECUTE on %s", objectName)
			}
		}
		if grantee == owner {
			ownerEntries[objectKey] = true
		}
		if grantee == hostileRole {
			t.Fatalf("unlisted hostile role retained raw ACL on %s", objectKey)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate raw broker-echo ACLs: %v", err)
	}
	if len(tableObjects) != expectedTables ||
		len(functionObjects) != expectedFunctions {
		t.Fatalf(
			"raw ACL object inventory tables=%v functions=%v, want counts %d/%d",
			tableObjects,
			functionObjects,
			expectedTables,
			expectedFunctions,
		)
	}
	if len(ownerEntries) != expectedTables+expectedFunctions {
		t.Fatalf(
			"raw ACL owner entries=%v, want one for every broker object",
			ownerEntries,
		)
	}
}

func assertBrokerEchoHostileAccessDenied(
	t *testing.T,
	pool *pgxpool.Pool,
	hostileRole string,
	policyExists bool,
	coverageExists bool,
) {
	t.Helper()
	for _, table := range append(
		[]string{
			"identity.idempotency_responses",
			"identity.broker_echo_replays",
		},
		func() []string {
			if policyExists {
				return []string{"identity.broker_echo_replay_policy"}
			}
			return nil
		}()...,
	) {
		assertBrokerEchoHostileStatementDenied(
			t,
			pool,
			hostileRole,
			"SELECT 1 FROM "+table+" LIMIT 0",
		)
		assertBrokerEchoHostileStatementDenied(
			t,
			pool,
			hostileRole,
			"TRUNCATE "+table,
		)
	}
	for _, statement := range []string{
		`SELECT identity.claim_broker_echo(
			'urn:xb:apikey:hostile',
			'hostile-key',
			decode(repeat('91', 32), 'hex'),
			'019fa562-2c4f-4b7e-8db3-ec1fc8d53991',
			statement_timestamp() + interval '1 day'
		)`,
		`SELECT response_status
		   FROM identity.claim_broker_echo_response(
			'urn:xb:apikey:hostile',
			decode(repeat('92', 32), 'hex'),
			decode(repeat('93', 32), 'hex'),
			200,
			'{"Content-Type":["application/json"]}',
			convert_to(
				'{"id":"019fa562-2c4f-4b7e-8db3-ec1fc8d53992"}' ||
					chr(10),
				'UTF8'
			)
		   )`,
		`SELECT identity.purge_expired_broker_echo_replays(1)`,
	} {
		assertBrokerEchoHostileStatementDenied(
			t,
			pool,
			hostileRole,
			statement,
		)
	}
	if coverageExists {
		for _, statement := range []string{
			`SELECT total_rows
			   FROM identity.broker_echo_replay_coverage()`,
			`SELECT identity.valid_broker_echo_response(
				decode(repeat('94', 32), 'hex'),
				200,
				'{"Content-Type":["application/json"]}',
				convert_to(
					'{"id":"019fa562-2c4f-4b7e-8db3-ec1fc8d53994"}' ||
						chr(10),
					'UTF8'
				),
				statement_timestamp(),
				statement_timestamp() + interval '1 day'
			)`,
		} {
			assertBrokerEchoHostileStatementDenied(
				t,
				pool,
				hostileRole,
				statement,
			)
		}
	}
}

func assertBrokerEchoHostileStatementDenied(
	t *testing.T,
	pool *pgxpool.Pool,
	hostileRole string,
	statement string,
) {
	t.Helper()
	tx, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(
		context.Background(),
		"SET LOCAL ROLE "+quoteBrokerEchoIdentifier(hostileRole),
	); err != nil {
		t.Fatalf("assume unlisted hostile role: %v", err)
	}
	_, err = tx.Exec(context.Background(), statement)
	requireBrokerEchoPostgresCode(t, err, "42501")
}

func restoreBrokerEchoHostileRole(
	ctx context.Context,
	pool *pgxpool.Pool,
	ownerIdentifier string,
	hostileIdentifier string,
) error {
	restoreSQL := fmt.Sprintf(`
		ALTER DEFAULT PRIVILEGES FOR ROLE %[1]s IN SCHEMA identity
			REVOKE ALL PRIVILEGES ON FUNCTIONS FROM %[2]s;
		ALTER DEFAULT PRIVILEGES FOR ROLE %[1]s IN SCHEMA identity
			REVOKE ALL PRIVILEGES ON TABLES FROM %[2]s;
		REVOKE ALL PRIVILEGES ON ALL TABLES IN SCHEMA identity FROM %[2]s;
		REVOKE ALL PRIVILEGES ON ALL FUNCTIONS IN SCHEMA identity FROM %[2]s;
		REVOKE USAGE ON SCHEMA identity FROM %[2]s;
		DROP OWNED BY %[2]s;
		DROP ROLE %[2]s`,
		ownerIdentifier,
		hostileIdentifier,
	)
	_, err := pool.Exec(ctx, restoreSQL)
	return err
}

func quoteBrokerEchoIdentifier(identifier string) string {
	return `"` + strings.ReplaceAll(identifier, `"`, `""`) + `"`
}

func seedBrokerEchoIntermediateReplay(
	t *testing.T,
	pool *pgxpool.Pool,
	scope string,
	id string,
) ([]byte, time.Time, time.Time) {
	t.Helper()
	return seedBrokerEchoIntermediateReplayShape(
		t,
		pool,
		scope,
		[]byte(`{"id":"`+id+"\"}\n"),
		"24 hours",
	)
}

func seedBrokerEchoIntermediateReplayShape(
	t *testing.T,
	pool *pgxpool.Pool,
	scope string,
	responseBody []byte,
	lifetime string,
) ([]byte, time.Time, time.Time) {
	t.Helper()
	var (
		body      []byte
		createdAt time.Time
		expiresAt time.Time
	)
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO identity.broker_echo_replays (
			scope,
			idempotency_key_hash,
			request_hash,
			response_status,
			response_headers,
			response_body,
			created_at,
			expires_at
		) VALUES (
			$1,
			sha256(convert_to($1, 'UTF8')),
			decode(repeat('e3', 32), 'hex'),
			200,
			'{"Content-Type":["application/json"]}'::jsonb,
			$2,
			statement_timestamp() - interval '1 hour',
			statement_timestamp() - interval '1 hour' + $3::interval
		)
		RETURNING response_body, created_at, expires_at`,
		scope,
		responseBody,
		lifetime,
	).Scan(&body, &createdAt, &expiresAt); err != nil {
		t.Fatalf("seed intermediate broker-echo replay: %v", err)
	}
	return body, createdAt, expiresAt
}

func assertBrokerEchoIntermediateReplayPreserved(
	t *testing.T,
	pool *pgxpool.Pool,
	scope string,
	expectedBody []byte,
	expectedCreatedAt time.Time,
	expectedExpiresAt time.Time,
) {
	t.Helper()
	var (
		body         []byte
		createdAt    time.Time
		expiresAt    time.Time
		exactRequest bool
		exactStatus  bool
		exactHeaders bool
	)
	if err := pool.QueryRow(context.Background(), `
		SELECT
			response_body,
			created_at,
			expires_at,
			request_hash = decode(repeat('e3', 32), 'hex'),
			response_status = 200,
			response_headers =
				'{"Content-Type":["application/json"]}'::jsonb
		  FROM identity.broker_echo_replays
		 WHERE scope = $1`,
		scope,
	).Scan(
		&body,
		&createdAt,
		&expiresAt,
		&exactRequest,
		&exactStatus,
		&exactHeaders,
	); err != nil {
		t.Fatalf("read preserved intermediate broker-echo replay: %v", err)
	}
	if !slices.Equal(body, expectedBody) ||
		!createdAt.Equal(expectedCreatedAt) ||
		!expiresAt.Equal(expectedExpiresAt) ||
		!exactRequest ||
		!exactStatus ||
		!exactHeaders {
		t.Fatalf(
			"preserved replay body=%q created=%s expires=%s "+
				"request=%t status=%t headers=%t",
			body,
			createdAt,
			expiresAt,
			exactRequest,
			exactStatus,
			exactHeaders,
		)
	}
}

func assertBrokerEchoReplaySnapshot(
	t *testing.T,
	pool *pgxpool.Pool,
	scope string,
	expectedKeyHash []byte,
	expectedRequestHash []byte,
	expectedStatus int,
	expectedHeaders []byte,
	expectedBody []byte,
	expectedCreatedAt time.Time,
	expectedExpiresAt time.Time,
	expectedPostgresTimeAuthority *bool,
) {
	t.Helper()
	var (
		keyHash     []byte
		requestHash []byte
		status      int
		headers     []byte
		body        []byte
		createdAt   time.Time
		expiresAt   time.Time
		marker      bool
		err         error
	)
	if expectedPostgresTimeAuthority == nil {
		err = pool.QueryRow(context.Background(), `
			SELECT
				idempotency_key_hash,
				request_hash,
				response_status,
				response_headers,
				response_body,
				created_at,
				expires_at
			  FROM identity.broker_echo_replays
			 WHERE scope = $1`,
			scope,
		).Scan(
			&keyHash,
			&requestHash,
			&status,
			&headers,
			&body,
			&createdAt,
			&expiresAt,
		)
	} else {
		err = pool.QueryRow(context.Background(), `
			SELECT
				idempotency_key_hash,
				request_hash,
				response_status,
				response_headers,
				response_body,
				created_at,
				expires_at,
				postgres_time_authority
			  FROM identity.broker_echo_replays
			 WHERE scope = $1`,
			scope,
		).Scan(
			&keyHash,
			&requestHash,
			&status,
			&headers,
			&body,
			&createdAt,
			&expiresAt,
			&marker,
		)
	}
	if err != nil {
		t.Fatalf("read broker-echo replay snapshot: %v", err)
	}
	markerMatches := expectedPostgresTimeAuthority == nil ||
		marker == *expectedPostgresTimeAuthority
	if !slices.Equal(keyHash, expectedKeyHash) ||
		!slices.Equal(requestHash, expectedRequestHash) ||
		status != expectedStatus ||
		!slices.Equal(headers, expectedHeaders) ||
		!slices.Equal(body, expectedBody) ||
		!createdAt.Equal(expectedCreatedAt) ||
		!expiresAt.Equal(expectedExpiresAt) ||
		!markerMatches {
		t.Fatalf(
			"broker-echo replay snapshot key=%x request=%x status=%d "+
				"headers=%s body=%q created=%s expires=%s marker=%t",
			keyHash,
			requestHash,
			status,
			headers,
			body,
			createdAt,
			expiresAt,
			marker,
		)
	}
}

func brokerEchoMigrationJournalSnapshot(
	t *testing.T,
	pool *pgxpool.Pool,
) string {
	t.Helper()
	var snapshot string
	if err := pool.QueryRow(context.Background(), `
		SELECT COALESCE(
			jsonb_agg(
				jsonb_build_array(
					filename,
					encode(checksum, 'hex'),
					applied_at
				)
				ORDER BY filename
			),
			'[]'::jsonb
		)::text
		  FROM engine.schema_migrations`,
	).Scan(&snapshot); err != nil {
		t.Fatalf("snapshot migration journal: %v", err)
	}
	return snapshot
}

func brokerEchoReplayRowsSnapshot(
	t *testing.T,
	pool *pgxpool.Pool,
	includePostgresTimeAuthority bool,
) string {
	t.Helper()
	var (
		query    string
		snapshot string
	)
	if includePostgresTimeAuthority {
		query = `
			SELECT COALESCE(
				jsonb_agg(
					jsonb_build_array(
						scope,
						encode(idempotency_key_hash, 'hex'),
						encode(request_hash, 'hex'),
						response_status,
						response_headers,
						encode(response_body, 'hex'),
						created_at,
						expires_at,
						postgres_time_authority
					)
					ORDER BY scope, idempotency_key_hash
				),
				'[]'::jsonb
			)::text
			  FROM identity.broker_echo_replays`
	} else {
		query = `
			SELECT COALESCE(
				jsonb_agg(
					jsonb_build_array(
						scope,
						encode(idempotency_key_hash, 'hex'),
						encode(request_hash, 'hex'),
						response_status,
						response_headers,
						encode(response_body, 'hex'),
						created_at,
						expires_at
					)
					ORDER BY scope, idempotency_key_hash
				),
				'[]'::jsonb
			)::text
			  FROM identity.broker_echo_replays`
	}
	if err := pool.QueryRow(context.Background(), query).Scan(&snapshot); err != nil {
		t.Fatalf("snapshot broker-echo replay rows: %v", err)
	}
	return snapshot
}

func brokerEchoInsertGuardCatalogSnapshot(
	t *testing.T,
	pool *pgxpool.Pool,
) string {
	t.Helper()
	var snapshot string
	if err := pool.QueryRow(context.Background(), `
		SELECT jsonb_build_object(
			'function',
			pg_get_functiondef(
				'identity.guard_broker_echo_replay_insert()'::regprocedure
			),
			'trigger',
			(
				SELECT pg_get_triggerdef(trigger.oid, true)
				  FROM pg_trigger AS trigger
				 WHERE trigger.tgrelid =
				       'identity.broker_echo_replays'::regclass
				   AND trigger.tgname =
				       'broker_echo_replays_require_postgres_time_authority'
				   AND NOT trigger.tgisinternal
			)
		)::text`,
	).Scan(&snapshot); err != nil {
		t.Fatalf("snapshot broker-echo insert fence catalog: %v", err)
	}
	return snapshot
}

func brokerEchoReplayTriggerCatalogSnapshot(
	t *testing.T,
	pool *pgxpool.Pool,
) string {
	t.Helper()
	var snapshot string
	if err := pool.QueryRow(context.Background(), `
		SELECT COALESCE(
			jsonb_agg(
				jsonb_build_array(
					trigger.tgname,
					trigger.tgenabled,
					trigger.tgtype,
					pg_get_triggerdef(trigger.oid, true)
				)
				ORDER BY trigger.tgname
			),
			'[]'::jsonb
		)::text
		  FROM pg_trigger AS trigger
		 WHERE trigger.tgrelid =
		       'identity.broker_echo_replays'::regclass
		   AND NOT trigger.tgisinternal`,
	).Scan(&snapshot); err != nil {
		t.Fatalf("snapshot broker-echo replay trigger catalog: %v", err)
	}
	return snapshot
}

func assertBrokerEchoSnapshotsEqual(
	t *testing.T,
	name string,
	before string,
	after string,
) {
	t.Helper()
	if before != after {
		t.Fatalf("%s changed\nbefore: %s\nafter:  %s", name, before, after)
	}
}

func applyBrokerEchoPreviousSchema(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	resetDurableSchemas(t, pool)
	if err := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, brokerEchoPreviousMigration),
	).Migrate(context.Background()); err != nil {
		t.Fatalf("apply broker-echo previous schema: %v", err)
	}
}

func applyBrokerEchoCurrentSchema(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if err := migrateBrokerEchoCurrentSchema(pool); err != nil {
		t.Fatalf("apply broker-echo exact replay migration: %v", err)
	}
}

func migrateBrokerEchoCapacitySchema(
	t *testing.T,
	pool *pgxpool.Pool,
) error {
	t.Helper()
	return platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, brokerEchoCapacityMigration),
	).Migrate(context.Background())
}

func migrateBrokerEchoIntegritySchema(
	t *testing.T,
	pool *pgxpool.Pool,
) error {
	t.Helper()
	return platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, brokerEchoIntegrityMigration),
	).Migrate(context.Background())
}

func migrateBrokerEchoFinalGuardSchema(
	t *testing.T,
	pool *pgxpool.Pool,
) error {
	t.Helper()
	return platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, brokerEchoFinalGuardMigration),
	).Migrate(context.Background())
}

func migrateBrokerEchoCurrentSchema(pool *pgxpool.Pool) error {
	return platformpostgres.NewMigrator(
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).Migrate(context.Background())
}

func assertBrokerEchoMigrationAbsent(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	var (
		tableExists      bool
		migrationApplied bool
	)
	if err := pool.QueryRow(context.Background(), `
		SELECT
			to_regclass('identity.broker_echo_replays') IS NOT NULL,
			EXISTS (
				SELECT 1
				  FROM engine.schema_migrations
				 WHERE filename = $1
			)`,
		brokerEchoReplayMigration,
	).Scan(&tableExists, &migrationApplied); err != nil {
		t.Fatal(err)
	}
	if tableExists || migrationApplied {
		t.Fatalf(
			"failed migration partially applied table=%t history=%t",
			tableExists,
			migrationApplied,
		)
	}
}

func requireBrokerEchoPostgresCode(t *testing.T, err error, code string) {
	t.Helper()
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) || postgresError.Code != code {
		t.Fatalf("PostgreSQL error = %v, want SQLSTATE %s", err, code)
	}
}
