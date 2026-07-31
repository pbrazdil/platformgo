// Package postgres owns PostgreSQL-specific durable execution.
package postgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/upcomers-org/platformgo/internal/engine"
)

var (
	// ErrMigrationChecksumMismatch means an applied filename no longer has its
	// recorded immutable contents.
	ErrMigrationChecksumMismatch = errors.New("migration checksum mismatch")
	// ErrDatabaseSchemaAhead means the database contains an applied migration
	// unknown to this binary.
	ErrDatabaseSchemaAhead = errors.New("database schema is ahead of this binary")
	// ErrDatabaseSchemaBehind means this binary expects a migration that the
	// database has not applied yet.
	ErrDatabaseSchemaBehind = errors.New("database schema is behind this binary")
	// ErrUnsupportedPostgresVersion means the server is older than the
	// repository's supported PostgreSQL floor.
	ErrUnsupportedPostgresVersion = errors.New("unsupported PostgreSQL version")
)

const (
	// MinimumPostgresMajorVersion is the oldest PostgreSQL release supported by
	// the platform.
	MinimumPostgresMajorVersion = 19
	// MinimumPostgres19BetaVersion rejects development snapshots and the
	// superseded first beta while PostgreSQL 19 is still prerelease software.
	MinimumPostgres19BetaVersion = 2
	migrationAdvisoryLockKey     = int64(0x504c4154474f)
	migrationLockTimeout         = "5s"
)

const runtimeRoleSafetyPreflight = `
DO $$
DECLARE
    required_role text;
BEGIN
    FOREACH required_role IN ARRAY ARRAY[
        'platformgo_api',
        'platformgo_engine',
        'platformgo_outbox',
        'platformgo_projector',
        'platformgo_realtime',
        'platformgo_realtime_repair'
    ]
    LOOP
        IF NOT EXISTS (
            SELECT 1
              FROM pg_roles
             WHERE rolname = required_role
               AND NOT rolcanlogin
               AND NOT rolsuper
               AND NOT rolcreatedb
               AND NOT rolcreaterole
               AND NOT rolreplication
               AND NOT rolbypassrls
               AND NOT EXISTS (
                   SELECT 1
                     FROM pg_auth_members AS membership
                    WHERE membership.member = pg_roles.oid
               )
        ) THEN
            RAISE EXCEPTION
                'required pre-provisioned runtime role % is missing or unsafe',
                required_role
                USING ERRCODE = '42501';
        END IF;
    END LOOP;
END;
$$`

var migrationNamePattern = regexp.MustCompile(`^[0-9]{14}_[a-z0-9_]+\.up\.sql$`)

// Migrator applies an immutable forward migration filesystem under one global
// PostgreSQL advisory lock.
type Migrator struct {
	pool       *pgxpool.Pool
	migrations fs.FS
}

// NewMigrator binds a connection pool and migration filesystem.
func NewMigrator(pool *pgxpool.Pool, migrations fs.FS) *Migrator {
	return &Migrator{pool: pool, migrations: migrations}
}

// MigrateAndProvision applies migrations and explicitly binds the only engine
// shard before any API or engine runtime is allowed to serve traffic.
func (migrator *Migrator) MigrateAndProvision(
	ctx context.Context,
	shardID engine.ShardID,
) error {
	if err := migrator.Migrate(ctx); err != nil {
		return err
	}
	return migrator.ProvisionDeploymentShard(ctx, shardID)
}

// ProvisionDeploymentShard is the privileged, idempotent bootstrap operation
// for the initial single-shard deployment. Runtime roles are validation-only.
func (migrator *Migrator) ProvisionDeploymentShard(
	ctx context.Context,
	shardID engine.ShardID,
) error {
	if migrator == nil || migrator.pool == nil {
		return errors.New("provision deployment shard: PostgreSQL pool is required")
	}
	tag, err := migrator.pool.Exec(ctx, `
		INSERT INTO engine.deployment_shard (singleton, shard_id)
		VALUES (true, $1)
		ON CONFLICT (singleton) DO NOTHING`,
		int64(shardID),
	)
	if err != nil {
		return fmt.Errorf("provision deployment shard %d: %w", shardID, err)
	}
	if tag.RowsAffected() > 1 {
		return fmt.Errorf("provision deployment shard %d: unexpected row count", shardID)
	}
	return ensureDeploymentShard(ctx, migrator.pool, shardID)
}

