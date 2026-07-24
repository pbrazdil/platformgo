package defi

import "testing"

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/validation.rs:59
//	test: test_validate_address_invalid_prefix
func TestValidateAddressInvalidPrefix(t *testing.T) {
	const address = "742d35Cc6634C0532925a3b844Bc454e4438f44e"
	err := ValidateAddress(address)
	const want = "Ethereum address must start with '0x': " + address
	if err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/validation.rs:70
//	test: test_validate_invalid_address_format
func TestValidateInvalidAddressFormat(t *testing.T) {
	tests := map[string]string{
		"0x1233": "Blockchain address '0x1233' is incorrect: invalid string length",
		"0xZZZd35Cc6634C0532925a3b844Bc454e4438f44e": "Blockchain address '0xZZZd35Cc6634C0532925a3b844Bc454e4438f44e' is incorrect: invalid character 'Z' at position 0",
	}
	for address, want := range tests {
		err := ValidateAddress(address)
		if err == nil || err.Error() != want {
			t.Errorf("ValidateAddress(%q) = %v, want %q", address, err, want)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/validation.rs:89
//	test: test_validate_invalid_checksum
func TestValidateInvalidChecksum(t *testing.T) {
	const address = "0x742d35cc6634c0532925a3b844bc454e4438f44e"
	err := ValidateAddress(address)
	const want = "Blockchain address '" + address + "' has incorrect checksum"
	if err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}
}
