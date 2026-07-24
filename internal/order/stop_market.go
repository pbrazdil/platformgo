package order

import (
	"fmt"
	"strings"

	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
)

type StopMarketConfig struct {
	TraderID            ids.TraderID
	StrategyID          ids.StrategyID
	InstrumentID        ids.InstrumentID
	ClientOrderID       ids.ClientOrderID
	Side                OrderSide
	Quantity            decimal.Quantity
	TriggerPrice        decimal.Price
	TriggerType         TriggerType
	TimeInForce         TimeInForce
	ExpireTime          *uint64
	DisplayQuantity     *decimal.Quantity
	TriggerInstrumentID *ids.InstrumentID
	OrderListID         *ids.OrderListID
	Tags                []string
	TimestampInit       uint64
}

type StopMarket struct {
	core                *Core
	traderID            ids.TraderID
	strategyID          ids.StrategyID
	triggerPrice        decimal.Price
	triggerType         TriggerType
	expireTime          *uint64
	displayQuantity     *decimal.Quantity
	triggerInstrumentID *ids.InstrumentID
	protectionPrice     *decimal.Price
	tags                []string
	triggered           bool
}

func NewStopMarket(config StopMarketConfig) (*StopMarket, error) {
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
		Side: config.Side, Type: OrderTypeStopMarket,
		TimeInForce: config.TimeInForce, Quantity: config.Quantity,
		TriggerPrice: &config.TriggerPrice, TriggerType: config.TriggerType,
		ExpireTime: config.ExpireTime, DisplayQuantity: config.DisplayQuantity,
		OrderListID: config.OrderListID, TimestampInit: config.TimestampInit,
	})
	if err != nil {
		return nil, err
	}
	return &StopMarket{
		core: core, traderID: config.TraderID, strategyID: config.StrategyID,
		triggerPrice: config.TriggerPrice, triggerType: config.TriggerType,
		expireTime:          copyPointerValue(config.ExpireTime),
		displayQuantity:     copyPointerValue(config.DisplayQuantity),
		triggerInstrumentID: copyPointerValue(config.TriggerInstrumentID),
		tags:                append([]string(nil), config.Tags...),
	}, nil
}

type StopMarketUpdate struct {
	ClientOrderID   ids.ClientOrderID
	StrategyID      ids.StrategyID
	TriggerPrice    *decimal.Price
	ProtectionPrice *decimal.Price
	Quantity        *decimal.Quantity
	Timestamp       uint64
}

func (o *StopMarket) Accept(accountID ids.AccountID, venueOrderID ids.VenueOrderID, timestamp uint64) error {
	return o.core.Accept(accountID, venueOrderID, timestamp)
}

func (o *StopMarket) ApplyUpdate(event StopMarketUpdate) error {
	if event.ClientOrderID != o.ClientOrderID() || event.StrategyID != o.StrategyID() {
		return &Error{Kind: ErrorInvalidOrderEvent}
	}
	if err := o.core.Update(Update{Quantity: event.Quantity, Timestamp: event.Timestamp}); err != nil {
		return err
	}
	if event.TriggerPrice != nil {
		o.triggerPrice = *event.TriggerPrice
		o.core.config.TriggerPrice = copyPointer(*event.TriggerPrice)
	}
	o.protectionPrice = copyPointerValue(event.ProtectionPrice)
	return nil
}

func (o *StopMarket) TraderID() ids.TraderID           { return o.traderID }
func (o *StopMarket) StrategyID() ids.StrategyID       { return o.strategyID }
func (o *StopMarket) InstrumentID() ids.InstrumentID   { return o.core.config.InstrumentID }
func (o *StopMarket) ClientOrderID() ids.ClientOrderID { return o.core.config.ClientOrderID }
func (o *StopMarket) Quantity() decimal.Quantity       { return o.core.Quantity() }
func (o *StopMarket) TriggerPrice() decimal.Price      { return o.triggerPrice }
func (o *StopMarket) TriggerType() TriggerType         { return o.triggerType }
func (o *StopMarket) Price() *decimal.Price            { return copyPointerValue(o.protectionPrice) }
func (o *StopMarket) HasPrice() bool                   { return o.protectionPrice != nil }
func (o *StopMarket) TimeInForce() TimeInForce         { return o.core.config.TimeInForce }
func (o *StopMarket) ExpireTime() *uint64              { return copyPointerValue(o.expireTime) }
func (o *StopMarket) IsTriggered() bool                { return o.triggered }
func (o *StopMarket) FilledQuantity() decimal.Quantity { return o.core.FilledQuantity() }
func (o *StopMarket) LeavesQuantity() decimal.Quantity { return o.core.LeavesQuantity() }
func (o *StopMarket) DisplayQuantity() *decimal.Quantity {
	return copyPointerValue(o.displayQuantity)
}
func (o *StopMarket) TriggerInstrumentID() *ids.InstrumentID {
	return copyPointerValue(o.triggerInstrumentID)
}

func (o *StopMarket) String() string {
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
		"StopMarketOrder(%s %s %s STOP_MARKET %s, status=%s, client_order_id=%s, venue_order_id=%s, position_id=None, exec_algorithm_id=None, exec_spawn_id=None, tags=%s)",
		o.core.config.Side, o.Quantity().FormattedString(), o.InstrumentID(),
		o.TimeInForce(), o.core.Status(), o.ClientOrderID(), venueOrderID, tags,
	)
}

type StopMarketInitialization struct {
	TraderID            ids.TraderID
	StrategyID          ids.StrategyID
	InstrumentID        ids.InstrumentID
	ClientOrderID       ids.ClientOrderID
	Side                OrderSide
	Quantity            decimal.Quantity
	TriggerPrice        *decimal.Price
	TriggerType         *TriggerType
	TimeInForce         TimeInForce
	ExpireTime          *uint64
	DisplayQuantity     *decimal.Quantity
	TriggerInstrumentID *ids.InstrumentID
	TimestampInit       uint64
}

func StopMarketFromInitialization(event StopMarketInitialization) (*StopMarket, error) {
	if event.TriggerPrice == nil {
		return nil, &Error{Kind: ErrorInvariant, Message: "`trigger_price` is required for `StopMarketOrder` initialization"}
	}
	if event.TriggerType == nil {
		return nil, &Error{Kind: ErrorInvariant, Message: "`trigger_type` is required for `StopMarketOrder` initialization"}
	}
	return NewStopMarket(StopMarketConfig{
		TraderID: event.TraderID, StrategyID: event.StrategyID,
		InstrumentID: event.InstrumentID, ClientOrderID: event.ClientOrderID,
		Side: event.Side, Quantity: event.Quantity,
		TriggerPrice: *event.TriggerPrice, TriggerType: *event.TriggerType,
		TimeInForce: event.TimeInForce, ExpireTime: event.ExpireTime,
		DisplayQuantity:     event.DisplayQuantity,
		TriggerInstrumentID: event.TriggerInstrumentID,
		TimestampInit:       event.TimestampInit,
	})
}
