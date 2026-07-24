package order

import (
	"fmt"

	"github.com/upcomers-org/platformgo/internal/ids"
)

// OrderPendingUpdateEvent is the event produced by OrderPendingUpdateSpec.
type OrderPendingUpdateEvent struct {
	TraderID       ids.TraderID
	StrategyID     ids.StrategyID
	InstrumentID   ids.InstrumentID
	ClientOrderID  ids.ClientOrderID
	AccountID      *ids.AccountID
	EventID        string
	TsEvent        uint64
	TsInit         uint64
	Reconciliation bool
	VenueOrderID   *ids.VenueOrderID
}

func NewOrderPendingUpdateEvent(
	traderID ids.TraderID,
	strategyID ids.StrategyID,
	instrumentID ids.InstrumentID,
	clientOrderID ids.ClientOrderID,
	accountID *ids.AccountID,
	eventID string,
	tsEvent uint64,
	tsInit uint64,
	reconciliation bool,
	venueOrderID *ids.VenueOrderID,
) OrderPendingUpdateEvent {
	return OrderPendingUpdateEvent{
		TraderID:       traderID,
		StrategyID:     strategyID,
		InstrumentID:   instrumentID,
		ClientOrderID:  clientOrderID,
		AccountID:      copyPointerValue(accountID),
		EventID:        eventID,
		TsEvent:        tsEvent,
		TsInit:         tsInit,
		Reconciliation: reconciliation,
		VenueOrderID:   copyPointerValue(venueOrderID),
	}
}

// PendingUpdateEventIDSequence is an instance-owned deterministic event-ID source.
type PendingUpdateEventIDSequence struct {
	next uint64
}

func NewPendingUpdateEventIDSequence() *PendingUpdateEventIDSequence {
	return &PendingUpdateEventIDSequence{next: 1}
}

func (sequence *PendingUpdateEventIDSequence) Next() string {
	value := sequence.next
	sequence.next++
	return fmt.Sprintf("00000000-0000-4000-8000-%012x", value)
}

// OrderPendingUpdateSpec supplies sensible defaults and builds through the event constructor.
type OrderPendingUpdateSpec struct {
	TraderID       ids.TraderID
	StrategyID     ids.StrategyID
	InstrumentID   ids.InstrumentID
	ClientOrderID  ids.ClientOrderID
	AccountID      *ids.AccountID
	TsEvent        uint64
	TsInit         uint64
	Reconciliation bool
	VenueOrderID   *ids.VenueOrderID
	sequence       *PendingUpdateEventIDSequence
}

func NewOrderPendingUpdateSpec(sequence *PendingUpdateEventIDSequence) OrderPendingUpdateSpec {
	if sequence == nil {
		panic("pending-update event ID sequence is required")
	}
	return OrderPendingUpdateSpec{
		TraderID:      ids.DefaultTraderID(),
		StrategyID:    ids.MustStrategyID("S-001"),
		InstrumentID:  ids.MustInstrumentID("AUD/USD.SIM"),
		ClientOrderID: ids.MustClientOrderID("O-19700101-000000-001-001-1"),
		sequence:      sequence,
	}
}

func (spec OrderPendingUpdateSpec) WithAccountID(value ids.AccountID) OrderPendingUpdateSpec {
	spec.AccountID = copyPointer(value)
	return spec
}

func (spec OrderPendingUpdateSpec) WithOptionalAccountID(value *ids.AccountID) OrderPendingUpdateSpec {
	spec.AccountID = copyPointerValue(value)
	return spec
}

func (spec OrderPendingUpdateSpec) WithVenueOrderID(value ids.VenueOrderID) OrderPendingUpdateSpec {
	spec.VenueOrderID = copyPointer(value)
	return spec
}

func (spec OrderPendingUpdateSpec) WithReconciliation(value bool) OrderPendingUpdateSpec {
	spec.Reconciliation = value
	return spec
}

func (spec OrderPendingUpdateSpec) Build() OrderPendingUpdateEvent {
	return NewOrderPendingUpdateEvent(
		spec.TraderID,
		spec.StrategyID,
		spec.InstrumentID,
		spec.ClientOrderID,
		spec.AccountID,
		spec.sequence.Next(),
		spec.TsEvent,
		spec.TsInit,
		spec.Reconciliation,
		spec.VenueOrderID,
	)
}
