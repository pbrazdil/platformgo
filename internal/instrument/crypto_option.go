package instrument

import (
	"encoding/json"
	"fmt"

	"github.com/upcomers-org/platformgo/internal/currency"
	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
	"github.com/upcomers-org/platformgo/internal/money"
)

type CryptoOptionConfig struct {
	InstrumentID                                  ids.InstrumentID
	RawSymbol                                     ids.Symbol
	Underlying, QuoteCurrency, SettlementCurrency currency.Currency
	Inverse                                       bool
	OptionKind                                    OptionKind
	StrikePrice                                   decimal.Price
	Activation, Expiration                        uint64
	PricePrecision, SizePrecision                 uint8
	PriceIncrement                                decimal.Price
	SizeIncrement                                 decimal.Quantity
	Multiplier, LotSize                           *decimal.Quantity
	MaxQuantity, MinQuantity                      *decimal.Quantity
	MaxNotional, MinNotional                      *money.Money
	MaxPrice, MinPrice                            *decimal.Price
	MarginInit, MarginMaint, MakerFee, TakerFee   *decimal.Decimal
	TickScheme                                    *string
	Info                                          map[string]any
	TsEvent, TsInit                               uint64
}

type CryptoOption struct {
	InstrumentID                                  ids.InstrumentID
	RawSymbol                                     ids.Symbol
	Underlying, QuoteCurrency, SettlementCurrency currency.Currency
	Inverse                                       bool
	OptionKind                                    OptionKind
	StrikePrice                                   decimal.Price
	Activation, Expiration                        uint64
	PricePrecision, SizePrecision                 uint8
	PriceIncrement                                decimal.Price
	SizeIncrement, Multiplier, LotSize            decimal.Quantity
	MarginInit, MarginMaint, MakerFee, TakerFee   decimal.Decimal
	MaxQuantity, MinQuantity                      *decimal.Quantity
	MaxNotional, MinNotional                      *money.Money
	MaxPrice, MinPrice                            *decimal.Price
	TickScheme                                    *string
	Info                                          map[string]any
	TsEvent, TsInit                               uint64
}

func NewCheckedCryptoOption(config CryptoOptionConfig) (CryptoOption, error) {
	if config.PricePrecision != config.PriceIncrement.Precision() {
		return CryptoOption{}, fmt.Errorf(
			"price_precision %d was not equal to price_increment.precision %d",
			config.PricePrecision,
			config.PriceIncrement.Precision(),
		)
	}
	if config.SizePrecision != config.SizeIncrement.Precision() {
		return CryptoOption{}, fmt.Errorf(
			"size_precision %d was not equal to size_increment.precision %d",
			config.SizePrecision,
			config.SizeIncrement.Precision(),
		)
	}
	if err := config.PriceIncrement.RequirePositive("price_increment"); err != nil {
		return CryptoOption{}, err
	}
	if err := config.SizeIncrement.RequirePositive("size_increment"); err != nil {
		return CryptoOption{}, err
	}
	if config.TickScheme != nil {
		if _, err := ParseTickScheme(*config.TickScheme); err != nil {
			return CryptoOption{}, err
		}
	}
	if config.Multiplier != nil {
		if err := config.Multiplier.RequirePositive("multiplier"); err != nil {
			return CryptoOption{}, err
		}
	}
	if config.LotSize != nil {
		if err := config.LotSize.RequirePositive("lot_size"); err != nil {
			return CryptoOption{}, err
		}
	}

	one := decimal.MustQuantity("1")
	multiplier, lotSize := one, one
	if config.Multiplier != nil {
		multiplier = *config.Multiplier
	}
	if config.LotSize != nil {
		lotSize = *config.LotSize
	}
	minQuantity := copyValue(config.MinQuantity)
	if minQuantity == nil {
		minQuantity = &one
	}
	return CryptoOption{
		InstrumentID: config.InstrumentID, RawSymbol: config.RawSymbol,
		Underlying: config.Underlying, QuoteCurrency: config.QuoteCurrency,
		SettlementCurrency: config.SettlementCurrency, Inverse: config.Inverse,
		OptionKind: config.OptionKind, StrikePrice: config.StrikePrice,
		Activation: config.Activation, Expiration: config.Expiration,
		PricePrecision: config.PricePrecision, SizePrecision: config.SizePrecision,
		PriceIncrement: config.PriceIncrement, SizeIncrement: config.SizeIncrement,
		Multiplier: multiplier, LotSize: lotSize,
		MarginInit: decimalValue(config.MarginInit), MarginMaint: decimalValue(config.MarginMaint),
		MakerFee: decimalValue(config.MakerFee), TakerFee: decimalValue(config.TakerFee),
		MaxQuantity: copyValue(config.MaxQuantity), MinQuantity: minQuantity,
		MaxNotional: copyValue(config.MaxNotional), MinNotional: copyValue(config.MinNotional),
		MaxPrice: copyValue(config.MaxPrice), MinPrice: copyValue(config.MinPrice),
		TickScheme: copyValue(config.TickScheme), Info: cloneInfo(config.Info),
		TsEvent: config.TsEvent, TsInit: config.TsInit,
	}, nil
}

