package order

import (
	"fmt"
	"strings"

	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
)

type TrailingStopLimitConfig struct {
	TraderID            ids.TraderID
	StrategyID          ids.StrategyID
	InstrumentID        ids.InstrumentID
	ClientOrderID       ids.ClientOrderID
	Side                OrderSide
	Quantity            decimal.Quantity
	Price               *decimal.Price
	ActivationPrice     *decimal.Price
	TriggerPrice        *decimal.Price
	TriggerType         TriggerType
	LimitOffset         decimal.Decimal
	TrailingOffset      decimal.Decimal
	TrailingOffsetType  TrailingOffsetType
	TimeInForce         TimeInForce
	ExpireTime          *uint64
	DisplayQuantity     *decimal.Quantity
	TriggerInstrumentID *ids.InstrumentID
	OrderListID         *ids.OrderListID
	Tags                []string
	TimestampInit       uint64
}

type TrailingStopLimit struct {
	core                *Core
	traderID            ids.TraderID
	strategyID          ids.StrategyID
	price               *decimal.Price
	activationPrice     *decimal.Price
	triggerPrice        *decimal.Price
	triggerType         TriggerType
	limitOffset         decimal.Decimal
	trailingOffset      decimal.Decimal
	trailingOffsetType  TrailingOffsetType
	expireTime          *uint64
	displayQuantity     *decimal.Quantity
	triggerInstrumentID *ids.InstrumentID
	tags                []string
	activated           bool
	triggered           bool
}

func NewTrailingStopLimit(config TrailingStopLimitConfig) (*TrailingStopLimit, error) {
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
		Side: config.Side, Type: OrderTypeTrailingStopLimit,
		TimeInForce: config.TimeInForce, Quantity: config.Quantity,
		Price: config.Price, ActivationPrice: config.ActivationPrice,
		TriggerPrice: config.TriggerPrice, TriggerType: config.TriggerType,
		LimitOffset: &config.LimitOffset, TrailingOffset: &config.TrailingOffset,
		TrailingOffsetType: config.TrailingOffsetType,
		ExpireTime:         config.ExpireTime, DisplayQuantity: config.DisplayQuantity,
		OrderListID: config.OrderListID, TimestampInit: config.TimestampInit,
	})
	if err != nil {
		return nil, err
	}
	return &TrailingStopLimit{
		core: core, traderID: config.TraderID, strategyID: config.StrategyID,
		price:           copyPointerValue(config.Price),
		activationPrice: copyPointerValue(config.ActivationPrice),
		triggerPrice:    copyPointerValue(config.TriggerPrice),
		triggerType:     config.TriggerType, limitOffset: config.LimitOffset,
		trailingOffset:      config.TrailingOffset,
		trailingOffsetType:  config.TrailingOffsetType,
		expireTime:          copyPointerValue(config.ExpireTime),
		displayQuantity:     copyPointerValue(config.DisplayQuantity),
		triggerInstrumentID: copyPointerValue(config.TriggerInstrumentID),
		tags:                append([]string(nil), config.Tags...),
	}, nil
}

type TrailingStopLimitUpdate struct {
	ClientOrderID ids.ClientOrderID
	StrategyID    ids.StrategyID
	TriggerPrice  *decimal.Price
	Quantity      decimal.Quantity
	Timestamp     uint64
}

func (o *TrailingStopLimit) Accept(accountID ids.AccountID, venueOrderID ids.VenueOrderID, timestamp uint64) error {
	return o.core.Accept(accountID, venueOrderID, timestamp)
}

func (o *TrailingStopLimit) ApplyUpdate(event TrailingStopLimitUpdate) error {
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

func (o *TrailingStopLimit) TraderID() ids.TraderID           { return o.traderID }
func (o *TrailingStopLimit) StrategyID() ids.StrategyID       { return o.strategyID }
func (o *TrailingStopLimit) InstrumentID() ids.InstrumentID   { return o.core.config.InstrumentID }
func (o *TrailingStopLimit) ClientOrderID() ids.ClientOrderID { return o.core.config.ClientOrderID }
func (o *TrailingStopLimit) Side() OrderSide                  { return o.core.config.Side }
func (o *TrailingStopLimit) OrderType() OrderType             { return OrderTypeTrailingStopLimit }
func (o *TrailingStopLimit) Quantity() decimal.Quantity       { return o.core.Quantity() }
func (o *TrailingStopLimit) Price() *decimal.Price            { return copyPointerValue(o.price) }
func (o *TrailingStopLimit) HasPrice() bool                   { return o.price != nil }
func (o *TrailingStopLimit) ActivationPrice() *decimal.Price {
	return copyPointerValue(o.activationPrice)
}
func (o *TrailingStopLimit) TriggerPrice() *decimal.Price {
	return copyPointerValue(o.triggerPrice)
}
func (o *TrailingStopLimit) TriggerType() TriggerType { return o.triggerType }
func (o *TrailingStopLimit) LimitOffset() decimal.Decimal {
	return decimal.MustParse(o.limitOffset.String())
}
func (o *TrailingStopLimit) TrailingOffset() decimal.Decimal {
	return decimal.MustParse(o.trailingOffset.String())
}
func (o *TrailingStopLimit) TrailingOffsetType() TrailingOffsetType {
	return o.trailingOffsetType
}
func (o *TrailingStopLimit) TimeInForce() TimeInForce         { return o.core.config.TimeInForce }
func (o *TrailingStopLimit) ExpireTime() *uint64              { return copyPointerValue(o.expireTime) }
func (o *TrailingStopLimit) IsActivated() bool                { return o.activated }
func (o *TrailingStopLimit) IsTriggered() bool                { return o.triggered }
func (o *TrailingStopLimit) FilledQuantity() decimal.Quantity { return o.core.FilledQuantity() }
func (o *TrailingStopLimit) LeavesQuantity() decimal.Quantity { return o.core.LeavesQuantity() }
func (o *TrailingStopLimit) DisplayQuantity() *decimal.Quantity {
	return copyPointerValue(o.displayQuantity)
}
func (o *TrailingStopLimit) TriggerInstrumentID() *ids.InstrumentID {
	return copyPointerValue(o.triggerInstrumentID)
}

func (o *TrailingStopLimit) String() string {
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
		"TrailingStopLimitOrder(%s %s %s TRAILING_STOP_LIMIT %s, status=%s, client_order_id=%s, venue_order_id=%s, position_id=None, exec_algorithm_id=None, exec_spawn_id=None, tags=%s, activation_price=%s, is_activated=%t)",
		o.Side(), o.Quantity().FormattedString(), o.InstrumentID(), o.TimeInForce(),
		o.core.Status(), o.ClientOrderID(), venueOrderID, tags, activation, o.activated,
	)
}

