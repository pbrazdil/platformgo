package order

import (
	"fmt"
	"strings"

	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
)

type TrailingStopMarketConfig struct {
	TraderID            ids.TraderID
	StrategyID          ids.StrategyID
	InstrumentID        ids.InstrumentID
	ClientOrderID       ids.ClientOrderID
	Side                OrderSide
	Quantity            decimal.Quantity
	ActivationPrice     *decimal.Price
	TriggerPrice        *decimal.Price
	TriggerType         TriggerType
	TrailingOffset      decimal.Decimal
	TrailingOffsetType  TrailingOffsetType
	TimeInForce         TimeInForce
	ExpireTime          *uint64
	ReduceOnly          bool
	QuoteQuantity       bool
	DisplayQuantity     *decimal.Quantity
	TriggerInstrumentID *ids.InstrumentID
	OrderListID         *ids.OrderListID
	Tags                []string
	TimestampInit       uint64
}

type TrailingStopMarket struct {
	core                *Core
	traderID            ids.TraderID
	strategyID          ids.StrategyID
	activationPrice     *decimal.Price
	triggerPrice        *decimal.Price
	triggerType         TriggerType
	trailingOffset      decimal.Decimal
	trailingOffsetType  TrailingOffsetType
	expireTime          *uint64
	displayQuantity     *decimal.Quantity
	triggerInstrumentID *ids.InstrumentID
	tags                []string
	activated           bool
	triggered           bool
	slippage            *decimal.Decimal
}

func NewTrailingStopMarket(config TrailingStopMarketConfig) (*TrailingStopMarket, error) {
	if config.TraderID == "" {
		config.TraderID = ids.DefaultTraderID()
	}
	if config.StrategyID == "" {
		config.StrategyID = ids.MustStrategyID("S-001")
	}
	if config.InstrumentID.String() == "." {
		config.InstrumentID = ids.MustInstrumentID("AUD/USD.SIM")
	}
	if config.ClientOrderID == "" {
		config.ClientOrderID = ids.MustClientOrderID("O-19700101-000000-001-001-1")
	}
	if config.Side == OrderSideNoOrderSide {
		config.Side = OrderSideBuy
	}
	if config.TriggerType == TriggerTypeNoTrigger {
		config.TriggerType = TriggerTypeDefault
	}
	if config.TimeInForce == 0 {
		config.TimeInForce = TimeInForceGTC
	}
	if err := config.Quantity.RequirePositive("quantity"); err != nil {
		return nil, err
	}
	if err := CheckDisplayQuantity(config.DisplayQuantity, config.Quantity); err != nil {
		return nil, err
	}
	if config.TimeInForce == TimeInForceGTD &&
		(config.ExpireTime == nil || *config.ExpireTime == 0) {
		return nil, &Error{Kind: ErrorInvariant, Message: "`expire_time` is required for `GTD` order"}
	}
	core, err := NewCore(Config{
		ClientOrderID: config.ClientOrderID, InstrumentID: config.InstrumentID,
		Side: config.Side, Type: OrderTypeTrailingStopMarket,
		TimeInForce: config.TimeInForce, Quantity: config.Quantity,
		ActivationPrice: config.ActivationPrice, TriggerPrice: config.TriggerPrice,
		TriggerType: config.TriggerType, TrailingOffset: &config.TrailingOffset,
		TrailingOffsetType: config.TrailingOffsetType,
		ExpireTime:         config.ExpireTime, ReduceOnly: config.ReduceOnly,
		QuoteQuantity: config.QuoteQuantity, DisplayQuantity: config.DisplayQuantity,
		OrderListID: config.OrderListID, TimestampInit: config.TimestampInit,
	})
	if err != nil {
		return nil, err
	}
	return &TrailingStopMarket{
		core: core, traderID: config.TraderID, strategyID: config.StrategyID,
		activationPrice: copyPointerValue(config.ActivationPrice),
		triggerPrice:    copyPointerValue(config.TriggerPrice),
		triggerType:     config.TriggerType, trailingOffset: config.TrailingOffset,
		trailingOffsetType:  config.TrailingOffsetType,
		expireTime:          copyPointerValue(config.ExpireTime),
		displayQuantity:     copyPointerValue(config.DisplayQuantity),
		triggerInstrumentID: copyPointerValue(config.TriggerInstrumentID),
		tags:                append([]string(nil), config.Tags...),
	}, nil
}

