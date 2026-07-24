// Package nats implements durable JetStream transport. PostgreSQL remains the
// authority for every economic effect and acknowledgment follows commit.
package nats

import (
	"context"
	"errors"
	"fmt"
	"time"

	gonats "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
	"github.com/upcomers-org/platformgo/internal/engine"
)

const (
	EngineInputsStream = "ENGINE_INPUTS"
	DomainEventsStream = "DOMAIN_EVENTS"
	JobsStream         = "JOBS"
	OpsStream          = "OPS"
)

// StreamLimits are explicit capacity and durability settings. Zero values are
// rejected so a deployment cannot silently inherit unsafe server defaults.
type StreamLimits struct {
	Replicas        int
	MaxBytes        int64
	MaxMessageBytes int32
	MaxAge          time.Duration
	DuplicateWindow time.Duration
}

// EnsureStreams creates or updates the Phase 2 durable stream set with file
// storage, limits retention, and fail-closed DiscardNew capacity behavior.
func EnsureStreams(
	ctx context.Context,
	js jetstream.JetStream,
	limits StreamLimits,
) error {
	if js == nil {
		return errors.New("ensure streams: JetStream is required")
	}
	if limits.Replicas < 1 ||
		limits.MaxBytes <= 0 ||
		limits.MaxMessageBytes <= 0 ||
		limits.MaxAge <= 0 ||
		limits.DuplicateWindow <= 0 {
		return errors.New("ensure streams: explicit positive limits are required")
	}
	configs := []jetstream.StreamConfig{
		streamConfig(
			DomainEventsStream,
			[]string{"domain.v1.>"},
			"committed domain events",
			limits,
		),
		streamConfig(
			JobsStream,
			[]string{"jobs.v1.>"},
			"durable jobs",
			limits,
		),
		streamConfig(
			OpsStream,
			[]string{"ops.v1.>"},
			"operational events",
			limits,
		),
	}
	for _, config := range configs {
		if _, err := js.CreateOrUpdateStream(ctx, config); err != nil {
			return fmt.Errorf("ensure stream %s: %w", config.Name, err)
		}
	}
	return nil
}

// EnsureEngineShardStream creates one physical ordered stream for one shard.
// Separate streams make the JetStream stream sequence stable across redelivery
// and contiguous without traffic from unrelated shards.
func EnsureEngineShardStream(
	ctx context.Context,
	js jetstream.JetStream,
	shardID engine.ShardID,
	limits StreamLimits,
) error {
	if js == nil {
		return errors.New("ensure engine shard stream: JetStream is required")
	}
	if limits.Replicas < 1 ||
		limits.MaxBytes <= 0 ||
		limits.MaxMessageBytes <= 0 ||
		limits.MaxAge <= 0 ||
		limits.DuplicateWindow <= 0 {
		return errors.New(
			"ensure engine shard stream: explicit positive limits are required",
		)
	}
	name := engineShardStreamName(shardID)
	config := streamConfig(
		name,
		[]string{fmt.Sprintf("engine.input.%d.>", shardID)},
		fmt.Sprintf("ordered engine inputs for shard %d", shardID),
		limits,
	)
	if _, err := js.CreateOrUpdateStream(ctx, config); err != nil {
		return fmt.Errorf("ensure stream %s: %w", name, err)
	}
	return nil
}

func engineShardStreamName(shardID engine.ShardID) string {
	return fmt.Sprintf("%s_%d", EngineInputsStream, shardID)
}

func streamConfig(
	name string,
	subjects []string,
	description string,
	limits StreamLimits,
) jetstream.StreamConfig {
	return jetstream.StreamConfig{
		Name:              name,
		Description:       description,
		Subjects:          subjects,
		Retention:         jetstream.LimitsPolicy,
		MaxMsgs:           -1,
		MaxBytes:          limits.MaxBytes,
		Discard:           jetstream.DiscardNew,
		MaxAge:            limits.MaxAge,
		MaxMsgsPerSubject: -1,
		MaxMsgSize:        limits.MaxMessageBytes,
		Storage:           jetstream.FileStorage,
		Replicas:          limits.Replicas,
		Duplicates:        limits.DuplicateWindow,
		NoAck:             false,
	}
}

type publishAPI interface {
	PublishMsg(
		context.Context,
		*gonats.Msg,
		...jetstream.PublishOpt,
	) (*jetstream.PubAck, error)
}

// Publisher waits for the JetStream server acknowledgment and always supplies
// the PostgreSQL outbox ID as Nats-Msg-Id.
type Publisher struct {
	js publishAPI
}

// NewPublisher constructs a PostgreSQL outbox-compatible JetStream publisher.
func NewPublisher(js jetstream.JetStream) *Publisher {
	return &Publisher{js: js}
}

