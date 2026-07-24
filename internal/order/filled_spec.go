package order

import (
	"github.com/upcomers-org/platformgo/internal/currency"
	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
	"github.com/upcomers-org/platformgo/internal/money"
)

type OrderFilledSpec struct {
	TraderID       ids.TraderID
	StrategyID     ids.StrategyID
	InstrumentID   ids.InstrumentID
	ClientOrderID  ids.ClientOrderID
	VenueOrderID   ids.VenueOrderID
	AccountID      ids.AccountID
	TradeID        ids.TradeID
	OrderSide      OrderSide
	OrderType      OrderType
	LastQuantity   decimal.Quantity
	LastPrice      decimal.Price
	Currency       currency.Currency
	LiquiditySide  LiquiditySide
	TsEvent        uint64
	TsInit         uint64
	Reconciliation bool
	PositionID     *ids.PositionID
	Commission     *money.Money
	Info           []FillEventInfo
	sequence       *OrderSpecEventIDSequence
}

func NewOrderFilledSpec(sequence *OrderSpecEventIDSequence) OrderFilledSpec {
	requireOrderSpecSequence(sequence)
	return OrderFilledSpec{
		TraderID: ids.DefaultTraderID(), StrategyID: ids.MustStrategyID("S-001"),
		InstrumentID:  ids.MustInstrumentID("AUD/USD.SIM"),
		ClientOrderID: ids.MustClientOrderID("O-19700101-000000-001-001-1"),
		VenueOrderID:  ids.MustVenueOrderID("001"), AccountID: ids.MustAccountID("SIM-001"),
		TradeID: ids.MustTradeID("1"), OrderSide: OrderSideBuy, OrderType: OrderTypeMarket,
		LastQuantity: decimal.MustQuantity("100000"), LastPrice: decimal.MustPrice("1.00000"),
		Currency: currency.USD(), LiquiditySide: LiquiditySideTaker, sequence: sequence,
	}
}

func (spec OrderFilledSpec) WithOrderSide(value OrderSide) OrderFilledSpec {
	spec.OrderSide = value
	return spec
}
func (spec OrderFilledSpec) WithLastQuantity(value decimal.Quantity) OrderFilledSpec {
	spec.LastQuantity = value
	return spec
}
func (spec OrderFilledSpec) WithLastPrice(value decimal.Price) OrderFilledSpec {
	spec.LastPrice = value
	return spec
}
func (spec OrderFilledSpec) WithCommission(value money.Money) OrderFilledSpec {
	spec.Commission = copyPointer(value)
	return spec
}
func (spec OrderFilledSpec) Build() OrderFilledEvent {
	return NewOrderFilledEvent(OrderFilledEventConfig{
		TraderID: spec.TraderID, StrategyID: spec.StrategyID,
		InstrumentID: spec.InstrumentID, ClientOrderID: spec.ClientOrderID,
		VenueOrderID: spec.VenueOrderID, AccountID: spec.AccountID, TradeID: spec.TradeID,
		OrderSide: spec.OrderSide, OrderType: spec.OrderType,
		LastQuantity: spec.LastQuantity, LastPrice: spec.LastPrice,
		Currency: spec.Currency, LiquiditySide: spec.LiquiditySide,
		EventID: spec.sequence.Next(), TsEvent: spec.TsEvent, TsInit: spec.TsInit,
		Reconciliation: spec.Reconciliation, PositionID: spec.PositionID,
		Commission: spec.Commission, Info: spec.Info,
	})
}
