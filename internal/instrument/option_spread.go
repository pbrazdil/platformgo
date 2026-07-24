package instrument

import (
	"encoding/json"
	"fmt"

	"github.com/upcomers-org/platformgo/internal/currency"
	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
)

type OptionSpreadConfig struct {
	InstrumentID                                ids.InstrumentID
	RawSymbol                                   ids.Symbol
	AssetClass                                  AssetClass
	Exchange                                    *string
	Underlying, StrategyType                    string
	Activation, Expiration                      uint64
	Currency                                    currency.Currency
	PricePrecision                              uint8
	PriceIncrement                              decimal.Price
	Multiplier, LotSize                         decimal.Quantity
	MaxQuantity, MinQuantity                    *decimal.Quantity
	MaxPrice, MinPrice                          *decimal.Price
	MarginInit, MarginMaint, MakerFee, TakerFee *decimal.Decimal
	TickScheme                                  *string
	Info                                        map[string]any
	TsEvent, TsInit                             uint64
}

type OptionSpread struct {
	InstrumentID                                ids.InstrumentID
	RawSymbol                                   ids.Symbol
	AssetClass                                  AssetClass
	Exchange                                    *string
	Underlying, StrategyType                    string
	Activation, Expiration                      uint64
	Currency                                    currency.Currency
	PricePrecision, SizePrecision               uint8
	PriceIncrement                              decimal.Price
	SizeIncrement, Multiplier, LotSize          decimal.Quantity
	MarginInit, MarginMaint, MakerFee, TakerFee decimal.Decimal
	MaxQuantity, MinQuantity                    *decimal.Quantity
	MaxPrice, MinPrice                          *decimal.Price
	TickScheme                                  *string
	Info                                        map[string]any
	TsEvent, TsInit                             uint64
}

func NewCheckedOptionSpread(config OptionSpreadConfig) (OptionSpread, error) {
	if config.Exchange != nil {
		if err := validateASCIIText(*config.Exchange, "exchange"); err != nil {
			return OptionSpread{}, err
		}
	}
	if err := validateASCIIText(config.StrategyType, "strategy_type"); err != nil {
		return OptionSpread{}, err
	}
	if config.PricePrecision != config.PriceIncrement.Precision() {
		return OptionSpread{}, fmt.Errorf(
			"price_precision %d was not equal to price_increment.precision %d",
			config.PricePrecision,
			config.PriceIncrement.Precision(),
		)
	}
	if err := config.PriceIncrement.RequirePositive("price_increment"); err != nil {
		return OptionSpread{}, err
	}
	if config.TickScheme != nil {
		if _, err := ParseTickScheme(*config.TickScheme); err != nil {
			return OptionSpread{}, err
		}
	}
	if err := config.Multiplier.RequirePositive("multiplier"); err != nil {
		return OptionSpread{}, err
	}
	if err := config.LotSize.RequirePositive("lot_size"); err != nil {
		return OptionSpread{}, err
	}
	minQuantity := copyValue(config.MinQuantity)
	if minQuantity == nil {
		value := decimal.MustQuantity("1")
		minQuantity = &value
	}
	return OptionSpread{
		InstrumentID: config.InstrumentID, RawSymbol: config.RawSymbol,
		AssetClass: config.AssetClass, Exchange: copyValue(config.Exchange),
		Underlying: config.Underlying, StrategyType: config.StrategyType,
		Activation: config.Activation, Expiration: config.Expiration, Currency: config.Currency,
		PricePrecision: config.PricePrecision, PriceIncrement: config.PriceIncrement,
		SizePrecision: 0, SizeIncrement: decimal.MustQuantity("1"),
		Multiplier: config.Multiplier, LotSize: config.LotSize,
		MarginInit: decimalValue(config.MarginInit), MarginMaint: decimalValue(config.MarginMaint),
		MakerFee: decimalValue(config.MakerFee), TakerFee: decimalValue(config.TakerFee),
		MaxQuantity: copyValue(config.MaxQuantity), MinQuantity: minQuantity,
		MaxPrice: copyValue(config.MaxPrice), MinPrice: copyValue(config.MinPrice),
		TickScheme: copyValue(config.TickScheme), Info: cloneInfo(config.Info),
		TsEvent: config.TsEvent, TsInit: config.TsInit,
	}, nil
}

