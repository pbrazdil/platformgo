package ids

import (
	"strings"
	"testing"
)

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/venue.rs:236
//	test: test_string_reprs
func TestVenueStringRepresentations(t *testing.T) {
	if got := MustVenue("BINANCE").String(); got != "BINANCE" {
		t.Fatalf("String() = %q, want BINANCE", got)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/venue.rs:242
//	test: test_new_checked_returns_typed_error_with_stable_display
func TestParseVenueReturnsTypedErrorWithStableDisplay(t *testing.T) {
	_, err := ParseVenue("")
	if err == nil {
		t.Fatal("expected error")
	}
	if got := validationKind(err); got != "empty_string" {
		t.Fatalf("error kind = %q, want empty_string", got)
	}
	const want = "invalid string for 'value', was empty"
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err, want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/venue.rs:272
//	test: test_validate_blockchain_venue_returns_typed_error_with_stable_display
func TestValidateBlockchainVenueReturnsTypedErrorWithStableDisplay(t *testing.T) {
	tests := []struct {
		value string
		want  string
	}{
		{"Arbitrum:", "invalid blockchain venue 'Arbitrum:': expected format 'Chain:DexId'"},
		{"InvalidChain:UniswapV3", "invalid blockchain venue 'InvalidChain:UniswapV3': chain 'InvalidChain' not recognized"},
		{"Arbitrum:InvalidDex", "invalid blockchain venue 'Arbitrum:InvalidDex': dex 'InvalidDex' not recognized"},
		{"no-colon", "invalid blockchain venue 'no-colon': expected format 'Chain:DexId'"},
	}
	for _, test := range tests {
		t.Run(test.value, func(t *testing.T) {
			err := ValidateBlockchainVenue(test.value)
			if err == nil {
				t.Fatal("expected error")
			}
			if got := validationKind(err); got != "predicate_violation" {
				t.Fatalf("error kind = %q, want predicate_violation", got)
			}
			if err.Error() != test.want {
				t.Fatalf("error = %q, want %q", err, test.want)
			}
		})
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/venue.rs:288
//	test: test_blockchain_venue_valid_dex_names
func TestBlockchainVenueValidDEXNames(t *testing.T) {
	validDEXes := []string{
		"UniswapV3", "UniswapV2", "UniswapV4", "SushiSwapV2", "SushiSwapV3",
		"PancakeSwapV3", "CamelotV3", "CurveFinance", "FluidDEX", "MaverickV1",
		"MaverickV2", "BaseX", "BaseSwapV2", "AerodromeV1",
		"AerodromeSlipstream", "BalancerV2", "BalancerV3",
	}
	for _, dex := range validDEXes {
		t.Run(dex, func(t *testing.T) {
			value := "Arbitrum:" + dex
			if got := MustVenue(value).String(); got != value {
				t.Fatalf("String() = %q, want %q", got, value)
			}
		})
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/venue.rs:321
//	test: test_blockchain_venue_invalid_chain
func TestBlockchainVenueInvalidChainPanics(t *testing.T) {
	requirePanicContains(t, "Error creating `Venue` from 'InvalidChain:UniswapV3': invalid blockchain venue 'InvalidChain:UniswapV3': chain 'InvalidChain' not recognized", func() {
		MustVenue("InvalidChain:UniswapV3")
	})
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/venue.rs:330
//	test: test_blockchain_venue_empty_dex
func TestBlockchainVenueEmptyDEXPanics(t *testing.T) {
	requirePanicContains(t, "Error creating `Venue` from 'Arbitrum:': invalid blockchain venue 'Arbitrum:': expected format 'Chain:DexId'", func() {
		MustVenue("Arbitrum:")
	})
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/venue.rs:336
//	test: test_regular_venue_with_blockchain_like_name_but_without_dex
func TestRegularVenueWithBlockchainLikeNameWithoutDEX(t *testing.T) {
	if got := MustVenue("Ethereum").String(); got != "Ethereum" {
		t.Fatalf("String() = %q, want Ethereum", got)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/venue.rs:347
//	test: test_blockchain_venue_invalid_dex
func TestBlockchainVenueInvalidDEXPanics(t *testing.T) {
	requirePanicContains(t, "Error creating `Venue` from 'Arbitrum:InvalidDex': invalid blockchain venue 'Arbitrum:InvalidDex': dex 'InvalidDex' not recognized", func() {
		MustVenue("Arbitrum:InvalidDex")
	})
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/venue.rs:356
//	test: test_blockchain_venue_dex_case_sensitive
func TestBlockchainVenueDEXCaseSensitive(t *testing.T) {
	requirePanicContains(t, "Error creating `Venue` from 'Arbitrum:uniswapv3': invalid blockchain venue 'Arbitrum:uniswapv3': dex 'uniswapv3' not recognized", func() {
		MustVenue("Arbitrum:uniswapv3")
	})
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/venue.rs:363
//	test: test_blockchain_venue_various_chain_dex_combinations
func TestBlockchainVenueVariousChainDEXCombinations(t *testing.T) {
	tests := []struct {
		chain string
		dex   string
	}{
		{"Ethereum", "UniswapV2"},
		{"Ethereum", "BalancerV2"},
		{"Arbitrum", "CamelotV3"},
		{"Base", "AerodromeV1"},
		{"Polygon", "SushiSwapV3"},
	}
	for _, test := range tests {
		value := test.chain + ":" + test.dex
		t.Run(value, func(t *testing.T) {
			if got := MustVenue(value).String(); got != value {
				t.Fatalf("String() = %q, want %q", got, value)
			}
		})
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/venue.rs:386
//	test: test_parse_dex_valid
func TestVenueParseDEXValid(t *testing.T) {
	tests := []struct {
		value string
		chain Blockchain
		dex   DEXType
	}{
		{"Ethereum:UniswapV3", "Ethereum", "UniswapV3"},
		{"Arbitrum:CamelotV3", "Arbitrum", "CamelotV3"},
		{"Base:AerodromeV1", "Base", "AerodromeV1"},
		{"Polygon:SushiSwapV2", "Polygon", "SushiSwapV2"},
	}
	for _, test := range tests {
		t.Run(test.value, func(t *testing.T) {
			chain, dex, err := MustVenue(test.value).ParseDEX()
			if err != nil {
				t.Fatal(err)
			}
			if chain != test.chain {
				t.Fatalf("chain = %q, want %q", chain, test.chain)
			}
			if dex != test.dex {
				t.Fatalf("DEX = %q, want %q", dex, test.dex)
			}
		})
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/venue.rs:400
//	test: test_parse_dex_non_dex_venue
func TestVenueParseDEXNonDEXVenue(t *testing.T) {
	_, _, err := MustVenue("BINANCE").ParseDEX()
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "is not a DEX venue") {
		t.Fatalf("error = %q", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/venue.rs:414
//	test: test_parse_dex_invalid_components
func TestVenueParseDEXInvalidComponents(t *testing.T) {
	if _, _, err := UncheckedVenue("InvalidChain:UniswapV3").ParseDEX(); err == nil {
		t.Fatal("invalid chain accepted")
	}
	if _, _, err := UncheckedVenue("Ethereum:InvalidDex").ParseDEX(); err == nil {
		t.Fatal("invalid DEX accepted")
	}
}
