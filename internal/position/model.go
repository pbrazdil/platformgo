// Package position implements deterministic, exact-decimal position accounting.
package position

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/upcomers-org/platformgo/internal/currency"
	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/money"
)

type OrderSide uint8

const (
	NoOrderSide OrderSide = iota
	Buy
	Sell
)

func (side OrderSide) String() string {
	switch side {
	case Buy:
		return "BUY"
	case Sell:
		return "SELL"
	default:
		return "NO_ORDER_SIDE"
	}
}

type Side uint8

const (
	Flat Side = iota
	Long
	Short
)

func (side Side) String() string {
	switch side {
	case Long:
		return "LONG"
	case Short:
		return "SHORT"
	default:
		return "FLAT"
	}
}

type AdjustmentType string

const (
	Commission AdjustmentType = "COMMISSION"
	Funding    AdjustmentType = "FUNDING"
)

type Instrument struct {
	ID                 string
	PricePrecision     uint8
	SizePrecision      uint8
	Multiplier         decimal.Decimal
	Inverse            bool
	CurrencyPair       bool
	BaseCurrency       *currency.Currency
	QuoteCurrency      currency.Currency
	SettlementCurrency currency.Currency
}

type Fill struct {
	ClientOrderID string
	TradeID       string
	Side          OrderSide
	Quantity      decimal.Decimal
	Price         decimal.Decimal
	Commission    *money.Money
	TsEvent       uint64
	TsInit        uint64
}

type Adjustment struct {
	Type           AdjustmentType
	QuantityChange *decimal.Decimal
	PnLChange      *money.Money
	Reason         *string
	TsEvent        uint64
	TsInit         uint64
}

type FillVoid struct {
	ClientOrderID  string
	TradeID        string
	VoidedQuantity decimal.Decimal
	CommissionVoid *money.Money
}

type replayEvent struct {
	Fill       *Fill
	Adjustment *Adjustment
}

type Position struct {
	Instrument Instrument
	ID         string

	Events      []Fill
	Adjustments []Adjustment
	replay      []replayEvent
	FillVoids   []FillVoid
	tradeIDs    map[string]struct{}

	OpeningOrderID string
	ClosingOrderID *string
	Entry          OrderSide
	Side           Side
	SignedQuantity decimal.Decimal
	Quantity       decimal.Decimal
	PeakQuantity   decimal.Decimal
	AverageOpen    decimal.Decimal
	AverageClose   *decimal.Decimal
	RealizedReturn decimal.Decimal
	RealizedPnL    *money.Money
	BuyQuantity    decimal.Decimal
	SellQuantity   decimal.Decimal

	TsInit          uint64
	TsOpened        uint64
	TsLast          uint64
	TsClosed        *uint64
	Duration        uint64
	commissions     map[string]money.Money
	commissionOrder []string
}

func New(instrument Instrument, id string, opening Fill) (*Position, error) {
	if opening.Side != Buy && opening.Side != Sell {
		return nil, errors.New("fill has no specified order side")
	}
	position := &Position{
		Instrument:  instrument,
		ID:          id,
		tradeIDs:    make(map[string]struct{}),
		commissions: make(map[string]money.Money),
	}
	if err := position.apply(opening, true); err != nil {
		return nil, err
	}
	return position, nil
}

func (position *Position) Apply(fill Fill) error {
	return position.apply(fill, true)
}

