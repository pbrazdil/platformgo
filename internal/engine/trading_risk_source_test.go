package engine

import "testing"

// Ported from:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/margin/e2e_funding_gate.rs:52
//	test: unfunded_account_is_rejected_then_trades_after_deposit
//
// Adaptations:
//   - Balance projection and order admission are one synchronous engine seam.
//
// Assertions preserved:
//   - An unfunded account cannot create a position.
//   - The same account can trade after an explicit deposit.
func TestTradingUnfundedAccountIsRejectedThenTradesAfterDeposit(t *testing.T) {
	fixture := newTradingFixture(t)
	fixture.setBalance(t, "account-1", "0", BalanceOperationSet)
	command := marketOrder(fixture.id(800), "account-1", SideBuy, "1", nil)
	decision := fixture.applyDecision(t, TradingAction{
		Kind:        TradingActionSubmitOrder,
		SubmitOrder: &command,
	})
	if decision.CommandResult.Reason != RejectionInsufficientFunds {
		t.Fatalf("unfunded result = %+v, want insufficient_funds", decision.CommandResult)
	}
	if len(fixture.state.OpenPositions("account-1")) != 0 {
		t.Fatal("unfunded order created a position")
	}

	fixture.setBalance(t, "account-1", "100", BalanceOperationDeposit)
	order := fixture.submit(t, marketOrder(fixture.id(801), "account-1", SideBuy, "1", nil))
	assertOrderStatus(t, order, OrderStatusFilled)
}

// Ported from:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/margin/e2e_margin_denial_taxonomy.rs:191
//	test: engine_margin_denial_surfaces_as_typed_insufficient_margin
//
// Adaptations:
//   - HTTP error projection is replaced by the typed engine command result.
//
// Assertions preserved:
//   - A funded but over-margin order is rejected.
//   - The rejection reason is exactly insufficient_margin.
func TestTradingOverMarginOrderHasTypedInsufficientMargin(t *testing.T) {
	fixture := newTradingFixture(t)
	fixture.setBalance(t, "account-1", "1", BalanceOperationSet)
	command := marketOrder(fixture.id(810), "account-1", SideBuy, "1", nil)
	decision := fixture.applyDecision(t, TradingAction{
		Kind:        TradingActionSubmitOrder,
		SubmitOrder: &command,
	})
	if decision.CommandResult.Reason != RejectionInsufficientMargin {
		t.Fatalf("over-margin result = %+v, want insufficient_margin", decision.CommandResult)
	}
	if _, ok := fixture.state.Order(command.OrderID); ok {
		t.Fatal("over-margin command created an order")
	}
}

// Ported from:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/margin/e2e_visibility.rs:53
//	test: balances_used_margin_equals_qty_entry_margin_init_over_leverage
//
// Adaptations:
//   - REST/database polling is replaced by an immutable balance snapshot.
//   - Tolerance is strengthened to exact settlement-currency arithmetic.
//
// Assertions preserved:
//   - Used margin equals quantity times entry times initial rate over leverage.
//   - Free balance is total less used margin.
func TestTradingUsedMarginEqualsExactPositionFormula(t *testing.T) {
	fixture := newTradingFixture(t)
	fixture.setBalance(t, "account-1", "100", BalanceOperationSet)
	fixture.submit(t, marketOrder(fixture.id(820), "account-1", SideBuy, "2", nil))

	balance, ok := fixture.state.Balance("account-1", "USDC")
	if !ok {
		t.Fatal("USDC balance is missing")
	}
	if balance.Total != "100" || balance.Used != "20" ||
		balance.Free != "80" || balance.Equity != "100" {
		t.Fatalf("margin balance = %+v, want total=100 used=20 free=80 equity=100", balance)
	}
}

