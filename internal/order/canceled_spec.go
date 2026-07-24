package order

import (
	"fmt"

	"github.com/upcomers-org/platformgo/internal/ids"
)

// OrderCanceledEvent is the event produced by OrderCanceledSpec.
type OrderCanceledEvent struct {
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

// CanceledEventIDSequence is an instance-owned deterministic event-ID source.
type CanceledEventIDSequence struct {
	next uint64
}

func NewCanceledEventIDSequence() *CanceledEventIDSequence {
	return &CanceledEventIDSequence{next: 1}
}

func (sequence *CanceledEventIDSequence) Next() string {
	value := sequence.next
	sequence.next++
	return fmt.Sprintf("00000000-0000-4000-8000-%012x", value)
}

// OrderCanceledSpec supplies sensible defaults and builds a canceled event.
type OrderCanceledSpec struct {
	TraderID       ids.TraderID
	StrategyID     ids.StrategyID
	InstrumentID   ids.InstrumentID
	ClientOrderID  ids.ClientOrderID
	TsEvent        uint64
	TsInit         uint64
	Reconciliation bool
	VenueOrderID   *ids.VenueOrderID
	AccountID      *ids.AccountID
	sequence       *CanceledEventIDSequence
}

func NewOrderCanceledSpec(sequence *CanceledEventIDSequence) OrderCanceledSpec {
	if sequence == nil {
		panic("canceled event ID sequence is required")
	}
	return OrderCanceledSpec{
		TraderID:      ids.DefaultTraderID(),
		StrategyID:    ids.MustStrategyID("S-001"),
		InstrumentID:  ids.MustInstrumentID("AUD/USD.SIM"),
		ClientOrderID: ids.MustClientOrderID("O-19700101-000000-001-001-1"),
		sequence:      sequence,
	}
}

func (spec OrderCanceledSpec) WithVenueOrderID(value ids.VenueOrderID) OrderCanceledSpec {
	spec.VenueOrderID = &value
	return spec
}

func (spec OrderCanceledSpec) WithAccountID(value ids.AccountID) OrderCanceledSpec {
	spec.AccountID = &value
	return spec
}

func (spec OrderCanceledSpec) WithReconciliation(value bool) OrderCanceledSpec {
	spec.Reconciliation = value
	return spec
}

func (spec OrderCanceledSpec) Build() OrderCanceledEvent {
	return OrderCanceledEvent{
		TraderID:       spec.TraderID,
		StrategyID:     spec.StrategyID,
		InstrumentID:   spec.InstrumentID,
		ClientOrderID:  spec.ClientOrderID,
		EventID:        spec.sequence.Next(),
		TsEvent:        spec.TsEvent,
		TsInit:         spec.TsInit,
		Reconciliation: spec.Reconciliation,
		VenueOrderID:   copyCanceledPointer(spec.VenueOrderID),
		AccountID:      copyCanceledPointer(spec.AccountID),
	}
}

func copyCanceledPointer[T any](value *T) *T {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}
