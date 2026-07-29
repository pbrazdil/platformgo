package nats_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"sort"
	"testing"
	"time"

	gonats "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	platformnats "github.com/upcomers-org/platformgo/internal/adapters/nats"
	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
	"github.com/upcomers-org/platformgo/internal/engine"
)

// Ported from:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/messaging/e2e_dlq.rs:9
//	test: redelivery_cap_then_dead_letter
//
// Adaptations:
//   - The source RabbitMQ subscription is replaced by an isolated durable
//     JetStream consumer on an exact non-economic ops.v1 subject.
//   - The source subscription sleep is replaced by creating the consumer
//     before publication; deterministic IDs replace random fixture identity.
//   - RabbitMQ's redelivered flag is represented by JetStream NumDelivered.
//   - The quarantine record and capture helper are test-owned adapter
//     translations composed through the production Publisher. Direct
//     OutboxMessage construction is test fixture setup only; it does not prove
//     or authorize a production DLQ consumer, runtime retry policy, PostgreSQL
//     outbox workflow, or atomic source-to-quarantine move.
//
// Assertions preserved:
//   - The first delivery has the published subject and is not a redelivery.
//   - An explicit requeueing negative acknowledgment produces one redelivery.
//   - Dead-letter capture succeeds before the source delivery is acknowledged.
//   - The dead-lettered message is not delivered again by the source consumer.
//
// Strengthening from MESSAGING.md:
//   - The durable capture preserves original identity, subject, stream
//     sequence, attempt, payload, and failure reason under a distinct stable
//     transport ID, and remains available to a fresh repair consumer.
func TestRedeliveryCapThenDeadLetter(t *testing.T) {
	const (
		sourceSubject = "ops.v1.bus.dlq.source"
		deadSubject   = "ops.v1.bus.dlq.captured"
		reason        = "test: persistent handler failure"
	)
	ctx, js, sourceConsumer := sourceBusFixture(
		t,
		"source-bus-dlq",
		sourceSubject,
	)
	publisher := platformnats.NewPublisher(js)
	originalID := engine.IDFromSequence(engine.ID{}, 91)
	deadLetterID := engine.IDFromSequence(engine.ID{}, 92)
	payload := []byte(`"poison"`)

	publishedSequence, err := publisher.Publish(
		ctx,
		platformpostgres.OutboxMessage{
			MessageID:     originalID,
			Subject:       sourceSubject,
			SchemaVersion: 1,
			Payload:       payload,
		},
	)
	if err != nil || publishedSequence == 0 {
		t.Fatalf(
			"publish poison = sequence %d, error %v",
			publishedSequence,
			err,
		)
	}

	first := fetchSourceBusMessages(t, ctx, sourceConsumer, 1)[0]
	firstMetadata := requireSourceDelivery(
		t,
		first,
		originalID,
		sourceSubject,
		payload,
		publishedSequence,
		1,
	)
	if err := first.Nak(); err != nil {
		t.Fatalf("negative-ack first delivery: %v", err)
	}

	second := fetchSourceBusMessages(t, ctx, sourceConsumer, 1)[0]
	secondMetadata := requireSourceDelivery(
		t,
		second,
		originalID,
		sourceSubject,
		payload,
		publishedSequence,
		2,
	)
	if secondMetadata.Sequence.Consumer <= firstMetadata.Sequence.Consumer {
		t.Fatalf(
			"redelivery consumer sequence = %d, want greater than %d",
			secondMetadata.Sequence.Consumer,
			firstMetadata.Sequence.Consumer,
		)
	}

	deadLetterSequence, err := captureSourceBusDeadLetter(
		ctx,
		publisher,
		second,
		deadLetterID,
		deadSubject,
		reason,
	)
	if err != nil || deadLetterSequence <= publishedSequence {
		t.Fatalf(
			"dead-letter capture = sequence %d, error %v; want greater than source %d",
			deadLetterSequence,
			err,
			publishedSequence,
		)
	}

	stream, err := js.Stream(ctx, platformnats.OpsStream)
	if err != nil {
		t.Fatalf("load ops stream after dead-letter capture: %v", err)
	}
	recreatedSource, err := stream.CreateOrUpdateConsumer(
		ctx,
		jetstream.ConsumerConfig{
			Name:          "source-bus-dlq",
			Durable:       "source-bus-dlq",
			DeliverPolicy: jetstream.DeliverAllPolicy,
			AckPolicy:     jetstream.AckExplicitPolicy,
			FilterSubject: sourceSubject,
		},
	)
	if err != nil {
		t.Fatalf("recreate source consumer: %v", err)
	}
	sourceInfo, err := recreatedSource.Info(ctx)
	if err != nil {
		t.Fatalf("read recreated source consumer state: %v", err)
	}
	if sourceInfo.NumPending != 0 || sourceInfo.NumAckPending != 0 {
		t.Fatalf(
			"settled source consumer = pending %d ack-pending %d, want 0 and 0",
			sourceInfo.NumPending,
			sourceInfo.NumAckPending,
		)
	}
	if message, err := recreatedSource.Next(
		jetstream.FetchMaxWait(300 * time.Millisecond),
	); message != nil || (!errors.Is(err, gonats.ErrTimeout) &&
		!errors.Is(err, jetstream.ErrNoMessages)) {
		t.Fatalf(
			"source delivery after dead-letter = message %#v, error %v",
			message,
			err,
		)
	}

	repairConsumer, err := stream.CreateOrUpdateConsumer(
		ctx,
		jetstream.ConsumerConfig{
			Name:          "source-bus-dlq-repair",
			Durable:       "source-bus-dlq-repair",
			DeliverPolicy: jetstream.DeliverAllPolicy,
			AckPolicy:     jetstream.AckExplicitPolicy,
			FilterSubject: deadSubject,
		},
	)
	if err != nil {
		t.Fatalf("create dead-letter repair consumer: %v", err)
	}
	captured := fetchSourceBusMessages(t, ctx, repairConsumer, 1)[0]
	if captured.Subject() != deadSubject {
		t.Fatalf(
			"captured subject = %q, want %q",
			captured.Subject(),
			deadSubject,
		)
	}
	if got := captured.Headers().Get(gonats.MsgIdHdr); got != deadLetterID.String() {
		t.Fatalf(
			"captured Nats-Msg-Id = %q, want %q",
			got,
			deadLetterID,
		)
	}
	capturedMetadata, err := captured.Metadata()
	if err != nil {
		t.Fatalf("read captured delivery metadata: %v", err)
	}
	if capturedMetadata.Sequence.Stream != deadLetterSequence {
		t.Fatalf(
			"captured stream sequence = %d, want %d",
			capturedMetadata.Sequence.Stream,
			deadLetterSequence,
		)
	}
	var record sourceBusDeadLetterRecord
	if err := json.Unmarshal(captured.Data(), &record); err != nil {
		t.Fatalf("decode captured dead-letter record: %v", err)
	}
	if record.OriginalMessageID != originalID.String() ||
		record.OriginalSubject != sourceSubject ||
		record.OriginalStreamSequence != publishedSequence ||
		record.DeliveryAttempt != 2 ||
		record.Reason != reason ||
		!bytes.Equal(record.Payload, payload) {
		t.Fatalf("captured dead-letter record = %+v", record)
	}
	if err := captured.DoubleAck(ctx); err != nil {
		t.Fatalf("acknowledge captured dead-letter record: %v", err)
	}
}