// Migrate verifies applied history and applies every new file in lexical order.
func (migrator *Migrator) Migrate(ctx context.Context) error {
	if migrator == nil || migrator.pool == nil {
		return errors.New("migrate: PostgreSQL pool is required")
	}
	if migrator.migrations == nil {
		return errors.New("migrate: migration filesystem is required")
	}
	files, err := readMigrationFiles(migrator.migrations)
	if err != nil {
		return err
	}

	connection, err := migrator.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("migrate: acquire PostgreSQL connection: %w", err)
	}
	defer connection.Release()

	var (
		postgresVersionNumber int
		postgresVersion       string
	)
	if versionErr := connection.QueryRow(
		ctx,
		`SELECT
			current_setting('server_version_num')::integer,
			current_setting('server_version')`,
	).Scan(
		&postgresVersionNumber,
		&postgresVersion,
	); versionErr != nil {
		return fmt.Errorf("migrate: read PostgreSQL server version: %w", versionErr)
	}
	if versionErr := validatePostgresVersion(
		postgresVersionNumber,
		postgresVersion,
	); versionErr != nil {
		return fmt.Errorf("migrate: %w", versionErr)
	}

	var previousLockTimeout string
	if lockTimeoutErr := connection.QueryRow(
		ctx,
		"SELECT current_setting('lock_timeout')",
	).Scan(&previousLockTimeout); lockTimeoutErr != nil {
		return fmt.Errorf(
			"migrate: read advisory lock timeout: %w",
			lockTimeoutErr,
		)
	}
	if _, lockTimeoutErr := connection.Exec(
		ctx,
		"SELECT set_config('lock_timeout', $1, false)",
		migrationLockTimeout,
	); lockTimeoutErr != nil {
		return fmt.Errorf(
			"migrate: configure advisory lock timeout: %w",
			lockTimeoutErr,
		)
	}
	_, lockErr := connection.Exec(
		ctx,
		"SELECT pg_advisory_lock($1)",
		migrationAdvisoryLockKey,
	)
	_, restoreLockTimeoutErr := connection.Exec(
		context.WithoutCancel(ctx),
		"SELECT set_config('lock_timeout', $1, false)",
		previousLockTimeout,
	)
	if lockErr != nil {
		if restoreLockTimeoutErr != nil {
			return fmt.Errorf(
				"migrate: acquire advisory lock: %w; restore lock timeout: %w",
				lockErr,
				restoreLockTimeoutErr,
			)
		}
		return fmt.Errorf("migrate: acquire advisory lock: %w", lockErr)
	}
	defer releaseMigrationLock(context.WithoutCancel(ctx), connection)
	if restoreLockTimeoutErr != nil {
		return fmt.Errorf(
			"migrate: restore advisory lock timeout: %w",
			restoreLockTimeoutErr,
		)
	}

	if _, metadataErr := connection.Exec(ctx, `
		CREATE SCHEMA IF NOT EXISTS engine;
		CREATE TABLE IF NOT EXISTS engine.schema_migrations (
			filename text PRIMARY KEY,
			checksum bytea NOT NULL CHECK (octet_length(checksum) = 32),
			applied_at timestamptz NOT NULL DEFAULT clock_timestamp()
		)`); metadataErr != nil {
		return fmt.Errorf("migrate: initialize migration metadata: %w", metadataErr)
	}

	applied, err := loadAppliedMigrations(ctx, connection)
	if err != nil {
		return err
	}
	available := make(map[string]migrationFile, len(files))
	for _, file := range files {
		available[file.name] = file
	}
	for _, name := range sortedMigrationNames(applied) {
		checksum := applied[name]
		file, ok := available[name]
		if !ok {
			return fmt.Errorf("%w: applied migration %s is unavailable", ErrDatabaseSchemaAhead, name)
		}
		if !bytes.Equal(checksum, file.checksum[:]) {
			return fmt.Errorf("%w: %s", ErrMigrationChecksumMismatch, name)
		}
	}

	hasPending := false
	for _, file := range files {
		if _, ok := applied[file.name]; !ok {
			hasPending = true
			break
		}
	}
	if hasPending {
		if _, err := connection.Exec(ctx, runtimeRoleSafetyPreflight); err != nil {
			return fmt.Errorf("migrate: runtime role safety preflight: %w", err)
		}
	}

	for _, file := range files {
		if _, ok := applied[file.name]; ok {
			continue
		}
		transaction, err := connection.Begin(ctx)
		if err != nil {
			return fmt.Errorf("migrate %s: begin: %w", file.name, err)
		}
		if _, err := transaction.Exec(
			ctx,
			"SELECT set_config('lock_timeout', $1, true)",
			migrationLockTimeout,
		); err != nil {
			_ = transaction.Rollback(ctx)
			return fmt.Errorf(
				"migrate %s: configure lock timeout: %w",
				file.name,
				err,
			)
		}
		if _, err := transaction.Exec(ctx, string(file.contents)); err != nil {
			_ = transaction.Rollback(ctx)
			return fmt.Errorf("migrate %s: execute: %w", file.name, err)
		}
		if _, err := transaction.Exec(
			ctx,
			`INSERT INTO engine.schema_migrations (filename, checksum)
			 VALUES ($1, $2)`,
			file.name,
			file.checksum[:],
		); err != nil {
			_ = transaction.Rollback(ctx)
			return fmt.Errorf("migrate %s: record checksum: %w", file.name, err)
		}
		if err := transaction.Commit(ctx); err != nil {
			return fmt.Errorf("migrate %s: commit: %w", file.name, err)
		}
	}
	return nil
}

