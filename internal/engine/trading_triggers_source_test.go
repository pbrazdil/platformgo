package engine

import "testing"

// TestSourceRestingStopLimitsTriggerThenFill ports:
//   - source: apps/nautilus/tests/live/trading/e2e_stop_trigger.rs
//   - test: resting_stop_limits_trigger_then_fill
//   - pinned revision: 50141367492be46ebf5623f6191a14b94af2f2bd
//
// Observable contract: a stop-limit trigger is durable. Once crossed, the
// order remains triggered and may fill later even if the market moves back
// across the trigger.
func TestSourceRestingStopLimitsTriggerThenFill(t *testing.T) {
	fixture := newTradingFixture(t)
	orderID := fixture.id(700)
	order := fixture.submit(t, SubmitOrder{
		OrderID: orderID, AccountID: "account-1", InstrumentID: "BTC-PERP",
		Side: SideBuy, Type: OrderTypeStopLimit, TimeInForce: TimeInForceGTC,
		Quantity: "1", Price: "101", TriggerPrice: "105",
	})
	if order.Triggered {
		t.Fatal("stop-limit triggered before the adverse cross")
	}
	sellOrderID := fixture.id(701)
	sellOrder := fixture.submit(t, SubmitOrder{
		OrderID: sellOrderID, AccountID: "account-2", InstrumentID: "BTC-PERP",
		Side: SideSell, Type: OrderTypeStopLimit, TimeInForce: TimeInForceGTC,
		Quantity: "1", Price: "99", TriggerPrice: "95",
	})
	if sellOrder.Triggered {
		t.Fatal("sell stop-limit triggered before the adverse cross")
	}

	updateTriggerQuote(t, fixture, "104", "105")
	triggered, _ := fixture.state.Order(orderID)
	if !triggered.Triggered || triggered.TriggeredAt == 0 ||
		triggered.Status != OrderStatusWorking {
		t.Fatalf("trigger transition = %+v, want latched and working", triggered)
	}
	updateTriggerQuote(t, fixture, "94", "95")
	sellTriggered, _ := fixture.state.Order(sellOrderID)
	if !sellTriggered.Triggered || sellTriggered.TriggeredAt == 0 ||
		sellTriggered.Status != OrderStatusWorking {
		t.Fatalf("sell trigger transition = %+v, want latched and working", sellTriggered)
	}

	updateTriggerQuote(t, fixture, "99", "100")
	filled, _ := fixture.state.Order(orderID)
	sellFilled, _ := fixture.state.Order(sellOrderID)
	assertOrderStatus(t, filled, OrderStatusFilled)
	assertOrderStatus(t, sellFilled, OrderStatusFilled)
	if !filled.Triggered {
		t.Fatal("stop-limit lost its trigger latch")
	}
	if !sellFilled.Triggered {
		t.Fatal("sell stop-limit lost its trigger latch")
	}
}

// TestSourceTakeProfitMarketHoldsUntilFavorableTouchCross ports:
//   - source: apps/nautilus/tests/live/trading/e2e_touch_trigger.rs
//   - test: take_profit_market_holds_until_the_favorable_touch_cross
//   - pinned revision: 50141367492be46ebf5623f6191a14b94af2f2bd
func TestSourceTakeProfitMarketHoldsUntilFavorableTouchCross(t *testing.T) {
	fixture := newTradingFixture(t)
	open := fixture.submit(t, SubmitOrder{
		OrderID: fixture.id(710), AccountID: "account-1", InstrumentID: "BTC-PERP",
		Side: SideBuy, Type: OrderTypeMarket, TimeInForce: TimeInForceGTC,
		Quantity: "1",
	})
	assertOrderStatus(t, open, OrderStatusFilled)
	positionID := fixture.state.OpenPositions("account-1")[0].PositionID

	orderID := fixture.id(711)
	takeProfit := fixture.submit(t, SubmitOrder{
		OrderID: orderID, AccountID: "account-1", InstrumentID: "BTC-PERP",
		Side: SideSell, Type: OrderTypeTakeProfitMarket, TimeInForce: TimeInForceGTC,
		Quantity: "1", TriggerPrice: "110", ReduceOnly: true, PositionID: positionID,
	})
	assertOrderStatus(t, takeProfit, OrderStatusWorking)

	updateTriggerQuote(t, fixture, "109", "110")
	held, _ := fixture.state.Order(orderID)
	assertOrderStatus(t, held, OrderStatusWorking)

	updateTriggerQuote(t, fixture, "110", "111")
	filled, _ := fixture.state.Order(orderID)
	assertOrderStatus(t, filled, OrderStatusFilled)
	if !filled.Triggered {
		t.Fatal("take-profit fill was not stamped triggered")
	}
}

