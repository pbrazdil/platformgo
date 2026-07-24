package ids

import (
	"encoding/json"
	"testing"
)

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/strategy_id.rs:155
//	test: test_string_reprs
func TestStrategyIDStringRepresentations(t *testing.T) {
	id := MustStrategyID("EMACross-001")
	if got := string(id); got != "EMACross-001" {
		t.Fatalf("inner string = %q, want %q", got, "EMACross-001")
	}
	if got := id.String(); got != "EMACross-001" {
		t.Fatalf("String() = %q, want %q", got, "EMACross-001")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/strategy_id.rs:161
//	test: test_get_external
func TestStrategyIDExternal(t *testing.T) {
	if got := ExternalStrategyID().String(); got != "EXTERNAL" {
		t.Fatalf("ExternalStrategyID() = %q, want %q", got, "EXTERNAL")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/strategy_id.rs:166
//	test: test_is_external
func TestStrategyIDIsExternal(t *testing.T) {
	if !ExternalStrategyID().IsExternal() {
		t.Fatal("ExternalStrategyID().IsExternal() = false, want true")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/strategy_id.rs:171
//	test: test_get_tag
func TestStrategyIDTag(t *testing.T) {
	if got := MustStrategyID("EMACross-001").Tag(); got != "001" {
		t.Fatalf("Tag() = %q, want %q", got, "001")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/strategy_id.rs:176
//	test: test_get_tag_external
func TestStrategyIDExternalTag(t *testing.T) {
	if got := ExternalStrategyID().Tag(); got != "EXTERNAL" {
		t.Fatalf("Tag() = %q, want %q", got, "EXTERNAL")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/strategy_id.rs:186
//	test: test_normalize_order_id_tag
func TestNormalizeOrderIDTag(t *testing.T) {
	tests := []struct {
		name  string
		input *string
		want  *string
	}{
		{name: "none"},
		{name: "empty", input: stringPointer(""), want: nil},
		{name: "None sentinel", input: stringPointer("None"), want: nil},
		{name: "numeric", input: stringPointer("001"), want: stringPointer("001")},
		{name: "alphabetic", input: stringPointer("ABC"), want: stringPointer("ABC")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizeOrderIDTag(tt.input)
			if tt.want == nil {
				if got != nil {
					t.Fatalf("NormalizeOrderIDTag() = %q, want nil", *got)
				}
				return
			}
			if got == nil || *got != *tt.want {
				t.Fatalf("NormalizeOrderIDTag() = %v, want %q", got, *tt.want)
			}
		})
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/strategy_id.rs:195
//	test: test_new_with_empty_name_panics
func TestStrategyIDEmptyNamePanics(t *testing.T) {
	requirePanicContains(t, "name part (before '-') cannot be empty", func() {
		MustStrategyID("-001")
	})
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/strategy_id.rs:201
//	test: test_new_with_empty_tag_panics
func TestStrategyIDEmptyTagPanics(t *testing.T) {
	requirePanicContains(t, "tag part (after '-') cannot be empty", func() {
		MustStrategyID("EMACross-")
	})
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/strategy_id.rs:206
//	test: test_new_checked_with_empty_name_returns_error
func TestParseStrategyIDEmptyNameReturnsError(t *testing.T) {
	if _, err := ParseStrategyID("-001"); err == nil {
		t.Fatal("expected error")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/strategy_id.rs:211
//	test: test_new_checked_with_empty_tag_returns_error
func TestParseStrategyIDEmptyTagReturnsError(t *testing.T) {
	if _, err := ParseStrategyID("EMACross-"); err == nil {
		t.Fatal("expected error")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/strategy_id.rs:216
//	test: test_new_checked_with_empty_name_returns_typed_error_with_stable_display
func TestParseStrategyIDEmptyNameReturnsTypedErrorWithStableDisplay(t *testing.T) {
	const want = "`value` name part (before '-') cannot be empty"
	_, err := ParseStrategyID("-001")
	if err == nil {
		t.Fatal("expected error")
	}
	if got := validationKind(err); got != "predicate_violation" {
		t.Fatalf("error kind = %q, want %q", got, "predicate_violation")
	}
	if got := err.Error(); got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/strategy_id.rs:233
//	test: test_new_checked_with_empty_tag_returns_typed_error_with_stable_display
func TestParseStrategyIDEmptyTagReturnsTypedErrorWithStableDisplay(t *testing.T) {
	const want = "`value` tag part (after '-') cannot be empty"
	_, err := ParseStrategyID("EMACross-")
	if err == nil {
		t.Fatal("expected error")
	}
	if got := validationKind(err); got != "predicate_violation" {
		t.Fatalf("error kind = %q, want %q", got, "predicate_violation")
	}
	if got := err.Error(); got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/strategy_id.rs:253
//	test: test_deserialize_inside_tagged_enum
func TestStrategyIDDeserializeInsideTaggedObject(t *testing.T) {
	var wrapper struct {
		Type string     `json:"type"`
		ID   StrategyID `json:"id"`
	}
	if err := json.Unmarshal([]byte(`{"type":"Strategy","id":"EMACross-001"}`), &wrapper); err != nil {
		t.Fatal(err)
	}
	if got := wrapper.ID.String(); got != "EMACross-001" {
		t.Fatalf("deserialized ID = %q, want %q", got, "EMACross-001")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/strategy_id.rs:266
//	test: test_deserialize_from_serde_json_value
func TestStrategyIDDeserializeFromJSONValue(t *testing.T) {
	var id StrategyID
	if err := json.Unmarshal([]byte(`"EMACross-001"`), &id); err != nil {
		t.Fatal(err)
	}
	if got := id.String(); got != "EMACross-001" {
		t.Fatalf("deserialized ID = %q, want %q", got, "EMACross-001")
	}
}

func stringPointer(value string) *string { return &value }
