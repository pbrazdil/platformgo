package orderbook

import (
	"fmt"
	"math/big"

	"github.com/upcomers-org/platformgo/internal/decimal"
)

var maxRawQuantity = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 128), big.NewInt(1))

// Level is a discrete price level. Its order slice preserves FIFO position
// when an existing order is updated.
type Level struct {
	Price  BookPrice
	orders []Order
	index  map[uint64]int
}

func NewLevel(price BookPrice) *Level {
	return &Level{Price: price, index: make(map[uint64]int)}
}

func LevelFromOrder(order Order) *Level {
	level := NewLevel(order.bookPrice())
	level.Add(order)
	return level
}

func (l *Level) Side() Side { return l.Price.Side }
func (l *Level) Len() int   { return len(l.orders) }
func (l *Level) Empty() bool {
	return len(l.orders) == 0
}

func (l *Level) First() (Order, bool) {
	if len(l.orders) == 0 {
		return Order{}, false
	}
	return l.orders[0], true
}

// Orders returns a copy in FIFO order.
func (l *Level) Orders() []Order {
	return append([]Order(nil), l.orders...)
}

func (l *Level) AddBulk(orders []Order) {
	for _, order := range orders {
		l.Add(order)
	}
}

func (l *Level) Add(order Order) {
	l.requirePrice(order)
	if !order.Quantity.IsPositive() {
		return
	}
	if position, exists := l.index[order.ID]; exists {
		l.orders[position] = order
		return
	}
	l.index[order.ID] = len(l.orders)
	l.orders = append(l.orders, order)
}

func (l *Level) Update(order Order) {
	l.requirePrice(order)
	if order.Quantity.IsZero() {
		l.remove(order.ID)
		return
	}
	l.Add(order)
}

func (l *Level) Delete(order Order) { l.remove(order.ID) }

func (l *Level) RemoveByID(id, sequence, eventTime uint64) {
	if !l.remove(id) {
		panic(fmt.Sprintf("Integrity error: order not found: order_id=%d, sequence=%d, ts_event=%d", id, sequence, eventTime))
	}
}

func (l *Level) Contains(id uint64) bool {
	_, ok := l.index[id]
	return ok
}

// Size returns the exact sum of order quantities.
func (l *Level) Size() decimal.Decimal {
	total := decimal.MustParse("0")
	for _, order := range l.orders {
		total = total.Add(order.Quantity.Decimal())
	}
	return total
}

// Exposure returns the exact sum of price multiplied by quantity.
func (l *Level) Exposure() decimal.Decimal {
	total := decimal.MustParse("0")
	for _, order := range l.orders {
		total = total.Add(order.Price.MulDecimal(order.Quantity.Decimal()))
	}
	return total
}

// SizeRaw returns size scaled to the model's fixed precision.
func (l *Level) SizeRaw() *big.Int {
	total := new(big.Int)
	for _, order := range l.orders {
		total.Add(total, order.Quantity.Raw())
	}
	return total
}

// ExposureRaw returns exposure scaled to the model's fixed precision,
// truncating sub-raw units, treating non-positive prices as zero, and
// saturating to the unsigned 128-bit maximum.
func (l *Level) ExposureRaw() *big.Int {
	total := new(big.Int)
	for _, order := range l.orders {
		if !order.Price.IsPositive() {
			continue
		}
		raw := fixedRaw(order.Price.MulDecimal(order.Quantity.Decimal()), true)
		total.Add(total, raw)
		if total.Cmp(maxRawQuantity) > 0 {
			return new(big.Int).Set(maxRawQuantity)
		}
	}
	return total
}

func (l *Level) requirePrice(order Order) {
	if !order.Price.Equal(l.Price.Value) {
		panic(fmt.Sprintf("order price %s does not match level price %s", order.Price, l.Price.Value))
	}
}

func (l *Level) remove(id uint64) bool {
	position, exists := l.index[id]
	if !exists {
		return false
	}
	delete(l.index, id)
	l.orders = append(l.orders[:position], l.orders[position+1:]...)
	for i := position; i < len(l.orders); i++ {
		l.index[l.orders[i].ID] = i
	}
	return true
}

func fixedRaw(value decimal.Decimal, saturate bool) *big.Int {
	value = value.Quantize(decimal.MaxPrecision, decimal.RoundTowardZero)
	text := value.String()
	raw, ok := new(big.Int).SetString(removeDecimalPoint(text), 10)
	if !ok {
		panic("invalid decimal representation: " + text)
	}
	if saturate && raw.Cmp(maxRawQuantity) > 0 {
		return new(big.Int).Set(maxRawQuantity)
	}
	return raw
}

func removeDecimalPoint(text string) string {
	result := make([]byte, 0, len(text))
	for i := range len(text) {
		if text[i] != '.' {
			result = append(result, text[i])
		}
	}
	return string(result)
}
