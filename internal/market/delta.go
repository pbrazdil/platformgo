package market

import (
	"bytes"
	"encoding/binary"
	"encoding/gob"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"strconv"

	"github.com/upcomers-org/platformgo/internal/decimal"
)

const (
	// OrderSideNoOrderSide is the null side used by clear deltas.
	OrderSideNoOrderSide OrderSide = 2
	// RecordFlagSnapshot marks a complete order-book snapshot.
	RecordFlagSnapshot uint8 = 32
)

func NewOrderBookDeltaChecked(
	instrumentID InstrumentID,
	action BookAction,
	order BookOrder,
	flags uint8,
	sequence uint64,
	tsEvent, tsInit UnixNanos,
) (OrderBookDelta, error) {
	if (action == BookActionAdd || action == BookActionUpdate) && order.Size.IsZero() {
		return OrderBookDelta{}, errors.New("invalid `Quantity` for 'order.size' not positive, was 0")
	}
	return NewOrderBookDelta(instrumentID, action, order, flags, sequence, tsEvent, tsInit), nil
}

// MustNewOrderBookDelta is the checked constructor's panicking form.
func MustNewOrderBookDelta(
	instrumentID InstrumentID,
	action BookAction,
	order BookOrder,
	flags uint8,
	sequence uint64,
	tsEvent, tsInit UnixNanos,
) OrderBookDelta {
	delta, err := NewOrderBookDeltaChecked(
		instrumentID,
		action,
		order,
		flags,
		sequence,
		tsEvent,
		tsInit,
	)
	if err != nil {
		panic(err.Error())
	}
	return delta
}

// NewClearOrderBookDelta constructs the source model's canonical clear event:
// a null order and the snapshot record flag.
func NewClearOrderBookDelta(
	instrumentID InstrumentID,
	sequence uint64,
	tsEvent, tsInit UnixNanos,
) OrderBookDelta {
	return NewOrderBookDelta(
		instrumentID,
		BookActionClear,
		NewBookOrder(
			OrderSideNoOrderSide,
			decimal.MustPrice("0"),
			decimal.MustQuantity("0"),
			0,
		),
		RecordFlagSnapshot,
		sequence,
		tsEvent,
		tsInit,
	)
}

func OrderBookDeltaMetadata(
	instrumentID InstrumentID,
	pricePrecision, sizePrecision uint8,
) map[string]string {
	return map[string]string{
		"instrument_id":   string(instrumentID),
		"price_precision": strconv.Itoa(int(pricePrecision)),
		"size_precision":  strconv.Itoa(int(sizePrecision)),
	}
}

func OrderBookDeltaFields() []Field {
	return []Field{
		{Name: "action", Type: "UInt8"},
		{Name: "side", Type: "UInt8"},
		{Name: "price", Type: fixedSizeBinary},
		{Name: "size", Type: fixedSizeBinary},
		{Name: "order_id", Type: "UInt64"},
		{Name: "flags", Type: "UInt8"},
		{Name: "sequence", Type: "UInt64"},
		{Name: "ts_event", Type: "UInt64"},
		{Name: "ts_init", Type: "UInt64"},
	}
}

func (o BookOrder) Equal(other BookOrder) bool {
	return bookOrdersEqual(o, other)
}

func (o BookOrder) String() string {
	side := o.Side.String()
	if o.Side == OrderSideNoOrderSide {
		side = "NO_ORDER_SIDE"
	}
	return fmt.Sprintf("%s,%s,%s,%d", side, o.Price, o.Size, o.OrderID)
}

func (d OrderBookDelta) Equal(other OrderBookDelta) bool {
	return d.InstrumentID == other.InstrumentID &&
		d.Action == other.Action &&
		d.Order.Equal(other.Order) &&
		d.Flags == other.Flags &&
		d.Sequence == other.Sequence &&
		d.TsEvent == other.TsEvent &&
		d.TsInit == other.TsInit
}

