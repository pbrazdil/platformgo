package order

import (
	"encoding/json"
	"fmt"
	"math/big"
	"reflect"

	"github.com/upcomers-org/platformgo/internal/currency"
	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
	"github.com/upcomers-org/platformgo/internal/money"
)

type FillEventInfo struct {
	Key   string
	Value string
}

type SpecifiedOrderSide uint8

const (
	SpecifiedOrderSideBuy SpecifiedOrderSide = iota + 1
	SpecifiedOrderSideSell
)

type OrderFilledEventConfig struct {
	TraderID       ids.TraderID
	StrategyID     ids.StrategyID
	InstrumentID   ids.InstrumentID
	ClientOrderID  ids.ClientOrderID
	VenueOrderID   ids.VenueOrderID
	AccountID      ids.AccountID
	TradeID        ids.TradeID
	OrderSide      OrderSide
	OrderType      OrderType
	LastQuantity   decimal.Quantity
	LastPrice      decimal.Price
	Currency       currency.Currency
	LiquiditySide  LiquiditySide
	EventID        string
	TsEvent        uint64
	TsInit         uint64
	Reconciliation bool
	PositionID     *ids.PositionID
	Commission     *money.Money
	Info           []FillEventInfo
	CausationID    *string
}

// OrderFilledEvent represents one execution applied to an order.
type OrderFilledEvent struct {
	TraderID       ids.TraderID
	StrategyID     ids.StrategyID
	InstrumentID   ids.InstrumentID
	ClientOrderID  ids.ClientOrderID
	VenueOrderID   ids.VenueOrderID
	AccountID      ids.AccountID
	TradeID        ids.TradeID
	OrderSide      OrderSide
	OrderType      OrderType
	LastQuantity   decimal.Quantity
	LastPrice      decimal.Price
	Currency       currency.Currency
	LiquiditySide  LiquiditySide
	EventID        string
	TsEvent        uint64
	TsInit         uint64
	Reconciliation bool
	PositionID     *ids.PositionID
	Commission     *money.Money
	Info           []FillEventInfo
	CausationID    *string
}

func NewOrderFilledEvent(config OrderFilledEventConfig) OrderFilledEvent {
	return OrderFilledEvent{
		TraderID:       config.TraderID,
		StrategyID:     config.StrategyID,
		InstrumentID:   config.InstrumentID,
		ClientOrderID:  config.ClientOrderID,
		VenueOrderID:   config.VenueOrderID,
		AccountID:      config.AccountID,
		TradeID:        config.TradeID,
		OrderSide:      config.OrderSide,
		OrderType:      config.OrderType,
		LastQuantity:   config.LastQuantity,
		LastPrice:      config.LastPrice,
		Currency:       config.Currency,
		LiquiditySide:  config.LiquiditySide,
		EventID:        config.EventID,
		TsEvent:        config.TsEvent,
		TsInit:         config.TsInit,
		Reconciliation: config.Reconciliation,
		PositionID:     copyFilledPointer(config.PositionID),
		Commission:     copyFilledPointer(config.Commission),
		Info:           append([]FillEventInfo(nil), config.Info...),
		CausationID:    copyFilledPointer(config.CausationID),
	}
}

func (event OrderFilledEvent) SpecifiedSide() SpecifiedOrderSide {
	switch event.OrderSide {
	case OrderSideBuy:
		return SpecifiedOrderSideBuy
	case OrderSideSell:
		return SpecifiedOrderSideSell
	default:
		panic("order side is not specified")
	}
}

func (event OrderFilledEvent) IsBuy() bool {
	return event.OrderSide == OrderSideBuy
}

func (event OrderFilledEvent) IsSell() bool {
	return event.OrderSide == OrderSideSell
}

func (event OrderFilledEvent) Equal(other OrderFilledEvent) bool {
	return reflect.DeepEqual(event, other)
}

func (event OrderFilledEvent) String() string {
	positionID := "None"
	if event.PositionID != nil {
		positionID = event.PositionID.String()
	}
	commission := money.Zero(currency.USD()).String()
	if event.Commission != nil {
		commission = event.Commission.String()
	}
	return fmt.Sprintf(
		"OrderFilled(instrument_id=%s, client_order_id=%s, venue_order_id=%s, account_id=%s, trade_id=%s, position_id=%s, order_side=%s, order_type=%s, last_qty=%s, last_px=%s %s, commission=%s, liquidity_side=%s, ts_event=%d)",
		event.InstrumentID,
		event.ClientOrderID,
		event.VenueOrderID,
		event.AccountID,
		event.TradeID,
		positionID,
		event.OrderSide,
		event.OrderType,
		event.LastQuantity.FormattedString(),
		event.LastPrice.FormattedString(),
		event.Currency,
		commission,
		event.LiquiditySide,
		event.TsEvent,
	)
}

