package order

import "github.com/upcomers-org/platformgo/internal/ids"

type OrderModifyRejectedEvent struct {
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

type OrderModifyRejectedSpec struct {
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

func NewOrderModifyRejectedSpec(sequence *OrderSpecEventIDSequence) OrderModifyRejectedSpec {
	requireOrderSpecSequence(sequence)
	return OrderModifyRejectedSpec{
		TraderID: ids.DefaultTraderID(), StrategyID: ids.MustStrategyID("S-001"),
		InstrumentID:  ids.MustInstrumentID("AUD/USD.SIM"),
		ClientOrderID: ids.MustClientOrderID("O-19700101-000000-001-001-1"),
		Reason:        "TEST", sequence: sequence,
	}
}

func (spec OrderModifyRejectedSpec) WithReason(value string) OrderModifyRejectedSpec {
	spec.Reason = value
	return spec
}
func (spec OrderModifyRejectedSpec) WithVenueOrderID(value ids.VenueOrderID) OrderModifyRejectedSpec {
	spec.VenueOrderID = copyPointer(value)
	return spec
}
func (spec OrderModifyRejectedSpec) WithAccountID(value ids.AccountID) OrderModifyRejectedSpec {
	spec.AccountID = copyPointer(value)
	return spec
}
func (spec OrderModifyRejectedSpec) WithReconciliation(value bool) OrderModifyRejectedSpec {
	spec.Reconciliation = value
	return spec
}
func (spec OrderModifyRejectedSpec) Build() OrderModifyRejectedEvent {
	return OrderModifyRejectedEvent{
		TraderID: spec.TraderID, StrategyID: spec.StrategyID,
		InstrumentID: spec.InstrumentID, ClientOrderID: spec.ClientOrderID,
		Reason: spec.Reason, EventID: spec.sequence.Next(),
		TsEvent: spec.TsEvent, TsInit: spec.TsInit, Reconciliation: spec.Reconciliation,
		VenueOrderID: copyPointerValue(spec.VenueOrderID),
		AccountID:    copyPointerValue(spec.AccountID),
	}
}
