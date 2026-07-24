package instrument

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/upcomers-org/platformgo/internal/currency"
	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
)

const AssetClassIndex AssetClass = "INDEX"

type FuturesContractConfig struct {
	InstrumentID   ids.InstrumentID
	RawSymbol      ids.Symbol
	AssetClass     AssetClass
	Exchange       *string
	Underlying     string
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

type FuturesContract struct {
	InstrumentID   ids.InstrumentID
	RawSymbol      ids.Symbol
	AssetClass     AssetClass
	Exchange       *string
	Underlying     string
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

func NewCheckedFuturesContract(config FuturesContractConfig) (FuturesContract, error) {
	if config.Exchange != nil {
		if err := validateASCIIText(*config.Exchange, "exchange"); err != nil {
			return FuturesContract{}, err
		}
	}
	if err := validateASCIIText(config.Underlying, "underlying"); err != nil {
		return FuturesContract{}, err
	}
	if config.PricePrecision != config.PriceIncrement.Precision() {
		return FuturesContract{}, fmt.Errorf(
			"price_precision %d was not equal to price_increment.precision %d",
			config.PricePrecision,
			config.PriceIncrement.Precision(),
		)
	}
	if err := config.PriceIncrement.RequirePositive("price_increment"); err != nil {
		return FuturesContract{}, err
	}
	if config.TickScheme != nil {
		if _, err := ParseTickScheme(*config.TickScheme); err != nil {
			return FuturesContract{}, err
		}
	}
	if err := config.Multiplier.RequirePositive("multiplier"); err != nil {
		return FuturesContract{}, err
	}
	if err := config.LotSize.RequirePositive("lot_size"); err != nil {
		return FuturesContract{}, err
	}
	minQuantity := config.MinQuantity
	if minQuantity == nil {
		value := decimal.MustQuantity("1")
		minQuantity = &value
	}
	return FuturesContract{
		InstrumentID: config.InstrumentID, RawSymbol: config.RawSymbol,
		AssetClass: config.AssetClass, Exchange: copyValue(config.Exchange),
		Underlying: config.Underlying, Activation: config.Activation, Expiration: config.Expiration,
		Currency: config.Currency, PricePrecision: config.PricePrecision,
		PriceIncrement: config.PriceIncrement, SizePrecision: 0,
		SizeIncrement: decimal.MustQuantity("1"), Multiplier: config.Multiplier, LotSize: config.LotSize,
		MaxQuantity: copyValue(config.MaxQuantity), MinQuantity: copyValue(minQuantity),
		MaxPrice: copyValue(config.MaxPrice), MinPrice: copyValue(config.MinPrice),
		MarginInit: decimalValue(config.MarginInit), MarginMaint: decimalValue(config.MarginMaint),
		MakerFee: decimalValue(config.MakerFee), TakerFee: decimalValue(config.TakerFee),
		TickScheme: copyValue(config.TickScheme), Info: cloneInfo(config.Info),
		TsEvent: config.TsEvent, TsInit: config.TsInit,
	}, nil
}

func validateASCIIText(value, field string) error {
	if value == "" || strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s must not be empty", field)
	}
	for _, char := range value {
		if char > 127 {
			return fmt.Errorf("%s must contain only ASCII", field)
		}
	}
	return nil
}

func (contract FuturesContract) InstrumentClass() InstrumentClass { return InstrumentClassFuture }
func (contract FuturesContract) QuoteCurrency() currency.Currency { return contract.Currency }
func (contract FuturesContract) SettlementCurrency() currency.Currency {
	return contract.Currency
}
func (contract FuturesContract) IsInverse() bool                  { return false }
func (contract FuturesContract) BaseCurrency() *currency.Currency { return nil }
func (contract FuturesContract) UnderlyingValue() *string {
	value := contract.Underlying
	return &value
}
func (contract FuturesContract) ActivationNanosValue() *uint64 {
	value := contract.Activation
	return &value
}
func (contract FuturesContract) ExpirationNanosValue() *uint64 {
	value := contract.Expiration
	return &value
}
func (contract FuturesContract) Equal(other FuturesContract) bool {
	return contract.InstrumentID == other.InstrumentID
}
func (contract FuturesContract) String() string {
	return fmt.Sprintf(
		"FuturesContract(id=%s, raw_symbol=%s, underlying=%s, expiration=%d)",
		contract.InstrumentID, contract.RawSymbol, contract.Underlying, contract.Expiration,
	)
}

type FuturesContractBuilder struct{ config FuturesContractConfig }

func NewFuturesContractBuilder() *FuturesContractBuilder { return &FuturesContractBuilder{} }
func (builder *FuturesContractBuilder) Instrument(value ids.InstrumentID) *FuturesContractBuilder {
	builder.config.InstrumentID = value
	return builder
}
func (builder *FuturesContractBuilder) Symbol(value ids.Symbol) *FuturesContractBuilder {
	builder.config.RawSymbol = value
	return builder
}
func (builder *FuturesContractBuilder) Class(value AssetClass) *FuturesContractBuilder {
	builder.config.AssetClass = value
	return builder
}
func (builder *FuturesContractBuilder) OnExchange(value string) *FuturesContractBuilder {
	builder.config.Exchange = &value
	return builder
}
func (builder *FuturesContractBuilder) ForUnderlying(value string) *FuturesContractBuilder {
	builder.config.Underlying = value
	return builder
}
func (builder *FuturesContractBuilder) ActiveBetween(activation, expiration uint64) *FuturesContractBuilder {
	builder.config.Activation, builder.config.Expiration = activation, expiration
	return builder
}
func (builder *FuturesContractBuilder) DenominatedIn(value currency.Currency) *FuturesContractBuilder {
	builder.config.Currency = value
	return builder
}
func (builder *FuturesContractBuilder) PriceDigits(value uint8) *FuturesContractBuilder {
	builder.config.PricePrecision = value
	return builder
}
func (builder *FuturesContractBuilder) TickSize(value decimal.Price) *FuturesContractBuilder {
	builder.config.PriceIncrement = value
	return builder
}
func (builder *FuturesContractBuilder) WithMultiplier(value decimal.Quantity) *FuturesContractBuilder {
	builder.config.Multiplier = value
	return builder
}
func (builder *FuturesContractBuilder) WithLotSize(value decimal.Quantity) *FuturesContractBuilder {
	builder.config.LotSize = value
	return builder
}
func (builder *FuturesContractBuilder) WithMaxQuantity(value decimal.Quantity) *FuturesContractBuilder {
	builder.config.MaxQuantity = &value
	return builder
}
func (builder *FuturesContractBuilder) WithMinQuantity(value decimal.Quantity) *FuturesContractBuilder {
	builder.config.MinQuantity = &value
	return builder
}
func (builder *FuturesContractBuilder) WithMaxPrice(value decimal.Price) *FuturesContractBuilder {
	builder.config.MaxPrice = &value
	return builder
}
func (builder *FuturesContractBuilder) WithMinPrice(value decimal.Price) *FuturesContractBuilder {
	builder.config.MinPrice = &value
	return builder
}
func (builder *FuturesContractBuilder) Margins(initial, maintenance decimal.Decimal) *FuturesContractBuilder {
	builder.config.MarginInit, builder.config.MarginMaint = &initial, &maintenance
	return builder
}
func (builder *FuturesContractBuilder) Fees(maker, taker decimal.Decimal) *FuturesContractBuilder {
	builder.config.MakerFee, builder.config.TakerFee = &maker, &taker
	return builder
}
func (builder *FuturesContractBuilder) Timestamps(event, init uint64) *FuturesContractBuilder {
	builder.config.TsEvent, builder.config.TsInit = event, init
	return builder
}
func (builder *FuturesContractBuilder) Build() (FuturesContract, error) {
	return NewCheckedFuturesContract(builder.config)
}

type futuresContractWire struct {
	InstrumentID                                ids.InstrumentID
	RawSymbol                                   ids.Symbol
	AssetClass                                  AssetClass
	Exchange                                    *string
	Underlying                                  string
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

func (contract FuturesContract) MarshalJSON() ([]byte, error) {
	return json.Marshal(futuresContractWire{
		InstrumentID: contract.InstrumentID, RawSymbol: contract.RawSymbol,
		AssetClass: contract.AssetClass, Exchange: contract.Exchange, Underlying: contract.Underlying,
		Activation: contract.Activation, Expiration: contract.Expiration,
		Currency: cryptoCurrency(contract.Currency), PricePrecision: contract.PricePrecision,
		PriceIncrement: contract.PriceIncrement, SizePrecision: contract.SizePrecision,
		SizeIncrement: contract.SizeIncrement, Multiplier: contract.Multiplier, LotSize: contract.LotSize,
		MaxQuantity: contract.MaxQuantity, MinQuantity: contract.MinQuantity,
		MaxPrice: contract.MaxPrice, MinPrice: contract.MinPrice,
		MarginInit: contract.MarginInit.String(), MarginMaint: contract.MarginMaint.String(),
		MakerFee: contract.MakerFee.String(), TakerFee: contract.TakerFee.String(),
		TickScheme: contract.TickScheme, Info: contract.Info, TsEvent: contract.TsEvent, TsInit: contract.TsInit,
	})
}

func (contract *FuturesContract) UnmarshalJSON(data []byte) error {
	var wire futuresContractWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	denomination, err := currencyFromCrypto(wire.Currency)
	if err != nil {
		return err
	}
	*contract = FuturesContract{
		InstrumentID: wire.InstrumentID, RawSymbol: wire.RawSymbol, AssetClass: wire.AssetClass,
		Exchange: copyValue(wire.Exchange), Underlying: wire.Underlying,
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
