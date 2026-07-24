package orderbook

import (
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strings"

	"github.com/upcomers-org/platformgo/internal/decimal"
)

var (
	ErrOrdersCrossed      = errors.New("order book is crossed")
	ErrInstrumentMismatch = errors.New("instrument mismatch")
	ErrOrderNotFound      = errors.New("order not found")
)

// Book is a two-sided, exact-decimal order book. Sequence metadata is supplied
// by the market-data record; UpdateCount counts successfully applied records.
type Book struct {
	InstrumentID string
	BookType     BookType
	Bids         *Ladder
	Asks         *Ladder
	Sequence     uint64
	LastTime     uint64
	UpdateCount  uint64
}

type PriceLevel struct {
	Price decimal.Price
	Size  decimal.Quantity
}

func NewBook(instrumentID string, bookType BookType) *Book {
	return &Book{
		InstrumentID: instrumentID,
		BookType:     bookType,
		Bids:         NewLadder(Buy, bookType),
		Asks:         NewLadder(Sell, bookType),
	}
}

func (b *Book) String() string {
	return fmt.Sprintf("OrderBook(instrument_id=%s, book_type=%s, update_count=%d)",
		b.InstrumentID, b.BookType, b.UpdateCount)
}

func (b *Book) HasBid() bool { return !b.Bids.Empty() }
func (b *Book) HasAsk() bool { return !b.Asks.Empty() }

func (b *Book) ladder(side Side) *Ladder {
	if side == Buy {
		return b.Bids
	}
	if side == Sell {
		return b.Asks
	}
	panic(fmt.Sprintf("invalid order side %d", side))
}

func (b *Book) Add(order Order, flags uint8, sequence, eventTime uint64) {
	b.ladder(order.Side).Add(order, flags)
	b.advance(sequence, eventTime)
}

func (b *Book) Update(order Order, flags uint8, sequence, eventTime uint64) {
	b.ladder(order.Side).Update(order, flags)
	b.advance(sequence, eventTime)
}

func (b *Book) Delete(order Order, sequence, eventTime uint64) {
	b.ladder(order.Side).Delete(order, sequence, eventTime)
	b.advance(sequence, eventTime)
}

func (b *Book) advance(sequence, eventTime uint64) {
	b.Sequence = sequence
	b.LastTime = eventTime
	b.UpdateCount++
}

func (b *Book) Clear(sequence, eventTime uint64) {
	b.Bids.Clear()
	b.Asks.Clear()
	b.advance(sequence, eventTime)
}

func (b *Book) ClearSide(side Side, sequence, eventTime uint64) {
	b.ladder(side).Clear()
	b.advance(sequence, eventTime)
}

func (b *Book) Reset() {
	b.Bids.Clear()
	b.Asks.Clear()
	b.Sequence = 0
	b.LastTime = 0
	b.UpdateCount = 0
}

func (b *Book) BestBidPrice() (decimal.Price, bool)   { return bestPrice(b.Bids) }
func (b *Book) BestAskPrice() (decimal.Price, bool)   { return bestPrice(b.Asks) }
func (b *Book) BestBidSize() (decimal.Quantity, bool) { return bestSize(b.Bids) }
func (b *Book) BestAskSize() (decimal.Quantity, bool) { return bestSize(b.Asks) }

func bestPrice(l *Ladder) (decimal.Price, bool) {
	level, ok := l.Top()
	if !ok {
		return decimal.Price{}, false
	}
	return level.Price.Value, true
}

func bestSize(l *Ladder) (decimal.Quantity, bool) {
	level, ok := l.Top()
	if !ok {
		return decimal.Quantity{}, false
	}
	q, err := decimal.ParseQuantity(level.Size().String())
	if err != nil {
		panic(err)
	}
	return q, true
}

