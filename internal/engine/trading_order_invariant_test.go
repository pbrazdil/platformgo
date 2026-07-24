package engine

import (
	"errors"
	"testing"
)

func TestTradingDuplicateReturnsSameEconomicDecisionWithoutAnotherFill(t *testing.T) {
	fixture := newTradingFixture(t)
	order := fixture.submit(t, SubmitOrder{
		OrderID:      fixture.id(100),
		AccountID:    "account-1",
		InstrumentID: "BTC-PERP",
		Side:         SideBuy,
		Type:         OrderTypeMarket,
		TimeInForce:  TimeInForceGTC,
		Quantity:     "0.001",
	})
	beforeHash := fixture.state.Hash()
	recorded := cloneDecision(fixture.lastDecision)

	next, duplicate, err := ApplyTrading(
		fixture.state,
		fixture.lastInput,
		fixture.lastAction,
	)
	if err != nil {
		t.Fatalf("duplicate ApplyTrading: %v", err)
	}
	if next.Hash() != beforeHash {
		t.Fatalf("duplicate state hash = %s, want unchanged %s", next.Hash(), beforeHash)
	}
	if !equalDecision(duplicate, recorded) {
		t.Fatalf("duplicate decision differs:\nrecorded:  %+v\nduplicate: %+v", recorded, duplicate)
	}
	if fills := next.FillsForOrder(order.OrderID); len(fills) != 1 {
		t.Fatalf("duplicate produced %d fills, want exactly one", len(fills))
	}
}

func TestTradingReturnedDecisionCannotMutateReceipt(t *testing.T) {
	fixture := newTradingFixture(t)
	fixture.submit(t, SubmitOrder{
		OrderID:      fixture.id(110),
		AccountID:    "account-1",
		InstrumentID: "BTC-PERP",
		Side:         SideBuy,
		Type:         OrderTypeMarket,
		TimeInForce:  TimeInForceGTC,
		Quantity:     "0.001",
	})
	returned := fixture.lastDecision
	returned.Fills[0].Quantity = "999"
	returned.OrderChanges[0].Status = OrderStatusCancelled

	_, duplicate, err := ApplyTrading(
		fixture.state,
		fixture.lastInput,
		fixture.lastAction,
	)
	if err != nil {
		t.Fatalf("duplicate ApplyTrading: %v", err)
	}
	if duplicate.Fills[0].Quantity != "0.001" ||
		duplicate.OrderChanges[0].Status != OrderStatusFilled {
		t.Fatalf("caller mutation reached stored receipt: %+v", duplicate)
	}
}

func TestTradingTypedPayloadMismatchHaltsReadiness(t *testing.T) {
	fixture := newTradingFixture(t)
	fixture.submit(t, SubmitOrder{
		OrderID:      fixture.id(120),
		AccountID:    "account-1",
		InstrumentID: "BTC-PERP",
		Side:         SideBuy,
		Type:         OrderTypeMarket,
		TimeInForce:  TimeInForceGTC,
		Quantity:     "0.001",
	})
	mismatched := fixture.lastAction
	mismatched.SubmitOrder = &SubmitOrder{
		OrderID:      fixture.id(120),
		AccountID:    "account-1",
		InstrumentID: "BTC-PERP",
		Side:         SideBuy,
		Type:         OrderTypeMarket,
		TimeInForce:  TimeInForceGTC,
		Quantity:     "0.002",
	}

	halted, _, err := ApplyTrading(fixture.state, fixture.lastInput, mismatched)
	if !errors.Is(err, ErrPayloadMismatch) {
		t.Fatalf("payload mismatch error = %v, want ErrPayloadMismatch", err)
	}
	if halted.Ready() {
		t.Fatal("payload mismatch left shard ready")
	}
	orderID := fixture.lastAction.SubmitOrder.OrderID
	if fills := halted.FillsForOrder(orderID); len(fills) != 1 {
		t.Fatalf("payload mismatch changed committed fills: %+v", fills)
	}
}

func TestTradingBusinessRejectionIsCommittedWithoutHalting(t *testing.T) {
	fixture := newTradingFixture(t)
	decision := fixture.applyDecision(t, TradingAction{
		Kind: TradingActionCancelOrder,
		CancelOrder: &CancelOrder{
			AccountID: "account-1",
			OrderID:   fixture.id(999),
		},
	})
	if decision.CommandResult.Status != CommandStatusRejected ||
		decision.CommandResult.Reason != RejectionOrderNotFound {
		t.Fatalf("command result = %+v, want rejected/order_not_found", decision.CommandResult)
	}
	if !fixture.state.Ready() {
		t.Fatal("business rejection halted shard readiness")
	}
	if fixture.state.NextStreamSequence() != fixture.sequence {
		t.Fatalf("next stream sequence = %d, want consumed rejection sequence %d", fixture.state.NextStreamSequence(), fixture.sequence)
	}

	duplicateState, duplicate, err := ApplyTrading(
		fixture.state,
		fixture.lastInput,
		fixture.lastAction,
	)
	if err != nil {
		t.Fatalf("duplicate rejected input: %v", err)
	}
	if duplicateState.Hash() != fixture.state.Hash() ||
		!equalDecision(duplicate, decision) {
		t.Fatal("duplicate rejected input did not return its recorded result")
	}
}