// Publish implements postgres.DurablePublisher.
func (publisher *Publisher) Publish(
	ctx context.Context,
	message platformpostgres.OutboxMessage,
) (uint64, error) {
	if publisher == nil || publisher.js == nil {
		return 0, errors.New("publish JetStream message: publisher is not configured")
	}
	natsMessage := gonats.NewMsg(message.Subject)
	natsMessage.Data = append([]byte(nil), message.Payload...)
	natsMessage.Header.Set(gonats.MsgIdHdr, message.MessageID.String())
	ack, err := publisher.js.PublishMsg(
		ctx,
		natsMessage,
	)
	if err != nil {
		return 0, fmt.Errorf(
			"publish JetStream message %s: %w",
			message.MessageID,
			err,
		)
	}
	if ack == nil || ack.Sequence == 0 {
		return 0, fmt.Errorf(
			"publish JetStream message %s: invalid acknowledgment",
			message.MessageID,
		)
	}
	return ack.Sequence, nil
}

// InboundMessage is the bounded transport metadata supplied to a committed
// consumer handler.
type InboundMessage struct {
	MessageID      engine.ID
	Subject        string
	Data           []byte
	StreamSequence uint64
	NumDelivered   uint64
}

// Handler must return nil only after its PostgreSQL side effect is committed.
type Handler func(context.Context, InboundMessage) error

// PullConsumer is a durable serialized JetStream consumer.
type PullConsumer struct {
	consumer jetstream.Consumer
	maxWait  time.Duration
}

// NewEnginePullConsumer creates or updates one durable consumer for one shard.
// MaxAckPending=1 makes its live delivery serial and unlimited MaxDeliver
// prevents automatic poison-message skipping.
func NewEnginePullConsumer(
	ctx context.Context,
	js jetstream.JetStream,
	shardID engine.ShardID,
	durable string,
	ackWait time.Duration,
) (*PullConsumer, error) {
	if js == nil {
		return nil, errors.New("create engine consumer: JetStream is required")
	}
	if durable == "" || ackWait <= 0 {
		return nil, errors.New(
			"create engine consumer: durable name and ack wait are required",
		)
	}
	consumer, err := js.CreateOrUpdateConsumer(
		ctx,
		engineShardStreamName(shardID),
		jetstream.ConsumerConfig{
			Name:              durable,
			Durable:           durable,
			DeliverPolicy:     jetstream.DeliverAllPolicy,
			AckPolicy:         jetstream.AckExplicitPolicy,
			AckWait:           ackWait,
			MaxDeliver:        -1,
			FilterSubject:     fmt.Sprintf("engine.input.%d.>", shardID),
			ReplayPolicy:      jetstream.ReplayInstantPolicy,
			MaxAckPending:     1,
			MaxRequestBatch:   1,
			MaxRequestExpires: ackWait,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("create engine shard %d consumer: %w", shardID, err)
	}
	return &PullConsumer{consumer: consumer, maxWait: ackWait}, nil
}

// ProcessOne fetches at most one input, invokes the committed handler, then
// synchronously acknowledges. Handler failures remain unacknowledged.
func (consumer *PullConsumer) ProcessOne(
	ctx context.Context,
	handler Handler,
) (bool, error) {
	if consumer == nil || consumer.consumer == nil {
		return false, errors.New("process JetStream input: consumer is not configured")
	}
	if handler == nil {
		return false, errors.New("process JetStream input: handler is required")
	}
	batch, err := consumer.consumer.Fetch(
		1,
		jetstream.FetchMaxWait(consumer.maxWait),
	)
	if err != nil {
		return false, fmt.Errorf("fetch JetStream input: %w", err)
	}
	processed := false
	for message := range batch.Messages() {
		processed = true
		inbound, err := decodeInboundMessage(message)
		if err != nil {
			return true, err
		}
		if err := handler(ctx, inbound); err != nil {
			nakErr := message.Nak()
			if nakErr != nil {
				return true, errors.Join(
					fmt.Errorf(
						"process JetStream message %s: %w",
						inbound.MessageID,
						err,
					),
					fmt.Errorf(
						"negative-ack JetStream message %s: %w",
						inbound.MessageID,
						nakErr,
					),
				)
			}
			return true, fmt.Errorf(
				"process JetStream message %s: %w",
				inbound.MessageID,
				err,
			)
		}
		if err := message.DoubleAck(ctx); err != nil {
			return true, fmt.Errorf(
				"ack committed JetStream message %s: %w",
				inbound.MessageID,
				err,
			)
		}
	}
	if batchErr := batch.Error(); batchErr != nil {
		return processed, fmt.Errorf("fetch JetStream input batch: %w", batchErr)
	}
	return processed, nil
}

func decodeInboundMessage(message jetstream.Msg) (InboundMessage, error) {
	messageIDText := message.Headers().Get(gonats.MsgIdHdr)
	messageID, err := engine.ParseID(messageIDText)
	if err != nil {
		return InboundMessage{}, fmt.Errorf(
			"decode JetStream message ID %q: %w",
			messageIDText,
			err,
		)
	}
	metadata, err := message.Metadata()
	if err != nil {
		return InboundMessage{}, fmt.Errorf(
			"decode JetStream message %s metadata: %w",
			messageID,
			err,
		)
	}
	return InboundMessage{
		MessageID:      messageID,
		Subject:        message.Subject(),
		Data:           append([]byte(nil), message.Data()...),
		StreamSequence: metadata.Sequence.Stream,
		NumDelivered:   metadata.NumDelivered,
	}, nil
}
