package engine

import "testing"

func TestTradingDuplicateFillDoesNotDuplicatePositionEffect(t *testing.T) {
	fixture := newTradingFixture(t)
	fixture.submit(t, marketOrder(fixture.id(760), "account-1", SideBuy, "1", nil))
	recorded := cloneDecision(fixture.lastDecision)

	next, duplicate, err := ApplyTrading(
		fixture.state,
		fixture.lastInput,
		fixture.lastAction,
	)
	if err != nil {
		t.Fatalf("duplicate ApplyTrading: %v", err)
	}
	if !equalDecision(duplicate, recorded) {
		t.Fatal("duplicate position decision differs from recorded receipt")
	}
	open := next.OpenPositions("account-1")
	if len(open) != 1 || open[0].SignedQuantity != "1" ||
		open[0].Version != 1 {
		t.Fatalf("duplicate position effect = %+v, want one version-1 long", open)
	}
}

func TestTradingReturnedPositionDecisionCannotMutateReceipt(t *testing.T) {
	fixture := newTradingFixture(t)
	fixture.submit(t, marketOrder(fixture.id(761), "account-1", SideBuy, "1", nil))
	returned := fixture.lastDecision
	returned.Fills[0].PositionEffect = PositionEffectClose
	returned.PositionChanges[0].SignedQuantity = "999"

	_, duplicate, err := ApplyTrading(
		fixture.state,
		fixture.lastInput,
		fixture.lastAction,
	)
	if err != nil {
		t.Fatalf("duplicate ApplyTrading: %v", err)
	}
	if duplicate.Fills[0].PositionEffect != PositionEffectOpen ||
		duplicate.PositionChanges[0].SignedQuantity != "1" {
		t.Fatalf("caller mutation reached stored position receipt: %+v", duplicate)
	}
}

func TestTradingReduceOnlyRejectsWithoutOpposingOwnedPosition(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(*tradingFixture, *testing.T) ID
		account string
		side    Side
	}{
		{
			name:    "no position",
			prepare: func(*tradingFixture, *testing.T) ID { return ID{} },
			account: "account-1",
			side:    SideSell,
		},
		{
			name: "same side",
			prepare: func(fixture *tradingFixture, t *testing.T) ID {
				order := fixture.submit(t, marketOrder(fixture.id(762), "account-1", SideBuy, "1", nil))
				return fixture.state.FillsForOrder(order.OrderID)[0].PositionID
			},
			account: "account-1",
			side:    SideBuy,
		},
		{
			name: "other account position",
			prepare: func(fixture *tradingFixture, t *testing.T) ID {
				fixture.configureAccount(t, "account-1", OmsModeHedging)
				order := fixture.submit(t, marketOrder(fixture.id(763), "account-1", SideBuy, "1", nil))
				return fixture.state.FillsForOrder(order.OrderID)[0].PositionID
			},
			account: "account-2",
			side:    SideSell,
		},
	}
	for index, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newTradingFixture(t)
			positionID := testCase.prepare(fixture, t)
			command := marketOrder(
				fixture.id(positionInvariantID(index)),
				testCase.account,
				testCase.side,
				"1",
				nil,
			)
			command.ReduceOnly = true
			command.PositionID = positionID
			decision := fixture.applyDecision(t, TradingAction{
				Kind:        TradingActionSubmitOrder,
				SubmitOrder: &command,
			})
			if decision.CommandResult.Reason != RejectionReduceOnly {
				t.Fatalf("result = %+v, want reduce_only", decision.CommandResult)
			}
			if _, ok := fixture.state.Order(command.OrderID); ok {
				t.Fatal("rejected reduce-only command created an order")
			}
		})
	}
}

func positionInvariantID(index int) uint64 {
	sequence := uint64(770)
	for range index {
		sequence++
	}
	return sequence
}

func TestTradingOMSModeCannotChangeAcrossActiveEconomicState(t *testing.T) {
	t.Run("working order", func(t *testing.T) {
		fixture := newTradingFixture(t)
		fixture.submit(t, marketableOrder(
			fixture.id(780),
			"account-1",
			SideBuy,
			"1",
			"1",
		))
		decision := fixture.applyDecision(t, TradingAction{
			Kind: TradingActionConfigureAccount,
			ConfigureAccount: &ConfigureAccount{
				AccountID: "account-1",
				OmsMode:   OmsModeHedging,
			},
		})
		if decision.CommandResult.Status != CommandStatusRejected {
			t.Fatalf("mode change with working order = %+v", decision.CommandResult)
		}
	})

	t.Run("open position", func(t *testing.T) {
		fixture := newTradingFixture(t)
		fixture.submit(t, marketOrder(fixture.id(781), "account-1", SideBuy, "1", nil))
		decision := fixture.applyDecision(t, TradingAction{
			Kind: TradingActionConfigureAccount,
			ConfigureAccount: &ConfigureAccount{
				AccountID: "account-1",
				OmsMode:   OmsModeHedging,
			},
		})
		if decision.CommandResult.Status != CommandStatusRejected {
			t.Fatalf("mode change with open position = %+v", decision.CommandResult)
		}
	})
}
