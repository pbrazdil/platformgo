package orderbook

import (
	"math"
	"testing"

	"github.com/upcomers-org/platformgo/internal/decimal"
)

func top(t *testing.T, ladder *Ladder) *Level {
	t.Helper()
	level, ok := ladder.Top()
	if !ok {
		t.Fatal("ladder has no top")
	}
	return level
}

func topPrice(t *testing.T, ladder *Ladder, want string) {
	t.Helper()
	requirePrice(t, top(t, ladder).Price.Value, want)
}

// Ported from nautilus-trader@116c9b5159ebeb6b578b737d72298cac8d723723
// sources:
//
//	crates/model/src/orderbook/ladder.rs:522 test_is_empty
//	crates/model/src/orderbook/ladder.rs:528 test_is_empty_after_add
//	crates/model/src/orderbook/ladder.rs:540 test_add_bulk_empty
//	crates/model/src/orderbook/ladder.rs:550 test_add_bulk_orders
func checkLadderEmptyAndBulk(t *testing.T) {
	ladder := NewLadder(Buy, L3MBO)
	if !ladder.Empty() {
		t.Fatal("new ladder is not empty")
	}
	ladder.AddBulk(nil)
	if !ladder.Empty() {
		t.Fatal("empty bulk changed ladder")
	}
	ladder.AddBulk([]Order{
		order(Buy, "10.00", "20", 1),
		order(Buy, "10.00", "30", 2),
		order(Buy, "10.00", "50", 3),
	})
	if ladder.Empty() || ladder.Len() != 1 || top(t, ladder).Len() != 3 {
		t.Fatalf("empty/levels/orders = %v/%d/%d", ladder.Empty(), ladder.Len(), top(t, ladder).Len())
	}
}

// Ported from nautilus-trader@116c9b5159ebeb6b578b737d72298cac8d723723
// sources:
//
//	crates/model/src/orderbook/ladder.rs:569 test_book_price_bid_sorting
//	crates/model/src/orderbook/ladder.rs:581 test_book_price_ask_sorting
//	crates/model/src/orderbook/ladder.rs:655 test_add_descending_buy_orders
//	crates/model/src/orderbook/ladder.rs:667 test_add_ascending_sell_orders
func checkBookPriceSorting(t *testing.T) {
	for _, tc := range []struct {
		side Side
		want string
	}{
		{Buy, "4.0"},
		{Sell, "1.0"},
	} {
		ladder := NewLadder(tc.side, L3MBO)
		for id, p := range []string{"2.0", "4.0", "1.0", "3.0"} {
			ladder.Add(order(tc.side, p, "1", uint64(id)), 0)
		}
		topPrice(t, ladder, tc.want)
	}
}

// Ported from nautilus-trader@116c9b5159ebeb6b578b737d72298cac8d723723
// sources:
//
//	crates/model/src/orderbook/ladder.rs:594 test_add_single_order
//	crates/model/src/orderbook/ladder.rs:606 test_add_multiple_buy_orders
//	crates/model/src/orderbook/ladder.rs:621 test_add_multiple_sell_orders
//	crates/model/src/orderbook/ladder.rs:641 test_add_to_same_price_level
func checkLadderAddAndTotals(t *testing.T) {
	bids := NewLadder(Buy, L3MBO)
	bids.AddBulk([]Order{
		order(Buy, "10.00", "20", 0),
		order(Buy, "9.00", "30", 1),
		order(Buy, "9.00", "50", 2),
		order(Buy, "8.00", "200", 3),
	})
	if bids.Len() != 3 {
		t.Fatalf("bid levels = %d", bids.Len())
	}
	requireDecimal(t, bids.Sizes(), "300")
	requireDecimal(t, bids.Exposures(), "2520")
	topPrice(t, bids, "10.0")

	asks := NewLadder(Sell, L3MBO)
	asks.AddBulk([]Order{
		order(Sell, "11.00", "20", 0),
		order(Sell, "12.00", "30", 1),
		order(Sell, "12.00", "50", 2),
		order(Sell, "13.00", "200", 3),
	})
	if asks.Len() != 3 {
		t.Fatalf("ask levels = %d", asks.Len())
	}
	requireDecimal(t, asks.Sizes(), "300")
	requireDecimal(t, asks.Exposures(), "3780")
	topPrice(t, asks, "11.0")

	same := NewLadder(Buy, L3MBO)
	same.Add(order(Buy, "10.00", "20", 1), 0)
	same.Add(order(Buy, "10.00", "30", 2), 0)
	if same.Len() != 1 {
		t.Fatalf("same-price levels = %d", same.Len())
	}
	requireDecimal(t, same.Sizes(), "50")
	requireDecimal(t, same.Exposures(), "500")
}