func (b *Book) Spread() (decimal.Decimal, bool) {
	bid, bidOK := b.BestBidPrice()
	ask, askOK := b.BestAskPrice()
	if !bidOK || !askOK {
		return decimal.Decimal{}, false
	}
	return ask.Decimal().Sub(bid.Decimal()), true
}

func (b *Book) Midpoint() (decimal.Decimal, bool) {
	bid, bidOK := b.BestBidPrice()
	ask, askOK := b.BestAskPrice()
	if !bidOK || !askOK {
		return decimal.Decimal{}, false
	}
	mid, err := bid.Decimal().Add(ask.Decimal()).Quo(
		decimal.MustParse("2"), max(bid.Precision(), ask.Precision())+1, decimal.RoundHalfEven)
	if err != nil {
		panic(err)
	}
	return mid, true
}

// Integrity permits a locked market but rejects a crossed one.
func (b *Book) Integrity() error {
	bid, bidOK := b.BestBidPrice()
	ask, askOK := b.BestAskPrice()
	if bidOK && askOK && bid.Cmp(ask) > 0 {
		return ErrOrdersCrossed
	}
	return nil
}

// QuantityAtLevel returns liquidity at the exact price on the side consumed by
// an incoming order (Buy consumes asks; Sell consumes bids).
func (b *Book) QuantityAtLevel(price decimal.Price, incomingSide Side) decimal.Decimal {
	level := b.opposite(incomingSide).find(NewBookPrice(price, opposite(incomingSide)))
	if level == nil {
		return decimal.MustParse("0")
	}
	return level.Size()
}

func (b *Book) OrdersAtLevel(price decimal.Price, incomingSide Side) []Order {
	level := b.opposite(incomingSide).find(NewBookPrice(price, opposite(incomingSide)))
	if level == nil {
		return nil
	}
	return level.Orders()
}

func (b *Book) opposite(incoming Side) *Ladder { return b.ladder(opposite(incoming)) }

func opposite(side Side) Side {
	if side == Buy {
		return Sell
	}
	if side == Sell {
		return Buy
	}
	panic(fmt.Sprintf("invalid order side %d", side))
}

// QuantityForPrice returns cumulative executable quantity through limit.
func (b *Book) QuantityForPrice(price decimal.Price, incomingSide Side) decimal.Decimal {
	total := decimal.MustParse("0")
	for _, level := range b.opposite(incomingSide).levels {
		if incomingSide == Buy && level.Price.Value.Cmp(price) > 0 {
			break
		}
		if incomingSide == Sell && level.Price.Value.Cmp(price) < 0 {
			break
		}
		total = total.Add(level.Size())
	}
	return total
}

// AveragePriceForQuantity returns a quantity-weighted execution price. The
// second result is false when no market exists; partial depth is averaged.
func (b *Book) AveragePriceForQuantity(quantity decimal.Quantity, incomingSide Side) (decimal.Decimal, bool) {
	remaining := quantity.Decimal()
	exposure := decimal.MustParse("0")
	filled := decimal.MustParse("0")
	for _, level := range b.opposite(incomingSide).levels {
		if remaining.IsZero() {
			break
		}
		take := level.Size()
		if take.Cmp(remaining) > 0 {
			take = remaining
		}
		exposure = exposure.Add(level.Price.Value.Decimal().Mul(take))
		filled = filled.Add(take)
		remaining = remaining.Sub(take)
	}
	if filled.IsZero() {
		return decimal.MustParse("0"), false
	}
	value, err := exposure.Quo(filled, decimal.MaxPrecision, decimal.RoundHalfEven)
	if err != nil {
		panic(err)
	}
	return value.Normalize(), true
}

func (b *Book) WorstPriceForQuantity(quantity decimal.Quantity, incomingSide Side) (decimal.Price, bool) {
	remaining := quantity.Decimal()
	var result decimal.Price
	found := false
	for _, level := range b.opposite(incomingSide).levels {
		result, found = level.Price.Value, true
		remaining = remaining.Sub(level.Size())
		if remaining.Sign() <= 0 {
			break
		}
	}
	return result, found
}

