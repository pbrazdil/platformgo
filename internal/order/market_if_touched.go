package order

import (
	"fmt"

	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
)

type MarketIfTouchedConfig struct {
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
	TimestampInit       uint64
}

type MarketIfTouched struct {
	core                *Core
	traderID            ids.TraderID
	strategyID          ids.StrategyID
	triggerPrice        decimal.Price
	triggerType         TriggerType
	expireTime          *uint64
	displayQuantity     *decimal.Quantity
	triggerInstrumentID *ids.InstrumentID
	triggered           bool
	slippage            *decimal.Decimal
}

func NewMarketIfTouched(config MarketIfTouchedConfig) (*MarketIfTouched, error) {
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
		Side: config.Side, Type: OrderTypeMarketIfTouched,
		TimeInForce: config.TimeInForce, Quantity: config.Quantity,
		TriggerPrice: &config.TriggerPrice, TriggerType: config.TriggerType,
		ExpireTime: config.ExpireTime, DisplayQuantity: config.DisplayQuantity,
		OrderListID: config.OrderListID, TimestampInit: config.TimestampInit,
	})
	if err != nil {
		return nil, err
	}
	return &MarketIfTouched{
		core: core, traderID: config.TraderID, strategyID: config.StrategyID,
		triggerPrice: config.TriggerPrice, triggerType: config.TriggerType,
		expireTime:          copyPointerValue(config.ExpireTime),
		displayQuantity:     copyPointerValue(config.DisplayQuantity),
		triggerInstrumentID: copyPointerValue(config.TriggerInstrumentID),
	}, nil
}

type MarketIfTouchedUpdate struct {
	ClientOrderID ids.ClientOrderID
	StrategyID    ids.StrategyID
	TriggerPrice  *decimal.Price
	Quantity      decimal.Quantity
	Timestamp     uint64
}

func (o *MarketIfTouched) Accept(accountID ids.AccountID, venueOrderID ids.VenueOrderID, timestamp uint64) error {
	return o.core.Accept(accountID, venueOrderID, timestamp)
}

func (o *MarketIfTouched) ApplyUpdate(event MarketIfTouchedUpdate) error {
	if event.ClientOrderID != o.ClientOrderID() || event.StrategyID != o.StrategyID() {
		return &Error{Kind: ErrorInvalidOrderEvent}
	}
	if err := o.core.Update(Update{Quantity: &event.Quantity, Timestamp: event.Timestamp}); err != nil {
		return err
	}
	if event.TriggerPrice != nil {
		o.triggerPrice = *event.TriggerPrice
		o.core.config.TriggerPrice = copyPointer(*event.TriggerPrice)
	}
	return nil
}

func (o *MarketIfTouched) Fill(event Fill) error {
	if err := o.core.Fill(event); err != nil {
		return err
	}
	if o.core.AveragePrice() == nil {
		o.slippage = nil
		return nil
	}
	average := *o.core.AveragePrice()
	reference := o.triggerPrice.Decimal()
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

func (o *MarketIfTouched) TraderID() ids.TraderID           { return o.traderID }
func (o *MarketIfTouched) StrategyID() ids.StrategyID       { return o.strategyID }
func (o *MarketIfTouched) InstrumentID() ids.InstrumentID   { return o.core.config.InstrumentID }
func (o *MarketIfTouched) ClientOrderID() ids.ClientOrderID { return o.core.config.ClientOrderID }
func (o *MarketIfTouched) Side() OrderSide                  { return o.core.config.Side }
func (o *MarketIfTouched) Quantity() decimal.Quantity       { return o.core.Quantity() }
func (o *MarketIfTouched) Price() *decimal.Price            { return nil }
func (o *MarketIfTouched) TriggerPrice() decimal.Price      { return o.triggerPrice }
func (o *MarketIfTouched) TriggerType() TriggerType         { return o.triggerType }
func (o *MarketIfTouched) TimeInForce() TimeInForce         { return o.core.config.TimeInForce }
func (o *MarketIfTouched) ExpireTime() *uint64              { return copyPointerValue(o.expireTime) }
func (o *MarketIfTouched) IsTriggered() bool                { return o.triggered }
func (o *MarketIfTouched) FilledQuantity() decimal.Quantity { return o.core.FilledQuantity() }
func (o *MarketIfTouched) LeavesQuantity() decimal.Quantity { return o.core.LeavesQuantity() }
func (o *MarketIfTouched) DisplayQuantity() *decimal.Quantity {
	return copyPointerValue(o.displayQuantity)
}
func (o *MarketIfTouched) TriggerInstrumentID() *ids.InstrumentID {
	return copyPointerValue(o.triggerInstrumentID)
}
func (o *MarketIfTouched) OrderListID() *ids.OrderListID {
	return copyPointerValue(o.core.config.OrderListID)
}
func (o *MarketIfTouched) Slippage() *decimal.Decimal { return copyDecimal(o.slippage) }

func (o *MarketIfTouched) String() string {
	return fmt.Sprintf(
		"MarketIfTouchedOrder { side: %s, qty: %s, instrument: %s, tif: %s, trigger_price: %s, trigger_type: %s, status: %s }",
		o.Side(), o.Quantity().FormattedString(), o.InstrumentID(), o.TimeInForce(),
		o.TriggerPrice(), o.TriggerType(), o.core.Status(),
	)
}

type MarketIfTouchedInitialization struct {
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
	OrderListID         *ids.OrderListID
	TimestampInit       uint64
}

func MarketIfTouchedFromInitialization(event MarketIfTouchedInitialization) (*MarketIfTouched, error) {
	if event.TriggerPrice == nil {
		return nil, &Error{Kind: ErrorInvariant, Message: "`trigger_price` is required for `MarketIfTouchedOrder` initialization"}
	}
	if event.TriggerType == nil {
		return nil, &Error{Kind: ErrorInvariant, Message: "`trigger_type` is required for `MarketIfTouchedOrder` initialization"}
	}
	return NewMarketIfTouched(MarketIfTouchedConfig{
		TraderID: event.TraderID, StrategyID: event.StrategyID,
		InstrumentID: event.InstrumentID, ClientOrderID: event.ClientOrderID,
		Side: event.Side, Quantity: event.Quantity,
		TriggerPrice: *event.TriggerPrice, TriggerType: *event.TriggerType,
		TimeInForce: event.TimeInForce, ExpireTime: event.ExpireTime,
		DisplayQuantity:     event.DisplayQuantity,
		TriggerInstrumentID: event.TriggerInstrumentID,
		OrderListID:         event.OrderListID,
		TimestampInit:       event.TimestampInit,
	})
}
