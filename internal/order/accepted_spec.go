package order

import (
	"fmt"

	"github.com/upcomers-org/platformgo/internal/ids"
)

// OrderAcceptedEvent is the event produced by OrderAcceptedSpec.
type OrderAcceptedEvent struct {
	TraderID       ids.TraderID
	StrategyID     ids.StrategyID
	InstrumentID   ids.InstrumentID
	ClientOrderID  ids.ClientOrderID
	VenueOrderID   ids.VenueOrderID
	AccountID      ids.AccountID
	EventID        string
	TsEvent        uint64
	TsInit         uint64
	Reconciliation bool
}

// AcceptedEventIDSequence is an instance-owned deterministic event-ID source.
type AcceptedEventIDSequence struct {
	next uint64
}

func NewAcceptedEventIDSequence() *AcceptedEventIDSequence {
	return &AcceptedEventIDSequence{next: 1}
}

func (sequence *AcceptedEventIDSequence) Next() string {
	value := sequence.next
	sequence.next++
	return fmt.Sprintf("00000000-0000-4000-8000-%012x", value)
}

// OrderAcceptedSpec supplies sensible defaults and builds an accepted event.
type OrderAcceptedSpec struct {
	TraderID       ids.TraderID
	StrategyID     ids.StrategyID
	InstrumentID   ids.InstrumentID
	ClientOrderID  ids.ClientOrderID
	VenueOrderID   ids.VenueOrderID
	AccountID      ids.AccountID
	TsEvent        uint64
	TsInit         uint64
	Reconciliation bool
	sequence       *AcceptedEventIDSequence
}

func NewOrderAcceptedSpec(sequence *AcceptedEventIDSequence) OrderAcceptedSpec {
	if sequence == nil {
		panic("accepted event ID sequence is required")
	}
	return OrderAcceptedSpec{
		TraderID:      ids.DefaultTraderID(),
		StrategyID:    ids.MustStrategyID("S-001"),
		InstrumentID:  ids.MustInstrumentID("AUD/USD.SIM"),
		ClientOrderID: ids.MustClientOrderID("O-19700101-000000-001-001-1"),
		VenueOrderID:  ids.MustVenueOrderID("001"),
		AccountID:     ids.MustAccountID("SIM-001"),
		sequence:      sequence,
	}
}

func (spec OrderAcceptedSpec) WithVenueOrderID(value ids.VenueOrderID) OrderAcceptedSpec {
	spec.VenueOrderID = value
	return spec
}

func (spec OrderAcceptedSpec) WithReconciliation(value bool) OrderAcceptedSpec {
	spec.Reconciliation = value
	return spec
}

func (spec OrderAcceptedSpec) Build() OrderAcceptedEvent {
	return OrderAcceptedEvent{
		TraderID:       spec.TraderID,
		StrategyID:     spec.StrategyID,
		InstrumentID:   spec.InstrumentID,
		ClientOrderID:  spec.ClientOrderID,
		VenueOrderID:   spec.VenueOrderID,
		AccountID:      spec.AccountID,
		EventID:        spec.sequence.Next(),
		TsEvent:        spec.TsEvent,
		TsInit:         spec.TsInit,
		Reconciliation: spec.Reconciliation,
	}
}
