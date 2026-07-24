package position

import (
	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
	"github.com/upcomers-org/platformgo/internal/money"
)

// PositionLifecycleFill is the typed fill projection retained by opened and
// changed position events.
type PositionLifecycleFill struct {
	LastQuantity decimal.Quantity
	LastPrice    decimal.Price
	TsEvent      uint64
}

func CreatePositionOpened(
	position *Position,
	identity PositionIdentity,
	fill PositionLifecycleFill,
	eventID EventID,
	tsInit uint64,
) PositionOpened {
	return PositionOpened{
		TraderID: identity.TraderID, StrategyID: identity.StrategyID,
		InstrumentID: ids.MustInstrumentID(position.Instrument.ID),
		PositionID:   ids.MustPositionID(position.ID), AccountID: identity.AccountID,
		OpeningOrderID: ids.MustClientOrderID(position.OpeningOrderID),
		Entry:          position.Entry, Side: position.Side,
		SignedQuantity: position.SignedQuantity,
		Quantity:       decimal.MustQuantity(position.Quantity.String()),
		LastQuantity:   fill.LastQuantity, LastPrice: fill.LastPrice,
		Currency: position.Instrument.QuoteCurrency, AverageOpen: position.AverageOpen,
		EventID: eventID, TsEvent: fill.TsEvent, TsInit: tsInit,
	}
}

func CreatePositionChanged(
	position *Position,
	identity PositionIdentity,
	fill PositionLifecycleFill,
	eventID EventID,
	tsInit uint64,
) PositionChanged {
	return PositionChanged{
		TraderID: identity.TraderID, StrategyID: identity.StrategyID,
		InstrumentID: ids.MustInstrumentID(position.Instrument.ID),
		PositionID:   ids.MustPositionID(position.ID), AccountID: identity.AccountID,
		OpeningOrderID: ids.MustClientOrderID(position.OpeningOrderID),
		Entry:          position.Entry, Side: position.Side,
		SignedQuantity: position.SignedQuantity,
		Quantity:       decimal.MustQuantity(position.Quantity.String()),
		PeakQuantity:   decimal.MustQuantity(position.PeakQuantity.String()),
		LastQuantity:   fill.LastQuantity, LastPrice: fill.LastPrice,
		Currency: position.Instrument.QuoteCurrency, AverageOpen: position.AverageOpen,
		AverageClose:   cloneDecimal(position.AverageClose),
		RealizedReturn: position.RealizedReturn, RealizedPnL: cloneMoney(position.RealizedPnL),
		UnrealizedPnL: money.Zero(position.Instrument.QuoteCurrency),
		EventID:       eventID, TsOpened: position.TsOpened,
		TsEvent: fill.TsEvent, TsInit: tsInit,
	}
}
