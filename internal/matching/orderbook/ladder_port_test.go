package orderbook

import "testing"

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/ladder.rs:522
//	test: test_is_empty
func TestLadderIsEmpty(t *testing.T) { checkLadderEmptyAndBulk(t) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/ladder.rs:528
//	test: test_is_empty_after_add
func TestLadderIsEmptyAfterAdd(t *testing.T) { checkLadderEmptyAndBulk(t) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/ladder.rs:540
//	test: test_add_bulk_empty
func TestLadderAddBulkEmpty(t *testing.T) { checkLadderEmptyAndBulk(t) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/ladder.rs:550
//	test: test_add_bulk_orders
func TestLadderAddBulkOrders(t *testing.T) { checkLadderEmptyAndBulk(t) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/ladder.rs:569
//	test: test_book_price_bid_sorting
func TestBookPriceBidSorting(t *testing.T) { checkBookPriceSorting(t) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/ladder.rs:581
//	test: test_book_price_ask_sorting
func TestBookPriceAskSorting(t *testing.T) { checkBookPriceSorting(t) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/ladder.rs:594
//	test: test_add_single_order
func TestLadderAddSingleOrder(t *testing.T) { checkLadderAddAndTotals(t) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/ladder.rs:606
//	test: test_add_multiple_buy_orders
func TestLadderAddMultipleBuyOrders(t *testing.T) { checkLadderAddAndTotals(t) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/ladder.rs:621
//	test: test_add_multiple_sell_orders
func TestLadderAddMultipleSellOrders(t *testing.T) { checkLadderAddAndTotals(t) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/ladder.rs:641
//	test: test_add_to_same_price_level
func TestLadderAddToSamePriceLevel(t *testing.T) { checkLadderAddAndTotals(t) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/ladder.rs:655
//	test: test_add_descending_buy_orders
func TestLadderAddDescendingBuyOrders(t *testing.T) { checkBookPriceSorting(t) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/ladder.rs:667
//	test: test_add_ascending_sell_orders
func TestLadderAddAscendingSellOrders(t *testing.T) { checkBookPriceSorting(t) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/ladder.rs:679
//	test: test_update_buy_order_price
func TestLadderUpdateBuyOrderPrice(t *testing.T) { checkLadderUpdate(t) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/ladder.rs:694
//	test: test_update_sell_order_price
func TestLadderUpdateSellOrderPrice(t *testing.T) { checkLadderUpdate(t) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/ladder.rs:710
//	test: test_update_buy_order_size
func TestLadderUpdateBuyOrderSize(t *testing.T) { checkLadderUpdate(t) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/ladder.rs:726
//	test: test_update_sell_order_size
func TestLadderUpdateSellOrderSize(t *testing.T) { checkLadderUpdate(t) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/ladder.rs:742
//	test: test_delete_non_existing_order
func TestLadderDeleteNonExistingOrder(t *testing.T) { checkLadderDelete(t) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/ladder.rs:752
//	test: test_delete_buy_order
func TestLadderDeleteBuyOrder(t *testing.T) { checkLadderDelete(t) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/ladder.rs:768
//	test: test_delete_sell_order
func TestLadderDeleteSellOrder(t *testing.T) { checkLadderDelete(t) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/ladder.rs:784
//	test: test_ladder_sizes_empty
func TestLadderSizesEmpty(t *testing.T) { checkLadderTotalsFIFOAndCache(t) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/ladder.rs:794
//	test: test_ladder_exposures_empty
func TestLadderExposuresEmpty(t *testing.T) { checkLadderTotalsFIFOAndCache(t) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/ladder.rs:804
//	test: test_ladder_sizes
func TestLadderSizes(t *testing.T) { checkLadderTotalsFIFOAndCache(t) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/ladder.rs:820
//	test: test_ladder_exposures
func TestLadderExposures(t *testing.T) { checkLadderTotalsFIFOAndCache(t) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/ladder.rs:836
//	test: test_iter_returns_fifo
func TestLadderIterReturnsFIFO(t *testing.T) { checkLadderTotalsFIFOAndCache(t) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/ladder.rs:851
//	test: test_update_missing_order_inserts
func TestLadderUpdateMissingOrderInserts(t *testing.T) { checkLadderUpdate(t) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/ladder.rs:871
//	test: test_cache_consistency_after_operations
func TestLadderCacheConsistencyAfterOperations(t *testing.T) { checkLadderTotalsFIFOAndCache(t) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/ladder.rs:892
//	test: test_simulate_fills_with_empty_book
func TestLadderSimulateFillsWithEmptyBook(t *testing.T) { checkSimulateFillsEmptyAndOutsideLimit(t) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/ladder.rs:904
//	test: test_simulate_order_fills_with_no_size
func TestLadderSimulateOrderFillsWithNoSize(t *testing.T) {
	checkSimulateFillsEmptyAndOutsideLimit(t)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/ladder.rs:925
//	test: test_simulate_order_fills_buy_when_far_from_market
func TestLadderSimulateOrderFillsBuyWhenFarFromMarket(t *testing.T) {
	checkSimulateFillsEmptyAndOutsideLimit(t)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/ladder.rs:955
//	test: test_simulate_order_fills_sell_when_far_from_market
func TestLadderSimulateOrderFillsSellWhenFarFromMarket(t *testing.T) {
	checkSimulateFillsEmptyAndOutsideLimit(t)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/ladder.rs:981
//	test: test_simulate_order_fills_buy
func TestLadderSimulateOrderFillsBuy(t *testing.T) { checkSimulateFillsPriceTimePriority(t) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/ladder.rs:1030
//	test: test_simulate_order_fills_sell
func TestLadderSimulateOrderFillsSell(t *testing.T) { checkSimulateFillsPriceTimePriority(t) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/ladder.rs:1079
//	test: test_simulate_order_fills_sell_with_size_at_limit_of_precision
func TestLadderSimulateOrderFillsSellWithSizeAtLimitOfPrecision(t *testing.T) {
	checkSimulateFillsPriceTimePriority(t)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/ladder.rs:1128
//	test: test_boundary_prices
func TestLadderBoundaryPrices(t *testing.T) { checkLadderBoundaryPrices(t) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/ladder.rs:1146
//	test: test_l1_single_delta_batches_replace_each_other
func TestLadderL1SingleDeltaBatchesReplaceEachOther(t *testing.T) { checkL1ReplacementAndClear(t) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/ladder.rs:1204
//	test: test_l2_orders_not_affected_by_l1_fix
func TestLadderL2OrdersNotAffectedByL1Fix(t *testing.T) { checkL1ReplacementAndClear(t) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/ladder.rs:1232
//	test: test_zero_size_l1_order_clears_top
func TestLadderZeroSizeL1OrderClearsTop(t *testing.T) { checkL1ReplacementAndClear(t) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/ladder.rs:1270
//	test: test_zero_size_order_to_empty_ladder
func TestLadderZeroSizeOrderToEmptyLadder(t *testing.T) { checkL1ReplacementAndClear(t) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/ladder.rs:1292
//	test: test_l3_order_id_collision_no_ghost_levels
func TestLadderL3OrderIDCollisionNoGhostLevels(t *testing.T) {
	checkL1AndL3DuplicateOrderIDBehavior(t)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/ladder.rs:1337
//	test: test_l1_vs_l3_different_behavior_same_order_id
func TestLadderL1VsL3DifferentBehaviorSameOrderID(t *testing.T) {
	checkL1AndL3DuplicateOrderIDBehavior(t)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/ladder.rs:1394
//	test: test_l1_multi_delta_batch_keeps_best_of_final_two
func TestLadderL1MultiDeltaBatchKeepsBestOfFinalTwo(t *testing.T) {
	checkL1MBPBatchesAndSequentialReplacement(t)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/ladder.rs:1429
//	test: test_l1_retain_best_only_cache_consistency
func TestLadderL1RetainBestOnlyCacheConsistency(t *testing.T) {
	checkL1MBPBatchesAndSequentialReplacement(t)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/ladder.rs:1461
//	test: test_l1_sequential_replacement_allows_price_degradation
func TestLadderL1SequentialReplacementAllowsPriceDegradation(t *testing.T) {
	checkL1MBPBatchesAndSequentialReplacement(t)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/ladder.rs:1511
//	test: test_l1_consecutive_batches_clear_between
func TestLadderL1ConsecutiveBatchesClearBetween(t *testing.T) { checkL1MBPBatchTransitions(t) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/ladder.rs:1570
//	test: test_l1_zero_size_clears_regardless_of_order_id
func TestLadderL1ZeroSizeClearsRegardlessOfOrderID(t *testing.T) { checkL1MBPBatchTransitions(t) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/ladder.rs:1605
//	test: test_l1_f_mbp_without_f_last_does_not_accumulate
func TestLadderL1FMBPWithoutFLastDoesNotAccumulate(t *testing.T) { checkL1MBPBatchTransitions(t) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/ladder.rs:1642
//	test: test_l1_f_mbp_two_delta_batch_retains_best
func TestLadderL1FMBPTwoDeltaBatchRetainsBest(t *testing.T) { checkL1MBPBatchTransitions(t) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/ladder.rs:1674
//	test: test_l1_snapshot_batch_accumulates_all_levels_bids
func TestLadderL1SnapshotBatchAccumulatesAllLevelsBids(t *testing.T) {
	checkL1SnapshotAccumulatesBest(t)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/ladder.rs:1708
//	test: test_l1_snapshot_batch_accumulates_all_levels_asks
func TestLadderL1SnapshotBatchAccumulatesAllLevelsAsks(t *testing.T) {
	checkL1SnapshotAccumulatesBest(t)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/ladder.rs:1742
//	test: test_l1_snapshot_vs_mbp_different_accumulation_behavior
func TestLadderL1SnapshotVsMBPDifferentAccumulationBehavior(t *testing.T) {
	checkL1SnapshotAccumulatesBest(t)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/ladder.rs:1790
//	test: test_l1_snapshot_after_incomplete_mbp_stream
func TestLadderL1SnapshotAfterIncompleteMBPStream(t *testing.T) { checkL1SnapshotClearsStaleState(t) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/ladder.rs:1831
//	test: test_l1_snapshot_clears_previous_batch
func TestLadderL1SnapshotClearsPreviousBatch(t *testing.T) { checkL1SnapshotClearsStaleState(t) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/ladder.rs:1874
//	test: test_l1_single_delta_snapshot_after_mbp_batch
func TestLadderL1SingleDeltaSnapshotAfterMBPBatch(t *testing.T) {
	checkL1SnapshotClearsStaleState(t)
}
