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
	gonats "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	platformnats "github.com/upcomers-org/platformgo/internal/adapters/nats"
	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
	"github.com/upcomers-org/platformgo/internal/engine"
)

type sourceDedupeEnvelope struct {
	MessageID     string `json:"messageId"`
	SchemaVersion uint32 `json:"schemaVersion"`
	Payload       []byte `json:"payload"`
}

// Ported from:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/messaging/e2e_dedupe.rs:12
//	test: duplicate_delivery_is_deduped_by_the_inbox
//
// Adaptations:
//   - The source's generic RabbitMQ topic is represented by a non-economic
//     production-owned ops.v1 subject.
//   - Random UUIDs are replaced by deterministic application message IDs.
//   - The source subscription sleep is replaced by creating the durable
//     JetStream consumer before publication.
//   - Three physical test deliveries carry the source application IDs in a
//     bounded envelope. They bypass transport deduplication because the source
//     explicitly publishes and receives dup, dup, and other.
//   - The source inbox claim is replaced by the production PostgreSQL
//     MessagingStore.ApplyInbox transaction.
//
// Assertions preserved:
//   - Three actual deliveries carry only two unique application message IDs.
//   - The repeated ID is claimed once, yielding exactly two effects.
//   - All three deliveries are synchronously acknowledged after inbox commit.
//
// Strengthening:
//   - Each new inbox receipt and a non-unique projection probe commit in the
//     same PostgreSQL transaction.
//   - The three deliveries have distinct, ordered JetStream stream sequences.
func TestDuplicateDeliveryIsDedupedByTheInbox(t *testing.T) {
	natsServerBinary := os.Getenv("PLATFORMGO_TEST_NATS_SERVER_BIN")
	postgresDSN := os.Getenv("PLATFORMGO_TEST_POSTGRES_DSN")
	if natsServerBinary == "" || postgresDSN == "" {
		t.Skip("NATS server binary and PostgreSQL integration URL are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool := resetOutboxDatabase(t, ctx, postgresDSN)
	if err := platformpostgres.NewMigrator(
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		CREATE TABLE messaging.inbox_dedupe_probe (
			delivery_sequence bigint NOT NULL,
			message_id uuid NOT NULL
		)`,
	); err != nil {
		t.Fatalf("create inbox projection probe: %v", err)
	}

	port := availableTCPPort(t)
	server := startNATSServer(t, natsServerBinary, t.TempDir(), port)
	t.Cleanup(func() {
		stopNATSServer(server)
	})
	connection, err := gonats.Connect(
		fmt.Sprintf("nats://127.0.0.1:%d", port),
	)
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
		Name:          "source-inbox-dedupe",
		Durable:       "source-inbox-dedupe",
		DeliverPolicy: jetstream.DeliverAllPolicy,
		AckPolicy:     jetstream.AckExplicitPolicy,
		FilterSubject: "ops.v1.inbox.dedupe",
	})
	if err != nil {
		t.Fatalf("create durable consumer: %v", err)
	}

	duplicateID := engine.IDFromSequence(engine.ID{}, 61)
	otherID := engine.IDFromSequence(engine.ID{}, 62)
	sourceIDs := []engine.ID{duplicateID, duplicateID, otherID}
	publishedSequences := make([]uint64, 0, len(sourceIDs))
	for _, messageID := range sourceIDs {
		payload, err := json.Marshal(sourceDedupeEnvelope{
			MessageID:     messageID.String(),
			SchemaVersion: 1,
			Payload:       []byte("x"),
		})
		if err != nil {
			t.Fatalf("encode source envelope: %v", err)
		}
		acknowledgment, err := js.Publish(
			ctx,
			"ops.v1.inbox.dedupe",
			payload,
		)
		if err != nil {
			t.Fatalf("publish source delivery: %v", err)
		}
		publishedSequences = append(
			publishedSequences,
			acknowledgment.Sequence,
		)
	}
	if publishedSequences[0] == 0 ||
		publishedSequences[1] <= publishedSequences[0] ||
		publishedSequences[2] <= publishedSequences[1] {
		t.Fatalf(
			"published stream sequences = %v, want three ordered deliveries",
			publishedSequences,
		)
	}

	batch, err := consumer.Fetch(3, jetstream.FetchMaxWait(10*time.Second))
	if err != nil {
		t.Fatalf("fetch source deliveries: %v", err)
	}
	store := platformpostgres.NewMessagingStore(pool)
	const consumerName = "source-inbox-projector"
	effects := 0
	acknowledged := 0
	delivered := 0
	wantDuplicate := []bool{false, true, false}
	for message := range batch.Messages() {
		if delivered >= len(sourceIDs) {
			t.Fatal("received more than three source deliveries")
		}
		if message.Subject() != "ops.v1.inbox.dedupe" {
			t.Fatalf("delivery subject = %q", message.Subject())
		}
		metadata, err := message.Metadata()
		if err != nil {
			t.Fatalf("read delivery metadata: %v", err)
		}
		if metadata.Sequence.Stream != publishedSequences[delivered] {
			t.Fatalf(
				"delivery %d stream sequence = %d, want %d",
				delivered,
				metadata.Sequence.Stream,
				publishedSequences[delivered],
			)
		}
		var envelope sourceDedupeEnvelope
		if err := json.Unmarshal(message.Data(), &envelope); err != nil {
			t.Fatalf("decode source delivery: %v", err)
		}
		messageID, err := engine.ParseID(envelope.MessageID)
		if err != nil {
			t.Fatalf("parse application message ID: %v", err)
		}
		if messageID != sourceIDs[delivered] ||
			envelope.SchemaVersion != 1 ||
			string(envelope.Payload) != "x" {
			t.Fatalf(
				"delivery %d envelope = %+v, want ID %s/schema 1/payload x",
				delivered,
				envelope,
				sourceIDs[delivered],
			)
		}

		duplicate, err := store.ApplyInbox(
			ctx,
			consumerName,
			messageID,
			func(effectContext context.Context, tx pgx.Tx) error {
				_, effectErr := tx.Exec(effectContext, `
					INSERT INTO messaging.inbox_dedupe_probe (
						delivery_sequence, message_id
					) VALUES ($1,$2)`,
					metadata.Sequence.Stream,
					messageID.String(),
				)
				return effectErr
			},
		)
		if err != nil {
			t.Fatalf("apply delivery %d inbox transaction: %v", delivered, err)
		}
		if duplicate != wantDuplicate[delivered] {
			t.Fatalf(
				"delivery %d duplicate = %t, want %t",
				delivered,
				duplicate,
				wantDuplicate[delivered],
			)
		}
		if !duplicate {
			effects++
		}

		var receiptExists bool
		if err := pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1
				  FROM messaging.inbox
				 WHERE consumer = $1 AND message_id = $2
			)`,
			consumerName,
			messageID.String(),
		).Scan(&receiptExists); err != nil {
			t.Fatalf("verify committed inbox receipt: %v", err)
		}
		if !receiptExists {
			t.Fatalf("delivery %d inbox receipt did not commit before ack", delivered)
		}
		if err := message.DoubleAck(ctx); err != nil {
			t.Fatalf("acknowledge delivery %d: %v", delivered, err)
		}
		acknowledged++
		delivered++
	}
	if err := batch.Error(); err != nil {
		t.Fatalf("fetch source delivery batch: %v", err)
	}

	var inboxRows int
	var probeRows int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		  FROM messaging.inbox
		 WHERE consumer = $1`,
		consumerName,
	).Scan(&inboxRows); err != nil {
		t.Fatalf("count inbox rows: %v", err)
	}
	if err := pool.QueryRow(
		ctx,
		"SELECT count(*) FROM messaging.inbox_dedupe_probe",
	).Scan(&probeRows); err != nil {
		t.Fatalf("count projection probe rows: %v", err)
	}
	if delivered != 3 ||
		acknowledged != 3 ||
		effects != 2 ||
		inboxRows != 2 ||
		probeRows != 2 {
		t.Fatalf(
			"deliveries=%d acknowledgments=%d effects=%d inbox=%d probe=%d, "+
				"want 3/3/2/2/2",
			delivered,
			acknowledged,
			effects,
			inboxRows,
			probeRows,
		)
	}
}
