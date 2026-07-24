package order

import (
	"fmt"

	"github.com/upcomers-org/platformgo/internal/ids"
)

// OrderExpiredEvent is the event produced by OrderExpiredSpec.
type OrderExpiredEvent struct {
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

// ExpiredEventIDSequence is an instance-owned deterministic event-ID source.
type ExpiredEventIDSequence struct {
	next uint64
}

func NewExpiredEventIDSequence() *ExpiredEventIDSequence {
	return &ExpiredEventIDSequence{next: 1}
}

func (sequence *ExpiredEventIDSequence) Next() string {
	value := sequence.next
	sequence.next++
	return fmt.Sprintf("00000000-0000-4000-8000-%012x", value)
}

// OrderExpiredSpec supplies sensible defaults and builds an expired event.
type OrderExpiredSpec struct {
	TraderID       ids.TraderID
	StrategyID     ids.StrategyID
	InstrumentID   ids.InstrumentID
	ClientOrderID  ids.ClientOrderID
	TsEvent        uint64
	TsInit         uint64
	Reconciliation bool
	VenueOrderID   *ids.VenueOrderID
	AccountID      *ids.AccountID
	sequence       *ExpiredEventIDSequence
}

func NewOrderExpiredSpec(sequence *ExpiredEventIDSequence) OrderExpiredSpec {
	if sequence == nil {
		panic("expired event ID sequence is required")
	}
	return OrderExpiredSpec{
		TraderID:      ids.DefaultTraderID(),
		StrategyID:    ids.MustStrategyID("S-001"),
		InstrumentID:  ids.MustInstrumentID("AUD/USD.SIM"),
		ClientOrderID: ids.MustClientOrderID("O-19700101-000000-001-001-1"),
		sequence:      sequence,
	}
}

func (spec OrderExpiredSpec) WithVenueOrderID(value ids.VenueOrderID) OrderExpiredSpec {
	spec.VenueOrderID = &value
	return spec
}

func (spec OrderExpiredSpec) WithAccountID(value ids.AccountID) OrderExpiredSpec {
	spec.AccountID = &value
	return spec
}

func (spec OrderExpiredSpec) WithReconciliation(value bool) OrderExpiredSpec {
	spec.Reconciliation = value
	return spec
}

func (spec OrderExpiredSpec) Build() OrderExpiredEvent {
	return OrderExpiredEvent{
		TraderID:       spec.TraderID,
		StrategyID:     spec.StrategyID,
		InstrumentID:   spec.InstrumentID,
		ClientOrderID:  spec.ClientOrderID,
		EventID:        spec.sequence.Next(),
		TsEvent:        spec.TsEvent,
		TsInit:         spec.TsInit,
		Reconciliation: spec.Reconciliation,
		VenueOrderID:   copyExpiredPointer(spec.VenueOrderID),
		AccountID:      copyExpiredPointer(spec.AccountID),
	}
}

func copyExpiredPointer[T any](value *T) *T {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}
