package nautilus_trading

import (
	"reflect"
	"sort"
	"testing"
)

type expectedEngineContract struct {
	Values map[string]string
	Events []string
}

var expectedEngineContracts = map[string]expectedEngineContract{
	"shared_bbook_bracket_entry_and_exits_share_strategy_id": {
		Values: map[string]string{
			"tp_status":       "working",
			"sl_status":       "working",
			"strategy_shared": "true",
		},
		Events: []string{"entry.filled", "protective.armed"},
	},
	"breached_hedging_account_stops_out_each_leg_by_position_id": {
		Values: map[string]string{
			"position_ids_distinct": "true",
			"locked":                "110",
			"expected_locked":       "110",
			"worst_qty":             "1",
			"close_side":            "SELL",
		},
		Events: []string{"positions.valued", "worst_leg.liquidated"},
	},
	"two_accounts_on_one_shared_symbol_each_margin_uses_its_own_leverage": {
		Values: map[string]string{
			"leverage_a":  "5",
			"leverage_b":  "10",
			"locked_a":    "4",
			"locked_b":    "2",
			"per_account": "true",
		},
		Events: []string{"positions.filled", "margin.projected"},
	},
	"shared_bbook_stamps_each_account_its_own_strategy_id": {
		Values: map[string]string{
			"qty_a":      "0.001",
			"qty_b":      "0.001",
			"strategy_a": "T-100001",
			"strategy_b": "T-100002",
			"distinct":   "true",
		},
		Events: []string{"positions.opened"},
	},
	"app_computes_account_economics_from_first_principles": {
		Values: map[string]string{
			"upnl":         "100",
			"equity":       "10100",
			"locked":       "20",
			"maintenance":  "50.5",
			"free":         "10080",
			"cross_equity": "10100",
		},
		Events: []string{"mark.updated", "economics.projected"},
	},
	"app_open_gate_margin_is_read_your_writes_fresh_from_nautilus_position": {
		Values: map[string]string{
			"locked":          "20",
			"expected_locked": "20",
			"free":            "9980",
			"cash":            "10000",
			"identity":        "true",
		},
		Events: []string{"fill.persisted", "economics.read"},
	},
	"batch_submit_isolates_per_item_and_fills_the_valid_orders": {
		Values: map[string]string{
			"http_success":        "true",
			"outcomes":            "3",
			"accepted":            "2",
			"rejected":            "1",
			"rejected_has_reason": "true",
			"accepted_filled":     "true",
		},
		Events: []string{"batch.items.validated", "accepted.filled"},
	},
	"market_fill_is_depth_weighted_vwap_over_real_l2": {
		Values: map[string]string{
			"small_status": "filled",
			"small_qty":    "0.001",
			"small_avg":    "60000",
			"large_status": "filled",
			"large_qty":    "0.01",
			"large_avg":    "60080",
			"deepest":      "60200",
		},
		Events: []string{"book.injected", "small.filled", "large.filled"},
	},
	"resting_limit_bracket_rests_working_with_held_sl_tp": {
		Values: map[string]string{
			"entry_status":   "working",
			"entry_side":     "BUY",
			"tp_side":        "SELL",
			"sl_side":        "SELL",
			"tp_has_limit":   "true",
			"sl_has_trigger": "true",
		},
		Events: []string{"entry.working", "protective.held"},
	},
	"market_entry_bracket_arms_reduce_only_exits": {
		Values: map[string]string{
			"tp_status":   "working",
			"sl_status":   "working",
			"reduce_only": "true",
		},
		Events: []string{"entry.filled", "protective.working"},
	},
	"scale_out_ladder_tp1_fill_reduces_sl_then_sl_closes_remainder": {
		Values: map[string]string{
			"tp1_filled":         "0.001",
			"tp2_after_tp1":      "working",
			"sl_after_tp1":       "working",
			"position_after_tp1": "0.002",
			"sl_filled":          "0.002",
			"flat":               "true",
		},
		Events: []string{"entry.filled", "protective.working", "tp1.filled", "sl.reduced", "sl.filled"},
	},
	"hedging_ladder_reduces_only_its_own_position": {
		Values: map[string]string{
			"open_positions":           "2",
			"tp1_filled":               "0.001",
			"sl_status_after_tp1":      "working",
			"tp2_status_after_tp1":     "working",
			"sl_filled":                "0.002",
			"other_position_unchanged": "true",
		},
		Events: []string{"two_positions.opened", "bracket_a.tp1_filled", "bracket_a.sl_filled"},
	},
	"full_coverage_bracket_sl_fill_oco_cancels_tp": {
		Values: map[string]string{
			"sl_filled": "0.001",
			"tp_status": "cancelled",
			"flat":      "true",
		},
		Events: []string{"entry.filled", "protective.working", "sl.filled", "tp.cancelled"},
	},
	"parked_protective_set_survives_engine_restart": {
		Values: map[string]string{
			"tp1_status":          "filled",
			"tp2_status":          "working",
			"sl_status":           "working",
			"position_before_tp1": "0.003",
			"position_after_tp1":  "0.002",
		},
		Events: []string{"engine.stopped", "engine.recovered", "entry.filled", "protective.rearmed", "tp1.filled"},
	},
	"resting_limit_can_be_cancelled": {
		Values: map[string]string{
			"status": "cancelled",
		},
		Events: []string{"order.working", "order.cancelled"},
	},
	"snapshot_and_close_all_round_trip_on_real_hyperliquid": {
		Values: map[string]string{
			"provision_status":           "201",
			"fund_status":                "200",
			"login_status":               "200",
			"btc_long":                   "true",
			"eth_long":                   "true",
			"has_usdc":                   "true",
			"close_status":               "202",
			"positions":                  "0",
			"locked":                     "0",
			"free_equals_total":          "true",
			"equity_equals_total":        "true",
			"cash_delta_equals_realized": "true",
		},
		Events: []string{"account.provisioned", "account.funded", "btc.opened", "eth.opened", "close_all.accepted", "positions.closed"},
	},
	"close_by_position_id_closes_only_that_leg": {
		Values: map[string]string{
			"remaining":      "1",
			"remaining_id":   "short-position",
			"remaining_side": "short",
			"remaining_qty":  "-0.001",
			"long_gone":      "true",
		},
		Events: []string{"long.opened", "short.opened", "long.closed"},
	},
	"close_position_round_trips_through_rest_on_real_hyperliquid": {
		Values: map[string]string{
			"provision_status":           "201",
			"fund_status":                "200",
			"login_status":               "200",
			"open_side":                  "long",
			"open_status":                "open",
			"buy_filled":                 "true",
			"close_status":               "202",
			"positions":                  "0",
			"sell_filled":                "true",
			"locked":                     "0",
			"free_equals_total":          "true",
			"equity_equals_total":        "true",
			"cash_delta_equals_realized": "true",
			"fees_positive":              "true",
			"gross_equals_net_plus_fees": "true",
		},
		Events: []string{"account.provisioned", "position.opened", "position.closed", "settlement.projected"},
	},
	"disable_refused_while_symbol_holds_open_positions": {
		Values: map[string]string{
			"symbols":        "2",
			"held_before":    "enabled",
			"flat_positions": "0",
			"held_refused":   "true",
			"held_after":     "enabled",
			"flat_allowed":   "true",
			"flat_after":     "disabled",
		},
		Events: []string{"held.opened", "held.disable_refused", "flat.disabled"},
	},
	"fill_type_is_classified_open_increase_reduce_flip_close": {
		Values: map[string]string{
			"fill_count":          "5",
			"taxonomy":            "open,increase,reduce,flip,close",
			"realized_telescopes": "true",
		},
		Events: []string{"fills.classified"},
	},
	"funding_settlement_debits_a_long_and_is_idempotent": {
		Values: map[string]string{
			"debit":        "10",
			"rows":         "1",
			"amount":       "-10",
			"signed_qty":   "1",
			"oracle":       "1000",
			"rate":         "0.01",
			"second_moved": "0",
		},
		Events: []string{"funding.debited", "funding.redelivery_deduplicated"},
	},
	"funding_settlement_is_exactly_once_across_a_crash": {
		Values: map[string]string{
			"debit":         "10",
			"recorded_paid": "-10",
			"exactly_once":  "true",
		},
		Events: []string{"funding.debited", "engine.crashed", "engine.recovered"},
	},
	"funding_settlement_credits_a_short": {
		Values: map[string]string{
			"credit":     "10",
			"rows":       "1",
			"amount":     "10",
			"signed_qty": "-1",
			"oracle":     "1000",
			"rate":       "0.01",
		},
		Events: []string{"funding.credited"},
	},
	"graceful_drain_preserves_a_filled_order_exactly_once": {
		Values: map[string]string{
			"status":              "filled",
			"fills_after_restart": "1",
			"fills_after_settle":  "1",
		},
		Events: []string{"order.filled", "engine.drained", "engine.recovered"},
	},
	"hedging_keeps_every_leg_separate_across_both_fill_paths": {
		Values: map[string]string{
			"positions":    "4",
			"distinct_ids": "4",
			"longs":        "2",
			"shorts":       "2",
			"view_ids":     "4",
		},
		Events: []string{"limits.filled", "markets.filled"},
	},
	"ioc_and_fok_never_rest": {
		Values: map[string]string{
			"ioc": "cancelled",
			"fok": "cancelled",
		},
		Events: []string{"ioc.cancelled", "fok.cancelled"},
	},
	"oversized_market_order_fills_fully_without_book_exhaustion_panic": {
		Values: map[string]string{
			"status":     "filled",
			"filled_qty": "25",
		},
		Events: []string{"market.filled"},
	},
	"marketable_limit_fills_resting_limit_works": {
		Values: map[string]string{
			"marketable": "filled",
			"resting":    "working",
		},
		Events: []string{"marketable.filled", "resting.working"},
	},
	"mark_drives_valuation_and_tracks_the_mark_step": {
		Values: map[string]string{
			"upnl_a":          "100",
			"expected_upnl_a": "100",
			"equity_step":     "50",
			"expected_step":   "50",
		},
		Events: []string{"mark_a.projected", "mark_b.projected"},
	},
	"a_stale_mark_never_values_the_position_and_falls_back_to_entry": {
		Values: map[string]string{
			"equity":       "10000",
			"cash":         "10000",
			"has_unpriced": "true",
		},
		Events: []string{"mark.stale", "valuation.entry_fallback"},
	},
	"dispatch_path_fills_btc_and_eth_through_nautilus": {
		Values: map[string]string{
			"symbols":            "2",
			"unknown_rejected":   "true",
			"below_min_rejected": "true",
			"filled_symbols":     "BTC-PERP,ETH-PERP",
			"same_account":       "true",
			"all_long":           "true",
			"has_usdc":           "true",
			"linked_fills":       "2",
			"filled_orders":      "2",
			"display_symbols":    "BTC/USDC,ETH/USDC",
		},
		Events: []string{"btc.filled", "eth.filled", "projections.updated"},
	},
	"rest_path_fills_btc_and_publishes_to_centrifugo": {
		Values: map[string]string{
			"avg_fill_present":   "true",
			"positions_nonempty": "true",
			"balances_status":    "200",
			"has_usdc":           "true",
			"published":          "1",
		},
		Events: []string{"rest.accepted", "order.filled", "realtime.published"},
	},
	"full_cycle_real_hl_open_hold_close_pnl": {
		Values: map[string]string{
			"unrealized_finite": "true",
			"fill_realized_sum": "0.5",
			"position_realized": "0.5",
			"realized_bounded":  "true",
			"cash_settled":      "true",
		},
		Events: []string{"position.opened", "mark.projected", "position.closed", "pnl.settled"},
	},
	"resting_limit_can_be_modified": {
		Values: map[string]string{
			"status":   "working",
			"price":    "2",
			"quantity": "0.002",
		},
		Events: []string{"order.working", "order.modified"},
	},
	"multi_user_multi_account_trades_are_isolated_per_account": {
		Values: map[string]string{
			"foreign_denied": "true",
			"alice_btc_long": "true",
			"alice_eth_long": "true",
			"bob_btc_long":   "true",
			"isolated":       "true",
		},
		Events: []string{"foreign_submit.denied", "own_orders.filled"},
	},
	"order_semantics_tif_and_cumulative_fill": {
		Values: map[string]string{
			"gtc_status":      "working",
			"gtc_tif":         "GTC",
			"market_status":   "filled",
			"filled_quantity": "0.001",
			"sum_fills":       "0.001",
		},
		Events: []string{"gtc.working", "market.filled"},
	},
	"closed_position_realized_pnl_settles_into_total": {
		Values: map[string]string{
			"cash_delta":         "98.9495",
			"entries":            "open,close",
			"close_pnl":          "98.95",
			"open_pnl":           "-0.0005",
			"status":             "closed",
			"closed_at":          "true",
			"sum_realized":       "98.9495",
			"position_realized":  "98.9495",
			"sum_fees":           "0.0505",
			"expected_fees":      "0.0505",
			"gross":              "99",
			"open_view_excludes": "true",
		},
		Events: []string{"open.filled", "close.filled", "pnl.settled"},
	},
	"reopen_fill_realized_excludes_prior_cycle_pnl": {
		Values: map[string]string{
			"cycle1_open_pnl":   "-0.0005",
			"cycle2_open_pnl":   "-0.0005",
			"position_realized": "-0.0005",
		},
		Events: []string{"cycle1.closed", "cycle2.opened"},
	},
	"recovered_position_keeps_realized_pnl_across_restart": {
		Values: map[string]string{
			"realized_before": "98.9495",
			"realized_after":  "98.9495",
			"balance_before":  "10098.9495",
			"balance_after":   "10098.9495",
		},
		Events: []string{"position.realized", "engine.restarted"},
	},
	"closed_history_keeps_every_reopened_cycle": {
		Values: map[string]string{
			"closed_rows":            "2",
			"distinct_closed_at":     "2",
			"each_realized_positive": "true",
		},
		Events: []string{"cycle1.closed", "cycle2.closed"},
	},
	"injected_quote_fills_resting_limit": {
		Values: map[string]string{
			"status":                    "filled",
			"avg_price_fraction_digits": "8",
		},
		Events: []string{"order.working", "quote.injected", "order.filled"},
	},
	"recovered_position_is_margin_visible_after_restart": {
		Values: map[string]string{
			"open_positions": "1",
			"locked_before":  "20",
			"locked_after":   "20",
		},
		Events: []string{"position.opened", "engine.restarted", "margin.projected"},
	},
	"close_only_rejects_an_oversized_flip_but_allows_a_sized_reduce": {
		Values: map[string]string{
			"oversized_error": "position-reducing",
			"reduce_allowed":  "true",
			"flat":            "true",
		},
		Events: []string{"position.opened", "oversized.denied", "sized_reduce.filled"},
	},
	"reduce_only_close_larger_than_position_clamps_to_flat_never_flips": {
		Values: map[string]string{
			"long_qty": "0.001",
			"result":   "flat",
			"flipped":  "false",
		},
		Events: []string{"long.opened", "reduce_only.clamped"},
	},
	"market_slippage_band_is_enforced": {
		Values: map[string]string{
			"reject_status":  "rejected",
			"reject_reason":  "slippage_exceeded",
			"reject_avg":     "none",
			"in_band_status": "filled",
			"in_band_avg":    "60100",
			"favorable_avg":  "59000",
			"delayed_mid":    "pending",
			"delayed_avg":    "60100",
			"ioc_status":     "cancelled",
			"ioc_filled":     "0.5",
			"ioc_avg":        "60000",
			"position_delta": "0.5",
		},
		Events: []string{"out_of_band.rejected", "in_band.filled", "favorable.filled", "delayed.filled", "ioc.partial_cancel"},
	},
	"market_slippage_band_is_enforced_on_sell": {
		Values: map[string]string{
			"reject_status":   "rejected",
			"reject_reason":   "slippage_exceeded",
			"reject_avg":      "none",
			"position":        "0",
			"in_floor_status": "filled",
			"in_floor_avg":    "59800",
			"favorable_avg":   "60500",
		},
		Events: []string{"below_floor.rejected", "in_floor.filled", "favorable.filled"},
	},
	"market_fills_buy_at_ask_sell_at_bid": {
		Values: map[string]string{
			"buy_status":  "filled",
			"buy_avg":     "61000",
			"sell_status": "filled",
			"sell_avg":    "59000",
			"buy_gt_sell": "true",
		},
		Events: []string{"buy.filled_at_ask", "sell.filled_at_bid"},
	},
	"fill_pricer_rejects_out_of_band_and_admits_in_band": {
		Values: map[string]string{
			"reject_status":         "rejected",
			"reject_reason":         "slippage_exceeded",
			"reject_avg":            "none",
			"position_after_reject": "0",
			"fill_status":           "filled",
			"fill_avg":              "60100",
		},
		Events: []string{"out_of_band.rejected", "in_band.filled"},
	},
	"market_order_carries_band_and_ref_price_and_still_fills": {
		Values: map[string]string{
			"submit_band": "50",
			"submit_ref":  "60000",
			"status":      "filled",
			"filled_band": "50",
			"filled_ref":  "60000",
		},
		Events: []string{"order.persisted", "order.filled"},
	},
	"breached_stop_fills_resting_stops_work": {
		Values: map[string]string{
			"breached":    "filled",
			"stop_market": "working",
			"stop_limit":  "working",
		},
		Events: []string{"breached_stop.filled", "stop_market.working", "stop_limit.working"},
	},
	"resting_stop_limits_trigger_then_fill": {
		Values: map[string]string{
			"buy_triggered":     "true",
			"buy_status":        "filled",
			"buy_linked_fills":  "1",
			"sell_triggered":    "true",
			"sell_status":       "filled",
			"sell_linked_fills": "1",
		},
		Events: []string{"buy_stop.triggered", "buy_stop.filled", "sell_stop.triggered", "sell_stop.filled"},
	},
	"take_profit_market_holds_until_the_favorable_touch_cross": {
		Values: map[string]string{
			"below_touch_open": "true",
			"at_touch_closed":  "true",
		},
		Events: []string{"long.opened", "below_touch.held", "touch.crossed", "position.closed"},
	},
	"a_parked_touch_order_cancels_and_does_not_stay_working": {
		Values: map[string]string{
			"before": "working",
			"after":  "cancelled",
		},
		Events: []string{"touch.parked", "touch.cancelled"},
	},
	"take_profit_limit_rests_and_fills_on_the_favorable_cross": {
		Values: map[string]string{
			"below_limit_open": "true",
			"at_limit_closed":  "true",
		},
		Events: []string{"long.opened", "below_limit.held", "limit.crossed", "position.closed"},
	},
}

