package order

import "github.com/upcomers-org/platformgo/internal/ids"

type OrderRejectedEvent struct {
	TraderID       ids.TraderID
	StrategyID     ids.StrategyID
	InstrumentID   ids.InstrumentID
	ClientOrderID  ids.ClientOrderID
	AccountID      ids.AccountID
	Reason         string
	EventID        string
	TsEvent        uint64
	TsInit         uint64
	Reconciliation bool
	DuePostOnly    bool
}

type OrderRejectedSpec struct {
	OrderRejectedEvent
	sequence *OrderSpecEventIDSequence
}

func NewOrderRejectedSpec(sequence *OrderSpecEventIDSequence) OrderRejectedSpec {
	requireOrderSpecSequence(sequence)
	return OrderRejectedSpec{
		OrderRejectedEvent: OrderRejectedEvent{
			TraderID: ids.DefaultTraderID(), StrategyID: ids.MustStrategyID("S-001"),
			InstrumentID:  ids.MustInstrumentID("AUD/USD.SIM"),
			ClientOrderID: ids.MustClientOrderID("O-19700101-000000-001-001-1"),
			AccountID:     ids.MustAccountID("SIM-001"), Reason: "TEST",
		},
		sequence: sequence,
	}
}

func (spec OrderRejectedSpec) WithReason(value string) OrderRejectedSpec {
	spec.Reason = value
	return spec
}
func (spec OrderRejectedSpec) WithReconciliation(value bool) OrderRejectedSpec {
	spec.Reconciliation = value
	return spec
}
func (spec OrderRejectedSpec) WithDuePostOnly(value bool) OrderRejectedSpec {
	spec.DuePostOnly = value
	return spec
}
func (spec OrderRejectedSpec) Build() OrderRejectedEvent {
	event := spec.OrderRejectedEvent
	event.EventID = spec.sequence.Next()
	return event
}
