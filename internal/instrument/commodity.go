package instrument

import (
	"encoding/json"
	"fmt"

	"github.com/upcomers-org/platformgo/internal/currency"
	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
	"github.com/upcomers-org/platformgo/internal/money"
)

type CommodityConfig struct {
	InstrumentID                  ids.InstrumentID
	RawSymbol                     ids.Symbol
	AssetClass                    AssetClass
	QuoteCurrency                 currency.Currency
	PricePrecision, SizePrecision uint8
	PriceIncrement                decimal.Price
	SizeIncrement                 decimal.Quantity
	LotSize                       *decimal.Quantity
	MaxQuantity, MinQuantity      *decimal.Quantity
	MaxNotional, MinNotional      *money.Money
	MaxPrice, MinPrice            *decimal.Price
	MarginInit, MarginMaint       *decimal.Decimal
	MakerFee, TakerFee            *decimal.Decimal
	TickScheme                    *string
	Info                          map[string]any
	TsEvent, TsInit               uint64
}

type Commodity struct {
	InstrumentID                  ids.InstrumentID
	RawSymbol                     ids.Symbol
	AssetClassValue               AssetClass
	QuoteCurrency                 currency.Currency
	PricePrecision, SizePrecision uint8
	PriceIncrement                decimal.Price
	SizeIncrement                 decimal.Quantity
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

func NewCheckedCommodity(config CommodityConfig) (Commodity, error) {
	if config.PricePrecision != config.PriceIncrement.Precision() {
		return Commodity{}, fmt.Errorf(
			"price_precision %d was not equal to price_increment.precision %d",
			config.PricePrecision,
			config.PriceIncrement.Precision(),
		)
	}
	if config.SizePrecision != config.SizeIncrement.Precision() {
		return Commodity{}, fmt.Errorf(
			"size_precision %d was not equal to size_increment.precision %d",
			config.SizePrecision,
			config.SizeIncrement.Precision(),
		)
	}
	if err := config.PriceIncrement.RequirePositive("price_increment"); err != nil {
		return Commodity{}, err
	}
	if err := config.SizeIncrement.RequirePositive("size_increment"); err != nil {
		return Commodity{}, err
	}
	if config.TickScheme != nil {
		if _, err := ParseTickScheme(*config.TickScheme); err != nil {
			return Commodity{}, err
		}
	}
	if config.LotSize != nil {
		if err := config.LotSize.RequirePositive("lot_size"); err != nil {
			return Commodity{}, err
		}
	}

	return Commodity{
		InstrumentID: config.InstrumentID, RawSymbol: config.RawSymbol,
		AssetClassValue: config.AssetClass, QuoteCurrency: config.QuoteCurrency,
		PricePrecision: config.PricePrecision, SizePrecision: config.SizePrecision,
		PriceIncrement: config.PriceIncrement, SizeIncrement: config.SizeIncrement,
		LotSize:    copyValue(config.LotSize),
		MarginInit: decimalValue(config.MarginInit), MarginMaint: decimalValue(config.MarginMaint),
		MakerFee: decimalValue(config.MakerFee), TakerFee: decimalValue(config.TakerFee),
		MaxQuantity: copyValue(config.MaxQuantity), MinQuantity: copyValue(config.MinQuantity),
		MaxNotional: copyValue(config.MaxNotional), MinNotional: copyValue(config.MinNotional),
		MaxPrice: copyValue(config.MaxPrice), MinPrice: copyValue(config.MinPrice),
		TickScheme: copyValue(config.TickScheme), Info: cloneInfo(config.Info),
		TsEvent: config.TsEvent, TsInit: config.TsInit,
	}, nil
}

func (commodity Commodity) AssetClass() AssetClass           { return commodity.AssetClassValue }
func (commodity Commodity) InstrumentClass() InstrumentClass { return InstrumentClassSpot }
func (commodity Commodity) SettlementCurrency() currency.Currency {
	return commodity.QuoteCurrency
}
func (commodity Commodity) IsInverse() bool           { return false }
func (commodity Commodity) AllowsNegativePrice() bool { return true }
func (commodity Commodity) Multiplier() decimal.Quantity {
	return decimal.MustQuantity("1")
}
func (commodity Commodity) Equal(other Commodity) bool {
	return commodity.InstrumentID == other.InstrumentID
}

type commodityWire struct {
	InstrumentID                  ids.InstrumentID
	RawSymbol                     ids.Symbol
	AssetClass                    AssetClass
	QuoteCurrency                 cryptoCurrencyWire
	PricePrecision, SizePrecision uint8
	PriceIncrement                decimal.Price
	SizeIncrement                 decimal.Quantity
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

func (commodity Commodity) MarshalJSON() ([]byte, error) {
	return json.Marshal(commodityWire{
		InstrumentID: commodity.InstrumentID, RawSymbol: commodity.RawSymbol,
		AssetClass: commodity.AssetClassValue, QuoteCurrency: cryptoCurrency(commodity.QuoteCurrency),
		PricePrecision: commodity.PricePrecision, SizePrecision: commodity.SizePrecision,
		PriceIncrement: commodity.PriceIncrement, SizeIncrement: commodity.SizeIncrement,
		LotSize:    commodity.LotSize,
		MarginInit: commodity.MarginInit.String(), MarginMaint: commodity.MarginMaint.String(),
		MakerFee: commodity.MakerFee.String(), TakerFee: commodity.TakerFee.String(),
		MaxQuantity: commodity.MaxQuantity, MinQuantity: commodity.MinQuantity,
		MaxNotional: cryptoMoney(commodity.MaxNotional), MinNotional: cryptoMoney(commodity.MinNotional),
		MaxPrice: commodity.MaxPrice, MinPrice: commodity.MinPrice,
		TickScheme: commodity.TickScheme, Info: commodity.Info,
		TsEvent: commodity.TsEvent, TsInit: commodity.TsInit,
	})
}

func (commodity *Commodity) UnmarshalJSON(data []byte) error {
	var wire commodityWire
	if err := json.Unmarshal(data, &wire); err != nil {
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
	*commodity = Commodity{
		InstrumentID: wire.InstrumentID, RawSymbol: wire.RawSymbol,
		AssetClassValue: wire.AssetClass, QuoteCurrency: quote,
		PricePrecision: wire.PricePrecision, SizePrecision: wire.SizePrecision,
		PriceIncrement: wire.PriceIncrement, SizeIncrement: wire.SizeIncrement,
		LotSize:    copyValue(wire.LotSize),
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

type CommodityBuilder struct{ config CommodityConfig }

func NewCommodityBuilder() *CommodityBuilder { return &CommodityBuilder{} }
func (builder *CommodityBuilder) Instrument(value ids.InstrumentID) *CommodityBuilder {
	builder.config.InstrumentID = value
	return builder
}
func (builder *CommodityBuilder) Symbol(value ids.Symbol) *CommodityBuilder {
	builder.config.RawSymbol = value
	return builder
}
func (builder *CommodityBuilder) Class(value AssetClass) *CommodityBuilder {
	builder.config.AssetClass = value
	return builder
}
func (builder *CommodityBuilder) Quote(value currency.Currency) *CommodityBuilder {
	builder.config.QuoteCurrency = value
	return builder
}
func (builder *CommodityBuilder) Precisions(price, size uint8) *CommodityBuilder {
	builder.config.PricePrecision, builder.config.SizePrecision = price, size
	return builder
}
func (builder *CommodityBuilder) Increments(price decimal.Price, size decimal.Quantity) *CommodityBuilder {
	builder.config.PriceIncrement, builder.config.SizeIncrement = price, size
	return builder
}
func (builder *CommodityBuilder) WithLotSize(value decimal.Quantity) *CommodityBuilder {
	builder.config.LotSize = &value
	return builder
}
func (builder *CommodityBuilder) QuantityLimits(maximum, minimum decimal.Quantity) *CommodityBuilder {
	builder.config.MaxQuantity, builder.config.MinQuantity = &maximum, &minimum
	return builder
}
func (builder *CommodityBuilder) NotionalLimits(maximum, minimum money.Money) *CommodityBuilder {
	builder.config.MaxNotional, builder.config.MinNotional = &maximum, &minimum
	return builder
}
func (builder *CommodityBuilder) PriceLimits(maximum, minimum decimal.Price) *CommodityBuilder {
	builder.config.MaxPrice, builder.config.MinPrice = &maximum, &minimum
	return builder
}
func (builder *CommodityBuilder) Margins(initial, maintenance decimal.Decimal) *CommodityBuilder {
	builder.config.MarginInit, builder.config.MarginMaint = &initial, &maintenance
	return builder
}
func (builder *CommodityBuilder) Fees(maker, taker decimal.Decimal) *CommodityBuilder {
	builder.config.MakerFee, builder.config.TakerFee = &maker, &taker
	return builder
}
func (builder *CommodityBuilder) Timestamps(event, init uint64) *CommodityBuilder {
	builder.config.TsEvent, builder.config.TsInit = event, init
	return builder
}
func (builder *CommodityBuilder) Build() (Commodity, error) {
	return NewCheckedCommodity(builder.config)
}
