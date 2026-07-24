package market

import (
	"bytes"
	"encoding/gob"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/upcomers-org/platformgo/internal/decimal"
)

// MarkPriceUpdate represents one mark-price update.
type MarkPriceUpdate struct {
	InstrumentID InstrumentID  `json:"instrument_id"`
	Value        decimal.Price `json:"value"`
	TsEvent      UnixNanos     `json:"ts_event"`
	TsInit       UnixNanos     `json:"ts_init"`
}

func NewMarkPriceUpdate(instrumentID InstrumentID, value decimal.Price, tsEvent, tsInit UnixNanos) MarkPriceUpdate {
	return MarkPriceUpdate{InstrumentID: instrumentID, Value: value, TsEvent: tsEvent, TsInit: tsInit}
}

func MarkPriceMetadata(instrumentID InstrumentID, pricePrecision uint8) map[string]string {
	return priceMetadata(instrumentID, pricePrecision)
}

func MarkPriceFields() []Field {
	return priceFields()
}

func (m MarkPriceUpdate) Equal(other MarkPriceUpdate) bool {
	return m.InstrumentID == other.InstrumentID &&
		m.Value.Equal(other.Value) &&
		m.TsEvent == other.TsEvent &&
		m.TsInit == other.TsInit
}

func (m MarkPriceUpdate) Hash64() uint64 {
	return priceUpdateHash(m.InstrumentID, m.Value, m.TsEvent, m.TsInit)
}

func (m MarkPriceUpdate) String() string {
	return priceUpdateString(m.InstrumentID, m.Value, m.TsEvent, m.TsInit)
}

func (m MarkPriceUpdate) MarshalJSON() ([]byte, error) {
	type wire MarkPriceUpdate
	return json.Marshal(struct {
		Type string `json:"type"`
		wire
	}{Type: "MarkPriceUpdate", wire: wire(m)})
}

func (m *MarkPriceUpdate) UnmarshalJSON(data []byte) error {
	type wire MarkPriceUpdate
	var decoded struct {
		Type string `json:"type"`
		wire
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	if decoded.Type != "" && decoded.Type != "MarkPriceUpdate" {
		return fmt.Errorf("invalid market data type %q", decoded.Type)
	}
	*m = MarkPriceUpdate(decoded.wire)
	return nil
}

func (m MarkPriceUpdate) MarshalBinary() ([]byte, error) {
	return marshalPriceUpdate(m.InstrumentID, m.Value, m.TsEvent, m.TsInit)
}

func (m *MarkPriceUpdate) UnmarshalBinary(data []byte) error {
	instrumentID, value, tsEvent, tsInit, err := unmarshalPriceUpdate(data)
	if err != nil {
		return err
	}
	*m = NewMarkPriceUpdate(instrumentID, value, tsEvent, tsInit)
	return nil
}

// IndexPriceUpdate represents one index-price update.
type IndexPriceUpdate struct {
	InstrumentID InstrumentID  `json:"instrument_id"`
	Value        decimal.Price `json:"value"`
	TsEvent      UnixNanos     `json:"ts_event"`
	TsInit       UnixNanos     `json:"ts_init"`
}

func NewIndexPriceUpdate(instrumentID InstrumentID, value decimal.Price, tsEvent, tsInit UnixNanos) IndexPriceUpdate {
	return IndexPriceUpdate{InstrumentID: instrumentID, Value: value, TsEvent: tsEvent, TsInit: tsInit}
}

func IndexPriceMetadata(instrumentID InstrumentID, pricePrecision uint8) map[string]string {
	return priceMetadata(instrumentID, pricePrecision)
}

func IndexPriceFields() []Field {
	return priceFields()
}

func (i IndexPriceUpdate) Equal(other IndexPriceUpdate) bool {
	return i.InstrumentID == other.InstrumentID &&
		i.Value.Equal(other.Value) &&
		i.TsEvent == other.TsEvent &&
		i.TsInit == other.TsInit
}

func (i IndexPriceUpdate) Hash64() uint64 {
	return priceUpdateHash(i.InstrumentID, i.Value, i.TsEvent, i.TsInit)
}

func (i IndexPriceUpdate) String() string {
	return priceUpdateString(i.InstrumentID, i.Value, i.TsEvent, i.TsInit)
}

func (i IndexPriceUpdate) MarshalJSON() ([]byte, error) {
	type wire IndexPriceUpdate
	return json.Marshal(struct {
		Type string `json:"type"`
		wire
	}{Type: "IndexPriceUpdate", wire: wire(i)})
}

func (i *IndexPriceUpdate) UnmarshalJSON(data []byte) error {
	type wire IndexPriceUpdate
	var decoded struct {
		Type string `json:"type"`
		wire
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	if decoded.Type != "" && decoded.Type != "IndexPriceUpdate" {
		return fmt.Errorf("invalid market data type %q", decoded.Type)
	}
	*i = IndexPriceUpdate(decoded.wire)
	return nil
}

func (i IndexPriceUpdate) MarshalBinary() ([]byte, error) {
	return marshalPriceUpdate(i.InstrumentID, i.Value, i.TsEvent, i.TsInit)
}

func (i *IndexPriceUpdate) UnmarshalBinary(data []byte) error {
	instrumentID, value, tsEvent, tsInit, err := unmarshalPriceUpdate(data)
	if err != nil {
		return err
	}
	*i = NewIndexPriceUpdate(instrumentID, value, tsEvent, tsInit)
	return nil
}

func priceMetadata(instrumentID InstrumentID, pricePrecision uint8) map[string]string {
	return map[string]string{
		"instrument_id":   string(instrumentID),
		"price_precision": strconv.Itoa(int(pricePrecision)),
	}
}

func priceFields() []Field {
	return []Field{
		{Name: "value", Type: fixedSizeBinary},
		{Name: "ts_event", Type: "UInt64"},
		{Name: "ts_init", Type: "UInt64"},
	}
}

func priceUpdateHash(instrumentID InstrumentID, value decimal.Price, tsEvent, tsInit UnixNanos) uint64 {
	return hashStrings(
		string(instrumentID),
		value.Decimal().Normalize().String(),
		fmt.Sprint(tsEvent),
		fmt.Sprint(tsInit),
	)
}

func priceUpdateString(instrumentID InstrumentID, value decimal.Price, tsEvent, tsInit UnixNanos) string {
	return fmt.Sprintf("%s,%s,%d,%d", instrumentID, value, tsEvent, tsInit)
}

func marshalPriceUpdate(instrumentID InstrumentID, value decimal.Price, tsEvent, tsInit UnixNanos) ([]byte, error) {
	wire := priceUpdateBinary{
		InstrumentID: string(instrumentID),
		Value:        value.String(),
		TsEvent:      uint64(tsEvent),
		TsInit:       uint64(tsInit),
	}
	var output bytes.Buffer
	if err := gob.NewEncoder(&output).Encode(wire); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func unmarshalPriceUpdate(data []byte) (InstrumentID, decimal.Price, UnixNanos, UnixNanos, error) {
	var wire priceUpdateBinary
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&wire); err != nil {
		return "", decimal.Price{}, 0, 0, err
	}
	value, err := decimal.ParsePrice(wire.Value)
	if err != nil {
		return "", decimal.Price{}, 0, 0, err
	}
	return InstrumentID(wire.InstrumentID), value, UnixNanos(wire.TsEvent), UnixNanos(wire.TsInit), nil
}

type priceUpdateBinary struct {
	InstrumentID string
	Value        string
	TsEvent      uint64
	TsInit       uint64
}