func TestSourceBusDeadLetterCaptureFailureLeavesDeliveryUnsettled(
	t *testing.T,
) {
	const sourceSubject = "ops.v1.bus.dlq.failure-source"
	ctx, js, sourceConsumer := sourceBusFixture(
		t,
		"source-bus-dlq-failure",
		sourceSubject,
	)
	publisher := platformnats.NewPublisher(js)
	originalID := engine.IDFromSequence(engine.ID{}, 93)
	deadLetterID := engine.IDFromSequence(engine.ID{}, 94)
	payload := []byte(`"poison"`)

	publishedSequence, err := publisher.Publish(
		ctx,
		platformpostgres.OutboxMessage{
			MessageID:     originalID,
			Subject:       sourceSubject,
			SchemaVersion: 1,
			Payload:       payload,
		},
	)
	if err != nil || publishedSequence == 0 {
		t.Fatalf(
			"publish poison = sequence %d, error %v",
			publishedSequence,
			err,
		)
	}
	first := fetchSourceBusMessages(t, ctx, sourceConsumer, 1)[0]
	requireSourceDelivery(
		t,
		first,
		originalID,
		sourceSubject,
		payload,
		publishedSequence,
		1,
	)

	if sequence, err := captureSourceBusDeadLetter(
		ctx,
		publisher,
		first,
		deadLetterID,
		"ops.v2.bus.dlq.unroutable",
		"test: capture unavailable",
	); err == nil || sequence != 0 {
		t.Fatalf(
			"unroutable capture = sequence %d, error %v, want zero and error",
			sequence,
			err,
		)
	}
	info, err := sourceConsumer.Info(ctx)
	if err != nil {
		t.Fatalf("read source consumer after capture failure: %v", err)
	}
	if info.NumAckPending != 1 {
		t.Fatalf(
			"source ack-pending after capture failure = %d, want 1",
			info.NumAckPending,
		)
	}
	if err := first.Nak(); err != nil {
		t.Fatalf("negative-ack unsettled source delivery: %v", err)
	}
	second := fetchSourceBusMessages(t, ctx, sourceConsumer, 1)[0]
	requireSourceDelivery(
		t,
		second,
		originalID,
		sourceSubject,
		payload,
		publishedSequence,
		2,
	)
	if err := second.DoubleAck(ctx); err != nil {
		t.Fatalf("acknowledge cleanup redelivery: %v", err)
	}
}

