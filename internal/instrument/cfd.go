package instrument

import (
	"encoding/json"
	"fmt"

	"github.com/upcomers-org/platformgo/internal/currency"
	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
	"github.com/upcomers-org/platformgo/internal/money"
)

type CfdConfig struct {
	InstrumentID                  ids.InstrumentID
	RawSymbol                     ids.Symbol
	AssetClass                    AssetClass
	BaseCurrency                  *currency.Currency
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

type Cfd struct {
	InstrumentID                  ids.InstrumentID
	RawSymbol                     ids.Symbol
	AssetClassValue               AssetClass
	BaseCurrency                  *currency.Currency
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

func NewCheckedCfd(config CfdConfig) (Cfd, error) {
	if config.PricePrecision != config.PriceIncrement.Precision() {
		return Cfd{}, fmt.Errorf(
			"price_precision %d was not equal to price_increment.precision %d",
			config.PricePrecision,
			config.PriceIncrement.Precision(),
		)
	}
	if config.SizePrecision != config.SizeIncrement.Precision() {
		return Cfd{}, fmt.Errorf(
			"size_precision %d was not equal to size_increment.precision %d",
			config.SizePrecision,
			config.SizeIncrement.Precision(),
		)
	}
	if err := config.PriceIncrement.RequirePositive("price_increment"); err != nil {
		return Cfd{}, err
	}
	if err := config.SizeIncrement.RequirePositive("size_increment"); err != nil {
		return Cfd{}, err
	}
	if config.TickScheme != nil {
		if _, err := ParseTickScheme(*config.TickScheme); err != nil {
			return Cfd{}, err
		}
	}
	if config.LotSize != nil {
		if err := config.LotSize.RequirePositive("lot_size"); err != nil {
			return Cfd{}, err
		}
	}

	return Cfd{
		InstrumentID: config.InstrumentID, RawSymbol: config.RawSymbol,
		AssetClassValue: config.AssetClass, BaseCurrency: copyValue(config.BaseCurrency),
		QuoteCurrency:  config.QuoteCurrency,
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

func (cfd Cfd) AssetClass() AssetClass           { return cfd.AssetClassValue }
func (cfd Cfd) InstrumentClass() InstrumentClass { return InstrumentClassCFD }
func (cfd Cfd) SettlementCurrency() currency.Currency {
	return cfd.QuoteCurrency
}
func (cfd Cfd) IsInverse() bool { return false }
func (cfd Cfd) Multiplier() decimal.Quantity {
	return decimal.MustQuantity("1")
}
func (cfd Cfd) Equal(other Cfd) bool {
	return cfd.InstrumentID == other.InstrumentID
}

type cfdWire struct {
	InstrumentID                  ids.InstrumentID
	RawSymbol                     ids.Symbol
	AssetClass                    AssetClass
	BaseCurrency                  *cryptoCurrencyWire
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

func cfdCurrency(value *currency.Currency) *cryptoCurrencyWire {
	if value == nil {
		return nil
	}
	wire := cryptoCurrency(*value)
	return &wire
}

func cfdCurrencyFromWire(value *cryptoCurrencyWire) (*currency.Currency, error) {
	if value == nil {
		return nil, nil
	}
	denomination, err := currencyFromCrypto(*value)
	if err != nil {
		return nil, err
	}
	return &denomination, nil
}

func (cfd Cfd) MarshalJSON() ([]byte, error) {
	return json.Marshal(cfdWire{
		InstrumentID: cfd.InstrumentID, RawSymbol: cfd.RawSymbol,
		AssetClass: cfd.AssetClassValue, BaseCurrency: cfdCurrency(cfd.BaseCurrency),
		QuoteCurrency:  cryptoCurrency(cfd.QuoteCurrency),
		PricePrecision: cfd.PricePrecision, SizePrecision: cfd.SizePrecision,
		PriceIncrement: cfd.PriceIncrement, SizeIncrement: cfd.SizeIncrement,
		LotSize:    cfd.LotSize,
		MarginInit: cfd.MarginInit.String(), MarginMaint: cfd.MarginMaint.String(),
		MakerFee: cfd.MakerFee.String(), TakerFee: cfd.TakerFee.String(),
		MaxQuantity: cfd.MaxQuantity, MinQuantity: cfd.MinQuantity,
		MaxNotional: cryptoMoney(cfd.MaxNotional), MinNotional: cryptoMoney(cfd.MinNotional),
		MaxPrice: cfd.MaxPrice, MinPrice: cfd.MinPrice,
		TickScheme: cfd.TickScheme, Info: cfd.Info,
		TsEvent: cfd.TsEvent, TsInit: cfd.TsInit,
	})
}

func (cfd *Cfd) UnmarshalJSON(data []byte) error {
	var wire cfdWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	base, err := cfdCurrencyFromWire(wire.BaseCurrency)
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
	*cfd = Cfd{
		InstrumentID: wire.InstrumentID, RawSymbol: wire.RawSymbol,
		AssetClassValue: wire.AssetClass, BaseCurrency: base, QuoteCurrency: quote,
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

type CfdBuilder struct{ config CfdConfig }

func NewCfdBuilder() *CfdBuilder { return &CfdBuilder{} }
func (builder *CfdBuilder) Instrument(value ids.InstrumentID) *CfdBuilder {
	builder.config.InstrumentID = value
	return builder
}
func (builder *CfdBuilder) Symbol(value ids.Symbol) *CfdBuilder {
	builder.config.RawSymbol = value
	return builder
}
func (builder *CfdBuilder) Class(value AssetClass) *CfdBuilder {
	builder.config.AssetClass = value
	return builder
}
func (builder *CfdBuilder) Base(value currency.Currency) *CfdBuilder {
	builder.config.BaseCurrency = &value
	return builder
}
func (builder *CfdBuilder) Quote(value currency.Currency) *CfdBuilder {
	builder.config.QuoteCurrency = value
	return builder
}
func (builder *CfdBuilder) Precisions(price, size uint8) *CfdBuilder {
	builder.config.PricePrecision, builder.config.SizePrecision = price, size
	return builder
}
func (builder *CfdBuilder) Increments(price decimal.Price, size decimal.Quantity) *CfdBuilder {
	builder.config.PriceIncrement, builder.config.SizeIncrement = price, size
	return builder
}
func (builder *CfdBuilder) WithLotSize(value decimal.Quantity) *CfdBuilder {
	builder.config.LotSize = &value
	return builder
}
func (builder *CfdBuilder) QuantityLimits(maximum, minimum decimal.Quantity) *CfdBuilder {
	builder.config.MaxQuantity, builder.config.MinQuantity = &maximum, &minimum
	return builder
}
func (builder *CfdBuilder) NotionalLimits(maximum, minimum money.Money) *CfdBuilder {
	builder.config.MaxNotional, builder.config.MinNotional = &maximum, &minimum
	return builder
}
func (builder *CfdBuilder) PriceLimits(maximum, minimum decimal.Price) *CfdBuilder {
	builder.config.MaxPrice, builder.config.MinPrice = &maximum, &minimum
	return builder
}
func (builder *CfdBuilder) Margins(initial, maintenance decimal.Decimal) *CfdBuilder {
	builder.config.MarginInit, builder.config.MarginMaint = &initial, &maintenance
	return builder
}
func (builder *CfdBuilder) Fees(maker, taker decimal.Decimal) *CfdBuilder {
	builder.config.MakerFee, builder.config.TakerFee = &maker, &taker
	return builder
}
func (builder *CfdBuilder) Timestamps(event, init uint64) *CfdBuilder {
	builder.config.TsEvent, builder.config.TsInit = event, init
	return builder
}
func (builder *CfdBuilder) Build() (Cfd, error) {
	return NewCheckedCfd(builder.config)
}
