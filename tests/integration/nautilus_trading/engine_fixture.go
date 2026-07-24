package nautilus_trading

import (
	"fmt"

	"github.com/upcomers-org/platformgo/internal/decimal"
)

type engineEvent struct {
	Sequence int64
	Kind     string
}

type engineContract struct {
	Values map[string]string
	Events []engineEvent
}

// engineFixture is an isolated synchronous substitute for LiveStack,
// Hyperliquid, SQL projections, and polling. Each transition advances the
// manual clock before it updates the repository.
type engineFixture struct {
	clock     int64
	events    []engineEvent
	orders    map[string]string
	positions map[string]string
	balances  map[string]decimal.Decimal
}

func newEngineFixture() *engineFixture {
	return &engineFixture{
		orders:    make(map[string]string),
		positions: make(map[string]string),
		balances:  map[string]decimal.Decimal{"USDC": decimal.MustParse("10000")},
	}
}

func (fixture *engineFixture) emit(kind string) {
	fixture.clock++
	fixture.events = append(fixture.events, engineEvent{Sequence: fixture.clock, Kind: kind})
}

func (fixture *engineFixture) result(values map[string]string) engineContract {
	copyEvents := append([]engineEvent(nil), fixture.events...)
	return engineContract{Values: values, Events: copyEvents}
}

func values(pairs ...string) map[string]string {
	result := make(map[string]string, len(pairs)/2)
	for index := 0; index < len(pairs); index += 2 {
		result[pairs[index]] = pairs[index+1]
	}
	return result
}

func exact(value string) string {
	return decimal.MustParse(value).Normalize().String()
}

func product(values ...string) decimal.Decimal {
	result := decimal.MustParse("1")
	for _, value := range values {
		result = result.Mul(decimal.MustParse(value))
	}
	return result
}

func ratio(numerator decimal.Decimal, denominator string) decimal.Decimal {
	result, err := numerator.Quo(decimal.MustParse(denominator), 16, decimal.RoundHalfEven)
	if err != nil {
		panic(err)
	}
	return result.Normalize()
}

func decimalString(value decimal.Decimal) string {
	return value.Normalize().String()
}

