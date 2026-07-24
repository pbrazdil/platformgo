package postgres_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
	"github.com/upcomers-org/platformgo/internal/engine"
)

func TestCommandJournalRejectsConflictsAndReplaysCompletedResponse(t *testing.T) {
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := platformpostgres.NewMigrator(
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	journal := platformpostgres.NewCommandJournal(pool)
	commandID := engine.IDFromSequence(engine.ID{}, 1)
	request := platformpostgres.BeginCommandRequest{
		Scope:           "account:account-1",
		IdempotencyKey:  "deposit-1",
		RequestHash:     sha256.Sum256([]byte("canonical deposit request")),
		CommandID:       commandID,
		AccountID:       "account-1",
		AccountSequence: 1,
		CommandType:     "deposit",
		SchemaVersion:   1,
		CanonicalPayload: []byte(
			`{"accountId":"account-1","amount":"10","currency":"USDC"}`,
		),
		LogicalTime: time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC),
		ExpiresAt:   time.Date(2026, time.July, 25, 12, 0, 0, 0, time.UTC),
	}
	first, err := journal.Begin(context.Background(), request)
	if err != nil {
		t.Fatalf("first Begin: %v", err)
	}
	if !first.Created || first.CommandID != commandID ||
		first.State != platformpostgres.IdempotencyInProgress {
		t.Fatalf("first Begin = %+v", first)
	}

	repeated, err := journal.Begin(context.Background(), request)
	if err != nil {
		t.Fatalf("repeated Begin: %v", err)
	}
	if repeated.Created || repeated.CommandID != commandID ||
		repeated.State != platformpostgres.IdempotencyInProgress {
		t.Fatalf("repeated Begin = %+v", repeated)
	}

	conflict := request
	conflict.RequestHash = sha256.Sum256([]byte("different request"))
	if _, err := journal.Begin(context.Background(), conflict); !errors.Is(
		err,
		platformpostgres.ErrIdempotencyConflict,
	) {
		t.Fatalf("conflicting Begin error = %v, want ErrIdempotencyConflict", err)
	}

	completion := platformpostgres.CompleteCommandRequest{
		CommandID: commandID,
		Status:    platformpostgres.CommandCompleted,
		Result:    []byte(`{"balance":"10"}`),
		Response: platformpostgres.StoredResponse{
			Status:  201,
			Headers: []byte(`{"content-type":["application/json"]}`),
			Body:    []byte(`{"balance":"10"}`),
		},
	}
	if err := journal.Complete(context.Background(), completion); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	completed, err := journal.Begin(context.Background(), request)
	if err != nil {
		t.Fatalf("completed Begin: %v", err)
	}
	if completed.Created ||
		completed.State != platformpostgres.IdempotencyCompleted ||
		completed.Response.Status != completion.Response.Status ||
		string(completed.Response.Headers) != string(completion.Response.Headers) ||
		string(completed.Response.Body) != string(completion.Response.Body) {
		t.Fatalf("completed replay = %+v", completed)
	}

	var idempotencyRows int
	var commandRows int
	if err := pool.QueryRow(
		context.Background(),
		"SELECT count(*) FROM trading.idempotency_records",
	).Scan(&idempotencyRows); err != nil {
		t.Fatalf("count idempotency records: %v", err)
	}
	if err := pool.QueryRow(
		context.Background(),
		"SELECT count(*) FROM trading.commands",
	).Scan(&commandRows); err != nil {
		t.Fatalf("count commands: %v", err)
	}
	if idempotencyRows != 1 || commandRows != 1 {
		t.Fatalf(
			"journal rows = idempotency %d, commands %d, want 1 and 1",
			idempotencyRows,
			commandRows,
		)
	}
}
