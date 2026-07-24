package instrument

import (
	"encoding/json"
	"fmt"

	"github.com/upcomers-org/platformgo/internal/currency"
	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
	"github.com/upcomers-org/platformgo/internal/money"
)

const AssetClassAlternative AssetClass = "ALTERNATIVE"

type BettingInstrumentConfig struct {
	InstrumentID                                ids.InstrumentID
	RawSymbol                                   ids.Symbol
	EventTypeID, CompetitionID, EventID         uint64
	EventTypeName, CompetitionName, EventName   string
	EventCountryCode, BettingType, MarketID     string
	MarketName, MarketType, SelectionName       string
	EventOpenDate, MarketStartTime, SelectionID uint64
	SelectionHandicap                           float64
	Currency                                    currency.Currency
	PricePrecision, SizePrecision               uint8
	PriceIncrement                              decimal.Price
	SizeIncrement                               decimal.Quantity
	MaxQuantity, MinQuantity                    *decimal.Quantity
	MaxNotional, MinNotional                    *money.Money
	MaxPrice, MinPrice                          *decimal.Price
	MarginInit, MarginMaint, MakerFee, TakerFee *decimal.Decimal
	TickScheme                                  *string
	Info                                        map[string]any
	TsEvent, TsInit                             uint64
}

type BettingInstrument struct {
	InstrumentID                                ids.InstrumentID
	RawSymbol                                   ids.Symbol
	EventTypeID, CompetitionID, EventID         uint64
	EventTypeName, CompetitionName, EventName   string
	EventCountryCode, BettingType, MarketID     string
	MarketName, MarketType, SelectionName       string
	EventOpenDate, MarketStartTime, SelectionID uint64
	SelectionHandicap                           float64
	Currency                                    currency.Currency
	PricePrecision, SizePrecision               uint8
	PriceIncrement                              decimal.Price
	SizeIncrement                               decimal.Quantity
	MarginInit, MarginMaint, MakerFee, TakerFee decimal.Decimal
	MaxQuantity, MinQuantity                    *decimal.Quantity
	MaxNotional, MinNotional                    *money.Money
	MaxPrice, MinPrice                          *decimal.Price
	TickScheme                                  *string
	Info                                        map[string]any
	TsEvent, TsInit                             uint64
}

func NewCheckedBettingInstrument(config BettingInstrumentConfig) (BettingInstrument, error) {
	if config.PricePrecision != config.PriceIncrement.Precision() {
		return BettingInstrument{}, fmt.Errorf(
			"price_precision %d was not equal to price_increment.precision %d",
			config.PricePrecision,
			config.PriceIncrement.Precision(),
		)
	}
	if config.SizePrecision != config.SizeIncrement.Precision() {
		return BettingInstrument{}, fmt.Errorf(
			"size_precision %d was not equal to size_increment.precision %d",
			config.SizePrecision,
			config.SizeIncrement.Precision(),
		)
	}
	if err := config.PriceIncrement.RequirePositive("price_increment"); err != nil {
		return BettingInstrument{}, err
	}
	if err := config.SizeIncrement.RequirePositive("size_increment"); err != nil {
		return BettingInstrument{}, err
	}
	if config.TickScheme != nil {
		if _, err := ParseTickScheme(*config.TickScheme); err != nil {
			return BettingInstrument{}, err
		}
	}

	one := decimal.MustParse("1")
	return BettingInstrument{
		InstrumentID: config.InstrumentID, RawSymbol: config.RawSymbol,
		EventTypeID: config.EventTypeID, EventTypeName: config.EventTypeName,
		CompetitionID: config.CompetitionID, CompetitionName: config.CompetitionName,
		EventID: config.EventID, EventName: config.EventName,
		EventCountryCode: config.EventCountryCode, EventOpenDate: config.EventOpenDate,
		BettingType: config.BettingType, MarketID: config.MarketID,
		MarketName: config.MarketName, MarketType: config.MarketType,
		MarketStartTime: config.MarketStartTime, SelectionID: config.SelectionID,
		SelectionName: config.SelectionName, SelectionHandicap: config.SelectionHandicap,
		Currency: config.Currency, PricePrecision: config.PricePrecision,
		SizePrecision: config.SizePrecision, PriceIncrement: config.PriceIncrement,
		SizeIncrement: config.SizeIncrement,
		MarginInit:    decimalOr(config.MarginInit, one), MarginMaint: decimalOr(config.MarginMaint, one),
		MakerFee: decimalValue(config.MakerFee), TakerFee: decimalValue(config.TakerFee),
		MaxQuantity: copyValue(config.MaxQuantity), MinQuantity: copyValue(config.MinQuantity),
		MaxNotional: copyValue(config.MaxNotional), MinNotional: copyValue(config.MinNotional),
		MaxPrice: copyValue(config.MaxPrice), MinPrice: copyValue(config.MinPrice),
		TickScheme: copyValue(config.TickScheme), Info: cloneInfo(config.Info),
		TsEvent: config.TsEvent, TsInit: config.TsInit,
	}, nil
}

