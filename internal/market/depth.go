package market

import (
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"strconv"
)

const Depth10Len = 10

// OrderBookDepth10 is an aggregated top-ten market-by-price snapshot. Prices
// and quantities remain exact through the BookOrder value type.
type OrderBookDepth10 struct {
	InstrumentID InstrumentID          `json:"instrument_id"`
	Bids         [Depth10Len]BookOrder `json:"bids"`
	Asks         [Depth10Len]BookOrder `json:"asks"`
	BidCounts    [Depth10Len]uint32    `json:"bid_counts"`
	AskCounts    [Depth10Len]uint32    `json:"ask_counts"`
	Flags        uint8                 `json:"flags"`
	Sequence     uint64                `json:"sequence"`
	TsEvent      UnixNanos             `json:"ts_event"`
	TsInit       UnixNanos             `json:"ts_init"`
}

func NewOrderBookDepth10(
	instrumentID InstrumentID,
	bids, asks [Depth10Len]BookOrder,
	bidCounts, askCounts [Depth10Len]uint32,
	flags uint8,
	sequence uint64,
	tsEvent, tsInit UnixNanos,
) OrderBookDepth10 {
	return OrderBookDepth10{
		InstrumentID: instrumentID,
		Bids:         bids,
		Asks:         asks,
		BidCounts:    bidCounts,
		AskCounts:    askCounts,
		Flags:        flags,
		Sequence:     sequence,
		TsEvent:      tsEvent,
		TsInit:       tsInit,
	}
}

func OrderBookDepth10Metadata(
	instrumentID InstrumentID,
	pricePrecision, sizePrecision uint8,
) map[string]string {
	return map[string]string{
		"instrument_id":   string(instrumentID),
		"price_precision": strconv.Itoa(int(pricePrecision)),
		"size_precision":  strconv.Itoa(int(sizePrecision)),
	}
}

// OrderBookDepth10Fields returns Arrow-compatible fields in serialization
// order. A slice is used because ordering is part of the schema contract.
func OrderBookDepth10Fields() []Field {
	fields := make([]Field, 0, 64)
	for _, prefix := range []string{"bid_price", "ask_price", "bid_size", "ask_size"} {
		for i := range Depth10Len {
			fields = append(fields, Field{Name: fmt.Sprintf("%s_%d", prefix, i), Type: fixedSizeBinary})
		}
	}
	for _, prefix := range []string{"bid_count", "ask_count"} {
		for i := range Depth10Len {
			fields = append(fields, Field{Name: fmt.Sprintf("%s_%d", prefix, i), Type: "UInt32"})
		}
	}
	return append(fields,
		Field{Name: "flags", Type: "UInt8"},
		Field{Name: "sequence", Type: "UInt64"},
		Field{Name: "ts_event", Type: "UInt64"},
		Field{Name: "ts_init", Type: "UInt64"},
	)
}

func (d OrderBookDepth10) Equal(other OrderBookDepth10) bool {
	if d.InstrumentID != other.InstrumentID ||
		d.BidCounts != other.BidCounts ||
		d.AskCounts != other.AskCounts ||
		d.Flags != other.Flags ||
		d.Sequence != other.Sequence ||
		d.TsEvent != other.TsEvent ||
		d.TsInit != other.TsInit {
		return false
	}
	for i := range Depth10Len {
		if !bookOrdersEqual(d.Bids[i], other.Bids[i]) ||
			!bookOrdersEqual(d.Asks[i], other.Asks[i]) {
			return false
		}
	}
	return true
}

func (d OrderBookDepth10) Hash() uint64 {
	hasher := fnv.New64a()
	writeDepthHashString(hasher, string(d.InstrumentID))
	for i := range Depth10Len {
		writeDepthHashOrder(hasher, d.Bids[i])
		writeDepthHashOrder(hasher, d.Asks[i])
		var count [4]byte
		binary.LittleEndian.PutUint32(count[:], d.BidCounts[i])
		_, _ = hasher.Write(count[:])
		binary.LittleEndian.PutUint32(count[:], d.AskCounts[i])
		_, _ = hasher.Write(count[:])
	}
	_, _ = hasher.Write([]byte{d.Flags})
	var value [8]byte
	for _, number := range []uint64{d.Sequence, uint64(d.TsEvent), uint64(d.TsInit)} {
		binary.LittleEndian.PutUint64(value[:], number)
		_, _ = hasher.Write(value[:])
	}
	return hasher.Sum64()
}

func (d OrderBookDepth10) String() string {
	return fmt.Sprintf(
		"%s,flags=%d,sequence=%d,ts_event=%d,ts_init=%d",
		d.InstrumentID,
		d.Flags,
		d.Sequence,
		d.TsEvent,
		d.TsInit,
	)
}

func (d OrderBookDepth10) GoString() string {
	return fmt.Sprintf(
		"OrderBookDepth10 { instrument_id: %s, flags: %d, sequence: %d, ts_event: %d, ts_init: %d }",
		d.InstrumentID,
		d.Flags,
		d.Sequence,
		d.TsEvent,
		d.TsInit,
	)
}

// DepthSerializable is the Go compile-time marker corresponding to the source
// model's serialization trait.
type DepthSerializable interface {
	isDepthSerializable()
}

func (OrderBookDepth10) isDepthSerializable() {}

func bookOrdersEqual(left, right BookOrder) bool {
	return left.Side == right.Side &&
		left.Price.Decimal().Cmp(right.Price.Decimal()) == 0 &&
		left.Size.Decimal().Cmp(right.Size.Decimal()) == 0 &&
		left.OrderID == right.OrderID
}

type depthHashWriter interface {
	Write([]byte) (int, error)
}

func writeDepthHashString(hasher depthHashWriter, value string) {
	_, _ = hasher.Write([]byte(value))
	_, _ = hasher.Write([]byte{0})
}

func writeDepthHashOrder(hasher depthHashWriter, order BookOrder) {
	_, _ = hasher.Write([]byte{byte(order.Side)})
	writeDepthHashString(hasher, order.Price.String())
	writeDepthHashString(hasher, order.Size.String())
	var orderID [8]byte
	binary.LittleEndian.PutUint64(orderID[:], order.OrderID)
	_, _ = hasher.Write(orderID[:])
}
