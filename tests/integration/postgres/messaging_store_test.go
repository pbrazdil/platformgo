package postgres_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
	"github.com/upcomers-org/platformgo/internal/engine"

	"github.com/jackc/pgx/v5"
)

func TestOutboxRetriesUnknownOutcomeWithStableMessageID(t *testing.T) {
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := platformpostgres.NewMigrator(
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	messageID := engine.IDFromSequence(engine.ID{}, 41)
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO messaging.outbox (
			message_id, subject, schema_version, payload, next_attempt_at
		) VALUES ($1,'domain.v1.order.filled',1,$2,$3)`,
		messageID.String(),
		[]byte(`{"orderId":"order-1"}`),
		time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC),
	); err != nil {
		t.Fatalf("seed outbox: %v", err)
	}

	publisher := &unknownOutcomePublisher{}
	store := platformpostgres.NewMessagingStore(pool)
	now := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	_, err := store.PublishOutboxBatch(
		context.Background(),
		publisher,
		now,
		10,
		time.Minute,
		time.Second,
	)
	if !errors.Is(err, errUnknownPublishOutcome) {
		t.Fatalf("first publish error = %v, want unknown outcome", err)
	}

	published, err := store.PublishOutboxBatch(
		context.Background(),
		publisher,
		now.Add(time.Second),
		10,
		time.Minute,
		time.Second,
	)
	if err != nil {
		t.Fatalf("retry PublishOutboxBatch: %v", err)
	}
	if published != 1 {
		t.Fatalf("published = %d, want 1", published)
	}
	if len(publisher.ids) != 2 ||
		publisher.ids[0] != messageID.String() ||
		publisher.ids[1] != messageID.String() {
		t.Fatalf("publish IDs = %v, want stable %s twice", publisher.ids, messageID)
	}

	var attempts int
	var sequence *uint64
	if err := pool.QueryRow(context.Background(), `
		SELECT attempts, publish_sequence
		  FROM messaging.outbox
		 WHERE message_id = $1`,
		messageID.String(),
	).Scan(&attempts, &sequence); err != nil {
		t.Fatalf("read published outbox: %v", err)
	}
	if attempts != 2 || sequence == nil || *sequence != 71 {
		t.Fatalf("outbox attempts=%d sequence=%v, want 2 and 71", attempts, sequence)
	}
}

func TestOutboxDoesNotPublishLaterAccountCommandBeforeEarlierCommand(t *testing.T) {
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := platformpostgres.NewMigrator(
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	now := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	journal := platformpostgres.NewCommandJournal(pool)
	commandIDs := []engine.ID{
		engine.IDFromSequence(engine.ID{}, 201),
		engine.IDFromSequence(engine.ID{}, 202),
	}
	for index, commandID := range commandIDs {
		sequence := uint64(index + 1)
		request := platformpostgres.BeginCommandRequest{
			Scope:            "account:account-1",
			IdempotencyKey:   commandID.String(),
			RequestHash:      [32]byte{byte(sequence)},
			CommandID:        commandID,
			AccountID:        "account-1",
			AccountSequence:  sequence,
			CommandType:      "command",
			SchemaVersion:    1,
			CanonicalPayload: []byte(`{"kind":"command"}`),
			OutboxSubject:    "engine.input.7.command.v1",
			OutboxPayload:    []byte(`{"kind":"command"}`),
			LogicalTime:      now,
			ExpiresAt:        now.Add(24 * time.Hour),
		}
		if _, err := journal.Begin(context.Background(), request); err != nil {
			t.Fatalf("Begin sequence %d: %v", sequence, err)
		}
	}

	publisher := &failFirstPublisher{}
	store := platformpostgres.NewMessagingStore(pool)
	publishNow := time.Now().UTC().Add(time.Second)
	if published, err := store.PublishOutboxBatch(
		context.Background(),
		publisher,
		publishNow,
		10,
		time.Minute,
		time.Second,
	); !errors.Is(err, errUnknownPublishOutcome) || published != 0 {
		t.Fatalf("first publish = %d, error %v", published, err)
	}
	if len(publisher.ids) != 1 || publisher.ids[0] != commandIDs[0] {
		t.Fatalf("first publish IDs = %v, want only %s", publisher.ids, commandIDs[0])
	}

	if published, err := store.PublishOutboxBatch(
		context.Background(),
		publisher,
		publishNow.Add(time.Second),
		10,
		time.Minute,
		time.Second,
	); err != nil || published != 1 {
		t.Fatalf("sequence 1 retry = %d, error %v", published, err)
	}
	if len(publisher.ids) != 2 || publisher.ids[1] != commandIDs[0] {
		t.Fatalf("retry publish IDs = %v, want sequence 1 twice", publisher.ids)
	}

	if published, err := store.PublishOutboxBatch(
		context.Background(),
		publisher,
		publishNow.Add(2*time.Second),
		10,
		time.Minute,
		time.Second,
	); err != nil || published != 1 {
		t.Fatalf("sequence 2 publish = %d, error %v", published, err)
	}
	if len(publisher.ids) != 3 || publisher.ids[2] != commandIDs[1] {
		t.Fatalf("ordered publish IDs = %v, want %v", publisher.ids, commandIDs)
	}
}

func TestInboxClaimAndConsumerEffectCommitTogether(t *testing.T) {
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := platformpostgres.NewMigrator(
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if _, err := pool.Exec(
		context.Background(),
		"CREATE TABLE messaging.projection_probe (message_id uuid PRIMARY KEY)",
	); err != nil {
		t.Fatalf("create projection probe: %v", err)
	}

	store := platformpostgres.NewMessagingStore(pool)
	messageID := engine.IDFromSequence(engine.ID{}, 51)
	fail := true
	effect := func(ctx context.Context, tx pgx.Tx) error {
		if fail {
			return errors.New("projection failed")
		}
		_, err := tx.Exec(
			ctx,
			"INSERT INTO messaging.projection_probe (message_id) VALUES ($1)",
			messageID.String(),
		)
		return err
	}
	if _, err := store.ApplyInbox(
		context.Background(),
		"projector",
		messageID,
		effect,
	); err == nil {
		t.Fatal("failed effect unexpectedly committed")
	}
	fail = false
	duplicate, err := store.ApplyInbox(
		context.Background(),
		"projector",
		messageID,
		effect,
	)
	if err != nil || duplicate {
		t.Fatalf("successful ApplyInbox = duplicate %t, error %v", duplicate, err)
	}
	duplicate, err = store.ApplyInbox(
		context.Background(),
		"projector",
		messageID,
		effect,
	)
	if err != nil || !duplicate {
		t.Fatalf("duplicate ApplyInbox = duplicate %t, error %v", duplicate, err)
	}

	var inboxRows int
	var projectionRows int
	if err := pool.QueryRow(
		context.Background(),
		"SELECT count(*) FROM messaging.inbox",
	).Scan(&inboxRows); err != nil {
		t.Fatalf("count inbox: %v", err)
	}
	if err := pool.QueryRow(
		context.Background(),
		"SELECT count(*) FROM messaging.projection_probe",
	).Scan(&projectionRows); err != nil {
		t.Fatalf("count projection: %v", err)
	}
	if inboxRows != 1 || projectionRows != 1 {
		t.Fatalf(
			"inbox rows=%d projection rows=%d, want 1 and 1",
			inboxRows,
			projectionRows,
		)
	}
}

var errUnknownPublishOutcome = errors.New("publish acknowledgment lost")

type unknownOutcomePublisher struct {
	ids []string
}

func (publisher *unknownOutcomePublisher) Publish(
	_ context.Context,
	message platformpostgres.OutboxMessage,
) (uint64, error) {
	publisher.ids = append(publisher.ids, message.MessageID.String())
	if len(publisher.ids) == 1 {
		return 0, errUnknownPublishOutcome
	}
	return 71, nil
}

type failFirstPublisher struct {
	ids []engine.ID
}

func (publisher *failFirstPublisher) Publish(
	_ context.Context,
	message platformpostgres.OutboxMessage,
) (uint64, error) {
	publisher.ids = append(publisher.ids, message.MessageID)
	if len(publisher.ids) == 1 {
		return 0, errUnknownPublishOutcome
	}
	return uint64(80 + len(publisher.ids)), nil
}
