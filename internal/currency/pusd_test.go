package currency

import "testing"

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/currencies.rs:1349
//	test: test_pusd_currency_invariants
//
// Adaptations:
//   - The global currency map is replaced by an isolated default registry.
func TestPUSDCurrencyInvariants(t *testing.T) {
	pusd := PUSD()
	if pusd.Code != "pUSD" {
		t.Fatalf("code = %q, want pUSD", pusd.Code)
	}
	if pusd.Precision != 6 {
		t.Fatalf("precision = %d, want 6", pusd.Precision)
	}
	if pusd.ISO4217 != 0 {
		t.Fatalf("ISO 4217 = %d, want 0", pusd.ISO4217)
	}
	if pusd.Type != Crypto {
		t.Fatalf("type = %v, want %v", pusd.Type, Crypto)
	}

	fromRegistry, err := NewDefaultRegistry().Lookup("pUSD")
	if err != nil {
		t.Fatalf("pUSD must be registered: %v", err)
	}
	if !fromRegistry.Equal(pusd) {
		t.Fatalf("registry pUSD = %#v, want %#v", fromRegistry, pusd)
	}
	if PUSD() != PUSD() {
		t.Fatal("PUSD accessor is not idempotent")
	}
}
