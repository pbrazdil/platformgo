package order

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/upcomers-org/platformgo/internal/ids"
)

func listClientOrderIDs(count int) []ids.ClientOrderID {
	result := make([]ids.ClientOrderID, 0, count)
	for index := 1; index <= count; index++ {
		result = append(result, ids.MustClientOrderID(fmt.Sprintf("O-%03d", index)))
	}
	return result
}

func listOrders(instruments []string, orderListID ids.OrderListID) []ListOrder {
	result := make([]ListOrder, 0, len(instruments))
	for index, instrument := range instruments {
		result = append(result, ListOrder{
			ClientOrderID: ids.MustClientOrderID(fmt.Sprintf("O-%03d", index+1)),
			InstrumentID:  ids.MustInstrumentID(instrument),
			StrategyID:    ids.MustStrategyID("S-001"),
			OrderListID:   &orderListID,
		})
	}
	return result
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/list.rs:256
//	test: test_new_and_display
func TestOrderListNewAndDisplay(t *testing.T) {
	orderList := NewOrderList(
		ids.MustOrderListID("OL-001"),
		ids.MustInstrumentID("AUD/USD.SIM"),
		ids.MustStrategyID("S-001"),
		listClientOrderIDs(3),
		0,
	)
	const prefix = "OrderList(id=OL-001, instrument_id=AUD/USD.SIM, strategy_id=S-001, client_order_ids="
	if !strings.HasPrefix(orderList.String(), prefix) {
		t.Fatalf("display = %q, want prefix %q", orderList.String(), prefix)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/list.rs:291
//	test: test_from_orders_accepts_mixed_instruments_same_venue
func TestOrderListFromOrdersAcceptsMixedInstrumentsSameVenue(t *testing.T) {
	orderListID := ids.MustOrderListID("OL-MIXED-001")
	orders := listOrders([]string{"AUD/USD.SIM", "EUR/USD.SIM"}, orderListID)
	orderList := OrderListFromOrders(orders, 0)
	if orderList.Len() != 2 || orderList.InstrumentID.String() != "AUD/USD.SIM" {
		t.Fatalf("order list = %+v", orderList)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/list.rs:303
//	test: test_from_orders_panics_on_mixed_venues
func TestOrderListFromOrdersPanicsOnMixedVenues(t *testing.T) {
	orderListID := ids.MustOrderListID("OL-MIXED-002")
	orders := listOrders([]string{"AUD/USD.SIM", "EUR/USD.IDEALPRO"}, orderListID)
	defer func() {
		value := recover()
		if value == nil || !strings.Contains(fmt.Sprint(value), "share the same venue") {
			t.Fatalf("panic = %v", value)
		}
	}()
	OrderListFromOrders(orders, 0)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/list.rs:312
//	test: test_from_orders
func TestOrderListFromOrders(t *testing.T) {
	orderListID := ids.MustOrderListID("OL-002")
	orders := listOrders([]string{"AUD/USD.SIM", "AUD/USD.SIM", "AUD/USD.SIM"}, orderListID)
	orderList := OrderListFromOrders(orders, 0)
	if orderList.ID != orderListID || orderList.Len() != 3 ||
		orderList.InstrumentID.String() != "AUD/USD.SIM" ||
		orderList.ClientOrderIDs[0] != ids.MustClientOrderID("O-001") {
		t.Fatalf("order list = %+v", orderList)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/list.rs:325
//	test: test_order_list_equality
func TestOrderListEquality(t *testing.T) {
	orders := listClientOrderIDs(1)
	first := NewOrderList(
		ids.MustOrderListID("OL-006"), ids.MustInstrumentID("AUD/USD.SIM"),
		ids.MustStrategyID("S-001"), orders, 0,
	)
	second := NewOrderList(
		ids.MustOrderListID("OL-006"), ids.MustInstrumentID("AUD/USD.SIM"),
		ids.MustStrategyID("S-001"), orders, 0,
	)
	if !first.Equal(second) {
		t.Fatal("lists with the same list ID are not equal")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/list.rs:348
//	test: test_order_list_inequality
func TestOrderListInequality(t *testing.T) {
	orders := listClientOrderIDs(1)
	first := NewOrderList(
		ids.MustOrderListID("OL-007"), ids.MustInstrumentID("AUD/USD.SIM"),
		ids.MustStrategyID("S-001"), orders, 0,
	)
	second := NewOrderList(
		ids.MustOrderListID("OL-008"), ids.MustInstrumentID("AUD/USD.SIM"),
		ids.MustStrategyID("S-001"), orders, 0,
	)
	if first.Equal(second) {
		t.Fatal("lists with different list IDs are equal")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/list.rs:371
//	test: test_order_list_first
func TestOrderListFirst(t *testing.T) {
	orders := listClientOrderIDs(2)
	orderList := NewOrderList(
		ids.MustOrderListID("OL-009"), ids.MustInstrumentID("AUD/USD.SIM"),
		ids.MustStrategyID("S-001"), orders, 0,
	)
	first, ok := orderList.First()
	if !ok || first != orders[0] {
		t.Fatalf("first = %s, %t", first, ok)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/list.rs:389
//	test: test_order_list_len
func TestOrderListLength(t *testing.T) {
	orderList := NewOrderList(
		ids.MustOrderListID("OL-010"), ids.MustInstrumentID("AUD/USD.SIM"),
		ids.MustStrategyID("S-001"), listClientOrderIDs(3), 0,
	)
	if orderList.Len() != 3 || orderList.IsEmpty() {
		t.Fatalf("length/empty = %d/%t", orderList.Len(), orderList.IsEmpty())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/list.rs:405
//	test: test_order_list_hash
func TestOrderListHash(t *testing.T) {
	orders := listClientOrderIDs(1)
	first := NewOrderList(
		ids.MustOrderListID("OL-011"), ids.MustInstrumentID("AUD/USD.SIM"),
		ids.MustStrategyID("S-001"), orders, 0,
	)
	second := NewOrderList(
		ids.MustOrderListID("OL-011"), ids.MustInstrumentID("AUD/USD.SIM"),
		ids.MustStrategyID("S-001"), orders, 0,
	)
	if first.Hash() != second.Hash() {
		t.Fatalf("hashes = %d/%d", first.Hash(), second.Hash())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/list.rs:433
//	test: test_validate_accepts_well_formed_list
func TestOrderListValidateAcceptsWellFormedList(t *testing.T) {
	orderList := NewOrderList(
		ids.MustOrderListID("OL-VALID-001"), ids.MustInstrumentID("AUD/USD.SIM"),
		ids.MustStrategyID("S-001"), listClientOrderIDs(3), 0,
	)
	if err := orderList.Validate(); err != nil {
		t.Fatal(err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/list.rs:448
//	test: test_validate_rejects_empty_list
func TestOrderListValidateRejectsEmptyList(t *testing.T) {
	orderListID := ids.MustOrderListID("OL-EMPTY-001")
	orderList := NewOrderList(
		orderListID, ids.MustInstrumentID("AUD/USD.SIM"),
		ids.MustStrategyID("S-001"), nil, 0,
	)
	err := orderList.Validate()
	var validationError *OrderListValidationError
	if !errors.As(err, &validationError) ||
		validationError.Kind != OrderListValidationEmpty ||
		validationError.OrderListID != orderListID ||
		err.Error() != "OrderList OL-EMPTY-001 has no orders" {
		t.Fatalf("error = %#v", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/list.rs:467
//	test: test_validate_rejects_duplicate_client_order_ids
func TestOrderListValidateRejectsDuplicateClientOrderIDs(t *testing.T) {
	orderListID := ids.MustOrderListID("OL-DUP-001")
	clientOrderID := ids.MustClientOrderID("O-001")
	orderList := NewOrderList(
		orderListID, ids.MustInstrumentID("AUD/USD.SIM"),
		ids.MustStrategyID("S-001"),
		[]ids.ClientOrderID{clientOrderID, clientOrderID}, 0,
	)
	err := orderList.Validate()
	var validationError *OrderListValidationError
	if !errors.As(err, &validationError) ||
		validationError.Kind != OrderListValidationDuplicate ||
		validationError.OrderListID != orderListID ||
		err.Error() != "OrderList OL-DUP-001 contains duplicate client_order_ids" {
		t.Fatalf("error = %#v", err)
	}
}
