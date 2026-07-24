package market

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/upcomers-org/platformgo/internal/decimal"
)

func dataString(value string) *string {
	return &value
}

func dataUint16(value uint16) *uint16 {
	return &value
}

func dataUnixNanos(value UnixNanos) *UnixNanos {
	return &value
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/mod.rs:923
//	test: test_funding_rate_update_does_not_convert_to_data_ffi
func TestFundingRateUpdateDoesNotConvertToDataFFI(t *testing.T) {
	fundingRate := NewFundingRateUpdate(
		"BTCUSDT-PERP.BINANCE",
		decimal.MustParse("0.0001"),
		dataUint16(480),
		dataUnixNanos(1_000_000_000),
		1,
		2,
	)
	_, err := DataFFIFromFundingRateUpdate(fundingRate)
	if err == nil {
		t.Fatal("expected FFI conversion error")
	}
	const want = "Cannot convert Data::FundingRateUpdate to DataFFI"
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err, want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/mod.rs:942
//	test: test_data_type_creation_with_metadata
func TestDataTypeCreationWithMetadata(t *testing.T) {
	metadata := Metadata{"key1": "value1", "key2": "value2"}
	dataType := NewDataType("ExampleType", metadata, nil)
	if dataType.TypeName() != "ExampleType" {
		t.Fatalf("type name = %q", dataType.TypeName())
	}
	if dataType.Topic() != "ExampleType.key1=value1.key2=value2" {
		t.Fatalf("topic = %q", dataType.Topic())
	}
	if !MetadataEqual(dataType.Metadata(), metadata) {
		t.Fatalf("metadata = %#v, want %#v", dataType.Metadata(), metadata)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/mod.rs:954
//	test: test_data_type_topic_identity_uses_canonical_metadata_order
func TestDataTypeTopicIdentityUsesCanonicalMetadataOrder(t *testing.T) {
	metadata1 := Metadata{"b": json.Number("2"), "a": json.Number("1")}
	metadata2 := Metadata{"a": json.Number("1"), "b": json.Number("2")}
	dataType1 := NewDataType("ExampleType", metadata1, nil)
	dataType2 := NewDataType("ExampleType", metadata2, nil)
	if dataType1.Topic() != "ExampleType.a=1.b=2" {
		t.Fatalf("topic = %q", dataType1.Topic())
	}
	if dataType1.Topic() != dataType2.Topic() || !dataType1.Equal(dataType2) ||
		dataType1.Hash() != dataType2.Hash() || dataType1.String() != dataType2.String() {
		t.Fatal("metadata insertion order changed topic identity")
	}
	if dataType1.MetadataString() != `{"a":1,"b":2}` ||
		dataType1.MetadataString() != dataType2.MetadataString() {
		t.Fatalf("canonical metadata differs: %q / %q", dataType1.MetadataString(), dataType2.MetadataString())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/mod.rs:981
//	test: test_data_type_creation_without_metadata
func TestDataTypeCreationWithoutMetadata(t *testing.T) {
	dataType := NewDataType("ExampleType", nil, nil)
	if dataType.TypeName() != "ExampleType" || dataType.Topic() != "ExampleType" ||
		dataType.Metadata() != nil {
		t.Fatalf("unexpected data type: %+v", dataType)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/mod.rs:990
//	test: test_data_type_equality
func TestDataTypeEquality(t *testing.T) {
	metadata1 := Metadata{"key1": "value1"}
	metadata2 := Metadata{"key1": "value1"}
	dataType1 := NewDataType("ExampleType", metadata1, nil)
	dataType2 := NewDataType("ExampleType", metadata2, nil)
	if !dataType1.Equal(dataType2) {
		t.Fatalf("%+v != %+v", dataType1, dataType2)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/mod.rs:1001
//	test: test_data_type_inequality
func TestDataTypeInequality(t *testing.T) {
	metadata1 := Metadata{"key1": "value1"}
	metadata2 := Metadata{"key2": "value2"}
	dataType1 := NewDataType("ExampleType", metadata1, nil)
	dataType2 := NewDataType("ExampleType", metadata2, nil)
	if dataType1.Equal(dataType2) {
		t.Fatalf("%+v == %+v", dataType1, dataType2)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/mod.rs:1012
//	test: test_data_type_ordering
func TestDataTypeOrdering(t *testing.T) {
	metadata1 := Metadata{"key1": "value1"}
	metadata2 := Metadata{"key2": "value2"}
	dataType1 := NewDataType("ExampleTypeA", metadata1, nil)
	dataType2 := NewDataType("ExampleTypeB", metadata2, nil)
	if dataType1.Compare(dataType2) >= 0 {
		t.Fatalf("%q is not less than %q", dataType1.Topic(), dataType2.Topic())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/mod.rs:1023
//	test: test_data_type_hash
func TestDataTypeHash(t *testing.T) {
	metadata := Metadata{"key1": "value1"}
	dataType1 := NewDataType("ExampleType", metadata, nil)
	dataType2 := NewDataType("ExampleType", metadata, nil)
	if dataType1.Hash() != dataType2.Hash() {
		t.Fatalf("hashes differ: %d != %d", dataType1.Hash(), dataType2.Hash())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/mod.rs:1041
//	test: test_data_type_display
func TestDataTypeDisplay(t *testing.T) {
	metadata := Metadata{"key1": "value1"}
	dataType := NewDataType("ExampleType", metadata, nil)
	if dataType.String() != "ExampleType.key1=value1" {
		t.Fatalf("display = %q", dataType.String())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/mod.rs:1049
//	test: test_data_type_debug
func TestDataTypeDebug(t *testing.T) {
	metadata := Metadata{"key1": "value1"}
	dataType := NewDataType("ExampleType", metadata, nil)
	const want = `DataType(type_name=ExampleType, metadata=Some({"key1":"value1"}), identifier=None)`
	if dataType.DebugString() != want {
		t.Fatalf("debug = %q, want %q", dataType.DebugString(), want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/mod.rs:1060
//	test: test_parse_instrument_id_from_metadata
func TestParseInstrumentIDFromMetadata(t *testing.T) {
	instrumentIDText := "MSFT.XNAS"
	metadata := Metadata{"instrument_id": instrumentIDText}
	dataType := NewDataType("InstrumentAny", metadata, nil)
	instrumentID, ok := dataType.InstrumentID()
	if !ok || instrumentID != InstrumentID(instrumentIDText) {
		t.Fatalf("instrument ID = %q, present=%v", instrumentID, ok)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/mod.rs:1074
//	test: test_parse_venue_from_metadata
func TestParseVenueFromMetadata(t *testing.T) {
	venueText := "BINANCE"
	metadata := Metadata{"venue": venueText}
	dataType := NewDataType("InstrumentAny", metadata, nil)
	venue, ok := dataType.Venue()
	if !ok || venue.String() != venueText {
		t.Fatalf("venue = %q, present=%v", venue, ok)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/mod.rs:1083
//	test: test_parse_start_from_metadata
func TestParseStartFromMetadata(t *testing.T) {
	const startNS uint64 = 1_600_054_595_844_758_000
	metadata := Metadata{"start": "1600054595844758000"}
	dataType := NewDataType("TradeTick", metadata, nil)
	start, ok := dataType.Start()
	if !ok || start != UnixNanos(startNS) {
		t.Fatalf("start = %d, present=%v", start, ok)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/mod.rs:1092
//	test: test_parse_end_from_metadata
func TestParseEndFromMetadata(t *testing.T) {
	const endNS uint64 = 1_720_954_595_844_758_000
	metadata := Metadata{"end": "1720954595844758000"}
	dataType := NewDataType("TradeTick", metadata, nil)
	end, ok := dataType.End()
	if !ok || end != UnixNanos(endNS) {
		t.Fatalf("end = %d, present=%v", end, ok)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/mod.rs:1101
//	test: test_parse_limit_from_metadata
func TestParseLimitFromMetadata(t *testing.T) {
	const limit = 1000
	metadata := Metadata{"limit": limit}
	dataType := NewDataType("TradeTick", metadata, nil)
	got, ok := dataType.Limit()
	if !ok || got != limit {
		t.Fatalf("limit = %d, present=%v", got, ok)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/mod.rs:1110
//	test: test_data_type_metadata_accessors_return_none_without_metadata
func TestDataTypeMetadataAccessorsReturnNoneWithoutMetadata(t *testing.T) {
	dataType := NewDataType("TradeTick", nil, nil)
	if _, ok := dataType.InstrumentID(); ok {
		t.Fatal("unexpected instrument ID")
	}
	if _, ok := dataType.Venue(); ok {
		t.Fatal("unexpected venue")
	}
	if _, ok := dataType.Start(); ok {
		t.Fatal("unexpected start")
	}
	if _, ok := dataType.End(); ok {
		t.Fatal("unexpected end")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/mod.rs:1120
//	test: test_data_type_persistence_json_with_identifier
func TestDataTypePersistenceJSONWithIdentifier(t *testing.T) {
	dataType := NewDataType("MyCustomType", nil, dataString("venue//symbol"))
	persistenceJSON, err := dataType.PersistenceJSON()
	if err != nil {
		t.Fatalf("persistence JSON: %v", err)
	}
	if strings.Contains(persistenceJSON, "topic") ||
		!strings.Contains(persistenceJSON, `"identifier":"venue//symbol"`) {
		t.Fatalf("unexpected persistence JSON: %s", persistenceJSON)
	}
	restored, err := DataTypeFromPersistenceJSON(persistenceJSON)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	identifier, ok := restored.Identifier()
	if restored.TypeName() != "MyCustomType" || !ok || identifier != "venue//symbol" ||
		restored.Topic() != "MyCustomType" {
		t.Fatalf("unexpected restored data type: %+v", restored)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/mod.rs:1132
//	test: test_data_type_from_persistence_json_rebuilds_canonical_topic
func TestDataTypeFromPersistenceJSONRebuildsCanonicalTopic(t *testing.T) {
	persistenceJSON := `{
		"type_name": "ExampleType",
		"topic": "ExampleType.z=9.a=1",
		"metadata": {"z": 9, "a": 1}
	}`
	restored, err := DataTypeFromPersistenceJSON(persistenceJSON)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if restored.Topic() != "ExampleType.a=1.z=9" {
		t.Fatalf("topic = %q", restored.Topic())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/mod.rs:1145
//	test: test_data_type_identifier_getter
func TestDataTypeIdentifierGetter(t *testing.T) {
	dataType := NewDataType("T", nil, dataString("id"))
	identifier, ok := dataType.Identifier()
	if !ok || identifier != "id" {
		t.Fatalf("identifier = %q, present=%v", identifier, ok)
	}
	dataTypeWithoutID := NewDataType("T", nil, nil)
	if _, ok := dataTypeWithoutID.Identifier(); ok {
		t.Fatal("unexpected identifier")
	}
}
