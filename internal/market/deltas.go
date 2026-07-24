package market

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"

	"github.com/upcomers-org/platformgo/internal/decimal"
)

// BookAction is the typed operation carried by an order-book delta.
type BookAction uint8

const (
	BookActionAdd BookAction = iota
	BookActionUpdate
	BookActionDelete
	BookActionClear
)

func (a BookAction) String() string {
	switch a {
	case BookActionAdd:
		return "ADD"
	case BookActionUpdate:
		return "UPDATE"
	case BookActionDelete:
		return "DELETE"
	case BookActionClear:
		return "CLEAR"
	default:
		return fmt.Sprintf("BookAction(%d)", a)
	}
}

// OrderSide is the typed side of an order resting in a book.
type OrderSide uint8

const (
	OrderSideBuy OrderSide = iota
	OrderSideSell
)

func (s OrderSide) String() string {
	switch s {
	case OrderSideBuy:
		return "BUY"
	case OrderSideSell:
		return "SELL"
	default:
		return fmt.Sprintf("OrderSide(%d)", s)
	}
}

// BookOrder uses exact decimal types for both economic price and quantity.
type BookOrder struct {
	Side    OrderSide        `json:"side"`
	Price   decimal.Price    `json:"price"`
	Size    decimal.Quantity `json:"size"`
	OrderID uint64           `json:"order_id"`
}

func NewBookOrder(
	side OrderSide,
	price decimal.Price,
	size decimal.Quantity,
	orderID uint64,
) BookOrder {
	return BookOrder{Side: side, Price: price, Size: size, OrderID: orderID}
}

// OrderBookDelta is one typed mutation to an order book.
type OrderBookDelta struct {
	InstrumentID InstrumentID `json:"instrument_id"`
	Action       BookAction   `json:"action"`
	Order        BookOrder    `json:"order"`
	Flags        uint8        `json:"flags"`
	Sequence     uint64       `json:"sequence"`
	TsEvent      UnixNanos    `json:"ts_event"`
	TsInit       UnixNanos    `json:"ts_init"`
}

func NewOrderBookDelta(
	instrumentID InstrumentID,
	action BookAction,
	order BookOrder,
	flags uint8,
	sequence uint64,
	tsEvent, tsInit UnixNanos,
) OrderBookDelta {
	return OrderBookDelta{
		InstrumentID: instrumentID,
		Action:       action,
		Order:        order,
		Flags:        flags,
		Sequence:     sequence,
		TsEvent:      tsEvent,
		TsInit:       tsInit,
	}
}

func ClearOrderBookDelta(
	instrumentID InstrumentID,
	sequence uint64,
	tsEvent, tsInit UnixNanos,
) OrderBookDelta {
	return NewOrderBookDelta(
		instrumentID,
		BookActionClear,
		NewBookOrder(
			OrderSideBuy,
			decimal.MustPrice("0"),
			decimal.MustQuantity("0"),
			0,
		),
		0,
		sequence,
		tsEvent,
		tsInit,
	)
}

// OrderBookDeltas carries a non-empty batch. Batch metadata is copied from the
// last delta, matching the event-boundary semantics of the source model.
type OrderBookDeltas struct {
	InstrumentID InstrumentID     `json:"instrument_id"`
	Deltas       []OrderBookDelta `json:"deltas"`
	Flags        uint8            `json:"flags"`
	Sequence     uint64           `json:"sequence"`
	TsEvent      UnixNanos        `json:"ts_event"`
	TsInit       UnixNanos        `json:"ts_init"`
}

func NewOrderBookDeltasChecked(
	instrumentID InstrumentID,
	deltas []OrderBookDelta,
) (OrderBookDeltas, error) {
	if len(deltas) == 0 {
		return OrderBookDeltas{}, errors.New("`deltas` cannot be empty")
	}
	last := deltas[len(deltas)-1]
	return OrderBookDeltas{
		InstrumentID: instrumentID,
		Deltas:       append([]OrderBookDelta(nil), deltas...),
		Flags:        last.Flags,
		Sequence:     last.Sequence,
		TsEvent:      last.TsEvent,
		TsInit:       last.TsInit,
	}, nil
}

func NewOrderBookDeltas(
	instrumentID InstrumentID,
	deltas []OrderBookDelta,
) OrderBookDeltas {
	result, err := NewOrderBookDeltasChecked(instrumentID, deltas)
	if err != nil {
		panic("Condition failed: " + err.Error())
	}
	return result
}

func (d OrderBookDeltas) Clone() OrderBookDeltas {
	d.Deltas = append([]OrderBookDelta(nil), d.Deltas...)
	return d
}

func (d OrderBookDeltas) Equal(other OrderBookDeltas) bool {
	return d.InstrumentID == other.InstrumentID && d.Sequence == other.Sequence
}

// Hash follows equality and therefore includes only instrument ID and sequence.
func (d OrderBookDeltas) Hash() uint64 {
	hasher := fnv.New64a()
	_, _ = hasher.Write([]byte(d.InstrumentID))
	var sequence [8]byte
	binary.LittleEndian.PutUint64(sequence[:], d.Sequence)
	_, _ = hasher.Write(sequence[:])
	return hasher.Sum64()
}

func (d OrderBookDeltas) String() string {
	return fmt.Sprintf(
		"%s,len=%d,flags=%d,sequence=%d,ts_event=%d,ts_init=%d",
		d.InstrumentID,
		len(d.Deltas),
		d.Flags,
		d.Sequence,
		d.TsEvent,
		d.TsInit,
	)
}

// OrderBookDeltasAPI is the Go adaptation of the source's owning FFI wrapper.
// Embedding preserves direct field and method access while retaining explicit
// ownership and extraction operations.
type OrderBookDeltasAPI struct {
	*OrderBookDeltas
}

func NewOrderBookDeltasAPI(deltas OrderBookDeltas) OrderBookDeltasAPI {
	cloned := deltas.Clone()
	return OrderBookDeltasAPI{OrderBookDeltas: &cloned}
}

func (a OrderBookDeltasAPI) IntoInner() OrderBookDeltas {
	return a.OrderBookDeltas.Clone()
}

func (a OrderBookDeltasAPI) Clone() OrderBookDeltasAPI {
	return NewOrderBookDeltasAPI(a.IntoInner())
}

func (a OrderBookDeltasAPI) Equal(other OrderBookDeltasAPI) bool {
	if a.OrderBookDeltas == nil || other.OrderBookDeltas == nil {
		return a.OrderBookDeltas == nil && other.OrderBookDeltas == nil
	}
	return a.OrderBookDeltas.Equal(*other.OrderBookDeltas)
}

func (a OrderBookDeltasAPI) MarshalJSON() ([]byte, error) {
	return json.Marshal(a.OrderBookDeltas)
}

func (a *OrderBookDeltasAPI) UnmarshalJSON(data []byte) error {
	var deltas OrderBookDeltas
	if err := json.Unmarshal(data, &deltas); err != nil {
		return err
	}
	a.OrderBookDeltas = &deltas
	return nil
}
