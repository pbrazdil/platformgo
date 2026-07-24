package instrument

import (
	"encoding/json"
	"fmt"

	"github.com/upcomers-org/platformgo/internal/currency"
	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
)

type FuturesSpreadConfig struct {
	InstrumentID   ids.InstrumentID
	RawSymbol      ids.Symbol
	AssetClass     AssetClass
	Exchange       *string
	Underlying     string
	StrategyType   string
	Activation     uint64
	Expiration     uint64
	Currency       currency.Currency
	PricePrecision uint8
	PriceIncrement decimal.Price
	Multiplier     decimal.Quantity
	LotSize        decimal.Quantity
	MaxQuantity    *decimal.Quantity
	MinQuantity    *decimal.Quantity
	MaxPrice       *decimal.Price
	MinPrice       *decimal.Price
	MarginInit     *decimal.Decimal
	MarginMaint    *decimal.Decimal
	MakerFee       *decimal.Decimal
	TakerFee       *decimal.Decimal
	TickScheme     *string
	Info           map[string]any
	TsEvent        uint64
	TsInit         uint64
}

type FuturesSpread struct {
	InstrumentID   ids.InstrumentID
	RawSymbol      ids.Symbol
	AssetClass     AssetClass
	Exchange       *string
	Underlying     string
	StrategyType   string
	Activation     uint64
	Expiration     uint64
	Currency       currency.Currency
	PricePrecision uint8
	PriceIncrement decimal.Price
	SizePrecision  uint8
	SizeIncrement  decimal.Quantity
	Multiplier     decimal.Quantity
	LotSize        decimal.Quantity
	MaxQuantity    *decimal.Quantity
	MinQuantity    *decimal.Quantity
	MaxPrice       *decimal.Price
	MinPrice       *decimal.Price
	MarginInit     decimal.Decimal
	MarginMaint    decimal.Decimal
	MakerFee       decimal.Decimal
	TakerFee       decimal.Decimal
	TickScheme     *string
	Info           map[string]any
	TsEvent        uint64
	TsInit         uint64
}

func NewCheckedFuturesSpread(config FuturesSpreadConfig) (FuturesSpread, error) {
	if config.Exchange != nil {
		if err := validateASCIIText(*config.Exchange, "exchange"); err != nil {
			return FuturesSpread{}, err
		}
	}
	if err := validateASCIIText(config.StrategyType, "strategy_type"); err != nil {
		return FuturesSpread{}, err
	}
	if config.PricePrecision != config.PriceIncrement.Precision() {
		return FuturesSpread{}, fmt.Errorf(
			"price_precision %d was not equal to price_increment.precision %d",
			config.PricePrecision,
			config.PriceIncrement.Precision(),
		)
	}
	if err := config.PriceIncrement.RequirePositive("price_increment"); err != nil {
		return FuturesSpread{}, err
	}
	if config.TickScheme != nil {
		if _, err := ParseTickScheme(*config.TickScheme); err != nil {
			return FuturesSpread{}, err
		}
	}
	if err := config.Multiplier.RequirePositive("multiplier"); err != nil {
		return FuturesSpread{}, err
	}
	if err := config.LotSize.RequirePositive("lot_size"); err != nil {
		return FuturesSpread{}, err
	}
	minQuantity := config.MinQuantity
	if minQuantity == nil {
		value := decimal.MustQuantity("1")
		minQuantity = &value
	}
	return FuturesSpread{
		InstrumentID: config.InstrumentID, RawSymbol: config.RawSymbol,
		AssetClass: config.AssetClass, Exchange: copyValue(config.Exchange),
		Underlying: config.Underlying, StrategyType: config.StrategyType,
		Activation: config.Activation, Expiration: config.Expiration, Currency: config.Currency,
		PricePrecision: config.PricePrecision, PriceIncrement: config.PriceIncrement,
		SizePrecision: 0, SizeIncrement: decimal.MustQuantity("1"),
		Multiplier: config.Multiplier, LotSize: config.LotSize,
		MaxQuantity: copyValue(config.MaxQuantity), MinQuantity: copyValue(minQuantity),
		MaxPrice: copyValue(config.MaxPrice), MinPrice: copyValue(config.MinPrice),
		MarginInit: decimalValue(config.MarginInit), MarginMaint: decimalValue(config.MarginMaint),
		MakerFee: decimalValue(config.MakerFee), TakerFee: decimalValue(config.TakerFee),
		TickScheme: copyValue(config.TickScheme), Info: cloneInfo(config.Info),
		TsEvent: config.TsEvent, TsInit: config.TsInit,
	}, nil
}

