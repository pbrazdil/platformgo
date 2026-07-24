package order

import (
	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
)

type OrderUpdatedEvent struct {
	TraderID        ids.TraderID
	StrategyID      ids.StrategyID
	InstrumentID    ids.InstrumentID
	ClientOrderID   ids.ClientOrderID
	Quantity        decimal.Quantity
	EventID         string
	TsEvent         uint64
	TsInit          uint64
	Reconciliation  bool
	VenueOrderID    *ids.VenueOrderID
	AccountID       *ids.AccountID
	Price           *decimal.Price
	TriggerPrice    *decimal.Price
	ProtectionPrice *decimal.Price
	IsQuoteQuantity bool
}

type OrderUpdatedSpec struct {
	OrderUpdatedEvent
	sequence *OrderSpecEventIDSequence
}

func NewOrderUpdatedSpec(sequence *OrderSpecEventIDSequence) OrderUpdatedSpec {
	requireOrderSpecSequence(sequence)
	return OrderUpdatedSpec{
		OrderUpdatedEvent: OrderUpdatedEvent{
			TraderID: ids.DefaultTraderID(), StrategyID: ids.MustStrategyID("S-001"),
			InstrumentID:  ids.MustInstrumentID("AUD/USD.SIM"),
			ClientOrderID: ids.MustClientOrderID("O-19700101-000000-001-001-1"),
			Quantity:      decimal.MustQuantity("100000"),
		},
		sequence: sequence,
	}
}

func (spec OrderUpdatedSpec) WithQuantity(value decimal.Quantity) OrderUpdatedSpec {
	spec.Quantity = value
	return spec
}
func (spec OrderUpdatedSpec) WithPrice(value decimal.Price) OrderUpdatedSpec {
	spec.Price = copyPointer(value)
	return spec
}
func (spec OrderUpdatedSpec) WithQuoteQuantity(value bool) OrderUpdatedSpec {
	spec.IsQuoteQuantity = value
	return spec
}
func (spec OrderUpdatedSpec) Build() OrderUpdatedEvent {
	event := spec.OrderUpdatedEvent
	event.EventID = spec.sequence.Next()
	event.VenueOrderID = copyPointerValue(event.VenueOrderID)
	event.AccountID = copyPointerValue(event.AccountID)
	event.Price = copyPointerValue(event.Price)
	event.TriggerPrice = copyPointerValue(event.TriggerPrice)
	event.ProtectionPrice = copyPointerValue(event.ProtectionPrice)
	return event
}
