package nats_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestRetryNATSFixtureMigrationRetriesLockNotAvailable(t *testing.T) {
	attempts := 0
	err := retryNATSFixtureMigration(
		context.Background(),
		func(context.Context) error {
			attempts++
			if attempts == 1 {
				return fmt.Errorf(
					"fixture catalog preflight: %w",
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

func TestRetryNATSFixtureMigrationDoesNotRetryOtherErrors(t *testing.T) {
	expected := errors.New("migration checksum mismatch")
	attempts := 0
	err := retryNATSFixtureMigration(
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

func TestRetryNATSFixtureMigrationDoesNotMaskMultiCauseError(t *testing.T) {
	restoreError := errors.New("restore lock timeout")
	migrationError := fmt.Errorf(
		"acquire migration lock: %w; restore session setting: %w",
		&pgconn.PgError{Code: "55P03"},
		restoreError,
	)
	attempts := 0
	err := retryNATSFixtureMigration(
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

func TestRetryNATSFixtureMigrationHonorsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	attempts := 0
	err := retryNATSFixtureMigration(ctx, func(context.Context) error {
		attempts++
		cancel()
		return &pgconn.PgError{Code: "55P03"}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("migration error = %v, want context cancellation", err)
	}
	if attempts != 1 {
		t.Fatalf("migration attempts = %d, want 1", attempts)
	}
}
