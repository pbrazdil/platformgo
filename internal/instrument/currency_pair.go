package instrument

import (
	"encoding/json"
	"fmt"

	"github.com/upcomers-org/platformgo/internal/currency"
	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
	"github.com/upcomers-org/platformgo/internal/money"
)

type CurrencyPairConfig struct {
	InstrumentID                  ids.InstrumentID
	RawSymbol                     ids.Symbol
	BaseCurrency, QuoteCurrency   currency.Currency
	PricePrecision, SizePrecision uint8
	PriceIncrement                decimal.Price
	SizeIncrement                 decimal.Quantity
	Multiplier, LotSize           *decimal.Quantity
	MaxQuantity, MinQuantity      *decimal.Quantity
	MaxNotional, MinNotional      *money.Money
	MaxPrice, MinPrice            *decimal.Price
	MarginInit, MarginMaint       *decimal.Decimal
	MakerFee, TakerFee            *decimal.Decimal
	TickScheme                    *string
	Info                          map[string]any
	TsEvent, TsInit               uint64
}

type CurrencyPair struct {
	InstrumentID                  ids.InstrumentID
	RawSymbol                     ids.Symbol
	BaseCurrency, QuoteCurrency   currency.Currency
	PricePrecision, SizePrecision uint8
	PriceIncrement                decimal.Price
	SizeIncrement                 decimal.Quantity
	Multiplier                    decimal.Quantity
	LotSize                       *decimal.Quantity
	MarginInit, MarginMaint       decimal.Decimal
	MakerFee, TakerFee            decimal.Decimal
	MaxQuantity, MinQuantity      *decimal.Quantity
	MaxNotional, MinNotional      *money.Money
	MaxPrice, MinPrice            *decimal.Price
	TickScheme                    *string
	Info                          map[string]any
	TsEvent, TsInit               uint64
}

func NewCheckedCurrencyPair(config CurrencyPairConfig) (CurrencyPair, error) {
	if config.PricePrecision != config.PriceIncrement.Precision() {
		return CurrencyPair{}, fmt.Errorf(
			"price_precision %d was not equal to price_increment.precision %d",
			config.PricePrecision,
			config.PriceIncrement.Precision(),
		)
	}
	if config.SizePrecision != config.SizeIncrement.Precision() {
		return CurrencyPair{}, fmt.Errorf(
			"size_precision %d was not equal to size_increment.precision %d",
			config.SizePrecision,
			config.SizeIncrement.Precision(),
		)
	}
	if err := config.PriceIncrement.RequirePositive("price_increment"); err != nil {
		return CurrencyPair{}, err
	}
	if err := config.SizeIncrement.RequirePositive("size_increment"); err != nil {
		return CurrencyPair{}, err
	}
	if config.TickScheme != nil {
		if _, err := ParseTickScheme(*config.TickScheme); err != nil {
			return CurrencyPair{}, err
		}
	}
	if config.Multiplier != nil {
		if err := config.Multiplier.RequirePositive("multiplier"); err != nil {
			return CurrencyPair{}, err
		}
	}
	if config.LotSize != nil {
		if err := config.LotSize.RequirePositive("lot_size"); err != nil {
			return CurrencyPair{}, err
		}
	}

	multiplier := decimal.MustQuantity("1")
	if config.Multiplier != nil {
		multiplier = *config.Multiplier
	}
	return CurrencyPair{
		InstrumentID: config.InstrumentID, RawSymbol: config.RawSymbol,
		BaseCurrency: config.BaseCurrency, QuoteCurrency: config.QuoteCurrency,
		PricePrecision: config.PricePrecision, SizePrecision: config.SizePrecision,
		PriceIncrement: config.PriceIncrement, SizeIncrement: config.SizeIncrement,
		Multiplier: multiplier, LotSize: copyValue(config.LotSize),
		MarginInit: decimalValue(config.MarginInit), MarginMaint: decimalValue(config.MarginMaint),
		MakerFee: decimalValue(config.MakerFee), TakerFee: decimalValue(config.TakerFee),
		MaxQuantity: copyValue(config.MaxQuantity), MinQuantity: copyValue(config.MinQuantity),
		MaxNotional: copyValue(config.MaxNotional), MinNotional: copyValue(config.MinNotional),
		MaxPrice: copyValue(config.MaxPrice), MinPrice: copyValue(config.MinPrice),
		TickScheme: copyValue(config.TickScheme), Info: cloneInfo(config.Info),
		TsEvent: config.TsEvent, TsInit: config.TsInit,
	}, nil
}

