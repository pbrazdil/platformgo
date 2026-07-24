package instrument

import (
	"encoding/json"
	"fmt"

	"github.com/upcomers-org/platformgo/internal/currency"
	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
)

type OptionKind string

const (
	OptionKindCall OptionKind = "CALL"
	OptionKindPut  OptionKind = "PUT"
)

type OptionContractConfig struct {
	InstrumentID   ids.InstrumentID
	RawSymbol      ids.Symbol
	AssetClass     AssetClass
	Exchange       *string
	Underlying     string
	OptionKind     OptionKind
	StrikePrice    decimal.Price
	Currency       currency.Currency
	Activation     uint64
	Expiration     uint64
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

type OptionContract struct {
	InstrumentID   ids.InstrumentID
	RawSymbol      ids.Symbol
	AssetClass     AssetClass
	Exchange       *string
	Underlying     string
	OptionKind     OptionKind
	StrikePrice    decimal.Price
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

func NewCheckedOptionContract(config OptionContractConfig) (OptionContract, error) {
	if config.Exchange != nil {
		if err := validateASCIIText(*config.Exchange, "exchange"); err != nil {
			return OptionContract{}, err
		}
	}
	if err := validateASCIIText(config.Underlying, "underlying"); err != nil {
		return OptionContract{}, err
	}
	if config.PricePrecision != config.PriceIncrement.Precision() {
		return OptionContract{}, fmt.Errorf(
			"price_precision %d was not equal to price_increment.precision %d",
			config.PricePrecision,
			config.PriceIncrement.Precision(),
		)
	}
	if err := config.PriceIncrement.RequirePositive("price_increment"); err != nil {
		return OptionContract{}, err
	}
	if config.TickScheme != nil {
		if _, err := ParseTickScheme(*config.TickScheme); err != nil {
			return OptionContract{}, err
		}
	}
	if err := config.Multiplier.RequirePositive("multiplier"); err != nil {
		return OptionContract{}, err
	}
	if err := config.LotSize.RequirePositive("lot_size"); err != nil {
		return OptionContract{}, err
	}
	minQuantity := config.MinQuantity
	if minQuantity == nil {
		value := decimal.MustQuantity("1")
		minQuantity = &value
	}
	return OptionContract{
		InstrumentID: config.InstrumentID, RawSymbol: config.RawSymbol,
		AssetClass: config.AssetClass, Exchange: copyValue(config.Exchange),
		Underlying: config.Underlying, OptionKind: config.OptionKind, StrikePrice: config.StrikePrice,
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

func (contract OptionContract) InstrumentClass() InstrumentClass { return InstrumentClassOption }
func (contract OptionContract) QuoteCurrency() currency.Currency { return contract.Currency }
func (contract OptionContract) SettlementCurrency() currency.Currency {
	return contract.Currency
}
func (contract OptionContract) IsInverse() bool                  { return false }
func (contract OptionContract) BaseCurrency() *currency.Currency { return nil }
func (contract OptionContract) UnderlyingValue() *string {
	value := contract.Underlying
	return &value
}
func (contract OptionContract) OptionKindValue() *OptionKind {
	value := contract.OptionKind
	return &value
}
func (contract OptionContract) StrikePriceValue() *decimal.Price {
	value := contract.StrikePrice
	return &value
}
func (contract OptionContract) ActivationNanosValue() *uint64 {
	value := contract.Activation
	return &value
}
func (contract OptionContract) ExpirationNanosValue() *uint64 {
	value := contract.Expiration
	return &value
}
func (contract OptionContract) Equal(other OptionContract) bool {
	return contract.InstrumentID == other.InstrumentID
}
func (contract OptionContract) String() string {
	return fmt.Sprintf(
		"OptionContract(id=%s, raw_symbol=%s, underlying=%s, option_kind=%s, strike_price=%s, expiration=%d)",
		contract.InstrumentID, contract.RawSymbol, contract.Underlying,
		contract.OptionKind, contract.StrikePrice, contract.Expiration,
	)
}

type OptionContractBuilder struct{ config OptionContractConfig }

func NewOptionContractBuilder() *OptionContractBuilder { return &OptionContractBuilder{} }
func (builder *OptionContractBuilder) Instrument(value ids.InstrumentID) *OptionContractBuilder {
	builder.config.InstrumentID = value
	return builder
}
func (builder *OptionContractBuilder) Symbol(value ids.Symbol) *OptionContractBuilder {
	builder.config.RawSymbol = value
	return builder
}
func (builder *OptionContractBuilder) Class(value AssetClass) *OptionContractBuilder {
	builder.config.AssetClass = value
	return builder
}
func (builder *OptionContractBuilder) OnExchange(value string) *OptionContractBuilder {
	builder.config.Exchange = &value
	return builder
}
func (builder *OptionContractBuilder) ForUnderlying(value string) *OptionContractBuilder {
	builder.config.Underlying = value
	return builder
}
func (builder *OptionContractBuilder) WithOptionKind(value OptionKind) *OptionContractBuilder {
	builder.config.OptionKind = value
	return builder
}
func (builder *OptionContractBuilder) AtStrike(value decimal.Price) *OptionContractBuilder {
	builder.config.StrikePrice = value
	return builder
}
func (builder *OptionContractBuilder) DenominatedIn(value currency.Currency) *OptionContractBuilder {
	builder.config.Currency = value
	return builder
}
func (builder *OptionContractBuilder) ActiveBetween(activation, expiration uint64) *OptionContractBuilder {
	builder.config.Activation, builder.config.Expiration = activation, expiration
	return builder
}
func (builder *OptionContractBuilder) PriceDigits(value uint8) *OptionContractBuilder {
	builder.config.PricePrecision = value
	return builder
}
func (builder *OptionContractBuilder) TickSize(value decimal.Price) *OptionContractBuilder {
	builder.config.PriceIncrement = value
	return builder
}
func (builder *OptionContractBuilder) WithMultiplier(value decimal.Quantity) *OptionContractBuilder {
	builder.config.Multiplier = value
	return builder
}
func (builder *OptionContractBuilder) WithLotSize(value decimal.Quantity) *OptionContractBuilder {
	builder.config.LotSize = value
	return builder
}
func (builder *OptionContractBuilder) WithMaxQuantity(value decimal.Quantity) *OptionContractBuilder {
	builder.config.MaxQuantity = &value
	return builder
}
func (builder *OptionContractBuilder) WithMinQuantity(value decimal.Quantity) *OptionContractBuilder {
	builder.config.MinQuantity = &value
	return builder
}
func (builder *OptionContractBuilder) WithMaxPrice(value decimal.Price) *OptionContractBuilder {
	builder.config.MaxPrice = &value
	return builder
}
func (builder *OptionContractBuilder) WithMinPrice(value decimal.Price) *OptionContractBuilder {
	builder.config.MinPrice = &value
	return builder
}
func (builder *OptionContractBuilder) Margins(initial, maintenance decimal.Decimal) *OptionContractBuilder {
	builder.config.MarginInit, builder.config.MarginMaint = &initial, &maintenance
	return builder
}
func (builder *OptionContractBuilder) Fees(maker, taker decimal.Decimal) *OptionContractBuilder {
	builder.config.MakerFee, builder.config.TakerFee = &maker, &taker
	return builder
}
func (builder *OptionContractBuilder) Timestamps(event, init uint64) *OptionContractBuilder {
	builder.config.TsEvent, builder.config.TsInit = event, init
	return builder
}
func (builder *OptionContractBuilder) Build() (OptionContract, error) {
	return NewCheckedOptionContract(builder.config)
}

type optionContractWire struct {
	InstrumentID                                ids.InstrumentID
	RawSymbol                                   ids.Symbol
	AssetClass                                  AssetClass
	Exchange                                    *string
	Underlying                                  string
	OptionKind                                  OptionKind
	StrikePrice                                 decimal.Price
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

func (contract OptionContract) MarshalJSON() ([]byte, error) {
	return json.Marshal(optionContractWire{
		InstrumentID: contract.InstrumentID, RawSymbol: contract.RawSymbol,
		AssetClass: contract.AssetClass, Exchange: contract.Exchange, Underlying: contract.Underlying,
		OptionKind: contract.OptionKind, StrikePrice: contract.StrikePrice,
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

func (contract *OptionContract) UnmarshalJSON(data []byte) error {
	var wire optionContractWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	denomination, err := currencyFromCrypto(wire.Currency)
	if err != nil {
		return err
	}
	*contract = OptionContract{
		InstrumentID: wire.InstrumentID, RawSymbol: wire.RawSymbol, AssetClass: wire.AssetClass,
		Exchange: copyValue(wire.Exchange), Underlying: wire.Underlying,
		OptionKind: wire.OptionKind, StrikePrice: wire.StrikePrice,
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
