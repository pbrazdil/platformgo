package order

import "github.com/upcomers-org/platformgo/internal/ids"

type OrderTriggeredEvent struct {
	TraderID       ids.TraderID
	StrategyID     ids.StrategyID
	InstrumentID   ids.InstrumentID
	ClientOrderID  ids.ClientOrderID
	EventID        string
	TsEvent        uint64
	TsInit         uint64
	Reconciliation bool
	VenueOrderID   *ids.VenueOrderID
	AccountID      *ids.AccountID
}

type OrderTriggeredSpec struct {
	OrderTriggeredEvent
	sequence *OrderSpecEventIDSequence
}

func NewOrderTriggeredSpec(sequence *OrderSpecEventIDSequence) OrderTriggeredSpec {
	requireOrderSpecSequence(sequence)
	return OrderTriggeredSpec{
		OrderTriggeredEvent: OrderTriggeredEvent{
			TraderID: ids.DefaultTraderID(), StrategyID: ids.MustStrategyID("S-001"),
			InstrumentID:  ids.MustInstrumentID("AUD/USD.SIM"),
			ClientOrderID: ids.MustClientOrderID("O-19700101-000000-001-001-1"),
		},
		sequence: sequence,
	}
}

func (spec OrderTriggeredSpec) WithVenueOrderID(value ids.VenueOrderID) OrderTriggeredSpec {
	spec.VenueOrderID = copyPointer(value)
	return spec
}
func (spec OrderTriggeredSpec) WithAccountID(value ids.AccountID) OrderTriggeredSpec {
	spec.AccountID = copyPointer(value)
	return spec
}
func (spec OrderTriggeredSpec) WithReconciliation(value bool) OrderTriggeredSpec {
	spec.Reconciliation = value
	return spec
}
func (spec OrderTriggeredSpec) Build() OrderTriggeredEvent {
	event := spec.OrderTriggeredEvent
	event.EventID = spec.sequence.Next()
	event.VenueOrderID = copyPointerValue(event.VenueOrderID)
	event.AccountID = copyPointerValue(event.AccountID)
	return event
}
