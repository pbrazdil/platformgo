package instrument

import (
	"encoding/json"
	"github.com/upcomers-org/platformgo/internal/currency"
	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
	"github.com/upcomers-org/platformgo/internal/money"
)

type BinaryOptionConfig struct {
	InstrumentID                                ids.InstrumentID
	RawSymbol                                   ids.Symbol
	AssetClass                                  AssetClass
	Currency                                    currency.Currency
	Activation, Expiration                      uint64
	PricePrecision, SizePrecision               uint8
	PriceIncrement                              decimal.Price
	SizeIncrement                               decimal.Quantity
	Outcome, Description                        *string
	MaxQuantity, MinQuantity                    *decimal.Quantity
	MaxNotional, MinNotional                    *money.Money
	MaxPrice, MinPrice                          *decimal.Price
	MarginInit, MarginMaint, MakerFee, TakerFee *decimal.Decimal
	TickScheme                                  *string
	Info                                        map[string]any
	TsEvent, TsInit                             uint64
}
type BinaryOption struct {
	Future               CryptoFuture
	AssetClassValue      AssetClass
	Outcome, Description *string
}

func NewCheckedBinaryOption(c BinaryOptionConfig) (BinaryOption, error) {
	f, err := NewCheckedCryptoFuture(CryptoFutureConfig{
		InstrumentID: c.InstrumentID, RawSymbol: c.RawSymbol, Underlying: c.Currency, QuoteCurrency: c.Currency, SettlementCurrency: c.Currency,
		Activation: c.Activation, Expiration: c.Expiration, PricePrecision: c.PricePrecision, SizePrecision: c.SizePrecision,
		PriceIncrement: c.PriceIncrement, SizeIncrement: c.SizeIncrement, MaxQuantity: c.MaxQuantity, MinQuantity: c.MinQuantity,
		MaxNotional: c.MaxNotional, MinNotional: c.MinNotional, MaxPrice: c.MaxPrice, MinPrice: c.MinPrice,
		MarginInit: c.MarginInit, MarginMaint: c.MarginMaint, MakerFee: c.MakerFee, TakerFee: c.TakerFee,
		TickScheme: c.TickScheme, Info: c.Info, TsEvent: c.TsEvent, TsInit: c.TsInit,
	})
	if err != nil {
		return BinaryOption{}, err
	}
	return BinaryOption{f, c.AssetClass, copyValue(c.Outcome), copyValue(c.Description)}, nil
}
func (o BinaryOption) AssetClass() AssetClass           { return o.AssetClassValue }
func (o BinaryOption) InstrumentClass() InstrumentClass { return InstrumentClassBinaryOption }
func (o BinaryOption) QuoteCurrency() currency.Currency { return o.Future.QuoteCurrency() }
func (o BinaryOption) IsInverse() bool                  { return false }
func (o BinaryOption) ActivationNanos() *uint64         { return o.Future.ActivationNanos() }
func (o BinaryOption) ExpirationNanos() *uint64         { return o.Future.ExpirationNanos() }
func (o BinaryOption) Equal(v BinaryOption) bool        { return o.Future.Equal(v.Future) }

type BinaryOptionBuilder struct{ c BinaryOptionConfig }

func NewBinaryOptionBuilder() *BinaryOptionBuilder { return &BinaryOptionBuilder{} }
func (b *BinaryOptionBuilder) Instrument(v ids.InstrumentID) *BinaryOptionBuilder {
	b.c.InstrumentID = v
	return b
}
func (b *BinaryOptionBuilder) Symbol(v ids.Symbol) *BinaryOptionBuilder { b.c.RawSymbol = v; return b }
func (b *BinaryOptionBuilder) Class(v AssetClass) *BinaryOptionBuilder  { b.c.AssetClass = v; return b }
func (b *BinaryOptionBuilder) DenominatedIn(v currency.Currency) *BinaryOptionBuilder {
	b.c.Currency = v
	return b
}
func (b *BinaryOptionBuilder) ActiveBetween(a, e uint64) *BinaryOptionBuilder {
	b.c.Activation = a
	b.c.Expiration = e
	return b
}
func (b *BinaryOptionBuilder) Precisions(p, s uint8) *BinaryOptionBuilder {
	b.c.PricePrecision = p
	b.c.SizePrecision = s
	return b
}
func (b *BinaryOptionBuilder) Increments(p decimal.Price, s decimal.Quantity) *BinaryOptionBuilder {
	b.c.PriceIncrement = p
	b.c.SizeIncrement = s
	return b
}
func (b *BinaryOptionBuilder) WithOutcome(v string) *BinaryOptionBuilder { b.c.Outcome = &v; return b }
func (b *BinaryOptionBuilder) WithDescription(v string) *BinaryOptionBuilder {
	b.c.Description = &v
	return b
}
func (b *BinaryOptionBuilder) QuantityLimits(x, n decimal.Quantity) *BinaryOptionBuilder {
	b.c.MaxQuantity = &x
	b.c.MinQuantity = &n
	return b
}
func (b *BinaryOptionBuilder) NotionalLimits(x, n money.Money) *BinaryOptionBuilder {
	b.c.MaxNotional = &x
	b.c.MinNotional = &n
	return b
}
func (b *BinaryOptionBuilder) PriceLimits(x, n decimal.Price) *BinaryOptionBuilder {
	b.c.MaxPrice = &x
	b.c.MinPrice = &n
	return b
}
func (b *BinaryOptionBuilder) Margins(x, n decimal.Decimal) *BinaryOptionBuilder {
	b.c.MarginInit = &x
	b.c.MarginMaint = &n
	return b
}
func (b *BinaryOptionBuilder) Fees(x, n decimal.Decimal) *BinaryOptionBuilder {
	b.c.MakerFee = &x
	b.c.TakerFee = &n
	return b
}
func (b *BinaryOptionBuilder) Timestamps(e, i uint64) *BinaryOptionBuilder {
	b.c.TsEvent = e
	b.c.TsInit = i
	return b
}
func (b *BinaryOptionBuilder) Build() (BinaryOption, error) { return NewCheckedBinaryOption(b.c) }
func (o BinaryOption) MarshalJSON() ([]byte, error) {
	type alias BinaryOption
	return json.Marshal(alias(o))
}
func (o *BinaryOption) UnmarshalJSON(data []byte) error {
	type alias BinaryOption
	return json.Unmarshal(data, (*alias)(o))
}