func (d OrderBookDelta) Hash() uint64 {
	hasher := fnv.New64a()
	writeDeltaHashString(hasher, string(d.InstrumentID))
	_, _ = hasher.Write([]byte{byte(d.Action), byte(d.Order.Side)})
	writeDeltaHashString(hasher, d.Order.Price.String())
	writeDeltaHashString(hasher, d.Order.Size.String())
	var value [8]byte
	for _, number := range []uint64{
		d.Order.OrderID,
		uint64(d.Flags),
		d.Sequence,
		uint64(d.TsEvent),
		uint64(d.TsInit),
	} {
		binary.LittleEndian.PutUint64(value[:], number)
		_, _ = hasher.Write(value[:])
	}
	return hasher.Sum64()
}

func (d OrderBookDelta) String() string {
	return fmt.Sprintf(
		"%s,%s,%s,%d,%d,%d,%d",
		d.InstrumentID,
		d.Action,
		d.Order,
		d.Flags,
		d.Sequence,
		d.TsEvent,
		d.TsInit,
	)
}

func (d OrderBookDelta) GoString() string {
	return fmt.Sprintf(
		"OrderBookDelta { instrument_id: %s, action: %s, order: %s, flags: %d, sequence: %d, ts_event: %d, ts_init: %d }",
		d.InstrumentID,
		deltaActionDebug(d.Action),
		d.Order,
		d.Flags,
		d.Sequence,
		d.TsEvent,
		d.TsInit,
	)
}

func (d OrderBookDelta) MarshalJSON() ([]byte, error) {
	type wire OrderBookDelta
	return json.Marshal(struct {
		Type string `json:"type"`
		wire
	}{Type: "OrderBookDelta", wire: wire(d)})
}

func (d *OrderBookDelta) UnmarshalJSON(data []byte) error {
	type wire OrderBookDelta
	var decoded struct {
		Type string `json:"type"`
		wire
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	if decoded.Type != "" && decoded.Type != "OrderBookDelta" {
		return fmt.Errorf("invalid market data type %q", decoded.Type)
	}
	*d = OrderBookDelta(decoded.wire)
	return nil
}

// MarshalBinary is the native Go adaptation of the source MessagePack path.
// Economic values remain exact decimal strings on the wire.
func (d OrderBookDelta) MarshalBinary() ([]byte, error) {
	wire := orderBookDeltaBinary{
		InstrumentID: string(d.InstrumentID),
		Action:       uint8(d.Action),
		Side:         uint8(d.Order.Side),
		Price:        d.Order.Price.String(),
		Size:         d.Order.Size.String(),
		OrderID:      d.Order.OrderID,
		Flags:        d.Flags,
		Sequence:     d.Sequence,
		TsEvent:      uint64(d.TsEvent),
		TsInit:       uint64(d.TsInit),
	}
	var output bytes.Buffer
	if err := gob.NewEncoder(&output).Encode(wire); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func (d *OrderBookDelta) UnmarshalBinary(data []byte) error {
	var wire orderBookDeltaBinary
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&wire); err != nil {
		return err
	}
	price, err := decimal.ParsePrice(wire.Price)
	if err != nil {
		return err
	}
	size, err := decimal.ParseQuantity(wire.Size)
	if err != nil {
		return err
	}
	*d = NewOrderBookDelta(
		InstrumentID(wire.InstrumentID),
		BookAction(wire.Action),
		NewBookOrder(OrderSide(wire.Side), price, size, wire.OrderID),
		wire.Flags,
		wire.Sequence,
		UnixNanos(wire.TsEvent),
		UnixNanos(wire.TsInit),
	)
	return nil
}

type orderBookDeltaBinary struct {
	InstrumentID string
	Action       uint8
	Side         uint8
	Price        string
	Size         string
	OrderID      uint64
	Flags        uint8
	Sequence     uint64
	TsEvent      uint64
	TsInit       uint64
}

type deltaHashWriter interface {
	Write([]byte) (int, error)
}

func writeDeltaHashString(hasher deltaHashWriter, value string) {
	_, _ = hasher.Write([]byte(value))
	_, _ = hasher.Write([]byte{0})
}

func deltaActionDebug(action BookAction) string {
	switch action {
	case BookActionAdd:
		return "Add"
	case BookActionUpdate:
		return "Update"
	case BookActionDelete:
		return "Delete"
	case BookActionClear:
		return "Clear"
	default:
		return action.String()
	}
}
