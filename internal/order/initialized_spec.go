package order

import (
	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
)

type OrderInitializedSpecEvent struct {
	TraderID            ids.TraderID
	StrategyID          ids.StrategyID
	InstrumentID        ids.InstrumentID
	ClientOrderID       ids.ClientOrderID
	OrderSide           OrderSide
	OrderType           OrderType
	Quantity            decimal.Quantity
	TimeInForce         TimeInForce
	PostOnly            bool
	ReduceOnly          bool
	QuoteQuantity       bool
	Reconciliation      bool
	EventID             string
	TsEvent             uint64
	TsInit              uint64
	Price               *decimal.Price
	ActivationPrice     *decimal.Price
	TriggerPrice        *decimal.Price
	TriggerType         *TriggerType
	LimitOffset         *decimal.Decimal
	TrailingOffset      *decimal.Decimal
	TrailingOffsetType  *TrailingOffsetType
	ExpireTime          *uint64
	DisplayQuantity     *decimal.Quantity
	EmulationTrigger    *TriggerType
	TriggerInstrumentID *ids.InstrumentID
	ContingencyType     *ContingencyType
	OrderListID         *ids.OrderListID
	LinkedOrderIDs      []ids.ClientOrderID
	ParentOrderID       *ids.ClientOrderID
	ExecAlgorithmID     *ids.ExecAlgorithmID
	ExecAlgorithmParams map[string]string
	ExecSpawnID         *ids.ClientOrderID
	Tags                []string
}

type OrderInitializedSpec struct {
	OrderInitializedSpecEvent
	sequence *OrderSpecEventIDSequence
}

func NewOrderInitializedSpec(sequence *OrderSpecEventIDSequence) OrderInitializedSpec {
	requireOrderSpecSequence(sequence)
	return OrderInitializedSpec{
		OrderInitializedSpecEvent: OrderInitializedSpecEvent{
			TraderID: ids.DefaultTraderID(), StrategyID: ids.MustStrategyID("S-001"),
			InstrumentID:  ids.MustInstrumentID("AUD/USD.SIM"),
			ClientOrderID: ids.MustClientOrderID("O-19700101-000000-001-001-1"),
			OrderSide:     OrderSideBuy, OrderType: OrderTypeMarket,
			Quantity: decimal.MustQuantity("100000"), TimeInForce: TimeInForceDay,
		},
		sequence: sequence,
	}
}

func (spec OrderInitializedSpec) WithOrderType(value OrderType) OrderInitializedSpec {
	spec.OrderType = value
	return spec
}
func (spec OrderInitializedSpec) WithOrderSide(value OrderSide) OrderInitializedSpec {
	spec.OrderSide = value
	return spec
}
func (spec OrderInitializedSpec) WithQuantity(value decimal.Quantity) OrderInitializedSpec {
	spec.Quantity = value
	return spec
}
func (spec OrderInitializedSpec) WithPrice(value decimal.Price) OrderInitializedSpec {
	spec.Price = copyPointer(value)
	return spec
}
func (spec OrderInitializedSpec) WithPostOnly(value bool) OrderInitializedSpec {
	spec.PostOnly = value
	return spec
}
func (spec OrderInitializedSpec) Build() OrderInitializedSpecEvent {
	event := spec.OrderInitializedSpecEvent
	event.EventID = spec.sequence.Next()
	event.Price = copyPointerValue(event.Price)
	event.ActivationPrice = copyPointerValue(event.ActivationPrice)
	event.TriggerPrice = copyPointerValue(event.TriggerPrice)
	event.TriggerType = copyPointerValue(event.TriggerType)
	event.LimitOffset = copyPointerValue(event.LimitOffset)
	event.TrailingOffset = copyPointerValue(event.TrailingOffset)
	event.TrailingOffsetType = copyPointerValue(event.TrailingOffsetType)
	event.ExpireTime = copyPointerValue(event.ExpireTime)
	event.DisplayQuantity = copyPointerValue(event.DisplayQuantity)
	event.EmulationTrigger = copyPointerValue(event.EmulationTrigger)
	event.TriggerInstrumentID = copyPointerValue(event.TriggerInstrumentID)
	event.ContingencyType = copyPointerValue(event.ContingencyType)
	event.OrderListID = copyPointerValue(event.OrderListID)
	event.LinkedOrderIDs = append([]ids.ClientOrderID(nil), event.LinkedOrderIDs...)
	event.ParentOrderID = copyPointerValue(event.ParentOrderID)
	event.ExecAlgorithmID = copyPointerValue(event.ExecAlgorithmID)
	if event.ExecAlgorithmParams != nil {
		event.ExecAlgorithmParams = cloneStringMap(event.ExecAlgorithmParams)
	}
	event.ExecSpawnID = copyPointerValue(event.ExecSpawnID)
	event.Tags = append([]string(nil), event.Tags...)
	return event
}

func cloneStringMap(source map[string]string) map[string]string {
	cloned := make(map[string]string, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}