// Ported from:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/messaging/e2e_bus.rs:11
//	test: publish_subscribe_roundtrip_through_kernel_init
//
// Adaptations:
//   - The source RabbitMQ composition is replaced by the production JetStream
//     Publisher on the non-economic ops.v1.bus.roundtrip subject.
//   - The direct OutboxMessage is test-only transport fixture setup; it does
//     not represent or authorize production publication without PostgreSQL.
//   - The source subscription sleep is replaced by creating a durable wildcard
//     consumer before publication, and random identity is deterministic.
//
// Assertions preserved:
//   - A subscribed consumer receives the published subject.
//   - The delivery is acknowledged successfully.
//
// Strengthening:
//   - The stable Nats-Msg-Id and source-derived JSON payload survive delivery.
//   - The delivered stream sequence equals the positive publisher acknowledgment.
func TestPublishSubscribeRoundtripThroughKernelInit(t *testing.T) {
	ctx, js, consumer := sourceBusFixture(
		t,
		"source-bus-roundtrip",
		"ops.v1.bus.roundtrip.>",
	)
	messageID := engine.IDFromSequence(engine.ID{}, 71)
	const subject = "ops.v1.bus.roundtrip.order.created"
	payload := []byte(`"payload"`)

	sequence, err := platformnats.NewPublisher(js).Publish(
		ctx,
		platformpostgres.OutboxMessage{
			MessageID:     messageID,
			Subject:       subject,
			SchemaVersion: 1,
			Payload:       payload,
		},
	)
	if err != nil || sequence == 0 {
		t.Fatalf("Publish = sequence %d, error %v", sequence, err)
	}

	message := fetchSourceBusMessages(t, ctx, consumer, 1)[0]
	if message.Subject() != subject {
		t.Fatalf("delivered subject = %q, want %q", message.Subject(), subject)
	}
	if got := message.Headers().Get(gonats.MsgIdHdr); got != messageID.String() {
		t.Fatalf("delivered Nats-Msg-Id = %q, want %q", got, messageID)
	}
	if !bytes.Equal(message.Data(), payload) {
		t.Fatalf("delivered payload = %q, want %q", message.Data(), payload)
	}
	metadata, err := message.Metadata()
	if err != nil {
		t.Fatalf("read delivered metadata: %v", err)
	}
	if metadata.Sequence.Stream != sequence {
		t.Fatalf(
			"delivered stream sequence = %d, want publication sequence %d",
			metadata.Sequence.Stream,
			sequence,
		)
	}
	if err := message.DoubleAck(ctx); err != nil {
		t.Fatalf("acknowledge delivered message: %v", err)
	}
}

