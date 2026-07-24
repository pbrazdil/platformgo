package ids

import (
	"encoding/json"
	"testing"
)

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/trader_id.rs:157
//	test: test_string_reprs
func TestTraderIDStringRepresentations(t *testing.T) {
	id := DefaultTraderID()
	if got := id.String(); got != "TRADER-001" {
		t.Fatalf("String() = %q, want TRADER-001", got)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/trader_id.rs:163
//	test: test_get_tag
func TestTraderIDTag(t *testing.T) {
	if got := DefaultTraderID().Tag(); got != "001" {
		t.Fatalf("Tag() = %q, want 001", got)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/trader_id.rs:169
//	test: test_new_with_empty_name_panics
func TestTraderIDEmptyNamePanics(t *testing.T) {
	requirePanicContains(t, "name part (before '-') cannot be empty", func() {
		MustTraderID("-001")
	})
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/trader_id.rs:175
//	test: test_new_with_empty_tag_panics
func TestTraderIDEmptyTagPanics(t *testing.T) {
	requirePanicContains(t, "tag part (after '-') cannot be empty", func() {
		MustTraderID("TRADER-")
	})
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/trader_id.rs:180
//	test: test_new_checked_with_empty_name_returns_error
func TestParseTraderIDEmptyNameReturnsError(t *testing.T) {
	if _, err := ParseTraderID("-001"); err == nil {
		t.Fatal("expected error")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/trader_id.rs:185
//	test: test_new_checked_with_empty_tag_returns_error
func TestParseTraderIDEmptyTagReturnsError(t *testing.T) {
	if _, err := ParseTraderID("TRADER-"); err == nil {
		t.Fatal("expected error")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/trader_id.rs:190
//	test: test_new_checked_with_empty_name_returns_typed_error_with_stable_display
func TestParseTraderIDEmptyNameReturnsTypedErrorWithStableDisplay(t *testing.T) {
	const want = "`value` name part (before '-') cannot be empty"
	_, err := ParseTraderID("-001")
	if err == nil {
		t.Fatal("expected error")
	}
	if got := validationKind(err); got != "predicate_violation" {
		t.Fatalf("error kind = %q, want predicate_violation", got)
	}
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err, want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/trader_id.rs:207
//	test: test_new_checked_with_empty_tag_returns_typed_error_with_stable_display
func TestParseTraderIDEmptyTagReturnsTypedErrorWithStableDisplay(t *testing.T) {
	const want = "`value` tag part (after '-') cannot be empty"
	_, err := ParseTraderID("TRADER-")
	if err == nil {
		t.Fatal("expected error")
	}
	if got := validationKind(err); got != "predicate_violation" {
		t.Fatalf("error kind = %q, want predicate_violation", got)
	}
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err, want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/symbol.rs:157
//	test: test_string_reprs
func TestSymbolStringRepresentations(t *testing.T) {
	if got := MustSymbol("ETH-PERP").String(); got != "ETH-PERP" {
		t.Fatalf("String() = %q, want ETH-PERP", got)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/symbol.rs:168
//	test: test_symbol_is_composite
func TestSymbolIsComposite(t *testing.T) {
	tests := []struct {
		value string
		want  bool
	}{
		{"AUDUSD", false},
		{"AUD/USD", false},
		{"CL.FUT", true},
		{"LO.OPT", true},
		{"ES.c.0", true},
	}
	for _, test := range tests {
		t.Run(test.value, func(t *testing.T) {
			if got := MustSymbol(test.value).IsComposite(); got != test.want {
				t.Fatalf("IsComposite() = %v, want %v", got, test.want)
			}
		})
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/symbol.rs:179
//	test: test_symbol_root
func TestSymbolRoot(t *testing.T) {
	tests := []struct {
		value string
		want  string
	}{
		{"AUDUSD", "AUDUSD"},
		{"AUD/USD", "AUD/USD"},
		{"CL.FUT", "CL"},
		{"LO.OPT", "LO"},
		{"ES.c.0", "ES"},
	}
	for _, test := range tests {
		t.Run(test.value, func(t *testing.T) {
			if got := MustSymbol(test.value).Root(); got != test.want {
				t.Fatalf("Root() = %q, want %q", got, test.want)
			}
		})
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/symbol.rs:190
//	test: test_symbol_topic
func TestSymbolTopic(t *testing.T) {
	tests := []struct {
		value string
		want  string
	}{
		{"AUDUSD", "AUDUSD"},
		{"AUD/USD", "AUD/USD"},
		{"CL.FUT", "CL*"},
		{"LO.OPT", "LO*"},
		{"ES.c.0", "ES*"},
	}
	for _, test := range tests {
		t.Run(test.value, func(t *testing.T) {
			if got := MustSymbol(test.value).Topic(); got != test.want {
				t.Fatalf("Topic() = %q, want %q", got, test.want)
			}
		})
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/symbol.rs:198
//	test: test_symbol_with_invalid_values
func TestParseSymbolInvalidValues(t *testing.T) {
	for _, value := range []string{"", "   "} {
		t.Run(value, func(t *testing.T) {
			if _, err := ParseSymbol(value); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/symbol.rs:203
//	test: test_symbol_new_checked_returns_typed_error_with_stable_display
func TestParseSymbolReturnsTypedErrorWithStableDisplay(t *testing.T) {
	_, err := ParseSymbol("")
	if err == nil {
		t.Fatal("expected error")
	}
	if got := validationKind(err); got != "empty_string" {
		t.Fatalf("error kind = %q, want empty_string", got)
	}
	const want = "invalid string for 'value', was empty"
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err, want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/symbol.rs:217
//	test: test_symbol_new_with_empty_string_panics_with_display_format
func TestMustSymbolEmptyPanicsWithDisplayFormat(t *testing.T) {
	requirePanicContains(t, "Condition failed: invalid string for 'value', was empty", func() {
		MustSymbol("")
	})
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/symbol.rs:222
//	test: test_symbol_deserialize_json_with_unicode_escapes
func TestSymbolUnmarshalJSONUnicodeEscapes(t *testing.T) {
	var symbol Symbol
	if err := json.Unmarshal([]byte(`"\u9f99\u867eUSDT"`), &symbol); err != nil {
		t.Fatal(err)
	}
	if got := symbol.String(); got != "龙虾USDT" {
		t.Fatalf("symbol = %q, want 龙虾USDT", got)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/symbol.rs:228
//	test: test_symbol_deserialize_from_owned_value_with_non_ascii
func TestSymbolUnmarshalFromOwnedValueWithNonASCII(t *testing.T) {
	data, err := json.Marshal("龙虾USDT")
	if err != nil {
		t.Fatal(err)
	}
	var symbol Symbol
	if err := json.Unmarshal(data, &symbol); err != nil {
		t.Fatal(err)
	}
	if got := symbol.String(); got != "龙虾USDT" {
		t.Fatalf("symbol = %q, want 龙虾USDT", got)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/symbol.rs:235
//	test: test_symbol_serialization_roundtrip_non_ascii
func TestSymbolJSONRoundTripNonASCII(t *testing.T) {
	symbol := MustSymbol("龙虾USDT")
	data, err := json.Marshal(symbol)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); got != `"龙虾USDT"` {
		t.Fatalf("JSON = %s, want %q", got, `"龙虾USDT"`)
	}
	var decoded Symbol
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded != symbol {
		t.Fatalf("decoded = %q, want %q", decoded, symbol)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/symbol.rs:245
//	test: test_symbol_deserialize_rejects_empty_string
func TestSymbolUnmarshalJSONRejectsEmpty(t *testing.T) {
	var symbol Symbol
	if err := json.Unmarshal([]byte(`""`), &symbol); err == nil {
		t.Fatal("expected error")
	}
}
