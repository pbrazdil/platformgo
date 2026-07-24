package market

import (
	"strings"
	"testing"

	"github.com/upcomers-org/platformgo/internal/decimal"
)

func betDecimal(value string) decimal.Decimal {
	return decimal.MustParse(value)
}

func betRequireDecimal(t *testing.T, got decimal.Decimal, want string) {
	t.Helper()
	expected := betDecimal(want)
	if !got.Equal(expected) {
		t.Fatalf("got %s, want %s", got, expected)
	}
}

func betRequireEqual(t *testing.T, got, want Bet) {
	t.Helper()
	if !got.Equal(want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bet.rs:463
//	test: test_from_liability_panics_on_back_side
func TestFromLiabilityPanicsOnBackSide(t *testing.T) {
	defer func() {
		recovered := recover()
		const want = "Liability-based betting is only applicable for Lay side."
		if recovered == nil || !strings.Contains(recovered.(string), want) {
			t.Fatalf("panic = %v, want %q", recovered, want)
		}
	}()
	_ = BetFromLiability(betDecimal("2.0"), betDecimal("100.0"), BetSideBack)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bet.rs:468
//	test: test_bet_creation
func TestBetCreation(t *testing.T) {
	price := betDecimal("2.0")
	stake := betDecimal("100.0")
	side := BetSideBack
	bet := NewBet(price, stake, side)
	if !bet.Price.Equal(price) || !bet.Stake.Equal(stake) || bet.Side != side {
		t.Fatalf("unexpected bet: %+v", bet)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bet.rs:479
//	test: test_display_bet
func TestDisplayBet(t *testing.T) {
	bet := NewBet(betDecimal("2.0"), betDecimal("100.0"), BetSideBack)
	formatted := bet.String()
	for _, want := range []string{"Back", "2.00", "100.00"} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("%q does not contain %q", formatted, want)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bet.rs:488
//	test: test_bet_exposure_back
func TestBetExposureBack(t *testing.T) {
	bet := NewBet(betDecimal("2.0"), betDecimal("100.0"), BetSideBack)
	betRequireDecimal(t, bet.Exposure(), "200.0")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bet.rs:495
//	test: test_bet_exposure_lay
func TestBetExposureLay(t *testing.T) {
	bet := NewBet(betDecimal("2.0"), betDecimal("100.0"), BetSideLay)
	betRequireDecimal(t, bet.Exposure(), "-200.0")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bet.rs:502
//	test: test_bet_liability_back
func TestBetLiabilityBack(t *testing.T) {
	bet := NewBet(betDecimal("2.0"), betDecimal("100.0"), BetSideBack)
	betRequireDecimal(t, bet.Liability(), "100.0")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bet.rs:509
//	test: test_bet_liability_lay
func TestBetLiabilityLay(t *testing.T) {
	bet := NewBet(betDecimal("2.0"), betDecimal("100.0"), BetSideLay)
	betRequireDecimal(t, bet.Liability(), "100.0")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bet.rs:516
//	test: test_bet_profit_back
func TestBetProfitBack(t *testing.T) {
	bet := NewBet(betDecimal("2.0"), betDecimal("100.0"), BetSideBack)
	betRequireDecimal(t, bet.Profit(), "100.0")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bet.rs:523
//	test: test_bet_profit_lay
func TestBetProfitLay(t *testing.T) {
	bet := NewBet(betDecimal("2.0"), betDecimal("100.0"), BetSideLay)
	betRequireDecimal(t, bet.Profit(), "100.0")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bet.rs:530
//	test: test_outcome_win_payoff_back
func TestOutcomeWinPayoffBack(t *testing.T) {
	bet := NewBet(betDecimal("2.0"), betDecimal("100.0"), BetSideBack)
	betRequireDecimal(t, bet.OutcomeWinPayoff(), "100.0")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bet.rs:537
//	test: test_outcome_win_payoff_lay
func TestOutcomeWinPayoffLay(t *testing.T) {
	bet := NewBet(betDecimal("2.0"), betDecimal("100.0"), BetSideLay)
	betRequireDecimal(t, bet.OutcomeWinPayoff(), "-100.0")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bet.rs:544
//	test: test_outcome_lose_payoff_back
func TestOutcomeLosePayoffBack(t *testing.T) {
	bet := NewBet(betDecimal("2.0"), betDecimal("100.0"), BetSideBack)
	betRequireDecimal(t, bet.OutcomeLosePayoff(), "-100.0")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bet.rs:551
//	test: test_outcome_lose_payoff_lay
func TestOutcomeLosePayoffLay(t *testing.T) {
	bet := NewBet(betDecimal("2.0"), betDecimal("100.0"), BetSideLay)
	betRequireDecimal(t, bet.OutcomeLosePayoff(), "100.0")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bet.rs:558
//	test: test_hedging_stake_back
func TestHedgingStakeBack(t *testing.T) {
	bet := NewBet(betDecimal("2.0"), betDecimal("100.0"), BetSideBack)
	got := bet.HedgingStake(betDecimal("1.5")).Quantize(8, decimal.RoundHalfEven)
	betRequireDecimal(t, got, "133.33333333")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bet.rs:566
//	test: test_hedging_bet_lay
func TestHedgingBetLay(t *testing.T) {
	bet := NewBet(betDecimal("2.0"), betDecimal("100.0"), BetSideLay)
	hedge := bet.HedgingBet(betDecimal("1.5"))
	if hedge.Side != BetSideBack {
		t.Fatalf("hedge side = %s", hedge.Side)
	}
	betRequireDecimal(t, hedge.Price, "1.5")
	betRequireDecimal(t, hedge.Stake.Quantize(8, decimal.RoundHalfEven), "133.33333333")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bet.rs:575
//	test: test_bet_position_initialization
func TestBetPositionInitialization(t *testing.T) {
	position := NewBetPosition()
	betRequireDecimal(t, position.Price, "0.0")
	betRequireDecimal(t, position.Exposure, "0.0")
	betRequireDecimal(t, position.RealizedPnL, "0.0")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bet.rs:583
//	test: test_display_bet_position
func TestDisplayBetPosition(t *testing.T) {
	position := NewBetPosition()
	position.AddBet(NewBet(betDecimal("2.0"), betDecimal("100.0"), BetSideBack))
	formatted := position.String()
	for _, want := range []string{"price", "exposure", "realized_pnl"} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("%q does not contain %q", formatted, want)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bet.rs:595
//	test: test_as_bet
func TestAsBet(t *testing.T) {
	position := NewBetPosition()
	position.AddBet(NewBet(betDecimal("2.0"), betDecimal("100.0"), BetSideBack))
	asBet, ok := position.AsBet()
	if !ok {
		t.Fatal("expected bet representation")
	}
	betRequireDecimal(t, asBet.Price, position.Price.String())
	betRequireDecimal(t, asBet.Stake, betQuo(position.Exposure, position.Price).String())
	if asBet.Side != BetSideBack {
		t.Fatalf("side = %s", asBet.Side)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bet.rs:608
//	test: test_reset_position
func TestResetPosition(t *testing.T) {
	position := NewBetPosition()
	position.AddBet(NewBet(betDecimal("2.0"), betDecimal("100.0"), BetSideBack))
	if position.Exposure.IsZero() || len(position.Bets) == 0 {
		t.Fatal("position did not open")
	}
	position.Reset()
	betRequireDecimal(t, position.Price, "0.0")
	betRequireDecimal(t, position.Exposure, "0.0")
	betRequireDecimal(t, position.RealizedPnL, "0.0")
	if len(position.Bets) != 0 {
		t.Fatalf("bets remain: %v", position.Bets)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bet.rs:624
//	test: test_bet_position_side_none
func TestBetPositionSideNone(t *testing.T) {
	position := NewBetPosition()
	if _, ok := position.Side(); ok {
		t.Fatal("empty position has a side")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bet.rs:630
//	test: test_bet_position_side_back
func TestBetPositionSideBack(t *testing.T) {
	position := NewBetPosition()
	position.AddBet(NewBet(betDecimal("2.0"), betDecimal("100.0"), BetSideBack))
	side, ok := position.Side()
	if !ok || side != BetSideBack {
		t.Fatalf("side = %s, present=%v", side, ok)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bet.rs:638
//	test: test_bet_position_side_lay
func TestBetPositionSideLay(t *testing.T) {
	position := NewBetPosition()
	position.AddBet(NewBet(betDecimal("2.0"), betDecimal("100.0"), BetSideLay))
	side, ok := position.Side()
	if !ok || side != BetSideLay {
		t.Fatalf("side = %s, present=%v", side, ok)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bet.rs:646
//	test: test_position_increase_back
func TestPositionIncreaseBack(t *testing.T) {
	position := NewBetPosition()
	position.AddBet(NewBet(betDecimal("2.0"), betDecimal("100.0"), BetSideBack))
	position.AddBet(NewBet(betDecimal("2.0"), betDecimal("50.0"), BetSideBack))
	betRequireDecimal(t, position.Exposure, "300.0")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bet.rs:657
//	test: test_position_increase_lay
func TestPositionIncreaseLay(t *testing.T) {
	position := NewBetPosition()
	position.AddBet(NewBet(betDecimal("2.0"), betDecimal("100.0"), BetSideLay))
	position.AddBet(NewBet(betDecimal("2.0"), betDecimal("50.0"), BetSideLay))
	betRequireDecimal(t, position.Exposure, "-300.0")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bet.rs:668
//	test: test_position_back_then_lay
func TestPositionBackThenLay(t *testing.T) {
	position := NewBetPosition()
	position.AddBet(NewBet(betDecimal("3.0"), betDecimal("100000"), BetSideBack))
	position.AddBet(NewBet(betDecimal("2.0"), betDecimal("10000"), BetSideLay))
	betRequireDecimal(t, position.Exposure, "280000.0")
	betRequireDecimal(t, position.RealizedPnL, "3333.333333333333333333333333")
	betRequireDecimal(t, position.UnrealizedPnL(betDecimal("4.0")), "-23333.33333333333333333333334")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bet.rs:684
//	test: test_position_lay_then_back
func TestPositionLayThenBack(t *testing.T) {
	position := NewBetPosition()
	position.AddBet(NewBet(betDecimal("2.0"), betDecimal("10000"), BetSideLay))
	position.AddBet(NewBet(betDecimal("3.0"), betDecimal("100000"), BetSideBack))
	betRequireDecimal(t, position.Exposure, "280000.0")
	betRequireDecimal(t, position.RealizedPnL, "190000")
	betRequireDecimal(t, position.UnrealizedPnL(betDecimal("4.0")), "-23333.33333333333333333333334")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bet.rs:700
//	test: test_position_flip
func TestPositionFlip(t *testing.T) {
	position := NewBetPosition()
	position.AddBet(NewBet(betDecimal("2.0"), betDecimal("100.0"), BetSideBack))
	position.AddBet(NewBet(betDecimal("2.0"), betDecimal("150.0"), BetSideLay))
	side, ok := position.Side()
	if !ok || side != BetSideLay {
		t.Fatalf("side = %s, present=%v", side, ok)
	}
	betRequireDecimal(t, position.Exposure, "-100.0")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bet.rs:712
//	test: test_position_flat
func TestPositionFlat(t *testing.T) {
	position := NewBetPosition()
	position.AddBet(NewBet(betDecimal("2.0"), betDecimal("100.0"), BetSideBack))
	position.AddBet(NewBet(betDecimal("2.0"), betDecimal("100.0"), BetSideLay))
	if _, ok := position.Side(); ok {
		t.Fatal("flat position has a side")
	}
	betRequireDecimal(t, position.Exposure, "0.0")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bet.rs:723
//	test: test_unrealized_pnl_negative
func TestUnrealizedPnLNegative(t *testing.T) {
	position := NewBetPosition()
	position.AddBet(NewBet(betDecimal("2.0"), betDecimal("100.0"), BetSideBack))
	betRequireDecimal(t, position.UnrealizedPnL(betDecimal("2.5")), "-20.0")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bet.rs:733
//	test: test_total_pnl
func TestTotalPnL(t *testing.T) {
	position := NewBetPosition()
	position.AddBet(NewBet(betDecimal("2.0"), betDecimal("100.0"), BetSideBack))
	position.RealizedPnL = betDecimal("10.0")
	betRequireDecimal(t, position.TotalPnL(betDecimal("2.5")), "-10.0")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bet.rs:744
//	test: test_flattening_bet_back_profit
func TestFlatteningBetBackProfit(t *testing.T) {
	position := NewBetPosition()
	position.AddBet(NewBet(betDecimal("2.0"), betDecimal("100.0"), BetSideBack))
	flatteningBet, ok := position.FlatteningBet(betDecimal("1.6"))
	if !ok {
		t.Fatal("expected flattening bet")
	}
	if flatteningBet.Side != BetSideLay {
		t.Fatalf("side = %s", flatteningBet.Side)
	}
	betRequireDecimal(t, flatteningBet.Stake, "125")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bet.rs:756
//	test: test_flattening_bet_back_hack
func TestFlatteningBetBackHack(t *testing.T) {
	position := NewBetPosition()
	position.AddBet(NewBet(betDecimal("2.0"), betDecimal("100.0"), BetSideBack))
	flatteningBet, ok := position.FlatteningBet(betDecimal("2.5"))
	if !ok || flatteningBet.Side != BetSideLay {
		t.Fatalf("unexpected flattening bet: %+v, present=%v", flatteningBet, ok)
	}
	betRequireDecimal(t, flatteningBet.Stake, "80.0")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bet.rs:769
//	test: test_flattening_bet_lay
func TestFlatteningBetLay(t *testing.T) {
	position := NewBetPosition()
	position.AddBet(NewBet(betDecimal("2.0"), betDecimal("100.0"), BetSideLay))
	flatteningBet, ok := position.FlatteningBet(betDecimal("1.5"))
	if !ok || flatteningBet.Side != BetSideBack {
		t.Fatalf("unexpected flattening bet: %+v, present=%v", flatteningBet, ok)
	}
	betRequireDecimal(t, flatteningBet.Stake.Quantize(8, decimal.RoundHalfEven), "133.33333333")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bet.rs:781
//	test: test_realized_pnl_flattening
func TestRealizedPnLFlattening(t *testing.T) {
	position := NewBetPosition()
	position.AddBet(NewBet(betDecimal("5.0"), betDecimal("100.0"), BetSideBack))
	position.AddBet(NewBet(betDecimal("4.0"), betDecimal("125.0"), BetSideLay))
	betRequireDecimal(t, position.RealizedPnL, "25.0")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bet.rs:792
//	test: test_realized_pnl_single_side
func TestRealizedPnLSingleSide(t *testing.T) {
	position := NewBetPosition()
	position.AddBet(NewBet(betDecimal("5.0"), betDecimal("100.0"), BetSideBack))
	betRequireDecimal(t, position.RealizedPnL, "0.0")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bet.rs:801
//	test: test_realized_pnl_open_position
func TestRealizedPnLOpenPosition(t *testing.T) {
	position := NewBetPosition()
	position.AddBet(NewBet(betDecimal("5.0"), betDecimal("100.0"), BetSideBack))
	position.AddBet(NewBet(betDecimal("4.0"), betDecimal("100.0"), BetSideLay))
	betRequireDecimal(t, position.RealizedPnL, "20.0")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bet.rs:812
//	test: test_realized_pnl_partial_close
func TestRealizedPnLPartialClose(t *testing.T) {
	position := NewBetPosition()
	position.AddBet(NewBet(betDecimal("5.0"), betDecimal("100.0"), BetSideBack))
	position.AddBet(NewBet(betDecimal("4.0"), betDecimal("110.0"), BetSideLay))
	betRequireDecimal(t, position.RealizedPnL, "22.0")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bet.rs:823
//	test: test_realized_pnl_flipping
func TestRealizedPnLFlipping(t *testing.T) {
	position := NewBetPosition()
	position.AddBet(NewBet(betDecimal("5.0"), betDecimal("100.0"), BetSideBack))
	position.AddBet(NewBet(betDecimal("4.0"), betDecimal("130.0"), BetSideLay))
	betRequireDecimal(t, position.RealizedPnL, "10.0")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bet.rs:834
//	test: test_unrealized_pnl_positive
func TestUnrealizedPnLPositive(t *testing.T) {
	position := NewBetPosition()
	position.AddBet(NewBet(betDecimal("5.0"), betDecimal("100.0"), BetSideBack))
	betRequireDecimal(t, position.UnrealizedPnL(betDecimal("4.0")), "25.0")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bet.rs:844
//	test: test_total_pnl_with_pnl
func TestTotalPnLWithPnL(t *testing.T) {
	position := NewBetPosition()
	position.AddBet(NewBet(betDecimal("5.0"), betDecimal("100.0"), BetSideBack))
	position.AddBet(NewBet(betDecimal("4.0"), betDecimal("120.0"), BetSideLay))
	betRequireDecimal(t, position.RealizedPnL, "24.0")
	betRequireDecimal(t, position.UnrealizedPnL(betDecimal("4.0")), "1.0")
	betRequireDecimal(t, position.TotalPnL(betDecimal("4.0")), "25.0")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bet.rs:860
//	test: test_open_position_realized_unrealized
func TestOpenPositionRealizedUnrealized(t *testing.T) {
	position := NewBetPosition()
	position.AddBet(NewBet(betDecimal("5.0"), betDecimal("100.0"), BetSideBack))
	position.AddBet(NewBet(betDecimal("4.0"), betDecimal("100.0"), BetSideLay))
	betRequireDecimal(t, position.UnrealizedPnL(betDecimal("4.0")), "5.0")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bet.rs:872
//	test: test_unrealized_no_position
func TestUnrealizedNoPosition(t *testing.T) {
	position := NewBetPosition()
	position.AddBet(NewBet(betDecimal("5.0"), betDecimal("100.0"), BetSideLay))
	betRequireDecimal(t, position.UnrealizedPnL(betDecimal("5.0")), "0.0")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bet.rs:881
//	test: test_calc_bets_pnl_single_back_bet
func TestCalcBetsPnLSingleBackBet(t *testing.T) {
	bet := NewBet(betDecimal("5.0"), betDecimal("100.0"), BetSideBack)
	betRequireDecimal(t, CalcBetsPnL([]Bet{bet}), "400.0")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bet.rs:888
//	test: test_calc_bets_pnl_single_lay_bet
func TestCalcBetsPnLSingleLayBet(t *testing.T) {
	bet := NewBet(betDecimal("4.0"), betDecimal("100.0"), BetSideLay)
	betRequireDecimal(t, CalcBetsPnL([]Bet{bet}), "-300.0")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bet.rs:895
//	test: test_calc_bets_pnl_multiple_bets
func TestCalcBetsPnLMultipleBets(t *testing.T) {
	back := NewBet(betDecimal("5.0"), betDecimal("100.0"), BetSideBack)
	lay := NewBet(betDecimal("4.0"), betDecimal("100.0"), BetSideLay)
	betRequireDecimal(t, CalcBetsPnL([]Bet{back, lay}), "100.0")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bet.rs:904
//	test: test_calc_bets_pnl_mixed_bets
func TestCalcBetsPnLMixedBets(t *testing.T) {
	back1 := NewBet(betDecimal("5.0"), betDecimal("100.0"), BetSideBack)
	back2 := NewBet(betDecimal("2.0"), betDecimal("50.0"), BetSideBack)
	lay1 := NewBet(betDecimal("3.0"), betDecimal("75.0"), BetSideLay)
	betRequireDecimal(t, CalcBetsPnL([]Bet{back1, back2, lay1}), "300.0")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bet.rs:914
//	test: test_calc_bets_pnl_no_bets
func TestCalcBetsPnLNoBets(t *testing.T) {
	betRequireDecimal(t, CalcBetsPnL(nil), "0.0")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bet.rs:921
//	test: test_calc_bets_pnl_zero_outcome
func TestCalcBetsPnLZeroOutcome(t *testing.T) {
	back := NewBet(betDecimal("5.0"), betDecimal("100.0"), BetSideBack)
	lay := NewBet(betDecimal("5.0"), betDecimal("100.0"), BetSideLay)
	betRequireDecimal(t, CalcBetsPnL([]Bet{back, lay}), "0.0")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bet.rs:929
//	test: test_probability_to_bet_back_simple
func TestProbabilityToBetBackSimple(t *testing.T) {
	bet, err := ProbabilityToBet(betDecimal("0.50"), betDecimal("50.0"), BetOrderSideBuy)
	if err != nil {
		t.Fatalf("probability to bet: %v", err)
	}
	betRequireEqual(t, bet, NewBet(betDecimal("2.0"), betDecimal("25.0"), BetSideBack))
	betRequireDecimal(t, bet.OutcomeWinPayoff(), "25.0")
	betRequireDecimal(t, bet.OutcomeLosePayoff(), "-25.0")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bet.rs:939
//	test: test_probability_to_bet_back_high_prob
func TestProbabilityToBetBackHighProb(t *testing.T) {
	bet, err := ProbabilityToBet(betDecimal("0.64"), betDecimal("50.0"), BetOrderSideBuy)
	if err != nil {
		t.Fatalf("probability to bet: %v", err)
	}
	betRequireEqual(t, bet, NewBet(betDecimal("1.5625"), betDecimal("32.0"), BetSideBack))
	betRequireDecimal(t, bet.OutcomeWinPayoff(), "18.0")
	betRequireDecimal(t, bet.OutcomeLosePayoff(), "-32.0")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bet.rs:948
//	test: test_probability_to_bet_back_low_prob
func TestProbabilityToBetBackLowProb(t *testing.T) {
	bet, err := ProbabilityToBet(betDecimal("0.40"), betDecimal("50.0"), BetOrderSideBuy)
	if err != nil {
		t.Fatalf("probability to bet: %v", err)
	}
	betRequireEqual(t, bet, NewBet(betDecimal("2.5"), betDecimal("20.0"), BetSideBack))
	betRequireDecimal(t, bet.OutcomeWinPayoff(), "30.0")
	betRequireDecimal(t, bet.OutcomeLosePayoff(), "-20.0")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bet.rs:957
//	test: test_probability_to_bet_sell
func TestProbabilityToBetSell(t *testing.T) {
	bet, err := ProbabilityToBet(betDecimal("0.80"), betDecimal("50.0"), BetOrderSideSell)
	if err != nil {
		t.Fatalf("probability to bet: %v", err)
	}
	betRequireEqual(t, bet, NewBet(betDecimal("1.25"), betDecimal("40"), BetSideLay))
	betRequireDecimal(t, bet.OutcomeWinPayoff(), "-10")
	betRequireDecimal(t, bet.OutcomeLosePayoff(), "40")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bet.rs:966
//	test: test_inverse_probability_to_bet
func TestInverseProbabilityToBet(t *testing.T) {
	original, err := ProbabilityToBet(betDecimal("0.80"), betDecimal("100.0"), BetOrderSideSell)
	if err != nil {
		t.Fatal(err)
	}
	reverse, err := ProbabilityToBet(betDecimal("0.20"), betDecimal("100.0"), BetOrderSideBuy)
	if err != nil {
		t.Fatal(err)
	}
	inverse, err := InverseProbabilityToBet(betDecimal("0.80"), betDecimal("100.0"), BetOrderSideSell)
	if err != nil {
		t.Fatal(err)
	}
	if !original.OutcomeWinPayoff().Equal(reverse.OutcomeLosePayoff()) ||
		!original.OutcomeWinPayoff().Equal(inverse.OutcomeLosePayoff()) ||
		!original.OutcomeLosePayoff().Equal(reverse.OutcomeWinPayoff()) ||
		!original.OutcomeLosePayoff().Equal(inverse.OutcomeWinPayoff()) {
		t.Fatal("inverse outcome payoffs differ")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/bet.rs:995
//	test: test_inverse_probability_to_bet_example2
func TestInverseProbabilityToBetExample2(t *testing.T) {
	original, err := ProbabilityToBet(betDecimal("0.64"), betDecimal("50.0"), BetOrderSideSell)
	if err != nil {
		t.Fatal(err)
	}
	inverse, err := InverseProbabilityToBet(betDecimal("0.64"), betDecimal("50.0"), BetOrderSideSell)
	if err != nil {
		t.Fatal(err)
	}
	betRequireDecimal(t, original.Stake, "32.0")
	betRequireDecimal(t, original.OutcomeWinPayoff(), "-18.0")
	betRequireDecimal(t, original.OutcomeLosePayoff(), "32.0")
	betRequireDecimal(t, inverse.Stake, "18.0")
	betRequireDecimal(t, inverse.OutcomeWinPayoff(), "32.0")
	betRequireDecimal(t, inverse.OutcomeLosePayoff(), "-18.0")
}
