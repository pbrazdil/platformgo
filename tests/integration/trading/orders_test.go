package trading

import (
	"strings"
	"testing"
)

func limitBuy(intent, quantity, price string) orderCommand {
	return orderCommand{
		AccountID: "acct-1", UserID: "user-1", IntentID: intent,
		Symbol: "BTC-PERP", Side: "buy", OrderType: "LIMIT",
		Quantity: quantity, LimitPrice: price, TimeInForce: "GTC",
	}
}

func requireOrderError(t *testing.T, err error, code, messagePart string) {
	t.Helper()
	orderErr, ok := err.(*orderFixtureError)
	if !ok || orderErr.Code != code || !strings.Contains(orderErr.Message, messagePart) {
		t.Fatalf("error=%#v, want code=%q containing %q", err, code, messagePart)
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/trading/e2e_orders.rs:28
//	test: order_view_carries_type_prices_owner_and_fee_fields
func TestOrderViewCarriesTypePricesOwnerAndFeeFields(t *testing.T) {
	fixture := newOrderFixture()
	order, err := fixture.submit(limitBuy("limit-1", "0.010", "60000"))
	if err != nil {
		t.Fatal(err)
	}
	fixture.applyFill("limit-1", "of-1", "pos-1", "0.010", "60000", "3.5", 1_700_000_000_123)
	view := fixture.view("limit-1")
	if view.OrderType != "LIMIT" || view.LimitPrice != "60000" || view.TriggerPrice != "" ||
		view.BracketGroupID != "" || view.ReduceOnly || view.ProductType != "perp" {
		t.Fatalf("order discriminators=%#v", view)
	}
	if view.AccountID != "acct-1" || view.UserID != "user-1" ||
		view.TradingFeeRate != "0.0005" || view.CumulativeTradingFees != "3.5" ||
		view.CumulativeQuote != "600" || view.PositionID != positionURN("pos-1") ||
		view.FilledAt == "" || view.ID != orderURN(order.ID) {
		t.Fatalf("order ownership/fills=%#v", view)
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/trading/e2e_orders.rs:202
//	test: order_view_carries_parity4_discriminators
func TestOrderViewCarriesParity4Discriminators(t *testing.T) {
	fixture := newOrderFixture()
	stop := limitBuy("stop-1", "0.010", "")
	stop.OrderType, stop.TriggerPrice = "STOP_MARKET", "70000"
	if _, err := fixture.submit(stop); err != nil {
		t.Fatal(err)
	}
	fixture.markWorking("stop-1")
	if !fixture.markTriggered("stop-1") || fixture.markTriggered("stop-1") {
		t.Fatal("trigger timestamp was not edge-triggered")
	}
	stopout := limitBuy("stopout:100001:BTC-PERP", "0.010", "")
	stopout.Side, stopout.OrderType, stopout.ReduceOnly = "sell", "MARKET", true
	if _, err := fixture.submit(stopout); err != nil {
		t.Fatal(err)
	}
	fixture.insertBracket("bracket-1",
		bracketLegSeed{ID: "br-entry-id", IntentID: "br:entry", Side: "BUY", OrderType: "LIMIT", Quantity: "0.010", LimitPrice: "60000", BracketLeg: "entry"},
		[]bracketLegSeed{
			{ID: "br-tp-id", IntentID: "br:tp", Side: "SELL", OrderType: "LIMIT", Quantity: "0.010", LimitPrice: "65000", BracketLeg: "take_profit"},
			{ID: "br-sl-id", IntentID: "br:sl", Side: "SELL", OrderType: "STOP_MARKET", Quantity: "0.010", TriggerPrice: "55000", BracketLeg: "stop_loss"},
		})
	stopView := fixture.view("stop-1")
	if stopView.TriggeredAtMS == nil || stopView.Liquidation || stopView.BracketLeg != "" {
		t.Fatalf("stop=%#v", stopView)
	}
	if !fixture.view("stopout:100001:BTC-PERP").Liquidation ||
		fixture.view("br:tp").BracketLeg != "take_profit" ||
		fixture.view("br:sl").BracketLeg != "stop_loss" ||
		fixture.view("br:tp").Liquidation {
		t.Fatalf("stopout=%#v tp=%#v sl=%#v", fixture.view("stopout:100001:BTC-PERP"), fixture.view("br:tp"), fixture.view("br:sl"))
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/trading/e2e_orders.rs:354
//	test: insert_bracket_batches_protective_legs_neutral_and_idempotent
func TestInsertBracketBatchesProtectiveLegsNeutralAndIdempotent(t *testing.T) {
	fixture := newOrderFixture()
	entry := bracketLegSeed{ID: "entry-id", IntentID: "bb:entry", Side: "BUY", OrderType: "LIMIT", Quantity: "0.030", LimitPrice: "60000", BracketLeg: "entry"}
	protective := []bracketLegSeed{
		{ID: "tp1-id", IntentID: "bb:tp1", Side: "SELL", OrderType: "LIMIT", Quantity: "0.010", LimitPrice: "61000", BracketLeg: "take_profit"},
		{ID: "tp2-id", IntentID: "bb:tp2", Side: "SELL", OrderType: "LIMIT", Quantity: "0.020", LimitPrice: "62000", BracketLeg: "take_profit"},
		{ID: "sl-id", IntentID: "bb:sl", Side: "SELL", OrderType: "STOP_MARKET", Quantity: "0.030", TriggerPrice: "55000", BracketLeg: "stop_loss"},
	}
	entryID, isNew := fixture.insertBracket("bracket-batch", entry, protective)
	if !isNew || entryID != entry.ID || len(fixture.orders) != 4 {
		t.Fatalf("entryID=%q isNew=%v orders=%d", entryID, isNew, len(fixture.orders))
	}
	e, tp1, tp2, sl := fixture.view("bb:entry"), fixture.view("bb:tp1"), fixture.view("bb:tp2"), fixture.view("bb:sl")
	if e.OrderType != "LIMIT" || e.LimitPrice != "60000" || e.ReduceOnly || e.BracketLeg == "" {
		t.Fatalf("entry=%#v", e)
	}
	if tp1.LimitPrice != "61000" || !tp1.ReduceOnly || tp1.BracketLeg != "take_profit" ||
		tp2.LimitPrice != "62000" || !tp2.ReduceOnly || tp2.BracketLeg != "take_profit" {
		t.Fatalf("tp1=%#v tp2=%#v", tp1, tp2)
	}
	if sl.OrderType != "STOP_MARKET" || sl.TriggerPrice != "55000" || sl.LimitPrice != "" ||
		!sl.ReduceOnly || sl.BracketLeg != "stop_loss" {
		t.Fatalf("sl=%#v", sl)
	}
	replayID, replayNew := fixture.insertBracket("bracket-batch", entry, protective)
	if replayNew || replayID != entryID || len(fixture.orders) != 4 {
		t.Fatalf("replayID=%q replayNew=%v orders=%d", replayID, replayNew, len(fixture.orders))
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/trading/e2e_orders.rs:529
//	test: trailing_stop_market_submits_persists_and_validates
func TestTrailingStopMarketSubmitsPersistsAndValidates(t *testing.T) {
	fixture := newOrderFixture()
	trailing := limitBuy("trail-1", "0.010", "")
	trailing.OrderType, trailing.Side = "TRAILING_STOP_MARKET", "sell"
	trailing.TriggerPrice, trailing.TrailingOffset = "65000", "500"
	if _, err := fixture.submit(trailing); err != nil {
		t.Fatal(err)
	}
	view := fixture.view("trail-1")
	if view.OrderType != "TRAILING_STOP_MARKET" || view.TriggerPrice != "65000" || view.TrailingOffset != "500" {
		t.Fatalf("trailing=%#v", view)
	}
	noOffset := trailing
	noOffset.IntentID, noOffset.TrailingOffset = "trail-no-offset", ""
	if _, err := fixture.submit(noOffset); err == nil {
		t.Fatal("trailing stop without offset succeeded")
	}
	marketOffset := trailing
	marketOffset.IntentID, marketOffset.OrderType, marketOffset.TriggerPrice = "market-with-offset", "MARKET", ""
	if _, err := fixture.submit(marketOffset); err == nil {
		t.Fatal("market order with trailing offset succeeded")
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/trading/e2e_order_caps.rs:63
//	test: submit_enforces_position_cap_and_max_notional
func TestSubmitEnforcesPositionCapAndMaxNotional(t *testing.T) {
	fixture := newOrderFixture()
	if _, err := fixture.submit(limitBuy("ok-1", "1", "100")); err != nil {
		t.Fatal(err)
	}
	_, err := fixture.submit(limitBuy("over-cap", "200", "1"))
	requireOrderError(t, err, "cap_exceeded", "position cap")
	_, err = fixture.submit(limitBuy("over-notional", "1", "20000000"))
	requireOrderError(t, err, "cap_exceeded", "notional cap")
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/trading/e2e_order_caps.rs:115
//	test: submit_rejects_quantity_that_rounds_to_zero
func TestSubmitRejectsQuantityThatRoundsToZero(t *testing.T) {
	fixture := newOrderFixture()
	fixture.instrument.SizeIncrement = "1"
	_, err := fixture.submit(limitBuy("dust", "0.4", "100"))
	requireOrderError(t, err, "min_size", "rounds to zero")
	if _, err := fixture.submit(limitBuy("step-ok", "1", "100")); err != nil {
		t.Fatal(err)
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/trading/e2e_order_caps.rs:208
//	test: submit_rejects_off_tick_price_and_off_step_qty
func TestSubmitRejectsOffTickPriceAndOffStepQty(t *testing.T) {
	fixture := newOrderFixture()
	fixture.instrument.SizeIncrement = "1"
	_, err := fixture.submit(limitBuy("off-tick", "1", "100.05"))
	requireOrderError(t, err, "precision_invalid", "price step")
	_, err = fixture.submit(limitBuy("off-step", "1.5", "100"))
	requireOrderError(t, err, "precision_invalid", "size step")
	if _, err := fixture.submit(limitBuy("aligned", "2", "100.1")); err != nil {
		t.Fatal(err)
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/trading/e2e_validation.rs:7
//	test: invalid_create_user_fails_validation_at_dispatch
func TestInvalidCreateUserFailsValidationAtDispatch(t *testing.T) {
	id, fields := createUser(createUserCommand{Email: "not-an-email", Password: "short"})
	if id != "" || fields["login"] == "" || fields["email"] == "" || fields["password"] == "" {
		t.Fatalf("id=%q fields=%#v", id, fields)
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/trading/e2e_validation.rs:46
//	test: valid_create_user_passes_validation
func TestValidCreateUserPassesValidation(t *testing.T) {
	id, fields := createUser(createUserCommand{
		Login: "validuser", Email: "valid@example.com", Password: "long-enough-password",
	})
	if id == "" || len(fields) != 0 {
		t.Fatalf("id=%q fields=%#v", id, fields)
	}
}
