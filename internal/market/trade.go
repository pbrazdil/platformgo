package market

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/upcomers-org/platformgo/internal/decimal"
)

// TradeTick represents one market trade.
type TradeTick struct {
	InstrumentID  InstrumentID     `json:"instrument_id"`
	Price         decimal.Price    `json:"price"`
	Size          decimal.Quantity `json:"size"`
	AggressorSide AggressorSide    `json:"aggressor_side"`
	TradeID       TradeID          `json:"trade_id"`
	TsEvent       UnixNanos        `json:"ts_event"`
	TsInit        UnixNanos        `json:"ts_init"`
}

// NewTradeTick requires a strictly positive size.
func NewTradeTick(
	instrumentID InstrumentID,
	price decimal.Price,
	size decimal.Quantity,
	aggressorSide AggressorSide,
	tradeID TradeID,
	tsEvent, tsInit UnixNanos,
) (TradeTick, error) {
	if !size.IsPositive() {
		return TradeTick{}, fmt.Errorf("invalid `Quantity` for 'size' not positive, was %s", size)
	}
	return TradeTick{
		InstrumentID:  instrumentID,
		Price:         price,
		Size:          size,
		AggressorSide: aggressorSide,
		TradeID:       tradeID,
		TsEvent:       tsEvent,
		TsInit:        tsInit,
	}, nil
}

// MustTradeTick is NewTradeTick for statically valid source-derived fixtures.
func MustTradeTick(
	instrumentID InstrumentID,
	price decimal.Price,
	size decimal.Quantity,
	aggressorSide AggressorSide,
	tradeID TradeID,
	tsEvent, tsInit UnixNanos,
) TradeTick {
	trade, err := NewTradeTick(instrumentID, price, size, aggressorSide, tradeID, tsEvent, tsInit)
	if err != nil {
		panic(err)
	}
	return trade
}

func TradeMetadata(instrumentID InstrumentID, pricePrecision, sizePrecision uint8) map[string]string {
	return metadata(instrumentID, pricePrecision, sizePrecision)
}

func TradeFields() []Field {
	return []Field{
		{Name: "price", Type: fixedSizeBinary},
		{Name: "size", Type: fixedSizeBinary},
		{Name: "aggressor_side", Type: "UInt8"},
		{Name: "trade_id", Type: "Utf8"},
		{Name: "ts_event", Type: "UInt64"},
		{Name: "ts_init", Type: "UInt64"},
	}
}

func (t TradeTick) Equal(other TradeTick) bool {
	return t.InstrumentID == other.InstrumentID &&
		t.Price.Equal(other.Price) &&
		t.Size.Equal(other.Size) &&
		t.AggressorSide == other.AggressorSide &&
		t.TradeID == other.TradeID &&
		t.TsEvent == other.TsEvent &&
		t.TsInit == other.TsInit
}

func (t TradeTick) Hash64() uint64 {
	return hashStrings(
		string(t.InstrumentID), t.Price.Decimal().Normalize().String(), t.Size.Decimal().Normalize().String(),
		t.AggressorSide.String(), string(t.TradeID),
		fmt.Sprint(t.TsEvent), fmt.Sprint(t.TsInit),
	)
}

func (t TradeTick) String() string {
	return fmt.Sprintf("%s,%s,%s,%s,%s,%d",
		t.InstrumentID, t.Price, t.Size, t.AggressorSide, t.TradeID, t.TsEvent)
}

func (t TradeTick) DebugString() string {
	side := map[AggressorSide]string{
		NoAggressor: "NoAggressor",
		Buyer:       "Buyer",
		Seller:      "Seller",
	}[t.AggressorSide]
	return fmt.Sprintf(
		"TradeTick { instrument_id: %s, price: %s, size: %s, aggressor_side: %s, trade_id: %s, ts_event: %d, ts_init: %d }",
		t.InstrumentID, t.Price, t.Size, side, t.TradeID, t.TsEvent, t.TsInit,
	)
}

func (t TradeTick) MarshalJSON() ([]byte, error) {
	type wire TradeTick
	return json.Marshal(struct {
		Type string `json:"type"`
		wire
	}{Type: "TradeTick", wire: wire(t)})
}

func (t *TradeTick) UnmarshalJSON(data []byte) error {
	if t == nil {
		return errors.New("cannot unmarshal TradeTick into nil receiver")
	}
	type wire TradeTick
	var decoded struct {
		Type string `json:"type"`
		wire
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	if decoded.Type != "" && decoded.Type != "TradeTick" {
		return fmt.Errorf("invalid market data type %q", decoded.Type)
	}
	*t = TradeTick(decoded.wire)
	return nil
}