// Ported from nautilus-trader@116c9b5159ebeb6b578b737d72298cac8d723723
// sources:
//
//	crates/model/src/orderbook/ladder.rs:679 test_update_buy_order_price
//	crates/model/src/orderbook/ladder.rs:694 test_update_sell_order_price
//	crates/model/src/orderbook/ladder.rs:710 test_update_buy_order_size
//	crates/model/src/orderbook/ladder.rs:726 test_update_sell_order_size
//	crates/model/src/orderbook/ladder.rs:851 test_update_missing_order_inserts
func checkLadderUpdate(t *testing.T) {
	for _, side := range []Side{Buy, Sell} {
		ladder := NewLadder(side, L3MBO)
		ladder.Add(order(side, "11.00", "20", 1), 0)
		ladder.Update(order(side, "11.10", "20", 1), 0)
		if ladder.Len() != 1 {
			t.Fatalf("%s price-update levels = %d", side, ladder.Len())
		}
		requireDecimal(t, ladder.Sizes(), "20")
		requireDecimal(t, ladder.Exposures(), "222")
		topPrice(t, ladder, "11.1")
		ladder.Update(order(side, "11.10", "10", 1), 0)
		requireDecimal(t, ladder.Sizes(), "10")
		requireDecimal(t, ladder.Exposures(), "111")
	}
	upsert := NewLadder(Buy, L3MBO)
	inserted := order(Buy, "10.00", "20", 1)
	upsert.Update(inserted, 0)
	orders := top(t, upsert).Orders()
	if upsert.Len() != 1 || len(orders) != 1 || orders[0].ID != inserted.ID {
		t.Fatalf("upsert result: levels=%d orders=%+v", upsert.Len(), orders)
	}
}

// Ported from nautilus-trader@116c9b5159ebeb6b578b737d72298cac8d723723
// sources:
//
//	crates/model/src/orderbook/ladder.rs:742 test_delete_non_existing_order
//	crates/model/src/orderbook/ladder.rs:752 test_delete_buy_order
//	crates/model/src/orderbook/ladder.rs:768 test_delete_sell_order
func checkLadderDelete(t *testing.T) {
	for _, side := range []Side{Buy, Sell} {
		ladder := NewLadder(side, L3MBO)
		ladder.Delete(order(side, "10.00", "20", 1), 0, 0)
		if ladder.Len() != 0 {
			t.Fatalf("%s nonexistent delete changed ladder", side)
		}
		ladder.Add(order(side, "10.00", "20", 1), 0)
		ladder.Delete(order(side, "10.00", "10", 1), 0, 0)
		if ladder.Len() != 0 || !ladder.Empty() {
			t.Fatalf("%s delete left levels", side)
		}
		requireDecimal(t, ladder.Sizes(), "0")
		requireDecimal(t, ladder.Exposures(), "0")
		if _, ok := ladder.Top(); ok {
			t.Fatal("empty ladder has top")
		}
	}
}