func TestTradingInvalidAndCrossAccountTransitionsCannotMutateOrder(t *testing.T) {
	fixture := newTradingFixture(t)
	orderID := fixture.id(130)
	working := fixture.submit(t, SubmitOrder{
		OrderID:      orderID,
		AccountID:    "account-1",
		InstrumentID: "BTC-PERP",
		Side:         SideBuy,
		Type:         OrderTypeLimit,
		TimeInForce:  TimeInForceGTC,
		Quantity:     "0.001",
		Price:        "1",
	})

	rejected := fixture.applyDecision(t, TradingAction{
		Kind: TradingActionCancelOrder,
		CancelOrder: &CancelOrder{
			AccountID: "account-2",
			OrderID:   orderID,
		},
	})
	if rejected.CommandResult.Reason != RejectionOrderOwnership {
		t.Fatalf("cross-account cancellation reason = %s, want %s", rejected.CommandResult.Reason, RejectionOrderOwnership)
	}
	after, ok := fixture.state.Order(orderID)
	if !ok || after != working {
		t.Fatalf("cross-account cancellation mutated order:\nbefore: %+v\nafter:  %+v", working, after)
	}
}

func TestTradingInvalidOrderFieldsRejectWithoutCreatingOrder(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*SubmitOrder)
	}{
		{name: "missing side", mutate: func(order *SubmitOrder) { order.Side = "" }},
		{name: "missing type", mutate: func(order *SubmitOrder) { order.Type = "" }},
		{name: "missing TIF", mutate: func(order *SubmitOrder) { order.TimeInForce = "" }},
		{name: "zero quantity", mutate: func(order *SubmitOrder) { order.Quantity = "0" }},
		{name: "negative quantity", mutate: func(order *SubmitOrder) { order.Quantity = "-0.001" }},
		{name: "off step quantity", mutate: func(order *SubmitOrder) { order.Quantity = "0.0001" }},
		{name: "limit missing price", mutate: func(order *SubmitOrder) { order.Price = "" }},
		{name: "off tick price", mutate: func(order *SubmitOrder) { order.Price = "1.001" }},
		{name: "market with price", mutate: func(order *SubmitOrder) {
			order.Type = OrderTypeMarket
			order.Price = "1"
		}},
		{name: "stop missing trigger", mutate: func(order *SubmitOrder) {
			order.Type = OrderTypeStopMarket
			order.Price = ""
		}},
	}

	for index, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newTradingFixture(t)
			order := SubmitOrder{
				OrderID:      fixture.id(orderTestID(index)),
				AccountID:    "account-1",
				InstrumentID: "BTC-PERP",
				Side:         SideBuy,
				Type:         OrderTypeLimit,
				TimeInForce:  TimeInForceGTC,
				Quantity:     "0.001",
				Price:        "1",
			}
			testCase.mutate(&order)
			decision := fixture.applyDecision(t, TradingAction{
				Kind:        TradingActionSubmitOrder,
				SubmitOrder: &order,
			})
			if decision.CommandResult.Status != CommandStatusRejected ||
				decision.CommandResult.Reason != RejectionInvalidOrder {
				t.Fatalf("result = %+v, want rejected/invalid_order", decision.CommandResult)
			}
			if _, exists := fixture.state.Order(order.OrderID); exists {
				t.Fatalf("invalid order %s was created", order.OrderID)
			}
			if !fixture.state.Ready() {
				t.Fatal("business validation rejection halted readiness")
			}
		})
	}
}