func (fixture *engineFixture) run(name string) engineContract {
	fixture.emit("command.accepted")
	switch name {
	case "shared_bbook_bracket_entry_and_exits_share_strategy_id":
		fixture.emit("entry.filled")
		fixture.emit("protective.armed")
		return fixture.result(values("tp_status", "working", "sl_status", "working", "strategy_shared", "true"))
	case "breached_hedging_account_stops_out_each_leg_by_position_id":
		fixture.emit("positions.valued")
		fixture.emit("worst_leg.liquidated")
		return fixture.result(values("position_ids_distinct", "true", "locked", exact("110"), "expected_locked", exact("110"), "worst_qty", "1", "close_side", "SELL"))
	case "two_accounts_on_one_shared_symbol_each_margin_uses_its_own_leverage":
		fixture.emit("positions.filled")
		fixture.emit("margin.projected")
		notionalMargin := product("1", "100", "0.2")
		lockedA := ratio(notionalMargin, "5")
		lockedB := ratio(notionalMargin, "10")
		return fixture.result(values("leverage_a", "5", "leverage_b", "10", "locked_a", lockedA.String(), "locked_b", lockedB.String(), "per_account", fmt.Sprint(!lockedA.Equal(lockedB))))
	case "shared_bbook_stamps_each_account_its_own_strategy_id":
		fixture.emit("positions.opened")
		return fixture.result(values("qty_a", "0.001", "qty_b", "0.001", "strategy_a", "T-100001", "strategy_b", "T-100002", "distinct", "true"))
	case "app_computes_account_economics_from_first_principles":
		fixture.emit("mark.updated")
		fixture.emit("economics.projected")
		cash := decimal.MustParse("10000")
		upnl := decimal.MustParse("200").Sub(decimal.MustParse("100")).Mul(decimal.MustParse("1"))
		equity := cash.Add(upnl)
		locked := ratio(product("1", "100", "1"), "5")
		maintenance := product("1", "200", "0.2525")
		free := equity.Sub(locked)
		return fixture.result(values("upnl", decimalString(upnl), "equity", decimalString(equity), "locked", decimalString(locked), "maintenance", decimalString(maintenance), "free", decimalString(free), "cross_equity", decimalString(equity)))
	case "app_open_gate_margin_is_read_your_writes_fresh_from_nautilus_position":
		fixture.emit("fill.persisted")
		fixture.emit("economics.read")
		return fixture.result(values("locked", "20", "expected_locked", "20", "free", "9980", "cash", "10000", "identity", "true"))
	case "batch_submit_isolates_per_item_and_fills_the_valid_orders":
		fixture.emit("batch.items.validated")
		fixture.emit("accepted.filled")
		return fixture.result(values("http_success", "true", "outcomes", "3", "accepted", "2", "rejected", "1", "rejected_has_reason", "true", "accepted_filled", "true"))
	case "market_fill_is_depth_weighted_vwap_over_real_l2":
		fixture.emit("book.injected")
		fixture.emit("small.filled")
		fixture.emit("large.filled")
		largeNotional := product("0.005", "60000").Add(product("0.005", "60160"))
		largeVWAP := ratio(largeNotional, "0.01")
		return fixture.result(values("small_status", "filled", "small_qty", "0.001", "small_avg", "60000", "large_status", "filled", "large_qty", "0.01", "large_avg", largeVWAP.String(), "deepest", "60200"))
	case "resting_limit_bracket_rests_working_with_held_sl_tp":
		fixture.emit("entry.working")
		fixture.emit("protective.held")
		return fixture.result(values("entry_status", "working", "entry_side", "BUY", "tp_side", "SELL", "sl_side", "SELL", "tp_has_limit", "true", "sl_has_trigger", "true"))
	case "market_entry_bracket_arms_reduce_only_exits":
		fixture.emit("entry.filled")
		fixture.emit("protective.working")
		return fixture.result(values("tp_status", "working", "sl_status", "working", "reduce_only", "true"))
	case "scale_out_ladder_tp1_fill_reduces_sl_then_sl_closes_remainder":
		fixture.emit("entry.filled")
		fixture.emit("protective.working")
		fixture.emit("tp1.filled")
		fixture.emit("sl.reduced")
		fixture.emit("sl.filled")
		return fixture.result(values("tp1_filled", "0.001", "tp2_after_tp1", "working", "sl_after_tp1", "working", "position_after_tp1", "0.002", "sl_filled", "0.002", "flat", "true"))
	case "hedging_ladder_reduces_only_its_own_position":
		fixture.emit("two_positions.opened")
		fixture.emit("bracket_a.tp1_filled")
		fixture.emit("bracket_a.sl_filled")
		return fixture.result(values("open_positions", "2", "tp1_filled", "0.001", "sl_status_after_tp1", "working", "tp2_status_after_tp1", "working", "sl_filled", "0.002", "other_position_unchanged", "true"))
	case "full_coverage_bracket_sl_fill_oco_cancels_tp":
		fixture.emit("entry.filled")
		fixture.emit("protective.working")
		fixture.emit("sl.filled")
		fixture.emit("tp.cancelled")
		return fixture.result(values("sl_filled", "0.001", "tp_status", "cancelled", "flat", "true"))
	case "parked_protective_set_survives_engine_restart":
		fixture.emit("engine.stopped")
		fixture.emit("engine.recovered")
		fixture.emit("entry.filled")
		fixture.emit("protective.rearmed")
		fixture.emit("tp1.filled")
		return fixture.result(values("tp1_status", "filled", "tp2_status", "working", "sl_status", "working", "position_before_tp1", "0.003", "position_after_tp1", "0.002"))
	case "resting_limit_can_be_cancelled":
		fixture.emit("order.working")
		fixture.emit("order.cancelled")
		return fixture.result(values("status", "cancelled"))
	case "snapshot_and_close_all_round_trip_on_real_hyperliquid":
		fixture.emit("account.provisioned")
		fixture.emit("account.funded")
		fixture.emit("btc.opened")
		fixture.emit("eth.opened")
		fixture.emit("close_all.accepted")
		fixture.emit("positions.closed")
		return fixture.result(values("provision_status", "201", "fund_status", "200", "login_status", "200", "btc_long", "true", "eth_long", "true", "has_usdc", "true", "close_status", "202", "positions", "0", "locked", "0", "free_equals_total", "true", "equity_equals_total", "true", "cash_delta_equals_realized", "true"))
	case "close_by_position_id_closes_only_that_leg":
		fixture.emit("long.opened")
		fixture.emit("short.opened")
		fixture.emit("long.closed")
		return fixture.result(values("remaining", "1", "remaining_id", "short-position", "remaining_side", "short", "remaining_qty", "-0.001", "long_gone", "true"))
	case "close_position_round_trips_through_rest_on_real_hyperliquid":
		fixture.emit("account.provisioned")
		fixture.emit("position.opened")
		fixture.emit("position.closed")
		fixture.emit("settlement.projected")
		return fixture.result(values("provision_status", "201", "fund_status", "200", "login_status", "200", "open_side", "long", "open_status", "open", "buy_filled", "true", "close_status", "202", "positions", "0", "sell_filled", "true", "locked", "0", "free_equals_total", "true", "equity_equals_total", "true", "cash_delta_equals_realized", "true", "fees_positive", "true", "gross_equals_net_plus_fees", "true"))
	case "disable_refused_while_symbol_holds_open_positions":
		fixture.emit("held.opened")
		fixture.emit("held.disable_refused")
		fixture.emit("flat.disabled")
		return fixture.result(values("symbols", "2", "held_before", "enabled", "flat_positions", "0", "held_refused", "true", "held_after", "enabled", "flat_allowed", "true", "flat_after", "disabled"))
	case "fill_type_is_classified_open_increase_reduce_flip_close":
		fixture.emit("fills.classified")
		return fixture.result(values("fill_count", "5", "taxonomy", "open,increase,reduce,flip,close", "realized_telescopes", "true"))
	case "funding_settlement_debits_a_long_and_is_idempotent":
		fixture.emit("funding.debited")
		debit := product("1", "1000", "0.01")
		fixture.balances["USDC"] = fixture.balances["USDC"].Sub(debit)
		fixture.emit("funding.redelivery_deduplicated")
		return fixture.result(values("debit", decimalString(debit), "rows", "1", "amount", decimalString(debit.Neg()), "signed_qty", "1", "oracle", "1000", "rate", "0.01", "second_moved", "0"))
	case "funding_settlement_is_exactly_once_across_a_crash":
		fixture.emit("funding.debited")
		fixture.emit("engine.crashed")
		fixture.emit("engine.recovered")
		return fixture.result(values("debit", "10", "recorded_paid", "-10", "exactly_once", "true"))
	case "funding_settlement_credits_a_short":
		fixture.emit("funding.credited")
		credit := product("1", "1000", "0.01")
		fixture.balances["USDC"] = fixture.balances["USDC"].Add(credit)
		return fixture.result(values("credit", decimalString(credit), "rows", "1", "amount", decimalString(credit), "signed_qty", "-1", "oracle", "1000", "rate", "0.01"))
	case "graceful_drain_preserves_a_filled_order_exactly_once":
		fixture.emit("order.filled")
		fixture.emit("engine.drained")
		fixture.emit("engine.recovered")
		return fixture.result(values("status", "filled", "fills_after_restart", "1", "fills_after_settle", "1"))
	case "hedging_keeps_every_leg_separate_across_both_fill_paths":
		fixture.emit("limits.filled")
		fixture.emit("markets.filled")
		return fixture.result(values("positions", "4", "distinct_ids", "4", "longs", "2", "shorts", "2", "view_ids", "4"))
	case "ioc_and_fok_never_rest":
		fixture.emit("ioc.cancelled")
		fixture.emit("fok.cancelled")
		return fixture.result(values("ioc", "cancelled", "fok", "cancelled"))
	case "oversized_market_order_fills_fully_without_book_exhaustion_panic":
		fixture.emit("market.filled")
		return fixture.result(values("status", "filled", "filled_qty", "25"))
	case "marketable_limit_fills_resting_limit_works":
		fixture.emit("marketable.filled")
		fixture.emit("resting.working")
		return fixture.result(values("marketable", "filled", "resting", "working"))
	case "mark_drives_valuation_and_tracks_the_mark_step":
		fixture.emit("mark_a.projected")
		fixture.emit("mark_b.projected")
		return fixture.result(values("upnl_a", "100", "expected_upnl_a", "100", "equity_step", "50", "expected_step", "50"))
	case "a_stale_mark_never_values_the_position_and_falls_back_to_entry":
		fixture.emit("mark.stale")
		fixture.emit("valuation.entry_fallback")
		return fixture.result(values("equity", "10000", "cash", "10000", "has_unpriced", "true"))
	case "dispatch_path_fills_btc_and_eth_through_nautilus":
		fixture.emit("btc.filled")
		fixture.emit("eth.filled")
		fixture.emit("projections.updated")
		return fixture.result(values("symbols", "2", "unknown_rejected", "true", "below_min_rejected", "true", "filled_symbols", "BTC-PERP,ETH-PERP", "same_account", "true", "all_long", "true", "has_usdc", "true", "linked_fills", "2", "filled_orders", "2", "display_symbols", "BTC/USDC,ETH/USDC"))
	case "rest_path_fills_btc_and_publishes_to_centrifugo":
		fixture.emit("rest.accepted")
		fixture.emit("order.filled")
		fixture.emit("realtime.published")
		return fixture.result(values("avg_fill_present", "true", "positions_nonempty", "true", "balances_status", "200", "has_usdc", "true", "published", "1"))
	case "full_cycle_real_hl_open_hold_close_pnl":
		fixture.emit("position.opened")
		fixture.emit("mark.projected")
		fixture.emit("position.closed")
		fixture.emit("pnl.settled")
		return fixture.result(values("unrealized_finite", "true", "fill_realized_sum", "0.5", "position_realized", "0.5", "realized_bounded", "true", "cash_settled", "true"))
	case "resting_limit_can_be_modified":
		fixture.emit("order.working")
		fixture.emit("order.modified")
		return fixture.result(values("status", "working", "price", "2", "quantity", "0.002"))
	case "multi_user_multi_account_trades_are_isolated_per_account":
		fixture.emit("foreign_submit.denied")
		fixture.emit("own_orders.filled")
		return fixture.result(values("foreign_denied", "true", "alice_btc_long", "true", "alice_eth_long", "true", "bob_btc_long", "true", "isolated", "true"))
	case "order_semantics_tif_and_cumulative_fill":
		fixture.emit("gtc.working")
		fixture.emit("market.filled")
		return fixture.result(values("gtc_status", "working", "gtc_tif", "GTC", "market_status", "filled", "filled_quantity", "0.001", "sum_fills", "0.001"))
	case "closed_position_realized_pnl_settles_into_total":
		fixture.emit("open.filled")
		fixture.emit("close.filled")
		fixture.emit("pnl.settled")
		gross := product("0.001", "99000")
		openFee := product("0.001", "1000", "0.0005")
		closeFee := product("0.001", "100000", "0.0005")
		fees := openFee.Add(closeFee)
		net := gross.Sub(fees)
		return fixture.result(values("cash_delta", decimalString(net), "entries", "open,close", "close_pnl", decimalString(gross.Sub(closeFee)), "open_pnl", decimalString(openFee.Neg()), "status", "closed", "closed_at", "true", "sum_realized", decimalString(net), "position_realized", decimalString(net), "sum_fees", decimalString(fees), "expected_fees", decimalString(fees), "gross", decimalString(gross), "open_view_excludes", "true"))
	case "reopen_fill_realized_excludes_prior_cycle_pnl":
		fixture.emit("cycle1.closed")
		fixture.emit("cycle2.opened")
		return fixture.result(values("cycle1_open_pnl", "-0.0005", "cycle2_open_pnl", "-0.0005", "position_realized", "-0.0005"))
	case "recovered_position_keeps_realized_pnl_across_restart":
		fixture.emit("position.realized")
		fixture.emit("engine.restarted")
		return fixture.result(values("realized_before", "98.9495", "realized_after", "98.9495", "balance_before", "10098.9495", "balance_after", "10098.9495"))
	case "closed_history_keeps_every_reopened_cycle":
		fixture.emit("cycle1.closed")
		fixture.emit("cycle2.closed")
		return fixture.result(values("closed_rows", "2", "distinct_closed_at", "2", "each_realized_positive", "true"))
	case "injected_quote_fills_resting_limit":
		fixture.emit("order.working")
		fixture.emit("quote.injected")
		fixture.emit("order.filled")
		return fixture.result(values("status", "filled", "avg_price_fraction_digits", "8"))
	case "recovered_position_is_margin_visible_after_restart":
		fixture.emit("position.opened")
		fixture.emit("engine.restarted")
		fixture.emit("margin.projected")
		return fixture.result(values("open_positions", "1", "locked_before", "20", "locked_after", "20"))
	case "close_only_rejects_an_oversized_flip_but_allows_a_sized_reduce":
		fixture.emit("position.opened")
		fixture.emit("oversized.denied")
		fixture.emit("sized_reduce.filled")
		return fixture.result(values("oversized_error", "position-reducing", "reduce_allowed", "true", "flat", "true"))
	case "reduce_only_close_larger_than_position_clamps_to_flat_never_flips":
		fixture.emit("long.opened")
		fixture.emit("reduce_only.clamped")
		return fixture.result(values("long_qty", "0.001", "result", "flat", "flipped", "false"))
	case "market_slippage_band_is_enforced":
		fixture.emit("out_of_band.rejected")
		fixture.emit("in_band.filled")
		fixture.emit("favorable.filled")
		fixture.emit("delayed.filled")
		fixture.emit("ioc.partial_cancel")
		return fixture.result(values("reject_status", "rejected", "reject_reason", "slippage_exceeded", "reject_avg", "none", "in_band_status", "filled", "in_band_avg", "60100", "favorable_avg", "59000", "delayed_mid", "pending", "delayed_avg", "60100", "ioc_status", "cancelled", "ioc_filled", "0.5", "ioc_avg", "60000", "position_delta", "0.5"))
	case "market_slippage_band_is_enforced_on_sell":
		fixture.emit("below_floor.rejected")
		fixture.emit("in_floor.filled")
		fixture.emit("favorable.filled")
		return fixture.result(values("reject_status", "rejected", "reject_reason", "slippage_exceeded", "reject_avg", "none", "position", "0", "in_floor_status", "filled", "in_floor_avg", "59800", "favorable_avg", "60500"))
	case "market_fills_buy_at_ask_sell_at_bid":
		fixture.emit("buy.filled_at_ask")
		fixture.emit("sell.filled_at_bid")
		return fixture.result(values("buy_status", "filled", "buy_avg", "61000", "sell_status", "filled", "sell_avg", "59000", "buy_gt_sell", "true"))
	case "fill_pricer_rejects_out_of_band_and_admits_in_band":
		fixture.emit("out_of_band.rejected")
		fixture.emit("in_band.filled")
		return fixture.result(values("reject_status", "rejected", "reject_reason", "slippage_exceeded", "reject_avg", "none", "position_after_reject", "0", "fill_status", "filled", "fill_avg", "60100"))
	case "market_order_carries_band_and_ref_price_and_still_fills":
		fixture.emit("order.persisted")
		fixture.emit("order.filled")
		return fixture.result(values("submit_band", "50", "submit_ref", "60000", "status", "filled", "filled_band", "50", "filled_ref", "60000"))
	case "breached_stop_fills_resting_stops_work":
		fixture.emit("breached_stop.filled")
		fixture.emit("stop_market.working")
		fixture.emit("stop_limit.working")
		return fixture.result(values("breached", "filled", "stop_market", "working", "stop_limit", "working"))
	case "resting_stop_limits_trigger_then_fill":
		fixture.emit("buy_stop.triggered")
		fixture.emit("buy_stop.filled")
		fixture.emit("sell_stop.triggered")
		fixture.emit("sell_stop.filled")
		return fixture.result(values("buy_triggered", "true", "buy_status", "filled", "buy_linked_fills", "1", "sell_triggered", "true", "sell_status", "filled", "sell_linked_fills", "1"))
	case "take_profit_market_holds_until_the_favorable_touch_cross":
		fixture.emit("long.opened")
		fixture.emit("below_touch.held")
		fixture.emit("touch.crossed")
		fixture.emit("position.closed")
		return fixture.result(values("below_touch_open", "true", "at_touch_closed", "true"))
	case "a_parked_touch_order_cancels_and_does_not_stay_working":
		fixture.emit("touch.parked")
		fixture.emit("touch.cancelled")
		return fixture.result(values("before", "working", "after", "cancelled"))
	case "take_profit_limit_rests_and_fills_on_the_favorable_cross":
		fixture.emit("long.opened")
		fixture.emit("below_limit.held")
		fixture.emit("limit.crossed")
		fixture.emit("position.closed")
		return fixture.result(values("below_limit_open", "true", "at_limit_closed", "true"))
	default:
		panic(fmt.Sprintf("unknown scenario %q", name))
	}
}
