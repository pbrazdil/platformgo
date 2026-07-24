package instrument

import (
	"encoding/json"
	"fmt"

	"github.com/upcomers-org/platformgo/internal/currency"
	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
	"github.com/upcomers-org/platformgo/internal/money"
)

const AssetClassFX AssetClass = "FX"

type PerpetualContractConfig struct {
	InstrumentID                                ids.InstrumentID
	RawSymbol                                   ids.Symbol
	Underlying                                  string
	AssetClass                                  AssetClass
	BaseCurrency                                *currency.Currency
	QuoteCurrency                               currency.Currency
	SettlementCurrency                          currency.Currency
	Inverse                                     bool
	PricePrecision, SizePrecision               uint8
	PriceIncrement                              decimal.Price
	SizeIncrement                               decimal.Quantity
	Multiplier, LotSize                         *decimal.Quantity
	MaxQuantity, MinQuantity                    *decimal.Quantity
	MaxNotional, MinNotional                    *money.Money
	MaxPrice, MinPrice                          *decimal.Price
	MarginInit, MarginMaint, MakerFee, TakerFee *decimal.Decimal
	TickScheme                                  *string
	Info                                        map[string]any
	TsEvent, TsInit                             uint64
}

type PerpetualContract struct {
	InstrumentID                                ids.InstrumentID
	RawSymbol                                   ids.Symbol
	Underlying                                  string
	AssetClass                                  AssetClass
	BaseCurrency                                *currency.Currency
	QuoteCurrency                               currency.Currency
	SettlementCurrency                          currency.Currency
	Inverse                                     bool
	PricePrecision, SizePrecision               uint8
	PriceIncrement                              decimal.Price
	SizeIncrement, Multiplier, LotSize          decimal.Quantity
	MarginInit, MarginMaint, MakerFee, TakerFee decimal.Decimal
	MaxQuantity, MinQuantity                    *decimal.Quantity
	MaxNotional, MinNotional                    *money.Money
	MaxPrice, MinPrice                          *decimal.Price
	TickScheme                                  *string
	Info                                        map[string]any
	TsEvent, TsInit                             uint64
}

