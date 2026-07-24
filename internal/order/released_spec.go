package order

import (
	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
)

type OrderReleasedEvent struct {
	TraderID      ids.TraderID
	StrategyID    ids.StrategyID
	InstrumentID  ids.InstrumentID
	ClientOrderID ids.ClientOrderID
	ReleasedPrice decimal.Price
	EventID       string
	TsEvent       uint64
	TsInit        uint64
}

type OrderReleasedSpec struct {
	OrderReleasedEvent
	sequence *OrderSpecEventIDSequence
}

func NewOrderReleasedSpec(sequence *OrderSpecEventIDSequence) OrderReleasedSpec {
	requireOrderSpecSequence(sequence)
	return OrderReleasedSpec{
		OrderReleasedEvent: OrderReleasedEvent{
			TraderID: ids.DefaultTraderID(), StrategyID: ids.MustStrategyID("S-001"),
			InstrumentID:  ids.MustInstrumentID("AUD/USD.SIM"),
			ClientOrderID: ids.MustClientOrderID("O-19700101-000000-001-001-1"),
			ReleasedPrice: decimal.MustPrice("1.00000"),
		},
		sequence: sequence,
	}
}

func (spec OrderReleasedSpec) WithReleasedPrice(value decimal.Price) OrderReleasedSpec {
	spec.ReleasedPrice = value
	return spec
}
func (spec OrderReleasedSpec) Build() OrderReleasedEvent {
	event := spec.OrderReleasedEvent
	event.EventID = spec.sequence.Next()
	return event
}