// VerifyCurrent refuses to run against a database whose immutable migration
// set differs in either direction from the migrations embedded in this binary.
func (migrator *Migrator) VerifyCurrent(ctx context.Context) error {
	if migrator == nil || migrator.pool == nil {
		return errors.New("verify migrations: PostgreSQL pool is required")
	}
	if migrator.migrations == nil {
		return errors.New("verify migrations: migration filesystem is required")
	}
	files, err := readMigrationFiles(migrator.migrations)
	if err != nil {
		return err
	}
	connection, err := migrator.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("verify migrations: acquire PostgreSQL connection: %w", err)
	}
	defer connection.Release()

	var (
		postgresVersionNumber int
		postgresVersion       string
	)
	if versionReadErr := connection.QueryRow(
		ctx,
		`SELECT
			current_setting('server_version_num')::integer,
			current_setting('server_version')`,
	).Scan(
		&postgresVersionNumber,
		&postgresVersion,
	); versionReadErr != nil {
		return fmt.Errorf(
			"verify migrations: read PostgreSQL server version: %w",
			versionReadErr,
		)
	}
	if versionErr := validatePostgresVersion(
		postgresVersionNumber,
		postgresVersion,
	); versionErr != nil {
		return fmt.Errorf("verify migrations: %w", versionErr)
	}
	applied, err := loadRuntimeAppliedMigrations(ctx, connection)
	if err != nil {
		return fmt.Errorf("verify migrations: %w", err)
	}
	available := make(map[string]migrationFile, len(files))
	for _, file := range files {
		available[file.name] = file
	}
	for _, name := range sortedMigrationNames(applied) {
		checksum := applied[name]
		file, ok := available[name]
		if !ok {
			return fmt.Errorf(
				"%w: applied migration %s is unavailable",
				ErrDatabaseSchemaAhead,
				name,
			)
		}
		if !bytes.Equal(checksum, file.checksum[:]) {
			return fmt.Errorf("%w: %s", ErrMigrationChecksumMismatch, name)
		}
	}
	for _, file := range files {
		if _, ok := applied[file.name]; !ok {
			return fmt.Errorf("%w: %s", ErrDatabaseSchemaBehind, file.name)
		}
	}
	return nil
}

