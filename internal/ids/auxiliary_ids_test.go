package ids

import (
	"encoding/json"
	"testing"
)

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/actor_id.rs:104
//	test: test_string_reprs
func TestActorIDStringRepresentations(t *testing.T) {
	id := MustActorID("MyActor")
	if got := string(id); got != "MyActor" {
		t.Fatalf("inner string = %q, want %q", got, "MyActor")
	}
	if got := id.String(); got != "MyActor" {
		t.Fatalf("String() = %q, want %q", got, "MyActor")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/actor_id.rs:112
//	test: test_new_with_empty_string_panics_with_display_format
func TestActorIDEmptyPanicsWithDisplayFormat(t *testing.T) {
	requirePanicContains(t, "Condition failed: invalid string for 'value', was empty", func() {
		MustActorID("")
	})
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/component_id.rs:105
//	test: test_string_reprs
func TestComponentIDStringRepresentations(t *testing.T) {
	id := MustComponentID("RiskEngine")
	if got := string(id); got != "RiskEngine" {
		t.Fatalf("inner string = %q, want %q", got, "RiskEngine")
	}
	if got := id.String(); got != "RiskEngine" {
		t.Fatalf("String() = %q, want %q", got, "RiskEngine")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/component_id.rs:112
//	test: test_new_with_empty_string_panics_with_display_format
func TestComponentIDEmptyPanicsWithDisplayFormat(t *testing.T) {
	requirePanicContains(t, "Condition failed: invalid string for 'value', was empty", func() {
		MustComponentID("")
	})
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/exec_algorithm_id.rs:105
//	test: test_string_reprs
func TestExecAlgorithmIDStringRepresentations(t *testing.T) {
	id := MustExecAlgorithmID("001")
	if got := string(id); got != "001" {
		t.Fatalf("inner string = %q, want %q", got, "001")
	}
	if got := id.String(); got != "001" {
		t.Fatalf("String() = %q, want %q", got, "001")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/exec_algorithm_id.rs:112
//	test: test_new_with_empty_string_panics_with_display_format
func TestExecAlgorithmIDEmptyPanicsWithDisplayFormat(t *testing.T) {
	requirePanicContains(t, "Condition failed: invalid string for 'value', was empty", func() {
		MustExecAlgorithmID("")
	})
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/client_id.rs:105
//	test: test_string_reprs
func TestClientIDStringRepresentations(t *testing.T) {
	id := MustClientID("BINANCE")
	if got := string(id); got != "BINANCE" {
		t.Fatalf("inner string = %q, want %q", got, "BINANCE")
	}
	if got := id.String(); got != "BINANCE" {
		t.Fatalf("String() = %q, want %q", got, "BINANCE")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/client_id.rs:111
//	test: test_deserialize_from_owned_value
func TestClientIDDeserializeFromOwnedValue(t *testing.T) {
	var got ClientID
	if err := json.Unmarshal([]byte(`"BINANCE"`), &got); err != nil {
		t.Fatal(err)
	}
	want := MustClientID("BINANCE")
	if got != want {
		t.Fatalf("deserialized = %q, want %q", got, want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/client_id.rs:120
//	test: test_new_with_empty_string_panics_with_display_format
func TestClientIDEmptyPanicsWithDisplayFormat(t *testing.T) {
	requirePanicContains(t, "Condition failed: invalid string for 'value', was empty", func() {
		MustClientID("")
	})
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/venue_order_id.rs:100
//	test: test_string_reprs
func TestVenueOrderIDStringRepresentations(t *testing.T) {
	id := MustVenueOrderID("001")
	if got := string(id); got != "001" {
		t.Fatalf("inner string = %q, want %q", got, "001")
	}
	if got := id.String(); got != "001" {
		t.Fatalf("String() = %q, want %q", got, "001")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/venue_order_id.rs:107
//	test: test_new_with_empty_string_panics_with_display_format
func TestVenueOrderIDEmptyPanicsWithDisplayFormat(t *testing.T) {
	requirePanicContains(t, "Condition failed: invalid string for 'value', was empty", func() {
		MustVenueOrderID("")
	})
}