// Ported from:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/trading/e2e_funding_settlement.rs:115
//	test: funding_settlement_debits_a_long_and_is_idempotent
func TestTradingFundingDebitsLongAndIsIdempotent(t *testing.T) {
	fixture := newTradingFixture(t)
	fixture.setBalance(t, "account-1", "1000", BalanceOperationSet)
	fixture.submit(t, marketOrder(fixture.id(830), "account-1", SideBuy, "1", nil))

	settlement := SettleFunding{
		SettlementID: fixture.id(831),
		InstrumentID: "BTC-PERP",
		OraclePrice:  "1000",
		Rate:         "0.01",
	}
	first := fixture.apply(t, TradingAction{
		Kind:          TradingActionSettleFunding,
		SettleFunding: &settlement,
	})
	if len(first.FundingChanges) != 1 {
		t.Fatalf("funding changes = %+v, want one long debit", first.FundingChanges)
	}
	long, _ := fixture.state.Balance("account-1", "USDC")
	if long.Total != "990" {
		t.Fatalf("long funding balance = %s, want 990", long.Total)
	}

	second := fixture.apply(t, TradingAction{
		Kind:          TradingActionSettleFunding,
		SettleFunding: &settlement,
	})
	if len(second.FundingChanges) != 0 || len(second.BalanceChanges) != 0 {
		t.Fatalf("duplicate settlement effects = funding %+v balances %+v", second.FundingChanges, second.BalanceChanges)
	}
	longAgain, _ := fixture.state.Balance("account-1", "USDC")
	if longAgain.Total != long.Total {
		t.Fatal("duplicate funding settlement moved money")
	}
}

// Ported from:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/trading/e2e_funding_settlement.rs:351
//	test: funding_settlement_credits_a_short
func TestTradingFundingCreditsShort(t *testing.T) {
	fixture := newTradingFixture(t)
	fixture.setBalance(t, "account-2", "1000", BalanceOperationSet)
	fixture.submit(t, marketOrder(fixture.id(832), "account-2", SideSell, "1", nil))
	decision := fixture.apply(t, TradingAction{
		Kind: TradingActionSettleFunding,
		SettleFunding: &SettleFunding{
			SettlementID: fixture.id(833),
			InstrumentID: "BTC-PERP",
			OraclePrice:  "1000",
			Rate:         "0.01",
		},
	})
	if len(decision.FundingChanges) != 1 ||
		decision.FundingChanges[0].Amount != "10" {
		t.Fatalf("short funding changes = %+v, want exact credit 10", decision.FundingChanges)
	}
	short, _ := fixture.state.Balance("account-2", "USDC")
	if short.Total != "1010" {
		t.Fatalf("short funding balance = %s, want 1010", short.Total)
	}
}

// Ported from:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/margin/e2e_margin_mode_open_guard.rs:122
//	test: margin_mode_and_leverage_are_locked_while_a_position_is_open
//
// Adaptations:
//   - Explicit risk configuration is applied synchronously.
//
// Assertions preserved:
//   - Margin mode and leverage changes reject while a position is open.
//   - Both changes succeed after the account is flat.
func TestTradingMarginModeAndLeverageLockedWhilePositionOpen(t *testing.T) {
	fixture := newTradingFixture(t)
	open := fixture.submit(t, marketOrder(fixture.id(840), "account-1", SideBuy, "1", nil))
	positionID := fixture.state.FillsForOrder(open.OrderID)[0].PositionID
	for _, configure := range []ConfigureRisk{
		{
			AccountID:    "account-1",
			InstrumentID: "BTC-PERP",
			MarginMode:   MarginModeIsolated,
			Leverage:     "10",
		},
		{
			AccountID:    "account-1",
			InstrumentID: "BTC-PERP",
			MarginMode:   MarginModeCross,
			Leverage:     "5",
		},
	} {
		rejected := fixture.applyDecision(t, TradingAction{
			Kind:          TradingActionConfigureRisk,
			ConfigureRisk: &configure,
		})
		if rejected.CommandResult.Reason != RejectionRiskConfigLocked {
			t.Fatalf("risk change while open = %+v, want risk_config_locked", rejected.CommandResult)
		}
	}

	closeCommand := marketOrder(fixture.id(841), "account-1", SideSell, "1", nil)
	closeCommand.ReduceOnly = true
	closeCommand.PositionID = positionID
	fixture.submit(t, closeCommand)
	fixture.configureRisk(t, "account-1", "BTC-PERP", MarginModeIsolated, "10")
	fixture.configureRisk(t, "account-1", "BTC-PERP", MarginModeCross, "5")
}

