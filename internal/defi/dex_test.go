package defi

import "testing"

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/dex.rs:311
//	test: test_dex_type_from_dex_name_valid
func TestDexTypeFromDexNameValid(t *testing.T) {
	for _, name := range []string{"UniswapV3", "SushiSwapV2", "BalancerV2", "CamelotV3"} {
		if _, ok := ParseDexType(name); !ok {
			t.Errorf("DEX %q not found", name)
		}
	}
	if got, ok := ParseDexType("UniswapV3"); !ok || got != UniswapV3 {
		t.Fatalf("UniswapV3 = %q, %v", got, ok)
	}
	if got, ok := ParseDexType("AerodromeSlipstream"); !ok || got != AerodromeSlipstream {
		t.Fatalf("AerodromeSlipstream = %q, %v", got, ok)
	}
	if got, ok := ParseDexType("FluidDEX"); !ok || got != FluidDEX {
		t.Fatalf("FluidDEX = %q, %v", got, ok)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/dex.rs:332
//	test: test_dex_type_from_dex_name_invalid
func TestDexTypeFromDexNameInvalid(t *testing.T) {
	for _, name := range []string{"InvalidDEX", "", "NonExistentDEX"} {
		if _, ok := ParseDexType(name); ok {
			t.Errorf("invalid DEX %q resolved", name)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/dex.rs:340
//	test: test_dex_type_from_dex_name_case_sensitive
func TestDexTypeFromDexNameCaseSensitive(t *testing.T) {
	for _, name := range []string{"UniswapV3", "SushiSwapV2"} {
		if _, ok := ParseDexType(name); !ok {
			t.Errorf("canonical DEX %q missing", name)
		}
	}
	for _, name := range []string{"uniswapv3", "UNISWAPV3", "UniSwapV3", "sushiswapv2"} {
		if _, ok := ParseDexType(name); ok {
			t.Errorf("non-canonical DEX %q resolved", name)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/dex.rs:352
//	test: test_dex_type_all_variants_mappable
func TestDexTypeAllVariantsMappable(t *testing.T) {
	if len(allDexTypes) != 17 {
		t.Fatalf("variant count = %d, want 17", len(allDexTypes))
	}
	for _, dexType := range allDexTypes {
		if parsed, ok := ParseDexType(dexType.String()); !ok || parsed != dexType {
			t.Errorf("%q maps to %q, %v", dexType, parsed, ok)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/dex.rs:383
//	test: test_dex_type_display
func TestDexTypeDisplay(t *testing.T) {
	tests := map[DexType]string{
		UniswapV3: "UniswapV3", SushiSwapV2: "SushiSwapV2",
		AerodromeSlipstream: "AerodromeSlipstream", FluidDEX: "FluidDEX",
	}
	for dexType, want := range tests {
		if got := dexType.String(); got != want {
			t.Errorf("%v = %q, want %q", dexType, got, want)
		}
	}
}