func (position *Position) apply(fill Fill, record bool) error {
	if _, exists := position.tradeIDs[fill.TradeID]; exists {
		return errors.New("`fill.trade_id` already contained in `trade_ids`")
	}
	if position.Side == Flat {
		position.resetCycle(fill)
	}
	if record {
		copy := fill
		position.replay = append(position.replay, replayEvent{Fill: &copy})
	}
	position.Events = append(position.Events, fill)
	position.tradeIDs[fill.TradeID] = struct{}{}
	position.addCommission(fill.Commission)

	commissionPnL := decimal.Decimal{}
	if fill.Commission != nil &&
		fill.Commission.Currency().Equal(position.Instrument.SettlementCurrency) {
		commissionPnL = fill.Commission.Decimal().Neg()
	}
	realized := commissionPnL
	before := position.SignedQuantity
	fillQuantity := fill.Quantity
	if fill.Side == Buy {
		if before.Sign() > 0 {
			position.AverageOpen = weightedAverage(
				position.AverageOpen,
				absDecimal(before),
				fill.Price,
				fillQuantity,
			)
		} else if before.Sign() < 0 {
			closingQuantity := minDecimal(absDecimal(before), fillQuantity)
			position.updateAverageClose(fill.Price, closingQuantity)
			realized = realized.Add(position.pnlDecimal(position.AverageOpen, fill.Price, closingQuantity, Short))
			position.updateReturn(position.AverageOpen, fill.Price, Short)
		}
		position.SignedQuantity = before.Add(fillQuantity)
		position.BuyQuantity = position.BuyQuantity.Add(fillQuantity)
		if before.Sign() < 0 && position.SignedQuantity.Sign() > 0 {
			position.AverageOpen = fill.Price
		}
	} else {
		if before.Sign() < 0 {
			position.AverageOpen = weightedAverage(
				position.AverageOpen,
				absDecimal(before),
				fill.Price,
				fillQuantity,
			)
		} else if before.Sign() > 0 {
			closingQuantity := minDecimal(absDecimal(before), fillQuantity)
			position.updateAverageClose(fill.Price, closingQuantity)
			realized = realized.Add(position.pnlDecimal(position.AverageOpen, fill.Price, closingQuantity, Long))
			position.updateReturn(position.AverageOpen, fill.Price, Long)
		}
		position.SignedQuantity = before.Sub(fillQuantity)
		position.SellQuantity = position.SellQuantity.Add(fillQuantity)
		if before.Sign() > 0 && position.SignedQuantity.Sign() < 0 {
			position.AverageOpen = fill.Price
		}
	}
	position.addRealized(realized)

	if position.Instrument.CurrencyPair &&
		fill.Commission != nil &&
		position.Instrument.BaseCurrency != nil &&
		fill.Commission.Currency().Equal(*position.Instrument.BaseCurrency) {
		change := fill.Commission.Decimal().Neg()
		reason := fill.ClientOrderID
		position.applyAdjustment(Adjustment{
			Type: Commission, QuantityChange: &change, Reason: &reason,
			TsEvent: fill.TsEvent, TsInit: fill.TsInit,
		}, false)
	}
	position.refreshState(fill)
	return nil
}

func (position *Position) resetCycle(fill Fill) {
	position.Events = nil
	position.Adjustments = nil
	position.tradeIDs = make(map[string]struct{})
	position.commissions = make(map[string]money.Money)
	position.commissionOrder = nil
	position.OpeningOrderID = fill.ClientOrderID
	position.ClosingOrderID = nil
	position.Entry = fill.Side
	position.SignedQuantity = decimal.Decimal{}
	position.Quantity = decimal.Decimal{}.Quantize(position.Instrument.SizePrecision, decimal.RoundHalfEven)
	position.PeakQuantity = position.Quantity
	position.AverageOpen = fill.Price
	position.AverageClose = nil
	position.RealizedReturn = decimal.Decimal{}
	position.RealizedPnL = nil
	position.BuyQuantity = decimal.Decimal{}
	position.SellQuantity = decimal.Decimal{}
	position.TsInit = fill.TsInit
	position.TsOpened = fill.TsEvent
	position.TsLast = fill.TsEvent
	position.TsClosed = nil
	position.Duration = 0
}

func (position *Position) refreshState(fill Fill) {
	position.Quantity = absDecimal(position.SignedQuantity).
		Quantize(position.Instrument.SizePrecision, decimal.RoundHalfEven)
	if position.Quantity.IsZero() {
		position.Side = Flat
		position.SignedQuantity = decimal.Decimal{}
		closing := fill.ClientOrderID
		position.ClosingOrderID = &closing
		closed := fill.TsEvent
		position.TsClosed = &closed
		if closed > position.TsOpened {
			position.Duration = closed - position.TsOpened
		} else {
			position.Duration = 0
		}
	} else if position.SignedQuantity.Sign() > 0 {
		position.Side = Long
		position.Entry = Buy
	} else {
		position.Side = Short
		position.Entry = Sell
	}
	if position.Quantity.Cmp(position.PeakQuantity) > 0 {
		position.PeakQuantity = position.Quantity
	}
	position.TsLast = fill.TsEvent
}

func (position *Position) ApplyAdjustment(adjustment Adjustment) {
	position.applyAdjustment(adjustment, true)
}

