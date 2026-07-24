package domain

import "testing"

func TestPositionMarginUsesExactNotionalRateAndLeverage(t *testing.T) {
	instrument := mustInstrument(t, "BTC-PERP", 1, 2, 3)
	usdc := mustCurrency(t, "USDC", 8)
	marginRate := mustRate(t, "1")
	leverage := mustRatio(t, "10")

	got, err := PositionMargin(
		mustPrice(t, "50000", instrument),
		mustQuantity(t, "0.21", instrument),
		marginRate,
		leverage,
		usdc,
	)
	if err != nil {
		t.Fatalf("PositionMargin: %v", err)
	}
	if got.Decimal().String() != "1050" {
		t.Fatalf("position margin = %s, want 1050 USDC", got)
	}
}

func TestFundingPaymentDebitsLongAndCreditsShortExactly(t *testing.T) {
	instrument := mustInstrument(t, "BTC-PERP", 1, 2, 3)
	usdc := mustCurrency(t, "USDC", 8)
	oracle := mustPrice(t, "1000", instrument)
	quantity := mustQuantity(t, "1", instrument)
	rate := mustRate(t, "0.01")

	long, err := FundingPayment(oracle, quantity, rate, true, usdc)
	if err != nil {
		t.Fatalf("long FundingPayment: %v", err)
	}
	short, err := FundingPayment(oracle, quantity, rate, false, usdc)
	if err != nil {
		t.Fatalf("short FundingPayment: %v", err)
	}
	if long.Decimal().String() != "-10" ||
		short.Decimal().String() != "10" {
		t.Fatalf("funding long/short = %s/%s, want -10/+10", long, short)
	}
}

func TestRiskMathRejectsNonPositiveLeverage(t *testing.T) {
	instrument := mustInstrument(t, "BTC-PERP", 1, 2, 3)
	usdc := mustCurrency(t, "USDC", 8)
	if _, err := PositionMargin(
		mustPrice(t, "100", instrument),
		mustQuantity(t, "1", instrument),
		mustRate(t, "1"),
		mustRatio(t, "0"),
		usdc,
	); err == nil {
		t.Fatal("zero leverage did not fail")
	}
}

func mustRate(t *testing.T, text string) Rate {
	t.Helper()
	value, err := NewRate(text)
	if err != nil {
		t.Fatalf("NewRate(%q): %v", text, err)
	}
	return value
}

func mustRatio(t *testing.T, text string) Ratio {
	t.Helper()
	value, err := NewRatio(text)
	if err != nil {
		t.Fatalf("NewRatio(%q): %v", text, err)
	}
	return value
}