type TrailingStopMarketUpdate struct {
	ClientOrderID ids.ClientOrderID
	StrategyID    ids.StrategyID
	TriggerPrice  *decimal.Price
	Quantity      decimal.Quantity
	Timestamp     uint64
}

func (o *TrailingStopMarket) Accept(accountID ids.AccountID, venueOrderID ids.VenueOrderID, timestamp uint64) error {
	return o.core.Accept(accountID, venueOrderID, timestamp)
}

func (o *TrailingStopMarket) ApplyUpdate(event TrailingStopMarketUpdate) error {
	if event.ClientOrderID != o.ClientOrderID() || event.StrategyID != o.StrategyID() {
		return &Error{Kind: ErrorInvalidOrderEvent}
	}
	if err := o.core.Update(Update{Quantity: &event.Quantity, Timestamp: event.Timestamp}); err != nil {
		return err
	}
	if event.TriggerPrice != nil {
		o.triggerPrice = copyPointer(*event.TriggerPrice)
		o.core.config.TriggerPrice = copyPointer(*event.TriggerPrice)
	}
	return nil
}

func (o *TrailingStopMarket) Fill(event Fill) error {
	if err := o.core.Fill(event); err != nil {
		return err
	}
	if o.triggerPrice == nil || o.core.AveragePrice() == nil {
		o.slippage = nil
		return nil
	}
	average := *o.core.AveragePrice()
	trigger := o.triggerPrice.Decimal()
	var value decimal.Decimal
	switch {
	case o.Side() == OrderSideBuy && average.Cmp(trigger) > 0:
		value = average.Sub(trigger)
	case o.Side() == OrderSideSell && average.Cmp(trigger) < 0:
		value = trigger.Sub(average)
	default:
		o.slippage = nil
		return nil
	}
	o.slippage = copyPointer(value.Normalize())
	return nil
}

func (o *TrailingStopMarket) TraderID() ids.TraderID           { return o.traderID }
func (o *TrailingStopMarket) StrategyID() ids.StrategyID       { return o.strategyID }
func (o *TrailingStopMarket) InstrumentID() ids.InstrumentID   { return o.core.config.InstrumentID }
func (o *TrailingStopMarket) ClientOrderID() ids.ClientOrderID { return o.core.config.ClientOrderID }
func (o *TrailingStopMarket) Side() OrderSide                  { return o.core.config.Side }
func (o *TrailingStopMarket) OrderType() OrderType             { return OrderTypeTrailingStopMarket }
func (o *TrailingStopMarket) Quantity() decimal.Quantity       { return o.core.Quantity() }
func (o *TrailingStopMarket) Price() *decimal.Price            { return nil }
func (o *TrailingStopMarket) ActivationPrice() *decimal.Price {
	return copyPointerValue(o.activationPrice)
}
func (o *TrailingStopMarket) TriggerPrice() *decimal.Price {
	return copyPointerValue(o.triggerPrice)
}
func (o *TrailingStopMarket) TriggerType() TriggerType { return o.triggerType }
func (o *TrailingStopMarket) TrailingOffset() decimal.Decimal {
	return decimal.MustParse(o.trailingOffset.String())
}
func (o *TrailingStopMarket) TrailingOffsetType() TrailingOffsetType {
	return o.trailingOffsetType
}
func (o *TrailingStopMarket) TimeInForce() TimeInForce         { return o.core.config.TimeInForce }
func (o *TrailingStopMarket) ExpireTime() *uint64              { return copyPointerValue(o.expireTime) }
func (o *TrailingStopMarket) IsActivated() bool                { return o.activated }
func (o *TrailingStopMarket) IsTriggered() bool                { return o.triggered }
func (o *TrailingStopMarket) FilledQuantity() decimal.Quantity { return o.core.FilledQuantity() }
func (o *TrailingStopMarket) LeavesQuantity() decimal.Quantity { return o.core.LeavesQuantity() }
func (o *TrailingStopMarket) DisplayQuantity() *decimal.Quantity {
	return copyPointerValue(o.displayQuantity)
}
func (o *TrailingStopMarket) TriggerInstrumentID() *ids.InstrumentID {
	return copyPointerValue(o.triggerInstrumentID)
}
func (o *TrailingStopMarket) Slippage() *decimal.Decimal {
	return copyDecimal(o.slippage)
}

