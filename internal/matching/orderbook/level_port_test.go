package orderbook

import "testing"

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/level.rs:304
//	test: test_empty_level
func TestLevelEmptyLevel(t *testing.T) { checkLevelEmpty(t) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/level.rs:311
//	test: test_level_from_order
func TestLevelFromOrder(t *testing.T) { checkLevelFromOrder(t) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/level.rs:324
//	test: test_add_order_incorrect_price_level
func TestLevelAddOrderIncorrectPriceLevel(t *testing.T) { checkLevelRejectsIncorrectPrice(t) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/level.rs:334
//	test: test_add_bulk_orders_incorrect_price
func TestLevelAddBulkOrdersIncorrectPrice(t *testing.T) { checkLevelRejectsIncorrectPrice(t) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/level.rs:345
//	test: test_add_bulk_empty
func TestLevelAddBulkEmpty(t *testing.T) { checkLevelAddBulkEmpty(t) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/level.rs:353
//	test: test_comparisons_bid_side
func TestLevelComparisonsBidSide(t *testing.T) { checkLevelComparisonAndSorting(t) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/level.rs:361
//	test: test_comparisons_ask_side
func TestLevelComparisonsAskSide(t *testing.T) { checkLevelComparisonAndSorting(t) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/level.rs:375
//	test: test_book_level_sorting
func TestBookLevelSorting(t *testing.T) { checkLevelComparisonAndSorting(t) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/level.rs:397
//	test: test_add_single_order
func TestLevelAddSingleOrder(t *testing.T) { checkLevelAddAndFIFO(t) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/level.rs:410
//	test: test_add_multiple_orders
func TestLevelAddMultipleOrders(t *testing.T) { checkLevelAddAndFIFO(t) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/level.rs:425
//	test: test_get_orders
func TestLevelGetOrders(t *testing.T) { checkLevelAddAndFIFO(t) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/level.rs:441
//	test: test_iter_returns_fifo
func TestLevelIterReturnsFIFO(t *testing.T) { checkLevelAddAndFIFO(t) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/level.rs:454
//	test: test_update_order
func TestLevelUpdateOrder(t *testing.T) { checkLevelUpdateSemantics(t) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/level.rs:468
//	test: test_update_inserts_if_missing
func TestLevelUpdateInsertsIfMissing(t *testing.T) { checkLevelUpdateSemantics(t) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/level.rs:478
//	test: test_update_zero_size_nonexistent
func TestLevelUpdateZeroSizeNonexistent(t *testing.T) { checkLevelUpdateSemantics(t) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/level.rs:487
//	test: test_fifo_order_after_updates
func TestLevelFIFOOrderAfterUpdates(t *testing.T) { checkLevelUpdateSemantics(t) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/level.rs:509
//	test: test_insertion_order_after_mixed_operations
func TestLevelInsertionOrderAfterMixedOperations(t *testing.T) { checkLevelUpdateSemantics(t) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/level.rs:534
//	test: test_update_order_incorrect_price
func TestLevelUpdateOrderIncorrectPrice(t *testing.T) { checkLevelRejectsIncorrectPrice(t) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/level.rs:550
//	test: test_update_order_with_zero_size
func TestLevelUpdateOrderWithZeroSize(t *testing.T) { checkLevelUpdateSemantics(t) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/level.rs:564
//	test: test_delete_nonexistent_order
func TestLevelDeleteNonexistentOrder(t *testing.T) { checkLevelDeleteAndRemove(t) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/level.rs:573
//	test: test_delete_order
func TestLevelDeleteOrder(t *testing.T) { checkLevelDeleteAndRemove(t) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/level.rs:601
//	test: test_remove_order_by_id
func TestLevelRemoveOrderByID(t *testing.T) { checkLevelDeleteAndRemove(t) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/level.rs:629
//	test: test_add_bulk_orders
func TestLevelAddBulkOrders(t *testing.T) { checkLevelBulkSizeAndExposure(t) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/level.rs:655
//	test: test_maximum_order_id
func TestLevelMaximumOrderID(t *testing.T) { checkLevelBulkSizeAndExposure(t) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/level.rs:675
//	test: test_remove_nonexistent_order
func TestLevelRemoveNonexistentOrder(t *testing.T) { checkLevelDeleteAndRemove(t) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/level.rs:682
//	test: test_size
func TestLevelSize(t *testing.T) { checkLevelBulkSizeAndExposure(t) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/level.rs:694
//	test: test_size_raw
func TestLevelSizeRaw(t *testing.T) { checkLevelBulkSizeAndExposure(t) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/level.rs:709
//	test: test_size_decimal
func TestLevelSizeDecimal(t *testing.T) { checkLevelBulkSizeAndExposure(t) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/level.rs:721
//	test: test_exposure
func TestLevelExposure(t *testing.T) { checkLevelBulkSizeAndExposure(t) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/level.rs:736
//	test: test_exposure_raw_exact_whole
func TestLevelExposureRawExactWhole(t *testing.T) { checkLevelExposureRaw(t) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/level.rs:757
//	test: test_exposure_raw_truncates_sub_raw_unit
func TestLevelExposureRawTruncatesSubRawUnit(t *testing.T) { checkLevelExposureRaw(t) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/level.rs:768
//	test: test_exposure_raw_accumulates_exactly
func TestLevelExposureRawAccumulatesExactly(t *testing.T) { checkLevelExposureRaw(t) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/level.rs:781
//	test: test_exposure_raw_preserves_non_saturating_raw_units
func TestLevelExposureRawPreservesNonSaturatingRawUnits(t *testing.T) {
	checkLevelExposureRawPreservesExactEconomicValueAcrossSourceScales(t)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/level.rs:792
//	test: test_exposure_raw_avoids_phantom_overflow
func TestLevelExposureRawAvoidsPhantomOverflow(t *testing.T) { checkLevelExposureRaw(t) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/level.rs:810
//	test: test_exposure_raw_saturates_single_order
func TestLevelExposureRawSaturatesSingleOrder(t *testing.T) { checkLevelExposureRaw(t) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/level.rs:833
//	test: test_exposure_raw_accumulation_saturates
func TestLevelExposureRawAccumulationSaturates(t *testing.T) { checkLevelExposureRaw(t) }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/level.rs:867
//	test: test_exposure_raw_preserves_native_defi_scales
func TestLevelExposureRawPreservesNativeDefiScales(t *testing.T) {
	checkLevelExposureRawPreservesExactEconomicValueAcrossSourceScales(t)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/level.rs:888
//	test: test_exposure_raw_preserves_mixed_defi_scales
func TestLevelExposureRawPreservesMixedDefiScales(t *testing.T) {
	checkLevelExposureRawPreservesExactEconomicValueAcrossSourceScales(t)
}