func (option CryptoOption) AssetClass() AssetClass           { return AssetClassCryptocurrency }
func (option CryptoOption) InstrumentClass() InstrumentClass { return InstrumentClassOption }
func (option CryptoOption) IsInverse() bool                  { return option.Inverse }
func (option CryptoOption) UnderlyingValue() *string {
	value := option.Underlying.Code
	return &value
}
func (option CryptoOption) BaseCurrency() *currency.Currency {
	value := option.Underlying
	return &value
}
func (option CryptoOption) OptionKindValue() *OptionKind {
	value := option.OptionKind
	return &value
}
func (option CryptoOption) StrikePriceValue() *decimal.Price {
	value := option.StrikePrice
	return &value
}
func (option CryptoOption) ActivationNanosValue() *uint64 {
	value := option.Activation
	return &value
}
func (option CryptoOption) ExpirationNanosValue() *uint64 {
	value := option.Expiration
	return &value
}
func (option CryptoOption) Equal(other CryptoOption) bool {
	return option.InstrumentID == other.InstrumentID
}

type cryptoOptionWire struct {
	InstrumentID                                  ids.InstrumentID
	RawSymbol                                     ids.Symbol
	Underlying, QuoteCurrency, SettlementCurrency cryptoCurrencyWire
	Inverse                                       bool
	OptionKind                                    OptionKind
	StrikePrice                                   decimal.Price
	Activation, Expiration                        uint64
	PricePrecision, SizePrecision                 uint8
	PriceIncrement                                decimal.Price
	SizeIncrement, Multiplier, LotSize            decimal.Quantity
	MarginInit, MarginMaint, MakerFee, TakerFee   string
	MaxQuantity, MinQuantity                      *decimal.Quantity
	MaxNotional, MinNotional                      *cryptoMoneyWire
	MaxPrice, MinPrice                            *decimal.Price
	TickScheme                                    *string
	Info                                          map[string]any
	TsEvent, TsInit                               uint64
}

func (option CryptoOption) MarshalJSON() ([]byte, error) {
	return json.Marshal(cryptoOptionWire{
		InstrumentID: option.InstrumentID, RawSymbol: option.RawSymbol,
		Underlying:         cryptoCurrency(option.Underlying),
		QuoteCurrency:      cryptoCurrency(option.QuoteCurrency),
		SettlementCurrency: cryptoCurrency(option.SettlementCurrency),
		Inverse:            option.Inverse, OptionKind: option.OptionKind, StrikePrice: option.StrikePrice,
		Activation: option.Activation, Expiration: option.Expiration,
		PricePrecision: option.PricePrecision, SizePrecision: option.SizePrecision,
		PriceIncrement: option.PriceIncrement, SizeIncrement: option.SizeIncrement,
		Multiplier: option.Multiplier, LotSize: option.LotSize,
		MarginInit: option.MarginInit.String(), MarginMaint: option.MarginMaint.String(),
		MakerFee: option.MakerFee.String(), TakerFee: option.TakerFee.String(),
		MaxQuantity: option.MaxQuantity, MinQuantity: option.MinQuantity,
		MaxNotional: cryptoMoney(option.MaxNotional), MinNotional: cryptoMoney(option.MinNotional),
		MaxPrice: option.MaxPrice, MinPrice: option.MinPrice,
		TickScheme: option.TickScheme, Info: option.Info,
		TsEvent: option.TsEvent, TsInit: option.TsInit,
	})
}

