package instrument

import (
	"encoding/json"
	"fmt"

	"github.com/upcomers-org/platformgo/internal/currency"
	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
	"github.com/upcomers-org/platformgo/internal/money"
)

type TokenizedAssetConfig struct {
	InstrumentID                                ids.InstrumentID
	RawSymbol                                   ids.Symbol
	AssetClass                                  AssetClass
	BaseCurrency, QuoteCurrency                 currency.Currency
	ISIN                                        *string
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

type TokenizedAsset struct {
	InstrumentID                                ids.InstrumentID
	RawSymbol                                   ids.Symbol
	AssetClass                                  AssetClass
	BaseCurrency, QuoteCurrency                 currency.Currency
	ISIN                                        *string
	PricePrecision, SizePrecision               uint8
	PriceIncrement                              decimal.Price
	SizeIncrement, Multiplier                   decimal.Quantity
	LotSize                                     *decimal.Quantity
	MaxQuantity, MinQuantity                    *decimal.Quantity
	MaxNotional, MinNotional                    *money.Money
	MaxPrice, MinPrice                          *decimal.Price
	MarginInit, MarginMaint, MakerFee, TakerFee decimal.Decimal
	TickScheme                                  *string
	Info                                        map[string]any
	TsEvent, TsInit                             uint64
}

func NewCheckedTokenizedAsset(config TokenizedAssetConfig) (TokenizedAsset, error) {
	if config.ISIN != nil {
		for _, char := range *config.ISIN {
			if char > 127 {
				return TokenizedAsset{}, fmt.Errorf("isin contained non-ASCII character")
			}
		}
	}
	if config.PricePrecision != config.PriceIncrement.Precision() {
		return TokenizedAsset{}, fmt.Errorf("price precision mismatch")
	}
	if config.SizePrecision != config.SizeIncrement.Precision() {
		return TokenizedAsset{}, fmt.Errorf("size precision mismatch")
	}
	if err := config.PriceIncrement.RequirePositive("price_increment"); err != nil {
		return TokenizedAsset{}, err
	}
	if err := config.SizeIncrement.RequirePositive("size_increment"); err != nil {
		return TokenizedAsset{}, err
	}
	if config.TickScheme != nil {
		if _, err := ParseTickScheme(*config.TickScheme); err != nil {
			return TokenizedAsset{}, err
		}
	}
	one := decimal.MustQuantity("1")
	multiplier := one
	if config.Multiplier != nil {
		if err := config.Multiplier.RequirePositive("multiplier"); err != nil {
			return TokenizedAsset{}, err
		}
		multiplier = *config.Multiplier
	}
	if config.LotSize != nil {
		if err := config.LotSize.RequirePositive("lot_size"); err != nil {
			return TokenizedAsset{}, err
		}
	}
	return TokenizedAsset{
		InstrumentID: config.InstrumentID, RawSymbol: config.RawSymbol, AssetClass: config.AssetClass,
		BaseCurrency: config.BaseCurrency, QuoteCurrency: config.QuoteCurrency, ISIN: copyValue(config.ISIN),
		PricePrecision: config.PricePrecision, SizePrecision: config.SizePrecision,
		PriceIncrement: config.PriceIncrement, SizeIncrement: config.SizeIncrement, Multiplier: multiplier,
		LotSize: copyValue(config.LotSize), MaxQuantity: copyValue(config.MaxQuantity), MinQuantity: copyValue(config.MinQuantity),
		MaxNotional: copyValue(config.MaxNotional), MinNotional: copyValue(config.MinNotional),
		MaxPrice: copyValue(config.MaxPrice), MinPrice: copyValue(config.MinPrice),
		MarginInit: decimalValue(config.MarginInit), MarginMaint: decimalValue(config.MarginMaint),
		MakerFee: decimalValue(config.MakerFee), TakerFee: decimalValue(config.TakerFee),
		TickScheme: copyValue(config.TickScheme), Info: cloneInfo(config.Info), TsEvent: config.TsEvent, TsInit: config.TsInit,
	}, nil
}

func (asset TokenizedAsset) InstrumentClass() InstrumentClass      { return InstrumentClassSpot }
func (asset TokenizedAsset) SettlementCurrency() currency.Currency { return asset.QuoteCurrency }
func (asset TokenizedAsset) IsInverse() bool                       { return false }
func (asset TokenizedAsset) Equal(other TokenizedAsset) bool {
	return asset.InstrumentID == other.InstrumentID
}
func (asset TokenizedAsset) String() string {
	return fmt.Sprintf("TokenizedAsset(id=%s, base=%s, quote=%s)", asset.InstrumentID, asset.BaseCurrency, asset.QuoteCurrency)
}

type TokenizedAssetBuilder struct{ config TokenizedAssetConfig }

func NewTokenizedAssetBuilder() *TokenizedAssetBuilder { return &TokenizedAssetBuilder{} }
func (b *TokenizedAssetBuilder) Instrument(v ids.InstrumentID) *TokenizedAssetBuilder {
	b.config.InstrumentID = v
	return b
}
func (b *TokenizedAssetBuilder) Symbol(v ids.Symbol) *TokenizedAssetBuilder {
	b.config.RawSymbol = v
	return b
}
func (b *TokenizedAssetBuilder) Class(v AssetClass) *TokenizedAssetBuilder {
	b.config.AssetClass = v
	return b
}
func (b *TokenizedAssetBuilder) Currencies(base, quote currency.Currency) *TokenizedAssetBuilder {
	b.config.BaseCurrency, b.config.QuoteCurrency = base, quote
	return b
}
func (b *TokenizedAssetBuilder) WithISIN(v string) *TokenizedAssetBuilder {
	b.config.ISIN = &v
	return b
}
func (b *TokenizedAssetBuilder) Precisions(price, size uint8) *TokenizedAssetBuilder {
	b.config.PricePrecision, b.config.SizePrecision = price, size
	return b
}
func (b *TokenizedAssetBuilder) Increments(price decimal.Price, size decimal.Quantity) *TokenizedAssetBuilder {
	b.config.PriceIncrement, b.config.SizeIncrement = price, size
	return b
}
func (b *TokenizedAssetBuilder) Sizing(multiplier, lot decimal.Quantity) *TokenizedAssetBuilder {
	b.config.Multiplier, b.config.LotSize = &multiplier, &lot
	return b
}
func (b *TokenizedAssetBuilder) QuantityLimits(max, min decimal.Quantity) *TokenizedAssetBuilder {
	b.config.MaxQuantity, b.config.MinQuantity = &max, &min
	return b
}
func (b *TokenizedAssetBuilder) NotionalLimits(max, min money.Money) *TokenizedAssetBuilder {
	b.config.MaxNotional, b.config.MinNotional = &max, &min
	return b
}
func (b *TokenizedAssetBuilder) PriceLimits(max, min decimal.Price) *TokenizedAssetBuilder {
	b.config.MaxPrice, b.config.MinPrice = &max, &min
	return b
}
func (b *TokenizedAssetBuilder) Margins(initial, maintenance decimal.Decimal) *TokenizedAssetBuilder {
	b.config.MarginInit, b.config.MarginMaint = &initial, &maintenance
	return b
}
func (b *TokenizedAssetBuilder) Fees(maker, taker decimal.Decimal) *TokenizedAssetBuilder {
	b.config.MakerFee, b.config.TakerFee = &maker, &taker
	return b
}
func (b *TokenizedAssetBuilder) Timestamps(event, init uint64) *TokenizedAssetBuilder {
	b.config.TsEvent, b.config.TsInit = event, init
	return b
}
func (b *TokenizedAssetBuilder) Build() (TokenizedAsset, error) {
	return NewCheckedTokenizedAsset(b.config)
}

type tokenizedAssetWire struct {
	Data    json.RawMessage
	ISIN    *string
	LotSize *decimal.Quantity
}

func (asset TokenizedAsset) MarshalJSON() ([]byte, error) {
	contract := PerpetualContract{
		InstrumentID: asset.InstrumentID, RawSymbol: asset.RawSymbol, AssetClass: asset.AssetClass,
		BaseCurrency: &asset.BaseCurrency, QuoteCurrency: asset.QuoteCurrency, SettlementCurrency: asset.QuoteCurrency,
		PricePrecision: asset.PricePrecision, SizePrecision: asset.SizePrecision, PriceIncrement: asset.PriceIncrement,
		SizeIncrement: asset.SizeIncrement, Multiplier: asset.Multiplier, LotSize: decimal.MustQuantity("1"),
		MarginInit: asset.MarginInit, MarginMaint: asset.MarginMaint, MakerFee: asset.MakerFee, TakerFee: asset.TakerFee,
		MaxQuantity: asset.MaxQuantity, MinQuantity: asset.MinQuantity, MaxNotional: asset.MaxNotional, MinNotional: asset.MinNotional,
		MaxPrice: asset.MaxPrice, MinPrice: asset.MinPrice, TickScheme: asset.TickScheme, Info: asset.Info, TsEvent: asset.TsEvent, TsInit: asset.TsInit,
	}
	data, err := json.Marshal(contract)
	if err != nil {
		return nil, err
	}
	return json.Marshal(tokenizedAssetWire{Data: data, ISIN: asset.ISIN, LotSize: asset.LotSize})
}
func (asset *TokenizedAsset) UnmarshalJSON(data []byte) error {
	var wire tokenizedAssetWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	var contract PerpetualContract
	if err := json.Unmarshal(wire.Data, &contract); err != nil {
		return err
	}
	*asset = TokenizedAsset{
		InstrumentID: contract.InstrumentID, RawSymbol: contract.RawSymbol, AssetClass: contract.AssetClass,
		BaseCurrency: *contract.BaseCurrency, QuoteCurrency: contract.QuoteCurrency, ISIN: copyValue(wire.ISIN),
		PricePrecision: contract.PricePrecision, SizePrecision: contract.SizePrecision, PriceIncrement: contract.PriceIncrement,
		SizeIncrement: contract.SizeIncrement, Multiplier: contract.Multiplier, LotSize: copyValue(wire.LotSize),
		MaxQuantity: contract.MaxQuantity, MinQuantity: contract.MinQuantity, MaxNotional: contract.MaxNotional, MinNotional: contract.MinNotional,
		MaxPrice: contract.MaxPrice, MinPrice: contract.MinPrice, MarginInit: contract.MarginInit, MarginMaint: contract.MarginMaint,
		MakerFee: contract.MakerFee, TakerFee: contract.TakerFee, TickScheme: contract.TickScheme, Info: contract.Info, TsEvent: contract.TsEvent, TsInit: contract.TsInit,
	}
	return nil
}
