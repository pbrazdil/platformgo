package orderbook

import (
	"fmt"

	"github.com/upcomers-org/platformgo/internal/decimal"
)

type DepthSnapshot struct {
	InstrumentID string
	Bids         []Order
	Asks         []Order
	Sequence     uint64
	EventTime    uint64
}

func (b *Book) ApplyDepth(depth DepthSnapshot) error {
	if depth.InstrumentID != b.InstrumentID {
		return fmt.Errorf("%w: book=%s depth=%s", ErrInstrumentMismatch, b.InstrumentID, depth.InstrumentID)
	}
	b.Bids.Clear()
	b.Asks.Clear()
	for _, order := range depth.Bids {
		if order.Side == Buy && order.Price.IsPositive() && order.Quantity.IsPositive() {
			b.Bids.Add(order, 0)
		}
	}
	for _, order := range depth.Asks {
		if order.Side == Sell && order.Price.IsPositive() && order.Quantity.IsPositive() {
			b.Asks.Add(order, 0)
		}
	}
	b.advance(depth.Sequence, depth.EventTime)
	return nil
}

type QuoteTick struct {
	InstrumentID        string
	BidPrice, AskPrice  decimal.Price
	BidSize, AskSize    decimal.Quantity
	EventTime, InitTime uint64
}

type TradeTick struct {
	InstrumentID        string
	Price               decimal.Price
	Size                decimal.Quantity
	EventTime, InitTime uint64
}

func (b *Book) UpdateQuoteTick(quote QuoteTick) error {
	if quote.InstrumentID != b.InstrumentID {
		return fmt.Errorf("%w: book=%s quote=%s", ErrInstrumentMismatch, b.InstrumentID, quote.InstrumentID)
	}
	if quote.EventTime < b.LastTime {
		return nil
	}
	b.Bids.Clear()
	b.Asks.Clear()
	b.Bids.Add(NewOrder(Buy, quote.BidPrice, quote.BidSize, 1), 0)
	b.Asks.Add(NewOrder(Sell, quote.AskPrice, quote.AskSize, 1), 0)
	b.advance(b.Sequence+1, quote.EventTime)
	return nil
}

func (b *Book) UpdateTradeTick(trade TradeTick) error {
	if trade.InstrumentID != b.InstrumentID {
		return fmt.Errorf("%w: book=%s trade=%s", ErrInstrumentMismatch, b.InstrumentID, trade.InstrumentID)
	}
	if trade.EventTime < b.LastTime {
		return nil
	}
	b.Bids.Clear()
	b.Asks.Clear()
	b.Bids.Add(NewOrder(Buy, trade.Price, trade.Size, 1), 0)
	b.Asks.Add(NewOrder(Sell, trade.Price, trade.Size, 1), 0)
	b.advance(b.Sequence+1, trade.EventTime)
	return nil
}

type Action uint8

const (
	ActionAdd Action = iota + 1
	ActionUpdate
	ActionDelete
	ActionClear
)

type Delta struct {
	InstrumentID string
	Action       Action
	Order        Order
	Flags        uint8
	Sequence     uint64
	EventTime    uint64
	InitTime     uint64
}

func ClearDelta(instrumentID string, sequence, eventTime, initTime uint64) Delta {
	return Delta{
		InstrumentID: instrumentID, Action: ActionClear,
		Flags: FlagSnapshot, Sequence: sequence, EventTime: eventTime, InitTime: initTime,
	}
}