// Ported from:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/messaging/e2e_bus.rs:34
//	test: confirmed_batch_acks_and_delivers_every_message
//
// Adaptations:
//   - The source batch success path is represented by three sequential calls
//     to the production JetStream Publisher. This is not an atomic batch API.
//   - The source RabbitMQ composition and subscription sleep are replaced by
//     an isolated JetStream server and a pre-created durable wildcard consumer.
//   - Direct OutboxMessage values are deterministic test-only transport
//     fixtures, not evidence of PostgreSQL outbox authority.
//
// Assertions preserved:
//   - The sent and broker-confirmed message-ID sets are exactly equal.
//   - The sent and delivered message-ID sets are exactly equal.
//   - All three deliveries are acknowledged successfully.
//
// Strengthening:
//   - Every subject, payload, stable Nats-Msg-Id, and acknowledged stream
//     sequence is preserved through the real transport.
func TestConfirmedBatchAcksAndDeliversEveryMessage(t *testing.T) {
	ctx, js, consumer := sourceBusFixture(
		t,
		"source-bus-batch",
		"ops.v1.bus.batch.>",
	)
	const subject = "ops.v1.bus.batch.event"
	messages := []platformpostgres.OutboxMessage{
		{
			MessageID:     engine.IDFromSequence(engine.ID{}, 81),
			Subject:       subject,
			SchemaVersion: 1,
			Payload:       []byte("event-0"),
		},
		{
			MessageID:     engine.IDFromSequence(engine.ID{}, 82),
			Subject:       subject,
			SchemaVersion: 1,
			Payload:       []byte("event-1"),
		},
		{
			MessageID:     engine.IDFromSequence(engine.ID{}, 83),
			Subject:       subject,
			SchemaVersion: 1,
			Payload:       []byte("event-2"),
		},
	}
	sentIDs := make([]engine.ID, 0, len(messages))
	wantPayload := make(map[engine.ID][]byte, len(messages))
	for _, message := range messages {
		sentIDs = append(sentIDs, message.MessageID)
		wantPayload[message.MessageID] = message.Payload
	}
	if len(wantPayload) != len(messages) {
		t.Fatalf("fixture contains %d unique IDs, want %d", len(wantPayload), len(messages))
	}

	publisher := platformnats.NewPublisher(js)
	confirmedIDs := make([]engine.ID, 0, len(messages))
	confirmedSequence := make(map[engine.ID]uint64, len(messages))
	var previousSequence uint64
	for _, message := range messages {
		sequence, err := publisher.Publish(ctx, message)
		if err != nil || sequence == 0 {
			t.Fatalf(
				"Publish message %s = sequence %d, error %v",
				message.MessageID,
				sequence,
				err,
			)
		}
		if sequence <= previousSequence {
			t.Fatalf(
				"publication sequence for %s = %d, want greater than %d",
				message.MessageID,
				sequence,
				previousSequence,
			)
		}
		previousSequence = sequence
		confirmedIDs = append(confirmedIDs, message.MessageID)
		confirmedSequence[message.MessageID] = sequence
	}
	requireSameSourceBusIDs(t, "broker-confirmed", sentIDs, confirmedIDs)

	deliveredIDs := make([]engine.ID, 0, len(messages))
	seen := make(map[engine.ID]struct{}, len(messages))
	physicalDeliveries := 0
	acknowledged := 0
	for _, message := range fetchSourceBusMessages(t, ctx, consumer, len(messages)) {
		physicalDeliveries++
		if message.Subject() != subject {
			t.Fatalf("delivered subject = %q, want %q", message.Subject(), subject)
		}
		messageID, err := engine.ParseID(message.Headers().Get(gonats.MsgIdHdr))
		if err != nil {
			t.Fatalf("parse delivered Nats-Msg-Id: %v", err)
		}
		payload, expected := wantPayload[messageID]
		if !expected {
			t.Fatalf("unexpected delivered message ID %s", messageID)
		}
		if _, duplicate := seen[messageID]; duplicate {
			t.Fatalf("message ID %s delivered more than once", messageID)
		}
		seen[messageID] = struct{}{}
		if !bytes.Equal(message.Data(), payload) {
			t.Fatalf(
				"delivered payload for %s = %q, want %q",
				messageID,
				message.Data(),
				payload,
			)
		}
		metadata, err := message.Metadata()
		if err != nil {
			t.Fatalf("read delivered metadata for %s: %v", messageID, err)
		}
		if metadata.Sequence.Stream != confirmedSequence[messageID] {
			t.Fatalf(
				"delivered sequence for %s = %d, want %d",
				messageID,
				metadata.Sequence.Stream,
				confirmedSequence[messageID],
			)
		}
		if err := message.DoubleAck(ctx); err != nil {
			t.Fatalf("acknowledge delivered message %s: %v", messageID, err)
		}
		acknowledged++
		deliveredIDs = append(deliveredIDs, messageID)
	}
	requireSameSourceBusIDs(t, "delivered", sentIDs, deliveredIDs)
	if physicalDeliveries != 3 || acknowledged != 3 {
		t.Fatalf(
			"physical deliveries = %d, acknowledgments = %d, want 3 and 3",
			physicalDeliveries,
			acknowledged,
		)
	}
}