func decimalOr(value *decimal.Decimal, fallback decimal.Decimal) decimal.Decimal {
	if value == nil {
		return fallback
	}
	return *value
}

func (instrument BettingInstrument) AssetClass() AssetClass { return AssetClassAlternative }
func (instrument BettingInstrument) InstrumentClass() InstrumentClass {
	return InstrumentClassSportsBetting
}
func (instrument BettingInstrument) IsInverse() bool { return false }
func (instrument BettingInstrument) Equal(other BettingInstrument) bool {
	return instrument.InstrumentID == other.InstrumentID
}

func (instrument BettingInstrument) EffectiveTickScheme() *string {
	if instrument.TickScheme != nil {
		return copyValue(instrument.TickScheme)
	}
	if instrument.InstrumentID.Venue == BetfairTickSchemeName {
		name := BetfairTickSchemeName
		return &name
	}
	return nil
}

func (instrument BettingInstrument) EffectiveMinPrice() *decimal.Price {
	if instrument.MinPrice != nil {
		return copyValue(instrument.MinPrice)
	}
	if instrument.InstrumentID.Venue == BetfairTickSchemeName {
		value := BetfairTickScheme().MinPrice()
		return &value
	}
	return nil
}

func (instrument BettingInstrument) EffectiveMaxPrice() *decimal.Price {
	if instrument.MaxPrice != nil {
		return copyValue(instrument.MaxPrice)
	}
	if instrument.InstrumentID.Venue == BetfairTickSchemeName {
		value := BetfairTickScheme().MaxPrice()
		return &value
	}
	return nil
}

func (instrument BettingInstrument) NextBidPrice(value float64, n int32) (decimal.Price, bool) {
	scheme := instrument.EffectiveTickScheme()
	if scheme == nil {
		return decimal.Price{}, false
	}
	rule, err := ParseTickScheme(*scheme)
	if err != nil {
		return decimal.Price{}, false
	}
	return rule.NextBidPrice(value, n, instrument.PricePrecision)
}

func (instrument BettingInstrument) NextAskPrice(value float64, n int32) (decimal.Price, bool) {
	scheme := instrument.EffectiveTickScheme()
	if scheme == nil {
		return decimal.Price{}, false
	}
	rule, err := ParseTickScheme(*scheme)
	if err != nil {
		return decimal.Price{}, false
	}
	return rule.NextAskPrice(value, n, instrument.PricePrecision)
}

func (instrument BettingInstrument) NextBidPrices(value float64, count int) []decimal.Price {
	return instrument.nextPrices(value, count, false)
}

func (instrument BettingInstrument) NextAskPrices(value float64, count int) []decimal.Price {
	return instrument.nextPrices(value, count, true)
}

func (instrument BettingInstrument) nextPrices(value float64, count int, ask bool) []decimal.Price {
	if count <= 0 {
		return nil
	}
	result := make([]decimal.Price, 0, count)
	for step := 0; step < count; step++ {
		var price decimal.Price
		var ok bool
		if ask {
			price, ok = instrument.NextAskPrice(value, int32(step))
		} else {
			price, ok = instrument.NextBidPrice(value, int32(step))
		}
		if !ok {
			break
		}
		result = append(result, price)
	}
	return result
}

