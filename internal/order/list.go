package order

import (
	"fmt"
	"hash/fnv"

	"github.com/upcomers-org/platformgo/internal/ids"
)

type ListOrder struct {
	ClientOrderID ids.ClientOrderID
	InstrumentID  ids.InstrumentID
	StrategyID    ids.StrategyID
	OrderListID   *ids.OrderListID
}

type OrderList struct {
	ID             ids.OrderListID
	InstrumentID   ids.InstrumentID
	StrategyID     ids.StrategyID
	ClientOrderIDs []ids.ClientOrderID
	TimestampInit  uint64
}

func NewOrderList(
	orderListID ids.OrderListID,
	instrumentID ids.InstrumentID,
	strategyID ids.StrategyID,
	clientOrderIDs []ids.ClientOrderID,
	timestampInit uint64,
) OrderList {
	return OrderList{
		ID: orderListID, InstrumentID: instrumentID, StrategyID: strategyID,
		ClientOrderIDs: append([]ids.ClientOrderID(nil), clientOrderIDs...),
		TimestampInit:  timestampInit,
	}
}

func OrderListFromOrders(orders []ListOrder, timestampInit uint64) OrderList {
	if len(orders) == 0 {
		panic("OrderList::from_orders requires non-empty orders")
	}
	first := orders[0]
	if first.OrderListID == nil {
		panic("OrderList::from_orders requires first order to have order_list_id")
	}
	venue := first.InstrumentID.Venue
	clientOrderIDs := make([]ids.ClientOrderID, 0, len(orders))
	for _, order := range orders {
		if order.InstrumentID.Venue != venue {
			panic(fmt.Sprintf(
				"OrderList::from_orders requires all orders to share the same venue; expected %s, found %s on %s",
				venue, order.InstrumentID.Venue, order.ClientOrderID,
			))
		}
		clientOrderIDs = append(clientOrderIDs, order.ClientOrderID)
	}
	return NewOrderList(
		*first.OrderListID, first.InstrumentID, first.StrategyID,
		clientOrderIDs, timestampInit,
	)
}

type OrderListValidationKind string

const (
	OrderListValidationEmpty     OrderListValidationKind = "empty_client_order_ids"
	OrderListValidationDuplicate OrderListValidationKind = "duplicate_client_order_ids"
)

type OrderListValidationError struct {
	Kind        OrderListValidationKind
	OrderListID ids.OrderListID
}

func (e *OrderListValidationError) Error() string {
	switch e.Kind {
	case OrderListValidationEmpty:
		return fmt.Sprintf("OrderList %s has no orders", e.OrderListID)
	case OrderListValidationDuplicate:
		return fmt.Sprintf("OrderList %s contains duplicate client_order_ids", e.OrderListID)
	default:
		return string(e.Kind)
	}
}

func (o OrderList) Validate() error {
	if len(o.ClientOrderIDs) == 0 {
		return &OrderListValidationError{
			Kind: OrderListValidationEmpty, OrderListID: o.ID,
		}
	}
	seen := make(map[ids.ClientOrderID]struct{}, len(o.ClientOrderIDs))
	for _, clientOrderID := range o.ClientOrderIDs {
		if _, exists := seen[clientOrderID]; exists {
			return &OrderListValidationError{
				Kind: OrderListValidationDuplicate, OrderListID: o.ID,
			}
		}
		seen[clientOrderID] = struct{}{}
	}
	return nil
}

func (o OrderList) Equal(other OrderList) bool { return o.ID == other.ID }

func (o OrderList) First() (ids.ClientOrderID, bool) {
	if len(o.ClientOrderIDs) == 0 {
		return "", false
	}
	return o.ClientOrderIDs[0], true
}

func (o OrderList) Len() int      { return len(o.ClientOrderIDs) }
func (o OrderList) IsEmpty() bool { return len(o.ClientOrderIDs) == 0 }

func (o OrderList) Hash() uint64 {
	hasher := fnv.New64a()
	_, _ = hasher.Write([]byte(o.ID))
	return hasher.Sum64()
}

func (o OrderList) String() string {
	return fmt.Sprintf(
		"OrderList(id=%s, instrument_id=%s, strategy_id=%s, client_order_ids=%v, ts_init=%d)",
		o.ID, o.InstrumentID, o.StrategyID, o.ClientOrderIDs, o.TimestampInit,
	)
}
