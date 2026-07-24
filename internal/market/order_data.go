package market

import (
	"fmt"

	"github.com/upcomers-org/platformgo/internal/decimal"
)

// BookPrice pairs an exact price with the specified side whose ordering it
// follows in an order book.
type BookPrice struct {
	Value decimal.Price
	Side  OrderSide
}

func NewBookPrice(value decimal.Price, side OrderSide) BookPrice {
	if side == OrderSideNoOrderSide {
		panic("order side was NO_ORDER_SIDE")
	}
	return BookPrice{Value: value, Side: side}
}

func (o BookOrder) ToBookPrice() BookPrice {
	return NewBookPrice(o.Price, o.Side)
}

// Exposure returns the exact price-times-size value.
func (o BookOrder) Exposure() decimal.Decimal {
	return o.Price.Decimal().Mul(o.Size.Decimal())
}

// SignedSize returns positive size for buys and negative size for sells.
func (o BookOrder) SignedSize() decimal.Decimal {
	switch o.Side {
	case OrderSideBuy:
		return o.Size.Decimal()
	case OrderSideSell:
		return o.Size.Decimal().Neg()
	default:
		panic("order side was NO_ORDER_SIDE")
	}
}

func (o BookOrder) GoString() string {
	return fmt.Sprintf(
		"BookOrder(side=%s, price=%s, size=%s, order_id=%d)",
		o.Side,
		o.Price,
		o.Size,
		o.OrderID,
	)
}
