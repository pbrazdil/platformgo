package order

import (
	"fmt"

	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
)

type ReplayErrorKind string

const (
	ReplayErrorEmptyInput            ReplayErrorKind = "empty_input"
	ReplayErrorWrongFirstEvent       ReplayErrorKind = "wrong_first_event"
	ReplayErrorInvalidInitialization ReplayErrorKind = "invalid_initialization"
	ReplayErrorApplyFailed           ReplayErrorKind = "apply_failed"
)

type ReplayError struct {
	Kind   ReplayErrorKind
	Source error
}

func (e *ReplayError) Error() string {
	switch e.Kind {
	case ReplayErrorEmptyInput:
		return "No order events provided to create OrderAny"
	case ReplayErrorWrongFirstEvent:
		return "First event must be `OrderInitialized`"
	case ReplayErrorInvalidInitialization:
		return "Invalid `OrderInitialized` event: " + e.Source.Error()
	case ReplayErrorApplyFailed:
		if orderError, ok := e.Source.(*Error); ok && orderError.Kind == ErrorInvalidStateTransition {
			return "Invalid order state transition"
		}
		return e.Source.Error()
	default:
		return string(e.Kind)
	}
}

func (e *ReplayError) Unwrap() error { return e.Source }

type Initialization struct {
	ClientOrderID      ids.ClientOrderID
	InstrumentID       ids.InstrumentID
	Side               OrderSide
	Type               OrderType
	Quantity           decimal.Quantity
	Price              *decimal.Price
	TriggerPrice       *decimal.Price
	TriggerType        *TriggerType
	ActivationPrice    *decimal.Price
	LimitOffset        *decimal.Decimal
	TrailingOffset     *decimal.Decimal
	TrailingOffsetType *TrailingOffsetType
	ProtectionPrice    *decimal.Price
}

type AnyEventKind uint8

const (
	AnyEventInitialized AnyEventKind = iota + 1
	AnyEventUpdated
)

type AnyEvent struct {
	Kind           AnyEventKind
	Initialization Initialization
	Update         Update
}

func InitializationEvent(initialization Initialization) AnyEvent {
	return AnyEvent{Kind: AnyEventInitialized, Initialization: initialization}
}

func UpdateEvent(update Update) AnyEvent {
	return AnyEvent{Kind: AnyEventUpdated, Update: update}
}

type Any struct {
	config          Config
	protectionPrice *decimal.Price
}

func NewAny(config Config) (*Any, error) {
	initialization := Initialization{
		ClientOrderID:   config.ClientOrderID,
		InstrumentID:    config.InstrumentID,
		Side:            config.Side,
		Type:            config.Type,
		Quantity:        config.Quantity,
		Price:           config.Price,
		TriggerPrice:    config.TriggerPrice,
		ActivationPrice: config.ActivationPrice,
		LimitOffset:     config.LimitOffset,
		TrailingOffset:  config.TrailingOffset,
	}
	if config.TriggerType != TriggerTypeNoTrigger {
		initialization.TriggerType = copyPointer(config.TriggerType)
	} else {
		switch config.Type {
		case OrderTypeLimitIfTouched, OrderTypeMarketIfTouched, OrderTypeStopLimit,
			OrderTypeStopMarket, OrderTypeTrailingStopLimit, OrderTypeTrailingStopMarket:
			initialization.TriggerType = copyPointer(TriggerTypeLastPrice)
		}
	}
	if config.TrailingOffset != nil {
		initialization.TrailingOffsetType = copyPointer(config.TrailingOffsetType)
	}
	return newAny(initialization)
}

func FromEvents(events []AnyEvent) (*Any, error) {
	if len(events) == 0 {
		return nil, &ReplayError{Kind: ReplayErrorEmptyInput}
	}
	if events[0].Kind != AnyEventInitialized {
		return nil, &ReplayError{Kind: ReplayErrorWrongFirstEvent}
	}
	order, err := newAny(events[0].Initialization)
	if err != nil {
		return nil, &ReplayError{Kind: ReplayErrorInvalidInitialization, Source: err}
	}
	for _, event := range events[1:] {
		switch event.Kind {
		case AnyEventUpdated:
			if event.Update.Quantity != nil {
				order.config.Quantity = *event.Update.Quantity
			}
		default:
			return nil, &ReplayError{Kind: ReplayErrorApplyFailed, Source: stateError()}
		}
	}
	return order, nil
}

