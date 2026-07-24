package ids

import (
	"errors"
	"testing"
)

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/instrument_id.rs:250
//	test: test_instrument_id_parse_success
func TestInstrumentIDParseSuccess(t *testing.T) {
	id := MustInstrumentID("ETHUSDT.BINANCE")
	if id.Symbol != "ETHUSDT" {
		t.Fatalf("Symbol = %q", id.Symbol)
	}
	if id.Venue != "BINANCE" {
		t.Fatalf("Venue = %q", id.Venue)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/instrument_id.rs:256
//	test: test_instrument_id_from_str_missing_separator_returns_typed_error
func TestInstrumentIDMissingSeparatorReturnsTypedError(t *testing.T) {
	_, err := ParseInstrumentID("ETHUSDT-BINANCE")
	var idErr *InstrumentIDError
	if !errors.As(err, &idErr) {
		t.Fatalf("error type = %T", err)
	}
	if idErr.Kind != "missing_separator" || idErr.Value != "ETHUSDT-BINANCE" {
		t.Fatalf("error = %#v", idErr)
	}
	const want = "invalid `InstrumentId` value 'ETHUSDT-BINANCE': missing '.' separator between symbol and venue components"
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err, want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/instrument_id.rs:273
//	test: test_instrument_id_from_panics_with_display_error
func TestInstrumentIDMustPanicsWithDisplayError(t *testing.T) {
	requirePanicContains(t, "missing '.' separator between symbol and venue components", func() {
		MustInstrumentID("ETHUSDT-BINANCE")
	})
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/instrument_id.rs:278
//	test: test_instrument_id_from_str_invalid_symbol_returns_typed_error
func TestInstrumentIDInvalidSymbolReturnsTypedError(t *testing.T) {
	_, err := ParseInstrumentID(".BINANCE")
	var idErr *InstrumentIDError
	if !errors.As(err, &idErr) || idErr.Kind != "invalid_symbol" || idErr.Value != ".BINANCE" {
		t.Fatalf("error = %#v", err)
	}
	var validationErr *ValidationError
	if !errors.As(err, &validationErr) || validationErr.Kind != "empty_string" {
		t.Fatalf("source error = %#v", errors.Unwrap(idErr))
	}
	const want = "invalid `InstrumentId` value '.BINANCE': invalid symbol: invalid string for 'value', was empty"
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err, want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/instrument_id.rs:297
//	test: test_instrument_id_from_str_invalid_venue_returns_typed_error
func TestInstrumentIDInvalidVenueReturnsTypedError(t *testing.T) {
	_, err := ParseInstrumentID("ETHUSDT.BINANCÉ")
	var idErr *InstrumentIDError
	if !errors.As(err, &idErr) || idErr.Kind != "invalid_venue" || idErr.Value != "ETHUSDT.BINANCÉ" {
		t.Fatalf("error = %#v", err)
	}
	var validationErr *ValidationError
	if !errors.As(err, &validationErr) || validationErr.Kind != "non_ascii_string" {
		t.Fatalf("source error = %#v", errors.Unwrap(idErr))
	}
	const want = "invalid `InstrumentId` value 'ETHUSDT.BINANCÉ': invalid venue: invalid string for 'value' contained a non-ASCII char, was 'BINANCÉ'"
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err, want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/instrument_id.rs:320
//	test: test_string_reprs
func TestInstrumentIDStringRepresentations(t *testing.T) {
	if got := MustInstrumentID("ETH/USDT.BINANCE").String(); got != "ETH/USDT.BINANCE" {
		t.Fatalf("String() = %q", got)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/instrument_id.rs:327
//	test: test_instrument_id_from_str_with_utf8_symbol
func TestInstrumentIDUTF8Symbol(t *testing.T) {
	id, err := ParseInstrumentID("TËST-PÉRP.BINANCE")
	if err != nil {
		t.Fatal(err)
	}
	if id.Symbol != "TËST-PÉRP" || id.Venue != "BINANCE" || id.String() != "TËST-PÉRP.BINANCE" {
		t.Fatalf("id = %#v (%q)", id, id)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/instrument_id.rs:339
//	test: test_blockchain_instrument_id_valid
func TestBlockchainInstrumentIDValid(t *testing.T) {
	id := MustInstrumentID("0xC31E54c7a869B9FcBEcc14363CF510d1c41fa443.Arbitrum:UniswapV3")
	if id.Symbol != "0xC31E54c7a869B9FcBEcc14363CF510d1c41fa443" {
		t.Fatalf("Symbol = %q", id.Symbol)
	}
	if id.Venue != "Arbitrum:UniswapV3" {
		t.Fatalf("Venue = %q", id.Venue)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/instrument_id.rs:354
//	test: test_blockchain_instrument_id_invalid_chain
func TestBlockchainInstrumentIDInvalidChain(t *testing.T) {
	requirePanicContains(t, "invalid venue: Error creating `Venue` from 'InvalidChain:UniswapV3'", func() {
		MustInstrumentID("0xC31E54c7a869B9FcBEcc14363CF510d1c41fa443.InvalidChain:UniswapV3")
	})
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/instrument_id.rs:362
//	test: test_blockchain_instrument_id_empty_dex
func TestBlockchainInstrumentIDEmptyDEX(t *testing.T) {
	requirePanicContains(t, "invalid venue: Error creating `Venue` from 'Arbitrum:'", func() {
		MustInstrumentID("0xC31E54c7a869B9FcBEcc14363CF510d1c41fa443.Arbitrum:")
	})
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/instrument_id.rs:368
//	test: test_regular_venue_with_blockchain_like_name_but_without_dex
func TestRegularVenueWithBlockchainLikeName(t *testing.T) {
	id := MustInstrumentID("0xC31E54c7a869B9FcBEcc14363CF510d1c41fa443.Ethereum")
	if id.Symbol != "0xC31E54c7a869B9FcBEcc14363CF510d1c41fa443" || id.Venue != "Ethereum" {
		t.Fatalf("id = %#v", id)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/instrument_id.rs:383
//	test: test_blockchain_instrument_id_invalid_address_no_prefix
func TestBlockchainInstrumentIDInvalidAddressNoPrefix(t *testing.T) {
	requirePanicContains(t, "invalid blockchain address: Ethereum address must start with '0x': invalidaddress", func() {
		MustInstrumentID("invalidaddress.Ethereum:UniswapV3")
	})
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/instrument_id.rs:392
//	test: test_blockchain_instrument_id_invalid_address_short
func TestBlockchainInstrumentIDInvalidAddressShort(t *testing.T) {
	requirePanicContains(t, "invalid blockchain address: Blockchain address '0x123' is incorrect", func() {
		MustInstrumentID("0x123.Ethereum:UniswapV3")
	})
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/instrument_id.rs:399
//	test: test_blockchain_instrument_id_invalid_address_non_hex
func TestBlockchainInstrumentIDInvalidAddressNonHex(t *testing.T) {
	requirePanicContains(t, "invalid character 'G' at position 39", func() {
		MustInstrumentID("0xC31E54c7a869B9FcBEcc14363CF510d1c41fa44G.Ethereum:UniswapV3")
	})
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/instrument_id.rs:406
//	test: test_blockchain_instrument_id_invalid_address_checksum
func TestBlockchainInstrumentIDInvalidAddressChecksum(t *testing.T) {
	requirePanicContains(t, "has incorrect checksum", func() {
		MustInstrumentID("0xc31e54c7a869b9fcbecc14363cf510d1c41fa443.Ethereum:UniswapV3")
	})
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/instrument_id.rs:412
//	test: test_blockchain_extraction_valid_dex
func TestBlockchainExtractionValidDEX(t *testing.T) {
	chain, ok := MustInstrumentID("0xC31E54c7a869B9FcBEcc14363CF510d1c41fa443.Arbitrum:UniswapV3").Blockchain()
	if !ok || chain != "Arbitrum" {
		t.Fatalf("Blockchain() = %q, %v", chain, ok)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/instrument_id.rs:422
//	test: test_blockchain_extraction_tradifi_venue
func TestBlockchainExtractionTradFiVenue(t *testing.T) {
	if chain, ok := MustInstrumentID("ETH/USDT.BINANCE").Blockchain(); ok {
		t.Fatalf("Blockchain() = %q, true; want none", chain)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/instrument_id.rs:446
//	test: test_parse_parent_components
func TestInstrumentIDParentComponents(t *testing.T) {
	tests := []struct {
		value string
		root  string
		class InstrumentClass
		ok    bool
	}{
		{"ES.FUT.XCME", "ES", InstrumentClassFuture, true},
		{"ES.FUTURE.XCME", "ES", InstrumentClassFuture, true},
		{"ES.OPT.XCME", "ES", InstrumentClassOption, true},
		{"ES.OPTION.XCME", "ES", InstrumentClassOption, true},
		{"CL.FUT.XNYM", "CL", InstrumentClassFuture, true},
		{"ECES.OPT.XCME", "ECES", InstrumentClassOption, true},
		{"ESZ4.XCME", "", "", false},
		{"AUDUSD.SIM", "", "", false},
		{"1.211334112-31570229.BETFAIR", "", "", false},
		{"ES.UNKNOWN.XCME", "", "", false},
		{"ES.FUT.OOPS.XCME", "", "", false},
		{"ES.fut.XCME", "", "", false},
		{"ES.opt.XCME", "", "", false},
		{".FUT.XCME", "", "", false},
		{".OPT.XCME", "", "", false},
	}
	for _, test := range tests {
		t.Run(test.value, func(t *testing.T) {
			root, class, ok := MustInstrumentID(test.value).ParentComponents()
			if root != test.root || class != test.class || ok != test.ok {
				t.Fatalf("ParentComponents() = %q, %q, %v; want %q, %q, %v",
					root, class, ok, test.root, test.class, test.ok)
			}
		})
	}
}