func (position *Position) applyAdjustment(adjustment Adjustment, record bool) {
	if record {
		copy := adjustment
		position.replay = append(position.replay, replayEvent{Adjustment: &copy})
	}
	if adjustment.QuantityChange != nil {
		position.SignedQuantity = position.SignedQuantity.Add(*adjustment.QuantityChange)
		position.Quantity = absDecimal(position.SignedQuantity).
			Quantize(position.Instrument.SizePrecision, decimal.RoundHalfEven)
		if position.Quantity.Cmp(position.PeakQuantity) > 0 {
			position.PeakQuantity = position.Quantity
		}
	}
	if adjustment.PnLChange != nil {
		position.addRealized(adjustment.PnLChange.Decimal())
	}
	if position.Quantity.IsZero() {
		position.Side = Flat
		position.SignedQuantity = decimal.Decimal{}
	} else if position.SignedQuantity.Sign() > 0 {
		position.Side = Long
		if position.Entry == NoOrderSide {
			position.Entry = Buy
		}
	} else {
		position.Side = Short
		if position.Entry == NoOrderSide {
			position.Entry = Sell
		}
	}
	position.Adjustments = append(position.Adjustments, adjustment)
	position.TsLast = adjustment.TsEvent
}

func (position *Position) CalculatePnL(open, close, quantity decimal.Decimal) (money.Money, error) {
	if position.Instrument.Inverse {
		if position.Instrument.BaseCurrency == nil {
			return money.Money{}, fmt.Errorf("inverse position %s has no base currency", position.Instrument.ID)
		}
		if open.Sign() <= 0 || close.Sign() <= 0 {
			return money.Money{}, errors.New("price must be positive for inverse notional valuation")
		}
	}
	value := position.pnlDecimal(open, close, minDecimal(quantity, absDecimal(position.SignedQuantity)), position.Side)
	return money.FromDecimal(value, position.Instrument.SettlementCurrency)
}

func (position *Position) pnlDecimal(open, close, quantity decimal.Decimal, side Side) decimal.Decimal {
	var points decimal.Decimal
	if position.Instrument.Inverse {
		if open.Sign() <= 0 || close.Sign() <= 0 {
			return decimal.Decimal{}
		}
		inverseOpen, _ := decimal.MustParse("1").Quo(open, 28, decimal.RoundHalfEven)
		inverseClose, _ := decimal.MustParse("1").Quo(close, 28, decimal.RoundHalfEven)
		if side == Long {
			points = inverseOpen.Sub(inverseClose)
		} else if side == Short {
			points = inverseClose.Sub(inverseOpen)
		}
	} else if side == Long {
		points = close.Sub(open)
	} else if side == Short {
		points = open.Sub(close)
	}
	return quantity.Mul(position.Instrument.Multiplier).Mul(points)
}

func (position *Position) UnrealizedPnL(last decimal.Decimal) (money.Money, error) {
	if position.Side == Flat {
		return money.Zero(position.Instrument.SettlementCurrency), nil
	}
	if position.Instrument.Inverse && last.Sign() <= 0 {
		return money.Money{}, errors.New("price must be positive for inverse notional valuation")
	}
	if position.Instrument.Inverse && position.Instrument.BaseCurrency == nil {
		return money.Money{}, fmt.Errorf("inverse position %s has no base currency", position.Instrument.ID)
	}
	return position.CalculatePnL(position.AverageOpen, last, position.Quantity)
}

func (position *Position) TotalPnL(last decimal.Decimal) (money.Money, error) {
	unrealized, err := position.UnrealizedPnL(last)
	if err != nil {
		return money.Money{}, err
	}
	if position.RealizedPnL == nil {
		return unrealized, nil
	}
	return position.RealizedPnL.Add(unrealized), nil
}

func (position *Position) NotionalValue(last decimal.Decimal) (money.Money, error) {
	value := position.Quantity.Mul(position.Instrument.Multiplier)
	denomination := position.Instrument.SettlementCurrency
	if position.Instrument.Inverse {
		if position.Instrument.BaseCurrency == nil {
			return money.Money{}, fmt.Errorf("inverse position %s has no base currency", position.Instrument.ID)
		}
		if last.Sign() <= 0 {
			return money.Money{}, errors.New("price must be positive for inverse notional valuation")
		}
		denomination = *position.Instrument.BaseCurrency
		var err error
		value, err = value.Quo(last, 28, decimal.RoundHalfEven)
		if err != nil {
			return money.Money{}, err
		}
	} else {
		value = value.Mul(last)
	}
	return money.FromDecimal(value, denomination)
}

