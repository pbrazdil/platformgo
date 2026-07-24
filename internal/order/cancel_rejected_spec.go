package order

import "github.com/upcomers-org/platformgo/internal/ids"

type OrderCancelRejectedEvent struct {
	TraderID       ids.TraderID
	StrategyID     ids.StrategyID
	InstrumentID   ids.InstrumentID
	ClientOrderID  ids.ClientOrderID
	Reason         string
	EventID        string
	TsEvent        uint64
	TsInit         uint64
	Reconciliation bool
	VenueOrderID   *ids.VenueOrderID
	AccountID      *ids.AccountID
}

type OrderCancelRejectedSpec struct {
	TraderID       ids.TraderID
	StrategyID     ids.StrategyID
	InstrumentID   ids.InstrumentID
	ClientOrderID  ids.ClientOrderID
	Reason         string
	TsEvent        uint64
	TsInit         uint64
	Reconciliation bool
	VenueOrderID   *ids.VenueOrderID
	AccountID      *ids.AccountID
	sequence       *OrderSpecEventIDSequence
}

func NewOrderCancelRejectedSpec(sequence *OrderSpecEventIDSequence) OrderCancelRejectedSpec {
	requireOrderSpecSequence(sequence)
	return OrderCancelRejectedSpec{
		TraderID: ids.DefaultTraderID(), StrategyID: ids.MustStrategyID("S-001"),
		InstrumentID:  ids.MustInstrumentID("AUD/USD.SIM"),
		ClientOrderID: ids.MustClientOrderID("O-19700101-000000-001-001-1"),
		Reason:        "TEST", sequence: sequence,
	}
}

func (spec OrderCancelRejectedSpec) WithReason(value string) OrderCancelRejectedSpec {
	spec.Reason = value
	return spec
}
func (spec OrderCancelRejectedSpec) WithVenueOrderID(value ids.VenueOrderID) OrderCancelRejectedSpec {
	spec.VenueOrderID = copyPointer(value)
	return spec
}
func (spec OrderCancelRejectedSpec) WithAccountID(value ids.AccountID) OrderCancelRejectedSpec {
	spec.AccountID = copyPointer(value)
	return spec
}
func (spec OrderCancelRejectedSpec) WithReconciliation(value bool) OrderCancelRejectedSpec {
	spec.Reconciliation = value
	return spec
}
func (spec OrderCancelRejectedSpec) Build() OrderCancelRejectedEvent {
	return OrderCancelRejectedEvent{
		TraderID: spec.TraderID, StrategyID: spec.StrategyID,
		InstrumentID: spec.InstrumentID, ClientOrderID: spec.ClientOrderID,
		Reason: spec.Reason, EventID: spec.sequence.Next(),
		TsEvent: spec.TsEvent, TsInit: spec.TsInit, Reconciliation: spec.Reconciliation,
		VenueOrderID: copyPointerValue(spec.VenueOrderID),
		AccountID:    copyPointerValue(spec.AccountID),
	}
}
