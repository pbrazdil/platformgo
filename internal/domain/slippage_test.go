package domain

import (
	"errors"
	"testing"
)

func TestAdverseSlippageBasisPointBoundary(t *testing.T) {
	instrument := mustInstrument(t, "BTC-PERP", 1, 2, 3)
	reference := mustPrice(t, "60000", instrument)

	tests := []struct {
		name      string
		candidate string
		buy       bool
		want      bool
	}{
		{name: "buy exact ceiling", candidate: "60300", buy: true, want: true},
		{name: "buy above ceiling", candidate: "60300.01", buy: true, want: false},
		{name: "buy favorable", candidate: "59000", buy: true, want: true},
		{name: "sell exact floor", candidate: "59700", buy: false, want: true},
		{name: "sell below floor", candidate: "59699.99", buy: false, want: false},
		{name: "sell favorable", candidate: "60500", buy: false, want: true},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			candidate := mustPrice(t, testCase.candidate, instrument)
			got, err := PriceWithinAdverseBasisPoints(
				reference,
				candidate,
				testCase.buy,
				50,
			)
			if err != nil {
				t.Fatalf("PriceWithinAdverseBasisPoints: %v", err)
			}
			if got != testCase.want {
				t.Fatalf("got %t, want %t", got, testCase.want)
			}
		})
	}
}

func TestAdverseSlippageRejectsInvalidReferenceAndUnits(t *testing.T) {
	instrument := mustInstrument(t, "BTC-PERP", 1, 2, 3)
	other := mustInstrument(t, "ETH-PERP", 1, 2, 3)
	if _, err := PriceWithinAdverseBasisPoints(
		mustPrice(t, "0", instrument),
		mustPrice(t, "1", instrument),
		true,
		50,
	); !errors.Is(err, ErrInvalidReferencePrice) {
		t.Fatalf("zero reference error = %v, want ErrInvalidReferencePrice", err)
	}
	if _, err := PriceWithinAdverseBasisPoints(
		mustPrice(t, "1", instrument),
		mustPrice(t, "1", other),
		true,
		50,
	); !errors.Is(err, ErrUnitMismatch) {
		t.Fatalf("unit mismatch error = %v, want ErrUnitMismatch", err)
	}
}