// TestSourceParkedTouchOrderCancels ports:
//   - source: apps/nautilus/tests/live/trading/e2e_touch_trigger.rs
//   - test: a_parked_touch_order_cancels_and_does_not_stay_working
//   - pinned revision: 50141367492be46ebf5623f6191a14b94af2f2bd
func TestSourceParkedTouchOrderCancels(t *testing.T) {
	fixture := newTradingFixture(t)
	open := fixture.submit(t, SubmitOrder{
		OrderID: fixture.id(720), AccountID: "account-1", InstrumentID: "BTC-PERP",
		Side: SideBuy, Type: OrderTypeMarket, TimeInForce: TimeInForceGTC,
		Quantity: "1",
	})
	assertOrderStatus(t, open, OrderStatusFilled)
	positionID := fixture.state.OpenPositions("account-1")[0].PositionID
	orderID := fixture.id(721)
	fixture.submit(t, SubmitOrder{
		OrderID: orderID, AccountID: "account-1", InstrumentID: "BTC-PERP",
		Side: SideSell, Type: OrderTypeTakeProfitMarket, TimeInForce: TimeInForceGTC,
		Quantity: "1", TriggerPrice: "110", ReduceOnly: true, PositionID: positionID,
	})
	fixture.apply(t, TradingAction{
		Kind:        TradingActionCancelOrder,
		CancelOrder: &CancelOrder{AccountID: "account-1", OrderID: orderID},
	})
	cancelled, _ := fixture.state.Order(orderID)
	assertOrderStatus(t, cancelled, OrderStatusCancelled)
	updateTriggerQuote(t, fixture, "120", "121")
	stillCancelled, _ := fixture.state.Order(orderID)
	assertOrderStatus(t, stillCancelled, OrderStatusCancelled)
}

// TestSourceTakeProfitLimitRestsAndFillsOnFavorableCross ports:
//   - source: apps/nautilus/tests/live/trading/e2e_touch_trigger.rs
//   - test: take_profit_limit_rests_and_fills_on_the_favorable_cross
//   - pinned revision: 50141367492be46ebf5623f6191a14b94af2f2bd
func TestSourceTakeProfitLimitRestsAndFillsOnFavorableCross(t *testing.T) {
	fixture := newTradingFixture(t)
	open := fixture.submit(t, SubmitOrder{
		OrderID: fixture.id(730), AccountID: "account-1", InstrumentID: "BTC-PERP",
		Side: SideBuy, Type: OrderTypeMarket, TimeInForce: TimeInForceGTC,
		Quantity: "1",
	})
	assertOrderStatus(t, open, OrderStatusFilled)
	positionID := fixture.state.OpenPositions("account-1")[0].PositionID
	orderID := fixture.id(731)
	fixture.submit(t, SubmitOrder{
		OrderID: orderID, AccountID: "account-1", InstrumentID: "BTC-PERP",
		Side: SideSell, Type: OrderTypeTakeProfitLimit, TimeInForce: TimeInForceGTC,
		Quantity: "1", Price: "110", TriggerPrice: "110",
		ReduceOnly: true, PositionID: positionID,
	})
	updateTriggerQuote(t, fixture, "110", "111")
	filled, _ := fixture.state.Order(orderID)
	assertOrderStatus(t, filled, OrderStatusFilled)
}

func updateTriggerQuote(
	t *testing.T,
	fixture *tradingFixture,
	bid string,
	ask string,
) {
	t.Helper()
	fixture.updateBook(t, bid,
		[]BookLevel{{Price: bid, Quantity: "100"}},
		[]BookLevel{{Price: ask, Quantity: "100"}},
	)
}
