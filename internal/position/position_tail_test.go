package position

import (
	"fmt"
	"testing"

	"github.com/upcomers-org/platformgo/internal/decimal"
)

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/position.rs:3913
//	test: test_purge_events_for_order_clears_adjustments_when_flat
func TestPurgeEventsForOrderClearsAdjustmentsWhenFlat(t *testing.T) {
	position, _ := New(btcusdtSpot(), "1", fill("O-1", "T-1", Buy, "1", "10000", cashPtr("0.001", btc), 0))
	position.PurgeEventsForOrder("O-1")
	if position.Side != Flat || position.EventCount() != 0 || len(position.Adjustments) != 0 || !position.Quantity.IsZero() {
		t.Fatalf("unexpected empty position: %+v", position)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/position.rs:3955
//	test: test_purge_events_for_order_clears_adjustments_on_rebuild
func TestPurgeEventsForOrderClearsAdjustmentsOnRebuild(t *testing.T) {
	position, _ := New(btcusdtSpot(), "1", fill("O-1", "T-1", Buy, "1", "10000", cashPtr("0.001", btc), 0))
	_ = position.Apply(fill("O-2", "T-2", Buy, "2", "10000", cashPtr("0.002", btc), 1))
	position.PurgeEventsForOrder("O-1")
	if position.EventCount() != 1 || len(position.Adjustments) != 1 {
		t.Fatalf("unexpected rebuilt counts")
	}
	requireDec(t, position.Quantity, "1.998")
	requireDec(t, *position.Adjustments[0].QuantityChange, "-0.002")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/position.rs:4030
//	test: test_purge_events_preserves_manual_adjustments
func TestPurgeEventsPreservesManualAdjustments(t *testing.T) {
	position, _ := New(btcusdtSpot(), "1", fill("O-1", "T-1", Buy, "1", "10000", cashPtr("0.001", btc), 0))
	_ = position.Apply(fill("O-2", "T-2", Buy, "2", "10000", cashPtr("0.002", btc), 1))
	position.ApplyAdjustment(Adjustment{Type: Funding, PnLChange: cashPtr("10", usdt), TsEvent: 2})
	position.PurgeEventsForOrder("O-1")
	if position.EventCount() != 1 || len(position.Adjustments) != 2 {
		t.Fatalf("manual adjustment lost: %+v", position.Adjustments)
	}
	requireMoney(t, position.RealizedPnL, "10", usdt)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/position.rs:4132
//	test: test_position_commission_affects_buy_and_sell_qty
func TestPositionCommissionAffectsBuyAndSellQty(t *testing.T) {
	position, _ := New(btcusdtSpot(), "1", fill("O-1", "T-1", Buy, "1", "10000", cashPtr("0.001", btc), 0))
	_ = position.Apply(fill("O-2", "T-2", Buy, "1", "10000", nil, 1))
	requireDec(t, position.Quantity, "1.999")
	requireDec(t, position.BuyQuantity, "2")
	if len(position.Adjustments) != 1 {
		t.Fatal("missing commission adjustment")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/position.rs:4182
//	test: test_position_perpetual_commission_no_adjustment
func TestPositionPerpetualCommissionNoAdjustment(t *testing.T) {
	instrument := btcusdtSpot()
	instrument.ID, instrument.CurrencyPair = "ETHUSDT.PERP", false
	position, _ := New(instrument, "1", fill("O", "T", Buy, "1", "2000", cashPtr("0.001", btc), 0))
	requireDec(t, position.SignedQuantity, "1")
	if len(position.Adjustments) != 0 {
		t.Fatal("perpetual commission adjusted quantity")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/position.rs:4225
//	test: test_signed_decimal_qty_long
func TestSignedDecimalQtyLong(t *testing.T) {
	position, _ := New(btcusdtSpot(), "1", fill("O", "T", Buy, "1.25", "1", nil, 0))
	if position.SignedQuantity.Sign() <= 0 {
		t.Fatal(position.SignedQuantity)
	}
	requireDec(t, position.SignedQuantity, "1.25")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/position.rs:4235
//	test: test_signed_decimal_qty_short
func TestSignedDecimalQtyShort(t *testing.T) {
	position, _ := New(btcusdtSpot(), "1", fill("O", "T", Sell, "1.25", "1", nil, 0))
	if position.SignedQuantity.Sign() >= 0 {
		t.Fatal(position.SignedQuantity)
	}
	requireDec(t, position.SignedQuantity, "-1.25")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/position.rs:4245
//	test: test_signed_decimal_qty_flat
func TestSignedDecimalQtyFlat(t *testing.T) {
	position, _ := New(btcusdtSpot(), "1", fill("O-1", "T-1", Buy, "1", "1", nil, 0))
	_ = position.Apply(fill("O-2", "T-2", Sell, "1", "1", nil, 1))
	requireDec(t, position.SignedQuantity, "0")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/position.rs:4290
//	test: test_position_flat_with_floating_point_precision_edge_case
func TestPositionFlatWithFloatingPointPrecisionEdgeCase(t *testing.T) {
	position, _ := New(btcusdtSpot(), "1", fill("O-1", "T-1", Buy, "0.123456789", "10000", nil, 0))
	_ = position.Apply(fill("O-2", "T-2", Sell, "0.123456789", "10000", nil, 1))
	if position.Side != Flat || !position.SignedQuantity.IsZero() || !position.IsClosed() {
		t.Fatalf("exact close failed")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/position.rs:4358
//	test: test_position_adjustment_floating_point_precision_edge_case
func TestPositionAdjustmentFloatingPointPrecisionEdgeCase(t *testing.T) {
	position, _ := New(btcusdtSpot(), "1", fill("O", "T", Buy, "1", "1", nil, 0))
	position.ApplyAdjustment(Adjustment{Type: Funding, QuantityChange: decPtr("-1"), TsEvent: 1})
	if position.Side != Flat || !position.SignedQuantity.IsZero() {
		t.Fatalf("exact adjustment close failed")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/position.rs:4414
//	test: test_position_spot_buy_partial_fills_with_base_commission
func TestPositionSpotBuyPartialFillsWithBaseCommission(t *testing.T) {
	position, _ := New(ethusdtSpot(), "1", fill("O-1", "T-1", Buy, "0.0035", "2000", cashPtr("0.00001", eth), 0))
	requireDec(t, position.Quantity, "0.00349")
	_ = position.Apply(fill("O-2", "T-2", Buy, "0.0035", "2000", cashPtr("0.00001", eth), 1))
	requireDec(t, position.Quantity, "0.00698")
	_ = position.Apply(fill("O-3", "T-3", Buy, "0.003", "2000", cashPtr("0.00001", eth), 2))
	requireDec(t, position.Quantity, "0.00997")
	requireDec(t, position.BuyQuantity, "0.0100")
	if len(position.Adjustments) != 3 {
		t.Fatalf("got %d adjustments", len(position.Adjustments))
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/position.rs:4508
//	test: test_position_spot_sell_partial_fills_with_base_commission
func TestPositionSpotSellPartialFillsWithBaseCommission(t *testing.T) {
	position, _ := New(btcusdtSpot(), "1", fill("O-1", "T-1", Sell, "0.5", "10000", cashPtr("0.001", btc), 0))
	requireDec(t, position.SignedQuantity, "-0.501")
	_ = position.Apply(fill("O-2", "T-2", Sell, "0.5", "10000", cashPtr("0.001", btc), 1))
	requireDec(t, position.SignedQuantity, "-1.002")
	requireDec(t, position.SellQuantity, "1")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/position.rs:4567
//	test: test_position_spot_round_trip_close_flat_with_quote_commission
func TestPositionSpotRoundTripCloseFlatWithQuoteCommission(t *testing.T) {
	position, _ := New(ethusdtSpot(), "1", fill("O-1", "T-1", Buy, "1", "2000", cashPtr("0.001", eth), 0))
	_ = position.Apply(fill("O-2", "T-2", Sell, "0.999", "2100", cashPtr("2", usdt), 1))
	if position.Side != Flat {
		t.Fatalf("expected flat, got %s", position.Side)
	}
	requireMoney(t, position.RealizedPnL, "97.9", usdt)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/position.rs:4632
//	test: test_position_spot_commission_accumulation_multiple_partial_fills
func TestPositionSpotCommissionAccumulationMultiplePartialFills(t *testing.T) {
	position, _ := New(ethusdtSpot(), "1", fill("O-1", "T-1", Buy, "0.5", "2000", cashPtr("0.0005", eth), 0))
	_ = position.Apply(fill("O-2", "T-2", Buy, "0.5", "2000", cashPtr("0.0005", eth), 1))
	requireDec(t, position.Quantity, "0.999")
	requireDec(t, position.BuyQuantity, "1")
	if len(position.Adjustments) != 2 {
		t.Fatal("missing adjustments")
	}
	requireMoney(t, &position.Commissions()[0], "0.001", eth)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/position.rs:4702
//	test: test_position_apply_fill_with_earlier_timestamp_adjusts_ts_opened
func TestPositionApplyFillWithEarlierTimestampAdjustsTsOpened(t *testing.T) {
	position, _ := New(audusd(), "1", fill("O-1", "T-1", Buy, "1", "1", nil, 2000))
	_ = position.Apply(fill("O-2", "T-2", Buy, "1", "1", nil, 1000))
	if position.TsOpened != 2000 || position.OpeningOrderID != "O-1" {
		t.Fatalf("opening identity moved: %d %s", position.TsOpened, position.OpeningOrderID)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/position.rs:4753
//	test: test_position_close_before_open_clamps_duration
func TestPositionCloseBeforeOpenClampsDuration(t *testing.T) {
	position, _ := New(audusd(), "1", fill("O-1", "T-1", Buy, "1", "1", nil, 2000))
	_ = position.Apply(fill("O-2", "T-2", Sell, "1", "1", nil, 1000))
	if position.TsClosed == nil || *position.TsClosed != 1000 || position.TsOpened != 2000 || position.Duration != 0 {
		t.Fatalf("duration not clamped: %+v", position)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/position.rs:4804
//	test: test_position_commissions_multi_currency_insertion_order
func TestPositionCommissionsMultiCurrencyInsertionOrder(t *testing.T) {
	position, _ := New(audusd(), "1", fill("O-1", "T-1", Buy, "1", "1", cashPtr("1.5", usd), 0))
	_ = position.Apply(fill("O-2", "T-2", Buy, "1", "1", cashPtr("2", usdt), 1))
	_ = position.Apply(fill("O-3", "T-3", Buy, "1", "1", cashPtr("0.0001", btc), 2))
	got := position.Commissions()
	if len(got) != 3 || !got[0].Equal(cash("1.5", usd)) || !got[1].Equal(cash("2", usdt)) || !got[2].Equal(cash("0.0001", btc)) {
		t.Fatalf("commission order changed: %v", got)
	}
}

func leg(qty, price string, timestamp uint64) NetLeg {
	return NetLeg{SignedQuantity: dec(qty), AverageOpen: dec(price), TsOpened: timestamp}
}

func requireFold(t *testing.T, legs []NetLeg, quantity, average string) {
	t.Helper()
	gotQuantity, gotAverage := FoldNetPosition(legs)
	requireDec(t, gotQuantity, quantity)
	requireDec(t, gotAverage, average)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/position.rs:4885
//	test: test_fold_net_position_empty
func TestFoldNetPositionEmpty(t *testing.T) { requireFold(t, nil, "0", "0") }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/position.rs:4892
//	test: test_fold_net_position_single_long
func TestFoldNetPositionSingleLong(t *testing.T) {
	requireFold(t, []NetLeg{leg("100", "1.5", 1)}, "100", "1.5")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/position.rs:4900
//	test: test_fold_net_position_single_short
func TestFoldNetPositionSingleShort(t *testing.T) {
	requireFold(t, []NetLeg{leg("-100", "1.5", 1)}, "-100", "1.5")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/position.rs:4908
//	test: test_fold_net_position_same_side_weighted_average
func TestFoldNetPositionSameSideWeightedAverage(t *testing.T) {
	requireFold(t, []NetLeg{leg("100", "1", 1), leg("200", "0.5", 2)}, "300", "0.6666666666666666666666666667")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/position.rs:4918
//	test: test_fold_net_position_partial_close_preserves_avg
func TestFoldNetPositionPartialClosePreservesAvg(t *testing.T) {
	requireFold(t, []NetLeg{leg("300", "0.8", 1), leg("-100", "1", 2)}, "200", "0.8")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/position.rs:4930
//	test: test_fold_net_position_full_close
func TestFoldNetPositionFullClose(t *testing.T) {
	requireFold(t, []NetLeg{leg("100", "1", 1), leg("-100", "2", 2)}, "0", "0")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/position.rs:4938
//	test: test_fold_net_position_single_flip_uses_flipping_price
func TestFoldNetPositionSingleFlipUsesFlippingPrice(t *testing.T) {
	requireFold(t, []NetLeg{leg("100", "1", 1), leg("-50", "2", 2), leg("-100", "3", 3)}, "-50", "3")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/position.rs:4951
//	test: test_fold_net_position_double_flip
func TestFoldNetPositionDoubleFlip(t *testing.T) {
	requireFold(t, []NetLeg{leg("50", "1", 1), leg("-100", "2", 2), leg("100", "3", 3)}, "50", "3")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/position.rs:4964
//	test: test_fold_net_position_zero_quantity_legs_skipped
func TestFoldNetPositionZeroQuantityLegsSkipped(t *testing.T) {
	requireFold(t, []NetLeg{leg("100", "1", 1), leg("0", "99", 2), leg("50", "2", 3)}, "150", "1.3333333333333333333333333333")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/position.rs:4978
//	test: test_fold_net_position_stable_sort_preserves_input_order_for_equal_ts
func TestFoldNetPositionStableSortPreservesInputOrderForEqualTs(t *testing.T) {
	requireFold(t, []NetLeg{leg("100", "1", 1), leg("-150", "2", 1)}, "-50", "2")
	requireFold(t, []NetLeg{leg("-150", "2", 1), leg("100", "1", 1)}, "-50", "2")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/position.rs:5003
//	test: test_fold_net_position_close_then_reopen
func TestFoldNetPositionCloseThenReopen(t *testing.T) {
	requireFold(t, []NetLeg{leg("100", "1", 1), leg("-100", "2", 2), leg("50", "3", 3)}, "50", "3")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/position.rs:5016
//	test: test_fold_net_position_orders_by_ts_opened
func TestFoldNetPositionOrdersByTsOpened(t *testing.T) {
	ordered := []NetLeg{leg("100", "1", 1), leg("-150", "2", 2), leg("100", "3", 3)}
	shuffled := []NetLeg{ordered[2], ordered[0], ordered[1]}
	q1, a1 := FoldNetPosition(ordered)
	q2, a2 := FoldNetPosition(shuffled)
	if !q1.Equal(q2) || !a1.Equal(a2) {
		t.Fatalf("sort changed result: %s/%s vs %s/%s", q1, a1, q2, a2)
	}
}

func referenceFold(legs []NetLeg) (decimal.Decimal, decimal.Decimal) {
	net, average := decimal.Decimal{}, decimal.Decimal{}
	for _, current := range legs {
		if current.SignedQuantity.IsZero() {
			continue
		}
		if net.IsZero() {
			net, average = current.SignedQuantity, current.AverageOpen
			continue
		}
		next := net.Add(current.SignedQuantity)
		if net.Sign() == current.SignedQuantity.Sign() {
			numerator := average.Mul(absDecimal(net)).Add(current.AverageOpen.Mul(absDecimal(current.SignedQuantity)))
			average, _ = numerator.Quo(absDecimal(next), 28, decimal.RoundHalfEven)
		} else if next.IsZero() {
			average = decimal.Decimal{}
		} else if next.Sign() != net.Sign() {
			average = current.AverageOpen
		}
		net = next
	}
	return net, average
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/position.rs:5095
//	test: prop_fold_matches_netting_replay
func TestPropFoldMatchesNettingReplay(t *testing.T) {
	var state uint64 = 0x116c9b5159
	next := func(limit uint64) uint64 {
		state = state*6364136223846793005 + 1442695040888963407
		return state%limit + 1
	}
	for sample := 0; sample < 500; sample++ {
		count := int(next(5))
		legs := make([]NetLeg, 0, count)
		running := int64(0)
		for index := 0; index < count; index++ {
			quantity := int64(next(999))
			if next(2) == 1 {
				quantity = -quantity
			}
			if running+quantity == 0 {
				quantity++
			}
			running += quantity
			legs = append(legs, leg(fmt.Sprint(quantity), fmt.Sprint(next(99)), uint64(index)))
		}
		gotQuantity, gotAverage := FoldNetPosition(legs)
		wantQuantity, wantAverage := referenceFold(legs)
		if !gotQuantity.Equal(wantQuantity) || !gotAverage.Equal(wantAverage) {
			t.Fatalf("sample %d: got %s/%s, want %s/%s", sample, gotQuantity, gotAverage, wantQuantity, wantAverage)
		}
	}
}
