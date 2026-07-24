package nats

import (
	"context"
	"testing"

	gonats "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
	"github.com/upcomers-org/platformgo/internal/engine"
)

func TestPublisherUsesStableOutboxMessageIDAndWaitsForAck(t *testing.T) {
	api := &publishProbe{ack: &jetstream.PubAck{
		Stream:    DomainEventsStream,
		Sequence:  19,
		Duplicate: true,
	}}
	publisher := &Publisher{js: api}
	messageID := engine.IDFromSequence(engine.ID{}, 91)
	sequence, err := publisher.Publish(
		context.Background(),
		platformpostgres.OutboxMessage{
			MessageID: messageID,
			Subject:   "domain.v1.order.filled",
			Payload:   []byte(`{"orderId":"order-1"}`),
		},
	)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if sequence != 19 ||
		api.subject != "domain.v1.order.filled" ||
		api.messageID != messageID.String() {
		t.Fatalf(
			"publish probe = sequence %d subject %q message ID %q",
			sequence,
			api.subject,
			api.messageID,
		)
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
