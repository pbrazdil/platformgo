package engine

import "testing"

func TestBracketRejectsDuplicateChildIdentityAtomically(t *testing.T) {
	fixture := newTradingFixture(t)
	beforeOrders := fixture.state.Orders()
	ids := bracketIDs(fixture, 900)
	bracket := basicMarketBracket(ids, "account-1")
	bracket.StopLossOrderID = ids.tp1
	decision := fixture.applyDecision(t, TradingAction{
		Kind: TradingActionPlaceBracket, PlaceBracket: &bracket,
	})
	if decision.CommandResult.Status != CommandStatusRejected ||
		decision.CommandResult.Reason != RejectionInvalidOrder {
		t.Fatalf("duplicate child result = %+v", decision.CommandResult)
	}
	if after := fixture.state.Orders(); len(after) != len(beforeOrders) {
		t.Fatalf("rejected bracket created orders: before=%d after=%d", len(beforeOrders), len(after))
	}
}

func TestPartiallyFilledEntryExpandsProtectionAsMoreEntryFills(t *testing.T) {
	fixture := newTradingFixture(t)
	ids := bracketIDs(fixture, 910)
	fixture.placeBracket(t, PlaceBracket{
		BracketID: ids.bracket, EntryOrderID: ids.entry,
		AccountID: "account-1", InstrumentID: "BTC-PERP",
		Side: SideBuy, EntryType: OrderTypeLimit, TimeInForce: TimeInForceGTC,
		Quantity: "20", EntryPrice: "100",
		TakeProfits: []ProtectiveLeg{
			{OrderID: ids.tp1, Price: "110", Quantity: "10"},
			{OrderID: ids.tp2, Price: "120", Quantity: "10"},
		},
		StopLossOrderID: ids.stop, StopLoss: "80",
	})
	entry, _ := fixture.state.Order(ids.entry)
	stop, _ := fixture.state.Order(ids.stop)
	assertOrderStatus(t, entry, OrderStatusPartiallyFilled)
	if entry.FilledQuantity != "10" || stop.Quantity != "10" {
		t.Fatalf("initial partial entry=%s stop=%s, want 10", entry.FilledQuantity, stop.Quantity)
	}

	fixture.updateBook(t, "100",
		[]BookLevel{{Price: "99", Quantity: "10"}},
		[]BookLevel{{Price: "100", Quantity: "10"}},
	)
	entry, _ = fixture.state.Order(ids.entry)
	stop, _ = fixture.state.Order(ids.stop)
	assertOrderStatus(t, entry, OrderStatusFilled)
	if stop.Quantity != "20" {
		t.Fatalf("expanded stop quantity = %s, want 20", stop.Quantity)
	}
}

func TestBracketBusinessIdentityCannotBeReused(t *testing.T) {
	fixture := newTradingFixture(t)
	firstIDs := bracketIDs(fixture, 920)
	fixture.placeBracket(t, basicMarketBracket(firstIDs, "account-1"))
	secondIDs := bracketIDs(fixture, 930)
	second := basicMarketBracket(secondIDs, "account-1")
	second.BracketID = firstIDs.bracket
	before := len(fixture.state.Orders())
	decision := fixture.applyDecision(t, TradingAction{
		Kind: TradingActionPlaceBracket, PlaceBracket: &second,
	})
	if decision.CommandResult.Status != CommandStatusRejected {
		t.Fatalf("reused bracket ID result = %+v", decision.CommandResult)
	}
	if after := len(fixture.state.Orders()); after != before {
		t.Fatalf("reused bracket ID mutated orders: before=%d after=%d", before, after)
	}
}

func TestShortBracketProtectionUsesBuySideTriggerDirections(t *testing.T) {
	fixture := newTradingFixture(t)
	ids := bracketIDs(fixture, 940)
	bracket := basicMarketBracket(ids, "account-1")
	bracket.Side = SideSell
	bracket.TakeProfits[0].Price = "90"
	bracket.StopLoss = "110"
	fixture.placeBracket(t, bracket)

	takeProfit, _ := fixture.state.Order(ids.tp1)
	stop, _ := fixture.state.Order(ids.stop)
	if takeProfit.Side != SideBuy || stop.Side != SideBuy {
		t.Fatalf("short protection sides = tp %s stop %s, want BUY", takeProfit.Side, stop.Side)
	}
	updateTriggerQuote(t, fixture, "89", "90")
	takeProfit, _ = fixture.state.Order(ids.tp1)
	stop, _ = fixture.state.Order(ids.stop)
	assertOrderStatus(t, takeProfit, OrderStatusFilled)
	assertOrderStatus(t, stop, OrderStatusCancelled)
	if got := fixture.state.OpenPositions("account-1"); len(got) != 0 {
		t.Fatalf("short position after TP = %+v, want flat", got)
	}
}

