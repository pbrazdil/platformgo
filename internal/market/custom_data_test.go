package market

import (
	"encoding/json"
	"reflect"
	"testing"
)

type testCustomPayload struct {
	TsInit       uint64       `json:"ts_init"`
	InstrumentID InstrumentID `json:"instrument_id"`
}

func testCustomPayloadDecoder(payload json.RawMessage) (CustomRegistryValue, error) {
	var value testCustomPayload
	if err := json.Unmarshal(payload, &value); err != nil {
		return CustomRegistryValue{}, err
	}
	return CustomRegistryValue{
		TypeName: "TestCustomData", TsInit: value.TsInit,
		InstrumentID: value.InstrumentID, Payload: value,
	}, nil
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/custom.rs:579
//	test: test_custom_data_json_roundtrip
//
// Adaptations:
//   - Process-global type registration replaced by an isolated registry.
func TestCustomDataJSONRoundTrip(t *testing.T) {
	registry := NewCustomDataRegistry()
	if err := registry.RegisterJSON("TestCustomData", testCustomPayloadDecoder); err != nil {
		t.Fatalf("register: %v", err)
	}
	identifier := "TEST.SIM"
	metadata := map[string]string{"key1": "value1", "key2": "value2"}
	inner := testCustomPayload{TsInit: 100, InstrumentID: "TEST.SIM"}
	encoded, err := EncodeRegisteredCustom("TestCustomData", metadata, &identifier, inner)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	roundTripped, err := registry.DeserializeJSON(encoded)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if roundTripped.TypeName != "TestCustomData" {
		t.Fatalf("type name = %q", roundTripped.TypeName)
	}
	if !reflect.DeepEqual(roundTripped.Metadata, metadata) {
		t.Fatalf("metadata = %#v, want %#v", roundTripped.Metadata, metadata)
	}
	if roundTripped.Identifier == nil || *roundTripped.Identifier != identifier {
		t.Fatalf("identifier = %v, want %q", roundTripped.Identifier, identifier)
	}
	if !reflect.DeepEqual(roundTripped.Payload, inner) {
		t.Fatalf("payload = %#v, want %#v", roundTripped.Payload, inner)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/custom.rs:623
//	test: test_custom_data_wrapper
func TestCustomDataWrapper(t *testing.T) {
	identifier := "TEST.SIM"
	record := NewCustomDataRecord(
		CustomDataType{TypeName: "TestCustomData", Identifier: &identifier},
		testCustomPayload{TsInit: 100, InstrumentID: "TEST.SIM"},
		100,
		"TEST.SIM",
	)
	if record.TsInit() != 100 {
		t.Fatalf("ts init = %d, want 100", record.TsInit())
	}
	if record.InstrumentID() != "TEST.SIM" {
		t.Fatalf("instrument ID = %s, want TEST.SIM", record.InstrumentID())
	}
}