// Ported from nautilus-trader@116c9b5159ebeb6b578b737d72298cac8d723723
// sources:
//
//	crates/model/src/orderbook/ladder.rs:784 test_ladder_sizes_empty
//	crates/model/src/orderbook/ladder.rs:794 test_ladder_exposures_empty
//	crates/model/src/orderbook/ladder.rs:804 test_ladder_sizes
//	crates/model/src/orderbook/ladder.rs:820 test_ladder_exposures
//	crates/model/src/orderbook/ladder.rs:836 test_iter_returns_fifo
//	crates/model/src/orderbook/ladder.rs:871 test_cache_consistency_after_operations
func checkLadderTotalsFIFOAndCache(t *testing.T) {
	ladder := NewLadder(Buy, L3MBO)
	requireDecimal(t, ladder.Sizes(), "0")
	requireDecimal(t, ladder.Exposures(), "0")
	ladder.Add(order(Buy, "10.00", "20", 1), 0)
	ladder.Add(order(Buy, "9.50", "30", 2), 0)
	requireDecimal(t, ladder.Sizes(), "50")
	requireDecimal(t, ladder.Exposures(), "485")
	for id, bookPrice := range ladder.cache {
		level := ladder.find(bookPrice)
		if level == nil || !level.Contains(id) {
			t.Fatalf("cache entry %d/%v has no order", id, bookPrice)
		}
	}
	fifo := NewLadder(Buy, L3MBO)
	fifo.Add(order(Buy, "10.00", "20", 1), 0)
	fifo.Add(order(Buy, "10.00", "30", 2), 0)
	orders := top(t, fifo).Orders()
	if len(orders) != 2 || orders[0].ID != 1 || orders[1].ID != 2 {
		t.Fatalf("FIFO = %+v", orders)
	}
}

// Ported from nautilus-trader@116c9b5159ebeb6b578b737d72298cac8d723723
// sources:
//
//	crates/model/src/orderbook/ladder.rs:892 test_simulate_fills_with_empty_book
//	crates/model/src/orderbook/ladder.rs:904 test_simulate_order_fills_with_no_size
//	crates/model/src/orderbook/ladder.rs:925 test_simulate_order_fills_buy_when_far_from_market
//	crates/model/src/orderbook/ladder.rs:955 test_simulate_order_fills_sell_when_far_from_market
func checkSimulateFillsEmptyAndOutsideLimit(t *testing.T) {
	maxPrice, _ := decimal.MaxPrice(2)
	minPrice, _ := decimal.MinPrice(2)
	if fills := NewLadder(Buy, L3MBO).SimulateFills(NewOrder(Buy, maxPrice, quantity("500"), 1)); len(fills) != 0 {
		t.Fatalf("empty fills = %+v", fills)
	}
	for _, tc := range []struct {
		side, orderSide Side
		resting, limit  string
	}{
		{Sell, Buy, "60.0", "50.00"},
		{Buy, Sell, "40.0", "50.00"},
		{Buy, Buy, "100.00", "150.00"},
	} {
		ladder := NewLadder(tc.side, L3MBO)
		ladder.Add(order(tc.side, tc.resting, "100", 1), 0)
		if fills := ladder.SimulateFills(order(tc.orderSide, tc.limit, "500", 2)); len(fills) != 0 {
			t.Fatalf("%s resting %s limit %s fills = %+v", tc.side, tc.resting, tc.limit, fills)
		}
	}
	_ = minPrice
}

