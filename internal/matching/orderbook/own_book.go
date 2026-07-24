package orderbook

import (
	"fmt"
	"slices"
	"strings"

	"github.com/upcomers-org/platformgo/internal/decimal"
)

type OrderStatus uint8

const (
	StatusSubmitted OrderStatus = iota + 1
	StatusAccepted
	StatusCanceled
	StatusFilled
)

func (s OrderStatus) String() string {
	switch s {
	case StatusSubmitted:
		return "SUBMITTED"
	case StatusAccepted:
		return "ACCEPTED"
	case StatusCanceled:
		return "CANCELED"
	case StatusFilled:
		return "FILLED"
	default:
		return fmt.Sprintf("OrderStatus(%d)", s)
	}
}

// OwnOrder is the subset of a trader's working-order state needed to filter
// public liquidity and maintain price-level indexes.
type OwnOrder struct {
	TraderID      string
	ClientID      string
	VenueID       string
	Side          Side
	Price         decimal.Price
	Quantity      decimal.Quantity
	Status        OrderStatus
	LastTime      uint64
	AcceptedTime  uint64
	SubmittedTime uint64
	InitTime      uint64
}

func NewOwnOrder(clientID string, side Side, price decimal.Price, quantity decimal.Quantity, status OrderStatus) OwnOrder {
	return OwnOrder{
		TraderID: "TRADER-001", ClientID: clientID, Side: side, Price: price,
		Quantity: quantity, Status: status,
	}
}

func (o OwnOrder) Exposure() decimal.Decimal {
	return o.Price.Decimal().Mul(o.Quantity.Decimal())
}

func (o OwnOrder) ToBookPrice() BookPrice { return NewBookPrice(o.Price, o.Side) }

func (o OwnOrder) SignedSize() decimal.Decimal {
	if o.Side == Sell {
		return o.Quantity.Decimal().Neg()
	}
	return o.Quantity.Decimal()
}

func (o OwnOrder) Debug() string {
	venue := o.VenueID
	if venue == "" {
		venue = "None"
	}
	return fmt.Sprintf(
		"OwnBookOrder(trader_id=%s, client_order_id=%s, venue_order_id=%s, side=%s, price=%s, size=%s, order_type=LIMIT, time_in_force=GTC, status=%s, ts_last=%d, ts_accepted=%d, ts_submitted=%d, ts_init=%d)",
		o.TraderID, o.ClientID, venue, o.Side, o.Price, o.Quantity, o.Status,
		o.LastTime, o.AcceptedTime, o.SubmittedTime, o.InitTime,
	)
}

func (o OwnOrder) String() string {
	venue := o.VenueID
	if venue == "" {
		venue = "None"
	}
	return fmt.Sprintf("%s,%s,%s,%s,%s,%s,LIMIT,GTC,%s,%d,%d,%d,%d",
		o.TraderID, o.ClientID, venue, o.Side, o.Price, o.Quantity, o.Status,
		o.LastTime, o.AcceptedTime, o.SubmittedTime, o.InitTime)
}

type OwnBook struct {
	InstrumentID string
	orders       map[string]OwnOrder
	bidIDs       []string
	askIDs       []string
	UpdateCount  uint64
	LastTime     uint64
}

func NewOwnBook(instrumentID string) *OwnBook {
	return &OwnBook{InstrumentID: instrumentID, orders: make(map[string]OwnOrder)}
}

func (b *OwnBook) String() string {
	return fmt.Sprintf("OwnOrderBook(instrument_id=%s, orders=%d, update_count=%d)",
		b.InstrumentID, len(b.orders), b.UpdateCount)
}

func (b *OwnBook) PPrint(depth int) string {
	public := NewBook(b.InstrumentID, L3MBO)
	id := uint64(1)
	for _, clientID := range append(append([]string(nil), b.bidIDs...), b.askIDs...) {
		order := b.orders[clientID]
		public.ladder(order.Side).Add(NewOrder(order.Side, order.Price, order.Quantity, id), 0)
		id++
	}
	public.UpdateCount = b.UpdateCount
	public.LastTime = b.LastTime
	text := public.PPrint(depth)
	text = strings.Replace(text, "sequence: 0\n", "", 1)
	return text
}