func (b *Book) ApplyDelta(delta Delta) error {
	if delta.InstrumentID != b.InstrumentID {
		return fmt.Errorf("%w: book=%s delta=%s", ErrInstrumentMismatch, b.InstrumentID, delta.InstrumentID)
	}
	if delta.Action == ActionClear {
		b.Clear(delta.Sequence, delta.EventTime)
		return nil
	}
	order := delta.Order
	if order.Side != Buy && order.Side != Sell {
		if price, ok := b.Bids.cache[order.ID]; ok {
			order.Side, order.Price = Buy, price.Value
		} else if price, ok := b.Asks.cache[order.ID]; ok {
			order.Side, order.Price = Sell, price.Value
		} else if delta.Action == ActionAdd {
			return errorsNoOrderSide()
		} else {
			return nil
		}
	}
	switch delta.Action {
	case ActionAdd:
		b.Add(order, delta.Flags, delta.Sequence, delta.EventTime)
	case ActionUpdate:
		b.Update(order, delta.Flags, delta.Sequence, delta.EventTime)
	case ActionDelete:
		b.Delete(order, delta.Sequence, delta.EventTime)
	default:
		return fmt.Errorf("unknown book action %d", delta.Action)
	}
	return nil
}

func errorsNoOrderSide() error { return fmt.Errorf("order side is unspecified") }

func (b *Book) ApplyDeltas(deltas []Delta) error {
	for _, delta := range deltas {
		if err := b.ApplyDelta(delta); err != nil {
			return err
		}
	}
	return nil
}

func (b *Book) ToDeltas(eventTime, initTime uint64) []Delta {
	result := []Delta{ClearDelta(b.InstrumentID, b.Sequence, eventTime, initTime)}
	for _, ladder := range []*Ladder{b.Bids, b.Asks} {
		for _, level := range ladder.levels {
			for _, order := range level.orders {
				result = append(result, Delta{
					InstrumentID: b.InstrumentID, Action: ActionAdd, Order: order,
					Flags: FlagSnapshot, Sequence: b.Sequence, EventTime: eventTime, InitTime: initTime,
				})
			}
		}
	}
	result[len(result)-1].Flags |= FlagLast
	return result
}

// SanitizeOperations removes duplicate L2/L3 adds until a delete and gives
// every L1 side its stable synthetic ID.
func SanitizeOperations(bookType BookType, deltas []Delta) []Delta {
	seen := make(map[uint64]bool)
	result := make([]Delta, 0, len(deltas))
	for _, delta := range deltas {
		if bookType == L1MBP && delta.Action != ActionClear {
			if delta.Order.Side == Buy {
				delta.Order.ID = 1
			} else if delta.Order.Side == Sell {
				delta.Order.ID = 1
			}
			result = append(result, delta)
			continue
		}
		if delta.Action == ActionAdd && seen[delta.Order.ID] {
			continue
		}
		result = append(result, delta)
		if delta.Action == ActionAdd {
			seen[delta.Order.ID] = true
		} else if delta.Action == ActionDelete {
			delete(seen, delta.Order.ID)
		}
	}
	return result
}

// DeltasToQuotes emits only complete, changed top-of-book states.
func DeltasToQuotes(bookType BookType, deltas []Delta) []QuoteTick {
	if len(deltas) == 0 {
		panic("deltas must not be empty")
	}
	book := NewBook(deltas[0].InstrumentID, bookType)
	var result []QuoteTick
	var previous QuoteTick
	hasPrevious := false
	for _, delta := range deltas {
		if delta.Action == ActionClear {
			hasPrevious = false
		}
		if err := book.ApplyDelta(delta); err != nil {
			panic(err)
		}
		bidPrice, bidOK := book.BestBidPrice()
		askPrice, askOK := book.BestAskPrice()
		bidSize, bidSizeOK := book.BestBidSize()
		askSize, askSizeOK := book.BestAskSize()
		if !bidOK || !askOK || !bidSizeOK || !askSizeOK {
			continue
		}
		quote := QuoteTick{
			InstrumentID: book.InstrumentID, BidPrice: bidPrice, AskPrice: askPrice,
			BidSize: bidSize, AskSize: askSize, EventTime: delta.EventTime, InitTime: delta.InitTime,
		}
		if hasPrevious &&
			quote.BidPrice.Equal(previous.BidPrice) && quote.AskPrice.Equal(previous.AskPrice) &&
			quote.BidSize.Equal(previous.BidSize) && quote.AskSize.Equal(previous.AskSize) {
			continue
		}
		result = append(result, quote)
		previous, hasPrevious = quote, true
	}
	return result
}