// PriceForExposure returns average price, filled quantity and exposure.
func (b *Book) PriceForExposure(target decimal.Decimal, incomingSide Side) (decimal.Decimal, decimal.Decimal, decimal.Decimal) {
	exposure := decimal.MustParse("0")
	quantity := decimal.MustParse("0")
	lastPrice := decimal.MustParse("0")
	for _, level := range b.opposite(incomingSide).levels {
		lastPrice = level.Price.Value.Decimal()
		levelExposure := level.Exposure()
		if exposure.Add(levelExposure).Cmp(target) <= 0 {
			exposure = exposure.Add(levelExposure)
			quantity = quantity.Add(level.Size())
			continue
		}
		remaining := target.Sub(exposure)
		take, err := remaining.Quo(level.Price.Value.Decimal(), decimal.MaxPrecision, decimal.RoundHalfEven)
		if err != nil {
			panic(err)
		}
		exposure = target
		quantity = quantity.Add(take)
		break
	}
	if quantity.IsZero() {
		zero := decimal.MustParse("0")
		return zero, zero, zero
	}
	average, _ := exposure.Quo(quantity, decimal.MaxPrecision, decimal.RoundHalfEven)
	return average.Normalize(), quantity.Normalize(), lastPrice.Normalize()
}

func (b *Book) BidsMap(depth int) map[string]decimal.Decimal { return ladderMap(b.Bids, depth) }
func (b *Book) AsksMap(depth int) map[string]decimal.Decimal { return ladderMap(b.Asks, depth) }

func ladderMap(ladder *Ladder, depth int) map[string]decimal.Decimal {
	result := make(map[string]decimal.Decimal)
	for i, level := range ladder.levels {
		if depth > 0 && i >= depth {
			break
		}
		result[level.Price.Value.Decimal().Normalize().String()] = level.Size()
	}
	return result
}

// Group aggregates the best depth levels into side-aware price buckets.
func (b *Book) Group(side Side, increment decimal.Decimal, depth int) map[string]decimal.Decimal {
	if increment.Sign() <= 0 {
		panic("group increment must be positive")
	}
	result := make(map[string]decimal.Decimal)
	for i, level := range b.ladder(side).levels {
		if depth > 0 && i >= depth {
			break
		}
		ratio, err := level.Price.Value.Decimal().Quo(increment, 0,
			map[bool]decimal.RoundingMode{true: decimal.RoundTowardZero, false: decimal.RoundHalfEven}[side == Buy])
		if err != nil {
			panic(err)
		}
		bucket := ratio.Mul(increment)
		if side == Sell && bucket.Cmp(level.Price.Value.Decimal()) < 0 {
			bucket = bucket.Add(increment)
		}
		key := bucket.Normalize().String()
		result[key] = result[key].Add(level.Size())
	}
	return result
}

func (b *Book) LevelsForPrice(price decimal.Price, incomingSide Side) []*Level {
	levels := b.opposite(incomingSide).levels
	result := make([]*Level, 0, len(levels))
	for _, level := range levels {
		if incomingSide == Buy && level.Price.Value.Cmp(price) > 0 {
			break
		}
		if incomingSide == Sell && level.Price.Value.Cmp(price) < 0 {
			break
		}
		result = append(result, level)
	}
	return result
}

// PriceLevelsForPrice returns crossed levels as aggregate price/quantity
// pairs. It preserves fixed-point raw size and rejects a level sum that would
// overflow the model's unsigned 128-bit quantity representation.
func (b *Book) PriceLevelsForPrice(price decimal.Price, incomingSide Side, precision uint8) []PriceLevel {
	levels := b.LevelsForPrice(price, incomingSide)
	result := make([]PriceLevel, 0, len(levels))
	for _, level := range levels {
		raw := level.SizeRaw()
		if raw.Cmp(decimal.QuantityRawMax()) > 0 {
			panic("Overflow occurred when summing `BookLevel` raw size")
		}
		size, err := decimal.QuantityFromRawChecked(new(big.Int).Set(raw), precision)
		if err != nil {
			panic(err)
		}
		result = append(result, PriceLevel{Price: level.Price.Value, Size: size})
	}
	return result
}