func (position *Position) ClosingOrderSide() OrderSide {
	if position.Side == Long {
		return Sell
	}
	if position.Side == Short {
		return Buy
	}
	return NoOrderSide
}

func (position *Position) IsOppositeSide(side OrderSide) bool { return position.Entry != side }
func (position *Position) IsLong() bool                       { return position.Side == Long }
func (position *Position) IsShort() bool                      { return position.Side == Short }
func (position *Position) IsOpen() bool                       { return position.Side != Flat && position.TsClosed == nil }
func (position *Position) IsClosed() bool                     { return position.Side == Flat && position.TsClosed != nil }
func (position *Position) EventCount() int                    { return len(position.Events) }
func (position *Position) TradeIDs() []string {
	result := make([]string, 0, len(position.tradeIDs))
	for tradeID := range position.tradeIDs {
		result = append(result, tradeID)
	}
	sort.Strings(result)
	return result
}
func (position *Position) LastEvent() *Fill {
	if len(position.Events) == 0 {
		return nil
	}
	event := position.Events[len(position.Events)-1]
	return &event
}
func (position *Position) LastTradeID() *string {
	event := position.LastEvent()
	if event == nil {
		return nil
	}
	return &event.TradeID
}

func (position *Position) Commissions() []money.Money {
	result := make([]money.Money, 0, len(position.commissionOrder))
	for _, code := range position.commissionOrder {
		result = append(result, position.commissions[code])
	}
	return result
}

func (position *Position) String() string {
	quantity := ""
	if !position.Quantity.IsZero() {
		quantity = formatQuantity(position.Quantity) + " "
	}
	return fmt.Sprintf("Position(%s %s%s, id=%s)", position.Side, quantity, position.Instrument.ID, position.ID)
}

func (position *Position) addCommission(commission *money.Money) {
	if commission == nil {
		return
	}
	code := commission.Currency().Code
	if current, exists := position.commissions[code]; exists {
		position.commissions[code] = current.Add(*commission)
		return
	}
	position.commissionOrder = append(position.commissionOrder, code)
	position.commissions[code] = *commission
}

func (position *Position) addRealized(change decimal.Decimal) {
	current := decimal.Decimal{}
	if position.RealizedPnL != nil {
		current = position.RealizedPnL.Decimal()
	}
	value, err := money.FromDecimal(current.Add(change), position.Instrument.SettlementCurrency)
	if err != nil {
		panic(err)
	}
	position.RealizedPnL = &value
}

func (position *Position) updateAverageClose(price, quantity decimal.Decimal) {
	if position.AverageClose == nil {
		value := price
		position.AverageClose = &value
		return
	}
	closed := position.SellQuantity
	if position.Side == Short {
		closed = position.BuyQuantity
	}
	value := weightedAverage(*position.AverageClose, closed, price, quantity)
	position.AverageClose = &value
}

func (position *Position) updateReturn(open, close decimal.Decimal, side Side) {
	points := close.Sub(open)
	if side == Short {
		points = open.Sub(close)
	}
	result, err := points.Quo(open, 28, decimal.RoundHalfEven)
	if err == nil {
		position.RealizedReturn = result
	}
}

func weightedAverage(firstPrice, firstQuantity, secondPrice, secondQuantity decimal.Decimal) decimal.Decimal {
	total := firstQuantity.Add(secondQuantity)
	if total.IsZero() {
		return decimal.Decimal{}
	}
	value, _ := firstPrice.Mul(firstQuantity).
		Add(secondPrice.Mul(secondQuantity)).
		Quo(total, 28, decimal.RoundHalfEven)
	return value
}

func absDecimal(value decimal.Decimal) decimal.Decimal {
	if value.Sign() < 0 {
		return value.Neg()
	}
	return value
}

func minDecimal(left, right decimal.Decimal) decimal.Decimal {
	if left.Cmp(right) <= 0 {
		return left
	}
	return right
}

func formatQuantity(value decimal.Decimal) string {
	text := value.String()
	parts := strings.SplitN(text, ".", 2)
	sign := ""
	integer := parts[0]
	if strings.HasPrefix(integer, "-") {
		sign = "-"
		integer = strings.TrimPrefix(integer, "-")
	}
	for index := len(integer) - 3; index > 0; index -= 3 {
		integer = integer[:index] + "_" + integer[index:]
	}
	if len(parts) == 2 {
		return sign + integer + "." + parts[1]
	}
	return sign + integer
}

