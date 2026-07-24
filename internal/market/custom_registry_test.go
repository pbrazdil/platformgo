package market

import (
	"encoding/json"
	"strings"
	"testing"
)

type registryTestData struct {
	TsInit uint64 `json:"ts_init"`
}

func registryTestDecoder(strict bool) CustomJSONDeserializer {
	return func(payload json.RawMessage) (CustomRegistryValue, error) {
		var value registryTestData
		if err := DecodeCustomJSON(payload, &value, strict); err != nil {
			return CustomRegistryValue{}, err
		}
		return CustomRegistryValue{TypeName: "TestRegCustomData", TsInit: value.TsInit}, nil
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/registry.rs:509
//	test: json_registry_roundtrip
//
// Adaptations:
//   - Process-global registry replaced by an isolated registry owned by the test.
func TestCustomJSONRegistryRoundTrip(t *testing.T) {
	registry := NewCustomDataRegistry()
	if err := registry.RegisterJSON("TestRegCustomData", registryTestDecoder(false)); err != nil {
		t.Fatalf("register: %v", err)
	}
	encoded, err := EncodeCustomEnvelope("TestRegCustomData", registryTestData{TsInit: 100})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	back, err := registry.DeserializeJSON(encoded)
	if err != nil {
		t.Fatalf("deserialize: %v", err)
	}
	if back.TypeName != "TestRegCustomData" || back.TsInit != 100 {
		t.Fatalf("round trip = %#v", back)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/registry.rs:529
//	test: json_registry_roundtrip_with_deny_unknown_fields
//
// Adaptations:
//   - Process-global registry replaced by an isolated registry owned by the test.
func TestCustomJSONRegistryRoundTripWithDenyUnknownFields(t *testing.T) {
	registry := NewCustomDataRegistry()
	if err := registry.RegisterJSON("StrictRegCustomData", registryTestDecoder(true)); err != nil {
		t.Fatalf("register: %v", err)
	}
	encoded, err := EncodeCustomEnvelope("StrictRegCustomData", registryTestData{TsInit: 200})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	back, err := registry.DeserializeJSON(encoded)
	if err != nil {
		t.Fatalf("deserialize: %v", err)
	}
	if back.TypeName != "TestRegCustomData" || back.TsInit != 200 {
		t.Fatalf("round trip = %#v", back)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/registry.rs:549
//	test: ensure_json_deserializer_registered_is_idempotent
//
// Adaptations:
//   - Process-global registry replaced by an isolated registry owned by the test.
func TestEnsureCustomJSONDeserializerRegisteredIsIdempotent(t *testing.T) {
	registry := NewCustomDataRegistry()
	if err := registry.EnsureJSON("IdempotentTestJson", registryTestDecoder(false)); err != nil {
		t.Fatalf("first registration: %v", err)
	}
	if err := registry.EnsureJSON("IdempotentTestJson", registryTestDecoder(false)); err != nil {
		t.Fatalf("second registration: %v", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/registry.rs:568
//	test: register_json_deserializer_fails_on_duplicate
//
// Adaptations:
//   - Process-global registry replaced by an isolated registry owned by the test.
func TestRegisterCustomJSONDeserializerFailsOnDuplicate(t *testing.T) {
	registry := NewCustomDataRegistry()
	if err := registry.RegisterJSON("StrictDuplicateTestJson", registryTestDecoder(false)); err != nil {
		t.Fatalf("first registration: %v", err)
	}
	err := registry.RegisterJSON("StrictDuplicateTestJson", registryTestDecoder(false))
	if err == nil || !strings.Contains(err.Error(), "already registered") {
		t.Fatalf("duplicate error = %v", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/registry.rs:590
//	test: ensure_arrow_registered_is_idempotent
//
// Adaptations:
//   - Arrow callbacks are represented by typed Go callbacks without importing Arrow.
//   - Process-global registry replaced by an isolated registry owned by the test.
func TestEnsureCustomArrowRegisteredIsIdempotent(t *testing.T) {
	registry := NewCustomDataRegistry()
	first := CustomArrowRegistration{Schema: "empty"}
	second := CustomArrowRegistration{Schema: "empty"}
	if err := registry.EnsureArrow("IdempotentTestArrow", first); err != nil {
		t.Fatalf("first registration: %v", err)
	}
	if err := registry.EnsureArrow("IdempotentTestArrow", second); err != nil {
		t.Fatalf("second registration: %v", err)
	}
}
