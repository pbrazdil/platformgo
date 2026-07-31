package compatibility_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestRetryCompatibilityFixtureMigrationRetriesLockNotAvailable(
	t *testing.T,
) {
	attempts := 0
	err := retryCompatibilityFixtureMigration(
		context.Background(),
		func(context.Context) error {
			attempts++
			if attempts == 1 {
				return fmt.Errorf(
					"migrate %s: execute: %w",
					compatibilityContentionMigrationName,
					&pgconn.PgError{Code: "55P03"},
				)
			}
			return nil
		},
	)
	if err != nil {
		t.Fatalf("retry lock-not-available migration: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("migration attempts = %d, want 2", attempts)
	}
}

func TestRetryCompatibilityFixtureMigrationDoesNotRetryOtherErrors(
	t *testing.T,
) {
	expected := errors.New("migration checksum mismatch")
	attempts := 0
	err := retryCompatibilityFixtureMigration(
		context.Background(),
		func(context.Context) error {
			attempts++
			return expected
		},
	)
	if !errors.Is(err, expected) {
		t.Fatalf("migration error = %v, want %v", err, expected)
	}
	if attempts != 1 {
		t.Fatalf("migration attempts = %d, want 1", attempts)
	}
}

func TestRetryCompatibilityFixtureMigrationDoesNotRetryOtherSQLState(
	t *testing.T,
) {
	attempts := 0
	err := retryCompatibilityFixtureMigration(
		context.Background(),
		func(context.Context) error {
			attempts++
			return fmt.Errorf(
				"migrate %s: execute: %w",
				compatibilityContentionMigrationName,
				&pgconn.PgError{Code: "55000"},
			)
		},
	)
	if err == nil {
		t.Fatal("other SQLSTATE error = nil, want failure")
	}
	if attempts != 1 {
		t.Fatalf("migration attempts = %d, want 1", attempts)
	}
}

func TestRetryCompatibilityFixtureMigrationDoesNotMaskMultiCauseError(
	t *testing.T,
) {
	restoreError := errors.New("restore lock timeout")
	migrationError := fmt.Errorf(
		"migrate %s: execute: %w",
		compatibilityContentionMigrationName,
		fmt.Errorf(
			"acquire migration lock: %w; restore session setting: %w",
			&pgconn.PgError{Code: "55P03"},
			restoreError,
		),
	)
	attempts := 0
	err := retryCompatibilityFixtureMigration(
		context.Background(),
		func(context.Context) error {
			attempts++
			return migrationError
		},
	)
	if !errors.Is(err, restoreError) {
		t.Fatalf("migration error = %v, want restore error", err)
	}
	if attempts != 1 {
		t.Fatalf("migration attempts = %d, want 1", attempts)
	}
}

func TestRetryCompatibilityFixtureMigrationHonorsContextCancellation(
	t *testing.T,
) {
	ctx, cancel := context.WithCancel(context.Background())
	attempts := 0
	err := retryCompatibilityFixtureMigration(
		ctx,
		func(context.Context) error {
			attempts++
			cancel()
			return fmt.Errorf(
				"migrate %s: execute: %w",
				compatibilityContentionMigrationName,
				&pgconn.PgError{Code: "55P03"},
			)
		},
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("migration error = %v, want context cancellation", err)
	}
	if attempts != 1 {
		t.Fatalf("migration attempts = %d, want 1", attempts)
	}
}

func TestRetryCompatibilityFixtureMigrationDoesNotRetryCommitError(
	t *testing.T,
) {
	attempts := 0
	err := retryCompatibilityFixtureMigration(
		context.Background(),
		func(context.Context) error {
			attempts++
			return fmt.Errorf(
				"migrate %s: commit: %w",
				compatibilityContentionMigrationName,
				&pgconn.PgError{Code: "55P03"},
			)
		},
	)
	if err == nil {
		t.Fatal("commit error = nil, want failure")
	}
	if attempts != 1 {
		t.Fatalf("migration attempts = %d, want 1", attempts)
	}
}

func TestRetryCompatibilityFixtureMigrationDoesNotRetryOtherMigration(
	t *testing.T,
) {
	attempts := 0
	err := retryCompatibilityFixtureMigration(
		context.Background(),
		func(context.Context) error {
			attempts++
			return fmt.Errorf(
				"migrate 20260730000400_phase3_broker_funding_acl.up.sql: "+
					"execute: %w",
				&pgconn.PgError{Code: "55P03"},
			)
		},
	)
	if err == nil {
		t.Fatal("other migration error = nil, want failure")
	}
	if attempts != 1 {
		t.Fatalf("migration attempts = %d, want 1", attempts)
	}
}

func TestCompatibilityFixtureProvisioningIsNotRetried(t *testing.T) {
	migrationAttempts := 0
	provisionAttempts := 0
	provisionError := &pgconn.PgError{Code: "55P03"}
	err := migrateAndProvisionCompatibilityFixtureWith(
		context.Background(),
		func(context.Context) error {
			migrationAttempts++
			return nil
		},
		func(context.Context) error {
			provisionAttempts++
			return provisionError
		},
	)
	if !errors.Is(err, provisionError) {
		t.Fatalf("provision error = %v, want %v", err, provisionError)
	}
	if migrationAttempts != 1 || provisionAttempts != 1 {
		t.Fatalf(
			"migration/provision attempts = %d/%d, want 1/1",
			migrationAttempts,
			provisionAttempts,
		)
	}
}
