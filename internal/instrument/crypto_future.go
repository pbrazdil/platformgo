package instrument

import (
	"encoding/json"

	"github.com/upcomers-org/platformgo/internal/currency"
	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
	"github.com/upcomers-org/platformgo/internal/money"
)

type CryptoFutureConfig struct {
	InstrumentID       ids.InstrumentID
	RawSymbol          ids.Symbol
	Underlying         currency.Currency
	QuoteCurrency      currency.Currency
	SettlementCurrency currency.Currency
	Inverse            bool
	Activation         uint64
	Expiration         uint64
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

type CryptoFuture struct {
	Contract   CryptoPerpetual
	Underlying currency.Currency
	Activation uint64
	Expiration uint64
}

func NewCheckedCryptoFuture(config CryptoFutureConfig) (CryptoFuture, error) {
	contract, err := NewCheckedCryptoPerpetual(CryptoPerpetualConfig{
		InstrumentID: config.InstrumentID, RawSymbol: config.RawSymbol,
		BaseCurrency: config.Underlying, QuoteCurrency: config.QuoteCurrency,
		SettlementCurrency: config.SettlementCurrency, Inverse: config.Inverse,
		PricePrecision: config.PricePrecision, SizePrecision: config.SizePrecision,
		PriceIncrement: config.PriceIncrement, SizeIncrement: config.SizeIncrement,
		Multiplier: config.Multiplier, LotSize: config.LotSize,
		MaxQuantity: config.MaxQuantity, MinQuantity: config.MinQuantity,
		MaxNotional: config.MaxNotional, MinNotional: config.MinNotional,
		MaxPrice: config.MaxPrice, MinPrice: config.MinPrice,
		MarginInit: config.MarginInit, MarginMaint: config.MarginMaint,
		MakerFee: config.MakerFee, TakerFee: config.TakerFee,
		TickScheme: config.TickScheme, Info: config.Info,
		TsEvent: config.TsEvent, TsInit: config.TsInit,
	})
	if err != nil {
		return CryptoFuture{}, err
	}
	return CryptoFuture{
		Contract: contract, Underlying: config.Underlying,
		Activation: config.Activation, Expiration: config.Expiration,
	}, nil
}

func (future CryptoFuture) InstrumentID() ids.InstrumentID { return future.Contract.InstrumentID }
func (future CryptoFuture) AssetClass() AssetClass         { return AssetClassCryptocurrency }
func (future CryptoFuture) InstrumentClass() InstrumentClass {
	return InstrumentClassFuture
}
func (future CryptoFuture) BaseCurrency() *currency.Currency {
	value := future.Underlying
	return &value
}
func (future CryptoFuture) QuoteCurrency() currency.Currency {
	return future.Contract.QuoteCurrency
}
func (future CryptoFuture) SettlementCurrency() currency.Currency {
	return future.Contract.SettlementCurrency
}
func (future CryptoFuture) IsInverse() bool { return future.Contract.Inverse }
func (future CryptoFuture) UnderlyingValue() *string {
	value := future.Underlying.Code
	return &value
}
func (future CryptoFuture) ActivationNanos() *uint64 {
	value := future.Activation
	return &value
}
func (future CryptoFuture) ExpirationNanos() *uint64 {
	value := future.Expiration
	return &value
}
func (future CryptoFuture) Equal(other CryptoFuture) bool {
	return future.Contract.InstrumentID == other.Contract.InstrumentID
}

type CryptoFutureBuilder struct{ config CryptoFutureConfig }

func NewCryptoFutureBuilder() *CryptoFutureBuilder { return &CryptoFutureBuilder{} }
func (builder *CryptoFutureBuilder) Instrument(value ids.InstrumentID) *CryptoFutureBuilder {
	builder.config.InstrumentID = value
	return builder
}
func (builder *CryptoFutureBuilder) Symbol(value ids.Symbol) *CryptoFutureBuilder {
	builder.config.RawSymbol = value
	return builder
}
func (builder *CryptoFutureBuilder) Currencies(underlying, quote, settlement currency.Currency) *CryptoFutureBuilder {
	builder.config.Underlying, builder.config.QuoteCurrency, builder.config.SettlementCurrency =
		underlying, quote, settlement
	return builder
}
func (builder *CryptoFutureBuilder) IsInverse(value bool) *CryptoFutureBuilder {
	builder.config.Inverse = value
	return builder
}
func (builder *CryptoFutureBuilder) ActiveBetween(activation, expiration uint64) *CryptoFutureBuilder {
	builder.config.Activation, builder.config.Expiration = activation, expiration
	return builder
}
func (builder *CryptoFutureBuilder) Precisions(price, size uint8) *CryptoFutureBuilder {
	builder.config.PricePrecision, builder.config.SizePrecision = price, size
	return builder
}
func (builder *CryptoFutureBuilder) Increments(price decimal.Price, size decimal.Quantity) *CryptoFutureBuilder {
	builder.config.PriceIncrement, builder.config.SizeIncrement = price, size
	return builder
}
func (builder *CryptoFutureBuilder) WithMultiplier(value decimal.Quantity) *CryptoFutureBuilder {
	builder.config.Multiplier = &value
	return builder
}
func (builder *CryptoFutureBuilder) WithLotSize(value decimal.Quantity) *CryptoFutureBuilder {
	builder.config.LotSize = &value
	return builder
}
func (builder *CryptoFutureBuilder) QuantityLimits(maximum, minimum decimal.Quantity) *CryptoFutureBuilder {
	builder.config.MaxQuantity, builder.config.MinQuantity = &maximum, &minimum
	return builder
}
func (builder *CryptoFutureBuilder) NotionalLimits(maximum, minimum money.Money) *CryptoFutureBuilder {
	builder.config.MaxNotional, builder.config.MinNotional = &maximum, &minimum
	return builder
}
func (builder *CryptoFutureBuilder) PriceLimits(maximum, minimum decimal.Price) *CryptoFutureBuilder {
	builder.config.MaxPrice, builder.config.MinPrice = &maximum, &minimum
	return builder
}
func (builder *CryptoFutureBuilder) Margins(initial, maintenance decimal.Decimal) *CryptoFutureBuilder {
	builder.config.MarginInit, builder.config.MarginMaint = &initial, &maintenance
	return builder
}
func (builder *CryptoFutureBuilder) Fees(maker, taker decimal.Decimal) *CryptoFutureBuilder {
	builder.config.MakerFee, builder.config.TakerFee = &maker, &taker
	return builder
}
func (builder *CryptoFutureBuilder) Timestamps(event, init uint64) *CryptoFutureBuilder {
	builder.config.TsEvent, builder.config.TsInit = event, init
	return builder
}
func (builder *CryptoFutureBuilder) Build() (CryptoFuture, error) {
	return NewCheckedCryptoFuture(builder.config)
}

type cryptoFutureWire struct {
	Contract               cryptoPerpetualWire
	Underlying             cryptoCurrencyWire
	Activation, Expiration uint64
}

func (future CryptoFuture) MarshalJSON() ([]byte, error) {
	contractJSON, err := json.Marshal(future.Contract)
	if err != nil {
		return nil, err
	}
	var contract cryptoPerpetualWire
	if err := json.Unmarshal(contractJSON, &contract); err != nil {
		return nil, err
	}
	return json.Marshal(cryptoFutureWire{
		Contract: contract, Underlying: cryptoCurrency(future.Underlying),
		Activation: future.Activation, Expiration: future.Expiration,
	})
}

func (future *CryptoFuture) UnmarshalJSON(data []byte) error {
	var wire cryptoFutureWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	contractJSON, err := json.Marshal(wire.Contract)
	if err != nil {
		return err
	}
	var contract CryptoPerpetual
	if err := json.Unmarshal(contractJSON, &contract); err != nil {
		return err
	}
	underlying, err := currencyFromCrypto(wire.Underlying)
	if err != nil {
		return err
	}
	*future = CryptoFuture{
		Contract: contract, Underlying: underlying,
		Activation: wire.Activation, Expiration: wire.Expiration,
	}
	return nil
}