func (spread OptionSpread) InstrumentClass() InstrumentClass {
	return InstrumentClassOptionSpread
}
func (spread OptionSpread) QuoteCurrency() currency.Currency { return spread.Currency }
func (spread OptionSpread) SettlementCurrency() currency.Currency {
	return spread.Currency
}
func (spread OptionSpread) IsInverse() bool                  { return false }
func (spread OptionSpread) BaseCurrency() *currency.Currency { return nil }
func (spread OptionSpread) UnderlyingValue() *string {
	value := spread.Underlying
	return &value
}
func (spread OptionSpread) StrategyTypeValue() *string {
	value := spread.StrategyType
	return &value
}
func (spread OptionSpread) ActivationNanosValue() *uint64 {
	value := spread.Activation
	return &value
}
func (spread OptionSpread) ExpirationNanosValue() *uint64 {
	value := spread.Expiration
	return &value
}
func (spread OptionSpread) Equal(other OptionSpread) bool {
	return spread.InstrumentID == other.InstrumentID
}

type optionSpreadWire struct {
	InstrumentID                                ids.InstrumentID
	RawSymbol                                   ids.Symbol
	AssetClass                                  AssetClass
	Exchange                                    *string
	Underlying, StrategyType                    string
	Activation, Expiration                      uint64
	Currency                                    cryptoCurrencyWire
	PricePrecision, SizePrecision               uint8
	PriceIncrement                              decimal.Price
	SizeIncrement, Multiplier, LotSize          decimal.Quantity
	MaxQuantity, MinQuantity                    *decimal.Quantity
	MaxPrice, MinPrice                          *decimal.Price
	MarginInit, MarginMaint, MakerFee, TakerFee string
	TickScheme                                  *string
	Info                                        map[string]any
	TsEvent, TsInit                             uint64
}

func (spread OptionSpread) MarshalJSON() ([]byte, error) {
	return json.Marshal(optionSpreadWire{
		InstrumentID: spread.InstrumentID, RawSymbol: spread.RawSymbol,
		AssetClass: spread.AssetClass, Exchange: spread.Exchange,
		Underlying: spread.Underlying, StrategyType: spread.StrategyType,
		Activation: spread.Activation, Expiration: spread.Expiration,
		Currency: cryptoCurrency(spread.Currency), PricePrecision: spread.PricePrecision,
		PriceIncrement: spread.PriceIncrement, SizePrecision: spread.SizePrecision,
		SizeIncrement: spread.SizeIncrement, Multiplier: spread.Multiplier, LotSize: spread.LotSize,
		MaxQuantity: spread.MaxQuantity, MinQuantity: spread.MinQuantity,
		MaxPrice: spread.MaxPrice, MinPrice: spread.MinPrice,
		MarginInit: spread.MarginInit.String(), MarginMaint: spread.MarginMaint.String(),
		MakerFee: spread.MakerFee.String(), TakerFee: spread.TakerFee.String(),
		TickScheme: spread.TickScheme, Info: spread.Info,
		TsEvent: spread.TsEvent, TsInit: spread.TsInit,
	})
}

