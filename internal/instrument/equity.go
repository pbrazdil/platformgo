package instrument

import (
	"encoding/json"
	"fmt"

	"github.com/upcomers-org/platformgo/internal/currency"
	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
)

const AssetClassEquity AssetClass = "EQUITY"

type EquityConfig struct {
	InstrumentID   ids.InstrumentID
	RawSymbol      ids.Symbol
	ISIN           *string
	Currency       currency.Currency
	PricePrecision uint8
	PriceIncrement decimal.Price
	LotSize        *decimal.Quantity
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

type Equity struct {
	InstrumentID   ids.InstrumentID
	RawSymbol      ids.Symbol
	ISIN           *string
	Currency       currency.Currency
	PricePrecision uint8
	PriceIncrement decimal.Price
	MarginInit     decimal.Decimal
	MarginMaint    decimal.Decimal
	MakerFee       decimal.Decimal
	TakerFee       decimal.Decimal
	LotSize        *decimal.Quantity
	MaxQuantity    *decimal.Quantity
	MinQuantity    *decimal.Quantity
	MaxPrice       *decimal.Price
	MinPrice       *decimal.Price
	TickScheme     *string
	Info           map[string]any
	TsEvent        uint64
	TsInit         uint64
}

func NewCheckedEquity(config EquityConfig) (Equity, error) {
	if config.ISIN != nil {
		for _, char := range *config.ISIN {
			if char > 127 {
				return Equity{}, fmt.Errorf("isin contained non-ASCII character")
			}
		}
	}
	if config.PricePrecision != config.PriceIncrement.Precision() {
		return Equity{}, fmt.Errorf(
			"price_precision %d was not equal to price_increment.precision %d",
			config.PricePrecision,
			config.PriceIncrement.Precision(),
		)
	}
	if err := config.PriceIncrement.RequirePositive("price_increment"); err != nil {
		return Equity{}, err
	}
	if config.TickScheme != nil {
		if _, err := ParseTickScheme(*config.TickScheme); err != nil {
			return Equity{}, err
		}
	}
	if config.LotSize != nil {
		if err := config.LotSize.RequirePositive("lot_size"); err != nil {
			return Equity{}, err
		}
	}
	return Equity{
		InstrumentID: config.InstrumentID, RawSymbol: config.RawSymbol,
		ISIN: copyValue(config.ISIN), Currency: config.Currency,
		PricePrecision: config.PricePrecision, PriceIncrement: config.PriceIncrement,
		MarginInit: decimalValue(config.MarginInit), MarginMaint: decimalValue(config.MarginMaint),
		MakerFee: decimalValue(config.MakerFee), TakerFee: decimalValue(config.TakerFee),
		LotSize: copyValue(config.LotSize), MaxQuantity: copyValue(config.MaxQuantity),
		MinQuantity: copyValue(config.MinQuantity), MaxPrice: copyValue(config.MaxPrice),
		MinPrice: copyValue(config.MinPrice), TickScheme: copyValue(config.TickScheme),
		Info: cloneInfo(config.Info), TsEvent: config.TsEvent, TsInit: config.TsInit,
	}, nil
}

func (equity Equity) AssetClass() AssetClass           { return AssetClassEquity }
func (equity Equity) InstrumentClass() InstrumentClass { return InstrumentClassSpot }
func (equity Equity) QuoteCurrency() currency.Currency { return equity.Currency }
func (equity Equity) SettlementCurrency() currency.Currency {
	return equity.Currency
}
func (equity Equity) IsInverse() bool                  { return false }
func (equity Equity) SizePrecision() uint8             { return 0 }
func (equity Equity) SizeIncrement() decimal.Quantity  { return decimal.MustQuantity("1") }
func (equity Equity) Multiplier() decimal.Quantity     { return decimal.MustQuantity("1") }
func (equity Equity) BaseCurrency() *currency.Currency { return nil }
func (equity Equity) Underlying() *string              { return nil }
func (equity Equity) OptionKind() *string              { return nil }
func (equity Equity) StrikePrice() *decimal.Price      { return nil }
func (equity Equity) ActivationNanos() *uint64         { return nil }
func (equity Equity) ExpirationNanos() *uint64         { return nil }
func (equity Equity) Equal(other Equity) bool          { return equity.InstrumentID == other.InstrumentID }
func (equity Equity) String() string {
	return fmt.Sprintf(
		"Equity(id=%s, raw_symbol=%s, currency=%s, price_increment=%s)",
		equity.InstrumentID, equity.RawSymbol, equity.Currency, equity.PriceIncrement,
	)
}

type EquityBuilder struct{ config EquityConfig }

func NewEquityBuilder() *EquityBuilder { return &EquityBuilder{} }
func (builder *EquityBuilder) Instrument(value ids.InstrumentID) *EquityBuilder {
	builder.config.InstrumentID = value
	return builder
}
func (builder *EquityBuilder) Symbol(value ids.Symbol) *EquityBuilder {
	builder.config.RawSymbol = value
	return builder
}
func (builder *EquityBuilder) WithISIN(value string) *EquityBuilder {
	builder.config.ISIN = &value
	return builder
}
func (builder *EquityBuilder) DenominatedIn(value currency.Currency) *EquityBuilder {
	builder.config.Currency = value
	return builder
}
func (builder *EquityBuilder) PriceDigits(value uint8) *EquityBuilder {
	builder.config.PricePrecision = value
	return builder
}
func (builder *EquityBuilder) TickSize(value decimal.Price) *EquityBuilder {
	builder.config.PriceIncrement = value
	return builder
}
func (builder *EquityBuilder) WithLotSize(value decimal.Quantity) *EquityBuilder {
	builder.config.LotSize = &value
	return builder
}
func (builder *EquityBuilder) WithMaxQuantity(value decimal.Quantity) *EquityBuilder {
	builder.config.MaxQuantity = &value
	return builder
}
func (builder *EquityBuilder) WithMinQuantity(value decimal.Quantity) *EquityBuilder {
	builder.config.MinQuantity = &value
	return builder
}
func (builder *EquityBuilder) WithMaxPrice(value decimal.Price) *EquityBuilder {
	builder.config.MaxPrice = &value
	return builder
}
func (builder *EquityBuilder) WithMinPrice(value decimal.Price) *EquityBuilder {
	builder.config.MinPrice = &value
	return builder
}
func (builder *EquityBuilder) Margins(initial, maintenance decimal.Decimal) *EquityBuilder {
	builder.config.MarginInit, builder.config.MarginMaint = &initial, &maintenance
	return builder
}
func (builder *EquityBuilder) Fees(maker, taker decimal.Decimal) *EquityBuilder {
	builder.config.MakerFee, builder.config.TakerFee = &maker, &taker
	return builder
}
func (builder *EquityBuilder) Timestamps(event, init uint64) *EquityBuilder {
	builder.config.TsEvent, builder.config.TsInit = event, init
	return builder
}
func (builder *EquityBuilder) Build() (Equity, error) { return NewCheckedEquity(builder.config) }

type equityWire struct {
	InstrumentID   ids.InstrumentID
	RawSymbol      ids.Symbol
	ISIN           *string
	Currency       cryptoCurrencyWire
	PricePrecision uint8
	PriceIncrement decimal.Price
	MarginInit     string
	MarginMaint    string
	MakerFee       string
	TakerFee       string
	LotSize        *decimal.Quantity
	MaxQuantity    *decimal.Quantity
	MinQuantity    *decimal.Quantity
	MaxPrice       *decimal.Price
	MinPrice       *decimal.Price
	TickScheme     *string
	Info           map[string]any
	TsEvent        uint64
	TsInit         uint64
}

func (equity Equity) MarshalJSON() ([]byte, error) {
	return json.Marshal(equityWire{
		InstrumentID: equity.InstrumentID, RawSymbol: equity.RawSymbol, ISIN: equity.ISIN,
		Currency: cryptoCurrency(equity.Currency), PricePrecision: equity.PricePrecision,
		PriceIncrement: equity.PriceIncrement, MarginInit: equity.MarginInit.String(),
		MarginMaint: equity.MarginMaint.String(), MakerFee: equity.MakerFee.String(),
		TakerFee: equity.TakerFee.String(), LotSize: equity.LotSize,
		MaxQuantity: equity.MaxQuantity, MinQuantity: equity.MinQuantity,
		MaxPrice: equity.MaxPrice, MinPrice: equity.MinPrice,
		TickScheme: equity.TickScheme, Info: equity.Info, TsEvent: equity.TsEvent, TsInit: equity.TsInit,
	})
}

func (equity *Equity) UnmarshalJSON(data []byte) error {
	var wire equityWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	denomination, err := currencyFromCrypto(wire.Currency)
	if err != nil {
		return err
	}
	*equity = Equity{
		InstrumentID: wire.InstrumentID, RawSymbol: wire.RawSymbol, ISIN: copyValue(wire.ISIN),
		Currency: denomination, PricePrecision: wire.PricePrecision, PriceIncrement: wire.PriceIncrement,
		MarginInit: decimal.MustParse(wire.MarginInit), MarginMaint: decimal.MustParse(wire.MarginMaint),
		MakerFee: decimal.MustParse(wire.MakerFee), TakerFee: decimal.MustParse(wire.TakerFee),
		LotSize: copyValue(wire.LotSize), MaxQuantity: copyValue(wire.MaxQuantity),
		MinQuantity: copyValue(wire.MinQuantity), MaxPrice: copyValue(wire.MaxPrice),
		MinPrice: copyValue(wire.MinPrice), TickScheme: copyValue(wire.TickScheme),
		Info: cloneInfo(wire.Info), TsEvent: wire.TsEvent, TsInit: wire.TsInit,
	}
	return nil
}