func (spread FuturesSpread) InstrumentClass() InstrumentClass {
	return InstrumentClassFuturesSpread
}
func (spread FuturesSpread) QuoteCurrency() currency.Currency { return spread.Currency }
func (spread FuturesSpread) SettlementCurrency() currency.Currency {
	return spread.Currency
}
func (spread FuturesSpread) IsInverse() bool                  { return false }
func (spread FuturesSpread) BaseCurrency() *currency.Currency { return nil }
func (spread FuturesSpread) UnderlyingValue() *string {
	value := spread.Underlying
	return &value
}
func (spread FuturesSpread) StrategyTypeValue() *string {
	value := spread.StrategyType
	return &value
}
func (spread FuturesSpread) ActivationNanosValue() *uint64 {
	value := spread.Activation
	return &value
}
func (spread FuturesSpread) ExpirationNanosValue() *uint64 {
	value := spread.Expiration
	return &value
}
func (spread FuturesSpread) Equal(other FuturesSpread) bool {
	return spread.InstrumentID == other.InstrumentID
}
func (spread FuturesSpread) String() string {
	return fmt.Sprintf(
		"FuturesSpread(id=%s, raw_symbol=%s, underlying=%s, strategy_type=%s, expiration=%d)",
		spread.InstrumentID, spread.RawSymbol, spread.Underlying, spread.StrategyType, spread.Expiration,
	)
}

type FuturesSpreadBuilder struct{ config FuturesSpreadConfig }

func NewFuturesSpreadBuilder() *FuturesSpreadBuilder { return &FuturesSpreadBuilder{} }
func (builder *FuturesSpreadBuilder) Instrument(value ids.InstrumentID) *FuturesSpreadBuilder {
	builder.config.InstrumentID = value
	return builder
}
func (builder *FuturesSpreadBuilder) Symbol(value ids.Symbol) *FuturesSpreadBuilder {
	builder.config.RawSymbol = value
	return builder
}
func (builder *FuturesSpreadBuilder) Class(value AssetClass) *FuturesSpreadBuilder {
	builder.config.AssetClass = value
	return builder
}
func (builder *FuturesSpreadBuilder) OnExchange(value string) *FuturesSpreadBuilder {
	builder.config.Exchange = &value
	return builder
}
func (builder *FuturesSpreadBuilder) ForUnderlying(value string) *FuturesSpreadBuilder {
	builder.config.Underlying = value
	return builder
}
func (builder *FuturesSpreadBuilder) WithStrategy(value string) *FuturesSpreadBuilder {
	builder.config.StrategyType = value
	return builder
}
func (builder *FuturesSpreadBuilder) ActiveBetween(activation, expiration uint64) *FuturesSpreadBuilder {
	builder.config.Activation, builder.config.Expiration = activation, expiration
	return builder
}
func (builder *FuturesSpreadBuilder) DenominatedIn(value currency.Currency) *FuturesSpreadBuilder {
	builder.config.Currency = value
	return builder
}
func (builder *FuturesSpreadBuilder) PriceDigits(value uint8) *FuturesSpreadBuilder {
	builder.config.PricePrecision = value
	return builder
}
func (builder *FuturesSpreadBuilder) TickSize(value decimal.Price) *FuturesSpreadBuilder {
	builder.config.PriceIncrement = value
	return builder
}
func (builder *FuturesSpreadBuilder) WithMultiplier(value decimal.Quantity) *FuturesSpreadBuilder {
	builder.config.Multiplier = value
	return builder
}
func (builder *FuturesSpreadBuilder) WithLotSize(value decimal.Quantity) *FuturesSpreadBuilder {
	builder.config.LotSize = value
	return builder
}
func (builder *FuturesSpreadBuilder) WithMaxQuantity(value decimal.Quantity) *FuturesSpreadBuilder {
	builder.config.MaxQuantity = &value
	return builder
}
func (builder *FuturesSpreadBuilder) WithMinQuantity(value decimal.Quantity) *FuturesSpreadBuilder {
	builder.config.MinQuantity = &value
	return builder
}
func (builder *FuturesSpreadBuilder) WithMaxPrice(value decimal.Price) *FuturesSpreadBuilder {
	builder.config.MaxPrice = &value
	return builder
}
func (builder *FuturesSpreadBuilder) WithMinPrice(value decimal.Price) *FuturesSpreadBuilder {
	builder.config.MinPrice = &value
	return builder
}
func (builder *FuturesSpreadBuilder) Margins(initial, maintenance decimal.Decimal) *FuturesSpreadBuilder {
	builder.config.MarginInit, builder.config.MarginMaint = &initial, &maintenance
	return builder
}
func (builder *FuturesSpreadBuilder) Fees(maker, taker decimal.Decimal) *FuturesSpreadBuilder {
	builder.config.MakerFee, builder.config.TakerFee = &maker, &taker
	return builder
}
func (builder *FuturesSpreadBuilder) Timestamps(event, init uint64) *FuturesSpreadBuilder {
	builder.config.TsEvent, builder.config.TsInit = event, init
	return builder
}
func (builder *FuturesSpreadBuilder) Build() (FuturesSpread, error) {
	return NewCheckedFuturesSpread(builder.config)
}