// FoldNetPosition replays exact signed quantity, price, timestamp legs.
func FoldNetPosition(legs []NetLeg) (decimal.Decimal, decimal.Decimal) {
	sorted := append([]NetLeg(nil), legs...)
	sorted = slicesWithoutZero(sorted)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].TsOpened < sorted[j].TsOpened })
	netQuantity := decimal.Decimal{}
	netAverage := decimal.Decimal{}
	for _, leg := range sorted {
		if netQuantity.IsZero() {
			netQuantity, netAverage = leg.SignedQuantity, leg.AverageOpen
			continue
		}
		sameSide := netQuantity.Sign() == leg.SignedQuantity.Sign()
		next := netQuantity.Add(leg.SignedQuantity)
		if sameSide {
			netAverage = weightedAverage(netAverage, absDecimal(netQuantity), leg.AverageOpen, absDecimal(leg.SignedQuantity))
			netQuantity = next
		} else if next.IsZero() || next.Sign() == netQuantity.Sign() {
			netQuantity = next
			if next.IsZero() {
				netAverage = decimal.Decimal{}
			}
		} else {
			netQuantity, netAverage = next, leg.AverageOpen
		}
	}
	return netQuantity, netAverage
}

type NetLeg struct {
	SignedQuantity decimal.Decimal
	AverageOpen    decimal.Decimal
	TsOpened       uint64
}

func slicesWithoutZero(legs []NetLeg) []NetLeg {
	result := legs[:0]
	for _, leg := range legs {
		if !leg.SignedQuantity.IsZero() {
			result = append(result, leg)
		}
	}
	return result
}

type currencyWire struct {
	Code      string
	Precision uint8
	ISO4217   uint16
	Name      string
	Type      currency.Type
}

type instrumentWire struct {
	ID                 string
	PricePrecision     uint8
	SizePrecision      uint8
	Multiplier         string
	Inverse            bool
	CurrencyPair       bool
	BaseCurrency       *currencyWire
	QuoteCurrency      currencyWire
	SettlementCurrency currencyWire
}

type moneyWire struct {
	Amount   string
	Currency currencyWire
}

type fillWire struct {
	ClientOrderID string
	TradeID       string
	Side          OrderSide
	Quantity      string
	Price         string
	Commission    *moneyWire
	TsEvent       uint64
	TsInit        uint64
}

type adjustmentWire struct {
	Type           AdjustmentType
	QuantityChange *string
	PnLChange      *moneyWire
	Reason         *string
	TsEvent        uint64
	TsInit         uint64
}

type replayWire struct {
	Fill       *fillWire
	Adjustment *adjustmentWire
}

type fillVoidWire struct {
	ClientOrderID  string
	TradeID        string
	VoidedQuantity string
	CommissionVoid *moneyWire
}

type positionWire struct {
	Instrument instrumentWire
	ID         string
	Replay     []replayWire
	FillVoids  []fillVoidWire
}

func currencyToWire(value currency.Currency) currencyWire {
	return currencyWire{value.Code, value.Precision, value.ISO4217, value.Name, value.Type}
}

func currencyFromWire(value currencyWire) currency.Currency {
	if value.Code == "" {
		return currency.Currency{}
	}
	return currency.MustNew(value.Code, value.Precision, value.ISO4217, value.Name, value.Type)
}

func moneyToWire(value *money.Money) *moneyWire {
	if value == nil {
		return nil
	}
	return &moneyWire{Amount: value.Decimal().String(), Currency: currencyToWire(value.Currency())}
}

func moneyFromWire(value *moneyWire) *money.Money {
	if value == nil {
		return nil
	}
	result := money.MustNew(value.Amount, currencyFromWire(value.Currency))
	return &result
}

