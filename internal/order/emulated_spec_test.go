package order

import (
	"testing"

	"github.com/upcomers-org/platformgo/internal/ids"
)

func requireDefaultSpecIdentity(t *testing.T, trader ids.TraderID, strategy ids.StrategyID, instrument ids.InstrumentID, client ids.ClientOrderID) {
	t.Helper()
	if trader != ids.DefaultTraderID() || strategy != ids.MustStrategyID("S-001") ||
		instrument != ids.MustInstrumentID("AUD/USD.SIM") ||
		client != ids.MustClientOrderID("O-19700101-000000-001-001-1") {
		t.Fatalf("identity = %s/%s/%s/%s", trader, strategy, instrument, client)
	}
}

func requireUniqueSpecIDs(t *testing.T, build func(*OrderSpecEventIDSequence) string) {
	t.Helper()
	sequence := NewOrderSpecEventIDSequence()
	a, b, c := build(sequence), build(sequence), build(sequence)
	if a == b || b == c || a == c {
		t.Fatalf("event IDs were not unique: %s, %s, %s", a, b, c)
	}
}

func requireReproducibleSpecIDs(t *testing.T, build func(*OrderSpecEventIDSequence) string) {
	t.Helper()
	first := NewOrderSpecEventIDSequence()
	firstRun := []string{build(first), build(first), build(first)}
	second := NewOrderSpecEventIDSequence()
	secondRun := []string{build(second), build(second), build(second)}
	for i := range firstRun {
		if firstRun[i] != secondRun[i] {
			t.Fatalf("event ID %d = %s, want %s", i, secondRun[i], firstRun[i])
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/spec/emulated.rs:73
//	test: defaults_are_sensible
func TestOrderEmulatedSpecDefaultsAreSensible(t *testing.T) {
	event := NewOrderEmulatedSpec(NewOrderSpecEventIDSequence()).Build()
	requireDefaultSpecIdentity(t, event.TraderID, event.StrategyID, event.InstrumentID, event.ClientOrderID)
	if event.TsEvent != 0 || event.TsInit != 0 {
		t.Fatalf("default event = %#v", event)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/spec/emulated.rs:86
//	test: overrides_apply_through_constructor
func TestOrderEmulatedSpecOverridesApplyThroughConstructor(t *testing.T) {
	event := NewOrderEmulatedSpec(NewOrderSpecEventIDSequence()).
		WithEventTime(1_000).WithInitTime(2_000).Build()
	if event.TsEvent != 1_000 || event.TsInit != 2_000 || event.TraderID != ids.DefaultTraderID() {
		t.Fatalf("overridden event = %#v", event)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/spec/emulated.rs:98
//	test: event_ids_are_unique_within_a_run
func TestOrderEmulatedSpecEventIDsAreUniqueWithinARun(t *testing.T) {
	requireUniqueSpecIDs(t, func(sequence *OrderSpecEventIDSequence) string {
		return NewOrderEmulatedSpec(sequence).Build().EventID
	})
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/spec/emulated.rs:109
//	test: event_id_sequence_is_reproducible
func TestOrderEmulatedSpecEventIDSequenceIsReproducible(t *testing.T) {
	requireReproducibleSpecIDs(t, func(sequence *OrderSpecEventIDSequence) string {
		return NewOrderEmulatedSpec(sequence).Build().EventID
	})
}
