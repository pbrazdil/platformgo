package engine

import "testing"

func TestBalanceDecisionCarriesStableBalancedLedgerEffect(t *testing.T) {
	fixture := newTradingFixture(t)
	decision := fixture.apply(t, TradingAction{
		Kind: TradingActionAdjustBalance,
		AdjustBalance: &AdjustBalance{
			AccountID:     "account-1",
			Currency:      "USDC",
			CurrencyScale: 8,
			Operation:     BalanceOperationDeposit,
			Amount:        "10",
		},
	})

	if len(decision.LedgerChanges) != 1 {
		t.Fatalf("ledger changes = %d, want 1", len(decision.LedgerChanges))
	}
	transaction := decision.LedgerChanges[0]
	if transaction.InputID != fixture.lastInput.InputID ||
		transaction.LogicalTime != fixture.lastInput.LogicalTime ||
		len(transaction.Entries) != 2 {
		t.Fatalf("ledger transaction = %+v", transaction)
	}
	if transaction.Entries[0].AccountID != "account-1" ||
		transaction.Entries[0].Currency != "USDC" ||
		transaction.Entries[0].Amount != "10" {
		t.Fatalf("account ledger entry = %+v", transaction.Entries[0])
	}
	if transaction.Entries[1].AccountID != SystemClearingAccount ||
		transaction.Entries[1].Currency != "USDC" ||
		transaction.Entries[1].Amount != "-10" {
		t.Fatalf("clearing ledger entry = %+v", transaction.Entries[1])
	}

	_, duplicate, err := ApplyTrading(
		fixture.state,
		fixture.lastInput,
		fixture.lastAction,
	)
	if err != nil {
		t.Fatalf("duplicate balance input: %v", err)
	}
	if len(duplicate.LedgerChanges) != 1 ||
		duplicate.LedgerChanges[0].TransactionID != transaction.TransactionID {
		t.Fatal("duplicate did not return the stable recorded ledger effect")
	}
}

func TestTradingDuplicateBalanceInputDoesNotMoveMoneyTwice(t *testing.T) {
	fixture := newTradingFixture(t)
	fixture.setBalance(t, "account-1", "0", BalanceOperationSet)
	fixture.setBalance(t, "account-1", "10", BalanceOperationDeposit)
	before, _ := fixture.state.Balance("account-1", "USDC")

	next, duplicate, err := ApplyTrading(
		fixture.state,
		fixture.lastInput,
		fixture.lastAction,
	)
	if err != nil {
		t.Fatalf("duplicate balance input: %v", err)
	}
	after, _ := next.Balance("account-1", "USDC")
	if after.Total != "10" || after != before {
		t.Fatalf("duplicate deposit changed balance: before=%+v after=%+v", before, after)
	}
	if len(duplicate.BalanceChanges) != 1 ||
		duplicate.BalanceChanges[0].Total != "10" {
		t.Fatalf("duplicate receipt = %+v, want recorded total 10", duplicate.BalanceChanges)
	}
}

func TestTradingConflictingFundingBusinessKeyRejectsWithoutMoneyMovement(t *testing.T) {
	fixture := newTradingFixture(t)
	fixture.setBalance(t, "account-1", "1000", BalanceOperationSet)
	fixture.submit(t, marketOrder(fixture.id(860), "account-1", SideBuy, "1", nil))
	settlementID := fixture.id(861)
	fixture.apply(t, TradingAction{
		Kind: TradingActionSettleFunding,
		SettleFunding: &SettleFunding{
			SettlementID: settlementID,
			InstrumentID: "BTC-PERP",
			OraclePrice:  "1000",
			Rate:         "0.01",
		},
	})
	before, _ := fixture.state.Balance("account-1", "USDC")

	decision := fixture.applyDecision(t, TradingAction{
		Kind: TradingActionSettleFunding,
		SettleFunding: &SettleFunding{
			SettlementID: settlementID,
			InstrumentID: "BTC-PERP",
			OraclePrice:  "1000",
			Rate:         "0.02",
		},
	})
	if decision.CommandResult.Reason != RejectionDuplicateSettlement {
		t.Fatalf("conflicting settlement = %+v, want duplicate_settlement", decision.CommandResult)
	}
	after, _ := fixture.state.Balance("account-1", "USDC")
	if after != before {
		t.Fatalf("conflicting settlement moved balance: before=%+v after=%+v", before, after)
	}
}