type bettingInstrumentWire struct {
	InstrumentID                                ids.InstrumentID
	RawSymbol                                   ids.Symbol
	EventTypeID, CompetitionID, EventID         uint64
	EventTypeName, CompetitionName, EventName   string
	EventCountryCode, BettingType, MarketID     string
	MarketName, MarketType, SelectionName       string
	EventOpenDate, MarketStartTime, SelectionID uint64
	SelectionHandicap                           float64
	Currency                                    cryptoCurrencyWire
	PricePrecision, SizePrecision               uint8
	PriceIncrement                              decimal.Price
	SizeIncrement                               decimal.Quantity
	MarginInit, MarginMaint, MakerFee, TakerFee string
	MaxQuantity, MinQuantity                    *decimal.Quantity
	MaxNotional, MinNotional                    *cryptoMoneyWire
	MaxPrice, MinPrice                          *decimal.Price
	TickScheme                                  *string
	Info                                        map[string]any
	TsEvent, TsInit                             uint64
}

func (instrument BettingInstrument) MarshalJSON() ([]byte, error) {
	return json.Marshal(bettingInstrumentWire{
		InstrumentID: instrument.InstrumentID, RawSymbol: instrument.RawSymbol,
		EventTypeID: instrument.EventTypeID, EventTypeName: instrument.EventTypeName,
		CompetitionID: instrument.CompetitionID, CompetitionName: instrument.CompetitionName,
		EventID: instrument.EventID, EventName: instrument.EventName,
		EventCountryCode: instrument.EventCountryCode, EventOpenDate: instrument.EventOpenDate,
		BettingType: instrument.BettingType, MarketID: instrument.MarketID,
		MarketName: instrument.MarketName, MarketType: instrument.MarketType,
		MarketStartTime: instrument.MarketStartTime, SelectionID: instrument.SelectionID,
		SelectionName: instrument.SelectionName, SelectionHandicap: instrument.SelectionHandicap,
		Currency:       cryptoCurrency(instrument.Currency),
		PricePrecision: instrument.PricePrecision, SizePrecision: instrument.SizePrecision,
		PriceIncrement: instrument.PriceIncrement, SizeIncrement: instrument.SizeIncrement,
		MarginInit: instrument.MarginInit.String(), MarginMaint: instrument.MarginMaint.String(),
		MakerFee: instrument.MakerFee.String(), TakerFee: instrument.TakerFee.String(),
		MaxQuantity: instrument.MaxQuantity, MinQuantity: instrument.MinQuantity,
		MaxNotional: cryptoMoney(instrument.MaxNotional), MinNotional: cryptoMoney(instrument.MinNotional),
		MaxPrice: instrument.MaxPrice, MinPrice: instrument.MinPrice,
		TickScheme: instrument.TickScheme, Info: instrument.Info,
		TsEvent: instrument.TsEvent, TsInit: instrument.TsInit,
	})
}

func (instrument *BettingInstrument) UnmarshalJSON(data []byte) error {
	var wire bettingInstrumentWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	denomination, err := currencyFromCrypto(wire.Currency)
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
	*instrument = BettingInstrument{
		InstrumentID: wire.InstrumentID, RawSymbol: wire.RawSymbol,
		EventTypeID: wire.EventTypeID, EventTypeName: wire.EventTypeName,
		CompetitionID: wire.CompetitionID, CompetitionName: wire.CompetitionName,
		EventID: wire.EventID, EventName: wire.EventName,
		EventCountryCode: wire.EventCountryCode, EventOpenDate: wire.EventOpenDate,
		BettingType: wire.BettingType, MarketID: wire.MarketID,
		MarketName: wire.MarketName, MarketType: wire.MarketType,
		MarketStartTime: wire.MarketStartTime, SelectionID: wire.SelectionID,
		SelectionName: wire.SelectionName, SelectionHandicap: wire.SelectionHandicap,
		Currency: denomination, PricePrecision: wire.PricePrecision,
		SizePrecision: wire.SizePrecision, PriceIncrement: wire.PriceIncrement,
		SizeIncrement: wire.SizeIncrement,
		MarginInit:    decimal.MustParse(wire.MarginInit), MarginMaint: decimal.MustParse(wire.MarginMaint),
		MakerFee: decimal.MustParse(wire.MakerFee), TakerFee: decimal.MustParse(wire.TakerFee),
		MaxQuantity: copyValue(wire.MaxQuantity), MinQuantity: copyValue(wire.MinQuantity),
		MaxNotional: maxNotional, MinNotional: minNotional,
		MaxPrice: copyValue(wire.MaxPrice), MinPrice: copyValue(wire.MinPrice),
		TickScheme: copyValue(wire.TickScheme), Info: cloneInfo(wire.Info),
		TsEvent: wire.TsEvent, TsInit: wire.TsInit,
	}
	return nil
}

