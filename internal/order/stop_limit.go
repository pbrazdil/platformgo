package order

import (
	"fmt"
	"strings"

	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
)

type StopLimitConfig struct {
	TraderID            ids.TraderID
	StrategyID          ids.StrategyID
	InstrumentID        ids.InstrumentID
	ClientOrderID       ids.ClientOrderID
	Side                OrderSide
	Quantity            decimal.Quantity
	Price               decimal.Price
	TriggerPrice        decimal.Price
	TriggerType         TriggerType
	TimeInForce         TimeInForce
	ExpireTime          *uint64
	PostOnly            bool
	ReduceOnly          bool
	QuoteQuantity       bool
	DisplayQuantity     *decimal.Quantity
	TriggerInstrumentID *ids.InstrumentID
	Tags                []string
	TimestampInit       uint64
}

type StopLimit struct {
	core                *Core
	traderID            ids.TraderID
	strategyID          ids.StrategyID
	price               decimal.Price
	triggerPrice        decimal.Price
	triggerType         TriggerType
	expireTime          *uint64
	postOnly            bool
	reduceOnly          bool
	displayQuantity     *decimal.Quantity
	triggerInstrumentID *ids.InstrumentID
	tags                []string
	triggered           bool
}

func NewStopLimit(config StopLimitConfig) (*StopLimit, error) {
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
	if config.TimeInForce == 0 {
		config.TimeInForce = TimeInForceGTC
	}
	if config.TriggerType == TriggerTypeNoTrigger {
		config.TriggerType = TriggerTypeDefault
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
		Side: config.Side, Type: OrderTypeStopLimit,
		TimeInForce: config.TimeInForce, Quantity: config.Quantity,
		Price: &config.Price, TriggerPrice: &config.TriggerPrice,
		TriggerType: config.TriggerType, ExpireTime: config.ExpireTime,
		DisplayQuantity: config.DisplayQuantity, PostOnly: config.PostOnly,
		ReduceOnly: config.ReduceOnly, QuoteQuantity: config.QuoteQuantity,
		TimestampInit: config.TimestampInit,
	})
	if err != nil {
		return nil, err
	}
	return &StopLimit{
		core: core, traderID: config.TraderID, strategyID: config.StrategyID,
		price: config.Price, triggerPrice: config.TriggerPrice,
		triggerType: config.TriggerType, expireTime: copyPointerValue(config.ExpireTime),
		postOnly: config.PostOnly, reduceOnly: config.ReduceOnly,
		displayQuantity:     copyPointerValue(config.DisplayQuantity),
		triggerInstrumentID: copyPointerValue(config.TriggerInstrumentID),
		tags:                append([]string(nil), config.Tags...),
	}, nil
}

type StopLimitUpdate struct {
	ClientOrderID ids.ClientOrderID
	StrategyID    ids.StrategyID
	Price         *decimal.Price
	TriggerPrice  *decimal.Price
	Quantity      decimal.Quantity
	Timestamp     uint64
}

func (o *StopLimit) Accept(accountID ids.AccountID, venueOrderID ids.VenueOrderID, timestamp uint64) error {
	return o.core.Accept(accountID, venueOrderID, timestamp)
}

func (o *StopLimit) ApplyUpdate(event StopLimitUpdate) error {
	if event.ClientOrderID != o.ClientOrderID() || event.StrategyID != o.strategyID {
		return &Error{Kind: ErrorInvalidOrderEvent}
	}
	if err := o.core.Update(Update{Quantity: &event.Quantity, Timestamp: event.Timestamp}); err != nil {
		return err
	}
	if event.Price != nil {
		o.price = *event.Price
		o.core.config.Price = copyPointer(*event.Price)
	}
	if event.TriggerPrice != nil {
		o.triggerPrice = *event.TriggerPrice
		o.core.config.TriggerPrice = copyPointer(*event.TriggerPrice)
	}
	return nil
}

