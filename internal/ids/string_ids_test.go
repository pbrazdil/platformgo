package ids

import (
	"encoding/json"
	"testing"
)

func requirePanicContains(t *testing.T, want string, fn func()) {
	t.Helper()
	defer func() {
		value := recover()
		if value == nil {
			t.Fatalf("expected panic containing %q", want)
		}
		got, ok := value.(string)
		if !ok || !contains(got, want) {
			t.Fatalf("panic = %v, want substring %q", value, want)
		}
	}()
	fn()
}

func contains(value, substring string) bool {
	for i := 0; i+len(substring) <= len(value); i++ {
		if value[i:i+len(substring)] == substring {
			return true
		}
	}
	return false
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/client_order_id.rs:146
//	test: test_string_reprs
func TestClientOrderIDStringRepresentations(t *testing.T) {
	id := MustClientOrderID("O-19700101-000000-001-001-1")
	if got := id.String(); got != "O-19700101-000000-001-001-1" {
		t.Fatalf("String() = %q", got)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/client_order_id.rs:153
//	test: test_new_with_empty_string_panics_with_display_format
func TestClientOrderIDEmptyPanicsWithDisplayFormat(t *testing.T) {
	requirePanicContains(t, "Condition failed: invalid string for 'value', was empty", func() {
		MustClientOrderID("")
	})
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/client_order_id.rs:158
//	test: test_optional_ustr_to_vec_client_order_ids
func TestParseOptionalClientOrderIDs(t *testing.T) {
	if got := ParseClientOrderIDs(nil); got != nil {
		t.Fatalf("nil input = %#v, want nil", got)
	}
	value := "id1,id2,id3"
	got := ParseClientOrderIDs(&value)
	want := []ClientOrderID{"id1", "id2", "id3"}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("id[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/client_order_id.rs:171
//	test: test_optional_vec_client_order_ids_to_ustr
func TestFormatOptionalClientOrderIDs(t *testing.T) {
	if got := FormatClientOrderIDs(nil); got != nil {
		t.Fatalf("nil input = %q, want nil", *got)
	}
	got := FormatClientOrderIDs([]ClientOrderID{
		MustClientOrderID("id1"),
		MustClientOrderID("id2"),
		MustClientOrderID("id3"),
	})
	if got == nil || *got != "id1,id2,id3" {
		t.Fatalf("formatted = %v, want id1,id2,id3", got)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/order_list_id.rs:105
//	test: test_string_reprs
func TestOrderListIDStringRepresentations(t *testing.T) {
	if got := MustOrderListID("001").String(); got != "001" {
		t.Fatalf("String() = %q", got)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/order_list_id.rs:112
//	test: test_new_with_empty_string_panics_with_display_format
func TestOrderListIDEmptyPanicsWithDisplayFormat(t *testing.T) {
	requirePanicContains(t, "Condition failed: invalid string for 'value', was empty", func() {
		MustOrderListID("")
	})
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/position_id.rs:112
//	test: test_string_reprs
func TestPositionIDStringRepresentations(t *testing.T) {
	if got := MustPositionID("P-123456789").String(); got != "P-123456789" {
		t.Fatalf("String() = %q", got)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/position_id.rs:119
//	test: test_new_with_empty_string_panics_with_display_format
func TestPositionIDEmptyPanicsWithDisplayFormat(t *testing.T) {
	requirePanicContains(t, "Condition failed: invalid string for 'value', was empty", func() {
		MustPositionID("")
	})
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/position_id.rs:124
//	test: test_deserialize_json_with_unicode_escapes
func TestPositionIDUnmarshalJSONUnicodeEscapes(t *testing.T) {
	var id PositionID
	if err := json.Unmarshal([]byte(`"P-\u9f99\u867e-1"`), &id); err != nil {
		t.Fatal(err)
	}
	if got := id.String(); got != "P-龙虾-1" {
		t.Fatalf("id = %q", got)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/position_id.rs:130
//	test: test_serialization_roundtrip_non_ascii
func TestPositionIDJSONRoundTripNonASCII(t *testing.T) {
	id := MustPositionID("P-龙虾-1")
	data, err := json.Marshal(id)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); got != `"P-龙虾-1"` {
		t.Fatalf("JSON = %s", got)
	}
	var decoded PositionID
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded != id {
		t.Fatalf("decoded = %q, want %q", decoded, id)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/position_id.rs:140
//	test: test_deserialize_rejects_empty_string
func TestPositionIDUnmarshalJSONRejectsEmpty(t *testing.T) {
	var id PositionID
	if err := json.Unmarshal([]byte(`""`), &id); err == nil {
		t.Fatal("expected error")
	}
}