func newAny(initialization Initialization) (*Any, error) {
	if initialization.ClientOrderID == "" {
		initialization.ClientOrderID = ids.MustClientOrderID("O-19700101-000000-001-001-1")
	}
	if initialization.InstrumentID.String() == "." {
		initialization.InstrumentID = ids.MustInstrumentID("AUDUSD.SIM")
	}
	if initialization.Side == OrderSideNoOrderSide {
		initialization.Side = OrderSideBuy
	}
	if initialization.Type == 0 {
		initialization.Type = OrderTypeMarket
	}
	if initialization.Quantity.IsZero() {
		initialization.Quantity = decimal.MustQuantity("100000")
	}
	if err := validateInitialization(initialization); err != nil {
		return nil, err
	}
	config := Config{
		ClientOrderID:   initialization.ClientOrderID,
		InstrumentID:    initialization.InstrumentID,
		Side:            initialization.Side,
		Type:            initialization.Type,
		Quantity:        initialization.Quantity,
		Price:           initialization.Price,
		TriggerPrice:    initialization.TriggerPrice,
		ActivationPrice: initialization.ActivationPrice,
		LimitOffset:     initialization.LimitOffset,
		TrailingOffset:  initialization.TrailingOffset,
	}
	if initialization.TriggerType != nil {
		config.TriggerType = *initialization.TriggerType
	}
	if initialization.TrailingOffsetType != nil {
		config.TrailingOffsetType = *initialization.TrailingOffsetType
	}
	return &Any{config: config, protectionPrice: initialization.ProtectionPrice}, nil
}

func validateInitialization(initialization Initialization) error {
	required := func(present bool, field, orderName string) error {
		if !present {
			return fmt.Errorf("`%s` is required for `%s` initialization", field, orderName)
		}
		return nil
	}
	switch initialization.Type {
	case OrderTypeLimit:
		return required(initialization.Price != nil, "price", "LimitOrder")
	case OrderTypeLimitIfTouched:
		if err := required(initialization.Price != nil, "price", "LimitIfTouchedOrder"); err != nil {
			return err
		}
		if err := required(initialization.TriggerPrice != nil, "trigger_price", "LimitIfTouchedOrder"); err != nil {
			return err
		}
		if err := required(initialization.TriggerType != nil, "trigger_type", "LimitIfTouchedOrder"); err != nil {
			return err
		}
		comparison := initialization.TriggerPrice.Cmp(*initialization.Price)
		if initialization.Side == OrderSideBuy && comparison > 0 {
			return fmt.Errorf("invalid BUY Limit-If-Touched trigger predicate")
		}
		if initialization.Side == OrderSideSell && comparison < 0 {
			return fmt.Errorf("invalid SELL Limit-If-Touched trigger predicate")
		}
	case OrderTypeStopLimit:
		if err := required(initialization.Price != nil, "price", "StopLimitOrder"); err != nil {
			return err
		}
		if err := required(initialization.TriggerPrice != nil, "trigger_price", "StopLimitOrder"); err != nil {
			return err
		}
		return required(initialization.TriggerType != nil, "trigger_type", "StopLimitOrder")
	case OrderTypeStopMarket:
		if err := required(initialization.TriggerPrice != nil, "trigger_price", "StopMarketOrder"); err != nil {
			return err
		}
		return required(initialization.TriggerType != nil, "trigger_type", "StopMarketOrder")
	case OrderTypeMarketIfTouched:
		if err := required(initialization.TriggerPrice != nil, "trigger_price", "MarketIfTouchedOrder"); err != nil {
			return err
		}
		return required(initialization.TriggerType != nil, "trigger_type", "MarketIfTouchedOrder")
	case OrderTypeTrailingStopLimit:
		if err := required(initialization.TriggerType != nil, "trigger_type", "TrailingStopLimitOrder"); err != nil {
			return err
		}
		if err := required(initialization.LimitOffset != nil, "limit_offset", "TrailingStopLimitOrder"); err != nil {
			return err
		}
		if err := required(initialization.TrailingOffset != nil, "trailing_offset", "TrailingStopLimitOrder"); err != nil {
			return err
		}
		return required(initialization.TrailingOffsetType != nil, "trailing_offset_type", "TrailingStopLimitOrder")
	case OrderTypeTrailingStopMarket:
		if err := required(initialization.TriggerType != nil, "trigger_type", "TrailingStopMarketOrder"); err != nil {
			return err
		}
		if err := required(initialization.TrailingOffset != nil, "trailing_offset", "TrailingStopMarketOrder"); err != nil {
			return err
		}
		return required(initialization.TrailingOffsetType != nil, "trailing_offset_type", "TrailingStopMarketOrder")
	}
	return nil
}

