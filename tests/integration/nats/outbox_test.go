package nats_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	gonats "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	platformnats "github.com/upcomers-org/platformgo/internal/adapters/nats"
	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
	"github.com/upcomers-org/platformgo/internal/engine"
)

// Ported from:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/messaging/e2e_outbox.rs:13
//	test: direct_drain_publishes_with_outbox_id
//
// Adaptations:
//   - The source's generic test topic is represented by the production-owned
//     ops.v1 namespace rather than permitting an API producer to forge an
//     engine input or domain event.
//   - The old composition and bus are replaced by the production PostgreSQL
//     messaging store, JetStream publisher, stream, and durable pull consumer.
//   - The source's subscription delay and receive timeout are replaced by a
//     consumer created before publication and one bounded pull.
//
// Assertions preserved:
//   - One pending outbox row is published by one direct drain.
//   - The delivered subject is the subject stored with the outbox row.
//   - The delivered Nats-Msg-Id is the stable outbox message ID.
//   - The durable consumer acknowledgment succeeds.
//
// Strengthening:
//   - The JetStream acknowledgment sequence is recorded on the same PostgreSQL
//     outbox row, which is left with one attempt and no pending claim.
func TestDirectDrainPublishesWithOutboxID(t *testing.T) {
	natsServerBinary := os.Getenv("PLATFORMGO_TEST_NATS_SERVER_BIN")
	postgresDSN := os.Getenv("PLATFORMGO_TEST_POSTGRES_DSN")
	if natsServerBinary == "" || postgresDSN == "" {
		t.Skip("NATS server binary and PostgreSQL integration URL are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool := isolatedOutboxDatabase(t, ctx, postgresDSN)
	if err := platformpostgres.NewMigrator(
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	port := availableTCPPort(t)
	server := startNATSServer(t, natsServerBinary, t.TempDir(), port)
	t.Cleanup(func() {
		stopNATSServer(server)
	})
	natsURL := fmt.Sprintf("nats://127.0.0.1:%d", port)
	connection, err := gonats.Connect(natsURL)
	if err != nil {
		t.Fatalf("connect NATS: %v", err)
	}
	t.Cleanup(connection.Close)
	js, err := jetstream.New(connection)
	if err != nil {
		t.Fatalf("create JetStream context: %v", err)
	}
	if err := platformnats.EnsureStreams(ctx, js, platformnats.StreamLimits{
		Replicas:        1,
		MaxMessages:     10_000,
		MaxBytes:        64 << 20,
		MaxMessageBytes: 1 << 20,
		MaxAge:          time.Hour,
		DuplicateWindow: time.Minute,
	}); err != nil {
		t.Fatalf("EnsureStreams: %v", err)
	}
	stream, err := js.Stream(ctx, platformnats.OpsStream)
	if err != nil {
		t.Fatalf("load ops stream: %v", err)
	}
	consumer, err := stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		Name:          "direct-outbox-drain",
		Durable:       "direct-outbox-drain",
		DeliverPolicy: jetstream.DeliverAllPolicy,
		AckPolicy:     jetstream.AckExplicitPolicy,
		FilterSubject: "ops.v1.outbox.created",
	})
	if err != nil {
		t.Fatalf("create durable consumer: %v", err)
	}

	messageID := engine.IDFromSequence(engine.ID{}, 41)
	const subject = "ops.v1.outbox.created"
	if _, err := pool.Exec(ctx, `
		INSERT INTO messaging.outbox (
			message_id, subject, schema_version, payload
		) VALUES ($1, $2, 1, $3)`,
		messageID.String(),
		subject,
		[]byte(`{"message":"hello"}`),
	); err != nil {
		t.Fatalf("seed outbox: %v", err)
	}

	publishAt := time.Date(2100, time.January, 1, 0, 0, 0, 0, time.UTC)
	published, err := platformpostgres.NewMessagingStore(pool).PublishOutboxBatch(
		ctx,
		platformnats.NewPublisher(js),
		publishAt,
		10,
		time.Minute,
		time.Second,
	)
	if err != nil || published != 1 {
		t.Fatalf("PublishOutboxBatch = %d, error %v, want 1, nil", published, err)
	}

	batch, err := consumer.Fetch(1, jetstream.FetchMaxWait(10*time.Second))
	if err != nil {
		t.Fatalf("fetch delivered message: %v", err)
	}
	var delivered jetstream.Msg
	for message := range batch.Messages() {
		delivered = message
		break
	}
	if delivered == nil {
		t.Fatalf("durable consumer returned no message: %v", batch.Error())
	}
	if delivered.Subject() != subject {
		t.Fatalf("delivered subject = %q, want %q", delivered.Subject(), subject)
	}
	if got := delivered.Headers().Get(gonats.MsgIdHdr); got != messageID.String() {
		t.Fatalf("Nats-Msg-Id = %q, want %q", got, messageID)
	}
	var payload map[string]string
	if err := json.Unmarshal(delivered.Data(), &payload); err != nil {
		t.Fatalf("decode delivered payload: %v", err)
	}
	if payload["message"] != "hello" {
		t.Fatalf("delivered payload = %v, want message hello", payload)
	}
	metadata, err := delivered.Metadata()
	if err != nil {
		t.Fatalf("read delivered metadata: %v", err)
	}
	if err := delivered.DoubleAck(ctx); err != nil {
		t.Fatalf("acknowledge durable delivery: %v", err)
	}

	var attempts int
	var publishedSequence uint64
	var claimed bool
	if err := pool.QueryRow(ctx, `
		SELECT attempts, publish_sequence, claimed_at IS NOT NULL
		  FROM messaging.outbox
		 WHERE message_id = $1`,
		messageID.String(),
	).Scan(&attempts, &publishedSequence, &claimed); err != nil {
		t.Fatalf("read published outbox: %v", err)
	}
	if attempts != 1 ||
		publishedSequence != metadata.Sequence.Stream ||
		claimed {
		t.Fatalf(
			"outbox attempts=%d sequence=%d claimed=%t, want 1, %d, false",
			attempts,
			publishedSequence,
			claimed,
			metadata.Sequence.Stream,
		)
	}
}

func isolatedOutboxDatabase(
	t *testing.T,
	ctx context.Context,
	dsn string,
) *pgxpool.Pool {
	t.Helper()

	baseConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse PostgreSQL integration URL: %v", err)
	}
	adminConfig := baseConfig.Copy()
	adminConfig.ConnConfig.Database = "postgres"
	adminPool, err := pgxpool.NewWithConfig(ctx, adminConfig)
	if err != nil {
		t.Fatalf("open PostgreSQL maintenance pool: %v", err)
	}
	t.Cleanup(adminPool.Close)

	var backendPID int64
	if err := adminPool.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&backendPID); err != nil {
		t.Fatalf("read PostgreSQL maintenance backend PID: %v", err)
	}
	databaseName := fmt.Sprintf("platformgo_test_outbox_%d", backendPID)
	databaseIdentifier := pgx.Identifier{databaseName}.Sanitize()
	if _, err := adminPool.Exec(ctx, "CREATE DATABASE "+databaseIdentifier); err != nil {
		t.Fatalf("create isolated PostgreSQL database: %v", err)
	}

	testConfig := baseConfig.Copy()
	testConfig.ConnConfig.Database = databaseName
	pool, err := pgxpool.NewWithConfig(ctx, testConfig)
	if err != nil {
		_, _ = adminPool.Exec(
			context.WithoutCancel(ctx),
			"DROP DATABASE "+databaseIdentifier+" WITH (FORCE)",
		)
		t.Fatalf("open isolated PostgreSQL database: %v", err)
	}
	t.Cleanup(func() {
		pool.Close()
		cleanupContext, cancel := context.WithTimeout(
			context.Background(),
			10*time.Second,
		)
		defer cancel()
		if _, err := adminPool.Exec(
			cleanupContext,
			"DROP DATABASE "+databaseIdentifier+" WITH (FORCE)",
		); err != nil {
			t.Errorf("drop isolated PostgreSQL database: %v", err)
		}
	})
	return pool
}