func (pair CurrencyPair) AssetClass() AssetClass {
	if pair.BaseCurrency.Type == currency.Crypto || pair.QuoteCurrency.Type == currency.Crypto {
		return AssetClassCryptocurrency
	}
	return AssetClassFX
}

func (pair CurrencyPair) InstrumentClass() InstrumentClass { return InstrumentClassSpot }
func (pair CurrencyPair) SettlementCurrency() currency.Currency {
	return pair.QuoteCurrency
}
func (pair CurrencyPair) IsInverse() bool { return false }
func (pair CurrencyPair) Equal(other CurrencyPair) bool {
	return pair.InstrumentID == other.InstrumentID
}

type currencyPairWire struct {
	InstrumentID                  ids.InstrumentID
	RawSymbol                     ids.Symbol
	BaseCurrency, QuoteCurrency   cryptoCurrencyWire
	PricePrecision, SizePrecision uint8
	PriceIncrement                decimal.Price
	SizeIncrement, Multiplier     decimal.Quantity
	LotSize                       *decimal.Quantity
	MarginInit, MarginMaint       string
	MakerFee, TakerFee            string
	MaxQuantity, MinQuantity      *decimal.Quantity
	MaxNotional, MinNotional      *cryptoMoneyWire
	MaxPrice, MinPrice            *decimal.Price
	TickScheme                    *string
	Info                          map[string]any
	TsEvent, TsInit               uint64
}

func (pair CurrencyPair) MarshalJSON() ([]byte, error) {
	return json.Marshal(currencyPairWire{
		InstrumentID: pair.InstrumentID, RawSymbol: pair.RawSymbol,
		BaseCurrency: cryptoCurrency(pair.BaseCurrency), QuoteCurrency: cryptoCurrency(pair.QuoteCurrency),
		PricePrecision: pair.PricePrecision, SizePrecision: pair.SizePrecision,
		PriceIncrement: pair.PriceIncrement, SizeIncrement: pair.SizeIncrement,
		Multiplier: pair.Multiplier, LotSize: pair.LotSize,
		MarginInit: pair.MarginInit.String(), MarginMaint: pair.MarginMaint.String(),
		MakerFee: pair.MakerFee.String(), TakerFee: pair.TakerFee.String(),
		MaxQuantity: pair.MaxQuantity, MinQuantity: pair.MinQuantity,
		MaxNotional: cryptoMoney(pair.MaxNotional), MinNotional: cryptoMoney(pair.MinNotional),
		MaxPrice: pair.MaxPrice, MinPrice: pair.MinPrice,
		TickScheme: pair.TickScheme, Info: pair.Info,
		TsEvent: pair.TsEvent, TsInit: pair.TsInit,
	})
}