// Ported from:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/trading/e2e_pnl_settlement.rs:172
//	test: closed_position_realized_pnl_settles_into_total
//
// Adaptations:
//   - Projection polling is replaced by the atomic fill decision.
//   - Approximate float comparison is strengthened to exact USDC.
//
// Assertions preserved:
//   - Closing realized PnL moves the account total by the same amount.
//   - The fill delta and closed position total agree.
func TestTradingClosedPositionRealizedPnLSettlesIntoTotal(t *testing.T) {
	fixture := newTradingFixture(t)
	fixture.setBalance(t, "account-1", "1000", BalanceOperationSet)
	open := fixture.submit(t, marketOrder(fixture.id(845), "account-1", SideBuy, "1", nil))
	positionID := fixture.state.FillsForOrder(open.OrderID)[0].PositionID
	fixture.updateBook(t, "110",
		[]BookLevel{{Price: "110", Quantity: "10"}},
		[]BookLevel{{Price: "111", Quantity: "10"}},
	)
	closeCommand := marketOrder(fixture.id(846), "account-1", SideSell, "1", nil)
	closeCommand.ReduceOnly = true
	closeCommand.PositionID = positionID
	_, decision := fixture.submitDecision(t, closeCommand)

	balance, _ := fixture.state.Balance("account-1", "USDC")
	position, _ := fixture.state.Position(positionID)
	if balance.Total != "1010" ||
		position.RealizedPnL != "10" ||
		len(decision.Fills) != 1 ||
		decision.Fills[0].RealizedPnL != "10" {
		t.Fatalf(
			"settlement balance=%+v position=%+v fills=%+v, want exact +10",
			balance,
			position,
			decision.Fills,
		)
	}
}

// Ported from:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/margin/e2e_stopout_worst_pick.rs:58
//	test: stop_out_liquidates_the_largest_notional_position_first
//
// Adaptations:
//   - The periodic stop-out tick is an explicit ordered liquidation input.
//   - Position changes expose the deterministic liquidation order directly.
//
// Assertions preserved:
//   - The first liquidated position has the largest notional, not quantity.
func TestTradingStopOutLiquidatesLargestNotionalFirst(t *testing.T) {
	fixture := newTradingFixture(t)
	fixture.configureInstrument(t, ConfigureInstrument{
		InstrumentID:            "ETH-PERP",
		Revision:                1,
		PriceScale:              2,
		QuantityScale:           3,
		SettlementCurrency:      "USDC",
		SettlementCurrencyScale: 8,
		InitialMarginRate:       "1",
		MaintenanceMarginRate:   "0.05",
		MaxLeverage:             "10",
	})
	fixture.updateInstrumentBook(t, "ETH-PERP", "40",
		[]BookLevel{{Price: "40", Quantity: "10"}},
		[]BookLevel{{Price: "41", Quantity: "10"}},
	)
	fixture.setBalance(t, "account-1", "1000", BalanceOperationSet)
	btc := fixture.submit(t, marketOrder(fixture.id(850), "account-1", SideBuy, "1", nil))
	ethCommand := marketOrder(fixture.id(851), "account-1", SideBuy, "2", nil)
	ethCommand.InstrumentID = "ETH-PERP"
	fixture.submit(t, ethCommand)
	btcPosition := fixture.state.FillsForOrder(btc.OrderID)[0].PositionID

	fixture.setBalance(t, "account-1", "1", BalanceOperationSet)
	decision := fixture.apply(t, TradingAction{
		Kind: TradingActionLiquidateAccount,
		LiquidateAccount: &LiquidateAccount{
			AccountID: "account-1",
		},
	})
	if len(decision.PositionChanges) < 2 ||
		decision.PositionChanges[0].PositionID != btcPosition ||
		decision.PositionChanges[0].InstrumentID != "BTC-PERP" {
		t.Fatalf("liquidation order = %+v, want BTC largest-notional first", decision.PositionChanges)
	}
	if open := fixture.state.OpenPositions("account-1"); len(open) != 0 {
		t.Fatalf("positions after liquidation = %+v, want flat", open)
	}
}