func TestCancellingUnfilledBracketEntryCancelsHeldProtection(t *testing.T) {
	fixture := newTradingFixture(t)
	ids := bracketIDs(fixture, 950)
	fixture.placeBracket(t, PlaceBracket{
		BracketID: ids.bracket, EntryOrderID: ids.entry,
		AccountID: "account-1", InstrumentID: "BTC-PERP",
		Side: SideBuy, EntryType: OrderTypeLimit, TimeInForce: TimeInForceGTC,
		Quantity: "1", EntryPrice: "90",
		TakeProfits: []ProtectiveLeg{{
			OrderID: ids.tp1, Price: "110", Quantity: "1",
		}},
		StopLossOrderID: ids.stop, StopLoss: "80",
	})
	fixture.apply(t, TradingAction{
		Kind: TradingActionCancelOrder,
		CancelOrder: &CancelOrder{
			AccountID: "account-1", OrderID: ids.entry,
		},
	})
	for _, id := range []ID{ids.entry, ids.tp1, ids.stop} {
		order, _ := fixture.state.Order(id)
		assertOrderStatus(t, order, OrderStatusCancelled)
	}
}

func TestNonMarketableBookUpdatesDoNotMutateRestingOrderVersion(t *testing.T) {
	fixture := newTradingFixture(t)
	orderID := fixture.id(960)
	order := fixture.submit(t, SubmitOrder{
		OrderID: orderID, AccountID: "account-1", InstrumentID: "BTC-PERP",
		Side: SideBuy, Type: OrderTypeLimit, TimeInForce: TimeInForceGTC,
		Quantity: "1", Price: "90",
	})
	beforeVersion := order.Version
	decision := fixture.updateBook(t, "100",
		[]BookLevel{{Price: "99", Quantity: "10"}},
		[]BookLevel{{Price: "100", Quantity: "10"}},
	)
	order, _ = fixture.state.Order(orderID)
	if order.Version != beforeVersion {
		t.Fatalf("resting order version = %d, want unchanged %d", order.Version, beforeVersion)
	}
	for _, change := range decision.OrderChanges {
		if change.OrderID == orderID {
			t.Fatalf("non-transitioning order emitted change %+v", change)
		}
	}
}

func TestExternalFlattenCancelsPositionBoundProtection(t *testing.T) {
	fixture := newTradingFixture(t)
	ids := bracketIDs(fixture, 1_000)
	fixture.placeBracket(t, basicMarketBracket(ids, "account-1"))
	positionID := fixture.state.OpenPositions("account-1")[0].PositionID
	fixture.submit(t, SubmitOrder{
		OrderID: fixture.id(1_010), AccountID: "account-1",
		InstrumentID: "BTC-PERP", Side: SideSell,
		Type: OrderTypeMarket, TimeInForce: TimeInForceGTC,
		Quantity: "1", ReduceOnly: true, PositionID: positionID,
	})
	for _, id := range []ID{ids.tp1, ids.stop} {
		order, _ := fixture.state.Order(id)
		assertOrderStatus(t, order, OrderStatusCancelled)
	}
}

func TestExternalReversalCancelsOldDirectionProtection(t *testing.T) {
	fixture := newTradingFixture(t)
	ids := bracketIDs(fixture, 1_020)
	fixture.placeBracket(t, basicMarketBracket(ids, "account-1"))
	fixture.submit(t, SubmitOrder{
		OrderID: fixture.id(1_030), AccountID: "account-1",
		InstrumentID: "BTC-PERP", Side: SideSell,
		Type: OrderTypeMarket, TimeInForce: TimeInForceGTC,
		Quantity: "2",
	})
	positions := fixture.state.OpenPositions("account-1")
	if len(positions) != 1 ||
		positions[0].Side != PositionSideShort ||
		positions[0].SignedQuantity != "-1" {
		t.Fatalf("reversed position = %+v", positions)
	}
	for _, id := range []ID{ids.tp1, ids.stop} {
		order, _ := fixture.state.Order(id)
		assertOrderStatus(t, order, OrderStatusCancelled)
	}
}