func (option *CryptoOption) UnmarshalJSON(data []byte) error {
	var wire cryptoOptionWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	underlying, err := currencyFromCrypto(wire.Underlying)
	if err != nil {
		return err
	}
	quote, err := currencyFromCrypto(wire.QuoteCurrency)
	if err != nil {
		return err
	}
	settlement, err := currencyFromCrypto(wire.SettlementCurrency)
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
	*option = CryptoOption{
		InstrumentID: wire.InstrumentID, RawSymbol: wire.RawSymbol,
		Underlying: underlying, QuoteCurrency: quote, SettlementCurrency: settlement,
		Inverse: wire.Inverse, OptionKind: wire.OptionKind, StrikePrice: wire.StrikePrice,
		Activation: wire.Activation, Expiration: wire.Expiration,
		PricePrecision: wire.PricePrecision, SizePrecision: wire.SizePrecision,
		PriceIncrement: wire.PriceIncrement, SizeIncrement: wire.SizeIncrement,
		Multiplier: wire.Multiplier, LotSize: wire.LotSize,
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

type CryptoOptionBuilder struct{ config CryptoOptionConfig }

func NewCryptoOptionBuilder() *CryptoOptionBuilder { return &CryptoOptionBuilder{} }
func (builder *CryptoOptionBuilder) Instrument(value ids.InstrumentID) *CryptoOptionBuilder {
	builder.config.InstrumentID = value
	return builder
}
func (builder *CryptoOptionBuilder) Symbol(value ids.Symbol) *CryptoOptionBuilder {
	builder.config.RawSymbol = value
	return builder
}
func (builder *CryptoOptionBuilder) Currencies(underlying, quote, settlement currency.Currency) *CryptoOptionBuilder {
	builder.config.Underlying, builder.config.QuoteCurrency = underlying, quote
	builder.config.SettlementCurrency = settlement
	return builder
}
func (builder *CryptoOptionBuilder) IsInverse(value bool) *CryptoOptionBuilder {
	builder.config.Inverse = value
	return builder
}
func (builder *CryptoOptionBuilder) Contract(kind OptionKind, strike decimal.Price) *CryptoOptionBuilder {
	builder.config.OptionKind, builder.config.StrikePrice = kind, strike
	return builder
}
func (builder *CryptoOptionBuilder) ActiveBetween(activation, expiration uint64) *CryptoOptionBuilder {
	builder.config.Activation, builder.config.Expiration = activation, expiration
	return builder
}
func (builder *CryptoOptionBuilder) Precisions(price, size uint8) *CryptoOptionBuilder {
	builder.config.PricePrecision, builder.config.SizePrecision = price, size
	return builder
}
func (builder *CryptoOptionBuilder) Increments(price decimal.Price, size decimal.Quantity) *CryptoOptionBuilder {
	builder.config.PriceIncrement, builder.config.SizeIncrement = price, size
	return builder
}
func (builder *CryptoOptionBuilder) Sizing(multiplier, lotSize decimal.Quantity) *CryptoOptionBuilder {
	builder.config.Multiplier, builder.config.LotSize = &multiplier, &lotSize
	return builder
}
func (builder *CryptoOptionBuilder) QuantityLimits(maximum, minimum decimal.Quantity) *CryptoOptionBuilder {
	builder.config.MaxQuantity, builder.config.MinQuantity = &maximum, &minimum
	return builder
}
func (builder *CryptoOptionBuilder) NotionalLimits(maximum, minimum money.Money) *CryptoOptionBuilder {
	builder.config.MaxNotional, builder.config.MinNotional = &maximum, &minimum
	return builder
}
func (builder *CryptoOptionBuilder) PriceLimits(maximum, minimum decimal.Price) *CryptoOptionBuilder {
	builder.config.MaxPrice, builder.config.MinPrice = &maximum, &minimum
	return builder
}
func (builder *CryptoOptionBuilder) Margins(initial, maintenance decimal.Decimal) *CryptoOptionBuilder {
	builder.config.MarginInit, builder.config.MarginMaint = &initial, &maintenance
	return builder
}
func (builder *CryptoOptionBuilder) Fees(maker, taker decimal.Decimal) *CryptoOptionBuilder {
	builder.config.MakerFee, builder.config.TakerFee = &maker, &taker
	return builder
}
func (builder *CryptoOptionBuilder) Timestamps(event, init uint64) *CryptoOptionBuilder {
	builder.config.TsEvent, builder.config.TsInit = event, init
	return builder
}
func (builder *CryptoOptionBuilder) Build() (CryptoOption, error) {
	return NewCheckedCryptoOption(builder.config)
}
