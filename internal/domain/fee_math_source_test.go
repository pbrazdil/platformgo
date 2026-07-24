package domain

import "testing"

// TestSourceTradingFeeRoundsAtCurrencyBoundary ports:
//   - source: crates/model/src/accounts/cash.rs
//   - test: test_calculate_commission_for_taker_fx
//   - pinned revision: 116c9b5159ebeb6b578b737d72298cac8d723723
//
// The source vector is 1,500,000 * 0.8005 * 0.00002 = 24.015 USD,
// rounded half-even once at the USD currency boundary to 24.02 USD.
func TestSourceTradingFeeRoundsAtCurrencyBoundary(t *testing.T) {
	instrument, err := NewInstrumentRevision("AUD-USD", 1, 4, 0)
	if err != nil {
		t.Fatalf("NewInstrumentRevision: %v", err)
	}
	price, err := NewPrice("0.8005", instrument)
	if err != nil {
		t.Fatalf("NewPrice: %v", err)
	}
	quantity, err := NewQuantity("1500000", instrument)
	if err != nil {
		t.Fatalf("NewQuantity: %v", err)
	}
	rate, err := NewRate("0.00002")
	if err != nil {
		t.Fatalf("NewRate: %v", err)
	}
	usd, err := NewCurrency("USD", 2)
	if err != nil {
		t.Fatalf("NewCurrency: %v", err)
	}
	fee, err := TradingFee(price, quantity, rate, usd)
	if err != nil {
		t.Fatalf("TradingFee: %v", err)
	}
	if got := fee.Decimal().String(); got != "24.02" {
		t.Fatalf("fee = %s, want 24.02", got)
	}
}

func TestTradingFeeHalfEvenTable(t *testing.T) {
	instrument, err := NewInstrumentRevision("TEST-USD", 1, 2, 0)
	if err != nil {
		t.Fatalf("NewInstrumentRevision: %v", err)
	}
	currency, err := NewCurrency("USD", 2)
	if err != nil {
		t.Fatalf("NewCurrency: %v", err)
	}
	rate, err := NewRate("0.1")
	if err != nil {
		t.Fatalf("NewRate: %v", err)
	}
	quantity, err := NewQuantity("1", instrument)
	if err != nil {
		t.Fatalf("NewQuantity: %v", err)
	}
	for _, test := range []struct {
		price string
		want  string
	}{
		{price: "10.05", want: "1"},
		{price: "10.15", want: "1.02"},
	} {
		price, priceErr := NewPrice(test.price, instrument)
		if priceErr != nil {
			t.Fatalf("NewPrice(%s): %v", test.price, priceErr)
		}
		fee, feeErr := TradingFee(price, quantity, rate, currency)
		if feeErr != nil {
			t.Fatalf("TradingFee(%s): %v", test.price, feeErr)
		}
		if got := fee.Decimal().String(); got != test.want {
			t.Fatalf("TradingFee(%s) = %s, want %s", test.price, got, test.want)
		}
	}
}

func TestTradingFeeSupportsExactMakerRebate(t *testing.T) {
	instrument, err := NewInstrumentRevision("BTC-USD", 1, 2, 0)
	if err != nil {
		t.Fatalf("NewInstrumentRevision: %v", err)
	}
	price, _ := NewPrice("100", instrument)
	quantity, _ := NewQuantity("1", instrument)
	rate, _ := NewRate("-0.001")
	currency, _ := NewCurrency("USD", 2)
	fee, err := TradingFee(price, quantity, rate, currency)
	if err != nil {
		t.Fatalf("TradingFee: %v", err)
	}
	if got := fee.Decimal().String(); got != "-0.1" {
		t.Fatalf("maker rebate = %s, want -0.1", got)
	}
}
