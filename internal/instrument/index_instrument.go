package instrument

import (
	"encoding/json"

	"github.com/upcomers-org/platformgo/internal/currency"
	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
)

type IndexInstrumentConfig struct {
	InstrumentID                  ids.InstrumentID
	RawSymbol                     ids.Symbol
	Currency                      currency.Currency
	PricePrecision, SizePrecision uint8
	PriceIncrement                decimal.Price
	SizeIncrement                 decimal.Quantity
	TickScheme                    *string
	Info                          map[string]any
	TsEvent, TsInit               uint64
}

type IndexInstrument struct {
	InstrumentID                  ids.InstrumentID
	RawSymbol                     ids.Symbol
	Currency                      currency.Currency
	PricePrecision, SizePrecision uint8
	PriceIncrement                decimal.Price
	SizeIncrement                 decimal.Quantity
	TickScheme                    *string
	Info                          map[string]any
	TsEvent, TsInit               uint64
}

func NewCheckedIndexInstrument(config IndexInstrumentConfig) (IndexInstrument, error) {
	if config.PricePrecision != config.PriceIncrement.Precision() {
		return IndexInstrument{}, &InstrumentValidationError{Kind: "precision_mismatch", Field: "price_increment", Message: "price precision mismatch"}
	}
	if config.SizePrecision != config.SizeIncrement.Precision() {
		return IndexInstrument{}, &InstrumentValidationError{Kind: "precision_mismatch", Field: "size_increment", Message: "size precision mismatch"}
	}
	if err := config.PriceIncrement.RequirePositive("price_increment"); err != nil {
		return IndexInstrument{}, err
	}
	if err := config.SizeIncrement.RequirePositive("size_increment"); err != nil {
		return IndexInstrument{}, err
	}
	if config.TickScheme != nil {
		if _, err := ParseTickScheme(*config.TickScheme); err != nil {
			return IndexInstrument{}, err
		}
	}
	return IndexInstrument{
		InstrumentID: config.InstrumentID, RawSymbol: config.RawSymbol, Currency: config.Currency,
		PricePrecision: config.PricePrecision, SizePrecision: config.SizePrecision,
		PriceIncrement: config.PriceIncrement, SizeIncrement: config.SizeIncrement,
		TickScheme: copyValue(config.TickScheme), Info: cloneInfo(config.Info),
		TsEvent: config.TsEvent, TsInit: config.TsInit,
	}, nil
}

func (index IndexInstrument) AssetClass() AssetClass           { return AssetClassIndex }
func (index IndexInstrument) InstrumentClass() InstrumentClass { return InstrumentClassSpot }
func (index IndexInstrument) QuoteCurrency() currency.Currency { return index.Currency }
func (index IndexInstrument) IsInverse() bool                  { return false }
func (index IndexInstrument) Equal(other IndexInstrument) bool {
	return index.InstrumentID == other.InstrumentID
}

type IndexInstrumentBuilder struct{ config IndexInstrumentConfig }

func NewIndexInstrumentBuilder() *IndexInstrumentBuilder { return &IndexInstrumentBuilder{} }
func (b *IndexInstrumentBuilder) Instrument(v ids.InstrumentID) *IndexInstrumentBuilder {
	b.config.InstrumentID = v
	return b
}
func (b *IndexInstrumentBuilder) Symbol(v ids.Symbol) *IndexInstrumentBuilder {
	b.config.RawSymbol = v
	return b
}
func (b *IndexInstrumentBuilder) DenominatedIn(v currency.Currency) *IndexInstrumentBuilder {
	b.config.Currency = v
	return b
}
func (b *IndexInstrumentBuilder) Precisions(p, s uint8) *IndexInstrumentBuilder {
	b.config.PricePrecision = p
	b.config.SizePrecision = s
	return b
}
func (b *IndexInstrumentBuilder) Increments(p decimal.Price, s decimal.Quantity) *IndexInstrumentBuilder {
	b.config.PriceIncrement = p
	b.config.SizeIncrement = s
	return b
}
func (b *IndexInstrumentBuilder) Timestamps(e, i uint64) *IndexInstrumentBuilder {
	b.config.TsEvent = e
	b.config.TsInit = i
	return b
}
func (b *IndexInstrumentBuilder) Build() (IndexInstrument, error) {
	return NewCheckedIndexInstrument(b.config)
}

type indexInstrumentWire struct {
	InstrumentID                  ids.InstrumentID
	RawSymbol                     ids.Symbol
	Currency                      cryptoCurrencyWire
	PricePrecision, SizePrecision uint8
	PriceIncrement                decimal.Price
	SizeIncrement                 decimal.Quantity
	TickScheme                    *string
	Info                          map[string]any
	TsEvent, TsInit               uint64
}

func (index IndexInstrument) MarshalJSON() ([]byte, error) {
	return json.Marshal(indexInstrumentWire{index.InstrumentID, index.RawSymbol, cryptoCurrency(index.Currency), index.PricePrecision, index.SizePrecision, index.PriceIncrement, index.SizeIncrement, index.TickScheme, index.Info, index.TsEvent, index.TsInit})
}
func (index *IndexInstrument) UnmarshalJSON(data []byte) error {
	var wire indexInstrumentWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	denomination, err := currencyFromCrypto(wire.Currency)
	if err != nil {
		return err
	}
	*index = IndexInstrument{wire.InstrumentID, wire.RawSymbol, denomination, wire.PricePrecision, wire.SizePrecision, wire.PriceIncrement, wire.SizeIncrement, copyValue(wire.TickScheme), cloneInfo(wire.Info), wire.TsEvent, wire.TsInit}
	return nil
}