// Ported from nautilus-trader@116c9b5159ebeb6b578b737d72298cac8d723723
// sources:
//
//	crates/model/src/orderbook/ladder.rs:981 test_simulate_order_fills_buy
//	crates/model/src/orderbook/ladder.rs:1030 test_simulate_order_fills_sell
//	crates/model/src/orderbook/ladder.rs:1079 test_simulate_order_fills_sell_with_size_at_limit_of_precision
func checkSimulateFillsPriceTimePriority(t *testing.T) {
	maxPrice, _ := decimal.MaxPrice(2)
	minPrice, _ := decimal.MinPrice(2)
	cases := []struct {
		name             string
		ladderSide, side Side
		prices           []string
		sizes            []string
		limit            decimal.Price
		target           string
		wantPrices       []string
		wantSizes        []string
	}{
		{"buy", Sell, Buy, []string{"100.00", "101.00", "102.00"}, []string{"100", "200", "400"}, maxPrice, "500", []string{"100", "101", "102"}, []string{"100", "200", "200"}},
		{"sell", Buy, Sell, []string{"102.00", "101.00", "100.00"}, []string{"100", "200", "400"}, minPrice, "500", []string{"102", "101", "100"}, []string{"100", "200", "200"}},
		{"precision", Buy, Sell, []string{"102.00", "101.00", "100.00"}, []string{"100.000000000", "200.000000000", "400.000000000"}, minPrice, "699.999999999", []string{"102", "101", "100"}, []string{"100.000000000", "200.000000000", "399.999999999"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ladder := NewLadder(tc.ladderSide, L3MBO)
			for i := range tc.prices {
				ladder.Add(order(tc.ladderSide, tc.prices[i], tc.sizes[i], uint64(i+1)), 0)
			}
			fills := ladder.SimulateFills(NewOrder(tc.side, tc.limit, quantity(tc.target), 4))
			if len(fills) != 3 {
				t.Fatalf("fills len = %d: %+v", len(fills), fills)
			}
			for i := range fills {
				requirePrice(t, fills[i].Price, tc.wantPrices[i])
				requireQuantity(t, fills[i].Quantity, tc.wantSizes[i])
			}
		})
	}
}

// Ported from nautilus-trader@116c9b5159ebeb6b578b737d72298cac8d723723
// source: crates/model/src/orderbook/ladder.rs:1128 test_boundary_prices
func checkLadderBoundaryPrices(t *testing.T) {
	maxPrice, _ := decimal.MaxPrice(1)
	minPrice, _ := decimal.MinPrice(1)
	bids := NewLadder(Buy, L3MBO)
	asks := NewLadder(Sell, L3MBO)
	bids.Add(NewOrder(Buy, minPrice, quantity("1"), 1), 0)
	asks.Add(NewOrder(Sell, maxPrice, quantity("1"), 1), 0)
	if !top(t, bids).Price.Value.Equal(minPrice) || !top(t, asks).Price.Value.Equal(maxPrice) {
		t.Fatal("boundary prices were not retained")
	}
}

// Ported from nautilus-trader@116c9b5159ebeb6b578b737d72298cac8d723723
// sources:
//
//	crates/model/src/orderbook/ladder.rs:1146 test_l1_single_delta_batches_replace_each_other
//	crates/model/src/orderbook/ladder.rs:1204 test_l2_orders_not_affected_by_l1_fix
//	crates/model/src/orderbook/ladder.rs:1232 test_zero_size_l1_order_clears_top
//	crates/model/src/orderbook/ladder.rs:1270 test_zero_size_order_to_empty_ladder
func checkL1ReplacementAndClear(t *testing.T) {
	ladder := NewLadder(Buy, L1MBP)
	flags := FlagMBP | FlagLast
	for i, p := range []string{"100.00", "101.00", "100.50"} {
		ladder.Add(order(Buy, p, "50", uint64(i+1)), flags)
		if ladder.Len() != 1 {
			t.Fatalf("replacement %d left %d levels", i, ladder.Len())
		}
	}
	topPrice(t, ladder, "100.50")
	ladder.Add(order(Buy, "101.00", "0.000000000", 99), 0)
	if ladder.Len() != 0 || ladder.CacheLen() != 0 {
		t.Fatalf("zero clear left levels/cache = %d/%d", ladder.Len(), ladder.CacheLen())
	}
	empty := NewLadder(Sell, L1MBP)
	empty.Add(order(Sell, "100.00", "0.000000000", 2), 0)
	if !empty.Empty() || empty.CacheLen() != 0 {
		t.Fatal("zero add changed empty ladder")
	}
	l3 := NewLadder(Buy, L3MBO)
	l3.Add(order(Buy, "100", "50", 100), 0)
	l3.Add(order(Buy, "99", "60", 99), 0)
	if l3.Len() != 2 {
		t.Fatalf("L3 has %d levels, want 2", l3.Len())
	}
	topPrice(t, l3, "100")
}

