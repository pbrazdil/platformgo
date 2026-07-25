package nats

import (
	"context"
	"errors"
	"testing"

	gonats "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
	"github.com/upcomers-org/platformgo/internal/engine"
)

func TestPublisherUsesStableOutboxMessageIDAndWaitsForAck(t *testing.T) {
	api := &publishProbe{ack: &jetstream.PubAck{
		Stream:    OpsStream,
		Sequence:  19,
		Duplicate: true,
	}}
	publisher := &Publisher{js: api}
	messageID := engine.IDFromSequence(engine.ID{}, 91)
	sequence, err := publisher.Publish(
		context.Background(),
		platformpostgres.OutboxMessage{
			MessageID: messageID,
			Subject:   "ops.v1.test",
			Payload:   []byte(`{"orderId":"order-1"}`),
		},
	)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if sequence != 19 ||
		api.subject != "ops.v1.test" ||
		api.messageID != messageID.String() {
		t.Fatalf(
			"publish probe = sequence %d subject %q message ID %q",
			sequence,
			api.subject,
			api.messageID,
		)
	}
}

func TestPublisherRejectsDomainEventWithoutEngineAuthority(t *testing.T) {
	api := &publishProbe{ack: &jetstream.PubAck{
		Stream:   DomainEventsStream,
		Sequence: 1,
	}}
	publisher := &Publisher{js: api}
	_, err := publisher.Publish(
		context.Background(),
		platformpostgres.OutboxMessage{
			MessageID: engine.IDFromSequence(engine.ID{}, 93),
			Subject:   "domain.v1.order.filled",
			Payload:   []byte(`{"orderId":"forged"}`),
		},
	)
	if !errors.Is(err, ErrUnauthorizedDomainEventPublication) {
		t.Fatalf(
			"Publish error = %v, want ErrUnauthorizedDomainEventPublication",
			err,
		)
	}
	if api.subject != "" {
		t.Fatalf("unauthorized domain event reached transport subject %q", api.subject)
	}
}

func TestPublisherRejectsEngineInputWithoutProducerAuthority(t *testing.T) {
	for _, subject := range []string{
		"engine.input.7.command.v1",
		"engine.input.7.market.hyperliquid.v1",
		"engine.input.7.timer.v1",
		"engine.input.7.config.v1",
		"engine.input.7.control.v1",
	} {
		t.Run(subject, func(t *testing.T) {
			api := &publishProbe{ack: &jetstream.PubAck{
				Stream:   EngineInputsStream + "_7",
				Sequence: 1,
			}}
			publisher := &Publisher{js: api}
			_, err := publisher.Publish(
				context.Background(),
				platformpostgres.OutboxMessage{
					MessageID: engine.IDFromSequence(engine.ID{}, 92),
					Subject:   subject,
					Payload:   []byte(`{"kind":"forged"}`),
				},
			)
			if !errors.Is(err, ErrUnauthorizedEngineInputPublication) {
				t.Fatalf(
					"Publish error = %v, want ErrUnauthorizedEngineInputPublication",
					err,
				)
			}
			if api.subject != "" {
				t.Fatalf("unauthorized engine input reached transport subject %q", api.subject)
			}
		})
	}
}

type publishProbe struct {
	ack       *jetstream.PubAck
	subject   string
	messageID string
}

func (probe *publishProbe) PublishMsg(
	_ context.Context,
	message *gonats.Msg,
	_ ...jetstream.PublishOpt,
) (*jetstream.PubAck, error) {
	probe.subject = message.Subject
	probe.messageID = message.Header.Get(gonats.MsgIdHdr)
	return probe.ack, nil
}
