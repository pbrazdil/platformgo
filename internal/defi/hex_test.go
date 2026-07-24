package defi

import "testing"

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/hex.rs:148
//	test: test_from_str_hex_to_u64_valid
func TestFromStringHexToUint64Valid(t *testing.T) {
	tests := map[string]uint64{
		"0x0": 0, "0x1": 1, "0XfF": 255, "0xff": 255,
		"0xffffffffffffffff": ^uint64(0), "1234abcd": 0x1234abcd,
	}
	for input, want := range tests {
		if got, err := ParseHexU64(input); err != nil || got != want {
			t.Errorf("ParseHexU64(%q) = %d, %v; want %d", input, got, err, want)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/hex.rs:159
//	test: test_from_str_hex_to_u64_too_long
func TestFromStringHexToUint64TooLong(t *testing.T) {
	for _, input := range []string{"0x1ffffffffffffffff", "0x123456789abcdef123456789abcdef"} {
		if _, err := ParseHexU64(input); err == nil {
			t.Errorf("ParseHexU64(%q) succeeded", input)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/hex.rs:170
//	test: test_from_str_hex_to_u64_invalid_chars
func TestFromStringHexToUint64InvalidCharacters(t *testing.T) {
	for _, input := range []string{"0xzz", "0x123g"} {
		if _, err := ParseHexU64(input); err == nil {
			t.Errorf("ParseHexU64(%q) succeeded", input)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/hex.rs:176
//	test: test_deserialize_hex_timestamp
func TestDeserializeHexTimestamp(t *testing.T) {
	const seconds = uint64(0x64b5f3bb)
	got, err := HexTimestampNanos("0x64b5f3bb")
	if err != nil || got != seconds*1_000_000_000 {
		t.Fatalf("timestamp = %d, %v", got, err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/hex.rs:189
//	test: test_deserialize_opt_hex_u256_present
func TestDeserializeOptionalHexU256Present(t *testing.T) {
	value, err := ParseOptionalHexU256JSON([]byte(`"0x1a"`))
	if err != nil || value == nil || value.Uint64() != 26 {
		t.Fatalf("value = %v, %v", value, err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/hex.rs:196
//	test: test_deserialize_opt_hex_u256_null
func TestDeserializeOptionalHexU256Null(t *testing.T) {
	value, err := ParseOptionalHexU256JSON([]byte("null"))
	if err != nil || value != nil {
		t.Fatalf("value = %v, %v", value, err)
	}
}
