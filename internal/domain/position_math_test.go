package domain

import "testing"

func TestRealizedPnLRoundsOnceInSettlementCurrency(t *testing.T) {
	instrument := mustInstrument(t, "BTC-PERP", 1, 3, 3)
	usdc := mustCurrency(t, "USDC", 2)

	tests := []struct {
		name     string
		entry    string
		exit     string
		quantity string
		long     bool
		want     string
	}{
		{name: "long gain", entry: "100", exit: "110", quantity: "2", long: true, want: "20"},
		{name: "short gain", entry: "100", exit: "90", quantity: "2", long: false, want: "20"},
		{name: "long loss", entry: "100", exit: "90", quantity: "2", long: true, want: "-20"},
		{name: "half even tie", entry: "0", exit: "1.005", quantity: "1", long: true, want: "1"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := RealizedPnL(
				mustPrice(t, testCase.entry, instrument),
				mustPrice(t, testCase.exit, instrument),
				mustQuantity(t, testCase.quantity, instrument),
				testCase.long,
				usdc,
			)
			if err != nil {
				t.Fatalf("RealizedPnL: %v", err)
			}
			if got.Decimal().String() != testCase.want ||
				!got.Currency().Equal(usdc) {
				t.Fatalf("realized PnL = %s, want %s USDC", got, testCase.want)
			}
		})
	}
}

func TestRealizedPnLRejectsMixedInstrumentRevisions(t *testing.T) {
	btc := mustInstrument(t, "BTC-PERP", 1, 2, 3)
	eth := mustInstrument(t, "ETH-PERP", 1, 2, 3)
	usdc := mustCurrency(t, "USDC", 8)

	if _, err := RealizedPnL(
		mustPrice(t, "100", btc),
		mustPrice(t, "100", eth),
		mustQuantity(t, "1", btc),
		true,
		usdc,
	); err == nil {
		t.Fatal("mixed instrument revisions did not fail")
	}
}
