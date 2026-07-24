package orderbook

import (
	"fmt"
	"sort"

	"github.com/upcomers-org/platformgo/internal/decimal"
)

// OwnLevel is a FIFO level keyed by client order ID.
type OwnLevel struct {
	Price  BookPrice
	orders []OwnOrder
	index  map[string]int
}

func NewOwnLevel(price BookPrice) *OwnLevel {
	return &OwnLevel{Price: price, index: make(map[string]int)}
}

func (l *OwnLevel) Len() int    { return len(l.orders) }
func (l *OwnLevel) Empty() bool { return len(l.orders) == 0 }
func (l *OwnLevel) Orders() []OwnOrder {
	return append([]OwnOrder(nil), l.orders...)
}

func (l *OwnLevel) Add(order OwnOrder) {
	if !order.Price.Equal(l.Price.Value) || order.Side != l.Price.Side {
		panic("own order does not belong at level")
	}
	if position, ok := l.index[order.ClientID]; ok {
		l.orders[position] = order
		return
	}
	l.index[order.ClientID] = len(l.orders)
	l.orders = append(l.orders, order)
}

func (l *OwnLevel) Update(order OwnOrder) error {
	if _, ok := l.index[order.ClientID]; !ok {
		return fmt.Errorf("%w at level: client_order_id=%s, price=%v",
			ErrOrderNotFound, order.ClientID, l.Price)
	}
	l.Add(order)
	return nil
}

func (l *OwnLevel) Delete(clientID string) error {
	position, ok := l.index[clientID]
	if !ok {
		return fmt.Errorf("%w at level: client_order_id=%s, price=%v",
			ErrOrderNotFound, clientID, l.Price)
	}
	delete(l.index, clientID)
	l.orders = append(l.orders[:position], l.orders[position+1:]...)
	for i := position; i < len(l.orders); i++ {
		l.index[l.orders[i].ClientID] = i
	}
	return nil
}

func (l *OwnLevel) Size() decimal.Decimal {
	total := decimal.MustParse("0")
	for _, order := range l.orders {
		total = total.Add(order.Quantity.Decimal())
	}
	return total
}

func (l *OwnLevel) Exposure() decimal.Decimal {
	total := decimal.MustParse("0")
	for _, order := range l.orders {
		total = total.Add(order.Exposure())
	}
	return total
}

// OwnLadder indexes own orders by both client ID and price.
type OwnLadder struct {
	Side   Side
	levels map[string]*OwnLevel
	cache  map[string]string
}

func NewOwnLadder(side Side) *OwnLadder {
	return &OwnLadder{
		Side: side, levels: make(map[string]*OwnLevel), cache: make(map[string]string),
	}
}

func (l *OwnLadder) Len() int    { return len(l.levels) }
func (l *OwnLadder) Empty() bool { return len(l.levels) == 0 }

func (l *OwnLadder) Add(order OwnOrder) {
	if order.Side != l.Side {
		panic("own order side does not match ladder")
	}
	key := order.Price.Decimal().Normalize().String()
	level := l.levels[key]
	if level == nil {
		level = NewOwnLevel(order.ToBookPrice())
		l.levels[key] = level
	}
	level.Add(order)
	l.cache[order.ClientID] = key
}

func (l *OwnLadder) Update(order OwnOrder) error {
	key, ok := l.cache[order.ClientID]
	if !ok {
		return fmt.Errorf("%w in cache: client_order_id=%s", ErrOrderNotFound, order.ClientID)
	}
	level := l.levels[key]
	if level == nil {
		return fmt.Errorf("own book cached level missing: client_order_id=%s, price=%s",
			order.ClientID, key)
	}
	newKey := order.Price.Decimal().Normalize().String()
	if newKey != key {
		if err := level.Delete(order.ClientID); err != nil {
			return err
		}
		if level.Empty() {
			delete(l.levels, key)
		}
		l.Add(order)
		return nil
	}
	return level.Update(order)
}

func (l *OwnLadder) Delete(clientID string) error {
	key, ok := l.cache[clientID]
	if !ok {
		return fmt.Errorf("%w in cache: client_order_id=%s", ErrOrderNotFound, clientID)
	}
	level := l.levels[key]
	if level == nil {
		return fmt.Errorf("own book cached level missing: client_order_id=%s, price=%s", clientID, key)
	}
	if err := level.Delete(clientID); err != nil {
		return err
	}
	delete(l.cache, clientID)
	if level.Empty() {
		delete(l.levels, key)
	}
	return nil
}

func (l *OwnLadder) Clear() {
	clear(l.levels)
	clear(l.cache)
}

func (l *OwnLadder) ClearLevelsForTest() { clear(l.levels) }

func (l *OwnLadder) Sizes() decimal.Decimal {
	total := decimal.MustParse("0")
	for _, level := range l.levels {
		total = total.Add(level.Size())
	}
	return total
}

func (l *OwnLadder) Levels() []*OwnLevel {
	result := make([]*OwnLevel, 0, len(l.levels))
	for _, level := range l.levels {
		result = append(result, level)
	}
	sort.Slice(result, func(i, j int) bool {
		cmp := result[i].Price.Value.Cmp(result[j].Price.Value)
		if l.Side == Buy {
			return cmp > 0
		}
		return cmp < 0
	})
	return result
}