// Ported from nautilus-trader@116c9b5159ebeb6b578b737d72298cac8d723723
// sources:
//
//	crates/model/src/orderbook/ladder.rs:1292 test_l3_order_id_collision_no_ghost_levels
//	crates/model/src/orderbook/ladder.rs:1337 test_l1_vs_l3_different_behavior_same_order_id
func checkL1AndL3DuplicateOrderIDBehavior(t *testing.T) {
	l3 := NewLadder(Buy, L3MBO)
	l3.Add(order(Buy, "100.00", "50", 1), 0)
	l3.Add(order(Buy, "99.00", "60", 1), 0)
	if l3.Len() != 2 {
		t.Fatalf("L3 duplicate ID levels = %d", l3.Len())
	}
	got := []string{l3.Levels()[0].Price.Value.String(), l3.Levels()[1].Price.Value.String()}
	if got[0] != "100.00" || got[1] != "99.00" {
		t.Fatalf("L3 prices = %v", got)
	}
	l1 := NewLadder(Buy, L1MBP)
	l1.Add(order(Buy, "100.00", "50", 1), 0)
	l1.Add(order(Buy, "101.00", "60", 1), 0)
	if l1.Len() != 1 {
		t.Fatalf("L1 duplicate ID levels = %d", l1.Len())
	}
	topPrice(t, l1, "101")
}

// Ported from nautilus-trader@116c9b5159ebeb6b578b737d72298cac8d723723
// sources:
//
//	crates/model/src/orderbook/ladder.rs:1394 test_l1_multi_delta_batch_keeps_best_of_final_two
//	crates/model/src/orderbook/ladder.rs:1429 test_l1_retain_best_only_cache_consistency
//	crates/model/src/orderbook/ladder.rs:1461 test_l1_sequential_replacement_allows_price_degradation
func checkL1MBPBatchesAndSequentialReplacement(t *testing.T) {
	cases := []struct {
		side   Side
		prices []string
		want   string
	}{
		{Buy, []string{"99", "100", "101", "102"}, "102"},
		{Buy, []string{"102", "101", "100", "99"}, "100"},
		{Sell, []string{"105", "104", "103", "102"}, "102"},
		{Sell, []string{"102", "103", "104", "105"}, "104"},
	}
	for _, tc := range cases {
		ladder := NewLadder(tc.side, L1MBP)
		for i, p := range tc.prices {
			flags := FlagMBP
			if i == len(tc.prices)-1 {
				flags |= FlagLast
			}
			ladder.Add(order(tc.side, p, "10", uint64(i+100)), flags)
		}
		if ladder.Len() != 1 || ladder.CacheLen() != 1 {
			t.Fatalf("%s levels/cache = %d/%d", tc.side, ladder.Len(), ladder.CacheLen())
		}
		topPrice(t, ladder, tc.want)
	}
	replacement := NewLadder(Buy, L1MBP)
	replacement.Add(order(Buy, "101", "50", 1), 0)
	replacement.Add(order(Buy, "100", "60", 1), 0)
	topPrice(t, replacement, "100")
	first, _ := top(t, replacement).First()
	requireQuantity(t, first.Quantity, "60")
}

