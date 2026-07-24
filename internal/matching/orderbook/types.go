// Package orderbook provides deterministic in-memory price levels and
// one-sided order-book ladders.
package orderbook

import (
	"fmt"

	"github.com/upcomers-org/platformgo/internal/decimal"
)

// Side is the specified side of an order or ladder.
type Side uint8

const (
	NoSide Side = iota
	Buy
	Sell
)

func (s Side) String() string {
	switch s {
	case NoSide:
		return "NO_ORDER_SIDE"
	case Buy:
		return "BUY"
	case Sell:
		return "SELL"
	default:
		return fmt.Sprintf("Side(%d)", s)
	}
}

// BookType controls whether a ladder represents a single top-of-book value or
// an order-by-order book.
type BookType uint8

const (
	L1MBP BookType = iota + 1
	L2MBP
	L3MBO
)

func (b BookType) String() string {
	switch b {
	case L1MBP:
		return "L1_MBP"
	case L2MBP:
		return "L2_MBP"
	case L3MBO:
		return "L3_MBO"
	default:
		return fmt.Sprintf("BookType(%d)", b)
	}
}

// Record flags used by market-data batches.
const (
	FlagLast     uint8 = 1 << 7
	FlagTOB      uint8 = 1 << 6
	FlagSnapshot uint8 = 1 << 5
	FlagMBP      uint8 = 1 << 4
)

// Order is one resting order in a book.
type Order struct {
	Side     Side
	Price    decimal.Price
	Quantity decimal.Quantity
	ID       uint64
}

// NewOrder constructs an order.
func NewOrder(side Side, price decimal.Price, quantity decimal.Quantity, id uint64) Order {
	return Order{Side: side, Price: price, Quantity: quantity, ID: id}
}

// BookPrice is a price whose ordering depends on its side.
type BookPrice struct {
	Value decimal.Price
	Side  Side
}

// NewBookPrice constructs a side-aware price.
func NewBookPrice(value decimal.Price, side Side) BookPrice {
	return BookPrice{Value: value, Side: side}
}

// Compare returns -1, 0, or 1 in ladder order. Buy prices sort high-to-low;
// sell prices sort low-to-high. Comparing across sides is invalid.
func (p BookPrice) Compare(other BookPrice) int {
	if p.Side != other.Side {
		panic(fmt.Sprintf("BookPrice compared across sides: %s vs %s", p.Side, other.Side))
	}
	comparison := p.Value.Cmp(other.Value)
	if p.Side == Buy {
		return -comparison
	}
	return comparison
}

func (p BookPrice) Equal(other BookPrice) bool {
	return p.Side == other.Side && p.Value.Equal(other.Value)
}

func (o Order) bookPrice() BookPrice {
	return NewBookPrice(o.Price, o.Side)
}

// Fill is one simulated execution against a resting order.
type Fill struct {
	Price    decimal.Price
	Quantity decimal.Quantity
}