func (pair *CurrencyPair) UnmarshalJSON(data []byte) error {
	var wire currencyPairWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	base, err := currencyFromCrypto(wire.BaseCurrency)
	if err != nil {
		return err
	}
	quote, err := currencyFromCrypto(wire.QuoteCurrency)
	if err != nil {
		return err
	}
	maxNotional, err := moneyFromCrypto(wire.MaxNotional)
	if err != nil {
		return err
	}
	minNotional, err := moneyFromCrypto(wire.MinNotional)
	if err != nil {
		return err
	}
	*pair = CurrencyPair{
		InstrumentID: wire.InstrumentID, RawSymbol: wire.RawSymbol,
		BaseCurrency: base, QuoteCurrency: quote,
		PricePrecision: wire.PricePrecision, SizePrecision: wire.SizePrecision,
		PriceIncrement: wire.PriceIncrement, SizeIncrement: wire.SizeIncrement,
		Multiplier: wire.Multiplier, LotSize: copyValue(wire.LotSize),
		MarginInit: decimal.MustParse(wire.MarginInit), MarginMaint: decimal.MustParse(wire.MarginMaint),
		MakerFee: decimal.MustParse(wire.MakerFee), TakerFee: decimal.MustParse(wire.TakerFee),
		MaxQuantity: copyValue(wire.MaxQuantity), MinQuantity: copyValue(wire.MinQuantity),
		MaxNotional: maxNotional, MinNotional: minNotional,
		MaxPrice: copyValue(wire.MaxPrice), MinPrice: copyValue(wire.MinPrice),
		TickScheme: copyValue(wire.TickScheme), Info: cloneInfo(wire.Info),
		TsEvent: wire.TsEvent, TsInit: wire.TsInit,
	}
	return nil
}

type CurrencyPairBuilder struct{ config CurrencyPairConfig }

func NewCurrencyPairBuilder() *CurrencyPairBuilder { return &CurrencyPairBuilder{} }
func (builder *CurrencyPairBuilder) Instrument(value ids.InstrumentID) *CurrencyPairBuilder {
	builder.config.InstrumentID = value
	return builder
}
func (builder *CurrencyPairBuilder) Symbol(value ids.Symbol) *CurrencyPairBuilder {
	builder.config.RawSymbol = value
	return builder
}
func (builder *CurrencyPairBuilder) Currencies(base, quote currency.Currency) *CurrencyPairBuilder {
	builder.config.BaseCurrency, builder.config.QuoteCurrency = base, quote
	return builder
}
func (builder *CurrencyPairBuilder) Precisions(price, size uint8) *CurrencyPairBuilder {
	builder.config.PricePrecision, builder.config.SizePrecision = price, size
	return builder
}
func (builder *CurrencyPairBuilder) Increments(price decimal.Price, size decimal.Quantity) *CurrencyPairBuilder {
	builder.config.PriceIncrement, builder.config.SizeIncrement = price, size
	return builder
}
func (builder *CurrencyPairBuilder) WithMultiplier(value decimal.Quantity) *CurrencyPairBuilder {
	builder.config.Multiplier = &value
	return builder
}
func (builder *CurrencyPairBuilder) WithLotSize(value decimal.Quantity) *CurrencyPairBuilder {
	builder.config.LotSize = &value
	return builder
}
func (builder *CurrencyPairBuilder) QuantityLimits(maximum, minimum decimal.Quantity) *CurrencyPairBuilder {
	builder.config.MaxQuantity, builder.config.MinQuantity = &maximum, &minimum
	return builder
}
func (builder *CurrencyPairBuilder) NotionalLimits(maximum, minimum money.Money) *CurrencyPairBuilder {
	builder.config.MaxNotional, builder.config.MinNotional = &maximum, &minimum
	return builder
}
func (builder *CurrencyPairBuilder) PriceLimits(maximum, minimum decimal.Price) *CurrencyPairBuilder {
	builder.config.MaxPrice, builder.config.MinPrice = &maximum, &minimum
	return builder
}
func (builder *CurrencyPairBuilder) Margins(initial, maintenance decimal.Decimal) *CurrencyPairBuilder {
	builder.config.MarginInit, builder.config.MarginMaint = &initial, &maintenance
	return builder
}
func (builder *CurrencyPairBuilder) Fees(maker, taker decimal.Decimal) *CurrencyPairBuilder {
	builder.config.MakerFee, builder.config.TakerFee = &maker, &taker
	return builder
}
func (builder *CurrencyPairBuilder) Timestamps(event, init uint64) *CurrencyPairBuilder {
	builder.config.TsEvent, builder.config.TsInit = event, init
	return builder
}
func (builder *CurrencyPairBuilder) Build() (CurrencyPair, error) {
	return NewCheckedCurrencyPair(builder.config)
}
