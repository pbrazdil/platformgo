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
		OutboxSubject: "engine.input.7.command.v1",
		OutboxPayload: []byte(
			`{"messageId":"019f0000-0000-4000-8000-000000000001"}`,
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
		Status:    platformpostgres.CommandAccepted,
		Result:    []byte(`{"Status":"accepted","Reason":""}`),
		Response: platformpostgres.StoredResponse{
			Status:  201,
			Headers: []byte(`{"content-type":["application/json"]}`),
			Body:    []byte(`{"balance":"10"}`),
		},
	}
	if _, err := pool.Exec(context.Background(), `
		UPDATE trading.commands
		   SET status = 'accepted',
		       result = $2,
		       completed_at = clock_timestamp()
		 WHERE command_id = $1`,
		commandID.String(),
		completion.Result,
	); err != nil {
		t.Fatalf("simulate committed engine result: %v", err)
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
	var outboxRows int
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
	if err := pool.QueryRow(
		context.Background(),
		"SELECT count(*) FROM messaging.outbox",
	).Scan(&outboxRows); err != nil {
		t.Fatalf("count command outbox: %v", err)
	}
	if idempotencyRows != 1 || commandRows != 1 || outboxRows != 1 {
		t.Fatalf(
			"journal rows = idempotency %d, commands %d, outbox %d, want 1 each",
			idempotencyRows,
			commandRows,
			outboxRows,
		)
	}
}

func TestCommandJournalRejectsOutOfOrderAndPrematureCompletion(t *testing.T) {
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := platformpostgres.NewMigrator(
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	journal := platformpostgres.NewCommandJournal(pool)
	now := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	request := platformpostgres.BeginCommandRequest{
		Scope:            "account:account-1",
		IdempotencyKey:   "sequence-1",
		RequestHash:      sha256.Sum256([]byte("sequence-1")),
		CommandID:        engine.IDFromSequence(engine.ID{}, 101),
		AccountID:        "account-1",
		AccountSequence:  2,
		CommandType:      "deposit",
		SchemaVersion:    1,
		CanonicalPayload: []byte(`{"amount":"10"}`),
		OutboxSubject:    "engine.input.7.command.v1",
		OutboxPayload:    []byte(`{"messageId":"019f0000-0000-4000-8000-000000000101"}`),
		LogicalTime:      now,
		ExpiresAt:        now.Add(24 * time.Hour),
	}
	if _, err := journal.Begin(
		context.Background(),
		request,
	); !errors.Is(err, platformpostgres.ErrCommandSequenceGap) {
		t.Fatalf("sequence 2 first Begin error = %v, want ErrCommandSequenceGap", err)
	}

	request.AccountSequence = 1
	if _, err := journal.Begin(context.Background(), request); err != nil {
		t.Fatalf("sequence 1 Begin: %v", err)
	}
	if err := journal.Complete(context.Background(), platformpostgres.CompleteCommandRequest{
		CommandID: request.CommandID,
		Status:    platformpostgres.CommandRejected,
		Result:    []byte(`{"Status":"rejected","Reason":"invalid_order"}`),
		Response: platformpostgres.StoredResponse{
			Status:  400,
			Headers: []byte(`{"content-type":["application/json"]}`),
			Body:    []byte(`{"error":"invalid_order"}`),
		},
	}); !errors.Is(err, platformpostgres.ErrCommandCompletionConflict) {
		t.Fatalf(
			"premature Complete error = %v, want ErrCommandCompletionConflict",
			err,
		)
	}

	var commandStatus string
	var idempotencyState string
	if err := pool.QueryRow(context.Background(), `
		SELECT command.status, idempotency.state
		  FROM trading.commands AS command
		  JOIN trading.idempotency_records AS idempotency
		    ON idempotency.command_id = command.command_id
		 WHERE command.command_id = $1`,
		request.CommandID.String(),
	).Scan(&commandStatus, &idempotencyState); err != nil {
		t.Fatalf("read pending command: %v", err)
	}
	if commandStatus != "pending" || idempotencyState != "in_progress" {
		t.Fatalf(
			"premature completion state = command %s idempotency %s",
			commandStatus,
			idempotencyState,
		)
	}
}
