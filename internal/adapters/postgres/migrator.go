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
	// ErrUnsupportedPostgresVersion means the server is older than the
	// repository's supported PostgreSQL floor.
	ErrUnsupportedPostgresVersion = errors.New("unsupported PostgreSQL version")
)

const (
	// MinimumPostgresMajorVersion is the oldest PostgreSQL release supported by
	// the platform.
	MinimumPostgresMajorVersion = 17
	migrationAdvisoryLockKey    = int64(0x504c4154474f)
)

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

	var postgresVersionNumber int
	if versionErr := connection.QueryRow(
		ctx,
		"SELECT current_setting('server_version_num')::integer",
	).Scan(&postgresVersionNumber); versionErr != nil {
		return fmt.Errorf("migrate: read PostgreSQL server version: %w", versionErr)
	}
	if versionErr := validatePostgresVersionNumber(postgresVersionNumber); versionErr != nil {
		return fmt.Errorf("migrate: %w", versionErr)
	}

	if _, lockErr := connection.Exec(
		ctx,
		"SELECT pg_advisory_lock($1)",
		migrationAdvisoryLockKey,
	); lockErr != nil {
		return fmt.Errorf("migrate: acquire advisory lock: %w", lockErr)
	}
	defer releaseMigrationLock(context.WithoutCancel(ctx), connection)

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
	for name, checksum := range applied {
		file, ok := available[name]
		if !ok {
			return fmt.Errorf("%w: applied migration %s is unavailable", ErrDatabaseSchemaAhead, name)
		}
		if !bytes.Equal(checksum, file.checksum[:]) {
			return fmt.Errorf("%w: %s", ErrMigrationChecksumMismatch, name)
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
