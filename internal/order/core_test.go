package order

import (
	"errors"
	"testing"

	"github.com/upcomers-org/platformgo/internal/currency"
	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
	"github.com/upcomers-org/platformgo/internal/money"
)

const ordersSourceRevision = "116c9b5159ebeb6b578b737d72298cac8d723723"

var (
	testAccount = ids.MustAccountID("SIM-001")
	testVenue   = ids.MustVenueOrderID("V-001")
	testTrade1  = ids.MustTradeID("1")
	testTrade2  = ids.MustTradeID("2")
)

func testOrder(t *testing.T, config Config) *Core {
	t.Helper()
	order, err := NewCore(config)
	if err != nil {
		t.Fatal(err)
	}
	return order
}

func acceptedOrder(t *testing.T, config Config) *Core {
	t.Helper()
	order := testOrder(t, config)
	if err := order.Accept(testAccount, testVenue, 2); err != nil {
		t.Fatal(err)
	}
	return order
}

func testFill(trade ids.TradeID, quantity, price string, timestamp uint64) Fill {
	return Fill{
		TradeID: trade, Quantity: decimal.MustQuantity(quantity),
		Price: decimal.MustPrice(price), Timestamp: timestamp,
	}
}

func testCommission(amount string) *money.Money {
	value := money.MustNew(amount, currency.USD())
	return &value
}

func requireStatus(t *testing.T, order *Core, status OrderStatus) {
	t.Helper()
	if order.Status() != status {
		t.Fatalf("status = %v, want %v (%s)", order.Status(), status, order.DebugState())
	}
}

func requireQuantity(t *testing.T, got decimal.Quantity, want string) {
	t.Helper()
	expected := decimal.MustQuantity(want)
	if got.Cmp(expected) != 0 {
		t.Fatalf("quantity = %s, want %s", got, expected)
	}
}

func requireErrorKind(t *testing.T, err error, kind ErrorKind) {
	t.Helper()
	var orderError *Error
	if !errors.As(err, &orderError) || orderError.Kind != kind {
		t.Fatalf("error = %#v, want kind %q", err, kind)
	}
}