func (position Position) MarshalJSON() ([]byte, error) {
	instrument := instrumentWire{
		ID:                 position.Instrument.ID,
		PricePrecision:     position.Instrument.PricePrecision,
		SizePrecision:      position.Instrument.SizePrecision,
		Multiplier:         position.Instrument.Multiplier.String(),
		Inverse:            position.Instrument.Inverse,
		CurrencyPair:       position.Instrument.CurrencyPair,
		QuoteCurrency:      currencyToWire(position.Instrument.QuoteCurrency),
		SettlementCurrency: currencyToWire(position.Instrument.SettlementCurrency),
	}
	if position.Instrument.BaseCurrency != nil {
		base := currencyToWire(*position.Instrument.BaseCurrency)
		instrument.BaseCurrency = &base
	}
	wire := positionWire{Instrument: instrument, ID: position.ID}
	for _, event := range position.replay {
		item := replayWire{}
		if event.Fill != nil {
			item.Fill = &fillWire{
				ClientOrderID: event.Fill.ClientOrderID,
				TradeID:       event.Fill.TradeID,
				Side:          event.Fill.Side,
				Quantity:      event.Fill.Quantity.String(),
				Price:         event.Fill.Price.String(),
				Commission:    moneyToWire(event.Fill.Commission),
				TsEvent:       event.Fill.TsEvent,
				TsInit:        event.Fill.TsInit,
			}
		} else if event.Adjustment != nil {
			item.Adjustment = &adjustmentWire{
				Type:      event.Adjustment.Type,
				PnLChange: moneyToWire(event.Adjustment.PnLChange),
				Reason:    event.Adjustment.Reason,
				TsEvent:   event.Adjustment.TsEvent,
				TsInit:    event.Adjustment.TsInit,
			}
			if event.Adjustment.QuantityChange != nil {
				value := event.Adjustment.QuantityChange.String()
				item.Adjustment.QuantityChange = &value
			}
		}
		wire.Replay = append(wire.Replay, item)
	}
	for _, fillVoid := range position.FillVoids {
		wire.FillVoids = append(wire.FillVoids, fillVoidWire{
			ClientOrderID: fillVoid.ClientOrderID, TradeID: fillVoid.TradeID,
			VoidedQuantity: fillVoid.VoidedQuantity.String(),
			CommissionVoid: moneyToWire(fillVoid.CommissionVoid),
		})
	}
	return json.Marshal(wire)
}

func (position *Position) UnmarshalJSON(data []byte) error {
	var wire positionWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	base := (*currency.Currency)(nil)
	if wire.Instrument.BaseCurrency != nil {
		value := currencyFromWire(*wire.Instrument.BaseCurrency)
		base = &value
	}
	*position = Position{
		Instrument: Instrument{
			ID:                 wire.Instrument.ID,
			PricePrecision:     wire.Instrument.PricePrecision,
			SizePrecision:      wire.Instrument.SizePrecision,
			Multiplier:         decimal.MustParse(wire.Instrument.Multiplier),
			Inverse:            wire.Instrument.Inverse,
			CurrencyPair:       wire.Instrument.CurrencyPair,
			BaseCurrency:       base,
			QuoteCurrency:      currencyFromWire(wire.Instrument.QuoteCurrency),
			SettlementCurrency: currencyFromWire(wire.Instrument.SettlementCurrency),
		},
		ID: wire.ID,
	}
	for _, item := range wire.Replay {
		if item.Fill != nil {
			fill := Fill{
				ClientOrderID: item.Fill.ClientOrderID, TradeID: item.Fill.TradeID, Side: item.Fill.Side,
				Quantity: decimal.MustParse(item.Fill.Quantity), Price: decimal.MustParse(item.Fill.Price),
				Commission: moneyFromWire(item.Fill.Commission), TsEvent: item.Fill.TsEvent, TsInit: item.Fill.TsInit,
			}
			position.replay = append(position.replay, replayEvent{Fill: &fill})
		} else if item.Adjustment != nil {
			adjustment := Adjustment{
				Type: item.Adjustment.Type, PnLChange: moneyFromWire(item.Adjustment.PnLChange),
				Reason: item.Adjustment.Reason, TsEvent: item.Adjustment.TsEvent, TsInit: item.Adjustment.TsInit,
			}
			if item.Adjustment.QuantityChange != nil {
				value := decimal.MustParse(*item.Adjustment.QuantityChange)
				adjustment.QuantityChange = &value
			}
			position.replay = append(position.replay, replayEvent{Adjustment: &adjustment})
		}
	}
	for _, item := range wire.FillVoids {
		position.FillVoids = append(position.FillVoids, FillVoid{
			ClientOrderID: item.ClientOrderID, TradeID: item.TradeID,
			VoidedQuantity: decimal.MustParse(item.VoidedQuantity),
			CommissionVoid: moneyFromWire(item.CommissionVoid),
		})
	}
	position.rebuild()
	return nil
}