func (b *OwnBook) Add(order OwnOrder) {
	if old, exists := b.orders[order.ClientID]; exists {
		b.removeID(old.Side, old.ClientID)
	}
	b.orders[order.ClientID] = order
	b.appendID(order.Side, order.ClientID)
	b.UpdateCount++
	b.LastTime = order.LastTime
}

func (b *OwnBook) Update(order OwnOrder) error {
	old, exists := b.orders[order.ClientID]
	if !exists {
		return fmt.Errorf("%w: client_order_id=%s", ErrOrderNotFound, order.ClientID)
	}
	b.orders[order.ClientID] = order
	if old.Side != order.Side || !old.Price.Equal(order.Price) {
		b.removeID(old.Side, old.ClientID)
		b.appendID(order.Side, order.ClientID)
	}
	b.UpdateCount++
	b.LastTime = order.LastTime
	return nil
}

func (b *OwnBook) Delete(clientID string) error {
	order, exists := b.orders[clientID]
	if !exists {
		return fmt.Errorf("%w: client_order_id=%s", ErrOrderNotFound, clientID)
	}
	delete(b.orders, clientID)
	b.removeID(order.Side, clientID)
	b.UpdateCount++
	return nil
}

func (b *OwnBook) Clear() {
	clear(b.orders)
	b.bidIDs = nil
	b.askIDs = nil
	b.UpdateCount++
}

func (b *OwnBook) Contains(clientID string) bool {
	_, ok := b.orders[clientID]
	return ok
}

func (b *OwnBook) BidClientIDs() []string { return append([]string(nil), b.bidIDs...) }
func (b *OwnBook) AskClientIDs() []string { return append([]string(nil), b.askIDs...) }

func (b *OwnBook) appendID(side Side, id string) {
	ids := &b.askIDs
	if side == Buy {
		ids = &b.bidIDs
	}
	if !slices.Contains(*ids, id) {
		*ids = append(*ids, id)
	}
}

func (b *OwnBook) removeID(side Side, id string) {
	ids := &b.askIDs
	if side == Buy {
		ids = &b.bidIDs
	}
	for i, candidate := range *ids {
		if candidate == id {
			*ids = append((*ids)[:i], (*ids)[i+1:]...)
			return
		}
	}
}

func (b *OwnBook) Orders(side Side, statuses map[OrderStatus]bool, acceptedBuffer, now uint64) []OwnOrder {
	ids := b.askIDs
	if side == Buy {
		ids = b.bidIDs
	}
	result := make([]OwnOrder, 0, len(ids))
	for _, id := range ids {
		order := b.orders[id]
		if len(statuses) > 0 && !statuses[order.Status] {
			continue
		}
		if order.Status == StatusAccepted && acceptedBuffer > 0 &&
			(now < order.AcceptedTime || now-order.AcceptedTime < acceptedBuffer) {
			continue
		}
		result = append(result, order)
	}
	return result
}

func (b *OwnBook) Quantities(side Side, statuses map[OrderStatus]bool, acceptedBuffer, now uint64) map[string]decimal.Decimal {
	result := make(map[string]decimal.Decimal)
	for _, order := range b.Orders(side, statuses, acceptedBuffer, now) {
		key := order.Price.Decimal().Normalize().String()
		result[key] = result[key].Add(order.Quantity.Decimal())
	}
	return result
}

func (b *OwnBook) OrdersMap(side Side, statuses map[OrderStatus]bool, acceptedBuffer, now uint64) map[string][]OwnOrder {
	result := make(map[string][]OwnOrder)
	for _, order := range b.Orders(side, statuses, acceptedBuffer, now) {
		key := order.Price.Decimal().Normalize().String()
		result[key] = append(result[key], order)
	}
	return result
}