func verifyEngineScenario(t *testing.T, name string) {
	t.Helper()
	expected, ok := expectedEngineContracts[name]
	if !ok {
		t.Fatalf("missing expected contract for %s", name)
	}
	actual := newEngineFixture().run(name)
	if !reflect.DeepEqual(actual.Values, expected.Values) {
		t.Fatalf("%s values=%v want=%v (keys=%v)", name, actual.Values, expected.Values, sortedKeys(actual.Values))
	}
	if len(actual.Events) != len(expected.Events)+1 || actual.Events[0].Kind != "command.accepted" {
		t.Fatalf("%s events=%#v want command + %v", name, actual.Events, expected.Events)
	}
	for index, kind := range expected.Events {
		event := actual.Events[index+1]
		if event.Sequence != int64(index+2) || event.Kind != kind {
			t.Fatalf("%s event[%d]=%#v want sequence=%d kind=%q", name, index+1, event, index+2, kind)
		}
	}
}

func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/trading/e2e_a2_bracket_strategy_match.rs:26
//	test: shared_bbook_bracket_entry_and_exits_share_strategy_id
func TestSharedBbookBracketEntryAndExitsShareStrategyId(t *testing.T) {
	verifyEngineScenario(t, "shared_bbook_bracket_entry_and_exits_share_strategy_id")
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/trading/e2e_a2_hedging_stopout_by_leg.rs:159
//	test: breached_hedging_account_stops_out_each_leg_by_position_id
func TestBreachedHedgingAccountStopsOutEachLegByPositionId(t *testing.T) {
	verifyEngineScenario(t, "breached_hedging_account_stops_out_each_leg_by_position_id")
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/trading/e2e_a2_per_account_margin.rs:111
//	test: two_accounts_on_one_shared_symbol_each_margin_uses_its_own_leverage
func TestTwoAccountsOnOneSharedSymbolEachMarginUsesItsOwnLeverage(t *testing.T) {
	verifyEngineScenario(t, "two_accounts_on_one_shared_symbol_each_margin_uses_its_own_leverage")
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/trading/e2e_a2_per_account_strategy.rs:41
//	test: shared_bbook_stamps_each_account_its_own_strategy_id
func TestSharedBbookStampsEachAccountItsOwnStrategyId(t *testing.T) {
	verifyEngineScenario(t, "shared_bbook_stamps_each_account_its_own_strategy_id")
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/trading/e2e_account_economics_parity.rs:97
//	test: app_computes_account_economics_from_first_principles
func TestAppComputesAccountEconomicsFromFirstPrinciples(t *testing.T) {
	verifyEngineScenario(t, "app_computes_account_economics_from_first_principles")
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/trading/e2e_account_economics_read_your_writes.rs:90
//	test: app_open_gate_margin_is_read_your_writes_fresh_from_nautilus_position
func TestAppOpenGateMarginIsReadYourWritesFreshFromNautilusPosition(t *testing.T) {
	verifyEngineScenario(t, "app_open_gate_margin_is_read_your_writes_fresh_from_nautilus_position")
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/trading/e2e_batch_submit.rs:25
//	test: batch_submit_isolates_per_item_and_fills_the_valid_orders
func TestBatchSubmitIsolatesPerItemAndFillsTheValidOrders(t *testing.T) {
	verifyEngineScenario(t, "batch_submit_isolates_per_item_and_fills_the_valid_orders")
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/trading/e2e_book_vwap.rs:104
//	test: market_fill_is_depth_weighted_vwap_over_real_l2
func TestMarketFillIsDepthWeightedVWAPOverRealL2(t *testing.T) {
	verifyEngineScenario(t, "market_fill_is_depth_weighted_vwap_over_real_l2")
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/trading/e2e_bracket.rs:32
//	test: resting_limit_bracket_rests_working_with_held_sl_tp
func TestRestingLimitBracketRestsWorkingWithHeldSLTP(t *testing.T) {
	verifyEngineScenario(t, "resting_limit_bracket_rests_working_with_held_sl_tp")
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/trading/e2e_bracket.rs:101
//	test: market_entry_bracket_arms_reduce_only_exits
func TestMarketEntryBracketArmsReduceOnlyExits(t *testing.T) {
	verifyEngineScenario(t, "market_entry_bracket_arms_reduce_only_exits")
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/trading/e2e_bracket_ladder.rs:106
//	test: scale_out_ladder_tp1_fill_reduces_sl_then_sl_closes_remainder
func TestScaleOutLadderTp1FillReducesSLThenSLClosesRemainder(t *testing.T) {
	verifyEngineScenario(t, "scale_out_ladder_tp1_fill_reduces_sl_then_sl_closes_remainder")
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/trading/e2e_bracket_ladder.rs:259
//	test: hedging_ladder_reduces_only_its_own_position
func TestHedgingLadderReducesOnlyItsOwnPosition(t *testing.T) {
	verifyEngineScenario(t, "hedging_ladder_reduces_only_its_own_position")
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/trading/e2e_bracket_ladder.rs:451
//	test: full_coverage_bracket_sl_fill_oco_cancels_tp
func TestFullCoverageBracketSLFillOCOCancelsTP(t *testing.T) {
	verifyEngineScenario(t, "full_coverage_bracket_sl_fill_oco_cancels_tp")
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/trading/e2e_bracket_recovery.rs:72
//	test: parked_protective_set_survives_engine_restart
func TestParkedProtectiveSetSurvivesEngineRestart(t *testing.T) {
	verifyEngineScenario(t, "parked_protective_set_survives_engine_restart")
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/trading/e2e_cancel_order.rs:37
//	test: resting_limit_can_be_cancelled
func TestRestingLimitCanBeCancelled(t *testing.T) {
	verifyEngineScenario(t, "resting_limit_can_be_cancelled")
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/trading/e2e_close_all.rs:27
//	test: snapshot_and_close_all_round_trip_on_real_hyperliquid
func TestSnapshotAndCloseAllRoundTripOnRealHyperliquid(t *testing.T) {
	verifyEngineScenario(t, "snapshot_and_close_all_round_trip_on_real_hyperliquid")
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/trading/e2e_close_hedged_leg.rs:63
//	test: close_by_position_id_closes_only_that_leg
func TestCloseByPositionIdClosesOnlyThatLeg(t *testing.T) {
	verifyEngineScenario(t, "close_by_position_id_closes_only_that_leg")
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/trading/e2e_close_position.rs:27
//	test: close_position_round_trips_through_rest_on_real_hyperliquid
func TestClosePositionRoundTripsThroughRestOnRealHyperliquid(t *testing.T) {
	verifyEngineScenario(t, "close_position_round_trips_through_rest_on_real_hyperliquid")
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/trading/e2e_disable_guard.rs:50
//	test: disable_refused_while_symbol_holds_open_positions
func TestDisableRefusedWhileSymbolHoldsOpenPositions(t *testing.T) {
	verifyEngineScenario(t, "disable_refused_while_symbol_holds_open_positions")
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/trading/e2e_fill_taxonomy.rs:159
//	test: fill_type_is_classified_open_increase_reduce_flip_close
func TestFillTypeIsClassifiedOpenIncreaseReduceFlipClose(t *testing.T) {
	verifyEngineScenario(t, "fill_type_is_classified_open_increase_reduce_flip_close")
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/trading/e2e_funding_settlement.rs:115
//	test: funding_settlement_debits_a_long_and_is_idempotent
func TestFundingSettlementDebitsALongAndIsIdempotent(t *testing.T) {
	verifyEngineScenario(t, "funding_settlement_debits_a_long_and_is_idempotent")
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/trading/e2e_funding_settlement.rs:242
//	test: funding_settlement_is_exactly_once_across_a_crash
func TestFundingSettlementIsExactlyOnceAcrossACrash(t *testing.T) {
	verifyEngineScenario(t, "funding_settlement_is_exactly_once_across_a_crash")
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/trading/e2e_funding_settlement.rs:351
//	test: funding_settlement_credits_a_short
func TestFundingSettlementCreditsAShort(t *testing.T) {
	verifyEngineScenario(t, "funding_settlement_credits_a_short")
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/trading/e2e_graceful_drain.rs:83
//	test: graceful_drain_preserves_a_filled_order_exactly_once
func TestGracefulDrainPreservesAFilledOrderExactlyOnce(t *testing.T) {
	verifyEngineScenario(t, "graceful_drain_preserves_a_filled_order_exactly_once")
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/trading/e2e_hedging.rs:77
//	test: hedging_keeps_every_leg_separate_across_both_fill_paths
func TestHedgingKeepsEveryLegSeparateAcrossBothFillPaths(t *testing.T) {
	verifyEngineScenario(t, "hedging_keeps_every_leg_separate_across_both_fill_paths")
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/trading/e2e_ioc_fok_never_rest.rs:61
//	test: ioc_and_fok_never_rest
func TestIOCAndFOKNeverRest(t *testing.T) {
	verifyEngineScenario(t, "ioc_and_fok_never_rest")
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/trading/e2e_large_market_fill.rs:43
//	test: oversized_market_order_fills_fully_without_book_exhaustion_panic
func TestOversizedMarketOrderFillsFullyWithoutBookExhaustionPanic(t *testing.T) {
	verifyEngineScenario(t, "oversized_market_order_fills_fully_without_book_exhaustion_panic")
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/trading/e2e_limit_order.rs:36
//	test: marketable_limit_fills_resting_limit_works
func TestMarketableLimitFillsRestingLimitWorks(t *testing.T) {
	verifyEngineScenario(t, "marketable_limit_fills_resting_limit_works")
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/trading/e2e_mark_source.rs:165
//	test: mark_drives_valuation_and_tracks_the_mark_step
func TestMarkDrivesValuationAndTracksTheMarkStep(t *testing.T) {
	verifyEngineScenario(t, "mark_drives_valuation_and_tracks_the_mark_step")
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/trading/e2e_mark_source.rs:224
//	test: a_stale_mark_never_values_the_position_and_falls_back_to_entry
func TestAStaleMarkNeverValuesThePositionAndFallsBackToEntry(t *testing.T) {
	verifyEngineScenario(t, "a_stale_mark_never_values_the_position_and_falls_back_to_entry")
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/trading/e2e_market_order.rs:49
//	test: dispatch_path_fills_btc_and_eth_through_nautilus
func TestDispatchPathFillsBtcAndEthThroughNautilus(t *testing.T) {
	verifyEngineScenario(t, "dispatch_path_fills_btc_and_eth_through_nautilus")
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/trading/e2e_market_order.rs:211
//	test: rest_path_fills_btc_and_publishes_to_centrifugo
func TestRestPathFillsBtcAndPublishesToCentrifugo(t *testing.T) {
	verifyEngineScenario(t, "rest_path_fills_btc_and_publishes_to_centrifugo")
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/trading/e2e_market_order.rs:347
//	test: full_cycle_real_hl_open_hold_close_pnl
func TestFullCycleRealHLOpenHoldClosePNL(t *testing.T) {
	verifyEngineScenario(t, "full_cycle_real_hl_open_hold_close_pnl")
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/trading/e2e_modify_order.rs:49
//	test: resting_limit_can_be_modified
func TestRestingLimitCanBeModified(t *testing.T) {
	verifyEngineScenario(t, "resting_limit_can_be_modified")
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/trading/e2e_multi_account.rs:43
//	test: multi_user_multi_account_trades_are_isolated_per_account
func TestMultiUserMultiAccountTradesAreIsolatedPerAccount(t *testing.T) {
	verifyEngineScenario(t, "multi_user_multi_account_trades_are_isolated_per_account")
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/trading/e2e_order_semantics.rs:98
//	test: order_semantics_tif_and_cumulative_fill
func TestOrderSemanticsTIFAndCumulativeFill(t *testing.T) {
	verifyEngineScenario(t, "order_semantics_tif_and_cumulative_fill")
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/trading/e2e_pnl_settlement.rs:172
//	test: closed_position_realized_pnl_settles_into_total
func TestClosedPositionRealizedPNLSettlesIntoTotal(t *testing.T) {
	verifyEngineScenario(t, "closed_position_realized_pnl_settles_into_total")
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/trading/e2e_pnl_settlement.rs:372
//	test: reopen_fill_realized_excludes_prior_cycle_pnl
func TestReopenFillRealizedExcludesPriorCyclePNL(t *testing.T) {
	verifyEngineScenario(t, "reopen_fill_realized_excludes_prior_cycle_pnl")
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/trading/e2e_pnl_settlement.rs:450
//	test: recovered_position_keeps_realized_pnl_across_restart
func TestRecoveredPositionKeepsRealizedPNLAcrossRestart(t *testing.T) {
	verifyEngineScenario(t, "recovered_position_keeps_realized_pnl_across_restart")
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/trading/e2e_pnl_settlement.rs:516
//	test: closed_history_keeps_every_reopened_cycle
func TestClosedHistoryKeepsEveryReopenedCycle(t *testing.T) {
	verifyEngineScenario(t, "closed_history_keeps_every_reopened_cycle")
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/trading/e2e_quote_injection.rs:48
//	test: injected_quote_fills_resting_limit
func TestInjectedQuoteFillsRestingLimit(t *testing.T) {
	verifyEngineScenario(t, "injected_quote_fills_resting_limit")
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/trading/e2e_recovery_margin.rs:69
//	test: recovered_position_is_margin_visible_after_restart
func TestRecoveredPositionIsMarginVisibleAfterRestart(t *testing.T) {
	verifyEngineScenario(t, "recovered_position_is_margin_visible_after_restart")
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/trading/e2e_reduce_flip.rs:46
//	test: close_only_rejects_an_oversized_flip_but_allows_a_sized_reduce
func TestCloseOnlyRejectsAnOversizedFlipButAllowsASizedReduce(t *testing.T) {
	verifyEngineScenario(t, "close_only_rejects_an_oversized_flip_but_allows_a_sized_reduce")
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/trading/e2e_reduce_only_clamp.rs:69
//	test: reduce_only_close_larger_than_position_clamps_to_flat_never_flips
func TestReduceOnlyCloseLargerThanPositionClampsToFlatNeverFlips(t *testing.T) {
	verifyEngineScenario(t, "reduce_only_close_larger_than_position_clamps_to_flat_never_flips")
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/trading/e2e_slippage_band.rs:118
//	test: market_slippage_band_is_enforced
func TestMarketSlippageBandIsEnforced(t *testing.T) {
	verifyEngineScenario(t, "market_slippage_band_is_enforced")
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/trading/e2e_slippage_band.rs:385
//	test: market_slippage_band_is_enforced_on_sell
func TestMarketSlippageBandIsEnforcedOnSell(t *testing.T) {
	verifyEngineScenario(t, "market_slippage_band_is_enforced_on_sell")
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/trading/e2e_slippage_band.rs:495
//	test: market_fills_buy_at_ask_sell_at_bid
func TestMarketFillsBuyAtAskSellAtBid(t *testing.T) {
	verifyEngineScenario(t, "market_fills_buy_at_ask_sell_at_bid")
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/trading/e2e_slippage_band.rs:551
//	test: fill_pricer_rejects_out_of_band_and_admits_in_band
func TestFillPricerRejectsOutOfBandAndAdmitsInBand(t *testing.T) {
	verifyEngineScenario(t, "fill_pricer_rejects_out_of_band_and_admits_in_band")
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/trading/e2e_slippage_plumbing.rs:41
//	test: market_order_carries_band_and_ref_price_and_still_fills
func TestMarketOrderCarriesBandAndRefPriceAndStillFills(t *testing.T) {
	verifyEngineScenario(t, "market_order_carries_band_and_ref_price_and_still_fills")
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/trading/e2e_stop_order.rs:77
//	test: breached_stop_fills_resting_stops_work
func TestBreachedStopFillsRestingStopsWork(t *testing.T) {
	verifyEngineScenario(t, "breached_stop_fills_resting_stops_work")
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/trading/e2e_stop_trigger.rs:146
//	test: resting_stop_limits_trigger_then_fill
func TestRestingStopLimitsTriggerThenFill(t *testing.T) {
	verifyEngineScenario(t, "resting_stop_limits_trigger_then_fill")
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/trading/e2e_touch_trigger.rs:124
//	test: take_profit_market_holds_until_the_favorable_touch_cross
func TestTakeProfitMarketHoldsUntilTheFavorableTouchCross(t *testing.T) {
	verifyEngineScenario(t, "take_profit_market_holds_until_the_favorable_touch_cross")
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/trading/e2e_touch_trigger.rs:183
//	test: a_parked_touch_order_cancels_and_does_not_stay_working
func TestAParkedTouchOrderCancelsAndDoesNotStayWorking(t *testing.T) {
	verifyEngineScenario(t, "a_parked_touch_order_cancels_and_does_not_stay_working")
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/trading/e2e_touch_trigger.rs:260
//	test: take_profit_limit_rests_and_fills_on_the_favorable_cross
func TestTakeProfitLimitRestsAndFillsOnTheFavorableCross(t *testing.T) {
	verifyEngineScenario(t, "take_profit_limit_rests_and_fills_on_the_favorable_cross")
}
