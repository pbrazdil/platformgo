package order

import (
	"fmt"
	"strings"

	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
)

type LimitConfig struct {
	TraderID        ids.TraderID
	StrategyID      ids.StrategyID
	InstrumentID    ids.InstrumentID
	ClientOrderID   ids.ClientOrderID
	Side            OrderSide
	Quantity        decimal.Quantity
	Price           decimal.Price
	TimeInForce     TimeInForce
	ExpireTime      *uint64
	PostOnly        bool
	DisplayQuantity *decimal.Quantity
	OrderListID     *ids.OrderListID
	Tags            []string
	TimestampInit   uint64
}

type Limit struct {
	core            *Core
	traderID        ids.TraderID
	strategyID      ids.StrategyID
	price           decimal.Price
	expireTime      *uint64
	postOnly        bool
	displayQuantity *decimal.Quantity
	tags            []string
}

func NewLimit(config LimitConfig) (*Limit, error) {
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
		Side: config.Side, Type: OrderTypeLimit, TimeInForce: config.TimeInForce,
		Quantity: config.Quantity, Price: &config.Price, ExpireTime: config.ExpireTime,
		DisplayQuantity: config.DisplayQuantity, PostOnly: config.PostOnly,
		OrderListID: config.OrderListID, TimestampInit: config.TimestampInit,
	})
	if err != nil {
		return nil, err
	}
	return &Limit{
		core: core, traderID: config.TraderID, strategyID: config.StrategyID,
		price: config.Price, expireTime: copyPointerValue(config.ExpireTime),
		postOnly:        config.PostOnly,
		displayQuantity: copyPointerValue(config.DisplayQuantity),
		tags:            append([]string(nil), config.Tags...),
	}, nil
}

type LimitUpdate struct {
	ClientOrderID ids.ClientOrderID
	StrategyID    ids.StrategyID
	Price         *decimal.Price
	Quantity      decimal.Quantity
	Timestamp     uint64
}

func (o *Limit) Accept(accountID ids.AccountID, venueOrderID ids.VenueOrderID, timestamp uint64) error {
	return o.core.Accept(accountID, venueOrderID, timestamp)
}

func (o *Limit) ApplyUpdate(event LimitUpdate) error {
	if event.ClientOrderID != o.ClientOrderID() || event.StrategyID != o.StrategyID() {
		return &Error{Kind: ErrorInvalidOrderEvent}
	}
	if err := o.core.Update(Update{Quantity: &event.Quantity, Timestamp: event.Timestamp}); err != nil {
		return err
	}
	if event.Price != nil {
		o.price = *event.Price
		o.core.config.Price = copyPointer(*event.Price)
	}
	return nil
}

func (o *Limit) TraderID() ids.TraderID             { return o.traderID }
func (o *Limit) StrategyID() ids.StrategyID         { return o.strategyID }
func (o *Limit) InstrumentID() ids.InstrumentID     { return o.core.config.InstrumentID }
func (o *Limit) ClientOrderID() ids.ClientOrderID   { return o.core.config.ClientOrderID }
func (o *Limit) Side() OrderSide                    { return o.core.config.Side }
func (o *Limit) OrderType() OrderType               { return OrderTypeLimit }
func (o *Limit) Quantity() decimal.Quantity         { return o.core.Quantity() }
func (o *Limit) Price() decimal.Price               { return o.price }
func (o *Limit) TimeInForce() TimeInForce           { return o.core.config.TimeInForce }
func (o *Limit) ExpireTime() *uint64                { return copyPointerValue(o.expireTime) }
func (o *Limit) IsPostOnly() bool                   { return o.postOnly }
func (o *Limit) FilledQuantity() decimal.Quantity   { return o.core.FilledQuantity() }
func (o *Limit) LeavesQuantity() decimal.Quantity   { return o.core.LeavesQuantity() }
func (o *Limit) DisplayQuantity() *decimal.Quantity { return copyPointerValue(o.displayQuantity) }
func (o *Limit) TriggerInstrumentID() *ids.InstrumentID {
	return nil
}

func (o *Limit) String() string {
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
		"LimitOrder(%s %s %s LIMIT @ %s %s, status=%s, client_order_id=%s, venue_order_id=%s, position_id=None, exec_algorithm_id=None, exec_spawn_id=None, tags=%s)",
		o.Side(), o.Quantity().FormattedString(), o.InstrumentID(), o.Price(),
		o.TimeInForce(), o.core.Status(), o.ClientOrderID(), venueOrderID, tags,
	)
}
