package engine

import "testing"

func TestCurrencyCodeCannotBeConfiguredWithConflictingScales(t *testing.T) {
	fixture := newTradingFixture(t)
	configure := func(instrumentID string, scale uint8) TradingAction {
		return TradingAction{
			Kind: TradingActionConfigureInstrument,
			ConfigureInstrument: &ConfigureInstrument{
				InstrumentID:            instrumentID,
				Revision:                1,
				PriceScale:              2,
				QuantityScale:           3,
				SettlementCurrency:      "USDC",
				SettlementCurrencyScale: scale,
				InitialMarginRate:       "0.1",
				MaintenanceMarginRate:   "0.05",
				MaxLeverage:             "10",
				MakerFeeRate:            "0",
				TakerFeeRate:            "0",
			},
		}
	}

	conflict := fixture.applyDecision(t, configure("ETH-PERP", 2))
	if conflict.CommandResult.Status != CommandStatusRejected ||
		conflict.CommandResult.Reason != RejectionInvalidInstrument ||
		len(conflict.InstrumentChanges) != 0 {
		t.Fatalf("conflicting currency scale decision = %+v", conflict)
	}
	conflictingBalance := fixture.applyDecision(t, TradingAction{
		Kind: TradingActionAdjustBalance,
		AdjustBalance: &AdjustBalance{
			AccountID:     "account-1",
			Currency:      "USDC",
			CurrencyScale: 2,
			Operation:     BalanceOperationSet,
			Amount:        "20",
		},
	})
	if conflictingBalance.CommandResult.Status != CommandStatusRejected ||
		len(conflictingBalance.BalanceChanges) != 0 ||
		len(conflictingBalance.LedgerChanges) != 0 {
		t.Fatalf("conflicting scale balance decision = %+v", conflictingBalance)
	}

	sameScale := fixture.apply(t, configure("ETH-PERP", 8))
	if sameScale.CommandResult.Status != CommandStatusAccepted ||
		len(sameScale.InstrumentChanges) != 1 ||
		sameScale.InstrumentChanges[0].SettlementCurrencyScale != 8 {
		t.Fatalf("same currency scale decision = %+v", sameScale)
	}
}

func TestCurrencyScaleIdentitySurvivesInstrumentReconfiguration(t *testing.T) {
	fixture := newTradingFixture(t)
	configure := func(instrumentID, currency string, scale uint8) TradingAction {
		return TradingAction{
			Kind: TradingActionConfigureInstrument,
			ConfigureInstrument: &ConfigureInstrument{
				InstrumentID:            instrumentID,
				Revision:                2,
				PriceScale:              2,
				QuantityScale:           3,
				SettlementCurrency:      currency,
				SettlementCurrencyScale: scale,
				InitialMarginRate:       "0.1",
				MaintenanceMarginRate:   "0.05",
				MaxLeverage:             "10",
				MakerFeeRate:            "0",
				TakerFeeRate:            "0",
			},
		}
	}

	fixture.apply(t, configure("BTC-PERP", "EUR", 2))
	conflict := fixture.applyDecision(t, configure("ETH-PERP", "USDC", 2))
	if conflict.CommandResult.Status != CommandStatusRejected ||
		conflict.CommandResult.Reason != RejectionInvalidInstrument ||
		len(conflict.InstrumentChanges) != 0 ||
		len(conflict.BalanceChanges) != 0 ||
		len(conflict.LedgerChanges) != 0 {
		t.Fatalf("reintroduced currency scale decision = %+v", conflict)
	}
}

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