func (o *Any) Equal(other *Any) bool {
	return o != nil && other != nil && o.config.ClientOrderID == other.config.ClientOrderID
}

func (o *Any) ClientOrderID() ids.ClientOrderID { return o.config.ClientOrderID }
func (o *Any) InstrumentID() ids.InstrumentID   { return o.config.InstrumentID }
func (o *Any) OrderType() OrderType             { return o.config.Type }
func (o *Any) Quantity() decimal.Quantity       { return o.config.Quantity }
func (o *Any) Price() *decimal.Price            { return copyPointerValue(o.config.Price) }
func (o *Any) TriggerPrice() *decimal.Price     { return copyPointerValue(o.config.TriggerPrice) }
func (o *Any) TrailingOffset() *decimal.Decimal { return copyDecimal(o.config.TrailingOffset) }
func (o *Any) TrailingOffsetType() *TrailingOffsetType {
	if o.config.TrailingOffset == nil {
		return nil
	}
	return copyPointer(o.config.TrailingOffsetType)
}

type PassiveAny struct {
	order *Any
}

func ToPassiveAny(order *Any) (*PassiveAny, error) {
	switch order.OrderType() {
	case OrderTypeLimit, OrderTypeLimitIfTouched, OrderTypeMarket,
		OrderTypeMarketIfTouched, OrderTypeMarketToLimit, OrderTypeStopLimit,
		OrderTypeStopMarket, OrderTypeTrailingStopLimit, OrderTypeTrailingStopMarket:
		return &PassiveAny{order: order}, nil
	default:
		return nil, fmt.Errorf("cannot convert %v order to PassiveOrderAny", order.OrderType())
	}
}

func (o *PassiveAny) ToAny() *Any { return o.order.clone() }

type StopAny struct {
	order *Any
}

func ToStopAny(order *Any) (*StopAny, error) {
	switch order.OrderType() {
	case OrderTypeLimitIfTouched, OrderTypeMarketIfTouched, OrderTypeStopLimit,
		OrderTypeStopMarket, OrderTypeTrailingStopLimit, OrderTypeTrailingStopMarket:
		return &StopAny{order: order}, nil
	default:
		return nil, fmt.Errorf("cannot convert %v order to StopOrderAny", order.OrderType())
	}
}

func (o *StopAny) ToAny() *Any { return o.order.clone() }
func (o *StopAny) StopPrice() *decimal.Price {
	if o.order.config.ActivationPrice != nil {
		return copyPointerValue(o.order.config.ActivationPrice)
	}
	return o.order.TriggerPrice()
}

type LimitAny struct {
	order *Any
}

func ToLimitAny(order *Any) (*LimitAny, error) {
	switch order.OrderType() {
	case OrderTypeLimit, OrderTypeMarketToLimit, OrderTypeStopLimit, OrderTypeTrailingStopLimit:
		return &LimitAny{order: order}, nil
	case OrderTypeMarket:
		if order.protectionPrice != nil {
			return &LimitAny{order: order}, nil
		}
	}
	return nil, fmt.Errorf("cannot convert %v order to LimitOrderAny", order.OrderType())
}

func (o *LimitAny) ToAny() *Any { return o.order.clone() }
func (o *LimitAny) LimitPrice() decimal.Price {
	if o.order.config.Price != nil {
		return *o.order.config.Price
	}
	if o.order.protectionPrice != nil {
		return *o.order.protectionPrice
	}
	panic("No price for order")
}

func (o *Any) clone() *Any {
	copied := *o
	copied.config.Price = copyPointerValue(o.config.Price)
	copied.config.TriggerPrice = copyPointerValue(o.config.TriggerPrice)
	copied.config.ActivationPrice = copyPointerValue(o.config.ActivationPrice)
	copied.config.LimitOffset = copyDecimal(o.config.LimitOffset)
	copied.config.TrailingOffset = copyDecimal(o.config.TrailingOffset)
	copied.protectionPrice = copyPointerValue(o.protectionPrice)
	return &copied
}
