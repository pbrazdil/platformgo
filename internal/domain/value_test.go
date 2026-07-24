package domain

import (
	"errors"
	"testing"

	decimal "github.com/upcomers-org/platformgo/internal/decimal/economic"
)

func TestMoneyCarriesCurrencyAndRejectsImplicitRounding(t *testing.T) {
	usd := mustCurrency(t, "USD", 2)
	eur := mustCurrency(t, "EUR", 2)

	left, err := NewMoney("1.20", usd)
	if err != nil {
		t.Fatal(err)
	}
	right, err := NewMoney("2.30", usd)
	if err != nil {
		t.Fatal(err)
	}
	sum, err := left.Add(right)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Decimal().String() != "3.5" || !sum.Currency().Equal(usd) {
		t.Fatalf("sum = %s %s", sum.Decimal(), sum.Currency())
	}
	if left.Decimal().String() != "1.2" {
		t.Fatalf("left mutated to %s", left.Decimal())
	}

	otherCurrency, err := NewMoney("1", eur)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := left.Add(otherCurrency); !errors.Is(err, ErrUnitMismatch) {
		t.Fatalf("mixed-currency Add() error = %v, want ErrUnitMismatch", err)
	}
	if _, err := NewMoney("1.234", usd); !errors.Is(err, ErrUnitScale) {
		t.Fatalf("over-scale NewMoney() error = %v, want ErrUnitScale", err)
	}
	if _, err := NewMoney("1.230", usd); !errors.Is(err, ErrUnitScale) ||
		!errors.Is(err, decimal.ErrScale) {
		t.Fatalf("lexically over-scale NewMoney() error = %v, want ErrUnitScale and decimal.ErrScale", err)
	}
}

func TestPriceAndQuantityCarryInstrumentRevision(t *testing.T) {
	instrument := mustInstrument(t, "BTC-USD.HYPERLIQUID", 7, 2, 4)
	otherRevision := mustInstrument(t, "BTC-USD.HYPERLIQUID", 8, 2, 4)

	price, err := NewPrice("65000.25", instrument)
	if err != nil {
		t.Fatal(err)
	}
	if price.Decimal().String() != "65000.25" ||
		!price.Instrument().Equal(instrument) ||
		price.Scale() != 2 {
		t.Fatalf("price = %s instrument=%v scale=%d", price.Decimal(), price.Instrument(), price.Scale())
	}

	quantity, err := NewQuantity("0.125", instrument)
	if err != nil {
		t.Fatal(err)
	}
	if quantity.Decimal().String() != "0.125" ||
		!quantity.Instrument().Equal(instrument) ||
		quantity.Scale() != 4 {
		t.Fatalf(
			"quantity = %s instrument=%v scale=%d",
			quantity.Decimal(),
			quantity.Instrument(),
			quantity.Scale(),
		)
	}

	if _, err := price.Add(mustPrice(t, "1", otherRevision)); !errors.Is(err, ErrUnitMismatch) {
		t.Fatalf("mixed-revision Price.Add() error = %v, want ErrUnitMismatch", err)
	}
	if _, err := quantity.Sub(mustQuantity(t, "0.126", instrument)); !errors.Is(err, ErrNegativeQuantity) {
		t.Fatalf("negative Quantity.Sub() error = %v, want ErrNegativeQuantity", err)
	}
	if _, err := NewQuantity("-0.1", instrument); !errors.Is(err, ErrNegativeQuantity) {
		t.Fatalf("negative NewQuantity() error = %v, want ErrNegativeQuantity", err)
	}
}

func TestRateRatioAndBasisPointsAreDistinctExactTypes(t *testing.T) {
	rate, err := NewRate("0.0125")
	if err != nil {
		t.Fatal(err)
	}
	ratio, err := NewRatio("0.0125")
	if err != nil {
		t.Fatal(err)
	}
	if rate.Decimal().String() != ratio.Decimal().String() {
		t.Fatalf("rate = %s, ratio = %s", rate.Decimal(), ratio.Decimal())
	}

	converted, err := BasisPoints(125).Rate()
	if err != nil {
		t.Fatal(err)
	}
	if converted.Decimal().String() != "0.0125" {
		t.Fatalf("125 bps = %s, want 0.0125", converted.Decimal())
	}

	negative, err := BasisPoints(-25).Rate()
	if err != nil {
		t.Fatal(err)
	}
	if negative.Decimal().String() != "-0.0025" {
		t.Fatalf("-25 bps = %s, want -0.0025", negative.Decimal())
	}
}

func TestUnitConstructorsFailClosed(t *testing.T) {
	for _, code := range []string{"", "usd", "US D", "€"} {
		if _, err := NewCurrency(code, 2); !errors.Is(err, ErrInvalidCurrency) {
			t.Fatalf("NewCurrency(%q) error = %v, want ErrInvalidCurrency", code, err)
		}
	}
	if _, err := NewCurrency("USD", 19); !errors.Is(err, ErrUnitScale) {
		t.Fatalf("NewCurrency scale error = %v, want ErrUnitScale", err)
	}
	if _, err := NewInstrumentRevision("", 1, 2, 4); !errors.Is(err, ErrInvalidInstrument) {
		t.Fatalf("empty instrument error = %v, want ErrInvalidInstrument", err)
	}
	if _, err := NewInstrumentRevision("BTC-USD", 0, 2, 4); !errors.Is(err, ErrInvalidInstrument) {
		t.Fatalf("zero revision error = %v, want ErrInvalidInstrument", err)
	}
	for _, id := range []string{"BTC USD", "BTC\u00a0USD", "BTC\x00USD"} {
		if _, err := NewInstrumentRevision(id, 1, 2, 4); !errors.Is(err, ErrInvalidInstrument) {
			t.Fatalf("NewInstrumentRevision(%q) error = %v, want ErrInvalidInstrument", id, err)
		}
	}
}

func mustCurrency(t *testing.T, code string, scale uint8) Currency {
	t.Helper()
	value, err := NewCurrency(code, scale)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func mustInstrument(
	t *testing.T,
	id string,
	revision uint64,
	priceScale uint8,
	quantityScale uint8,
) InstrumentRevision {
	t.Helper()
	value, err := NewInstrumentRevision(id, revision, priceScale, quantityScale)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func mustPrice(t *testing.T, text string, instrument InstrumentRevision) Price {
	t.Helper()
	value, err := NewPrice(text, instrument)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func mustQuantity(
	t *testing.T,
	text string,
	instrument InstrumentRevision,
) Quantity {
	t.Helper()
	value, err := NewQuantity(text, instrument)
	if err != nil {
		t.Fatal(err)
	}
	return value
}
