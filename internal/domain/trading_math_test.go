package domain

import (
	"errors"
	"testing"
)

func TestWeightedAveragePriceUsesOneNamedRoundingBoundary(t *testing.T) {
	instrument := mustInstrument(t, "BTC-PERP", 1, 2, 3)
	average, err := WeightedAveragePrice([]PriceQuantity{
		{Price: mustPrice(t, "60000", instrument), Quantity: mustQuantity(t, "0.005", instrument)},
		{Price: mustPrice(t, "60160", instrument), Quantity: mustQuantity(t, "0.005", instrument)},
	})
	if err != nil {
		t.Fatalf("WeightedAveragePrice: %v", err)
	}
	if got, want := average.Decimal().String(), "60080"; got != want {
		t.Fatalf("weighted average = %s, want %s", got, want)
	}
}

func TestWeightedAveragePriceRejectsMissingAndMismatchedQuantity(t *testing.T) {
	instrument := mustInstrument(t, "BTC-PERP", 1, 2, 3)
	other := mustInstrument(t, "ETH-PERP", 1, 2, 3)

	if _, err := WeightedAveragePrice(nil); !errors.Is(err, ErrEmptyPriceQuantity) {
		t.Fatalf("empty error = %v, want ErrEmptyPriceQuantity", err)
	}
	if _, err := WeightedAveragePrice([]PriceQuantity{{
		Price:    mustPrice(t, "60000", instrument),
		Quantity: mustQuantity(t, "0", instrument),
	}}); !errors.Is(err, ErrEmptyPriceQuantity) {
		t.Fatalf("zero quantity error = %v, want ErrEmptyPriceQuantity", err)
	}
	if _, err := WeightedAveragePrice([]PriceQuantity{{
		Price:    mustPrice(t, "60000", instrument),
		Quantity: mustQuantity(t, "1", other),
	}}); !errors.Is(err, ErrUnitMismatch) {
		t.Fatalf("unit mismatch error = %v, want ErrUnitMismatch", err)
	}
}
