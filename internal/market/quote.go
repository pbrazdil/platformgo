package market

import (
	"fmt"

	"github.com/upcomers-org/platformgo/internal/decimal"
)

// QuoteTick represents one top-of-book state.
type QuoteTick struct {
	InstrumentID InstrumentID     `json:"instrument_id"`
	BidPrice     decimal.Price    `json:"bid_price"`
	AskPrice     decimal.Price    `json:"ask_price"`
	BidSize      decimal.Quantity `json:"bid_size"`
	AskSize      decimal.Quantity `json:"ask_size"`
	TsEvent      UnixNanos        `json:"ts_event"`
	TsInit       UnixNanos        `json:"ts_init"`
}

// NewQuoteTick validates matching bid/ask precision and returns a quote.
func NewQuoteTick(
	instrumentID InstrumentID,
	bidPrice, askPrice decimal.Price,
	bidSize, askSize decimal.Quantity,
	tsEvent, tsInit UnixNanos,
) (QuoteTick, error) {
	if bidPrice.Precision() != askPrice.Precision() {
		return QuoteTick{}, fmt.Errorf(
			"'bid_price.precision' u8 of %d was not equal to 'ask_price.precision' u8 of %d",
			bidPrice.Precision(), askPrice.Precision(),
		)
	}
	if bidSize.Precision() != askSize.Precision() {
		return QuoteTick{}, fmt.Errorf(
			"'bid_size.precision' u8 of %d was not equal to 'ask_size.precision' u8 of %d",
			bidSize.Precision(), askSize.Precision(),
		)
	}
	return QuoteTick{
		InstrumentID: instrumentID,
		BidPrice:     bidPrice,
		AskPrice:     askPrice,
		BidSize:      bidSize,
		AskSize:      askSize,
		TsEvent:      tsEvent,
		TsInit:       tsInit,
	}, nil
}

// MustQuoteTick is NewQuoteTick for statically valid source-derived fixtures.
func MustQuoteTick(
	instrumentID InstrumentID,
	bidPrice, askPrice decimal.Price,
	bidSize, askSize decimal.Quantity,
	tsEvent, tsInit UnixNanos,
) QuoteTick {
	quote, err := NewQuoteTick(instrumentID, bidPrice, askPrice, bidSize, askSize, tsEvent, tsInit)
	if err != nil {
		panic(err)
	}
	return quote
}

func QuoteMetadata(instrumentID InstrumentID, pricePrecision, sizePrecision uint8) map[string]string {
	return metadata(instrumentID, pricePrecision, sizePrecision)
}

func QuoteFields() []Field {
	return []Field{
		{Name: "bid_price", Type: fixedSizeBinary},
		{Name: "ask_price", Type: fixedSizeBinary},
		{Name: "bid_size", Type: fixedSizeBinary},
		{Name: "ask_size", Type: fixedSizeBinary},
		{Name: "ts_event", Type: "UInt64"},
		{Name: "ts_init", Type: "UInt64"},
	}
}

func (q QuoteTick) ExtractPrice(priceType PriceType) (decimal.Price, error) {
	switch priceType {
	case PriceTypeBid:
		return q.BidPrice, nil
	case PriceTypeAsk:
		return q.AskPrice, nil
	case PriceTypeMid:
		return midpointPrice(q.BidPrice, q.AskPrice), nil
	default:
		return decimal.Price{}, fmt.Errorf("Cannot extract price from quote with price type %s", priceType)
	}
}

func (q QuoteTick) ExtractSize(priceType PriceType) (decimal.Quantity, error) {
	switch priceType {
	case PriceTypeBid:
		return q.BidSize, nil
	case PriceTypeAsk:
		return q.AskSize, nil
	case PriceTypeMid:
		return midpointQuantity(q.BidSize, q.AskSize), nil
	default:
		return decimal.Quantity{}, fmt.Errorf("Cannot extract size from quote with price type %s", priceType)
	}
}

func (q QuoteTick) String() string {
	return fmt.Sprintf("%s,%s,%s,%s,%s,%d",
		q.InstrumentID, q.BidPrice, q.AskPrice, q.BidSize, q.AskSize, q.TsEvent)
}
