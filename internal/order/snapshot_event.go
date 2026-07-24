package order

import (
	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
)

type OrderSnapshot struct {
	TraderID        ids.TraderID
	StrategyID      ids.StrategyID
	InstrumentID    ids.InstrumentID
	ClientOrderID   ids.ClientOrderID
	VenueOrderID    *ids.VenueOrderID
	OrderType       OrderType
	OrderSide       OrderSide
	Quantity        decimal.Quantity
	Price           *decimal.Price
	Status          OrderStatus
	TsInit          uint64
	TsLast          uint64
	FilledQuantity  decimal.Quantity
	IsPostOnly      bool
	IsQuoteQuantity bool
}

func SnapshotFromMarketOrder(order *MarketOrder) OrderSnapshot {
	return snapshotFromCore(
		order.traderID, order.strategyID, order.core, order.Price(), false,
	)
}

func SnapshotFromLimitOrder(order *Limit) OrderSnapshot {
	price := order.Price()
	return snapshotFromCore(
		order.traderID, order.strategyID, order.core, &price, order.postOnly,
	)
}

func snapshotFromCore(
	traderID ids.TraderID,
	strategyID ids.StrategyID,
	core *Core,
	price *decimal.Price,
	postOnly bool,
) OrderSnapshot {
	var venueOrderID *ids.VenueOrderID
	venueIDs := core.VenueOrderIDs()
	if len(venueIDs) != 0 {
		venueOrderID = copyPointer(venueIDs[len(venueIDs)-1])
	}
	return OrderSnapshot{
		TraderID: traderID, StrategyID: strategyID,
		InstrumentID: core.config.InstrumentID, ClientOrderID: core.config.ClientOrderID,
		VenueOrderID: venueOrderID, OrderType: core.config.Type, OrderSide: core.config.Side,
		Quantity: core.Quantity(), Price: copyPointerValue(price), Status: core.Status(),
		TsInit: core.config.TimestampInit, TsLast: core.TimestampLast(),
		FilledQuantity: core.FilledQuantity(), IsPostOnly: postOnly,
		IsQuoteQuantity: core.config.QuoteQuantity,
	}
}