func requireErrorKindTrade(t *testing.T, err error, kind ErrorKind, tradeID ids.TradeID) {
	t.Helper()
	var orderError *Error
	if !errors.As(err, &orderError) || orderError.Kind != kind || orderError.TradeID != tradeID {
		t.Fatalf("error = %#v, want kind %q and trade ID %q", err, kind, tradeID)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/mod.rs:1431
//	test: test_closing_side
func TestClosingSide(t *testing.T) {
	if ClosingSide(PositionSideLong) != OrderSideSell ||
		ClosingSide(PositionSideShort) != OrderSideBuy ||
		ClosingSide(PositionSideNoPositionSide) != OrderSideNoOrderSide {
		t.Fatal("closing-side mapping differs from the source")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/mod.rs:1439
//	test: test_signed_decimal_qty
func TestSignedDecimalQuantity(t *testing.T) {
	buy := testOrder(t, Config{Side: OrderSideBuy, Quantity: decimal.MustQuantity("10000")})
	sell := testOrder(t, Config{Side: OrderSideSell, Quantity: decimal.MustQuantity("10000")})
	if buy.SignedQuantity().String() != "10000" || sell.SignedQuantity().String() != "-10000" {
		t.Fatalf("signed quantities = %s, %s", buy.SignedQuantity(), sell.SignedQuantity())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/mod.rs:1461
//	test: test_would_reduce_only
func TestWouldReduceOnly(t *testing.T) {
	cases := []struct {
		side        OrderSide
		orderQty    string
		pos         PositionSide
		positionQty string
		want        bool
	}{
		{OrderSideBuy, "100", PositionSideLong, "50", false},
		{OrderSideBuy, "50", PositionSideShort, "50", true},
		{OrderSideBuy, "50", PositionSideShort, "100", true},
		{OrderSideBuy, "50", PositionSideFlat, "0", false},
		{OrderSideSell, "50", PositionSideFlat, "0", false},
		{OrderSideSell, "50", PositionSideLong, "50", true},
		{OrderSideSell, "50", PositionSideLong, "100", true},
		{OrderSideSell, "100", PositionSideShort, "50", false},
	}
	for _, tc := range cases {
		order := testOrder(t, Config{Side: tc.side, Quantity: decimal.MustQuantity(tc.orderQty)})
		if got := order.WouldReduceOnly(tc.pos, decimal.MustQuantity(tc.positionQty)); got != tc.want {
			t.Fatalf("side=%v position=%v qty=%s: got %t, want %t", tc.side, tc.pos, tc.orderQty, got, tc.want)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/mod.rs:1482
//	test: test_order_state_transition_denied
func TestOrderStateTransitionDenied(t *testing.T) {
	order := testOrder(t, Config{})
	if err := order.Deny(1); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, order, OrderStatusDenied)
	if order.IsOpen() || !order.IsClosed() || order.EventCount() != 2 || order.LastEvent() != "Denied" {
		t.Fatal("denied lifecycle invariants not preserved")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/mod.rs:1497
//	test: test_order_life_cycle_to_filled
func TestOrderLifeCycleToFilled(t *testing.T) {
	order := testOrder(t, Config{})
	if err := order.Submit(testAccount, 1); err != nil {
		t.Fatal(err)
	}
	if err := order.Accept(testAccount, testVenue, 2); err != nil {
		t.Fatal(err)
	}
	fill := testFill(testTrade1, "100000", "1.00000", 3)
	if err := order.Fill(fill); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, order, OrderStatusFilled)
	requireQuantity(t, order.FilledQuantity(), "100000")
	requireQuantity(t, order.LeavesQuantity(), "0")
	if order.AveragePrice() == nil || order.AveragePrice().String() != "1" || order.IsOpen() || !order.IsClosed() {
		t.Fatal("filled lifecycle values differ")
	}
	if _, ok := order.Commission(currency.USD()); ok || len(order.Commissions()) != 0 {
		t.Fatal("commission recorded for commission-free fill")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/mod.rs:1520
//	test: test_order_life_cycle_fills_with_negative_prices
func TestOrderLifeCycleFillsWithNegativePrices(t *testing.T) {
	order := acceptedOrder(t, Config{})
	if err := order.Fill(testFill(testTrade1, "50000", "-5.00000", 3)); err != nil {
		t.Fatal(err)
	}
	if err := order.Fill(testFill(testTrade2, "50000", "-7.00000", 4)); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, order, OrderStatusFilled)
	requireQuantity(t, order.FilledQuantity(), "100000")
	requireQuantity(t, order.LeavesQuantity(), "0")
	if order.AveragePrice() == nil || order.AveragePrice().String() != "-6" {
		t.Fatalf("average price = %v, want -6", order.AveragePrice())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/mod.rs:1554
//	test: test_order_life_cycle_accumulates_fill_commissions
func TestOrderLifeCycleAccumulatesFillCommissions(t *testing.T) {
	order := acceptedOrder(t, Config{})
	first := testFill(testTrade1, "50000", "1", 3)
	first.Commission = testCommission("1.25")
	second := testFill(testTrade2, "50000", "1", 4)
	second.Commission = testCommission("1.35")
	if err := order.Fill(first); err != nil {
		t.Fatal(err)
	}
	if err := order.Fill(second); err != nil {
		t.Fatal(err)
	}
	commission, ok := order.Commission(currency.USD())
	if !ok || !commission.Equal(money.MustNew("2.60", currency.USD())) ||
		len(order.Commissions()) != 1 {
		t.Fatalf("commission = %v, %t", commission, ok)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/mod.rs:1586
//	test: test_fill_void_reopens_only_when_explicitly_marked
func TestFillVoidReopensOnlyWhenExplicitlyMarked(t *testing.T) {
	terminal := acceptedOrder(t, Config{})
	fill := testFill(testTrade1, "100000", "1", 3)
	fill.Commission = testCommission("1")
	if err := terminal.Fill(fill); err != nil {
		t.Fatal(err)
	}
	if err := terminal.VoidFill(FillVoid{TradeID: testTrade1, Quantity: decimal.MustQuantity("40000"), Commission: testCommission(".40"), Timestamp: 4}); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, terminal, OrderStatusVoided)
	if err := terminal.Fill(testFill(testTrade2, "40000", "1", 5)); err == nil {
		t.Fatal("late fill accepted after terminal void")
	}

	reopened := acceptedOrder(t, Config{})
	if err := reopened.Fill(fill); err != nil {
		t.Fatal(err)
	}
	if err := reopened.VoidFill(FillVoid{TradeID: testTrade1, Quantity: decimal.MustQuantity("40000"), Commission: testCommission(".40"), Reopened: true, Timestamp: 4}); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, reopened, OrderStatusPartiallyFilled)
	if err := reopened.Fill(testFill(testTrade2, "40000", "1", 5)); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, reopened, OrderStatusFilled)
	requireQuantity(t, reopened.FilledQuantity(), "100000")
	requireQuantity(t, reopened.VoidedQuantity(), "40000")
	requireQuantity(t, reopened.LeavesQuantity(), "0")
	if !reopened.IsClosed() {
		t.Fatal("corrected order did not close after replacement fill")
	}
	commission, _ := reopened.Commission(currency.USD())
	if !commission.Equal(money.MustNew(".60", currency.USD())) {
		t.Fatalf("commission = %s, want 0.60 USD", commission)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/mod.rs:1644
//	test: test_fill_void_preserves_existing_working_remainder
func TestFillVoidPreservesExistingWorkingRemainder(t *testing.T) {
	order := acceptedOrder(t, Config{})
	if err := order.Fill(testFill(testTrade1, "40000", "1", 3)); err != nil {
		t.Fatal(err)
	}
	if err := order.VoidFill(FillVoid{TradeID: testTrade1, Quantity: decimal.MustQuantity("40000"), Timestamp: 4}); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, order, OrderStatusAccepted)
	requireQuantity(t, order.FilledQuantity(), "0")
	requireQuantity(t, order.VoidedQuantity(), "40000")
	requireQuantity(t, order.LeavesQuantity(), "60000")
	if !order.IsOpen() {
		t.Fatal("working remainder was not left open")
	}
	if err := order.Fill(testFill(testTrade2, "60000", "1", 5)); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, order, OrderStatusVoided)
	requireQuantity(t, order.FilledQuantity(), "60000")
	requireQuantity(t, order.VoidedQuantity(), "40000")
	requireQuantity(t, order.LeavesQuantity(), "0")
	if !order.IsClosed() {
		t.Fatal("fill plus void did not close order")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/mod.rs:1689
//	test: test_reopened_fill_void_requires_existing_fill
func TestReopenedFillVoidRequiresExistingFill(t *testing.T) {
	order := acceptedOrder(t, Config{})
	err := order.VoidFill(FillVoid{TradeID: testTrade1, Quantity: decimal.MustQuantity("1"), Reopened: true})
	requireErrorKind(t, err, ErrorInvalidOrderEvent)
	if err := order.Fill(testFill(testTrade1, "40000", "1", 2)); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, order, OrderStatusPartiallyFilled)
	requireQuantity(t, order.FilledQuantity(), "40000")
	requireQuantity(t, order.VoidedQuantity(), "0")
	requireQuantity(t, order.LeavesQuantity(), "60000")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/mod.rs:1724
//	test: test_terminal_fill_void_rejects_later_order_events
func TestTerminalFillVoidRejectsLaterOrderEvents(t *testing.T) {
	for _, terminal := range []OrderStatus{OrderStatusAccepted, OrderStatusCanceled, OrderStatusExpired} {
		order := acceptedOrder(t, Config{})
		switch terminal {
		case OrderStatusCanceled:
			if err := order.Cancel(3); err != nil {
				t.Fatal(err)
			}
		case OrderStatusExpired:
			if err := order.Expire(3); err != nil {
				t.Fatal(err)
			}
		}
		if err := order.VoidFill(FillVoid{TradeID: testTrade1, Quantity: decimal.MustQuantity("40000"), Timestamp: 4}); err != nil {
			t.Fatal(err)
		}
		if terminal == OrderStatusAccepted {
			requireStatus(t, order, OrderStatusVoided)
		}
		if err := order.Update(Update{Timestamp: 5}); err == nil {
			t.Fatal("terminal order accepted update")
		}
		if err := order.Cancel(6); err == nil {
			t.Fatal("terminal order accepted cancel")
		}
		if err := order.Fill(testFill(testTrade1, "40000", "1", 7)); err == nil {
			t.Fatal("terminal order accepted fill")
		}
		requireStatus(t, order, OrderStatusVoided)
		requireQuantity(t, order.FilledQuantity(), "0")
		requireQuantity(t, order.VoidedQuantity(), "40000")
		requireQuantity(t, order.LeavesQuantity(), "0")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/mod.rs:1792
//	test: test_terminal_fill_void_rejects_invalid_economic_values
func TestTerminalFillVoidRejectsInvalidEconomicValues(t *testing.T) {
	order := acceptedOrder(t, Config{})
	requireErrorKindTrade(t, order.VoidFill(FillVoid{TradeID: testTrade1, Quantity: decimal.MustQuantity("100001")}), ErrorOverVoid, testTrade1)
	requireErrorKindTrade(t, order.VoidFill(FillVoid{TradeID: testTrade1, Quantity: decimal.MustQuantity("100000"), Commission: testCommission("1")}), ErrorOverVoid, testTrade1)
	requireStatus(t, order, OrderStatusAccepted)
	requireQuantity(t, order.FilledQuantity(), "0")
	requireQuantity(t, order.VoidedQuantity(), "0")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/mod.rs:1823
//	test: test_terminal_fill_void_accepts_current_venue_order_id_after_fill_before_accept
func TestTerminalFillVoidAcceptsCurrentVenueOrderIDAfterFillBeforeAccept(t *testing.T) {
	order := testOrder(t, Config{})
	if err := order.Submit(testAccount, 1); err != nil {
		t.Fatal(err)
	}
	fill := testFill(testTrade1, "40000", "1", 2)
	fill.AccountID, fill.VenueOrderID = &testAccount, &testVenue
	if err := order.Fill(fill); err != nil {
		t.Fatal(err)
	}
	if err := order.VoidFill(FillVoid{TradeID: testTrade2, Quantity: decimal.MustQuantity("10000"), AccountID: &testAccount, VenueOrderID: &testVenue, Timestamp: 3}); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, order, OrderStatusVoided)
	requireQuantity(t, order.FilledQuantity(), "40000")
	requireQuantity(t, order.VoidedQuantity(), "10000")
	requireQuantity(t, order.LeavesQuantity(), "0")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/mod.rs:1854
//	test: test_terminal_fill_void_requires_matching_order_identity
func TestTerminalFillVoidRequiresMatchingOrderIdentity(t *testing.T) {
	order := acceptedOrder(t, Config{})
	otherInstrument := ids.MustInstrumentID("GBPUSD.SIM")
	otherAccount := ids.MustAccountID("SIM-002")
	otherVenue := ids.MustVenueOrderID("V-002")
	for _, event := range []FillVoid{
		{TradeID: testTrade1, Quantity: decimal.MustQuantity("1"), InstrumentID: &otherInstrument},
		{TradeID: testTrade1, Quantity: decimal.MustQuantity("1"), AccountID: &otherAccount},
		{TradeID: testTrade1, Quantity: decimal.MustQuantity("1"), VenueOrderID: &otherVenue},
	} {
		requireErrorKind(t, order.VoidFill(event), ErrorInvalidOrderEvent)
	}
	requireStatus(t, order, OrderStatusAccepted)
	requireQuantity(t, order.VoidedQuantity(), "0")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/mod.rs:1880
//	test: test_fill_void_does_not_reopen_canceled_or_expired_order
func TestFillVoidDoesNotReopenCanceledOrExpiredOrder(t *testing.T) {
	for _, expire := range []bool{false, true} {
		order := acceptedOrder(t, Config{})
		if err := order.Fill(testFill(testTrade1, "60000", "1", 3)); err != nil {
			t.Fatal(err)
		}
		if expire {
			if err := order.Expire(4); err != nil {
				t.Fatal(err)
			}
		} else if err := order.Cancel(4); err != nil {
			t.Fatal(err)
		}
		before := order.Status()
		if err := order.VoidFill(FillVoid{TradeID: testTrade1, Quantity: decimal.MustQuantity("20000"), Reopened: true, Timestamp: 5}); err != nil {
			t.Fatal(err)
		}
		requireStatus(t, order, before)
		requireQuantity(t, order.FilledQuantity(), "40000")
		requireQuantity(t, order.VoidedQuantity(), "20000")
		if !order.IsClosed() || order.TimestampClosed() == 0 {
			t.Fatal("terminal order reopened after fill void")
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/mod.rs:1924
//	test: test_fill_void_rejects_duplicate_stale_and_over_void
func TestFillVoidRejectsDuplicateStaleAndOverVoid(t *testing.T) {
	order := acceptedOrder(t, Config{})
	if err := order.Fill(testFill(testTrade1, "100000", "1", 3)); err != nil {
		t.Fatal(err)
	}
	if err := order.VoidFill(FillVoid{TradeID: testTrade1, Quantity: decimal.MustQuantity("40000"), Timestamp: 4}); err != nil {
		t.Fatal(err)
	}
	requireErrorKindTrade(t, order.VoidFill(FillVoid{TradeID: testTrade1, Quantity: decimal.MustQuantity("40000")}), ErrorDuplicateFillVoid, testTrade1)
	requireErrorKindTrade(t, order.VoidFill(FillVoid{TradeID: testTrade1, Quantity: decimal.MustQuantity("30000")}), ErrorStaleFillVoid, testTrade1)
	requireErrorKindTrade(t, order.VoidFill(FillVoid{TradeID: testTrade1, Quantity: decimal.MustQuantity("100001")}), ErrorOverVoid, testTrade1)
	requireQuantity(t, order.FilledQuantity(), "60000")
	requireQuantity(t, order.VoidedQuantity(), "40000")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/mod.rs:1986
//	test: test_unknown_fill_void_does_not_reverse_surviving_fill
func TestUnknownFillVoidDoesNotReverseSurvivingFill(t *testing.T) {
	order := acceptedOrder(t, Config{})
	if err := order.Fill(testFill(testTrade1, "60000", "1", 3)); err != nil {
		t.Fatal(err)
	}
	if err := order.VoidFill(FillVoid{TradeID: testTrade2, Quantity: decimal.MustQuantity("40000"), Timestamp: 4}); err != nil {
		t.Fatal(err)
	}
	if err := order.VoidFill(FillVoid{TradeID: testTrade1, Quantity: decimal.MustQuantity("10000"), Reopened: true, Timestamp: 5}); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, order, OrderStatusVoided)
	requireQuantity(t, order.FilledQuantity(), "50000")
	requireQuantity(t, order.VoidedQuantity(), "50000")
	requireQuantity(t, order.LeavesQuantity(), "0")
	if !order.IsClosed() {
		t.Fatal("unknown terminal void did not close order")
	}
	if err := order.Fill(testFill(ids.MustTradeID("3"), "1", "1", 6)); err == nil {
		t.Fatal("voided order accepted late fill")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/mod.rs:2032
//	test: test_order_state_transition_to_canceled
func TestOrderStateTransitionToCanceled(t *testing.T) {
	order := testOrder(t, Config{})
	if err := order.Submit(testAccount, 1); err != nil {
		t.Fatal(err)
	}
	if err := order.Cancel(2); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, order, OrderStatusCanceled)
	if !order.IsClosed() || order.IsOpen() || order.TimestampClosed() != 2 {
		t.Fatal("canceled order is not closed at cancellation timestamp")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/mod.rs:2046
//	test: test_order_life_cycle_to_partially_filled
func TestOrderLifeCycleToPartiallyFilled(t *testing.T) {
	order := acceptedOrder(t, Config{})
	if err := order.Fill(testFill(testTrade1, "50000", "1", 3)); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, order, OrderStatusPartiallyFilled)
	requireQuantity(t, order.FilledQuantity(), "50000")
	requireQuantity(t, order.LeavesQuantity(), "50000")
	if !order.IsOpen() || order.IsClosed() {
		t.Fatal("partially filled order openness differs")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/mod.rs:2068
//	test: test_order_commission_calculation
func TestOrderCommissionCalculation(t *testing.T) {
	order := testOrder(t, Config{})
	order.commissions[currency.USD().Code] = money.MustNew("10", currency.USD())
	values := order.Commissions()
	commission, ok := order.Commission(currency.USD())
	if !ok || !commission.Equal(money.MustNew("10", currency.USD())) ||
		len(values) != 1 || !values[0].Equal(money.MustNew("10", currency.USD())) {
		t.Fatalf("commissions = %v", values)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/mod.rs:2085
//	test: test_order_is_primary
func TestOrderIsPrimary(t *testing.T) {
	orderID := ids.MustClientOrderID("O-1")
	algorithm := ids.MustExecAlgorithmID("ALG")
	if !testOrder(t, Config{ClientOrderID: orderID, ExecAlgorithmID: &algorithm, ExecSpawnID: &orderID}).IsPrimary() {
		t.Fatal("primary algorithm order not classified as primary")
	}
	if testOrder(t, Config{ClientOrderID: orderID, ExecAlgorithmID: &algorithm, ExecSpawnID: &orderID}).IsSpawned() {
		t.Fatal("primary algorithm order classified as spawned")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/mod.rs:2099
//	test: test_order_is_spawned
func TestOrderIsSpawned(t *testing.T) {
	orderID := ids.MustClientOrderID("O-1")
	spawnID := ids.MustClientOrderID("O-2")
	algorithm := ids.MustExecAlgorithmID("ALG")
	order := testOrder(t, Config{ClientOrderID: orderID, ExecAlgorithmID: &algorithm, ExecSpawnID: &spawnID})
	if order.IsPrimary() || !order.IsSpawned() {
		t.Fatal("spawned algorithm order not classified as spawned")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/mod.rs:2113
//	test: test_order_is_contingency
func TestOrderIsContingency(t *testing.T) {
	order := testOrder(t, Config{ContingencyType: ContingencyTypeOCO})
	if !order.IsContingency() || !order.IsParentOrder() || order.IsChildOrder() {
		t.Fatal("OCO parent order classification differs")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/mod.rs:2126
//	test: test_order_is_child_order
func TestOrderIsChildOrder(t *testing.T) {
	parent := ids.MustClientOrderID("P-1")
	order := testOrder(t, Config{ParentOrderID: &parent})
	if !order.IsChildOrder() || order.IsParentOrder() {
		t.Fatal("parent ID did not classify child order")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/mod.rs:2138
//	test: test_to_own_book_order_timestamp_ordering
func TestToOwnBookOrderTimestampOrdering(t *testing.T) {
	order := testOrder(t, Config{})
	if err := order.Submit(testAccount, 1_000_000); err != nil {
		t.Fatal(err)
	}
	if err := order.Accept(testAccount, testVenue, 2_000_000); err != nil {
		t.Fatal(err)
	}
	book := order.ToOwnBookOrder()
	if book.TimestampSubmitted != 1_000_000 || book.TimestampAccepted != 2_000_000 || book.TimestampLast != 2_000_000 {
		t.Fatalf("own-book timestamps = %+v", book)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/mod.rs:2165
//	test: test_to_own_book_order_partial_fill_uses_leaves_qty
func TestToOwnBookOrderPartialFillUsesLeavesQuantity(t *testing.T) {
	order := acceptedOrder(t, Config{})
	if err := order.Fill(testFill(testTrade1, "40000", "1", 3)); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, order, OrderStatusPartiallyFilled)
	requireQuantity(t, order.LeavesQuantity(), "60000")
	requireQuantity(t, order.ToOwnBookOrder().Size, "60000")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/mod.rs:2193
//	test: test_order_accepted_without_submitted_sets_account_id
func TestOrderAcceptedWithoutSubmittedSetsAccountID(t *testing.T) {
	external := ids.MustAccountID("EXTERNAL-001")
	order := testOrder(t, Config{})
	if order.AccountID() != nil {
		t.Fatal("new order unexpectedly has account ID")
	}
	if err := order.Accept(external, testVenue, 2); err != nil {
		t.Fatal(err)
	}
	if order.AccountID() == nil || *order.AccountID() != external {
		t.Fatalf("account ID = %v", order.AccountID())
	}
	requireStatus(t, order, OrderStatusAccepted)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/mod.rs:2214
//	test: test_order_accepted_after_submitted_preserves_account_id
func TestOrderAcceptedAfterSubmittedPreservesAccountID(t *testing.T) {
	order := testOrder(t, Config{})
	submittedAccount := ids.MustAccountID("SUBMITTED-001")
	acceptedAccount := ids.MustAccountID("ACCEPTED-001")
	if err := order.Submit(submittedAccount, 1); err != nil {
		t.Fatal(err)
	}
	if order.AccountID() == nil || *order.AccountID() != submittedAccount {
		t.Fatalf("submitted account ID = %v", order.AccountID())
	}
	if err := order.Accept(acceptedAccount, testVenue, 2); err != nil {
		t.Fatal(err)
	}
	if order.AccountID() == nil || *order.AccountID() != acceptedAccount {
		t.Fatalf("account ID = %v", order.AccountID())
	}
	requireStatus(t, order, OrderStatusAccepted)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/mod.rs:2239
//	test: test_overfill_tracks_overfill_qty
func TestOverfillTracksOverfillQuantity(t *testing.T) {
	order := acceptedOrder(t, Config{})
	if err := order.Fill(testFill(testTrade1, "110000", "1", 3)); err != nil {
		t.Fatal(err)
	}
	requireQuantity(t, order.OverfillQuantity(), "10000")
	requireQuantity(t, order.FilledQuantity(), "110000")
	requireQuantity(t, order.LeavesQuantity(), "0")
	requireStatus(t, order, OrderStatusFilled)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/mod.rs:2263
//	test: test_partial_fill_then_overfill
func TestPartialFillThenOverfill(t *testing.T) {
	order := acceptedOrder(t, Config{})
	if err := order.Fill(testFill(testTrade1, "80000", "1", 3)); err != nil {
		t.Fatal(err)
	}
	requireQuantity(t, order.OverfillQuantity(), "0")
	requireQuantity(t, order.FilledQuantity(), "80000")
	requireQuantity(t, order.LeavesQuantity(), "20000")
	if err := order.Fill(testFill(testTrade2, "30000", "1", 4)); err != nil {
		t.Fatal(err)
	}
	requireQuantity(t, order.OverfillQuantity(), "10000")
	requireQuantity(t, order.FilledQuantity(), "110000")
	requireQuantity(t, order.LeavesQuantity(), "0")
	requireStatus(t, order, OrderStatusFilled)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/mod.rs:2299
//	test: test_exact_fill_no_overfill
func TestExactFillNoOverfill(t *testing.T) {
	order := acceptedOrder(t, Config{})
	if err := order.Fill(testFill(testTrade1, "100000", "1", 3)); err != nil {
		t.Fatal(err)
	}
	requireQuantity(t, order.OverfillQuantity(), "0")
	requireQuantity(t, order.FilledQuantity(), "100000")
	requireQuantity(t, order.LeavesQuantity(), "0")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/mod.rs:2322
//	test: test_partial_fill_then_overfill_with_fractional_quantities
func TestPartialFillThenOverfillWithFractionalQuantities(t *testing.T) {
	order := acceptedOrder(t, Config{Quantity: decimal.MustQuantity("2450.5")})
	if err := order.Fill(testFill(testTrade1, "1202.5", "1", 3)); err != nil {
		t.Fatal(err)
	}
	requireQuantity(t, order.OverfillQuantity(), "0")
	requireQuantity(t, order.FilledQuantity(), "1202.5")
	requireQuantity(t, order.LeavesQuantity(), "1248.0")
	requireStatus(t, order, OrderStatusPartiallyFilled)
	if err := order.Fill(testFill(testTrade2, "1285.5", "1", 4)); err != nil {
		t.Fatal(err)
	}
	requireQuantity(t, order.OverfillQuantity(), "37.5")
	requireQuantity(t, order.FilledQuantity(), "2488.0")
	requireQuantity(t, order.LeavesQuantity(), "0")
	requireStatus(t, order, OrderStatusFilled)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/mod.rs:2361
//	test: test_calculate_overfill_returns_zero_when_no_overfill
func TestCalculateOverfillReturnsZeroWhenNoOverfill(t *testing.T) {
	order := testOrder(t, Config{})
	requireQuantity(t, order.CalculateOverfill(decimal.MustQuantity("50000")), "0")
	requireQuantity(t, order.CalculateOverfill(decimal.MustQuantity("100000")), "0")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/mod.rs:2378
//	test: test_calculate_overfill_returns_overfill_amount
func TestCalculateOverfillReturnsOverfillAmount(t *testing.T) {
	order := testOrder(t, Config{})
	requireQuantity(t, order.CalculateOverfill(decimal.MustQuantity("110000")), "10000")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/mod.rs:2391
//	test: test_calculate_overfill_accounts_for_existing_fills
func TestCalculateOverfillAccountsForExistingFills(t *testing.T) {
	order := acceptedOrder(t, Config{})
	if err := order.Fill(testFill(testTrade1, "60000", "1", 3)); err != nil {
		t.Fatal(err)
	}
	requireQuantity(t, order.CalculateOverfill(decimal.MustQuantity("50000")), "10000")
	requireQuantity(t, order.CalculateOverfill(decimal.MustQuantity("40000")), "0")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/mod.rs:2417
//	test: test_calculate_overfill_with_fractional_quantities
func TestCalculateOverfillWithFractionalQuantities(t *testing.T) {
	order := testOrder(t, Config{Quantity: decimal.MustQuantity("2450.5")})
	requireQuantity(t, order.CalculateOverfill(decimal.MustQuantity("2488.0")), "37.5")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/mod.rs:2431
//	test: test_calculate_overfill_zero_after_fractional_partial_fill
func TestCalculateOverfillZeroAfterFractionalPartialFill(t *testing.T) {
	order := acceptedOrder(t, Config{Quantity: decimal.MustQuantity("1.000")})
	if err := order.Fill(testFill(testTrade1, "0.072", "1", 3)); err != nil {
		t.Fatal(err)
	}
	requireQuantity(t, order.CalculateOverfill(decimal.MustQuantity("0.072")), "0.000")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/mod.rs:2452
//	test: test_duplicate_fill_rejected
func TestDuplicateFillRejected(t *testing.T) {
	order := acceptedOrder(t, Config{})
	if err := order.Fill(testFill(testTrade1, "50000", "1", 3)); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, order, OrderStatusPartiallyFilled)
	requireErrorKindTrade(t, order.Fill(testFill(testTrade1, "50000", "1", 4)), ErrorDuplicateFill, testTrade1)
	requireQuantity(t, order.FilledQuantity(), "50000")
	requireStatus(t, order, OrderStatusPartiallyFilled)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/mod.rs:2492
//	test: test_check_display_qty_returns_typed_invariant_with_stable_display
func TestCheckDisplayQuantityReturnsTypedInvariantWithStableDisplay(t *testing.T) {
	display := decimal.MustQuantity("2")
	err := CheckDisplayQuantity(&display, decimal.MustQuantity("1"))
	requireErrorKind(t, err, ErrorInvariant)
	if err.Error() != "`display_qty` may not exceed `quantity`" {
		t.Fatalf("error = %q", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/mod.rs:2506
//	test: test_check_time_in_force_returns_typed_invariant_with_stable_display
func TestCheckTimeInForceReturnsTypedInvariantWithStableDisplay(t *testing.T) {
	err := CheckTimeInForce(TimeInForceGTD, nil)
	requireErrorKind(t, err, ErrorInvariant)
	if err.Error() != "`expire_time` is required for `GTD` order" {
		t.Fatalf("error = %q", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/mod.rs:2523
//	test: test_different_trade_ids_allowed
func TestDifferentTradeIDsAllowed(t *testing.T) {
	order := acceptedOrder(t, Config{})
	if err := order.Fill(testFill(testTrade1, "50000", "1", 3)); err != nil {
		t.Fatal(err)
	}
	if err := order.Fill(testFill(testTrade2, "50000", "1", 4)); err != nil {
		t.Fatal(err)
	}
	if len(order.TradeIDs()) != 2 {
		t.Fatalf("trade IDs = %v", order.TradeIDs())
	}
	requireQuantity(t, order.FilledQuantity(), "100000")
	requireStatus(t, order, OrderStatusFilled)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/mod.rs:2551
//	test: test_pending_update_order_restores_status_on_updated
func TestPendingUpdateOrderRestoresStatusOnUpdated(t *testing.T) {
	order := acceptedOrder(t, Config{})
	if err := order.PendingUpdate(3); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, order, OrderStatusPendingUpdate)
	if err := order.Update(Update{Quantity: copyPointer(decimal.MustQuantity("50000")), Timestamp: 4}); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, order, OrderStatusAccepted)
	requireQuantity(t, order.Quantity(), "50000")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/mod.rs:2580
//	test: test_partially_filled_order_can_be_updated
func TestPartiallyFilledOrderCanBeUpdated(t *testing.T) {
	order := acceptedOrder(t, Config{})
	if err := order.Fill(testFill(testTrade1, "40000", "1", 3)); err != nil {
		t.Fatal(err)
	}
	if err := order.Update(Update{Quantity: copyPointer(decimal.MustQuantity("80000")), Timestamp: 4}); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, order, OrderStatusPartiallyFilled)
	requireQuantity(t, order.Quantity(), "80000")
	requireQuantity(t, order.LeavesQuantity(), "40000")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/mod.rs:2611
//	test: test_triggered_order_can_be_updated
func TestTriggeredOrderCanBeUpdated(t *testing.T) {
	order := acceptedOrder(t, Config{Type: OrderTypeStopLimit})
	if err := order.Trigger(3); err != nil {
		t.Fatal(err)
	}
	if err := order.Update(Update{Quantity: copyPointer(decimal.MustQuantity("80000")), Timestamp: 4}); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, order, OrderStatusTriggered)
	requireQuantity(t, order.Quantity(), "80000")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/mod.rs:2642
//	test: test_order_updated_with_is_quote_quantity_clears_flag
func TestOrderUpdatedWithIsQuoteQuantityClearsFlag(t *testing.T) {
	order := acceptedOrder(t, Config{QuoteQuantity: true, Quantity: decimal.MustQuantity("10.000000")})
	if err := order.Update(Update{Quantity: copyPointer(decimal.MustQuantity("47.393365")), IsQuoteQuantity: false, Timestamp: 3}); err != nil {
		t.Fatal(err)
	}
	if order.IsQuoteQuantity() {
		t.Fatal("quote quantity flag remained set")
	}
	requireQuantity(t, order.Quantity(), "47.393365")
	requireQuantity(t, order.LeavesQuantity(), "47.393365")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/mod.rs:2667
//	test: test_order_updated_default_is_quote_quantity_clears_flag
func TestOrderUpdatedDefaultIsQuoteQuantityClearsFlag(t *testing.T) {
	order := acceptedOrder(t, Config{QuoteQuantity: true, Quantity: decimal.MustQuantity("10.000000")})
	if err := order.Update(Update{Quantity: copyPointer(decimal.MustQuantity("8.000000")), Timestamp: 3}); err != nil {
		t.Fatal(err)
	}
	if order.IsQuoteQuantity() {
		t.Fatal("default update did not clear quote quantity flag")
	}
	requireQuantity(t, order.Quantity(), "8.000000")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/mod.rs:2691
//	test: test_canceled_then_partial_fill_then_canceled
func TestCanceledThenPartialFillThenCanceled(t *testing.T) {
	order := acceptedOrder(t, Config{})
	if err := order.Cancel(3); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, order, OrderStatusCanceled)
	if !order.IsClosed() {
		t.Fatal("canceled order is not closed")
	}
	if err := order.Fill(testFill(testTrade1, "50000", "1", 4)); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, order, OrderStatusPartiallyFilled)
	requireQuantity(t, order.FilledQuantity(), "50000")
	if !order.IsOpen() {
		t.Fatal("late partial fill did not reopen order")
	}
	if err := order.Cancel(5); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, order, OrderStatusCanceled)
	if !order.IsClosed() {
		t.Fatal("re-emitted cancel did not close order")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/mod.rs:2721
//	test: test_updated_restores_status_before_pending_cancel
func TestUpdatedRestoresStatusBeforePendingCancel(t *testing.T) {
	order := acceptedOrder(t, Config{})
	acceptedTimestamp := order.TimestampAccepted()
	venueIDs := order.VenueOrderIDs()
	if err := order.PendingCancel(3); err != nil {
		t.Fatal(err)
	}
	if err := order.Update(Update{Quantity: copyPointer(decimal.MustQuantity("150000")), Timestamp: 4}); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, order, OrderStatusAccepted)
	requireQuantity(t, order.Quantity(), "150000")
	if order.TimestampAccepted() != acceptedTimestamp || len(order.VenueOrderIDs()) != len(venueIDs) ||
		order.VenueOrderIDs()[0] != venueIDs[0] {
		t.Fatal("pending-cancel update changed accepted identity")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/mod.rs:2754
//	test: test_apply_triggered_to_stop_market_order_returns_error
func TestApplyTriggeredToStopMarketOrderReturnsError(t *testing.T) {
	order := acceptedOrder(t, Config{Type: OrderTypeStopMarket})
	requireErrorKind(t, order.Trigger(3), ErrorInvalidStateTransition)
	requireStatus(t, order, OrderStatusAccepted)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/mod.rs:2775
//	test: test_apply_triggered_to_stop_limit_order_succeeds
func TestApplyTriggeredToStopLimitOrderSucceeds(t *testing.T) {
	order := acceptedOrder(t, Config{Type: OrderTypeStopLimit})
	if err := order.Trigger(3); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, order, OrderStatusTriggered)
}