func TestDecisionHashV3AddsExactDerivedBalanceProjectionWithoutChangingState(
	t *testing.T,
) {
	fixture := newTradingFixture(t)
	maxSlippageBPS := uint32(50)
	action := TradingAction{
		Kind: TradingActionSubmitOrder,
		SubmitOrder: &SubmitOrder{
			OrderID:        fixture.id(8_703),
			AccountID:      "account-1",
			InstrumentID:   "BTC-PERP",
			Side:           SideBuy,
			Type:           OrderTypeStopMarket,
			TimeInForce:    TimeInForceGTC,
			Quantity:       "1",
			TriggerPrice:   "110",
			MaxSlippageBPS: &maxSlippageBPS,
		},
	}
	payload, err := EncodeTradingAction(action)
	if err != nil {
		t.Fatalf("encode order action: %v", err)
	}
	input := InputEnvelope{
		InputID:              fixture.id(1_000 + fixture.sequence),
		SchemaVersion:        CurrentSchemaVersion,
		ShardID:              fixture.state.ShardID(),
		Kind:                 InputKindCommand,
		SourceID:             "decision-version-test",
		SourceSequence:       fixture.sequence,
		StreamSequence:       fixture.state.NextStreamSequence(),
		MarketSequence:       fixture.marketSequence,
		LogicalTime:          fixture.logicalTime,
		ConfigurationVersion: 1,
		InstrumentVersion:    1,
		Payload:              payload,
	}

	legacyState, legacy, err :=
		ApplyTradingWithReceiptsAtDecisionHashVersion(
			fixture.state,
			input,
			action,
			nil,
			2,
		)
	if err != nil {
		t.Fatalf("apply decision v2: %v", err)
	}
	currentState, current, err :=
		ApplyTradingWithReceiptsAtDecisionHashVersion(
			fixture.state,
			input,
			action,
			nil,
			3,
		)
	if err != nil {
		t.Fatalf("apply decision v3: %v", err)
	}
	if legacy.DecisionHashVersion != 2 || len(legacy.BalanceChanges) != 0 {
		t.Fatalf("legacy decision = %+v", legacy)
	}
	if current.DecisionHashVersion != 3 ||
		len(current.BalanceChanges) != 1 ||
		current.BalanceChanges[0].Used != "11" ||
		current.BalanceChanges[0].Free != "999989" {
		t.Fatalf("current decision balances = %#v", current.BalanceChanges)
	}
	legacyBalance, legacyOK := legacyState.Balance("account-1", "USDC")
	currentBalance, currentOK := currentState.Balance("account-1", "USDC")
	if !legacyOK || !currentOK || legacyBalance != currentBalance {
		t.Fatalf(
			"economic state differs: legacy=%#v current=%#v",
			legacyBalance,
			currentBalance,
		)
	}
	if legacy.DecisionHash == current.DecisionHash ||
		legacyState.Hash() == currentState.Hash() {
		t.Fatal("decision hash generations did not separate the state chains")
	}
}

func TestDerivedBalanceProjectionCoversMarketOnlyTransitions(
	t *testing.T,
) {
	marketFixture := newTradingFixture(t)
	marketFixture.submit(
		t,
		marketOrder(marketFixture.id(8_704), "account-1", SideBuy, "1", nil),
	)
	marketDecision := marketFixture.apply(t, TradingAction{
		Kind: TradingActionUpdateBook,
		UpdateBook: &UpdateBook{
			InstrumentID: "BTC-PERP",
			MarkPrice:    "110",
			Bids:         []BookLevel{{Price: "109", Quantity: "10"}},
			Asks:         []BookLevel{{Price: "110", Quantity: "10"}},
		},
	})
	if len(marketDecision.OrderChanges) != 0 ||
		len(marketDecision.BalanceChanges) != 1 ||
		marketDecision.BalanceChanges[0].Used != "10" ||
		marketDecision.BalanceChanges[0].Equity != "1000010" ||
		marketDecision.BalanceChanges[0].Free != "1000000" {
		t.Fatalf(
			"market-only balance projection = orders %#v balances %#v",
			marketDecision.OrderChanges,
			marketDecision.BalanceChanges,
		)
	}
}

func TestRestoredMarkRepairsProjectionWithoutCreatingLedgerMoney(
	t *testing.T,
) {
	fixture := newTradingFixture(t)
	fixture.submit(
		t,
		marketOrder(fixture.id(8_705), "account-1", SideBuy, "1", nil),
	)

	markless := fixture.apply(t, TradingAction{
		Kind: TradingActionUpdateBook,
		UpdateBook: &UpdateBook{
			InstrumentID: "BTC-PERP",
			Bids:         []BookLevel{{Price: "99", Quantity: "10"}},
			Asks:         []BookLevel{{Price: "100", Quantity: "10"}},
		},
	})
	if len(markless.BalanceChanges) != 0 ||
		len(markless.LedgerChanges) != 0 {
		t.Fatalf(
			"markless effects = balances %#v ledger %#v",
			markless.BalanceChanges,
			markless.LedgerChanges,
		)
	}

	restored := fixture.apply(t, TradingAction{
		Kind: TradingActionUpdateBook,
		UpdateBook: &UpdateBook{
			InstrumentID: "BTC-PERP",
			MarkPrice:    "110",
			Bids:         []BookLevel{{Price: "109", Quantity: "10"}},
			Asks:         []BookLevel{{Price: "110", Quantity: "10"}},
		},
	})
	if len(restored.BalanceChanges) != 1 ||
		restored.BalanceChanges[0].Total != "1000000" ||
		restored.BalanceChanges[0].Used != "10" ||
		restored.BalanceChanges[0].Equity != "1000010" ||
		restored.BalanceChanges[0].Free != "1000000" {
		t.Fatalf("restored balance projection = %#v", restored.BalanceChanges)
	}
	if len(restored.LedgerChanges) != 0 {
		t.Fatalf("restored mark minted ledger money = %#v", restored.LedgerChanges)
	}
}