func (b *OwnBook) Group(side Side, increment decimal.Decimal, depth int, statuses map[OrderStatus]bool, acceptedBuffer, now uint64) map[string]decimal.Decimal {
	temp := NewBook(b.InstrumentID, L3MBO)
	for i, order := range b.Orders(side, statuses, acceptedBuffer, now) {
		temp.Add(NewOrder(order.Side, order.Price, order.Quantity, uint64(i+1)), 0, 0, 0)
	}
	return temp.Group(side, increment, depth)
}

// Audit removes every order not named by the authoritative open-order set.
func (b *OwnBook) Audit(openIDs map[string]bool) {
	for id := range b.orders {
		if !openIDs[id] {
			_ = b.Delete(id)
		}
	}
}

// CombinedWithOpposite clones this book and parity-transforms orders from the
// opposite instrument: side flips and price becomes 1-price.
func (b *OwnBook) CombinedWithOpposite(other *OwnBook) (*OwnBook, error) {
	if b.InstrumentID == other.InstrumentID {
		return nil, fmt.Errorf("opposite instrument must differ: %s", b.InstrumentID)
	}
	result := NewOwnBook(b.InstrumentID)
	for _, id := range append(append([]string(nil), b.bidIDs...), b.askIDs...) {
		result.Add(b.orders[id])
	}
	one := decimal.MustParse("1")
	for _, id := range append(append([]string(nil), other.bidIDs...), other.askIDs...) {
		order := other.orders[id]
		price, err := decimal.ParsePrice(one.Sub(order.Price.Decimal()).String())
		if err != nil {
			return nil, err
		}
		order.Side = opposite(order.Side)
		order.Price = price
		result.Add(order)
	}
	return result, nil
}

func (b *Book) FilteredMap(side Side, depth int, own *OwnBook, statuses map[OrderStatus]bool, acceptedBuffer, now uint64) map[string]decimal.Decimal {
	result := ladderMap(b.ladder(side), depth)
	if own == nil {
		return result
	}
	for key, quantity := range own.Quantities(side, statuses, acceptedBuffer, now) {
		public, exists := result[key]
		if !exists {
			continue
		}
		remaining := public.Sub(quantity)
		if remaining.Sign() <= 0 {
			delete(result, key)
		} else {
			result[key] = remaining
		}
	}
	return result
}

func (b *Book) GroupFiltered(side Side, increment decimal.Decimal, depth int, own *OwnBook, statuses map[OrderStatus]bool, acceptedBuffer, now uint64) map[string]decimal.Decimal {
	temp := NewBook(b.InstrumentID, L2MBP)
	id := uint64(1)
	for priceText, size := range b.FilteredMap(side, depth, own, statuses, acceptedBuffer, now) {
		temp.ladder(side).Add(NewOrder(
			side, decimal.MustPrice(priceText), decimal.MustQuantity(size.String()), id,
		), 0)
		id++
	}
	return temp.Group(side, increment, 0)
}

func (b *Book) FilteredView(own *OwnBook, depth int, statuses map[OrderStatus]bool, acceptedBuffer, now uint64) (*Book, error) {
	if own != nil && own.InstrumentID != b.InstrumentID {
		return nil, fmt.Errorf("%w: book=%s own_book=%s", ErrInstrumentMismatch, b.InstrumentID, own.InstrumentID)
	}
	result := NewBook(b.InstrumentID, b.BookType)
	result.Sequence, result.LastTime, result.UpdateCount = b.Sequence, b.LastTime, b.UpdateCount
	id := uint64(1)
	for _, side := range []Side{Buy, Sell} {
		for key, size := range b.FilteredMap(side, depth, own, statuses, acceptedBuffer, now) {
			price := decimal.MustPrice(key)
			quantity := decimal.MustQuantity(size.String())
			result.ladder(side).Add(NewOrder(side, price, quantity, id), 0)
			id++
		}
	}
	return result, nil
}
