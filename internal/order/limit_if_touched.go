package order

import (
	"fmt"

	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
)

type LimitIfTouchedConfig struct {
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
	DisplayQuantity     *decimal.Quantity
	TriggerInstrumentID *ids.InstrumentID
	OrderListID         *ids.OrderListID
	TimestampInit       uint64
}

type LimitIfTouched struct {
	core                *Core
	traderID            ids.TraderID
	strategyID          ids.StrategyID
	price               decimal.Price
	triggerPrice        decimal.Price
	triggerType         TriggerType
	expireTime          *uint64
	displayQuantity     *decimal.Quantity
	triggerInstrumentID *ids.InstrumentID
	triggered           bool
	slippage            *decimal.Decimal
}

func NewLimitIfTouched(config LimitIfTouchedConfig) (*LimitIfTouched, error) {
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
	comparison := config.TriggerPrice.Cmp(config.Price)
	if config.Side == OrderSideBuy && comparison > 0 {
		return nil, &Error{
			Kind:    ErrorInvariant,
			Message: "BUY Limit-If-Touched must have `trigger_price` <= `price`",
		}
	}
	if config.Side == OrderSideSell && comparison < 0 {
		return nil, &Error{
			Kind:    ErrorInvariant,
			Message: "SELL Limit-If-Touched must have `trigger_price` >= `price`",
		}
	}
	core, err := NewCore(Config{
		ClientOrderID: config.ClientOrderID, InstrumentID: config.InstrumentID,
		Side: config.Side, Type: OrderTypeLimitIfTouched,
		TimeInForce: config.TimeInForce, Quantity: config.Quantity,
		Price: &config.Price, TriggerPrice: &config.TriggerPrice,
		TriggerType: config.TriggerType, ExpireTime: config.ExpireTime,
		DisplayQuantity: config.DisplayQuantity, OrderListID: config.OrderListID,
		TimestampInit: config.TimestampInit,
	})
	if err != nil {
		return nil, err
	}
	return &LimitIfTouched{
		core: core, traderID: config.TraderID, strategyID: config.StrategyID,
		price: config.Price, triggerPrice: config.TriggerPrice,
		triggerType: config.TriggerType, expireTime: copyPointerValue(config.ExpireTime),
		displayQuantity:     copyPointerValue(config.DisplayQuantity),
		triggerInstrumentID: copyPointerValue(config.TriggerInstrumentID),
	}, nil
}

type LimitIfTouchedUpdate struct {
	ClientOrderID ids.ClientOrderID
	StrategyID    ids.StrategyID
	Price         *decimal.Price
	TriggerPrice  *decimal.Price
	Quantity      decimal.Quantity
	Timestamp     uint64
}

func (o *LimitIfTouched) Accept(accountID ids.AccountID, venueOrderID ids.VenueOrderID, timestamp uint64) error {
	return o.core.Accept(accountID, venueOrderID, timestamp)
}

func (o *LimitIfTouched) ApplyUpdate(event LimitIfTouchedUpdate) error {
	if event.ClientOrderID != o.ClientOrderID() || event.StrategyID != o.StrategyID() {
		return &Error{Kind: ErrorInvalidOrderEvent}
	}
	price, triggerPrice := o.price, o.triggerPrice
	if event.Price != nil {
		price = *event.Price
	}
	if event.TriggerPrice != nil {
		triggerPrice = *event.TriggerPrice
	}
	comparison := triggerPrice.Cmp(price)
	if (o.Side() == OrderSideBuy && comparison > 0) ||
		(o.Side() == OrderSideSell && comparison < 0) {
		return &Error{Kind: ErrorInvariant, Message: "updated Limit-If-Touched trigger predicate is invalid"}
	}
	if err := o.core.Update(Update{Quantity: &event.Quantity, Timestamp: event.Timestamp}); err != nil {
		return err
	}
	o.price, o.triggerPrice = price, triggerPrice
	o.core.config.Price = copyPointer(price)
	o.core.config.TriggerPrice = copyPointer(triggerPrice)
	return nil
}

