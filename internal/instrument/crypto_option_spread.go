package instrument

import (
	"encoding/json"

	"github.com/upcomers-org/platformgo/internal/currency"
	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
	"github.com/upcomers-org/platformgo/internal/money"
)

type CryptoOptionSpreadConfig = CryptoFuturesSpreadConfig

type CryptoOptionSpread struct {
	Spread CryptoFuturesSpread
}

func NewCheckedCryptoOptionSpread(config CryptoOptionSpreadConfig) (CryptoOptionSpread, error) {
	spread, err := NewCheckedCryptoFuturesSpread(config)
	if err != nil {
		return CryptoOptionSpread{}, err
	}
	return CryptoOptionSpread{Spread: spread}, nil
}

func (spread CryptoOptionSpread) InstrumentID() ids.InstrumentID {
	return spread.Spread.InstrumentID()
}
func (spread CryptoOptionSpread) AssetClass() AssetClass { return AssetClassCryptocurrency }
func (spread CryptoOptionSpread) InstrumentClass() InstrumentClass {
	return InstrumentClassOptionSpread
}
func (spread CryptoOptionSpread) QuoteCurrency() currency.Currency {
	return spread.Spread.QuoteCurrency()
}
func (spread CryptoOptionSpread) SettlementCurrency() currency.Currency {
	return spread.Spread.SettlementCurrency()
}
func (spread CryptoOptionSpread) IsInverse() bool { return spread.Spread.IsInverse() }
func (spread CryptoOptionSpread) ActivationNanos() *uint64 {
	return spread.Spread.ActivationNanos()
}
func (spread CryptoOptionSpread) ExpirationNanos() *uint64 {
	return spread.Spread.ExpirationNanos()
}
func (spread CryptoOptionSpread) Equal(other CryptoOptionSpread) bool {
	return spread.InstrumentID() == other.InstrumentID()
}

type CryptoOptionSpreadBuilder struct{ config CryptoOptionSpreadConfig }

func NewCryptoOptionSpreadBuilder() *CryptoOptionSpreadBuilder {
	return &CryptoOptionSpreadBuilder{}
}
func (builder *CryptoOptionSpreadBuilder) Instrument(value ids.InstrumentID) *CryptoOptionSpreadBuilder {
	builder.config.Future.InstrumentID = value
	return builder
}
func (builder *CryptoOptionSpreadBuilder) Symbol(value ids.Symbol) *CryptoOptionSpreadBuilder {
	builder.config.Future.RawSymbol = value
	return builder
}
func (builder *CryptoOptionSpreadBuilder) Currencies(underlying, quote, settlement currency.Currency) *CryptoOptionSpreadBuilder {
	builder.config.Future.Underlying, builder.config.Future.QuoteCurrency,
		builder.config.Future.SettlementCurrency = underlying, quote, settlement
	return builder
}
func (builder *CryptoOptionSpreadBuilder) IsInverse(value bool) *CryptoOptionSpreadBuilder {
	builder.config.Future.Inverse = value
	return builder
}
func (builder *CryptoOptionSpreadBuilder) WithStrategy(value string) *CryptoOptionSpreadBuilder {
	builder.config.StrategyType = value
	return builder
}
func (builder *CryptoOptionSpreadBuilder) ActiveBetween(activation, expiration uint64) *CryptoOptionSpreadBuilder {
	builder.config.Future.Activation, builder.config.Future.Expiration = activation, expiration
	return builder
}
func (builder *CryptoOptionSpreadBuilder) Precisions(price, size uint8) *CryptoOptionSpreadBuilder {
	builder.config.Future.PricePrecision, builder.config.Future.SizePrecision = price, size
	return builder
}
func (builder *CryptoOptionSpreadBuilder) Increments(price decimal.Price, size decimal.Quantity) *CryptoOptionSpreadBuilder {
	builder.config.Future.PriceIncrement, builder.config.Future.SizeIncrement = price, size
	return builder
}
func (builder *CryptoOptionSpreadBuilder) WithMultiplier(value decimal.Quantity) *CryptoOptionSpreadBuilder {
	builder.config.Future.Multiplier = &value
	return builder
}
func (builder *CryptoOptionSpreadBuilder) WithLotSize(value decimal.Quantity) *CryptoOptionSpreadBuilder {
	builder.config.Future.LotSize = &value
	return builder
}
func (builder *CryptoOptionSpreadBuilder) QuantityLimits(maximum, minimum decimal.Quantity) *CryptoOptionSpreadBuilder {
	builder.config.Future.MaxQuantity, builder.config.Future.MinQuantity = &maximum, &minimum
	return builder
}
func (builder *CryptoOptionSpreadBuilder) NotionalLimits(maximum, minimum money.Money) *CryptoOptionSpreadBuilder {
	builder.config.Future.MaxNotional, builder.config.Future.MinNotional = &maximum, &minimum
	return builder
}
func (builder *CryptoOptionSpreadBuilder) PriceLimits(maximum, minimum decimal.Price) *CryptoOptionSpreadBuilder {
	builder.config.Future.MaxPrice, builder.config.Future.MinPrice = &maximum, &minimum
	return builder
}
func (builder *CryptoOptionSpreadBuilder) Margins(initial, maintenance decimal.Decimal) *CryptoOptionSpreadBuilder {
	builder.config.Future.MarginInit, builder.config.Future.MarginMaint = &initial, &maintenance
	return builder
}
func (builder *CryptoOptionSpreadBuilder) Fees(maker, taker decimal.Decimal) *CryptoOptionSpreadBuilder {
	builder.config.Future.MakerFee, builder.config.Future.TakerFee = &maker, &taker
	return builder
}
func (builder *CryptoOptionSpreadBuilder) Timestamps(event, init uint64) *CryptoOptionSpreadBuilder {
	builder.config.Future.TsEvent, builder.config.Future.TsInit = event, init
	return builder
}
func (builder *CryptoOptionSpreadBuilder) Build() (CryptoOptionSpread, error) {
	return NewCheckedCryptoOptionSpread(builder.config)
}

func (spread CryptoOptionSpread) MarshalJSON() ([]byte, error) {
	return json.Marshal(spread.Spread)
}

func (spread *CryptoOptionSpread) UnmarshalJSON(data []byte) error {
	return json.Unmarshal(data, &spread.Spread)
}
