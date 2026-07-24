package instrument

import "testing"

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/any.rs:141
//	test: test_is_spread
func TestInstrumentAnyIsSpread(t *testing.T) {
	for _, test := range []struct {
		kind     InstrumentAnyKind
		expected bool
	}{
		{InstrumentAnyFuturesSpread, true}, {InstrumentAnyOptionSpread, true},
		{InstrumentAnyCryptoFuturesSpread, true}, {InstrumentAnyCryptoOptionSpread, true},
		{InstrumentAnyCryptoFuture, false}, {InstrumentAnyCryptoOption, false},
	} {
		if test.kind.IsSpread() != test.expected {
			t.Fatalf("%v spread mismatch", test.kind)
		}
	}
}
