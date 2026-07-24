package order

import (
	"fmt"
	"strings"

	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
)

type MarketToLimitConfig struct {
	TraderID        ids.TraderID
	StrategyID      ids.StrategyID
	InstrumentID    ids.InstrumentID
	ClientOrderID   ids.ClientOrderID
	Side            OrderSide
	Quantity        decimal.Quantity
	InitialPrice    *decimal.Price
	TimeInForce     TimeInForce
	ExpireTime      *uint64
	DisplayQuantity *decimal.Quantity
	OrderListID     *ids.OrderListID
	Tags            []string
	TimestampInit   uint64
}

type MarketToLimit struct {
	core            *Core
	traderID        ids.TraderID
	strategyID      ids.StrategyID
	price           *decimal.Price
	expireTime      *uint64
	displayQuantity *decimal.Quantity
	tags            []string
	slippage        *decimal.Decimal
}

func NewMarketToLimit(config MarketToLimitConfig) (*MarketToLimit, error) {
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
		Side: config.Side, Type: OrderTypeMarketToLimit,
		TimeInForce: config.TimeInForce, Quantity: config.Quantity,
		ExpireTime: config.ExpireTime, DisplayQuantity: config.DisplayQuantity,
		OrderListID: config.OrderListID, TimestampInit: config.TimestampInit,
	})
	if err != nil {
		return nil, err
	}
	return &MarketToLimit{
		core: core, traderID: config.TraderID, strategyID: config.StrategyID,
		expireTime:      copyPointerValue(config.ExpireTime),
		displayQuantity: copyPointerValue(config.DisplayQuantity),
		tags:            append([]string(nil), config.Tags...),
	}, nil
}

type MarketToLimitUpdate struct {
	ClientOrderID ids.ClientOrderID
	StrategyID    ids.StrategyID
	Price         *decimal.Price
	Quantity      decimal.Quantity
	Timestamp     uint64
}

func (o *MarketToLimit) Accept(accountID ids.AccountID, venueOrderID ids.VenueOrderID, timestamp uint64) error {
	return o.core.Accept(accountID, venueOrderID, timestamp)
}

func (o *MarketToLimit) ApplyUpdate(event MarketToLimitUpdate) error {
	if event.ClientOrderID != o.ClientOrderID() || event.StrategyID != o.StrategyID() {
		return &Error{Kind: ErrorInvalidOrderEvent}
	}
	if err := o.core.Update(Update{Quantity: &event.Quantity, Timestamp: event.Timestamp}); err != nil {
		return err
	}
	if event.Price != nil {
		o.price = copyPointer(*event.Price)
		o.core.config.Price = copyPointer(*event.Price)
	}
	return nil
}

func (o *MarketToLimit) Fill(event Fill) error {
	if err := o.core.Fill(event); err != nil {
		return err
	}
	if o.price == nil || o.core.AveragePrice() == nil {
		o.slippage = nil
		return nil
	}
	average := *o.core.AveragePrice()
	reference := o.price.Decimal()
	var value decimal.Decimal
	switch {
	case o.Side() == OrderSideBuy && average.Cmp(reference) > 0:
		value = average.Sub(reference)
	case o.Side() == OrderSideSell && average.Cmp(reference) < 0:
		value = reference.Sub(average)
	default:
		o.slippage = nil
		return nil
	}
	o.slippage = copyPointer(value.Normalize())
	return nil
}

func (o *MarketToLimit) TraderID() ids.TraderID           { return o.traderID }
func (o *MarketToLimit) StrategyID() ids.StrategyID       { return o.strategyID }
func (o *MarketToLimit) InstrumentID() ids.InstrumentID   { return o.core.config.InstrumentID }
func (o *MarketToLimit) ClientOrderID() ids.ClientOrderID { return o.core.config.ClientOrderID }
func (o *MarketToLimit) Side() OrderSide                  { return o.core.config.Side }
func (o *MarketToLimit) Quantity() decimal.Quantity       { return o.core.Quantity() }
func (o *MarketToLimit) Price() *decimal.Price            { return copyPointerValue(o.price) }
func (o *MarketToLimit) TimeInForce() TimeInForce         { return o.core.config.TimeInForce }
func (o *MarketToLimit) ExpireTime() *uint64              { return copyPointerValue(o.expireTime) }
func (o *MarketToLimit) IsTriggered() *bool               { return nil }
func (o *MarketToLimit) FilledQuantity() decimal.Quantity { return o.core.FilledQuantity() }
func (o *MarketToLimit) LeavesQuantity() decimal.Quantity { return o.core.LeavesQuantity() }
func (o *MarketToLimit) DisplayQuantity() *decimal.Quantity {
	return copyPointerValue(o.displayQuantity)
}
func (o *MarketToLimit) TriggerInstrumentID() *ids.InstrumentID { return nil }
func (o *MarketToLimit) Slippage() *decimal.Decimal             { return copyDecimal(o.slippage) }

func (o *MarketToLimit) String() string {
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
		"MarketToLimitOrder(%s %s %s MARKET_TO_LIMIT %s, status=%s, client_order_id=%s, venue_order_id=%s, position_id=None, exec_algorithm_id=None, exec_spawn_id=None, tags=%s)",
		o.Side(), o.Quantity().FormattedString(), o.InstrumentID(),
		o.TimeInForce(), o.core.Status(), o.ClientOrderID(), venueOrderID, tags,
	)
}

type MarketToLimitInitialization struct {
	TraderID      ids.TraderID
	StrategyID    ids.StrategyID
	InstrumentID  ids.InstrumentID
	ClientOrderID ids.ClientOrderID
	Side          OrderSide
	Quantity      decimal.Quantity
	TimeInForce   TimeInForce
	ExpireTime    *uint64
	TimestampInit uint64
}

func MarketToLimitFromInitialization(event MarketToLimitInitialization) (*MarketToLimit, error) {
	return NewMarketToLimit(MarketToLimitConfig{
		TraderID: event.TraderID, StrategyID: event.StrategyID,
		InstrumentID: event.InstrumentID, ClientOrderID: event.ClientOrderID,
		Side: event.Side, Quantity: event.Quantity,
		TimeInForce: event.TimeInForce, ExpireTime: event.ExpireTime,
		TimestampInit: event.TimestampInit,
	})
}