func (spread *OptionSpread) UnmarshalJSON(data []byte) error {
	var wire optionSpreadWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	denomination, err := currencyFromCrypto(wire.Currency)
	if err != nil {
		return err
	}
	*spread = OptionSpread{
		InstrumentID: wire.InstrumentID, RawSymbol: wire.RawSymbol,
		AssetClass: wire.AssetClass, Exchange: copyValue(wire.Exchange),
		Underlying: wire.Underlying, StrategyType: wire.StrategyType,
		Activation: wire.Activation, Expiration: wire.Expiration, Currency: denomination,
		PricePrecision: wire.PricePrecision, PriceIncrement: wire.PriceIncrement,
		SizePrecision: wire.SizePrecision, SizeIncrement: wire.SizeIncrement,
		Multiplier: wire.Multiplier, LotSize: wire.LotSize,
		MarginInit: decimal.MustParse(wire.MarginInit), MarginMaint: decimal.MustParse(wire.MarginMaint),
		MakerFee: decimal.MustParse(wire.MakerFee), TakerFee: decimal.MustParse(wire.TakerFee),
		MaxQuantity: copyValue(wire.MaxQuantity), MinQuantity: copyValue(wire.MinQuantity),
		MaxPrice: copyValue(wire.MaxPrice), MinPrice: copyValue(wire.MinPrice),
		TickScheme: copyValue(wire.TickScheme), Info: cloneInfo(wire.Info),
		TsEvent: wire.TsEvent, TsInit: wire.TsInit,
	}
	return nil
}

type OptionSpreadBuilder struct{ config OptionSpreadConfig }

func NewOptionSpreadBuilder() *OptionSpreadBuilder { return &OptionSpreadBuilder{} }
func (builder *OptionSpreadBuilder) Instrument(value ids.InstrumentID) *OptionSpreadBuilder {
	builder.config.InstrumentID = value
	return builder
}
func (builder *OptionSpreadBuilder) Symbol(value ids.Symbol) *OptionSpreadBuilder {
	builder.config.RawSymbol = value
	return builder
}
func (builder *OptionSpreadBuilder) Class(value AssetClass) *OptionSpreadBuilder {
	builder.config.AssetClass = value
	return builder
}
func (builder *OptionSpreadBuilder) OnExchange(value string) *OptionSpreadBuilder {
	builder.config.Exchange = &value
	return builder
}
func (builder *OptionSpreadBuilder) ForUnderlying(value string) *OptionSpreadBuilder {
	builder.config.Underlying = value
	return builder
}
func (builder *OptionSpreadBuilder) WithStrategy(value string) *OptionSpreadBuilder {
	builder.config.StrategyType = value
	return builder
}
func (builder *OptionSpreadBuilder) ActiveBetween(activation, expiration uint64) *OptionSpreadBuilder {
	builder.config.Activation, builder.config.Expiration = activation, expiration
	return builder
}
func (builder *OptionSpreadBuilder) DenominatedIn(value currency.Currency) *OptionSpreadBuilder {
	builder.config.Currency = value
	return builder
}
func (builder *OptionSpreadBuilder) Price(value uint8, increment decimal.Price) *OptionSpreadBuilder {
	builder.config.PricePrecision, builder.config.PriceIncrement = value, increment
	return builder
}
func (builder *OptionSpreadBuilder) Sizing(multiplier, lotSize decimal.Quantity) *OptionSpreadBuilder {
	builder.config.Multiplier, builder.config.LotSize = multiplier, lotSize
	return builder
}
func (builder *OptionSpreadBuilder) QuantityLimits(maximum, minimum decimal.Quantity) *OptionSpreadBuilder {
	builder.config.MaxQuantity, builder.config.MinQuantity = &maximum, &minimum
	return builder
}
func (builder *OptionSpreadBuilder) PriceLimits(maximum, minimum decimal.Price) *OptionSpreadBuilder {
	builder.config.MaxPrice, builder.config.MinPrice = &maximum, &minimum
	return builder
}
func (builder *OptionSpreadBuilder) Margins(initial, maintenance decimal.Decimal) *OptionSpreadBuilder {
	builder.config.MarginInit, builder.config.MarginMaint = &initial, &maintenance
	return builder
}
func (builder *OptionSpreadBuilder) Fees(maker, taker decimal.Decimal) *OptionSpreadBuilder {
	builder.config.MakerFee, builder.config.TakerFee = &maker, &taker
	return builder
}
func (builder *OptionSpreadBuilder) Timestamps(event, init uint64) *OptionSpreadBuilder {
	builder.config.TsEvent, builder.config.TsInit = event, init
	return builder
}
func (builder *OptionSpreadBuilder) Build() (OptionSpread, error) {
	return NewCheckedOptionSpread(builder.config)
}