// Ported from:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/margin/e2e_isolated_liquidation.rs:81
//	test: isolated_position_liquidates_on_its_own_collateral_while_the_account_stays_solvent
//
// Adaptations:
//   - Mark injection and the stop-out tick are explicit ordered inputs.
//
// Assertions preserved:
//   - Isolated collateral, rather than full account equity, triggers the close.
//   - The account remains solvent after the isolated loss is settled.
func TestTradingIsolatedPositionLiquidatesWhileAccountStaysSolvent(t *testing.T) {
	fixture := newTradingFixture(t)
	fixture.setBalance(t, "account-1", "1000", BalanceOperationSet)
	fixture.configureRisk(t, "account-1", "BTC-PERP", MarginModeIsolated, "10")
	order := fixture.submit(t, marketOrder(fixture.id(855), "account-1", SideBuy, "1", nil))
	positionID := fixture.state.FillsForOrder(order.OrderID)[0].PositionID
	position, _ := fixture.state.Position(positionID)
	if position.MarginMode != MarginModeIsolated ||
		position.IsolatedCollateral != "10" {
		t.Fatalf("isolated position = %+v, want collateral 10", position)
	}

	fixture.updateBook(t, "10",
		[]BookLevel{{Price: "10", Quantity: "10"}},
		[]BookLevel{{Price: "11", Quantity: "10"}},
	)
	fixture.apply(t, TradingAction{
		Kind: TradingActionLiquidateAccount,
		LiquidateAccount: &LiquidateAccount{
			AccountID: "account-1",
		},
	})
	if open := fixture.state.OpenPositions("account-1"); len(open) != 0 {
		t.Fatalf("isolated position remained open: %+v", open)
	}
	balance, _ := fixture.state.Balance("account-1", "USDC")
	if balance.Total != "910" {
		t.Fatalf("isolated liquidation total = %s, want solvent 910", balance.Total)
	}
}

// Ported from:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/margin/e2e_isolated_hedging_guard.rs:42
//	test: hedging_account_downgrades_a_stale_isolated_override_to_cross
//
// Adaptations:
//   - The effective mode is inspected directly on the position snapshot.
//
// Assertions preserved:
//   - Hedging positions always resolve cross despite an isolated override.
func TestTradingHedgingDowngradesIsolatedOverrideToCross(t *testing.T) {
	fixture := newTradingFixture(t)
	fixture.configureRisk(t, "account-1", "BTC-PERP", MarginModeIsolated, "10")
	fixture.configureAccount(t, "account-1", OmsModeHedging)
	order := fixture.submit(t, marketOrder(fixture.id(856), "account-1", SideBuy, "1", nil))
	positionID := fixture.state.FillsForOrder(order.OrderID)[0].PositionID
	position, _ := fixture.state.Position(positionID)
	if position.MarginMode != MarginModeCross ||
		position.IsolatedCollateral != "0" {
		t.Fatalf("hedging position margin = %+v, want cross with zero isolated collateral", position)
	}
}

func (fixture *tradingFixture) setBalance(
	t *testing.T,
	accountID string,
	amount string,
	operation BalanceOperation,
) {
	t.Helper()
	fixture.apply(t, TradingAction{
		Kind: TradingActionAdjustBalance,
		AdjustBalance: &AdjustBalance{
			AccountID:     accountID,
			Currency:      "USDC",
			CurrencyScale: 8,
			Operation:     operation,
			Amount:        amount,
		},
	})
}

func (fixture *tradingFixture) configureRisk(
	t *testing.T,
	accountID string,
	instrumentID string,
	marginMode MarginMode,
	leverage string,
) {
	t.Helper()
	fixture.apply(t, TradingAction{
		Kind: TradingActionConfigureRisk,
		ConfigureRisk: &ConfigureRisk{
			AccountID:    accountID,
			InstrumentID: instrumentID,
			MarginMode:   marginMode,
			Leverage:     leverage,
		},
	})
}

func (fixture *tradingFixture) configureInstrument(
	t *testing.T,
	configure ConfigureInstrument,
) {
	t.Helper()
	fixture.apply(t, TradingAction{
		Kind:                TradingActionConfigureInstrument,
		ConfigureInstrument: &configure,
	})
}

func (fixture *tradingFixture) updateInstrumentBook(
	t *testing.T,
	instrumentID string,
	markPrice string,
	bids []BookLevel,
	asks []BookLevel,
) {
	t.Helper()
	fixture.apply(t, TradingAction{
		Kind: TradingActionUpdateBook,
		UpdateBook: &UpdateBook{
			InstrumentID: instrumentID,
			MarkPrice:    markPrice,
			Bids:         bids,
			Asks:         asks,
		},
	})
}
