package position

import (
	"github.com/upcomers-org/platformgo/internal/currency"
	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
	"github.com/upcomers-org/platformgo/internal/money"
)

// EventID is the stable UUID representation attached to a position event.
type EventID string

// PositionOpened records the first fill in a position cycle.
type PositionOpened struct {
	TraderID       ids.TraderID
	StrategyID     ids.StrategyID
	InstrumentID   ids.InstrumentID
	PositionID     ids.PositionID
	AccountID      ids.AccountID
	OpeningOrderID ids.ClientOrderID
	Entry          OrderSide
	Side           Side
	SignedQuantity decimal.Decimal
	Quantity       decimal.Quantity
	LastQuantity   decimal.Quantity
	LastPrice      decimal.Price
	Currency       currency.Currency
	AverageOpen    decimal.Decimal
	EventID        EventID
	TsEvent        uint64
	TsInit         uint64
}

// PositionChanged records a fill which changes an open position.
type PositionChanged struct {
	TraderID       ids.TraderID
	StrategyID     ids.StrategyID
	InstrumentID   ids.InstrumentID
	PositionID     ids.PositionID
	AccountID      ids.AccountID
	OpeningOrderID ids.ClientOrderID
	Entry          OrderSide
	Side           Side
	SignedQuantity decimal.Decimal
	Quantity       decimal.Quantity
	PeakQuantity   decimal.Quantity
	LastQuantity   decimal.Quantity
	LastPrice      decimal.Price
	Currency       currency.Currency
	AverageOpen    decimal.Decimal
	AverageClose   *decimal.Decimal
	RealizedReturn decimal.Decimal
	RealizedPnL    *money.Money
	UnrealizedPnL  money.Money
	EventID        EventID
	TsOpened       uint64
	TsEvent        uint64
	TsInit         uint64
}

// PositionClosed records the fill which closes a position cycle.
type PositionClosed struct {
	TraderID       ids.TraderID
	StrategyID     ids.StrategyID
	InstrumentID   ids.InstrumentID
	PositionID     ids.PositionID
	AccountID      ids.AccountID
	OpeningOrderID ids.ClientOrderID
	ClosingOrderID *ids.ClientOrderID
	Entry          OrderSide
	Side           Side
	SignedQuantity decimal.Decimal
	Quantity       decimal.Quantity
	PeakQuantity   decimal.Quantity
	LastQuantity   decimal.Quantity
	LastPrice      decimal.Price
	Currency       currency.Currency
	AverageOpen    decimal.Decimal
	AverageClose   *decimal.Decimal
	RealizedReturn decimal.Decimal
	RealizedPnL    *money.Money
	UnrealizedPnL  money.Money
	Duration       uint64
	EventID        EventID
	TsOpened       uint64
	TsClosed       *uint64
	TsEvent        uint64
	TsInit         uint64
}

// PositionAdjusted records a quantity or realized-PnL change outside a fill.
type PositionAdjusted struct {
	TraderID       ids.TraderID
	StrategyID     ids.StrategyID
	InstrumentID   ids.InstrumentID
	PositionID     ids.PositionID
	AccountID      ids.AccountID
	AdjustmentType AdjustmentType
	QuantityChange *decimal.Decimal
	PnLChange      *money.Money
	Reason         *string
	EventID        EventID
	TsEvent        uint64
	TsInit         uint64
}

type PositionEventKind uint8

const (
	PositionEventOpened PositionEventKind = iota + 1
	PositionEventChanged
	PositionEventClosed
	PositionEventAdjusted
)

// PositionEvent is a closed tagged union of position lifecycle events.
type PositionEvent struct {
	kind     PositionEventKind
	opened   *PositionOpened
	changed  *PositionChanged
	closed   *PositionClosed
	adjusted *PositionAdjusted
}

func NewPositionOpenedEvent(value PositionOpened) PositionEvent {
	return PositionEvent{kind: PositionEventOpened, opened: &value}
}

func NewPositionChangedEvent(value PositionChanged) PositionEvent {
	return PositionEvent{kind: PositionEventChanged, changed: &value}
}

func NewPositionClosedEvent(value PositionClosed) PositionEvent {
	return PositionEvent{kind: PositionEventClosed, closed: &value}
}

func NewPositionAdjustedEvent(value PositionAdjusted) PositionEvent {
	return PositionEvent{kind: PositionEventAdjusted, adjusted: &value}
}

func (event PositionEvent) Kind() PositionEventKind { return event.kind }

func (event PositionEvent) InstrumentID() ids.InstrumentID {
	switch event.kind {
	case PositionEventOpened:
		return event.opened.InstrumentID
	case PositionEventChanged:
		return event.changed.InstrumentID
	case PositionEventClosed:
		return event.closed.InstrumentID
	case PositionEventAdjusted:
		return event.adjusted.InstrumentID
	default:
		return ids.InstrumentID{}
	}
}

func (event PositionEvent) AccountID() ids.AccountID {
	switch event.kind {
	case PositionEventOpened:
		return event.opened.AccountID
	case PositionEventChanged:
		return event.changed.AccountID
	case PositionEventClosed:
		return event.closed.AccountID
	case PositionEventAdjusted:
		return event.adjusted.AccountID
	default:
		return ""
	}
}

func (event PositionEvent) Opened() (*PositionOpened, bool) {
	return event.opened, event.kind == PositionEventOpened
}

func (event PositionEvent) Changed() (*PositionChanged, bool) {
	return event.changed, event.kind == PositionEventChanged
}

func (event PositionEvent) Closed() (*PositionClosed, bool) {
	return event.closed, event.kind == PositionEventClosed
}

func (event PositionEvent) Adjusted() (*PositionAdjusted, bool) {
	return event.adjusted, event.kind == PositionEventAdjusted
}