func TestTradingTerminalOrderNeverReturnsToWorking(t *testing.T) {
	fixture := newTradingFixture(t)
	orderID := fixture.id(200)
	filled := fixture.submit(t, SubmitOrder{
		OrderID:      orderID,
		AccountID:    "account-1",
		InstrumentID: "BTC-PERP",
		Side:         SideBuy,
		Type:         OrderTypeMarket,
		TimeInForce:  TimeInForceGTC,
		Quantity:     "0.001",
	})
	assertOrderStatus(t, filled, OrderStatusFilled)

	for _, action := range []TradingAction{
		{
			Kind: TradingActionAmendOrder,
			AmendOrder: &AmendOrder{
				AccountID: "account-1",
				OrderID:   orderID,
				Quantity:  "0.002",
				Price:     "2",
			},
		},
		{
			Kind: TradingActionCancelOrder,
			CancelOrder: &CancelOrder{
				AccountID: "account-1",
				OrderID:   orderID,
			},
		},
	} {
		decision := fixture.applyDecision(t, action)
		if decision.CommandResult.Reason != RejectionOrderTerminal {
			t.Fatalf("%s result = %+v, want order_terminal", action.Kind, decision.CommandResult)
		}
		after, ok := fixture.state.Order(orderID)
		if !ok || after.Status != OrderStatusFilled ||
			after.FilledQuantity != filled.FilledQuantity {
			t.Fatalf("%s changed terminal order: %+v", action.Kind, after)
		}
	}
}

func TestTradingDuplicateOrderIDRejectsWithoutReplacingOriginal(t *testing.T) {
	fixture := newTradingFixture(t)
	orderID := fixture.id(210)
	original := fixture.submit(t, SubmitOrder{
		OrderID:      orderID,
		AccountID:    "account-1",
		InstrumentID: "BTC-PERP",
		Side:         SideBuy,
		Type:         OrderTypeLimit,
		TimeInForce:  TimeInForceGTC,
		Quantity:     "0.001",
		Price:        "1",
	})
	decision := fixture.applyDecision(t, TradingAction{
		Kind: TradingActionSubmitOrder,
		SubmitOrder: &SubmitOrder{
			OrderID:      orderID,
			AccountID:    "account-2",
			InstrumentID: "BTC-PERP",
			Side:         SideSell,
			Type:         OrderTypeMarket,
			TimeInForce:  TimeInForceGTC,
			Quantity:     "1",
		},
	})
	if decision.CommandResult.Reason != RejectionDuplicateOrderID {
		t.Fatalf("duplicate ID result = %+v, want duplicate_order_id", decision.CommandResult)
	}
	after, ok := fixture.state.Order(orderID)
	if !ok || after != original {
		t.Fatalf("duplicate ID replaced original:\nbefore: %+v\nafter:  %+v", original, after)
	}
}

func TestTradingRiskIncreasingOrderRequiresExplicitMarketContext(t *testing.T) {
	fixture := newTradingFixtureWithoutBook(t)
	orderID := fixture.id(220)
	decision := fixture.applyDecision(t, TradingAction{
		Kind: TradingActionSubmitOrder,
		SubmitOrder: &SubmitOrder{
			OrderID:      orderID,
			AccountID:    "account-1",
			InstrumentID: "BTC-PERP",
			Side:         SideBuy,
			Type:         OrderTypeLimit,
			TimeInForce:  TimeInForceGTC,
			Quantity:     "0.001",
			Price:        "1",
		},
	})
	if decision.CommandResult.Reason != RejectionInsufficientMarket {
		t.Fatalf("missing market result = %+v, want insufficient_market", decision.CommandResult)
	}
	if _, exists := fixture.state.Order(orderID); exists {
		t.Fatal("order was created without explicit market context")
	}
	if !fixture.state.Ready() {
		t.Fatal("expected business rejection, not shard halt")
	}
}

func TestTradingIndependentAccountsReceiveSameExplicitMarketState(t *testing.T) {
	fixture := newTradingFixture(t)
	first := fixture.submit(t, SubmitOrder{
		OrderID:      fixture.id(230),
		AccountID:    "account-1",
		InstrumentID: "BTC-PERP",
		Side:         SideBuy,
		Type:         OrderTypeMarket,
		TimeInForce:  TimeInForceGTC,
		Quantity:     "10",
	})
	second := fixture.submit(t, SubmitOrder{
		OrderID:      fixture.id(231),
		AccountID:    "account-2",
		InstrumentID: "BTC-PERP",
		Side:         SideBuy,
		Type:         OrderTypeMarket,
		TimeInForce:  TimeInForceGTC,
		Quantity:     "10",
	})
	if first.Status != OrderStatusFilled || second.Status != OrderStatusFilled {
		t.Fatalf("orders did not independently fill: first=%s second=%s", first.Status, second.Status)
	}
	if first.AverageFillPrice != second.AverageFillPrice {
		t.Fatalf("account order changed shared market pricing: first=%s second=%s", first.AverageFillPrice, second.AverageFillPrice)
	}
}

func orderTestID(index int) uint64 {
	sequence := uint64(300)
	for range index {
		sequence++
	}
	return sequence
}
