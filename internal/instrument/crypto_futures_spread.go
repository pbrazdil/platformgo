package instrument

import (
	"encoding/json"

	"github.com/upcomers-org/platformgo/internal/currency"
	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
	"github.com/upcomers-org/platformgo/internal/money"
)

type CryptoFuturesSpreadConfig struct {
	Future       CryptoFutureConfig
	StrategyType string
}

type CryptoFuturesSpread struct {
	Future       CryptoFuture
	StrategyType string
}

func NewCheckedCryptoFuturesSpread(config CryptoFuturesSpreadConfig) (CryptoFuturesSpread, error) {
	if err := validateASCIIText(config.StrategyType, "strategy_type"); err != nil {
		return CryptoFuturesSpread{}, err
	}
	future, err := NewCheckedCryptoFuture(config.Future)
	if err != nil {
		return CryptoFuturesSpread{}, err
	}
	return CryptoFuturesSpread{Future: future, StrategyType: config.StrategyType}, nil
}

func (spread CryptoFuturesSpread) InstrumentID() ids.InstrumentID {
	return spread.Future.InstrumentID()
}
func (spread CryptoFuturesSpread) AssetClass() AssetClass { return AssetClassCryptocurrency }
func (spread CryptoFuturesSpread) InstrumentClass() InstrumentClass {
	return InstrumentClassFuturesSpread
}
func (spread CryptoFuturesSpread) QuoteCurrency() currency.Currency {
	return spread.Future.QuoteCurrency()
}
func (spread CryptoFuturesSpread) SettlementCurrency() currency.Currency {
	return spread.Future.SettlementCurrency()
}
func (spread CryptoFuturesSpread) IsInverse() bool { return spread.Future.IsInverse() }
func (spread CryptoFuturesSpread) ActivationNanos() *uint64 {
	return spread.Future.ActivationNanos()
}
func (spread CryptoFuturesSpread) ExpirationNanos() *uint64 {
	return spread.Future.ExpirationNanos()
}
func (spread CryptoFuturesSpread) StrategyTypeValue() *string {
	value := spread.StrategyType
	return &value
}
func (spread CryptoFuturesSpread) Equal(other CryptoFuturesSpread) bool {
	return spread.InstrumentID() == other.InstrumentID()
}

type CryptoFuturesSpreadBuilder struct{ config CryptoFuturesSpreadConfig }

func NewCryptoFuturesSpreadBuilder() *CryptoFuturesSpreadBuilder {
	return &CryptoFuturesSpreadBuilder{}
}
func (builder *CryptoFuturesSpreadBuilder) Instrument(value ids.InstrumentID) *CryptoFuturesSpreadBuilder {
	builder.config.Future.InstrumentID = value
	return builder
}
func (builder *CryptoFuturesSpreadBuilder) Symbol(value ids.Symbol) *CryptoFuturesSpreadBuilder {
	builder.config.Future.RawSymbol = value
	return builder
}
func (builder *CryptoFuturesSpreadBuilder) Currencies(underlying, quote, settlement currency.Currency) *CryptoFuturesSpreadBuilder {
	builder.config.Future.Underlying, builder.config.Future.QuoteCurrency,
		builder.config.Future.SettlementCurrency = underlying, quote, settlement
	return builder
}
func (builder *CryptoFuturesSpreadBuilder) IsInverse(value bool) *CryptoFuturesSpreadBuilder {
	builder.config.Future.Inverse = value
	return builder
}
func (builder *CryptoFuturesSpreadBuilder) WithStrategy(value string) *CryptoFuturesSpreadBuilder {
	builder.config.StrategyType = value
	return builder
}
func (builder *CryptoFuturesSpreadBuilder) ActiveBetween(activation, expiration uint64) *CryptoFuturesSpreadBuilder {
	builder.config.Future.Activation, builder.config.Future.Expiration = activation, expiration
	return builder
}
func (builder *CryptoFuturesSpreadBuilder) Precisions(price, size uint8) *CryptoFuturesSpreadBuilder {
	builder.config.Future.PricePrecision, builder.config.Future.SizePrecision = price, size
	return builder
}
func (builder *CryptoFuturesSpreadBuilder) Increments(price decimal.Price, size decimal.Quantity) *CryptoFuturesSpreadBuilder {
	builder.config.Future.PriceIncrement, builder.config.Future.SizeIncrement = price, size
	return builder
}
func (builder *CryptoFuturesSpreadBuilder) WithMultiplier(value decimal.Quantity) *CryptoFuturesSpreadBuilder {
	builder.config.Future.Multiplier = &value
	return builder
}
func (builder *CryptoFuturesSpreadBuilder) WithLotSize(value decimal.Quantity) *CryptoFuturesSpreadBuilder {
	builder.config.Future.LotSize = &value
	return builder
}
func (builder *CryptoFuturesSpreadBuilder) QuantityLimits(maximum, minimum decimal.Quantity) *CryptoFuturesSpreadBuilder {
	builder.config.Future.MaxQuantity, builder.config.Future.MinQuantity = &maximum, &minimum
	return builder
}
func (builder *CryptoFuturesSpreadBuilder) NotionalLimits(maximum, minimum money.Money) *CryptoFuturesSpreadBuilder {
	builder.config.Future.MaxNotional, builder.config.Future.MinNotional = &maximum, &minimum
	return builder
}
func (builder *CryptoFuturesSpreadBuilder) PriceLimits(maximum, minimum decimal.Price) *CryptoFuturesSpreadBuilder {
	builder.config.Future.MaxPrice, builder.config.Future.MinPrice = &maximum, &minimum
	return builder
}
func (builder *CryptoFuturesSpreadBuilder) Margins(initial, maintenance decimal.Decimal) *CryptoFuturesSpreadBuilder {
	builder.config.Future.MarginInit, builder.config.Future.MarginMaint = &initial, &maintenance
	return builder
}
func (builder *CryptoFuturesSpreadBuilder) Fees(maker, taker decimal.Decimal) *CryptoFuturesSpreadBuilder {
	builder.config.Future.MakerFee, builder.config.Future.TakerFee = &maker, &taker
	return builder
}
func (builder *CryptoFuturesSpreadBuilder) Timestamps(event, init uint64) *CryptoFuturesSpreadBuilder {
	builder.config.Future.TsEvent, builder.config.Future.TsInit = event, init
	return builder
}
func (builder *CryptoFuturesSpreadBuilder) Build() (CryptoFuturesSpread, error) {
	return NewCheckedCryptoFuturesSpread(builder.config)
}

type cryptoFuturesSpreadWire struct {
	Future       json.RawMessage
	StrategyType string
}

func (spread CryptoFuturesSpread) MarshalJSON() ([]byte, error) {
	future, err := json.Marshal(spread.Future)
	if err != nil {
		return nil, err
	}
	return json.Marshal(cryptoFuturesSpreadWire{Future: future, StrategyType: spread.StrategyType})
}

func (spread *CryptoFuturesSpread) UnmarshalJSON(data []byte) error {
	var wire cryptoFuturesSpreadWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	var future CryptoFuture
	if err := json.Unmarshal(wire.Future, &future); err != nil {
		return err
	}
	*spread = CryptoFuturesSpread{Future: future, StrategyType: wire.StrategyType}
	return nil
}
