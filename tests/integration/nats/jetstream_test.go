package nats_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	gonats "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	platformnats "github.com/upcomers-org/platformgo/internal/adapters/nats"
	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
	"github.com/upcomers-org/platformgo/internal/engine"
)

func TestJetStreamDuplicatePublishAndCommittedRedelivery(t *testing.T) {
	url := os.Getenv("PLATFORMGO_TEST_NATS_URL")
	if url == "" {
		t.Skip("PLATFORMGO_TEST_NATS_URL is required for NATS integration tests")
	}
	connection, err := gonats.Connect(url)
	if err != nil {
		t.Fatalf("connect NATS: %v", err)
	}
	t.Cleanup(connection.Close)
	js, err := jetstream.New(connection)
	if err != nil {
		t.Fatalf("create JetStream context: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	limits := platformnats.StreamLimits{
		Replicas:        1,
		MaxMessages:     10_000,
		MaxBytes:        64 << 20,
		MaxMessageBytes: 1 << 20,
		MaxAge:          time.Hour,
		DuplicateWindow: time.Minute,
	}
	if err := platformnats.EnsureStreams(ctx, js, limits); err != nil {
		t.Fatalf("EnsureStreams: %v", err)
	}
	if err := platformnats.EnsureEngineShardStream(ctx, js, 7, limits); err != nil {
		t.Fatalf("EnsureEngineShardStream: %v", err)
	}

	publisher := platformnats.NewPublisher(js)
	messageID := engine.IDFromSequence(engine.ID{}, 101)
	message := platformpostgres.OutboxMessage{
		MessageID: messageID,
		Subject:   "engine.input.7.control.v1",
		Payload:   []byte(`{"kind":"probe"}`),
	}
	firstSequence, err := publisher.Publish(ctx, message)
	if err != nil {
		t.Fatalf("first Publish: %v", err)
	}
	secondSequence, err := publisher.Publish(ctx, message)
	if err != nil {
		t.Fatalf("duplicate Publish: %v", err)
	}
	if firstSequence != secondSequence {
		t.Fatalf(
			"duplicate publish sequences = %d and %d",
			firstSequence,
			secondSequence,
		)
	}
	stream, err := js.Stream(ctx, platformnats.EngineInputsStream+"_7")
	if err != nil {
		t.Fatalf("load engine stream: %v", err)
	}
	info, err := stream.Info(ctx)
	if err != nil {
		t.Fatalf("engine stream info: %v", err)
	}
	if info.State.Msgs != 1 ||
		info.Config.Storage != jetstream.FileStorage ||
		info.Config.Discard != jetstream.DiscardNew {
		t.Fatalf(
			"engine stream state/config = messages %d storage %d discard %d",
			info.State.Msgs,
			info.Config.Storage,
			info.Config.Discard,
		)
	}

	consumer, err := platformnats.NewEnginePullConsumer(
		ctx,
		js,
		7,
		"engine-shard-7-test",
		time.Second,
	)
	if err != nil {
		t.Fatalf("NewEnginePullConsumer: %v", err)
	}
	handlerFailure := errors.New("database commit failed")
	processed, err := consumer.ProcessOne(
		ctx,
		func(context.Context, platformnats.InboundMessage) error {
			return handlerFailure
		},
	)
	if !processed || !errors.Is(err, handlerFailure) {
		t.Fatalf("failed delivery = processed %t error %v", processed, err)
	}

	var delivered platformnats.InboundMessage
	processed, err = consumer.ProcessOne(
		ctx,
		func(_ context.Context, message platformnats.InboundMessage) error {
			delivered = message
			return nil
		},
	)
	if err != nil || !processed {
		t.Fatalf("redelivery = processed %t error %v", processed, err)
	}
	if delivered.MessageID != messageID ||
		delivered.StreamSequence != firstSequence ||
		delivered.NumDelivered < 2 {
		t.Fatalf("redelivered message = %+v", delivered)
	}
}

func TestEngineInputStreamRejectsNewMessagesAtCapacity(t *testing.T) {
	url := os.Getenv("PLATFORMGO_TEST_NATS_URL")
	if url == "" {
		t.Skip("PLATFORMGO_TEST_NATS_URL is required for NATS integration tests")
	}
	connection, err := gonats.Connect(url)
	if err != nil {
		t.Fatalf("connect NATS: %v", err)
	}
	t.Cleanup(connection.Close)
	js, err := jetstream.New(connection)
	if err != nil {
		t.Fatalf("create JetStream context: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := platformnats.EnsureEngineShardStream(
		ctx,
		js,
		13,
		platformnats.StreamLimits{
			Replicas:        1,
			MaxMessages:     1,
			MaxBytes:        1 << 20,
			MaxMessageBytes: 1 << 10,
			MaxAge:          time.Hour,
			DuplicateWindow: time.Minute,
		},
	); err != nil {
		t.Fatalf("EnsureEngineShardStream: %v", err)
	}
	publisher := platformnats.NewPublisher(js)
	first := platformpostgres.OutboxMessage{
		MessageID: engine.IDFromSequence(engine.ID{}, 131),
		Subject:   "engine.input.13.control.v1",
		Payload:   []byte(`{"kind":"first"}`),
	}
	if _, err := publisher.Publish(ctx, first); err != nil {
		t.Fatalf("first Publish: %v", err)
	}
	second := first
	second.MessageID = engine.IDFromSequence(engine.ID{}, 132)
	second.Payload = []byte(`{"kind":"second"}`)
	if _, err := publisher.Publish(ctx, second); err == nil {
		t.Fatal("capacity-exceeding publish unexpectedly succeeded")
	}
}
