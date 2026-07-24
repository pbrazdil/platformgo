package order

import "github.com/upcomers-org/platformgo/internal/ids"

type OrderSubmittedEvent struct {
	TraderID      ids.TraderID
	StrategyID    ids.StrategyID
	InstrumentID  ids.InstrumentID
	ClientOrderID ids.ClientOrderID
	AccountID     ids.AccountID
	EventID       string
	TsEvent       uint64
	TsInit        uint64
}

type OrderSubmittedSpec struct {
	OrderSubmittedEvent
	sequence *OrderSpecEventIDSequence
}

func NewOrderSubmittedSpec(sequence *OrderSpecEventIDSequence) OrderSubmittedSpec {
	requireOrderSpecSequence(sequence)
	return OrderSubmittedSpec{
		OrderSubmittedEvent: OrderSubmittedEvent{
			TraderID: ids.DefaultTraderID(), StrategyID: ids.MustStrategyID("S-001"),
			InstrumentID:  ids.MustInstrumentID("AUD/USD.SIM"),
			ClientOrderID: ids.MustClientOrderID("O-19700101-000000-001-001-1"),
			AccountID:     ids.MustAccountID("SIM-001"),
		},
		sequence: sequence,
	}
}

func (spec OrderSubmittedSpec) WithAccountID(value ids.AccountID) OrderSubmittedSpec {
	spec.AccountID = value
	return spec
}
func (spec OrderSubmittedSpec) WithEventTime(value uint64) OrderSubmittedSpec {
	spec.TsEvent = value
	return spec
}
func (spec OrderSubmittedSpec) WithInitTime(value uint64) OrderSubmittedSpec {
	spec.TsInit = value
	return spec
}
func (spec OrderSubmittedSpec) Build() OrderSubmittedEvent {
	event := spec.OrderSubmittedEvent
	event.EventID = spec.sequence.Next()
	return event
}
