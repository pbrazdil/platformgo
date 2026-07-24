package engine

import "testing"

func TestFillsCarryAndSettleExplicitMakerTakerFees(t *testing.T) {
	t.Run("taker", func(t *testing.T) {
		fixture := newTradingFixtureWithFeeRates(t, "0.001", "0.002")
		order := fixture.submit(t, SubmitOrder{
			OrderID: fixture.id(970), AccountID: "account-1",
			InstrumentID: "BTC-PERP", Side: SideBuy,
			Type: OrderTypeMarket, TimeInForce: TimeInForceGTC,
			Quantity: "1",
		})
		fills := fixture.state.FillsForOrder(order.OrderID)
		if len(fills) != 1 || fills[0].LiquiditySide != LiquiditySideTaker ||
			fills[0].Fee != "0.2" || fills[0].FeeCurrency != "USDC" {
			t.Fatalf("taker fills = %+v", fills)
		}
		balance, _ := fixture.state.Balance("account-1", "USDC")
		if balance.Total != "999999.8" {
			t.Fatalf("balance after taker fee = %s, want 999999.8", balance.Total)
		}
	})

	t.Run("maker", func(t *testing.T) {
		fixture := newTradingFixtureWithFeeRates(t, "-0.001", "0.002")
		orderID := fixture.id(980)
		order := fixture.submit(t, SubmitOrder{
			OrderID: orderID, AccountID: "account-1",
			InstrumentID: "BTC-PERP", Side: SideBuy,
			Type: OrderTypeLimit, TimeInForce: TimeInForceGTC,
			Quantity: "1", Price: "90",
		})
		assertOrderStatus(t, order, OrderStatusWorking)
		updateTriggerQuote(t, fixture, "89", "90")
		fills := fixture.state.FillsForOrder(orderID)
		if len(fills) != 1 || fills[0].LiquiditySide != LiquiditySideMaker ||
			fills[0].Fee != "-0.09" || fills[0].FeeCurrency != "USDC" {
			t.Fatalf("maker fills = %+v", fills)
		}
		balance, _ := fixture.state.Balance("account-1", "USDC")
		if balance.Total != "1000000.09" {
			t.Fatalf("balance after maker rebate = %s, want 1000000.09", balance.Total)
		}
	})
}

func TestFeeReservationParticipatesInMarginAdmission(t *testing.T) {
	fixture := newTradingFixtureWithFeeRates(t, "0.1", "0.1")
	fixture.apply(t, TradingAction{
		Kind: TradingActionAdjustBalance,
		AdjustBalance: &AdjustBalance{
			AccountID: "account-1", Currency: "USDC", CurrencyScale: 8,
			Operation: BalanceOperationSet, Amount: "19",
		},
	})
	decision := fixture.applyDecision(t, TradingAction{
		Kind: TradingActionSubmitOrder,
		SubmitOrder: &SubmitOrder{
			OrderID: fixture.id(990), AccountID: "account-1",
			InstrumentID: "BTC-PERP", Side: SideBuy,
			Type: OrderTypeMarket, TimeInForce: TimeInForceGTC,
			Quantity: "1",
		},
	})
	if decision.CommandResult.Status != CommandStatusRejected ||
		decision.CommandResult.Reason != RejectionInsufficientMargin {
		t.Fatalf("fee-aware admission = %+v, want insufficient margin", decision.CommandResult)
	}
}

func TestDuplicateFeeBearingFillDoesNotDebitBalanceTwice(t *testing.T) {
	fixture := newTradingFixtureWithFeeRates(t, "0.001", "0.002")
	fixture.submit(t, SubmitOrder{
		OrderID: fixture.id(995), AccountID: "account-1",
		InstrumentID: "BTC-PERP", Side: SideBuy,
		Type: OrderTypeMarket, TimeInForce: TimeInForceGTC,
		Quantity: "1",
	})
	before, _ := fixture.state.Balance("account-1", "USDC")
	duplicateState, duplicateDecision, err := ApplyTrading(
		fixture.state,
		fixture.lastInput,
		fixture.lastAction,
	)
	if err != nil {
		t.Fatalf("duplicate ApplyTrading: %v", err)
	}
	after, _ := duplicateState.Balance("account-1", "USDC")
	if after.Total != before.Total {
		t.Fatalf("duplicate fee debit changed balance: before=%s after=%s", before.Total, after.Total)
	}
	if duplicateDecision.DecisionHash != fixture.lastDecision.DecisionHash ||
		len(duplicateState.FillsForOrder(fixture.id(995))) != 1 {
		t.Fatalf("duplicate fee decision/fills changed: decision=%+v", duplicateDecision)
	}
}