func NewCheckedPerpetualContract(config PerpetualContractConfig) (PerpetualContract, error) {
	if config.PricePrecision != config.PriceIncrement.Precision() {
		return PerpetualContract{}, fmt.Errorf("price_precision did not equal price_increment.precision")
	}
	if config.SizePrecision != config.SizeIncrement.Precision() {
		return PerpetualContract{}, fmt.Errorf("size_precision did not equal size_increment.precision")
	}
	if err := config.PriceIncrement.RequirePositive("price_increment"); err != nil {
		return PerpetualContract{}, err
	}
	if err := config.SizeIncrement.RequirePositive("size_increment"); err != nil {
		return PerpetualContract{}, err
	}
	if config.TickScheme != nil {
		if _, err := ParseTickScheme(*config.TickScheme); err != nil {
			return PerpetualContract{}, err
		}
	}
	if config.Inverse && config.BaseCurrency == nil {
		return PerpetualContract{}, fmt.Errorf("inverse perpetual contract requires a base_currency")
	}
	one := decimal.MustQuantity("1")
	multiplier, lotSize := one, one
	if config.Multiplier != nil {
		if err := config.Multiplier.RequirePositive("multiplier"); err != nil {
			return PerpetualContract{}, err
		}
		multiplier = *config.Multiplier
	}
	if config.LotSize != nil {
		if err := config.LotSize.RequirePositive("lot_size"); err != nil {
			return PerpetualContract{}, err
		}
		lotSize = *config.LotSize
	}
	return PerpetualContract{
		InstrumentID: config.InstrumentID, RawSymbol: config.RawSymbol,
		Underlying: config.Underlying, AssetClass: config.AssetClass,
		BaseCurrency: copyValue(config.BaseCurrency), QuoteCurrency: config.QuoteCurrency,
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

func (contract PerpetualContract) InstrumentClass() InstrumentClass { return InstrumentClassSwap }
func (contract PerpetualContract) CostCurrency() currency.Currency {
	if contract.Inverse {
		return contract.SettlementCurrency
	}
	return contract.QuoteCurrency
}
func (contract PerpetualContract) Equal(other PerpetualContract) bool {
	return contract.InstrumentID == other.InstrumentID
}
func (contract PerpetualContract) String() string {
	return fmt.Sprintf("PerpetualContract(id=%s, underlying=%s, inverse=%t)", contract.InstrumentID, contract.Underlying, contract.Inverse)
}

type PerpetualContractBuilder struct{ config PerpetualContractConfig }

func NewPerpetualContractBuilder() *PerpetualContractBuilder { return &PerpetualContractBuilder{} }
func (b *PerpetualContractBuilder) Instrument(v ids.InstrumentID) *PerpetualContractBuilder {
	b.config.InstrumentID = v
	return b
}
func (b *PerpetualContractBuilder) Symbol(v ids.Symbol) *PerpetualContractBuilder {
	b.config.RawSymbol = v
	return b
}
func (b *PerpetualContractBuilder) ForUnderlying(v string) *PerpetualContractBuilder {
	b.config.Underlying = v
	return b
}
func (b *PerpetualContractBuilder) Class(v AssetClass) *PerpetualContractBuilder {
	b.config.AssetClass = v
	return b
}
func (b *PerpetualContractBuilder) Base(v currency.Currency) *PerpetualContractBuilder {
	b.config.BaseCurrency = &v
	return b
}
func (b *PerpetualContractBuilder) Quote(v currency.Currency) *PerpetualContractBuilder {
	b.config.QuoteCurrency = v
	return b
}
func (b *PerpetualContractBuilder) Settlement(v currency.Currency) *PerpetualContractBuilder {
	b.config.SettlementCurrency = v
	return b
}
func (b *PerpetualContractBuilder) IsInverse(v bool) *PerpetualContractBuilder {
	b.config.Inverse = v
	return b
}
func (b *PerpetualContractBuilder) Precisions(price, size uint8) *PerpetualContractBuilder {
	b.config.PricePrecision, b.config.SizePrecision = price, size
	return b
}
func (b *PerpetualContractBuilder) Increments(price decimal.Price, size decimal.Quantity) *PerpetualContractBuilder {
	b.config.PriceIncrement, b.config.SizeIncrement = price, size
	return b
}
func (b *PerpetualContractBuilder) Sizing(multiplier, lot decimal.Quantity) *PerpetualContractBuilder {
	b.config.Multiplier, b.config.LotSize = &multiplier, &lot
	return b
}
func (b *PerpetualContractBuilder) QuantityLimits(max, min decimal.Quantity) *PerpetualContractBuilder {
	b.config.MaxQuantity, b.config.MinQuantity = &max, &min
	return b
}
func (b *PerpetualContractBuilder) NotionalLimits(max, min money.Money) *PerpetualContractBuilder {
	b.config.MaxNotional, b.config.MinNotional = &max, &min
	return b
}
func (b *PerpetualContractBuilder) PriceLimits(max, min decimal.Price) *PerpetualContractBuilder {
	b.config.MaxPrice, b.config.MinPrice = &max, &min
	return b
}
func (b *PerpetualContractBuilder) Margins(initial, maintenance decimal.Decimal) *PerpetualContractBuilder {
	b.config.MarginInit, b.config.MarginMaint = &initial, &maintenance
	return b
}
func (b *PerpetualContractBuilder) Fees(maker, taker decimal.Decimal) *PerpetualContractBuilder {
	b.config.MakerFee, b.config.TakerFee = &maker, &taker
	return b
}
func (b *PerpetualContractBuilder) Timestamps(event, init uint64) *PerpetualContractBuilder {
	b.config.TsEvent, b.config.TsInit = event, init
	return b
}
func (b *PerpetualContractBuilder) Build() (PerpetualContract, error) {
	return NewCheckedPerpetualContract(b.config)
}

type perpetualContractWire struct {
	InstrumentID                                ids.InstrumentID
	RawSymbol                                   ids.Symbol
	Underlying                                  string
	AssetClass                                  AssetClass
	BaseCurrency                                *cryptoCurrencyWire
	QuoteCurrency, SettlementCurrency           cryptoCurrencyWire
	Inverse                                     bool
	PricePrecision, SizePrecision               uint8
	PriceIncrement                              decimal.Price
	SizeIncrement, Multiplier, LotSize          decimal.Quantity
	MarginInit, MarginMaint, MakerFee, TakerFee string
	MaxQuantity, MinQuantity                    *decimal.Quantity
	MaxNotional, MinNotional                    *cryptoMoneyWire
	MaxPrice, MinPrice                          *decimal.Price
	TickScheme                                  *string
	Info                                        map[string]any
	TsEvent, TsInit                             uint64
}

func (contract PerpetualContract) MarshalJSON() ([]byte, error) {
	var base *cryptoCurrencyWire
	if contract.BaseCurrency != nil {
		value := cryptoCurrency(*contract.BaseCurrency)
		base = &value
	}
	return json.Marshal(perpetualContractWire{
		InstrumentID: contract.InstrumentID, RawSymbol: contract.RawSymbol, Underlying: contract.Underlying,
		AssetClass: contract.AssetClass, BaseCurrency: base,
		QuoteCurrency: cryptoCurrency(contract.QuoteCurrency), SettlementCurrency: cryptoCurrency(contract.SettlementCurrency),
		Inverse: contract.Inverse, PricePrecision: contract.PricePrecision, SizePrecision: contract.SizePrecision,
		PriceIncrement: contract.PriceIncrement, SizeIncrement: contract.SizeIncrement,
		Multiplier: contract.Multiplier, LotSize: contract.LotSize,
		MarginInit: contract.MarginInit.String(), MarginMaint: contract.MarginMaint.String(),
		MakerFee: contract.MakerFee.String(), TakerFee: contract.TakerFee.String(),
		MaxQuantity: contract.MaxQuantity, MinQuantity: contract.MinQuantity,
		MaxNotional: cryptoMoney(contract.MaxNotional), MinNotional: cryptoMoney(contract.MinNotional),
		MaxPrice: contract.MaxPrice, MinPrice: contract.MinPrice, TickScheme: contract.TickScheme,
		Info: contract.Info, TsEvent: contract.TsEvent, TsInit: contract.TsInit,
	})
}

func (contract *PerpetualContract) UnmarshalJSON(data []byte) error {
	var wire perpetualContractWire
	if err := json.Unmarshal(data, &wire); err != nil {
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
	var base *currency.Currency
	if wire.BaseCurrency != nil {
		value, e := currencyFromCrypto(*wire.BaseCurrency)
		if e != nil {
			return e
		}
		base = &value
	}
	maxNotional, err := moneyFromCrypto(wire.MaxNotional)
	if err != nil {
		return err
	}
	minNotional, err := moneyFromCrypto(wire.MinNotional)
	if err != nil {
		return err
	}
	*contract = PerpetualContract{
		InstrumentID: wire.InstrumentID, RawSymbol: wire.RawSymbol, Underlying: wire.Underlying, AssetClass: wire.AssetClass,
		BaseCurrency: base, QuoteCurrency: quote, SettlementCurrency: settlement, Inverse: wire.Inverse,
		PricePrecision: wire.PricePrecision, SizePrecision: wire.SizePrecision, PriceIncrement: wire.PriceIncrement,
		SizeIncrement: wire.SizeIncrement, Multiplier: wire.Multiplier, LotSize: wire.LotSize,
		MarginInit: decimal.MustParse(wire.MarginInit), MarginMaint: decimal.MustParse(wire.MarginMaint),
		MakerFee: decimal.MustParse(wire.MakerFee), TakerFee: decimal.MustParse(wire.TakerFee),
		MaxQuantity: copyValue(wire.MaxQuantity), MinQuantity: copyValue(wire.MinQuantity),
		MaxNotional: maxNotional, MinNotional: minNotional, MaxPrice: copyValue(wire.MaxPrice), MinPrice: copyValue(wire.MinPrice),
		TickScheme: copyValue(wire.TickScheme), Info: cloneInfo(wire.Info), TsEvent: wire.TsEvent, TsInit: wire.TsInit,
	}
	return nil
}
