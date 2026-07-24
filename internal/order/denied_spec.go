package order

import (
	"fmt"

	"github.com/upcomers-org/platformgo/internal/ids"
)

// OrderDenialReason is the stable text attached to a denied-order event.
type OrderDenialReason string

// OrderDeniedEvent is the event produced by OrderDeniedSpec.
type OrderDeniedEvent struct {
	TraderID      ids.TraderID
	StrategyID    ids.StrategyID
	InstrumentID  ids.InstrumentID
	ClientOrderID ids.ClientOrderID
	Reason        OrderDenialReason
	EventID       string
	TsEvent       uint64
	TsInit        uint64
}

func (event OrderDeniedEvent) String() string {
	return fmt.Sprintf(
		"OrderDenied(instrument_id=%s, client_order_id=%s, reason=%s, event_id=%s, ts_event=%d, ts_init=%d)",
		event.InstrumentID,
		event.ClientOrderID,
		event.Reason,
		event.EventID,
		event.TsEvent,
		event.TsInit,
	)
}

// DeniedEventIDSequence is an instance-owned deterministic event-ID source.
type DeniedEventIDSequence struct {
	next uint64
}

func NewDeniedEventIDSequence() *DeniedEventIDSequence {
	return &DeniedEventIDSequence{next: 1}
}

func (sequence *DeniedEventIDSequence) Next() string {
	value := sequence.next
	sequence.next++
	return fmt.Sprintf("00000000-0000-4000-8000-%012x", value)
}

// OrderDeniedSpec supplies sensible defaults and builds through the event constructor.
type OrderDeniedSpec struct {
	TraderID      ids.TraderID
	StrategyID    ids.StrategyID
	InstrumentID  ids.InstrumentID
	ClientOrderID ids.ClientOrderID
	Reason        OrderDenialReason
	TsEvent       uint64
	TsInit        uint64
	sequence      *DeniedEventIDSequence
}

func NewOrderDeniedSpec(sequence *DeniedEventIDSequence) OrderDeniedSpec {
	if sequence == nil {
		panic("denied event ID sequence is required")
	}
	return OrderDeniedSpec{
		TraderID:      ids.DefaultTraderID(),
		StrategyID:    ids.MustStrategyID("S-001"),
		InstrumentID:  ids.MustInstrumentID("AUD/USD.SIM"),
		ClientOrderID: ids.MustClientOrderID("O-19700101-000000-001-001-1"),
		Reason:        OrderDenialReason("TEST"),
		sequence:      sequence,
	}
}

func (spec OrderDeniedSpec) WithReason(reason OrderDenialReason) OrderDeniedSpec {
	spec.Reason = reason
	return spec
}

func (spec OrderDeniedSpec) WithEventTime(timestamp uint64) OrderDeniedSpec {
	spec.TsEvent = timestamp
	return spec
}

func (spec OrderDeniedSpec) WithInitTime(timestamp uint64) OrderDeniedSpec {
	spec.TsInit = timestamp
	return spec
}

func (spec OrderDeniedSpec) Build() OrderDeniedEvent {
	return OrderDeniedEvent{
		TraderID:      spec.TraderID,
		StrategyID:    spec.StrategyID,
		InstrumentID:  spec.InstrumentID,
		ClientOrderID: spec.ClientOrderID,
		Reason:        spec.Reason,
		EventID:       spec.sequence.Next(),
		TsEvent:       spec.TsEvent,
		TsInit:        spec.TsInit,
	}
}
