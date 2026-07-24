package ids

import (
	"bytes"
	"encoding/json"
	"testing"
)

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/trade_id.rs:142
//	test: test_trade_id_new_valid
func TestTradeIDNewValid(t *testing.T) {
	if got := MustTradeID("TRADE12345").String(); got != "TRADE12345" {
		t.Fatalf("String() = %q", got)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/trade_id.rs:148
//	test: test_trade_id_new_checked_returns_typed_error_with_stable_display
func TestTradeIDNewCheckedReturnsTypedErrorWithStableDisplay(t *testing.T) {
	_, err := ParseTradeID("")
	if err == nil {
		t.Fatal("expected error")
	}
	if got := validationKind(err); got != "predicate_violation" {
		t.Fatalf("error kind = %q", got)
	}
	if got := err.Error(); got != "String is empty" {
		t.Fatalf("error = %q", got)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/trade_id.rs:162
//	test: test_trade_id_new_invalid_length
func TestTradeIDNewInvalidLength(t *testing.T) {
	requirePanicContains(t, "exceeds maximum length", func() {
		MustTradeID("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	})
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/trade_id.rs:171
//	test: test_trade_id_from_valid_bytes
func TestTradeIDFromValidBytes(t *testing.T) {
	for _, test := range []struct {
		input []byte
		want  string
	}{
		{[]byte("1234567890"), "1234567890"},
		{[]byte("ABCDEFGHIJKLMNOPQRSTUVWXYZ1234"), "ABCDEFGHIJKLMNOPQRSTUVWXYZ1234"},
		{[]byte("1234567890\x00"), "1234567890"},
		{[]byte("ABCDEFGHIJKLMNOPQRSTUVWXYZ1234\x00"), "ABCDEFGHIJKLMNOPQRSTUVWXYZ1234"},
	} {
		id, err := ParseTradeIDBytes(test.input)
		if err != nil {
			t.Errorf("%q: %v", test.input, err)
			continue
		}
		if got := id.String(); got != test.want {
			t.Errorf("%q: String() = %q, want %q", test.input, got, test.want)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/trade_id.rs:178
//	test: test_trade_id_from_bytes_empty
func TestTradeIDFromBytesEmpty(t *testing.T) {
	requirePanicContains(t, "String is empty", func() {
		MustTradeIDBytes(nil)
	})
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/trade_id.rs:184
//	test: test_trade_id_single_null_byte
func TestTradeIDSingleNullByte(t *testing.T) {
	requirePanicContains(t, "String is empty", func() {
		MustTradeIDBytes([]byte{0})
	})
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/trade_id.rs:192
//	test: test_trade_id_exceeds_max_length
func TestTradeIDExceedsMaxLength(t *testing.T) {
	for _, input := range [][]byte{
		[]byte("ABCDEFGHIJKLMNOPQRSTUVWXYZ12345678901"),
		[]byte("ABCDEFGHIJKLMNOPQRSTUVWXYZ12345678901\x00"),
	} {
		requirePanicContains(t, "exceeds maximum length", func() {
			MustTradeIDBytes(input)
		})
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/trade_id.rs:197
//	test: test_trade_id_with_null_terminator_at_max_length
func TestTradeIDWithNullTerminatorAtMaxLength(t *testing.T) {
	id, err := ParseTradeIDBytes([]byte("ABCDEFGHIJKLMNOPQRSTUVWXYZ1234567890\x00"))
	if err != nil {
		t.Fatal(err)
	}
	if got := id.String(); got != "ABCDEFGHIJKLMNOPQRSTUVWXYZ1234567890" {
		t.Fatalf("String() = %q", got)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/trade_id.rs:204
//	test: test_trade_id_as_cstr
//
// Adaptations:
//   - Rust CStr is represented as a fresh NUL-terminated byte slice.
func TestTradeIDAsCStr(t *testing.T) {
	got := MustTradeID("TRADE12345").CBytes()
	if !bytes.Equal(got, []byte("TRADE12345\x00")) {
		t.Fatalf("CBytes() = %q", got)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/trade_id.rs:210
//	test: test_trade_id_as_str
func TestTradeIDAsStr(t *testing.T) {
	if got := MustTradeID("TRADE12345").String(); got != "TRADE12345" {
		t.Fatalf("String() = %q", got)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/trade_id.rs:216
//	test: test_trade_id_equality
func TestTradeIDEquality(t *testing.T) {
	if MustTradeID("TRADE12345") != MustTradeID("TRADE12345") {
		t.Fatal("equal trade IDs compare unequal")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/trade_id.rs:223
//	test: test_string_reprs
func TestTradeIDStringRepresentations(t *testing.T) {
	id := MustTradeID("1234567890")
	if id.String() != "1234567890" {
		t.Fatalf("String() = %q", id)
	}
	if got := id.DebugString(); got != "TradeId('1234567890')" {
		t.Fatalf("DebugString() = %q", got)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/trade_id.rs:230
//	test: test_trade_id_ordering
func TestTradeIDOrdering(t *testing.T) {
	if !(MustTradeID("TRADE12345") < MustTradeID("TRADE12346")) {
		t.Fatal("TRADE12345 should sort before TRADE12346")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/trade_id.rs:237
//	test: test_trade_id_serialization
func TestTradeIDSerialization(t *testing.T) {
	source := MustTradeID("TRADE12345")
	data, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); got != `"TRADE12345"` {
		t.Fatalf("JSON = %s", got)
	}
	var decoded TradeID
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded != source {
		t.Fatalf("decoded = %q, want %q", decoded, source)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/trade_id.rs:247
//	test: test_trade_id_deserialize_inside_tagged_enum
func TestTradeIDDeserializeInsideTaggedEnum(t *testing.T) {
	var wrapper struct {
		Type string  `json:"type"`
		ID   TradeID `json:"id"`
	}
	if err := json.Unmarshal([]byte(`{"type":"Trade","id":"TRADE12345"}`), &wrapper); err != nil {
		t.Fatal(err)
	}
	if wrapper.Type != "Trade" || wrapper.ID.String() != "TRADE12345" {
		t.Fatalf("unexpected wrapper: %+v", wrapper)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/trade_id.rs:260
//	test: test_trade_id_deserialize_from_serde_json_value
func TestTradeIDDeserializeFromSerdeJSONValue(t *testing.T) {
	data, err := json.Marshal(any("TRADE12345"))
	if err != nil {
		t.Fatal(err)
	}
	var id TradeID
	if err := json.Unmarshal(data, &id); err != nil {
		t.Fatal(err)
	}
	if got := id.String(); got != "TRADE12345" {
		t.Fatalf("String() = %q", got)
	}
}