type filledCurrencyWire struct {
	Code      string
	Precision uint8
	ISO4217   uint16
	Name      string
	Type      currency.Type
}

type filledMoneyWire struct {
	Raw      string
	Currency filledCurrencyWire
}

type orderFilledEventWire struct {
	TraderID       ids.TraderID
	StrategyID     ids.StrategyID
	InstrumentID   ids.InstrumentID
	ClientOrderID  ids.ClientOrderID
	VenueOrderID   ids.VenueOrderID
	AccountID      ids.AccountID
	TradeID        ids.TradeID
	OrderSide      OrderSide
	OrderType      OrderType
	LastQuantity   decimal.Quantity
	LastPrice      decimal.Price
	Currency       filledCurrencyWire
	LiquiditySide  LiquiditySide
	EventID        string
	TsEvent        uint64
	TsInit         uint64
	Reconciliation bool
	PositionID     *ids.PositionID
	Commission     *filledMoneyWire
	Info           []FillEventInfo
	CausationID    *string `json:"causation_id,omitempty"`
}

func (event OrderFilledEvent) MarshalJSON() ([]byte, error) {
	var commission *filledMoneyWire
	if event.Commission != nil {
		commission = &filledMoneyWire{
			Raw:      event.Commission.Raw().String(),
			Currency: filledCurrencyFromCurrency(event.Commission.Currency()),
		}
	}
	return json.Marshal(orderFilledEventWire{
		TraderID:       event.TraderID,
		StrategyID:     event.StrategyID,
		InstrumentID:   event.InstrumentID,
		ClientOrderID:  event.ClientOrderID,
		VenueOrderID:   event.VenueOrderID,
		AccountID:      event.AccountID,
		TradeID:        event.TradeID,
		OrderSide:      event.OrderSide,
		OrderType:      event.OrderType,
		LastQuantity:   event.LastQuantity,
		LastPrice:      event.LastPrice,
		Currency:       filledCurrencyFromCurrency(event.Currency),
		LiquiditySide:  event.LiquiditySide,
		EventID:        event.EventID,
		TsEvent:        event.TsEvent,
		TsInit:         event.TsInit,
		Reconciliation: event.Reconciliation,
		PositionID:     event.PositionID,
		Commission:     commission,
		Info:           event.Info,
		CausationID:    event.CausationID,
	})
}

func (event *OrderFilledEvent) UnmarshalJSON(data []byte) error {
	var wire orderFilledEventWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	denomination, err := wire.Currency.toCurrency()
	if err != nil {
		return err
	}
	var commission *money.Money
	if wire.Commission != nil {
		commissionCurrency, err := wire.Commission.Currency.toCurrency()
		if err != nil {
			return err
		}
		raw, ok := new(big.Int).SetString(wire.Commission.Raw, 10)
		if !ok {
			return fmt.Errorf("invalid commission raw value %q", wire.Commission.Raw)
		}
		value, err := money.FromRawChecked(raw, commissionCurrency)
		if err != nil {
			return err
		}
		commission = &value
	}
	*event = NewOrderFilledEvent(OrderFilledEventConfig{
		TraderID:       wire.TraderID,
		StrategyID:     wire.StrategyID,
		InstrumentID:   wire.InstrumentID,
		ClientOrderID:  wire.ClientOrderID,
		VenueOrderID:   wire.VenueOrderID,
		AccountID:      wire.AccountID,
		TradeID:        wire.TradeID,
		OrderSide:      wire.OrderSide,
		OrderType:      wire.OrderType,
		LastQuantity:   wire.LastQuantity,
		LastPrice:      wire.LastPrice,
		Currency:       denomination,
		LiquiditySide:  wire.LiquiditySide,
		EventID:        wire.EventID,
		TsEvent:        wire.TsEvent,
		TsInit:         wire.TsInit,
		Reconciliation: wire.Reconciliation,
		PositionID:     wire.PositionID,
		Commission:     commission,
		Info:           wire.Info,
		CausationID:    wire.CausationID,
	})
	return nil
}

func filledCurrencyFromCurrency(value currency.Currency) filledCurrencyWire {
	return filledCurrencyWire{
		Code:      value.Code,
		Precision: value.Precision,
		ISO4217:   value.ISO4217,
		Name:      value.Name,
		Type:      value.Type,
	}
}

func (wire filledCurrencyWire) toCurrency() (currency.Currency, error) {
	return currency.New(wire.Code, wire.Precision, wire.ISO4217, wire.Name, wire.Type)
}

func copyFilledPointer[T any](value *T) *T {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}
