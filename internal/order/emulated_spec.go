package order

import (
	"fmt"

	"github.com/upcomers-org/platformgo/internal/ids"
)

// OrderSpecEventIDSequence is an instance-owned deterministic event-ID source.
type OrderSpecEventIDSequence struct {
	next uint64
}

func NewOrderSpecEventIDSequence() *OrderSpecEventIDSequence {
	return &OrderSpecEventIDSequence{next: 1}
}

func (sequence *OrderSpecEventIDSequence) Next() string {
	value := sequence.next
	sequence.next++
	return fmt.Sprintf("00000000-0000-4000-8000-%012x", value)
}

type OrderEmulatedEvent struct {
	TraderID      ids.TraderID
	StrategyID    ids.StrategyID
	InstrumentID  ids.InstrumentID
	ClientOrderID ids.ClientOrderID
	EventID       string
	TsEvent       uint64
	TsInit        uint64
}

type OrderEmulatedSpec struct {
	TraderID      ids.TraderID
	StrategyID    ids.StrategyID
	InstrumentID  ids.InstrumentID
	ClientOrderID ids.ClientOrderID
	TsEvent       uint64
	TsInit        uint64
	sequence      *OrderSpecEventIDSequence
}

func NewOrderEmulatedSpec(sequence *OrderSpecEventIDSequence) OrderEmulatedSpec {
	requireOrderSpecSequence(sequence)
	return OrderEmulatedSpec{
		TraderID: ids.DefaultTraderID(), StrategyID: ids.MustStrategyID("S-001"),
		InstrumentID:  ids.MustInstrumentID("AUD/USD.SIM"),
		ClientOrderID: ids.MustClientOrderID("O-19700101-000000-001-001-1"),
		sequence:      sequence,
	}
}

func (spec OrderEmulatedSpec) WithEventTime(value uint64) OrderEmulatedSpec {
	spec.TsEvent = value
	return spec
}

func (spec OrderEmulatedSpec) WithInitTime(value uint64) OrderEmulatedSpec {
	spec.TsInit = value
	return spec
}

func (spec OrderEmulatedSpec) Build() OrderEmulatedEvent {
	return OrderEmulatedEvent{
		TraderID: spec.TraderID, StrategyID: spec.StrategyID,
		InstrumentID: spec.InstrumentID, ClientOrderID: spec.ClientOrderID,
		EventID: spec.sequence.Next(), TsEvent: spec.TsEvent, TsInit: spec.TsInit,
	}
}

func requireOrderSpecSequence(sequence *OrderSpecEventIDSequence) {
	if sequence == nil {
		panic("event ID sequence is required")
	}
}
