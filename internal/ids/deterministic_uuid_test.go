package ids

import "testing"

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/stubs.rs:239
//	test: test_uuid_is_valid_v4_rfc4122
//
// Adaptations:
//   - The resettable process-global test RNG is replaced by an instance-owned sequence.
func TestDeterministicUUIDIsValidV4RFC4122(t *testing.T) {
	value := NewDeterministicUUIDSequence().Next()
	if len(value) != 36 {
		t.Fatalf("UUID length = %d, want 36: %q", len(value), value)
	}
	if value[14:15] != "4" {
		t.Fatalf("version digit = %q, want 4: %q", value[14:15], value)
	}
	switch value[19] {
	case '8', '9', 'a', 'b':
	default:
		t.Fatalf("variant nibble = %q, want one of 8/9/a/b: %q", value[19], value)
	}
}
