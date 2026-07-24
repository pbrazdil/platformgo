package instrument

import (
	"encoding/json"
	"fmt"
	"math/big"

	"github.com/upcomers-org/platformgo/internal/currency"
	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
	"github.com/upcomers-org/platformgo/internal/money"
)

type AssetClass string
type InstrumentClass string

const (
	AssetClassCryptocurrency AssetClass      = "CRYPTOCURRENCY"
	InstrumentClassSwap      InstrumentClass = "SWAP"
)

type CryptoPerpetualConfig struct {
	InstrumentID       ids.InstrumentID
	RawSymbol          ids.Symbol
	BaseCurrency       currency.Currency
	QuoteCurrency      currency.Currency
	SettlementCurrency currency.Currency
	Inverse            bool
	PricePrecision     uint8
	SizePrecision      uint8
	PriceIncrement     decimal.Price
	SizeIncrement      decimal.Quantity
	Multiplier         *decimal.Quantity
	LotSize            *decimal.Quantity
	MaxQuantity        *decimal.Quantity
	MinQuantity        *decimal.Quantity
	MaxNotional        *money.Money
	MinNotional        *money.Money
	MaxPrice           *decimal.Price
	MinPrice           *decimal.Price
	MarginInit         *decimal.Decimal
	MarginMaint        *decimal.Decimal
	MakerFee           *decimal.Decimal
	TakerFee           *decimal.Decimal
	TickScheme         *string
	Info               map[string]any
	TsEvent            uint64
	TsInit             uint64
}

type CryptoPerpetual struct {
	InstrumentID       ids.InstrumentID
	RawSymbol          ids.Symbol
	BaseCurrency       currency.Currency
	QuoteCurrency      currency.Currency
	SettlementCurrency currency.Currency
	Inverse            bool
	PricePrecision     uint8
	SizePrecision      uint8
	PriceIncrement     decimal.Price
	SizeIncrement      decimal.Quantity
	Multiplier         decimal.Quantity
	LotSize            decimal.Quantity
	MarginInit         decimal.Decimal
	MarginMaint        decimal.Decimal
	MakerFee           decimal.Decimal
	TakerFee           decimal.Decimal
	MaxQuantity        *decimal.Quantity
	MinQuantity        *decimal.Quantity
	MaxNotional        *money.Money
	MinNotional        *money.Money
	MaxPrice           *decimal.Price
	MinPrice           *decimal.Price
	TickScheme         *string
	Info               map[string]any
	TsEvent            uint64
	TsInit             uint64
}

func NewCheckedCryptoPerpetual(config CryptoPerpetualConfig) (CryptoPerpetual, error) {
	if config.PricePrecision != config.PriceIncrement.Precision() {
		return CryptoPerpetual{}, fmt.Errorf(
			"price_precision %d was not equal to price_increment.precision %d",
			config.PricePrecision,
			config.PriceIncrement.Precision(),
		)
	}
	if config.SizePrecision != config.SizeIncrement.Precision() {
		return CryptoPerpetual{}, fmt.Errorf(
			"size_precision %d was not equal to size_increment.precision %d",
			config.SizePrecision,
			config.SizeIncrement.Precision(),
		)
	}
	if err := config.PriceIncrement.RequirePositive("price_increment"); err != nil {
		return CryptoPerpetual{}, err
	}
	if err := config.SizeIncrement.RequirePositive("size_increment"); err != nil {
		return CryptoPerpetual{}, err
	}
	if config.TickScheme != nil {
		if _, err := ParseTickScheme(*config.TickScheme); err != nil {
			return CryptoPerpetual{}, err
		}
	}
	one := decimal.MustQuantity("1")
	multiplier := one
	if config.Multiplier != nil {
		if err := config.Multiplier.RequirePositive("multiplier"); err != nil {
			return CryptoPerpetual{}, err
		}
		multiplier = *config.Multiplier
	}
	lotSize := one
	if config.LotSize != nil {
		if err := config.LotSize.RequirePositive("lot_size"); err != nil {
			return CryptoPerpetual{}, err
		}
		lotSize = *config.LotSize
	}
	return CryptoPerpetual{
		InstrumentID: config.InstrumentID, RawSymbol: config.RawSymbol,
		BaseCurrency: config.BaseCurrency, QuoteCurrency: config.QuoteCurrency,
		SettlementCurrency: config.SettlementCurrency, Inverse: config.Inverse,
		PricePrecision: config.PricePrecision, SizePrecision: config.SizePrecision,
		PriceIncrement: config.PriceIncrement, SizeIncrement: config.SizeIncrement,
		Multiplier: multiplier, LotSize: lotSize,
		MarginInit: decimalValue(config.MarginInit), MarginMaint: decimalValue(config.MarginMaint),
		MakerFee: decimalValue(config.MakerFee), TakerFee: decimalValue(config.TakerFee),
		MaxQuantity: copyValue(config.MaxQuantity), MinQuantity: copyValue(config.MinQuantity),
		MaxNotional: copyValue(config.MaxNotional), MinNotional: copyValue(config.MinNotional),
		MaxPrice: copyValue(config.MaxPrice), MinPrice: copyValue(config.MinPrice),
		TickScheme: copyValue(config.TickScheme), Info: cloneInfo(config.Info),
		TsEvent: config.TsEvent, TsInit: config.TsInit,
	}, nil
}