func (o *TrailingStopMarket) String() string {
	venueOrderID := "None"
	venueIDs := o.core.VenueOrderIDs()
	if len(venueIDs) != 0 {
		venueOrderID = venueIDs[len(venueIDs)-1].String()
	}
	tags := "None"
	if len(o.tags) != 0 {
		tags = strings.Join(o.tags, ", ")
	}
	activation := "None"
	if o.activationPrice != nil {
		activation = o.activationPrice.String()
	}
	return fmt.Sprintf(
		"TrailingStopMarketOrder(%s %s %s TRAILING_STOP_MARKET %s, status=%s, client_order_id=%s, venue_order_id=%s, position_id=None, exec_algorithm_id=None, exec_spawn_id=None, tags=%s, activation_price=%s, is_activated=%t)",
		o.Side(), o.Quantity().FormattedString(), o.InstrumentID(), o.TimeInForce(),
		o.core.Status(), o.ClientOrderID(), venueOrderID, tags, activation, o.activated,
	)
}

type TrailingStopMarketInitialization struct {
	TraderID            ids.TraderID
	StrategyID          ids.StrategyID
	InstrumentID        ids.InstrumentID
	ClientOrderID       ids.ClientOrderID
	Side                OrderSide
	Quantity            decimal.Quantity
	ActivationPrice     *decimal.Price
	TriggerPrice        *decimal.Price
	TriggerType         *TriggerType
	TrailingOffset      *decimal.Decimal
	TrailingOffsetType  *TrailingOffsetType
	TimeInForce         TimeInForce
	ExpireTime          *uint64
	DisplayQuantity     *decimal.Quantity
	TriggerInstrumentID *ids.InstrumentID
	TimestampInit       uint64
}

func TrailingStopMarketFromInitialization(event TrailingStopMarketInitialization) (*TrailingStopMarket, error) {
	if event.TriggerType == nil {
		return nil, &Error{Kind: ErrorInvariant, Message: "`trigger_type` is required for `TrailingStopMarketOrder` initialization"}
	}
	if event.TrailingOffset == nil {
		return nil, &Error{Kind: ErrorInvariant, Message: "`trailing_offset` is required for `TrailingStopMarketOrder` initialization"}
	}
	if event.TrailingOffsetType == nil {
		return nil, &Error{Kind: ErrorInvariant, Message: "`trailing_offset_type` is required for `TrailingStopMarketOrder` initialization"}
	}
	return NewTrailingStopMarket(TrailingStopMarketConfig{
		TraderID: event.TraderID, StrategyID: event.StrategyID,
		InstrumentID: event.InstrumentID, ClientOrderID: event.ClientOrderID,
		Side: event.Side, Quantity: event.Quantity,
		ActivationPrice: event.ActivationPrice, TriggerPrice: event.TriggerPrice,
		TriggerType: *event.TriggerType, TrailingOffset: *event.TrailingOffset,
		TrailingOffsetType: *event.TrailingOffsetType,
		TimeInForce:        event.TimeInForce, ExpireTime: event.ExpireTime,
		DisplayQuantity:     event.DisplayQuantity,
		TriggerInstrumentID: event.TriggerInstrumentID,
		TimestampInit:       event.TimestampInit,
	})
}

func (o *TrailingStopMarket) Initialization() TrailingStopMarketInitialization {
	triggerType := o.triggerType
	offset := decimal.MustParse(o.trailingOffset.String())
	offsetType := o.trailingOffsetType
	return TrailingStopMarketInitialization{
		TraderID: o.TraderID(), StrategyID: o.StrategyID(),
		InstrumentID: o.InstrumentID(), ClientOrderID: o.ClientOrderID(),
		Side: o.Side(), Quantity: o.Quantity(),
		ActivationPrice: o.ActivationPrice(), TriggerPrice: o.TriggerPrice(),
		TriggerType: &triggerType, TrailingOffset: &offset,
		TrailingOffsetType: &offsetType, TimeInForce: o.TimeInForce(),
		ExpireTime: o.ExpireTime(), DisplayQuantity: o.DisplayQuantity(),
		TriggerInstrumentID: o.TriggerInstrumentID(),
	}
}
