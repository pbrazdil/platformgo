package orderbook

import (
	"errors"
	"testing"

	"github.com/upcomers-org/platformgo/internal/decimal"
)

func portPrice(text string) decimal.Price       { return decimal.MustPrice(text) }
func portQuantity(text string) decimal.Quantity { return decimal.MustQuantity(text) }

func requireBookPortPrice(t *testing.T, got decimal.Price, ok bool, want string) {
	t.Helper()
	if !ok || !got.Equal(portPrice(want)) {
		t.Fatalf("price = %s, %v; want %s, true", got, ok, want)
	}
}

func requireBookPortQuantity(t *testing.T, got decimal.Quantity, ok bool, want string) {
	t.Helper()
	if !ok || !got.Equal(portQuantity(want)) {
		t.Fatalf("quantity = %s, %v; want %s, true", got, ok, want)
	}
}

func requireBookPortPriceResult(t *testing.T, result func() (decimal.Price, bool), want string) {
	t.Helper()
	got, ok := result()
	requireBookPortPrice(t, got, ok, want)
}

func requireBookPortQuantityResult(t *testing.T, result func() (decimal.Quantity, bool), want string) {
	t.Helper()
	got, ok := result()
	requireBookPortQuantity(t, got, ok, want)
}

func requireBookPortDecimal(t *testing.T, got decimal.Decimal, want string) {
	t.Helper()
	if !got.Equal(decimal.MustParse(want)) {
		t.Fatalf("decimal = %s; want %s", got, want)
	}
}

func executionBook() *Book {
	book := NewBook("ETHUSDT-PERP.BINANCE", L2MBP)
	for _, order := range []Order{
		NewOrder(Sell, portPrice("2.011"), portQuantity("3.0"), 1),
		NewOrder(Sell, portPrice("2.010"), portQuantity("2.0"), 2),
		NewOrder(Sell, portPrice("2.000"), portQuantity("1.0"), 3),
		NewOrder(Buy, portPrice("1.000"), portQuantity("1.0"), 4),
		NewOrder(Buy, portPrice("0.990"), portQuantity("2.0"), 5),
		NewOrder(Buy, portPrice("0.989"), portQuantity("3.0"), 6),
	} {
		book.Add(order, 0, uint64(order.ID), uint64(order.ID))
	}
	return book
}

func stubDepth10() DepthSnapshot {
	depth := DepthSnapshot{InstrumentID: "AAPL.XNAS", Sequence: 0, EventTime: 1}
	for i := range 10 {
		depth.Bids = append(depth.Bids, NewOrder(
			Buy, portPrice([]string{"99.00", "98.00", "97.00", "96.00", "95.00", "94.00", "93.00", "92.00", "91.00", "90.00"}[i]),
			portQuantity([]string{"100.0", "200.0", "300.0", "400.0", "500.0", "600.0", "700.0", "800.0", "900.0", "1000.0"}[i]), uint64(i+1)))
		depth.Asks = append(depth.Asks, NewOrder(
			Sell, portPrice([]string{"100.00", "101.00", "102.00", "103.00", "104.00", "105.00", "106.00", "107.00", "108.00", "109.00"}[i]),
			portQuantity([]string{"100.0", "200.0", "300.0", "400.0", "500.0", "600.0", "700.0", "800.0", "900.0", "1000.0"}[i]), uint64(i+11)))
	}
	return depth
}

func addLevels(book *Book, bids, asks []string) {
	id := uint64(1)
	for _, price := range bids {
		book.Add(NewOrder(Buy, portPrice(price), portQuantity("10"), id), 0, id, id)
		id++
	}
	for _, price := range asks {
		book.Add(NewOrder(Sell, portPrice(price), portQuantity("10"), id), 0, id, id)
		id++
	}
}

func ownOrder(id string, side Side, price, size string, status OrderStatus) OwnOrder {
	order := NewOwnOrder(id, side, portPrice(price), portQuantity(size), status)
	order.TraderID = "TRADER-001"
	return order
}

func requireErrorIs(t *testing.T, err, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("error = %v; want %v", err, target)
	}
}