func sourceBusFixture(
	t *testing.T,
	durable string,
	filterSubject string,
) (context.Context, jetstream.JetStream, jetstream.Consumer) {
	t.Helper()
	serverBinary := os.Getenv("PLATFORMGO_TEST_NATS_SERVER_BIN")
	if serverBinary == "" {
		t.Skip("PLATFORMGO_TEST_NATS_SERVER_BIN is required for bus acceptance tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	port := availableTCPPort(t)
	server := startNATSServer(t, serverBinary, t.TempDir(), port)
	t.Cleanup(func() {
		stopNATSServer(server)
	})
	connection, err := gonats.Connect(fmt.Sprintf("nats://127.0.0.1:%d", port))
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
		MaxMessages:     100,
		MaxBytes:        1 << 20,
		MaxMessageBytes: 1 << 10,
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
		Name:          durable,
		Durable:       durable,
		DeliverPolicy: jetstream.DeliverAllPolicy,
		AckPolicy:     jetstream.AckExplicitPolicy,
		FilterSubject: filterSubject,
	})
	if err != nil {
		t.Fatalf("create durable wildcard consumer: %v", err)
	}
	return ctx, js, consumer
}

func fetchSourceBusMessages(
	t *testing.T,
	ctx context.Context,
	consumer jetstream.Consumer,
	count int,
) []jetstream.Msg {
	t.Helper()
	batch, err := consumer.Fetch(count, jetstream.FetchMaxWait(10*time.Second))
	if err != nil {
		t.Fatalf("fetch %d source bus messages: %v", count, err)
	}
	messages := make([]jetstream.Msg, 0, count)
	for message := range batch.Messages() {
		messages = append(messages, message)
	}
	if batchErr := batch.Error(); batchErr != nil {
		t.Fatalf("fetch source bus messages: %v", batchErr)
	}
	if len(messages) != count {
		t.Fatalf("fetched %d source bus messages, want %d", len(messages), count)
	}
	return messages
}