type futuresSpreadWire struct {
	InstrumentID                                ids.InstrumentID
	RawSymbol                                   ids.Symbol
	AssetClass                                  AssetClass
	Exchange                                    *string
	Underlying, StrategyType                    string
	Activation, Expiration                      uint64
	Currency                                    cryptoCurrencyWire
	PricePrecision                              uint8
	PriceIncrement                              decimal.Price
	SizePrecision                               uint8
	SizeIncrement, Multiplier, LotSize          decimal.Quantity
	MaxQuantity, MinQuantity                    *decimal.Quantity
	MaxPrice, MinPrice                          *decimal.Price
	MarginInit, MarginMaint, MakerFee, TakerFee string
	TickScheme                                  *string
	Info                                        map[string]any
	TsEvent, TsInit                             uint64
}

func (spread FuturesSpread) MarshalJSON() ([]byte, error) {
	return json.Marshal(futuresSpreadWire{
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
		TickScheme: spread.TickScheme, Info: spread.Info, TsEvent: spread.TsEvent, TsInit: spread.TsInit,
	})
}

func (spread *FuturesSpread) UnmarshalJSON(data []byte) error {
	var wire futuresSpreadWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	denomination, err := currencyFromCrypto(wire.Currency)
	if err != nil {
		return err
	}
	*spread = FuturesSpread{
		InstrumentID: wire.InstrumentID, RawSymbol: wire.RawSymbol, AssetClass: wire.AssetClass,
		Exchange: copyValue(wire.Exchange), Underlying: wire.Underlying, StrategyType: wire.StrategyType,
		Activation: wire.Activation, Expiration: wire.Expiration, Currency: denomination,
		PricePrecision: wire.PricePrecision, PriceIncrement: wire.PriceIncrement,
		SizePrecision: wire.SizePrecision, SizeIncrement: wire.SizeIncrement,
		Multiplier: wire.Multiplier, LotSize: wire.LotSize,
		MaxQuantity: copyValue(wire.MaxQuantity), MinQuantity: copyValue(wire.MinQuantity),
		MaxPrice: copyValue(wire.MaxPrice), MinPrice: copyValue(wire.MinPrice),
		MarginInit: decimal.MustParse(wire.MarginInit), MarginMaint: decimal.MustParse(wire.MarginMaint),
		MakerFee: decimal.MustParse(wire.MakerFee), TakerFee: decimal.MustParse(wire.TakerFee),
		TickScheme: copyValue(wire.TickScheme), Info: cloneInfo(wire.Info),
		TsEvent: wire.TsEvent, TsInit: wire.TsInit,
	}
	return nil
}
