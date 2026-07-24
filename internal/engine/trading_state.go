package engine

import (
	"sort"

	"github.com/upcomers-org/platformgo/internal/domain"
)

type tradingState struct {
	instruments []instrumentRecord
	books       []bookRecord
	orders      []orderRecord
	fills       []fillRecord
}

type instrumentRecord struct {
	revision domain.InstrumentRevision
}

type bookRecord struct {
	instrumentID string
	markPrice    domain.Price
	hasMark      bool
	bids         []levelRecord
	asks         []levelRecord
}

type levelRecord struct {
	price    domain.Price
	quantity domain.Quantity
}

type orderRecord struct {
	orderID           ID
	accountID         string
	instrument        domain.InstrumentRevision
	side              Side
	orderType         OrderType
	timeInForce       TimeInForce
	status            OrderStatus
	quantity          domain.Quantity
	filledQuantity    domain.Quantity
	averagePrice      domain.Price
	hasAverage        bool
	price             domain.Price
	hasPrice          bool
	triggerPrice      domain.Price
	hasTrigger        bool
	reduceOnly        bool
	hasSlippageBand   bool
	maxSlippageBPS    uint32
	slippageReference domain.Price
	rejectReason      RejectionReason
	version           uint64
}

type fillRecord struct {
	fillID      ID
	orderID     ID
	accountID   string
	instrument  domain.InstrumentRevision
	side        Side
	price       domain.Price
	quantity    domain.Quantity
	logicalTime LogicalTime
}

func (state tradingState) clone() tradingState {
	cloned := tradingState{
		instruments: append([]instrumentRecord(nil), state.instruments...),
		orders:      append([]orderRecord(nil), state.orders...),
		fills:       append([]fillRecord(nil), state.fills...),
		books:       make([]bookRecord, len(state.books)),
	}
	for index, book := range state.books {
		cloned.books[index] = book
		cloned.books[index].bids = append([]levelRecord(nil), book.bids...)
		cloned.books[index].asks = append([]levelRecord(nil), book.asks...)
	}
	return cloned
}

func (state tradingState) instrument(instrumentID string) (domain.InstrumentRevision, bool) {
	for _, instrument := range state.instruments {
		if instrument.revision.ID() == instrumentID {
			return instrument.revision, true
		}
	}
	return domain.InstrumentRevision{}, false
}

func (state *tradingState) replaceInstrument(revision domain.InstrumentRevision) {
	for index := range state.instruments {
		if state.instruments[index].revision.ID() == revision.ID() {
			state.instruments[index] = instrumentRecord{revision: revision}
			return
		}
	}
	state.instruments = append(state.instruments, instrumentRecord{revision: revision})
	sort.Slice(state.instruments, func(left, right int) bool {
		return state.instruments[left].revision.ID() < state.instruments[right].revision.ID()
	})
}

func (state *tradingState) book(instrumentID string) (*bookRecord, bool) {
	for index := range state.books {
		if state.books[index].instrumentID == instrumentID {
			return &state.books[index], true
		}
	}
	return nil, false
}

func (state *tradingState) replaceBook(book bookRecord) {
	for index := range state.books {
		if state.books[index].instrumentID == book.instrumentID {
			state.books[index] = book
			return
		}
	}
	state.books = append(state.books, book)
	sort.Slice(state.books, func(left, right int) bool {
		return state.books[left].instrumentID < state.books[right].instrumentID
	})
}

func (state tradingState) orderIndex(orderID ID) (int, bool) {
	for index := range state.orders {
		if state.orders[index].orderID == orderID {
			return index, true
		}
	}
	return 0, false
}

// Order returns a detached snapshot of one order.
func (state State) Order(orderID ID) (OrderSnapshot, bool) {
	index, ok := state.trading.orderIndex(orderID)
	if !ok {
		return OrderSnapshot{}, false
	}
	return state.trading.orders[index].snapshot(), true
}

// Orders returns canonical order-ID-sorted snapshots.
func (state State) Orders() []OrderSnapshot {
	orders := make([]OrderSnapshot, len(state.trading.orders))
	for index, order := range state.trading.orders {
		orders[index] = order.snapshot()
	}
	sort.Slice(orders, func(left, right int) bool {
		return orders[left].OrderID.String() < orders[right].OrderID.String()
	})
	return orders
}

// FillsForOrder returns stable fill-ID-sorted snapshots for one order.
func (state State) FillsForOrder(orderID ID) []FillSnapshot {
	var fills []FillSnapshot
	for _, fill := range state.trading.fills {
		if fill.orderID == orderID {
			fills = append(fills, fill.snapshot())
		}
	}
	sort.Slice(fills, func(left, right int) bool {
		return fills[left].FillID.String() < fills[right].FillID.String()
	})
	return fills
}

func (order orderRecord) snapshot() OrderSnapshot {
	snapshot := OrderSnapshot{
		OrderID:         order.orderID,
		AccountID:       order.accountID,
		InstrumentID:    order.instrument.ID(),
		Side:            order.side,
		Type:            order.orderType,
		TimeInForce:     order.timeInForce,
		Status:          order.status,
		Quantity:        order.quantity.Decimal().String(),
		FilledQuantity:  order.filledQuantity.Decimal().String(),
		ReduceOnly:      order.reduceOnly,
		HasSlippageBand: order.hasSlippageBand,
		MaxSlippageBPS:  order.maxSlippageBPS,
		RejectReason:    order.rejectReason,
		Version:         order.version,
	}
	if order.hasSlippageBand {
		snapshot.SlippageReference = order.slippageReference.Decimal().String()
	}
	if order.hasAverage {
		snapshot.AverageFillPrice = order.averagePrice.Decimal().String()
	}
	if order.hasPrice {
		snapshot.Price = order.price.Decimal().String()
	}
	if order.hasTrigger {
		snapshot.TriggerPrice = order.triggerPrice.Decimal().String()
	}
	return snapshot
}

func (fill fillRecord) snapshot() FillSnapshot {
	return FillSnapshot{
		FillID:       fill.fillID,
		OrderID:      fill.orderID,
		AccountID:    fill.accountID,
		InstrumentID: fill.instrument.ID(),
		Side:         fill.side,
		Price:        fill.price.Decimal().String(),
		Quantity:     fill.quantity.Decimal().String(),
		LogicalTime:  fill.logicalTime,
	}
}

func (book bookRecord) snapshot() BookSnapshot {
	snapshot := BookSnapshot{
		InstrumentID: book.instrumentID,
		Bids:         levelSnapshots(book.bids),
		Asks:         levelSnapshots(book.asks),
	}
	if book.hasMark {
		snapshot.MarkPrice = book.markPrice.Decimal().String()
	}
	return snapshot
}

func levelSnapshots(levels []levelRecord) []BookLevel {
	snapshots := make([]BookLevel, len(levels))
	for index, level := range levels {
		snapshots[index] = BookLevel{
			Price:    level.price.Decimal().String(),
			Quantity: level.quantity.Decimal().String(),
		}
	}
	return snapshots
}
