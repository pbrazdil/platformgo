package market

import (
	"fmt"
	"testing"

	"github.com/upcomers-org/platformgo/internal/decimal"
)

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/order.rs:161
//	test: test_new
func TestBookOrderNew(t *testing.T) {
	price := decimal.MustPrice("100.00")
	size := decimal.MustQuantity("10")
	side := OrderSideBuy
	orderID := uint64(123_456)

	order := NewBookOrder(side, price, size, orderID)

	if !order.Price.Equal(price) || !order.Size.Equal(size) ||
		order.Side != side || order.OrderID != orderID {
		t.Fatalf("order = %#v", order)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/order.rs:176
//	test: test_to_book_price
func TestBookOrderToBookPrice(t *testing.T) {
	price := decimal.MustPrice("100.00")
	size := decimal.MustQuantity("10")
	side := OrderSideBuy
	order := NewBookOrder(side, price, size, 123_456)

	bookPrice := order.ToBookPrice()

	if !bookPrice.Value.Equal(price) || bookPrice.Side != side {
		t.Fatalf("book price = %#v", bookPrice)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/order.rs:190
//	test: test_exposure
func TestBookOrderExposure(t *testing.T) {
	order := NewBookOrder(
		OrderSideBuy,
		decimal.MustPrice("100.00"),
		decimal.MustQuantity("10"),
		123_456,
	)

	if order.Exposure().Cmp(decimal.MustParse("1000.00")) != 0 {
		t.Fatalf("exposure = %s, want 1000.00", order.Exposure())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/order.rs:203
//	test: test_signed_size
func TestBookOrderSignedSize(t *testing.T) {
	price := decimal.MustPrice("100.00")
	size := decimal.MustQuantity("10")

	orderBuy := NewBookOrder(OrderSideBuy, price, size, 123_456)
	if orderBuy.SignedSize().Cmp(decimal.MustParse("10")) != 0 {
		t.Fatalf("buy signed size = %s, want 10", orderBuy.SignedSize())
	}

	orderSell := NewBookOrder(OrderSideSell, price, size, 123_456)
	if orderSell.SignedSize().Cmp(decimal.MustParse("-10")) != 0 {
		t.Fatalf("sell signed size = %s, want -10", orderSell.SignedSize())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/order.rs:218
//	test: test_debug
func TestBookOrderDebug(t *testing.T) {
	order := NewBookOrder(
		OrderSideBuy,
		decimal.MustPrice("100.00"),
		decimal.MustQuantity("10"),
		123_456,
	)

	const expected = "BookOrder(side=BUY, price=100.00, size=10, order_id=123456)"
	if result := fmt.Sprintf("%#v", order); result != expected {
		t.Fatalf("debug = %q, want %q", result, expected)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/order.rs:230
//	test: test_display
func TestBookOrderDisplay(t *testing.T) {
	order := NewBookOrder(
		OrderSideBuy,
		decimal.MustPrice("100.00"),
		decimal.MustQuantity("10"),
		123_456,
	)

	const expected = "BUY,100.00,10,123456"
	if result := fmt.Sprint(order); result != expected {
		t.Fatalf("display = %q, want %q", result, expected)
	}
}
