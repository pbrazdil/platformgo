package order

import "testing"

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/mod.rs:1422
//	test: test_order_opposite_side
//
// Adaptations:
//   - Rust parameterization becomes Go table-driven subtests.
//
// Assertions preserved:
//   - BUY maps to SELL.
//   - SELL maps to BUY.
//   - NO_ORDER_SIDE maps to NO_ORDER_SIDE.
func TestOrderSideOpposite(t *testing.T) {
	tests := []struct {
		side OrderSide
		want OrderSide
	}{
		{OrderSideBuy, OrderSideSell},
		{OrderSideSell, OrderSideBuy},
		{OrderSideNoOrderSide, OrderSideNoOrderSide},
	}

	for _, tt := range tests {
		t.Run(tt.side.String(), func(t *testing.T) {
			if got := tt.side.Opposite(); got != tt.want {
				t.Fatalf("Opposite() = %v, want %v", got, tt.want)
			}
		})
	}
}