func TestTradingStaleMarkBlocksLiquidationWithoutPartialEffects(t *testing.T) {
	fixture := newTradingFixture(t)
	fixture.submit(t, marketOrder(fixture.id(870), "account-1", SideBuy, "1", nil))
	beforePositions := fixture.state.OpenPositions("account-1")
	beforeOrders := fixture.state.Orders()
	fixture.apply(t, TradingAction{
		Kind: TradingActionUpdateBook,
		UpdateBook: &UpdateBook{
			InstrumentID: "BTC-PERP",
			Bids:         []BookLevel{{Price: "90", Quantity: "10"}},
			Asks:         []BookLevel{{Price: "91", Quantity: "10"}},
		},
	})

	decision := fixture.applyDecision(t, TradingAction{
		Kind: TradingActionLiquidateAccount,
		LiquidateAccount: &LiquidateAccount{
			AccountID: "account-1",
		},
	})
	if decision.CommandResult.Reason != RejectionMarketDataStale {
		t.Fatalf("stale liquidation = %+v, want market_data_stale", decision.CommandResult)
	}
	afterPositions := fixture.state.OpenPositions("account-1")
	afterOrders := fixture.state.Orders()
	if len(afterPositions) != len(beforePositions) ||
		afterPositions[0] != beforePositions[0] ||
		len(afterOrders) != len(beforeOrders) {
		t.Fatalf(
			"stale liquidation mutated state: positions before=%+v after=%+v orders before=%d after=%d",
			beforePositions,
			afterPositions,
			len(beforeOrders),
			len(afterOrders),
		)
	}
}

func TestTradingDuplicateCloseDoesNotSettleRealizedPnLTwice(t *testing.T) {
	fixture := newTradingFixture(t)
	fixture.setBalance(t, "account-1", "1000", BalanceOperationSet)
	open := fixture.submit(t, marketOrder(fixture.id(880), "account-1", SideBuy, "1", nil))
	positionID := fixture.state.FillsForOrder(open.OrderID)[0].PositionID
	fixture.updateBook(t, "110",
		[]BookLevel{{Price: "110", Quantity: "10"}},
		[]BookLevel{{Price: "111", Quantity: "10"}},
	)
	closeCommand := marketOrder(fixture.id(881), "account-1", SideSell, "1", nil)
	closeCommand.ReduceOnly = true
	closeCommand.PositionID = positionID
	fixture.submit(t, closeCommand)
	before, _ := fixture.state.Balance("account-1", "USDC")

	next, _, err := ApplyTrading(
		fixture.state,
		fixture.lastInput,
		fixture.lastAction,
	)
	if err != nil {
		t.Fatalf("duplicate close: %v", err)
	}
	after, _ := next.Balance("account-1", "USDC")
	if after != before || after.Total != "1010" {
		t.Fatalf("duplicate close balance: before=%+v after=%+v", before, after)
	}
}

func TestTradingUnrealizedLossReducesFreeMarginAdmission(t *testing.T) {
	fixture := newTradingFixture(t)
	fixture.setBalance(t, "account-1", "20", BalanceOperationSet)
	fixture.submit(t, marketOrder(fixture.id(890), "account-1", SideBuy, "1", nil))
	fixture.updateBook(t, "90",
		[]BookLevel{{Price: "89", Quantity: "10"}},
		[]BookLevel{{Price: "90", Quantity: "10"}},
	)
	balance, _ := fixture.state.Balance("account-1", "USDC")
	if balance.Equity != "10" || balance.Used != "10" || balance.Free != "0" {
		t.Fatalf("loss-adjusted margin = %+v, want equity=10 used=10 free=0", balance)
	}

	command := marketOrder(fixture.id(891), "account-1", SideBuy, "1", nil)
	decision := fixture.applyDecision(t, TradingAction{
		Kind:        TradingActionSubmitOrder,
		SubmitOrder: &command,
	})
	if decision.CommandResult.Reason != RejectionInsufficientMargin {
		t.Fatalf("loss-exposed admission = %+v, want insufficient_margin", decision.CommandResult)
	}
}

func TestTradingRestingOrdersReserveMarginAgainstOvercommit(t *testing.T) {
	fixture := newTradingFixture(t)
	fixture.setBalance(t, "account-1", "15", BalanceOperationSet)
	first := marketableOrder(fixture.id(892), "account-1", SideBuy, "1", "90")
	assertOrderStatus(t, fixture.submit(t, first), OrderStatusWorking)
	balance, _ := fixture.state.Balance("account-1", "USDC")
	if balance.Used != "10" || balance.Free != "5" {
		t.Fatalf("resting reservation = %+v, want used=10 free=5", balance)
	}

	second := marketableOrder(fixture.id(893), "account-1", SideBuy, "1", "90")
	decision := fixture.applyDecision(t, TradingAction{
		Kind:        TradingActionSubmitOrder,
		SubmitOrder: &second,
	})
	if decision.CommandResult.Reason != RejectionInsufficientMargin {
		t.Fatalf("overcommitted resting order = %+v, want insufficient_margin", decision.CommandResult)
	}
}
