package engine

import (
	"encoding/json"
	"fmt"
)

// TradingActionKind identifies one deterministic trading state transition.
type TradingActionKind string

const (
	TradingActionConfigureInstrument TradingActionKind = "configure_instrument"
	TradingActionUpdateBook          TradingActionKind = "update_book"
	TradingActionSubmitOrder         TradingActionKind = "submit_order"
	TradingActionAmendOrder          TradingActionKind = "amend_order"
	TradingActionCancelOrder         TradingActionKind = "cancel_order"
)

// Side is an explicit order direction. Its zero value is invalid.
type Side string

const (
	SideBuy  Side = "BUY"
	SideSell Side = "SELL"
)

func (side Side) valid() bool {
	return side == SideBuy || side == SideSell
}

// OrderType is an explicit execution instruction. Its zero value is invalid.
type OrderType string

const (
	OrderTypeMarket     OrderType = "MARKET"
	OrderTypeLimit      OrderType = "LIMIT"
	OrderTypeStopMarket OrderType = "STOP_MARKET"
	OrderTypeStopLimit  OrderType = "STOP_LIMIT"
)

func (orderType OrderType) valid() bool {
	switch orderType {
	case OrderTypeMarket, OrderTypeLimit, OrderTypeStopMarket, OrderTypeStopLimit:
		return true
	default:
		return false
	}
}

// TimeInForce controls whether unfilled quantity may remain working.
type TimeInForce string

const (
	TimeInForceGTC TimeInForce = "GTC"
	TimeInForceIOC TimeInForce = "IOC"
	TimeInForceFOK TimeInForce = "FOK"
)

func (timeInForce TimeInForce) valid() bool {
	switch timeInForce {
	case TimeInForceGTC, TimeInForceIOC, TimeInForceFOK:
		return true
	default:
		return false
	}
}

// OrderStatus is the deterministic lifecycle state of an order.
type OrderStatus string

const (
	OrderStatusWorking         OrderStatus = "working"
	OrderStatusPartiallyFilled OrderStatus = "partially_filled"
	OrderStatusFilled          OrderStatus = "filled"
	OrderStatusCancelled       OrderStatus = "cancelled"
	OrderStatusRejected        OrderStatus = "rejected"
)

// CommandStatus is the terminal processing result for an engine input.
type CommandStatus string

const (
	CommandStatusAccepted CommandStatus = "accepted"
	CommandStatusRejected CommandStatus = "rejected"
)

// RejectionReason is a stable business-rejection code.
type RejectionReason string

const (
	RejectionInvalidAction      RejectionReason = "invalid_action"
	RejectionInvalidInstrument  RejectionReason = "invalid_instrument"
	RejectionInvalidOrder       RejectionReason = "invalid_order"
	RejectionOrderNotFound      RejectionReason = "order_not_found"
	RejectionOrderOwnership     RejectionReason = "order_ownership"
	RejectionOrderTerminal      RejectionReason = "order_terminal"
	RejectionInsufficientMarket RejectionReason = "insufficient_market"
	RejectionDuplicateOrderID   RejectionReason = "duplicate_order_id"
)

// CommandResult records whether a valid envelope produced or rejected a
// business transition. Rejections are committed decisions, not engine faults.
type CommandResult struct {
	Status CommandStatus
	Reason RejectionReason
}

// TradingAction is a closed tagged union. Exactly the member named by Kind
// must be present.
type TradingAction struct {
	Kind                TradingActionKind    `json:"kind"`
	ConfigureInstrument *ConfigureInstrument `json:"configureInstrument,omitempty"`
	UpdateBook          *UpdateBook          `json:"updateBook,omitempty"`
	SubmitOrder         *SubmitOrder         `json:"submitOrder,omitempty"`
	AmendOrder          *AmendOrder          `json:"amendOrder,omitempty"`
	CancelOrder         *CancelOrder         `json:"cancelOrder,omitempty"`
}

// ConfigureInstrument installs one immutable instrument revision.
type ConfigureInstrument struct {
	InstrumentID  string `json:"instrumentId"`
	Revision      uint64 `json:"revision"`
	PriceScale    uint8  `json:"priceScale"`
	QuantityScale uint8  `json:"quantityScale"`
}

// BookLevel is an exact price and available quantity.
type BookLevel struct {
	Price    string `json:"price"`
	Quantity string `json:"quantity"`
}

// UpdateBook atomically replaces the deterministic L2 snapshot.
type UpdateBook struct {
	InstrumentID string      `json:"instrumentId"`
	Bids         []BookLevel `json:"bids"`
	Asks         []BookLevel `json:"asks"`
}

// SubmitOrder creates one order with a caller-supplied stable identity.
type SubmitOrder struct {
	OrderID      ID          `json:"orderId"`
	AccountID    string      `json:"accountId"`
	InstrumentID string      `json:"instrumentId"`
	Side         Side        `json:"side"`
	Type         OrderType   `json:"type"`
	TimeInForce  TimeInForce `json:"timeInForce"`
	Quantity     string      `json:"quantity"`
	Price        string      `json:"price,omitempty"`
	TriggerPrice string      `json:"triggerPrice,omitempty"`
	ReduceOnly   bool        `json:"reduceOnly"`
}

// AmendOrder changes the exact price and quantity of a working order.
type AmendOrder struct {
	AccountID string `json:"accountId"`
	OrderID   ID     `json:"orderId"`
	Quantity  string `json:"quantity"`
	Price     string `json:"price"`
}

// CancelOrder cancels one working order owned by the account.
type CancelOrder struct {
	AccountID string `json:"accountId"`
	OrderID   ID     `json:"orderId"`
}

// EncodeTradingAction returns the canonical bytes bound into InputEnvelope.
func EncodeTradingAction(action TradingAction) ([]byte, error) {
	payload, err := json.Marshal(action)
	if err != nil {
		return nil, fmt.Errorf("encode trading action: %w", err)
	}
	return payload, nil
}

// InstrumentSnapshot is the externally inspectable configured revision.
type InstrumentSnapshot struct {
	InstrumentID  string
	Revision      uint64
	PriceScale    uint8
	QuantityScale uint8
}

// BookSnapshot is the canonical price-sorted market state.
type BookSnapshot struct {
	InstrumentID string
	Bids         []BookLevel
	Asks         []BookLevel
}

// OrderSnapshot is the immutable query representation of one order version.
type OrderSnapshot struct {
	OrderID          ID
	AccountID        string
	InstrumentID     string
	Side             Side
	Type             OrderType
	TimeInForce      TimeInForce
	Status           OrderStatus
	Quantity         string
	FilledQuantity   string
	AverageFillPrice string
	Price            string
	TriggerPrice     string
	ReduceOnly       bool
	Version          uint64
}

// FillSnapshot is one exact, stable execution fact.
type FillSnapshot struct {
	FillID       ID
	OrderID      ID
	AccountID    string
	InstrumentID string
	Side         Side
	Price        string
	Quantity     string
	LogicalTime  LogicalTime
}

// DomainEvent records one canonical aggregate transition.
type DomainEvent struct {
	EventID          ID
	Kind             string
	AggregateID      ID
	AggregateVersion uint64
	LogicalTime      LogicalTime
}
