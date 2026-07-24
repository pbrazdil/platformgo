package order

import (
	"fmt"

	"github.com/upcomers-org/platformgo/internal/ids"
)

// OrderPendingCancelEvent is the event produced by OrderPendingCancelSpec.
type OrderPendingCancelEvent struct {
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

func NewOrderPendingCancelEvent(
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
) OrderPendingCancelEvent {
	return OrderPendingCancelEvent{
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

// PendingCancelEventIDSequence is an instance-owned deterministic event-ID source.
type PendingCancelEventIDSequence struct {
	next uint64
}

func NewPendingCancelEventIDSequence() *PendingCancelEventIDSequence {
	return &PendingCancelEventIDSequence{next: 1}
}

func (sequence *PendingCancelEventIDSequence) Next() string {
	value := sequence.next
	sequence.next++
	return fmt.Sprintf("00000000-0000-4000-8000-%012x", value)
}

// OrderPendingCancelSpec supplies sensible defaults and builds through the event constructor.
type OrderPendingCancelSpec struct {
	TraderID       ids.TraderID
	StrategyID     ids.StrategyID
	InstrumentID   ids.InstrumentID
	ClientOrderID  ids.ClientOrderID
	AccountID      *ids.AccountID
	TsEvent        uint64
	TsInit         uint64
	Reconciliation bool
	VenueOrderID   *ids.VenueOrderID
	sequence       *PendingCancelEventIDSequence
}

func NewOrderPendingCancelSpec(sequence *PendingCancelEventIDSequence) OrderPendingCancelSpec {
	if sequence == nil {
		panic("pending-cancel event ID sequence is required")
	}
	return OrderPendingCancelSpec{
		TraderID:      ids.DefaultTraderID(),
		StrategyID:    ids.MustStrategyID("S-001"),
		InstrumentID:  ids.MustInstrumentID("AUD/USD.SIM"),
		ClientOrderID: ids.MustClientOrderID("O-19700101-000000-001-001-1"),
		sequence:      sequence,
	}
}

func (spec OrderPendingCancelSpec) WithAccountID(value ids.AccountID) OrderPendingCancelSpec {
	spec.AccountID = copyPointer(value)
	return spec
}

func (spec OrderPendingCancelSpec) WithOptionalAccountID(value *ids.AccountID) OrderPendingCancelSpec {
	spec.AccountID = copyPointerValue(value)
	return spec
}

func (spec OrderPendingCancelSpec) WithVenueOrderID(value ids.VenueOrderID) OrderPendingCancelSpec {
	spec.VenueOrderID = copyPointer(value)
	return spec
}

func (spec OrderPendingCancelSpec) WithReconciliation(value bool) OrderPendingCancelSpec {
	spec.Reconciliation = value
	return spec
}

func (spec OrderPendingCancelSpec) Build() OrderPendingCancelEvent {
	return NewOrderPendingCancelEvent(
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