func TestOrderLifecycleEmitsExactReservationAndPositionProjections(
	t *testing.T,
) {
	fixture := newTradingFixture(t)
	orderID := fixture.id(8_706)
	submitted := fixture.apply(t, TradingAction{
		Kind: TradingActionSubmitOrder,
		SubmitOrder: &SubmitOrder{
			OrderID:      orderID,
			AccountID:    "account-1",
			InstrumentID: "BTC-PERP",
			Side:         SideBuy,
			Type:         OrderTypeLimit,
			TimeInForce:  TimeInForceGTC,
			Quantity:     "1",
			Price:        "90",
		},
	})
	assertSingleBalanceProjection(t, submitted, "10", "999990")

	amended := fixture.apply(t, TradingAction{
		Kind: TradingActionAmendOrder,
		AmendOrder: &AmendOrder{
			OrderID:   orderID,
			AccountID: "account-1",
			Quantity:  "2",
			Price:     "90",
		},
	})
	assertSingleBalanceProjection(t, amended, "20", "999980")

	cancelled := fixture.apply(t, TradingAction{
		Kind: TradingActionCancelOrder,
		CancelOrder: &CancelOrder{
			OrderID:   orderID,
			AccountID: "account-1",
		},
	})
	assertSingleBalanceProjection(t, cancelled, "0", "1000000")

	partialFixture := newTradingFixtureWithoutBook(t)
	partialFixture.updateBook(
		t,
		"100",
		[]BookLevel{{Price: "99", Quantity: "10"}},
		[]BookLevel{{Price: "100", Quantity: "0.5"}},
	)
	partial := partialFixture.apply(t, TradingAction{
		Kind: TradingActionSubmitOrder,
		SubmitOrder: &SubmitOrder{
			OrderID:      partialFixture.id(8_707),
			AccountID:    "account-1",
			InstrumentID: "BTC-PERP",
			Side:         SideBuy,
			Type:         OrderTypeLimit,
			TimeInForce:  TimeInForceIOC,
			Quantity:     "1",
			Price:        "100",
		},
	})
	if len(partial.Fills) != 1 ||
		partial.OrderChanges[0].Status != OrderStatusCancelled {
		t.Fatalf("partial IOC decision = %+v", partial)
	}
	assertSingleBalanceProjection(t, partial, "5", "999995")

	fullFixture := newTradingFixture(t)
	full := fullFixture.apply(t, TradingAction{
		Kind: TradingActionSubmitOrder,
		SubmitOrder: &SubmitOrder{
			OrderID:      fullFixture.id(8_708),
			AccountID:    "account-1",
			InstrumentID: "BTC-PERP",
			Side:         SideBuy,
			Type:         OrderTypeMarket,
			TimeInForce:  TimeInForceGTC,
			Quantity:     "1",
		},
	})
	if len(full.Fills) != 1 ||
		full.OrderChanges[0].Status != OrderStatusFilled {
		t.Fatalf("full fill decision = %+v", full)
	}
	assertSingleBalanceProjection(t, full, "10", "999990")
}

func assertSingleBalanceProjection(
	t *testing.T,
	decision Decision,
	used string,
	free string,
) {
	t.Helper()
	if len(decision.BalanceChanges) != 1 ||
		decision.BalanceChanges[0].Used != used ||
		decision.BalanceChanges[0].Free != free {
		t.Fatalf(
			"balance projections = %#v, want used %s free %s",
			decision.BalanceChanges,
			used,
			free,
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
