package order

import (
	"fmt"
	"strings"

	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
)

type MarketOrderConfig struct {
	TraderID      ids.TraderID
	StrategyID    ids.StrategyID
	InstrumentID  ids.InstrumentID
	ClientOrderID ids.ClientOrderID
	Side          OrderSide
	Quantity      decimal.Quantity
	TimeInForce   TimeInForce
	Tags          []string
	TimestampInit uint64
}

type MarketOrder struct {
	core            *Core
	traderID        ids.TraderID
	strategyID      ids.StrategyID
	protectionPrice *decimal.Price
	tags            []string
}

func NewMarketOrder(config MarketOrderConfig) (*MarketOrder, error) {
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
	if config.TimeInForce == TimeInForceGTD {
		return nil, &Error{Kind: ErrorInvariant, Message: "GTD not supported for Market orders"}
	}
	core, err := NewCore(Config{
		ClientOrderID: config.ClientOrderID, InstrumentID: config.InstrumentID,
		Side: config.Side, Type: OrderTypeMarket, TimeInForce: config.TimeInForce,
		Quantity: config.Quantity, TimestampInit: config.TimestampInit,
	})
	if err != nil {
		return nil, err
	}
	return &MarketOrder{
		core: core, traderID: config.TraderID, strategyID: config.StrategyID,
		tags: append([]string(nil), config.Tags...),
	}, nil
}

type MarketOrderUpdate struct {
	ClientOrderID   ids.ClientOrderID
	StrategyID      ids.StrategyID
	Quantity        *decimal.Quantity
	ProtectionPrice *decimal.Price
	Timestamp       uint64
}

func (o *MarketOrder) Accept(accountID ids.AccountID, venueOrderID ids.VenueOrderID, timestamp uint64) error {
	return o.core.Accept(accountID, venueOrderID, timestamp)
}

func (o *MarketOrder) ApplyUpdate(event MarketOrderUpdate) error {
	if event.ClientOrderID != o.ClientOrderID() || event.StrategyID != o.StrategyID() {
		return &Error{Kind: ErrorInvalidOrderEvent}
	}
	if err := o.core.Update(Update{Quantity: event.Quantity, Timestamp: event.Timestamp}); err != nil {
		return err
	}
	o.protectionPrice = copyPointerValue(event.ProtectionPrice)
	return nil
}

func (o *MarketOrder) TraderID() ids.TraderID           { return o.traderID }
func (o *MarketOrder) StrategyID() ids.StrategyID       { return o.strategyID }
func (o *MarketOrder) InstrumentID() ids.InstrumentID   { return o.core.config.InstrumentID }
func (o *MarketOrder) ClientOrderID() ids.ClientOrderID { return o.core.config.ClientOrderID }
func (o *MarketOrder) Side() OrderSide                  { return o.core.config.Side }
func (o *MarketOrder) OrderType() OrderType             { return OrderTypeMarket }
func (o *MarketOrder) Quantity() decimal.Quantity       { return o.core.Quantity() }
func (o *MarketOrder) Price() *decimal.Price            { return copyPointerValue(o.protectionPrice) }
func (o *MarketOrder) HasPrice() bool                   { return o.protectionPrice != nil }
func (o *MarketOrder) TimeInForce() TimeInForce         { return o.core.config.TimeInForce }

func (o *MarketOrder) String() string {
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
		"MarketOrder(%s %s %s @ MARKET %s, status=%s, client_order_id=%s, venue_order_id=%s, position_id=None, exec_algorithm_id=None, exec_spawn_id=None, tags=%s)",
		o.Side(), o.Quantity().FormattedString(), o.InstrumentID(), o.TimeInForce(),
		o.core.Status(), o.ClientOrderID(), venueOrderID, tags,
	)
}

type MarketOrderInitialization struct {
	TraderID      ids.TraderID
	StrategyID    ids.StrategyID
	InstrumentID  ids.InstrumentID
	ClientOrderID ids.ClientOrderID
	Side          OrderSide
	Quantity      decimal.Quantity
	TimeInForce   TimeInForce
	TimestampInit uint64
}

func MarketOrderFromInitialization(event MarketOrderInitialization) (*MarketOrder, error) {
	return NewMarketOrder(MarketOrderConfig{
		TraderID: event.TraderID, StrategyID: event.StrategyID,
		InstrumentID: event.InstrumentID, ClientOrderID: event.ClientOrderID,
		Side: event.Side, Quantity: event.Quantity,
		TimeInForce: event.TimeInForce, TimestampInit: event.TimestampInit,
	})
}