type TrailingStopLimitInitialization struct {
	TraderID            ids.TraderID
	StrategyID          ids.StrategyID
	InstrumentID        ids.InstrumentID
	ClientOrderID       ids.ClientOrderID
	Side                OrderSide
	Quantity            decimal.Quantity
	Price               *decimal.Price
	ActivationPrice     *decimal.Price
	TriggerPrice        *decimal.Price
	TriggerType         *TriggerType
	LimitOffset         *decimal.Decimal
	TrailingOffset      *decimal.Decimal
	TrailingOffsetType  *TrailingOffsetType
	TimeInForce         TimeInForce
	ExpireTime          *uint64
	DisplayQuantity     *decimal.Quantity
	TriggerInstrumentID *ids.InstrumentID
	TimestampInit       uint64
}

func TrailingStopLimitFromInitialization(event TrailingStopLimitInitialization) (*TrailingStopLimit, error) {
	if event.TriggerType == nil {
		return nil, &Error{Kind: ErrorInvariant, Message: "`trigger_type` is required for `TrailingStopLimitOrder` initialization"}
	}
	if event.LimitOffset == nil {
		return nil, &Error{Kind: ErrorInvariant, Message: "`limit_offset` is required for `TrailingStopLimitOrder` initialization"}
	}
	if event.TrailingOffset == nil {
		return nil, &Error{Kind: ErrorInvariant, Message: "`trailing_offset` is required for `TrailingStopLimitOrder` initialization"}
	}
	if event.TrailingOffsetType == nil {
		return nil, &Error{Kind: ErrorInvariant, Message: "`trailing_offset_type` is required for `TrailingStopLimitOrder` initialization"}
	}
	return NewTrailingStopLimit(TrailingStopLimitConfig{
		TraderID: event.TraderID, StrategyID: event.StrategyID,
		InstrumentID: event.InstrumentID, ClientOrderID: event.ClientOrderID,
		Side: event.Side, Quantity: event.Quantity, Price: event.Price,
		ActivationPrice: event.ActivationPrice, TriggerPrice: event.TriggerPrice,
		TriggerType: *event.TriggerType, LimitOffset: *event.LimitOffset,
		TrailingOffset:     *event.TrailingOffset,
		TrailingOffsetType: *event.TrailingOffsetType,
		TimeInForce:        event.TimeInForce, ExpireTime: event.ExpireTime,
		DisplayQuantity:     event.DisplayQuantity,
		TriggerInstrumentID: event.TriggerInstrumentID,
		TimestampInit:       event.TimestampInit,
	})
}

func (o *TrailingStopLimit) Initialization() TrailingStopLimitInitialization {
	triggerType := o.triggerType
	limitOffset := decimal.MustParse(o.limitOffset.String())
	trailingOffset := decimal.MustParse(o.trailingOffset.String())
	offsetType := o.trailingOffsetType
	return TrailingStopLimitInitialization{
		TraderID: o.TraderID(), StrategyID: o.StrategyID(),
		InstrumentID: o.InstrumentID(), ClientOrderID: o.ClientOrderID(),
		Side: o.Side(), Quantity: o.Quantity(), Price: o.Price(),
		ActivationPrice: o.ActivationPrice(), TriggerPrice: o.TriggerPrice(),
		TriggerType: &triggerType, LimitOffset: &limitOffset,
		TrailingOffset: &trailingOffset, TrailingOffsetType: &offsetType,
		TimeInForce: o.TimeInForce(), ExpireTime: o.ExpireTime(),
		DisplayQuantity:     o.DisplayQuantity(),
		TriggerInstrumentID: o.TriggerInstrumentID(),
	}
}