func (o *StopLimit) TraderID() ids.TraderID             { return o.traderID }
func (o *StopLimit) StrategyID() ids.StrategyID         { return o.strategyID }
func (o *StopLimit) InstrumentID() ids.InstrumentID     { return o.core.config.InstrumentID }
func (o *StopLimit) ClientOrderID() ids.ClientOrderID   { return o.core.config.ClientOrderID }
func (o *StopLimit) Side() OrderSide                    { return o.core.config.Side }
func (o *StopLimit) OrderType() OrderType               { return OrderTypeStopLimit }
func (o *StopLimit) Quantity() decimal.Quantity         { return o.core.Quantity() }
func (o *StopLimit) Price() decimal.Price               { return o.price }
func (o *StopLimit) TriggerPrice() decimal.Price        { return o.triggerPrice }
func (o *StopLimit) TriggerType() TriggerType           { return o.triggerType }
func (o *StopLimit) TimeInForce() TimeInForce           { return o.core.config.TimeInForce }
func (o *StopLimit) ExpireTime() *uint64                { return copyPointerValue(o.expireTime) }
func (o *StopLimit) IsPostOnly() bool                   { return o.postOnly }
func (o *StopLimit) IsReduceOnly() bool                 { return o.reduceOnly }
func (o *StopLimit) IsTriggered() bool                  { return o.triggered }
func (o *StopLimit) FilledQuantity() decimal.Quantity   { return o.core.FilledQuantity() }
func (o *StopLimit) LeavesQuantity() decimal.Quantity   { return o.core.LeavesQuantity() }
func (o *StopLimit) DisplayQuantity() *decimal.Quantity { return copyPointerValue(o.displayQuantity) }
func (o *StopLimit) TriggerInstrumentID() *ids.InstrumentID {
	return copyPointerValue(o.triggerInstrumentID)
}
func (o *StopLimit) WouldReduceOnly(side PositionSide, quantity decimal.Quantity) bool {
	return o.core.WouldReduceOnly(side, quantity)
}

func (o *StopLimit) String() string {
	venueOrderID := "None"
	venueIDs := o.core.VenueOrderIDs()
	if len(venueIDs) != 0 {
		venueOrderID = venueIDs[len(venueIDs)-1].String()
	}
	tags := "None"
	if len(o.tags) != 0 {
		tags = strings.Join(o.tags, ", ")
	}
	return fmt.Sprintf(
		"StopLimitOrder(%s %s %s %s @ %s-STOP[%s] %s-LIMIT %s, status=%s, client_order_id=%s, venue_order_id=%s, position_id=None, tags=%s)",
		o.Side(), o.Quantity().FormattedString(), o.InstrumentID(), o.OrderType(),
		o.TriggerPrice(), o.TriggerType(), o.Price(), o.TimeInForce(),
		o.core.Status(), o.ClientOrderID(), venueOrderID, tags,
	)
}

type StopLimitInitialization struct {
	TraderID            ids.TraderID
	StrategyID          ids.StrategyID
	InstrumentID        ids.InstrumentID
	ClientOrderID       ids.ClientOrderID
	Side                OrderSide
	Quantity            decimal.Quantity
	Price               *decimal.Price
	TriggerPrice        *decimal.Price
	TriggerType         *TriggerType
	TimeInForce         TimeInForce
	ExpireTime          *uint64
	PostOnly            bool
	ReduceOnly          bool
	DisplayQuantity     *decimal.Quantity
	TriggerInstrumentID *ids.InstrumentID
	TimestampInit       uint64
}

func StopLimitFromInitialization(event StopLimitInitialization) (*StopLimit, error) {
	if event.Price == nil {
		return nil, &Error{Kind: ErrorInvariant, Message: "`price` is required for `StopLimitOrder` initialization"}
	}
	if event.TriggerPrice == nil {
		return nil, &Error{Kind: ErrorInvariant, Message: "`trigger_price` is required for `StopLimitOrder` initialization"}
	}
	if event.TriggerType == nil {
		return nil, &Error{Kind: ErrorInvariant, Message: "`trigger_type` is required for `StopLimitOrder` initialization"}
	}
	return NewStopLimit(StopLimitConfig{
		TraderID: event.TraderID, StrategyID: event.StrategyID,
		InstrumentID: event.InstrumentID, ClientOrderID: event.ClientOrderID,
		Side: event.Side, Quantity: event.Quantity, Price: *event.Price,
		TriggerPrice: *event.TriggerPrice, TriggerType: *event.TriggerType,
		TimeInForce: event.TimeInForce, ExpireTime: event.ExpireTime,
		PostOnly: event.PostOnly, ReduceOnly: event.ReduceOnly,
		DisplayQuantity:     event.DisplayQuantity,
		TriggerInstrumentID: event.TriggerInstrumentID,
		TimestampInit:       event.TimestampInit,
	})
}

type MarketToLimitDisplay struct {
	InstrumentID  ids.InstrumentID
	ClientOrderID ids.ClientOrderID
	Side          OrderSide
	Quantity      decimal.Quantity
}

func (o MarketToLimitDisplay) String() string {
	return fmt.Sprintf(
		"MarketToLimitOrder(%s %s %s MARKET_TO_LIMIT GTC, status=INITIALIZED, client_order_id=%s, venue_order_id=None, position_id=None, exec_algorithm_id=None, exec_spawn_id=None, tags=None)",
		o.Side, o.Quantity.FormattedString(), o.InstrumentID, o.ClientOrderID,
	)
}