func (o *LimitIfTouched) Fill(event Fill) error {
	if err := o.core.Fill(event); err != nil {
		return err
	}
	if o.core.AveragePrice() == nil {
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

func (o *LimitIfTouched) TraderID() ids.TraderID           { return o.traderID }
func (o *LimitIfTouched) StrategyID() ids.StrategyID       { return o.strategyID }
func (o *LimitIfTouched) InstrumentID() ids.InstrumentID   { return o.core.config.InstrumentID }
func (o *LimitIfTouched) ClientOrderID() ids.ClientOrderID { return o.core.config.ClientOrderID }
func (o *LimitIfTouched) Side() OrderSide                  { return o.core.config.Side }
func (o *LimitIfTouched) Quantity() decimal.Quantity       { return o.core.Quantity() }
func (o *LimitIfTouched) Price() decimal.Price             { return o.price }
func (o *LimitIfTouched) TriggerPrice() decimal.Price      { return o.triggerPrice }
func (o *LimitIfTouched) TriggerType() TriggerType         { return o.triggerType }
func (o *LimitIfTouched) TimeInForce() TimeInForce         { return o.core.config.TimeInForce }
func (o *LimitIfTouched) IsTriggered() bool                { return o.triggered }
func (o *LimitIfTouched) FilledQuantity() decimal.Quantity { return o.core.FilledQuantity() }
func (o *LimitIfTouched) LeavesQuantity() decimal.Quantity { return o.core.LeavesQuantity() }
func (o *LimitIfTouched) DisplayQuantity() *decimal.Quantity {
	return copyPointerValue(o.displayQuantity)
}
func (o *LimitIfTouched) TriggerInstrumentID() *ids.InstrumentID {
	return copyPointerValue(o.triggerInstrumentID)
}
func (o *LimitIfTouched) Slippage() *decimal.Decimal { return copyDecimal(o.slippage) }

func (o *LimitIfTouched) String() string {
	triggerLabel := o.TriggerType().String()
	if o.TriggerType() == TriggerTypeLastPrice {
		triggerLabel = "LastPrice"
	}
	return fmt.Sprintf(
		"LimitIfTouchedOrder(%s %s %s @ %s / trigger %s (%s) %s, status=%s)",
		o.Side(), o.Quantity().FormattedString(), o.InstrumentID(), o.Price(),
		o.TriggerPrice(), triggerLabel, o.TimeInForce(), o.core.Status(),
	)
}

type LimitIfTouchedInitialization struct {
	TraderID      ids.TraderID
	StrategyID    ids.StrategyID
	InstrumentID  ids.InstrumentID
	ClientOrderID ids.ClientOrderID
	Side          OrderSide
	Quantity      decimal.Quantity
	Price         *decimal.Price
	TriggerPrice  *decimal.Price
	TriggerType   *TriggerType
	TimeInForce   TimeInForce
	ExpireTime    *uint64
	TimestampInit uint64
}

func LimitIfTouchedFromInitialization(event LimitIfTouchedInitialization) (*LimitIfTouched, error) {
	if event.Price == nil {
		return nil, &Error{Kind: ErrorInvariant, Message: "`price` is required for `LimitIfTouchedOrder` initialization"}
	}
	if event.TriggerPrice == nil {
		return nil, &Error{Kind: ErrorInvariant, Message: "`trigger_price` is required for `LimitIfTouchedOrder` initialization"}
	}
	if event.TriggerType == nil {
		return nil, &Error{Kind: ErrorInvariant, Message: "`trigger_type` is required for `LimitIfTouchedOrder` initialization"}
	}
	return NewLimitIfTouched(LimitIfTouchedConfig{
		TraderID: event.TraderID, StrategyID: event.StrategyID,
		InstrumentID: event.InstrumentID, ClientOrderID: event.ClientOrderID,
		Side: event.Side, Quantity: event.Quantity, Price: *event.Price,
		TriggerPrice: *event.TriggerPrice, TriggerType: *event.TriggerType,
		TimeInForce: event.TimeInForce, ExpireTime: event.ExpireTime,
		TimestampInit: event.TimestampInit,
	})
}