type BettingInstrumentBuilder struct{ config BettingInstrumentConfig }

func NewBettingInstrumentBuilder() *BettingInstrumentBuilder { return &BettingInstrumentBuilder{} }
func (b *BettingInstrumentBuilder) Instrument(value ids.InstrumentID) *BettingInstrumentBuilder {
	b.config.InstrumentID = value
	return b
}
func (b *BettingInstrumentBuilder) Symbol(value ids.Symbol) *BettingInstrumentBuilder {
	b.config.RawSymbol = value
	return b
}
func (b *BettingInstrumentBuilder) EventType(id uint64, name string) *BettingInstrumentBuilder {
	b.config.EventTypeID, b.config.EventTypeName = id, name
	return b
}
func (b *BettingInstrumentBuilder) Competition(id uint64, name string) *BettingInstrumentBuilder {
	b.config.CompetitionID, b.config.CompetitionName = id, name
	return b
}
func (b *BettingInstrumentBuilder) Event(id uint64, name, country string, openDate uint64) *BettingInstrumentBuilder {
	b.config.EventID, b.config.EventName = id, name
	b.config.EventCountryCode, b.config.EventOpenDate = country, openDate
	return b
}
func (b *BettingInstrumentBuilder) Market(bettingType, id, name, marketType string, start uint64) *BettingInstrumentBuilder {
	b.config.BettingType, b.config.MarketID = bettingType, id
	b.config.MarketName, b.config.MarketType, b.config.MarketStartTime = name, marketType, start
	return b
}
func (b *BettingInstrumentBuilder) Selection(id uint64, name string, handicap float64) *BettingInstrumentBuilder {
	b.config.SelectionID, b.config.SelectionName, b.config.SelectionHandicap = id, name, handicap
	return b
}
func (b *BettingInstrumentBuilder) Denomination(value currency.Currency) *BettingInstrumentBuilder {
	b.config.Currency = value
	return b
}
func (b *BettingInstrumentBuilder) Precisions(price, size uint8) *BettingInstrumentBuilder {
	b.config.PricePrecision, b.config.SizePrecision = price, size
	return b
}
func (b *BettingInstrumentBuilder) Increments(price decimal.Price, size decimal.Quantity) *BettingInstrumentBuilder {
	b.config.PriceIncrement, b.config.SizeIncrement = price, size
	return b
}
func (b *BettingInstrumentBuilder) QuantityLimits(maximum, minimum decimal.Quantity) *BettingInstrumentBuilder {
	b.config.MaxQuantity, b.config.MinQuantity = &maximum, &minimum
	return b
}
func (b *BettingInstrumentBuilder) NotionalLimits(maximum, minimum money.Money) *BettingInstrumentBuilder {
	b.config.MaxNotional, b.config.MinNotional = &maximum, &minimum
	return b
}
func (b *BettingInstrumentBuilder) PriceLimits(maximum, minimum decimal.Price) *BettingInstrumentBuilder {
	b.config.MaxPrice, b.config.MinPrice = &maximum, &minimum
	return b
}
func (b *BettingInstrumentBuilder) Margins(initial, maintenance decimal.Decimal) *BettingInstrumentBuilder {
	b.config.MarginInit, b.config.MarginMaint = &initial, &maintenance
	return b
}
func (b *BettingInstrumentBuilder) Fees(maker, taker decimal.Decimal) *BettingInstrumentBuilder {
	b.config.MakerFee, b.config.TakerFee = &maker, &taker
	return b
}
func (b *BettingInstrumentBuilder) Timestamps(event, init uint64) *BettingInstrumentBuilder {
	b.config.TsEvent, b.config.TsInit = event, init
	return b
}
func (b *BettingInstrumentBuilder) Build() (BettingInstrument, error) {
	return NewCheckedBettingInstrument(b.config)
}
