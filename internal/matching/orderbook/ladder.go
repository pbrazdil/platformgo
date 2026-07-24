package orderbook

import (
	"fmt"
	"strings"

	"github.com/upcomers-org/platformgo/internal/decimal"
)

type batchState uint8

const (
	noBatch batchState = iota
	mbpBatch
	snapshotBatch
)

// Ladder is one side of an order book. Levels remain in best-to-worst order.
type Ladder struct {
	Side     Side
	BookType BookType
	levels   []*Level
	cache    map[uint64]BookPrice
	batch    batchState
}

func NewLadder(side Side, bookType BookType) *Ladder {
	return &Ladder{Side: side, BookType: bookType, cache: make(map[uint64]BookPrice)}
}

func (l *Ladder) Len() int    { return len(l.levels) }
func (l *Ladder) Empty() bool { return len(l.levels) == 0 }
func (l *Ladder) CacheLen() int {
	return len(l.cache)
}

func (l *Ladder) Clear() {
	l.levels = nil
	clear(l.cache)
	l.batch = noBatch
}

func (l *Ladder) AddBulk(orders []Order) {
	for _, order := range orders {
		l.Add(order, 0)
	}
}

func (l *Ladder) Add(order Order, flags uint8) {
	if order.Side != l.Side {
		panic(fmt.Sprintf("order side %s does not match ladder side %s", order.Side, l.Side))
	}
	if l.BookType == L1MBP {
		if !l.handleL1Add(order, flags) {
			return
		}
	} else if !order.Quantity.IsPositive() {
		return
	}

	price := order.bookPrice()
	l.cache[order.ID] = price
	if level := l.find(price); level != nil {
		level.Add(order)
	} else {
		l.levels = append(l.levels, LevelFromOrder(order))
		l.sortLevels()
	}

	isBatch := flags&(FlagMBP|FlagSnapshot) != 0
	if l.BookType == L1MBP && isBatch {
		l.retainBestOnly()
		if flags&FlagLast != 0 {
			l.batch = noBatch
		}
	}
}

func (l *Ladder) Update(order Order, flags uint8) {
	if oldPrice, ok := l.cache[order.ID]; ok {
		if level := l.find(oldPrice); level != nil {
			if order.Price.Equal(level.Price.Value) {
				level.Update(order)
				if order.Quantity.IsZero() {
					delete(l.cache, order.ID)
				}
				if level.Empty() {
					l.removeLevel(oldPrice)
				}
				return
			}
			delete(l.cache, order.ID)
			level.Delete(order)
			if level.Empty() {
				l.removeLevel(oldPrice)
			}
		}
	}
	if order.Quantity.IsPositive() {
		l.Add(order, flags)
	}
}

func (l *Ladder) Delete(order Order, sequence, eventTime uint64) {
	l.RemoveOrder(order.ID, sequence, eventTime)
}

func (l *Ladder) RemoveOrder(id, sequence, eventTime uint64) {
	price, ok := l.cache[id]
	if !ok {
		return
	}
	level := l.find(price)
	if level == nil || !level.Contains(id) {
		return
	}
	delete(l.cache, id)
	level.RemoveByID(id, sequence, eventTime)
	if level.Empty() {
		l.removeLevel(price)
	}
}

func (l *Ladder) RemoveLevel(price BookPrice) (*Level, bool) {
	for i, level := range l.levels {
		if level.Price.Equal(price) {
			l.levels = append(l.levels[:i], l.levels[i+1:]...)
			for _, order := range level.orders {
				delete(l.cache, order.ID)
			}
			return level, true
		}
	}
	return nil, false
}

func (l *Ladder) Top() (*Level, bool) {
	if len(l.levels) == 0 {
		return nil, false
	}
	return l.levels[0], true
}

func (l *Ladder) Levels() []*Level {
	return append([]*Level(nil), l.levels...)
}

func (l *Ladder) Sizes() decimal.Decimal {
	total := decimal.MustParse("0")
	for _, level := range l.levels {
		total = total.Add(level.Size())
	}
	return total
}

func (l *Ladder) Exposures() decimal.Decimal {
	total := decimal.MustParse("0")
	for _, level := range l.levels {
		total = total.Add(level.Exposure())
	}
	return total
}

// SimulateFills walks price-time priority until the requested quantity is
// exhausted or the order's limit stops further matching.
func (l *Ladder) SimulateFills(order Order) []Fill {
	fills := make([]Fill, 0)
	filled, _ := decimal.ZeroQuantity(order.Quantity.Precision())
	for _, level := range l.levels {
		if (l.Side == Buy && level.Price.Value.Cmp(order.Price) < 0) ||
			(l.Side == Sell && level.Price.Value.Cmp(order.Price) > 0) {
			break
		}
		for _, resting := range level.orders {
			next, ok := filled.Add(resting.Quantity)
			if !ok {
				panic("fill quantity overflow")
			}
			if next.Cmp(order.Quantity) >= 0 {
				remainder, ok := order.Quantity.Sub(filled)
				if !ok {
					panic("negative fill remainder")
				}
				if remainder.IsPositive() {
					fills = append(fills, Fill{Price: resting.Price, Quantity: remainder})
				}
				return fills
			}
			fills = append(fills, Fill{Price: resting.Price, Quantity: resting.Quantity})
			filled = next
		}
	}
	return fills
}

func (l *Ladder) String() string {
	var result strings.Builder
	fmt.Fprintf(&result, "Ladder(side=%s)\n", l.Side)
	for _, level := range l.levels {
		fmt.Fprintf(&result, "  %s -> %d orders\n", level.Price.Value, level.Len())
	}
	return result.String()
}

func (l *Ladder) handleL1Add(order Order, flags uint8) bool {
	if !order.Quantity.IsPositive() {
		l.Clear()
		return false
	}
	isMBP := flags&FlagMBP != 0
	isSnapshot := flags&FlagSnapshot != 0
	isLast := flags&FlagLast != 0
	switch {
	case isSnapshot && isLast:
		if l.batch != snapshotBatch {
			l.Clear()
		}
	case isSnapshot:
		if l.batch != snapshotBatch {
			l.Clear()
			l.batch = snapshotBatch
		}
	case isMBP && isLast:
		if l.batch != mbpBatch {
			l.Clear()
		}
	case isMBP:
		l.Clear()
		l.batch = mbpBatch
	default:
		l.Clear()
	}
	return true
}

func (l *Ladder) retainBestOnly() {
	if len(l.levels) <= 1 {
		return
	}
	l.levels = l.levels[:1]
	clear(l.cache)
	for _, order := range l.levels[0].orders {
		l.cache[order.ID] = l.levels[0].Price
	}
}

func (l *Ladder) find(price BookPrice) *Level {
	for _, level := range l.levels {
		if level.Price.Equal(price) {
			return level
		}
	}
	return nil
}

func (l *Ladder) removeLevel(price BookPrice) {
	for i, level := range l.levels {
		if level.Price.Equal(price) {
			l.levels = append(l.levels[:i], l.levels[i+1:]...)
			return
		}
	}
}

func (l *Ladder) sortLevels() {
	for i := 1; i < len(l.levels); i++ {
		for j := i; j > 0 && l.levels[j].Price.Compare(l.levels[j-1].Price) < 0; j-- {
			l.levels[j], l.levels[j-1] = l.levels[j-1], l.levels[j]
		}
	}
}