func (b *Book) BidsRangeDownTo(price decimal.Price) []*Level {
	return b.LevelsForPrice(price, Sell)
}

func (b *Book) AsksRangeUpTo(price decimal.Price) []*Level {
	return b.LevelsForPrice(price, Buy)
}

// ClearStaleLevels removes crossed levels, optionally on one side only. With
// both sides selected, asks at/below the best bid and bids at/above the first
// surviving ask are removed.
func (b *Book) ClearStaleLevels(side *Side) []*Level {
	if b.BookType == L1MBP {
		return nil
	}
	bid, bidOK := b.BestBidPrice()
	ask, askOK := b.BestAskPrice()
	if !bidOK || !askOK || bid.Cmp(ask) < 0 {
		return nil
	}
	var removed []*Level
	clearBoth := side == nil || *side == NoSide
	if clearBoth || *side == Buy {
		removed = append(removed, b.removeCrossed(b.Bids, ask, false)...)
	}
	if clearBoth || *side == Sell {
		removed = append(removed, b.removeCrossed(b.Asks, bid, true)...)
	}
	if len(removed) == 0 {
		return nil
	}
	b.UpdateCount++
	return removed
}

func (b *Book) removeCrossed(l *Ladder, price decimal.Price, asks bool) []*Level {
	kept := l.levels[:0]
	var removed []*Level
	for _, level := range l.levels {
		cmp := level.Price.Value.Cmp(price)
		remove := (asks && cmp <= 0) || (!asks && cmp >= 0)
		if remove {
			removed = append(removed, level)
			for _, order := range level.orders {
				delete(l.cache, order.ID)
			}
		} else {
			kept = append(kept, level)
		}
	}
	l.levels = kept
	return removed
}

func sortedMapKeys(values map[string]decimal.Decimal) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func (b *Book) PPrint(depth int) string {
	var result strings.Builder
	fmt.Fprintf(&result, "bid_levels: %d\nask_levels: %d\nsequence: %d\nupdate_count: %d\nts_last: %d\n",
		minDepth(b.Bids.Len(), depth), minDepth(b.Asks.Len(), depth),
		b.Sequence, b.UpdateCount, b.LastTime)
	result.WriteString("╭───────┬───────┬───────╮\n")
	result.WriteString("│ bids  │ price │ asks  │\n")
	result.WriteString("├───────┼───────┼───────┤\n")
	asks := b.Asks.Levels()
	if depth > 0 && len(asks) > depth {
		asks = asks[:depth]
	}
	for i := len(asks) - 1; i >= 0; i-- {
		fmt.Fprintf(&result, "│ %-5s │ %-5s │ %-5s │\n", "", asks[i].Price.Value, levelSizes(asks[i]))
	}
	bids := b.Bids.Levels()
	if depth > 0 && len(bids) > depth {
		bids = bids[:depth]
	}
	for _, level := range bids {
		fmt.Fprintf(&result, "│ %-5s │ %-5s │ %-5s │\n", levelSizes(level), level.Price.Value, "")
	}
	result.WriteString("╰───────┴───────┴───────╯")
	return result.String()
}

func minDepth(length, depth int) int {
	if depth > 0 && length > depth {
		return depth
	}
	return length
}

func levelSizes(level *Level) string {
	parts := make([]string, 0, level.Len())
	for _, order := range level.orders {
		parts = append(parts, order.Quantity.String())
	}
	return "[" + strings.Join(parts, ", ") + "]"
}
