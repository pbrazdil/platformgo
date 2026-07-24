package engine

import (
	"testing"

	decimal "github.com/upcomers-org/platformgo/internal/decimal/economic"
)

func FuzzTradingSubmitReplayAndFillInvariant(f *testing.F) {
	f.Add("0.001", "100")
	f.Add("0.010", "1")
	f.Add("0", "100")
	f.Add("-0.001", "100")
	f.Add("0.0001", "100.001")

	f.Fuzz(func(t *testing.T, quantity, price string) {
		left := newTradingFixture(t)
		right := newTradingFixture(t)
		action := TradingAction{
			Kind: TradingActionSubmitOrder,
			SubmitOrder: &SubmitOrder{
				OrderID:      left.id(500),
				AccountID:    "account-1",
				InstrumentID: "BTC-PERP",
				Side:         SideBuy,
				Type:         OrderTypeLimit,
				TimeInForce:  TimeInForceGTC,
				Quantity:     quantity,
				Price:        price,
			},
		}

		leftDecision := left.applyDecision(t, action)
		rightDecision := right.applyDecision(t, action)
		if left.state.Hash() != right.state.Hash() ||
			!equalDecision(leftDecision, rightDecision) {
			t.Fatal("identical trading input did not replay deterministically")
		}

		order, exists := left.state.Order(action.SubmitOrder.OrderID)
		if !exists {
			return
		}
		sum := decimal.Decimal{}
		for _, fill := range left.state.FillsForOrder(order.OrderID) {
			fillQuantity, err := decimal.Parse(fill.Quantity)
			if err != nil {
				t.Fatalf("parse fill quantity %q: %v", fill.Quantity, err)
			}
			sum, err = sum.Add(fillQuantity)
			if err != nil {
				t.Fatalf("sum fill quantity: %v", err)
			}
		}
		if got := sum.String(); got != order.FilledQuantity {
			t.Fatalf("fill sum = %s, cumulative filled quantity = %s", got, order.FilledQuantity)
		}
	})
}