// Ported from nautilus-trader@116c9b5159ebeb6b578b737d72298cac8d723723
// sources:
//
//	crates/model/src/orderbook/ladder.rs:1511 test_l1_consecutive_batches_clear_between
//	crates/model/src/orderbook/ladder.rs:1570 test_l1_zero_size_clears_regardless_of_order_id
//	crates/model/src/orderbook/ladder.rs:1605 test_l1_f_mbp_without_f_last_does_not_accumulate
//	crates/model/src/orderbook/ladder.rs:1642 test_l1_f_mbp_two_delta_batch_retains_best
func checkL1MBPBatchTransitions(t *testing.T) {
	ladder := NewLadder(Buy, L1MBP)
	addBatch := func(prices []string, base uint64) {
		for i, p := range prices {
			flags := FlagMBP
			if i == len(prices)-1 {
				flags |= FlagLast
			}
			ladder.Add(order(Buy, p, "10", base+uint64(i)), flags)
		}
	}
	addBatch([]string{"100", "101", "102"}, 100)
	topPrice(t, ladder, "102")
	addBatch([]string{"97", "98", "99"}, 200)
	topPrice(t, ladder, "99")

	stream := NewLadder(Buy, L1MBP)
	for i, p := range []string{"100", "99", "98", "97", "96", "95", "94", "93", "92", "91"} {
		stream.Add(order(Buy, p, "10", uint64(i+100)), FlagMBP)
		if stream.Len() != 1 {
			t.Fatalf("stream iteration %d has %d levels", i, stream.Len())
		}
	}
	topPrice(t, stream, "91")

	two := NewLadder(Sell, L1MBP)
	two.Add(order(Sell, "100", "10", 100), FlagMBP)
	two.Add(order(Sell, "101", "20", 101), FlagMBP|FlagLast)
	topPrice(t, two, "100")

	two.Add(order(Sell, "100", "0", math.MaxUint64), 0)
	if !two.Empty() || two.CacheLen() != 0 {
		t.Fatal("zero with different ID did not clear")
	}
}

// Ported from nautilus-trader@116c9b5159ebeb6b578b737d72298cac8d723723
// sources:
//
//	crates/model/src/orderbook/ladder.rs:1674 test_l1_snapshot_batch_accumulates_all_levels_bids
//	crates/model/src/orderbook/ladder.rs:1708 test_l1_snapshot_batch_accumulates_all_levels_asks
//	crates/model/src/orderbook/ladder.rs:1742 test_l1_snapshot_vs_mbp_different_accumulation_behavior
func checkL1SnapshotAccumulatesBest(t *testing.T) {
	for _, tc := range []struct {
		side   Side
		prices []string
		want   string
	}{
		{Buy, []string{"98", "99", "100", "101"}, "101"},
		{Sell, []string{"104", "103", "102", "101"}, "101"},
	} {
		ladder := NewLadder(tc.side, L1MBP)
		for i, p := range tc.prices {
			flags := FlagSnapshot
			if i == len(tc.prices)-1 {
				flags |= FlagLast
			}
			ladder.Add(order(tc.side, p, "10", uint64(i+100)), flags)
		}
		if ladder.Len() != 1 {
			t.Fatalf("%s snapshot levels = %d", tc.side, ladder.Len())
		}
		topPrice(t, ladder, tc.want)
	}
}

// Ported from nautilus-trader@116c9b5159ebeb6b578b737d72298cac8d723723
// sources:
//
//	crates/model/src/orderbook/ladder.rs:1790 test_l1_snapshot_after_incomplete_mbp_stream
//	crates/model/src/orderbook/ladder.rs:1831 test_l1_snapshot_clears_previous_batch
//	crates/model/src/orderbook/ladder.rs:1874 test_l1_single_delta_snapshot_after_mbp_batch
func checkL1SnapshotClearsStaleState(t *testing.T) {
	ladder := NewLadder(Buy, L1MBP)
	ladder.Add(order(Buy, "101", "10", 100), FlagMBP)
	ladder.Clear()
	for i, p := range []string{"98", "99", "100"} {
		flags := FlagSnapshot
		if i == 2 {
			flags |= FlagLast
		}
		ladder.Add(order(Buy, p, "10", uint64(i+200)), flags)
	}
	topPrice(t, ladder, "100")

	for i, p := range []string{"95", "96", "97"} {
		flags := FlagSnapshot
		if i == 2 {
			flags |= FlagLast
		}
		ladder.Add(order(Buy, p, "20", uint64(i+300)), flags)
	}
	topPrice(t, ladder, "97")

	mbp := NewLadder(Buy, L1MBP)
	mbp.Add(order(Buy, "100", "10", 1), FlagMBP)
	mbp.Add(order(Buy, "101", "10", 2), FlagMBP|FlagLast)
	mbp.Add(order(Buy, "95", "20", 100), FlagSnapshot|FlagLast)
	if mbp.Len() != 1 {
		t.Fatalf("single snapshot levels = %d", mbp.Len())
	}
	topPrice(t, mbp, "95")
}