func sortedMigrationNames(applied map[string][]byte) []string {
	names := make([]string, 0, len(applied))
	for name := range applied {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func loadRuntimeAppliedMigrations(
	ctx context.Context,
	connection *pgxpool.Conn,
) (map[string][]byte, error) {
	rows, err := connection.Query(
		ctx,
		`SELECT filename, checksum
		   FROM engine.runtime_schema_migrations()`,
	)
	if err != nil {
		var postgresErr *pgconn.PgError
		if errors.As(err, &postgresErr) &&
			(postgresErr.Code == "3F000" || postgresErr.Code == "42883") {
			return nil, fmt.Errorf(
				"%w: runtime migration manifest is unavailable",
				ErrDatabaseSchemaBehind,
			)
		}
		return nil, err
	}
	defer rows.Close()
	applied := make(map[string][]byte)
	for rows.Next() {
		var name string
		var checksum []byte
		if err := rows.Scan(&name, &checksum); err != nil {
			return nil, err
		}
		applied[name] = append([]byte(nil), checksum...)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return applied, nil
}

func validatePostgresVersionNumber(versionNumber int) error {
	majorVersion := versionNumber / 10000
	if majorVersion < MinimumPostgresMajorVersion {
		return fmt.Errorf(
			"%w: server major version %d, minimum %d",
			ErrUnsupportedPostgresVersion,
			majorVersion,
			MinimumPostgresMajorVersion,
		)
	}
	return nil
}

func validatePostgresVersion(versionNumber int, version string) error {
	if err := validatePostgresVersionNumber(versionNumber); err != nil {
		return err
	}

	displayMajor, suffix, ok := splitPostgresDisplayVersion(version)
	numericMajor := versionNumber / 10000
	if !ok || displayMajor != numericMajor {
		return unsupportedPostgresVersion(
			versionNumber,
			version,
			"numeric and display versions disagree",
		)
	}
	numericMinor := versionNumber % 10000
	if stableMinor, stable := stablePostgresVersionMinor(suffix); stable {
		if stableMinor != numericMinor {
			return unsupportedPostgresVersion(
				versionNumber,
				version,
				"numeric and display versions disagree",
			)
		}
		return nil
	}

	if beta, found := postgresPrereleaseNumber(suffix, "beta"); found {
		if numericMinor != 0 {
			return unsupportedPostgresVersion(
				versionNumber,
				version,
				"numeric and display versions disagree",
			)
		}
		if numericMajor == MinimumPostgresMajorVersion &&
			beta >= MinimumPostgres19BetaVersion {
			return nil
		}
		return unsupportedPostgresVersion(
			versionNumber,
			version,
			"prerelease beta is not qualified",
		)
	}
	if _, found := postgresPrereleaseNumber(suffix, "rc"); found {
		if numericMinor != 0 {
			return unsupportedPostgresVersion(
				versionNumber,
				version,
				"numeric and display versions disagree",
			)
		}
		if numericMajor == MinimumPostgresMajorVersion {
			return nil
		}
		return unsupportedPostgresVersion(
			versionNumber,
			version,
			"future release candidate is not qualified",
		)
	}
	return unsupportedPostgresVersion(
		versionNumber,
		version,
		"development or unknown prerelease is not qualified",
	)
}

func splitPostgresDisplayVersion(version string) (int, string, bool) {
	normalized := strings.ToLower(strings.TrimSpace(version))
	fields := strings.Fields(normalized)
	if len(fields) == 0 {
		return 0, "", false
	}
	releaseToken := fields[0]
	majorEnd := 0
	for majorEnd < len(releaseToken) &&
		releaseToken[majorEnd] >= '0' &&
		releaseToken[majorEnd] <= '9' {
		majorEnd++
	}
	if majorEnd == 0 || majorEnd > 1 && releaseToken[0] == '0' {
		return 0, "", false
	}
	major, err := strconv.Atoi(releaseToken[:majorEnd])
	if err != nil {
		return 0, "", false
	}
	return major, releaseToken[majorEnd:], true
}

func stablePostgresVersionMinor(suffix string) (int, bool) {
	if len(suffix) < 2 || suffix[0] != '.' {
		return 0, false
	}
	return canonicalPostgresVersionNumber(suffix[1:], true)
}

func postgresPrereleaseNumber(
	suffix string,
	prefix string,
) (int, bool) {
	if !strings.HasPrefix(suffix, prefix) {
		return 0, false
	}
	digits := suffix[len(prefix):]
	return canonicalPostgresVersionNumber(digits, false)
}

func canonicalPostgresVersionNumber(
	digits string,
	allowZero bool,
) (int, bool) {
	if digits == "" || len(digits) > 1 && digits[0] == '0' {
		return 0, false
	}
	for _, digit := range digits {
		if digit < '0' || digit > '9' {
			return 0, false
		}
	}
	number, err := strconv.Atoi(digits)
	if err != nil || number < 0 || !allowZero && number == 0 {
		return 0, false
	}
	return number, true
}

func unsupportedPostgresVersion(
	versionNumber int,
	version string,
	detail string,
) error {
	return fmt.Errorf(
		"%w: server version %q (%d): %s; minimum PostgreSQL 19 Beta %d",
		ErrUnsupportedPostgresVersion,
		version,
		versionNumber,
		detail,
		MinimumPostgres19BetaVersion,
	)
}

func releaseMigrationLock(ctx context.Context, connection *pgxpool.Conn) {
	_, _ = connection.Exec(
		ctx,
		"SELECT pg_advisory_unlock($1)",
		migrationAdvisoryLockKey,
	)
}

type migrationFile struct {
	name     string
	contents []byte
	checksum [sha256.Size]byte
}

func readMigrationFiles(migrations fs.FS) ([]migrationFile, error) {
	entries, err := fs.ReadDir(migrations, ".")
	if err != nil {
		return nil, fmt.Errorf("read migrations: %w", err)
	}
	files := make([]migrationFile, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".sql" {
			continue
		}
		if !migrationNamePattern.MatchString(entry.Name()) {
			return nil, fmt.Errorf("invalid migration filename %q", entry.Name())
		}
		contents, err := fs.ReadFile(migrations, entry.Name())
		if err != nil {
			return nil, fmt.Errorf("read migration %s: %w", entry.Name(), err)
		}
		files = append(files, migrationFile{
			name:     entry.Name(),
			contents: contents,
			checksum: sha256.Sum256(contents),
		})
	}
	slices.SortFunc(files, func(left, right migrationFile) int {
		return bytes.Compare([]byte(left.name), []byte(right.name))
	})
	return files, nil
}

func loadAppliedMigrations(
	ctx context.Context,
	connection *pgxpool.Conn,
) (map[string][]byte, error) {
	rows, err := connection.Query(
		ctx,
		`SELECT filename, checksum
		   FROM engine.schema_migrations
		  ORDER BY filename`,
	)
	if err != nil {
		return nil, fmt.Errorf("migrate: read applied migrations: %w", err)
	}
	defer rows.Close()

	applied := make(map[string][]byte)
	for rows.Next() {
		var name string
		var checksum []byte
		if err := rows.Scan(&name, &checksum); err != nil {
			return nil, fmt.Errorf("migrate: scan applied migration: %w", err)
		}
		applied[name] = append([]byte(nil), checksum...)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("migrate: iterate applied migrations: %w", err)
	}
	return applied, nil
}
