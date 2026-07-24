package defi

import (
	"encoding/json"
	"testing"
)

const (
	testPoolAddress = "0xC02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2"
	testPoolID      = "0xc9bc8043294146424a4e4607d8ad837d6a659142822bbaaabc83bb57e7447461"
)

func testPoolIDBytes() [32]byte {
	return [32]byte{
		0xc9, 0xbc, 0x80, 0x43, 0x29, 0x41, 0x46, 0x42, 0x4a, 0x4e, 0x46, 0x07, 0xd8, 0xad,
		0x83, 0x7d, 0x6a, 0x65, 0x91, 0x42, 0x82, 0x2b, 0xba, 0xaa, 0xbc, 0x83, 0xbb, 0x57,
		0xe7, 0x44, 0x74, 0x61,
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_identifier.rs:335
//	test: test_valid_pool_identifiers
func TestValidPoolIdentifiers(t *testing.T) {
	for _, input := range []string{
		testPoolAddress, "0xc02aaa39b223fe8d0a0e5c4f27ead9083c756cc2", testPoolID,
	} {
		if _, err := ParsePoolIdentifier(input); err != nil {
			t.Errorf("valid identifier %q rejected: %v", input, err)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_identifier.rs:345
//	test: test_invalid_pool_identifiers
func TestInvalidPoolIdentifiers(t *testing.T) {
	for _, input := range []string{
		"C02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2",
		"0xC02aaA39",
		"0xC02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2EXTRA",
		"0xGGGGGGGGb223FE8D0A0e5C4F27eAD9083C756Cc2",
	} {
		if _, err := ParsePoolIdentifier(input); err == nil {
			t.Errorf("invalid identifier %q accepted", input)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_identifier.rs:351
//	test: test_case_insensitive_equality
func TestPoolIdentifierCaseInsensitiveEquality(t *testing.T) {
	first := MustPoolIdentifier(testPoolAddress)
	second := MustPoolIdentifier("0xc02aaa39b223fe8d0a0e5c4f27ead9083c756cc2")
	third := MustPoolIdentifier("0xC02AAA39B223FE8D0A0E5C4F27EAD9083C756CC2")
	if first != second || second != third || first != third {
		t.Fatalf("identifiers differ: %v %v %v", first, second, third)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_identifier.rs:362
//	test: test_case_insensitive_hashing
func TestPoolIdentifierCaseInsensitiveHashing(t *testing.T) {
	first := MustPoolIdentifier(testPoolAddress)
	second := MustPoolIdentifier("0xc02aaa39b223fe8d0a0e5c4f27ead9083c756cc2")
	values := map[PoolIdentifier]string{first: "value1"}
	if values[second] != "value1" {
		t.Fatalf("case-insensitive lookup = %q", values[second])
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_identifier.rs:376
//	test: test_display_preserves_case
func TestPoolIdentifierDisplayPreservesCase(t *testing.T) {
	if got := MustPoolIdentifier(testPoolAddress).String(); got != testPoolAddress {
		t.Fatalf("display = %q", got)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_identifier.rs:385
//	test: test_variant_detection
func TestPoolIdentifierVariantDetection(t *testing.T) {
	address, poolID := MustPoolIdentifier(testPoolAddress), MustPoolIdentifier(testPoolID)
	if !address.IsAddress() || address.IsPoolID() || !poolID.IsPoolID() || poolID.IsAddress() {
		t.Fatalf("variants = address:%#v pool:%#v", address, poolID)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_identifier.rs:399
//	test: test_different_variants_not_equal
func TestPoolIdentifierDifferentVariantsNotEqual(t *testing.T) {
	if MustPoolIdentifier(testPoolAddress) == MustPoolIdentifier(testPoolID) {
		t.Fatal("different variants compare equal")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_identifier.rs:409
//	test: test_serialization_roundtrip
func TestPoolIdentifierSerializationRoundTrip(t *testing.T) {
	original := MustPoolIdentifier(testPoolAddress)
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var decoded PoolIdentifier
	if err := json.Unmarshal(data, &decoded); err != nil || decoded != original {
		t.Fatalf("decoded = %v, %v", decoded, err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_identifier.rs:419
//	test: test_deserialize_from_owned_value
func TestPoolIdentifierDeserializeFromOwnedValue(t *testing.T) {
	data, _ := json.Marshal(testPoolAddress)
	var decoded PoolIdentifier
	if err := json.Unmarshal(data, &decoded); err != nil || decoded != MustPoolIdentifier(testPoolAddress) {
		t.Fatalf("decoded = %v, %v", decoded, err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_identifier.rs:431
//	test: test_from_address
func TestPoolIdentifierFromAddress(t *testing.T) {
	identifier, err := PoolIdentifierFromAddress(testPoolAddress)
	if err != nil || !identifier.IsAddress() || identifier.String() != testPoolAddress {
		t.Fatalf("identifier = %v, %v", identifier, err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_identifier.rs:443
//	test: test_from_pool_id_bytes
func TestPoolIdentifierFromPoolIDBytes(t *testing.T) {
	bytes := testPoolIDBytes()
	identifier, err := PoolIdentifierFromBytes(bytes[:])
	if err != nil || !identifier.IsPoolID() || identifier.String() != testPoolID {
		t.Fatalf("identifier = %v, %v", identifier, err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_identifier.rs:460
//	test: test_to_address
func TestPoolIdentifierToAddress(t *testing.T) {
	address, err := MustPoolIdentifier(testPoolAddress).Address()
	if err != nil || address != testPoolAddress {
		t.Fatalf("address = %q, %v", address, err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_identifier.rs:471
//	test: test_to_address_fails_for_pool_id
func TestPoolIdentifierToAddressFailsForPoolID(t *testing.T) {
	if _, err := MustPoolIdentifier(testPoolID).Address(); err == nil {
		t.Fatal("pool ID converted to address")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_identifier.rs:481
//	test: test_to_pool_id_bytes
func TestPoolIdentifierToPoolIDBytes(t *testing.T) {
	bytes, err := MustPoolIdentifier(testPoolID).Bytes()
	if err != nil || len(bytes) != 32 || bytes[0] != 0xc9 || bytes[31] != 0x61 {
		t.Fatalf("bytes = %x, %v", bytes, err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_identifier.rs:493
//	test: test_to_pool_id_bytes_fails_for_address
func TestPoolIdentifierToPoolIDBytesFailsForAddress(t *testing.T) {
	if _, err := MustPoolIdentifier(testPoolAddress).Bytes(); err == nil {
		t.Fatal("address converted to pool bytes")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_identifier.rs:501
//	test: test_conversion_roundtrip_address
func TestPoolIdentifierConversionRoundTripAddress(t *testing.T) {
	identifier, err := PoolIdentifierFromAddress(testPoolAddress)
	if err != nil {
		t.Fatal(err)
	}
	address, err := identifier.Address()
	if err != nil || address != testPoolAddress {
		t.Fatalf("address = %q, %v", address, err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_identifier.rs:511
//	test: test_conversion_roundtrip_pool_id
func TestPoolIdentifierConversionRoundTripPoolID(t *testing.T) {
	original := testPoolIDBytes()
	identifier, err := PoolIdentifierFromBytes(original[:])
	if err != nil {
		t.Fatal(err)
	}
	converted, err := identifier.Bytes()
	if err != nil || converted != original {
		t.Fatalf("bytes = %x, %v", converted, err)
	}
}