func requireSameSourceBusIDs(
	t *testing.T,
	label string,
	sent []engine.ID,
	got []engine.ID,
) {
	t.Helper()
	canonical := func(ids []engine.ID) []string {
		values := make([]string, len(ids))
		for index, id := range ids {
			values[index] = id.String()
		}
		sort.Strings(values)
		return values
	}
	want := canonical(sent)
	actual := canonical(got)
	if len(actual) != 3 || !reflect.DeepEqual(actual, want) {
		t.Fatalf("%s IDs = %v, want %v with cardinality 3", label, actual, want)
	}
}

type sourceBusDeadLetterRecord struct {
	OriginalMessageID      string          `json:"originalMessageId"`
	OriginalSubject        string          `json:"originalSubject"`
	OriginalStreamSequence uint64          `json:"originalStreamSequence"`
	DeliveryAttempt        uint64          `json:"deliveryAttempt"`
	Reason                 string          `json:"reason"`
	Payload                json.RawMessage `json:"payload"`
}

func requireSourceDelivery(
	t *testing.T,
	message jetstream.Msg,
	messageID engine.ID,
	subject string,
	payload []byte,
	streamSequence uint64,
	deliveryAttempt uint64,
) *jetstream.MsgMetadata {
	t.Helper()
	if message.Subject() != subject {
		t.Fatalf("delivered subject = %q, want %q", message.Subject(), subject)
	}
	if got := message.Headers().Get(gonats.MsgIdHdr); got != messageID.String() {
		t.Fatalf("delivered Nats-Msg-Id = %q, want %q", got, messageID)
	}
	if !bytes.Equal(message.Data(), payload) {
		t.Fatalf("delivered payload = %q, want %q", message.Data(), payload)
	}
	metadata, err := message.Metadata()
	if err != nil {
		t.Fatalf("read delivered metadata: %v", err)
	}
	if metadata.Sequence.Stream != streamSequence ||
		metadata.NumDelivered != deliveryAttempt {
		t.Fatalf(
			"delivery metadata = stream %d attempt %d, want %d and %d",
			metadata.Sequence.Stream,
			metadata.NumDelivered,
			streamSequence,
			deliveryAttempt,
		)
	}
	return metadata
}

func captureSourceBusDeadLetter(
	ctx context.Context,
	publisher *platformnats.Publisher,
	message jetstream.Msg,
	deadLetterID engine.ID,
	deadLetterSubject string,
	reason string,
) (uint64, error) {
	metadata, err := message.Metadata()
	if err != nil {
		return 0, fmt.Errorf("read source delivery metadata: %w", err)
	}
	originalMessageID := message.Headers().Get(gonats.MsgIdHdr)
	if originalMessageID == "" {
		return 0, errors.New("capture dead letter: original message ID is required")
	}
	if originalMessageID == deadLetterID.String() {
		return 0, errors.New(
			"capture dead letter: transport ID must differ from original ID",
		)
	}
	record, err := json.Marshal(sourceBusDeadLetterRecord{
		OriginalMessageID:      originalMessageID,
		OriginalSubject:        message.Subject(),
		OriginalStreamSequence: metadata.Sequence.Stream,
		DeliveryAttempt:        metadata.NumDelivered,
		Reason:                 reason,
		Payload:                append(json.RawMessage(nil), message.Data()...),
	})
	if err != nil {
		return 0, fmt.Errorf("encode dead-letter record: %w", err)
	}
	sequence, err := publisher.Publish(
		ctx,
		platformpostgres.OutboxMessage{
			MessageID:     deadLetterID,
			Subject:       deadLetterSubject,
			SchemaVersion: 1,
			Payload:       record,
		},
	)
	if err != nil {
		return 0, fmt.Errorf("publish dead-letter record: %w", err)
	}
	if err := message.DoubleAck(ctx); err != nil {
		return sequence, fmt.Errorf("acknowledge source delivery: %w", err)
	}
	return sequence, nil
}