func decimalValue(value *decimal.Decimal) decimal.Decimal {
	if value == nil {
		return decimal.Decimal{}
	}
	return *value
}

func copyValue[T any](value *T) *T {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneInfo(info map[string]any) map[string]any {
	if info == nil {
		return nil
	}
	result := make(map[string]any, len(info))
	for key, value := range info {
		result[key] = value
	}
	return result
}

func (perpetual CryptoPerpetual) AssetClass() AssetClass { return AssetClassCryptocurrency }
func (perpetual CryptoPerpetual) InstrumentClass() InstrumentClass {
	return InstrumentClassSwap
}
func (perpetual CryptoPerpetual) CostCurrency() currency.Currency {
	if perpetual.Inverse {
		return perpetual.SettlementCurrency
	}
	return perpetual.QuoteCurrency
}
func (perpetual CryptoPerpetual) Underlying() *string         { return nil }
func (perpetual CryptoPerpetual) OptionKind() *string         { return nil }
func (perpetual CryptoPerpetual) StrikePrice() *decimal.Price { return nil }
func (perpetual CryptoPerpetual) ActivationNanos() *uint64    { return nil }
func (perpetual CryptoPerpetual) ExpirationNanos() *uint64    { return nil }
func (perpetual CryptoPerpetual) Equal(other CryptoPerpetual) bool {
	return perpetual.InstrumentID == other.InstrumentID
}

func (perpetual CryptoPerpetual) String() string {
	return fmt.Sprintf(
		"CryptoPerpetual(id=%s, raw_symbol=%s, inverse=%t, price_increment=%s, size_increment=%s)",
		perpetual.InstrumentID, perpetual.RawSymbol, perpetual.Inverse,
		perpetual.PriceIncrement, perpetual.SizeIncrement,
	)
}

type CryptoPerpetualBuilder struct{ config CryptoPerpetualConfig }

func NewCryptoPerpetualBuilder() *CryptoPerpetualBuilder { return &CryptoPerpetualBuilder{} }
func (builder *CryptoPerpetualBuilder) Instrument(value ids.InstrumentID) *CryptoPerpetualBuilder {
	builder.config.InstrumentID = value
	return builder
}
func (builder *CryptoPerpetualBuilder) Symbol(value ids.Symbol) *CryptoPerpetualBuilder {
	builder.config.RawSymbol = value
	return builder
}
func (builder *CryptoPerpetualBuilder) Base(value currency.Currency) *CryptoPerpetualBuilder {
	builder.config.BaseCurrency = value
	return builder
}
func (builder *CryptoPerpetualBuilder) Quote(value currency.Currency) *CryptoPerpetualBuilder {
	builder.config.QuoteCurrency = value
	return builder
}
func (builder *CryptoPerpetualBuilder) Settlement(value currency.Currency) *CryptoPerpetualBuilder {
	builder.config.SettlementCurrency = value
	return builder
}
func (builder *CryptoPerpetualBuilder) IsInverse(value bool) *CryptoPerpetualBuilder {
	builder.config.Inverse = value
	return builder
}
func (builder *CryptoPerpetualBuilder) PriceDigits(value uint8) *CryptoPerpetualBuilder {
	builder.config.PricePrecision = value
	return builder
}
func (builder *CryptoPerpetualBuilder) SizeDigits(value uint8) *CryptoPerpetualBuilder {
	builder.config.SizePrecision = value
	return builder
}
func (builder *CryptoPerpetualBuilder) TickSize(value decimal.Price) *CryptoPerpetualBuilder {
	builder.config.PriceIncrement = value
	return builder
}
func (builder *CryptoPerpetualBuilder) StepSize(value decimal.Quantity) *CryptoPerpetualBuilder {
	builder.config.SizeIncrement = value
	return builder
}
func (builder *CryptoPerpetualBuilder) WithMultiplier(value decimal.Quantity) *CryptoPerpetualBuilder {
	builder.config.Multiplier = &value
	return builder
}
func (builder *CryptoPerpetualBuilder) WithLotSize(value decimal.Quantity) *CryptoPerpetualBuilder {
	builder.config.LotSize = &value
	return builder
}
func (builder *CryptoPerpetualBuilder) WithMaxQuantity(value decimal.Quantity) *CryptoPerpetualBuilder {
	builder.config.MaxQuantity = &value
	return builder
}
func (builder *CryptoPerpetualBuilder) WithMinNotional(value *money.Money) *CryptoPerpetualBuilder {
	builder.config.MinNotional = copyValue(value)
	return builder
}
func (builder *CryptoPerpetualBuilder) WithMakerFee(value decimal.Decimal) *CryptoPerpetualBuilder {
	builder.config.MakerFee = &value
	return builder
}
func (builder *CryptoPerpetualBuilder) Timestamps(event, init uint64) *CryptoPerpetualBuilder {
	builder.config.TsEvent, builder.config.TsInit = event, init
	return builder
}
func (builder *CryptoPerpetualBuilder) Build() (CryptoPerpetual, error) {
	return NewCheckedCryptoPerpetual(builder.config)
}

type cryptoCurrencyWire struct {
	Code      string
	Precision uint8
	ISO4217   uint16
	Name      string
	Type      currency.Type
}
type cryptoMoneyWire struct {
	Raw      string
	Currency cryptoCurrencyWire
}
type cryptoPerpetualWire struct {
	InstrumentID                                    ids.InstrumentID
	RawSymbol                                       ids.Symbol
	BaseCurrency, QuoteCurrency, SettlementCurrency cryptoCurrencyWire
	Inverse                                         bool
	PricePrecision, SizePrecision                   uint8
	PriceIncrement                                  decimal.Price
	SizeIncrement, Multiplier, LotSize              decimal.Quantity
	MarginInit, MarginMaint, MakerFee, TakerFee     string
	MaxQuantity, MinQuantity                        *decimal.Quantity
	MaxNotional, MinNotional                        *cryptoMoneyWire
	MaxPrice, MinPrice                              *decimal.Price
	TickScheme                                      *string
	Info                                            map[string]any
	TsEvent, TsInit                                 uint64
}

func cryptoCurrency(value currency.Currency) cryptoCurrencyWire {
	return cryptoCurrencyWire{value.Code, value.Precision, value.ISO4217, value.Name, value.Type}
}
func currencyFromCrypto(value cryptoCurrencyWire) (currency.Currency, error) {
	return currency.New(value.Code, value.Precision, value.ISO4217, value.Name, value.Type)
}
func cryptoMoney(value *money.Money) *cryptoMoneyWire {
	if value == nil {
		return nil
	}
	return &cryptoMoneyWire{value.Raw().String(), cryptoCurrency(value.Currency())}
}
func moneyFromCrypto(value *cryptoMoneyWire) (*money.Money, error) {
	if value == nil {
		return nil, nil
	}
	denomination, err := currencyFromCrypto(value.Currency)
	if err != nil {
		return nil, err
	}
	raw, ok := new(big.Int).SetString(value.Raw, 10)
	if !ok {
		return nil, fmt.Errorf("invalid money raw %q", value.Raw)
	}
	result, err := money.FromRawChecked(raw, denomination)
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func (perpetual CryptoPerpetual) MarshalJSON() ([]byte, error) {
	return json.Marshal(cryptoPerpetualWire{
		InstrumentID: perpetual.InstrumentID, RawSymbol: perpetual.RawSymbol,
		BaseCurrency:       cryptoCurrency(perpetual.BaseCurrency),
		QuoteCurrency:      cryptoCurrency(perpetual.QuoteCurrency),
		SettlementCurrency: cryptoCurrency(perpetual.SettlementCurrency),
		Inverse:            perpetual.Inverse, PricePrecision: perpetual.PricePrecision,
		SizePrecision: perpetual.SizePrecision, PriceIncrement: perpetual.PriceIncrement,
		SizeIncrement: perpetual.SizeIncrement, Multiplier: perpetual.Multiplier, LotSize: perpetual.LotSize,
		MarginInit: perpetual.MarginInit.String(), MarginMaint: perpetual.MarginMaint.String(),
		MakerFee: perpetual.MakerFee.String(), TakerFee: perpetual.TakerFee.String(),
		MaxQuantity: perpetual.MaxQuantity, MinQuantity: perpetual.MinQuantity,
		MaxNotional: cryptoMoney(perpetual.MaxNotional), MinNotional: cryptoMoney(perpetual.MinNotional),
		MaxPrice: perpetual.MaxPrice, MinPrice: perpetual.MinPrice, TickScheme: perpetual.TickScheme,
		Info: perpetual.Info, TsEvent: perpetual.TsEvent, TsInit: perpetual.TsInit,
	})
}

func (perpetual *CryptoPerpetual) UnmarshalJSON(data []byte) error {
	var wire cryptoPerpetualWire
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
	*perpetual = CryptoPerpetual{
		InstrumentID: wire.InstrumentID, RawSymbol: wire.RawSymbol,
		BaseCurrency: base, QuoteCurrency: quote, SettlementCurrency: settlement,
		Inverse: wire.Inverse, PricePrecision: wire.PricePrecision, SizePrecision: wire.SizePrecision,
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
