package position

import (
	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
	"github.com/upcomers-org/platformgo/internal/money"
)

// PositionCloseFill is the source fill data retained by a PositionClosed
// event. The additional identifiers preserve the fill's typed identity even
// though closure projection only consumes quantity, price, and event time.
type PositionCloseFill struct {
	StrategyID    ids.StrategyID
	InstrumentID  ids.InstrumentID
	ClientOrderID ids.ClientOrderID
	VenueOrderID  ids.VenueOrderID
	TradeID       ids.TradeID
	PositionID    ids.PositionID
	Side          OrderSide
	LastQuantity  decimal.Quantity
	LastPrice     decimal.Price
	Commission    money.Money
	TsEvent       uint64
	TsInit        uint64
}

// CreatePositionClosed projects the current position and closing fill into a
// lifecycle event. It does not apply the fill; callers supply the position
// state which should be represented by the event.
func CreatePositionClosed(
	position *Position,
	identity PositionIdentity,
	fill PositionCloseFill,
	eventID EventID,
	tsInit uint64,
) PositionClosed {
	var closingOrderID *ids.ClientOrderID
	if position.ClosingOrderID != nil {
		value := ids.MustClientOrderID(*position.ClosingOrderID)
		closingOrderID = &value
	}
	return PositionClosed{
		TraderID:       identity.TraderID,
		StrategyID:     identity.StrategyID,
		InstrumentID:   ids.MustInstrumentID(position.Instrument.ID),
		PositionID:     ids.MustPositionID(position.ID),
		AccountID:      identity.AccountID,
		OpeningOrderID: ids.MustClientOrderID(position.OpeningOrderID),
		ClosingOrderID: closingOrderID,
		Entry:          position.Entry,
		Side:           position.Side,
		SignedQuantity: position.SignedQuantity,
		Quantity:       decimal.MustQuantity(position.Quantity.String()),
		PeakQuantity:   decimal.MustQuantity(position.PeakQuantity.String()),
		LastQuantity:   fill.LastQuantity,
		LastPrice:      fill.LastPrice,
		Currency:       position.Instrument.QuoteCurrency,
		AverageOpen:    position.AverageOpen,
		AverageClose:   cloneDecimal(position.AverageClose),
		RealizedReturn: position.RealizedReturn,
		RealizedPnL:    cloneMoney(position.RealizedPnL),
		UnrealizedPnL:  money.Zero(position.Instrument.QuoteCurrency),
		Duration:       position.Duration,
		EventID:        eventID,
		TsOpened:       position.TsOpened,
		TsClosed:       cloneUint64(position.TsClosed),
		TsEvent:        fill.TsEvent,
		TsInit:         tsInit,
	}
}
