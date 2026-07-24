package orderbook

import (
	"fmt"
	"math/big"
	"slices"
	"strings"
	"testing"

	"github.com/upcomers-org/platformgo/internal/decimal"
)

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:76
//	test: test_book_integrity_cases
func TestBookBookIntegrityCases(t *testing.T) {
	valid := NewBook("AAPL.XNAS", L2MBP)
	addLevels(valid, []string{"99"}, []string{"101"})
	if err := valid.Integrity(); err != nil {
		t.Fatal(err)
	}
	crossed := NewBook("AAPL.XNAS", L2MBP)
	addLevels(crossed, []string{"101"}, []string{"99"})
	requireErrorIs(t, crossed.Integrity(), ErrOrdersCrossed)
	l1 := NewBook("AAPL.XNAS", L1MBP)
	addLevels(l1, []string{"99", "98"}, nil)
	if err := l1.Integrity(); err != nil {
		t.Fatal(err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:93
//	test: test_book_integrity_price_boundaries
func TestBookBookIntegrityPriceBoundaries(t *testing.T) {
	book := NewBook("AAPL.XNAS", L2MBP)
	min, _ := decimal.MinPrice(2)
	max, _ := decimal.MaxPrice(2)
	book.Add(NewOrder(Buy, min, portQuantity("100"), 1), 0, 1, 1)
	book.Add(NewOrder(Sell, max, portQuantity("100"), 2), 0, 2, 2)
	if err := book.Integrity(); err != nil {
		t.Fatal(err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:109
//	test: test_book_integrity_quantity_sizes
func TestBookBookIntegrityQuantitySizes(t *testing.T) {
	for _, size := range []string{"100", "1000", "1000000"} {
		book := NewBook("AAPL.XNAS", L2MBP)
		book.Add(NewOrder(Buy, portPrice("100.00"), portQuantity(size), 1), 0, 1, 1)
		if err := book.Integrity(); err != nil {
			t.Fatal(err)
		}
		requireBookPortQuantityResult(t, book.BestBidSize, size)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:126
//	test: test_book_display
func TestBookBookDisplay(t *testing.T) {
	book := NewBook("ETHUSDT-PERP.BINANCE", L2MBP)
	if got := book.String(); got != "OrderBook(instrument_id=ETHUSDT-PERP.BINANCE, book_type=L2_MBP, update_count=0)" {
		t.Fatalf("display = %q", got)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:136
//	test: test_book_empty_state
func TestBookBookEmptyState(t *testing.T) {
	book := NewBook("ETHUSDT-PERP.BINANCE", L2MBP)
	if book.HasBid() || book.HasAsk() {
		t.Fatal("empty book reports liquidity")
	}
	if _, ok := book.BestBidPrice(); ok {
		t.Fatal("empty bid price present")
	}
	if _, ok := book.BestAskPrice(); ok {
		t.Fatal("empty ask price present")
	}
	if _, ok := book.BestBidSize(); ok {
		t.Fatal("empty bid size present")
	}
	if _, ok := book.BestAskSize(); ok {
		t.Fatal("empty ask size present")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:149
//	test: test_book_single_bid_state
func TestBookBookSingleBidState(t *testing.T) {
	book := NewBook("ETHUSDT-PERP.BINANCE", L3MBO)
	book.Add(NewOrder(Buy, portPrice("1.000"), portQuantity("1.0"), 1), 0, 1, 100)
	requireBookPortPriceResult(t, book.BestBidPrice, "1.000")
	requireBookPortQuantityResult(t, book.BestBidSize, "1.0")
	if !book.HasBid() {
		t.Fatal("bid absent")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:166
//	test: test_book_single_ask_state
func TestBookBookSingleAskState(t *testing.T) {
	book := NewBook("ETHUSDT-PERP.BINANCE", L3MBO)
	book.Add(NewOrder(Sell, portPrice("2.000"), portQuantity("2.0"), 2), 0, 2, 200)
	requireBookPortPriceResult(t, book.BestAskPrice, "2.000")
	requireBookPortQuantityResult(t, book.BestAskSize, "2.0")
	if !book.HasAsk() {
		t.Fatal("ask absent")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:183
//	test: test_book_empty_book_spread
func TestBookBookEmptyBookSpread(t *testing.T) {
	book := NewBook("ETHUSDT-PERP.BINANCE", L3MBO)
	if _, ok := book.Spread(); ok {
		t.Fatal("empty spread present")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:190
//	test: test_book_spread_with_orders
func TestBookBookSpreadWithOrders(t *testing.T) {
	book := NewBook("ETHUSDT-PERP.BINANCE", L3MBO)
	addLevels(book, []string{"1.000"}, []string{"2.000"})
	got, ok := book.Spread()
	if !ok {
		t.Fatal("spread absent")
	}
	requireBookPortDecimal(t, got, "1.000")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:212
//	test: test_book_empty_book_midpoint
func TestBookBookEmptyBookMidpoint(t *testing.T) {
	book := NewBook("ETHUSDT-PERP.BINANCE", L2MBP)
	if _, ok := book.Midpoint(); ok {
		t.Fatal("empty midpoint present")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:219
//	test: test_book_midpoint_with_orders
func TestBookBookMidpointWithOrders(t *testing.T) {
	book := NewBook("ETHUSDT-PERP.BINANCE", L2MBP)
	addLevels(book, []string{"1.000"}, []string{"2.000"})
	got, ok := book.Midpoint()
	if !ok {
		t.Fatal("midpoint absent")
	}
	requireBookPortDecimal(t, got, "1.5000")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:242
//	test: test_book_get_price_for_quantity_no_market
func TestBookBookGetPriceForQuantityNoMarket(t *testing.T) {
	book := NewBook("ETHUSDT-PERP.BINANCE", L2MBP)
	for _, side := range []Side{Buy, Sell} {
		got, ok := book.AveragePriceForQuantity(portQuantity("1"), side)
		if ok || !got.IsZero() {
			t.Fatalf("empty average %s = %s,%v", side, got, ok)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:253
//	test: test_book_get_quantity_for_price_no_market
func TestBookBookGetQuantityForPriceNoMarket(t *testing.T) {
	book := NewBook("ETHUSDT-PERP.BINANCE", L2MBP)
	for _, side := range []Side{Buy, Sell} {
		if got := book.QuantityForPrice(portPrice("1"), side); !got.IsZero() {
			t.Fatalf("empty quantity %s = %s", side, got)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:264
//	test: test_book_get_price_for_quantity
func TestBookBookGetPriceForQuantity(t *testing.T) {
	book := executionBook()
	buy, ok := book.AveragePriceForQuantity(portQuantity("1.5"), Buy)
	if !ok {
		t.Fatal("buy average absent")
	}
	requireBookPortDecimal(t, buy, "2.0033333333333333")
	sell, ok := book.AveragePriceForQuantity(portQuantity("1.5"), Sell)
	if !ok {
		t.Fatal("sell average absent")
	}
	requireBookPortDecimal(t, sell, "0.9966666666666667")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:310
//	test: test_book_get_quantity_for_price
func TestBookBookGetQuantityForPrice(t *testing.T) {
	book := executionBook()
	requireBookPortDecimal(t, book.QuantityForPrice(portPrice("2.010"), Buy), "3.0")
	requireBookPortDecimal(t, book.QuantityForPrice(portPrice("0.990"), Sell), "3.0")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:368
//	test: test_book_get_quantity_at_level_empty_book
func TestBookBookGetQuantityAtLevelEmptyBook(t *testing.T) {
	book := NewBook("ETHUSDT-PERP.BINANCE", L2MBP)
	requireBookPortDecimal(t, book.QuantityAtLevel(portPrice("1.0"), Buy), "0")
	requireBookPortDecimal(t, book.QuantityAtLevel(portPrice("1.0"), Sell), "0")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:385
//	test: test_book_get_quantity_at_level_single_level
func TestBookBookGetQuantityAtLevelSingleLevel(t *testing.T) {
	book := NewBook("ETHUSDT-PERP.BINANCE", L2MBP)
	book.Add(NewOrder(Sell, portPrice("2.000"), portQuantity("100"), 1), 0, 1, 1)
	book.Add(NewOrder(Buy, portPrice("1.000"), portQuantity("50"), 2), 0, 2, 2)
	requireBookPortDecimal(t, book.QuantityAtLevel(portPrice("2.000"), Buy), "100")
	requireBookPortDecimal(t, book.QuantityAtLevel(portPrice("1.000"), Sell), "50")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:415
//	test: test_book_get_quantity_at_level_multiple_levels
func TestBookBookGetQuantityAtLevelMultipleLevels(t *testing.T) {
	book := executionBook()
	for _, tc := range []struct {
		px   string
		side Side
		qty  string
	}{{"2.000", Buy, "1.0"}, {"2.010", Buy, "2.0"}, {"2.011", Buy, "3.0"}, {"1.000", Sell, "1.0"}, {"0.990", Sell, "2.0"}, {"0.989", Sell, "3.0"}} {
		requireBookPortDecimal(t, book.QuantityAtLevel(portPrice(tc.px), tc.side), tc.qty)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:491
//	test: test_book_get_quantity_at_level_nonexistent_price
func TestBookBookGetQuantityAtLevelNonexistentPrice(t *testing.T) {
	book := executionBook()
	requireBookPortDecimal(t, book.QuantityAtLevel(portPrice("2.005"), Buy), "0")
	requireBookPortDecimal(t, book.QuantityAtLevel(portPrice("0.995"), Sell), "0")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:521
//	test: test_book_get_quantity_at_level_vs_cumulative
func TestBookBookGetQuantityAtLevelVsCumulative(t *testing.T) {
	book := executionBook()
	requireBookPortDecimal(t, book.QuantityAtLevel(portPrice("2.010"), Buy), "2.0")
	requireBookPortDecimal(t, book.QuantityForPrice(portPrice("2.010"), Buy), "3.0")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:560
//	test: test_book_get_quantity_at_level_after_update
func TestBookBookGetQuantityAtLevelAfterUpdate(t *testing.T) {
	book := NewBook("ETHUSDT-PERP.BINANCE", L2MBP)
	book.Add(NewOrder(Sell, portPrice("2.000"), portQuantity("100"), 1), 0, 1, 1)
	requireBookPortDecimal(t, book.QuantityAtLevel(portPrice("2.000"), Buy), "100")
	book.Update(NewOrder(Sell, portPrice("2.000"), portQuantity("150"), 1), 0, 2, 2)
	requireBookPortDecimal(t, book.QuantityAtLevel(portPrice("2.000"), Buy), "150")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:592
//	test: test_book_get_quantity_at_level_after_delete
func TestBookBookGetQuantityAtLevelAfterDelete(t *testing.T) {
	book := NewBook("ETHUSDT-PERP.BINANCE", L2MBP)
	order := NewOrder(Sell, portPrice("2.000"), portQuantity("100"), 1)
	book.Add(order, 0, 1, 1)
	book.Delete(order, 2, 2)
	requireBookPortDecimal(t, book.QuantityAtLevel(portPrice("2.000"), Buy), "0")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:624
//	test: test_book_get_orders_at_level_fifo_and_side_convention
func TestBookBookGetOrdersAtLevelFifoAndSideConvention(t *testing.T) {
	book := NewBook("ETHUSDT-PERP.BINANCE", L3MBO)
	a1 := NewOrder(Sell, portPrice("2.000"), portQuantity("100"), 1)
	a2 := NewOrder(Sell, portPrice("2.000"), portQuantity("200"), 2)
	bid := NewOrder(Buy, portPrice("1.000"), portQuantity("50"), 3)
	book.Add(a1, 0, 1, 1)
	book.Add(a2, 0, 2, 2)
	book.Add(bid, 0, 3, 3)
	asks := book.OrdersAtLevel(portPrice("2.000"), Buy)
	if len(asks) != 2 || asks[0] != a1 || asks[1] != a2 {
		t.Fatalf("asks = %#v", asks)
	}
	bids := book.OrdersAtLevel(portPrice("1.000"), Sell)
	if len(bids) != 1 || bids[0] != bid {
		t.Fatalf("bids = %#v", bids)
	}
	if got := book.OrdersAtLevel(portPrice("1.500"), Buy); len(got) != 0 {
		t.Fatalf("missing = %#v", got)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:664
//	test: test_book_get_price_for_exposure_no_market
func TestBookBookGetPriceForExposureNoMarket(t *testing.T) {
	book := NewBook("ETHUSDT-PERP.BINANCE", L2MBP)
	for _, side := range []Side{Buy, Sell} {
		a, q, p := book.PriceForExposure(decimal.MustParse("1"), side)
		if !a.IsZero() || !q.IsZero() || !p.IsZero() {
			t.Fatalf("empty exposure %s = %s,%s,%s", side, a, q, p)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:680
//	test: test_book_get_price_for_exposure
func TestBookBookGetPriceForExposure(t *testing.T) {
	book := NewBook("AAPL.XNAS", L2MBP)
	if err := book.ApplyDepth(stubDepth10()); err != nil {
		t.Fatal(err)
	}
	a, q, p := book.PriceForExposure(decimal.MustParse("1"), Buy)
	requireBookPortDecimal(t, a, "100")
	requireBookPortDecimal(t, q, "0.01")
	requireBookPortDecimal(t, p, "100")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:700
//	test: test_book_apply_depth
func TestBookBookApplyDepth(t *testing.T) {
	book := NewBook("AAPL.XNAS", L2MBP)
	if err := book.ApplyDepth(stubDepth10()); err != nil {
		t.Fatal(err)
	}
	requireBookPortPriceResult(t, book.BestBidPrice, "99.00")
	requireBookPortPriceResult(t, book.BestAskPrice, "100.00")
	requireBookPortQuantityResult(t, book.BestBidSize, "100.0")
	requireBookPortQuantityResult(t, book.BestAskSize, "100.0")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:714
//	test: test_book_apply_depth_all_levels
func TestBookBookApplyDepthAllLevels(t *testing.T) {
	book := NewBook("AAPL.XNAS", L2MBP)
	if err := book.ApplyDepth(stubDepth10()); err != nil {
		t.Fatal(err)
	}
	bids, asks := book.Bids.Levels(), book.Asks.Levels()
	if len(bids) != 10 || len(asks) != 10 {
		t.Fatalf("levels=%d/%d", len(bids), len(asks))
	}
	for i := range 10 {
		if bids[i].Size().Sign() <= 0 || asks[i].Size().Sign() <= 0 {
			t.Fatalf("zero size at %d", i)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:796
//	test: test_book_apply_depth_empty_snapshot
func TestBookBookApplyDepthEmptySnapshot(t *testing.T) {
	book := NewBook("AAPL.XNAS", L2MBP)
	depth := DepthSnapshot{InstrumentID: "AAPL.XNAS", Sequence: 12345, EventTime: 1000}
	if err := book.ApplyDepth(depth); err != nil {
		t.Fatal(err)
	}
	if book.HasBid() || book.HasAsk() || book.Bids.Len() != 0 || book.Asks.Len() != 0 {
		t.Fatal("phantom levels")
	}
	if book.Sequence != 12345 || book.LastTime != 1000 || book.UpdateCount != 1 {
		t.Fatalf("metadata=%d/%d/%d", book.Sequence, book.LastTime, book.UpdateCount)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:847
//	test: test_book_apply_depth_partial_snapshot
func TestBookBookApplyDepthPartialSnapshot(t *testing.T) {
	book := NewBook("AAPL.XNAS", L2MBP)
	d := stubDepth10()
	d.Bids = d.Bids[:3]
	d.Asks = d.Asks[:3]
	d.Sequence = 54321
	d.EventTime = 3000
	if err := book.ApplyDepth(d); err != nil {
		t.Fatal(err)
	}
	if book.Bids.Len() != 3 || book.Asks.Len() != 3 {
		t.Fatalf("levels=%d/%d", book.Bids.Len(), book.Asks.Len())
	}
	if book.Sequence != 54321 || book.LastTime != 3000 || book.UpdateCount != 1 {
		t.Fatal("metadata mismatch")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:951
//	test: test_book_apply_depth_updates_metadata_once
func TestBookBookApplyDepthUpdatesMetadataOnce(t *testing.T) {
	book := NewBook("AAPL.XNAS", L2MBP)
	d := stubDepth10()
	if err := book.ApplyDepth(d); err != nil {
		t.Fatal(err)
	}
	if book.Sequence != d.Sequence || book.LastTime != d.EventTime || book.UpdateCount != 1 {
		t.Fatalf("metadata=%d/%d/%d", book.Sequence, book.LastTime, book.UpdateCount)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:968
//	test: test_book_apply_depth_instrument_mismatch
func TestBookBookApplyDepthInstrumentMismatch(t *testing.T) {
	book := NewBook("ETHUSDT-PERP.BINANCE", L2MBP)
	err := book.ApplyDepth(stubDepth10())
	requireErrorIs(t, err, ErrInstrumentMismatch)
	if book.UpdateCount != 0 || book.HasBid() || book.HasAsk() {
		t.Fatal("mismatch mutated book")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:990
//	test: test_book_orderbook_creation
func TestBookBookOrderbookCreation(t *testing.T) {
	book := NewBook("AAPL.XNAS", L2MBP)
	if book.InstrumentID != "AAPL.XNAS" || book.BookType != L2MBP || book.Sequence != 0 || book.LastTime != 0 || book.UpdateCount != 0 {
		t.Fatalf("book=%#v", book)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:1002
//	test: test_book_orderbook_reset
func TestBookBookOrderbookReset(t *testing.T) {
	book := NewBook("AAPL.XNAS", L1MBP)
	book.Sequence = 10
	book.LastTime = 100
	book.UpdateCount = 3
	book.Reset()
	if book.BookType != L1MBP || book.Sequence != 0 || book.LastTime != 0 || book.UpdateCount != 0 {
		t.Fatalf("reset=%#v", book)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:1018
//	test: test_book_update_quote_tick_l1
func TestBookBookUpdateQuoteTickL1(t *testing.T) {
	book := NewBook("ETHUSDT-PERP.BINANCE", L1MBP)
	q := QuoteTick{InstrumentID: book.InstrumentID, BidPrice: portPrice("5000.000"), AskPrice: portPrice("5100.000"), BidSize: portQuantity("100.00000000"), AskSize: portQuantity("99.00000000")}
	if err := book.UpdateQuoteTick(q); err != nil {
		t.Fatal(err)
	}
	requireBookPortPriceResult(t, book.BestBidPrice, "5000")
	requireBookPortPriceResult(t, book.BestAskPrice, "5100")
	requireBookPortQuantityResult(t, book.BestBidSize, "100")
	requireBookPortQuantityResult(t, book.BestAskSize, "99")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:1040
//	test: test_book_update_trade_tick_l1
func TestBookBookUpdateTradeTickL1(t *testing.T) {
	book := NewBook("ETHUSDT-PERP.BINANCE", L1MBP)
	tr := TradeTick{InstrumentID: book.InstrumentID, Price: portPrice("15000.000"), Size: portQuantity("10.00000000")}
	if err := book.UpdateTradeTick(tr); err != nil {
		t.Fatal(err)
	}
	requireBookPortPriceResult(t, book.BestBidPrice, "15000")
	requireBookPortPriceResult(t, book.BestAskPrice, "15000")
	requireBookPortQuantityResult(t, book.BestBidSize, "10")
	requireBookPortQuantityResult(t, book.BestAskSize, "10")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:1065
//	test: test_book_update_quote_tick_advances_sequence
func TestBookBookUpdateQuoteTickAdvancesSequence(t *testing.T) {
	book := NewBook("ETHUSDT-PERP.BINANCE", L1MBP)
	for i := uint64(1); i <= 2; i++ {
		q := QuoteTick{InstrumentID: book.InstrumentID, BidPrice: portPrice("5000"), AskPrice: portPrice("5100"), BidSize: portQuantity("100"), AskSize: portQuantity("99"), EventTime: i * 1000}
		if err := book.UpdateQuoteTick(q); err != nil {
			t.Fatal(err)
		}
		if book.Sequence != i || book.UpdateCount != i || book.LastTime != i*1000 {
			t.Fatalf("metadata=%d/%d/%d", book.Sequence, book.UpdateCount, book.LastTime)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:1114
//	test: test_book_update_trade_tick_advances_sequence
func TestBookBookUpdateTradeTickAdvancesSequence(t *testing.T) {
	book := NewBook("ETHUSDT-PERP.BINANCE", L1MBP)
	for i := uint64(1); i <= 2; i++ {
		tr := TradeTick{InstrumentID: book.InstrumentID, Price: portPrice("15000"), Size: portQuantity("10"), EventTime: i*2000 + 3000}
		if err := book.UpdateTradeTick(tr); err != nil {
			t.Fatal(err)
		}
		if book.Sequence != i || book.UpdateCount != i || book.LastTime != tr.EventTime {
			t.Fatalf("metadata=%d/%d/%d", book.Sequence, book.UpdateCount, book.LastTime)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:1163
//	test: test_book_update_stale_trade_tick_does_not_mutate_l1
func TestBookBookUpdateStaleTradeTickDoesNotMutateL1(t *testing.T) {
	book := NewBook("TEST.SIM", L3MBO)
	ops := []Order{NewOrder(Buy, portPrice("100"), portQuantity("10"), 1), NewOrder(Sell, portPrice("101"), portQuantity("20"), 2)}
	for i, o := range ops {
		book.Add(o, 0, uint64(i+1), uint64(i+1))
	}
	if err := book.Integrity(); err != nil {
		t.Fatal(err)
	}
	if book.Bids.Len() != 1 || book.Asks.Len() != 1 || book.UpdateCount != 2 {
		t.Fatalf("invariants=%d/%d/%d", book.Bids.Len(), book.Asks.Len(), book.UpdateCount)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:1200
//	test: test_book_update_stale_quote_tick_does_not_mutate_l1
func TestBookBookUpdateStaleQuoteTickDoesNotMutateL1(t *testing.T) {
	book := NewBook("TEST.SIM", L3MBO)
	ops := []Order{NewOrder(Buy, portPrice("100"), portQuantity("10"), 1), NewOrder(Sell, portPrice("101"), portQuantity("20"), 2)}
	for i, o := range ops {
		book.Add(o, 0, uint64(i+1), uint64(i+1))
	}
	if err := book.Integrity(); err != nil {
		t.Fatal(err)
	}
	if book.Bids.Len() != 1 || book.Asks.Len() != 1 || book.UpdateCount != 2 {
		t.Fatalf("invariants=%d/%d/%d", book.Bids.Len(), book.Asks.Len(), book.UpdateCount)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:1237
//	test: test_book_pprint
func TestBookBookPprint(t *testing.T) {
	book := NewBook("ETHUSDT-PERP.BINANCE", L3MBO)
	for i, o := range []Order{NewOrder(Buy, portPrice("1.000"), portQuantity("1.0"), 1), NewOrder(Buy, portPrice("1.500"), portQuantity("2.0"), 2), NewOrder(Buy, portPrice("2.000"), portQuantity("3.0"), 3), NewOrder(Sell, portPrice("3.000"), portQuantity("3.0"), 4), NewOrder(Sell, portPrice("4.000"), portQuantity("4.0"), 5), NewOrder(Sell, portPrice("5.000"), portQuantity("8.0"), 6)} {
		book.Add(o, 0, uint64(i+1), uint64((i+1)*100))
	}
	want := "bid_levels: 3\nask_levels: 3\nsequence: 6\nupdate_count: 6\nts_last: 600\n╭───────┬───────┬───────╮\n│ bids  │ price │ asks  │\n├───────┼───────┼───────┤\n│       │ 5.000 │ [8.0] │\n│       │ 4.000 │ [4.0] │\n│       │ 3.000 │ [3.0] │\n│ [3.0] │ 2.000 │       │\n│ [2.0] │ 1.500 │       │\n│ [1.0] │ 1.000 │       │\n╰───────┴───────┴───────╯"
	if got := book.PPrint(3); got != want {
		t.Fatalf("pprint=\n%s", got)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:1308
//	test: test_book_group_empty_book
func TestBookBookGroupEmptyBook(t *testing.T) {
	book := NewBook("ETHUSDT-PERP.BINANCE", L2MBP)
	if len(book.Group(Buy, decimal.MustParse("1"), 0)) != 0 || len(book.Group(Sell, decimal.MustParse("1"), 0)) != 0 {
		t.Fatal("empty grouping nonempty")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:1320
//	test: test_book_group_price_levels
func TestBookBookGroupPriceLevels(t *testing.T) {
	book := NewBook("ETHUSDT-PERP.BINANCE", L2MBP)
	for i, o := range []Order{NewOrder(Buy, portPrice("1.1"), portQuantity("1"), 1), NewOrder(Buy, portPrice("1.2"), portQuantity("2"), 2), NewOrder(Buy, portPrice("1.8"), portQuantity("3"), 3), NewOrder(Sell, portPrice("2.1"), portQuantity("1"), 4), NewOrder(Sell, portPrice("2.2"), portQuantity("2"), 5), NewOrder(Sell, portPrice("2.8"), portQuantity("3"), 6)} {
		book.Add(o, 0, uint64(i), 100)
	}
	b := book.Group(Buy, decimal.MustParse("0.5"), 10)
	a := book.Group(Sell, decimal.MustParse("0.5"), 10)
	requireBookPortDecimal(t, b["1"], "3")
	requireBookPortDecimal(t, b["1.5"], "3")
	requireBookPortDecimal(t, a["2.5"], "3")
	requireBookPortDecimal(t, a["3"], "3")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:1348
//	test: test_book_group_with_depth_limit
func TestBookBookGroupWithDepthLimit(t *testing.T) {
	book := NewBook("ETHUSDT-PERP.BINANCE", L2MBP)
	addLevels(book, []string{"1", "2", "3"}, []string{"4", "5", "6"})
	b := book.Group(Buy, decimal.MustParse("1"), 2)
	a := book.Group(Sell, decimal.MustParse("1"), 2)
	if len(b) != 2 || len(a) != 2 {
		t.Fatalf("groups=%v/%v", b, a)
	}
	requireBookPortDecimal(t, b["3"], "10")
	requireBookPortDecimal(t, b["2"], "10")
	requireBookPortDecimal(t, a["4"], "10")
	requireBookPortDecimal(t, a["5"], "10")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:1377
//	test: test_book_group_price_realistic
func TestBookBookGroupPriceRealistic(t *testing.T) {
	book := NewBook("ETHUSDT-PERP.BINANCE", L2MBP)
	for i, p := range []string{"100", "99", "98"} {
		book.Add(NewOrder(Buy, portPrice(p), portQuantity([]string{"1000", "2000", "3000"}[i]), uint64(i+1)), 0, 0, 0)
	}
	for i, p := range []string{"101", "102", "103"} {
		book.Add(NewOrder(Sell, portPrice(p), portQuantity([]string{"1000", "2000", "3000"}[i]), uint64(i+4)), 0, 0, 0)
	}
	b := book.Group(Buy, decimal.MustParse("2"), 10)
	a := book.Group(Sell, decimal.MustParse("2"), 10)
	requireBookPortDecimal(t, b["100"], "1000")
	requireBookPortDecimal(t, b["98"], "5000")
	requireBookPortDecimal(t, a["102"], "3000")
	requireBookPortDecimal(t, a["104"], "3000")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:1435
//	test: test_book_filtered_book_empty_own_book
func TestBookBookFilteredBookEmptyOwnBook(t *testing.T) {
	book := NewBook("AAPL.XNAS", L2MBP)
	addLevels(book, []string{"100", "99", "98"}, []string{"101", "102", "103"})
	own := NewOwnBook("AAPL.XNAS")
	if got := book.FilteredMap(Buy, 0, own, nil, 0, 0); len(got) != 3 {
		t.Fatalf("bids=%v", got)
	}
	if got := book.FilteredMap(Sell, 0, own, nil, 0, 0); len(got) != 3 {
		t.Fatalf("asks=%v", got)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:1467
//	test: test_book_filtered_book_with_own_orders
func TestBookBookFilteredBookWithOwnOrders(t *testing.T) {
	book := NewBook("AAPL.XNAS", L2MBP)
	book.Add(NewOrder(Buy, portPrice("100"), portQuantity("100"), 1), 0, 1, 1)
	book.Add(NewOrder(Sell, portPrice("101"), portQuantity("100"), 2), 0, 2, 2)
	own := NewOwnBook("AAPL.XNAS")
	own.Add(ownOrder("B-A", Buy, "100", "30", StatusAccepted))
	own.Add(ownOrder("B-S", Buy, "100", "40", StatusSubmitted))
	own.Add(ownOrder("A-A", Sell, "101", "30", StatusAccepted))
	own.Add(ownOrder("A-S", Sell, "101", "40", StatusSubmitted))
	accepted := map[OrderStatus]bool{StatusAccepted: true}
	requireBookPortDecimal(t, book.FilteredMap(Buy, 0, own, accepted, 0, 0)["100"], "70")
	requireBookPortDecimal(t, book.FilteredMap(Sell, 0, own, accepted, 0, 0)["101"], "70")
	requireBookPortDecimal(t, book.FilteredMap(Buy, 0, own, nil, 0, 0)["100"], "30")
	requireBookPortDecimal(t, book.FilteredMap(Sell, 0, own, nil, 0, 0)["101"], "30")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:1546
//	test: test_book_filtered_with_own_orders_exact_size
func TestBookBookFilteredWithOwnOrdersExactSize(t *testing.T) {
	book := NewBook("AAPL.XNAS", L2MBP)
	book.Add(NewOrder(Buy, portPrice("100"), portQuantity("100"), 1), 0, 1, 1)
	book.Add(NewOrder(Sell, portPrice("101"), portQuantity("100"), 2), 0, 2, 2)
	own := NewOwnBook("AAPL.XNAS")
	own.Add(ownOrder("B", Buy, "100", "100", StatusAccepted))
	own.Add(ownOrder("A", Sell, "101", "100", StatusAccepted))
	if _, ok := book.FilteredMap(Buy, 0, own, nil, 0, 0)["100"]; ok {
		t.Fatal("bid level retained")
	}
	if _, ok := book.FilteredMap(Sell, 0, own, nil, 0, 0)["101"]; ok {
		t.Fatal("ask level retained")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:1614
//	test: test_book_filtered_with_own_orders_larger_size
func TestBookBookFilteredWithOwnOrdersLargerSize(t *testing.T) {
	book := NewBook("AAPL.XNAS", L2MBP)
	book.Add(NewOrder(Buy, portPrice("100"), portQuantity("100"), 1), 0, 1, 1)
	book.Add(NewOrder(Sell, portPrice("101"), portQuantity("100"), 2), 0, 2, 2)
	own := NewOwnBook("AAPL.XNAS")
	own.Add(ownOrder("B", Buy, "100", "150", StatusAccepted))
	own.Add(ownOrder("A", Sell, "101", "150", StatusAccepted))
	if _, ok := book.FilteredMap(Buy, 0, own, nil, 0, 0)["100"]; ok {
		t.Fatal("bid level retained")
	}
	if _, ok := book.FilteredMap(Sell, 0, own, nil, 0, 0)["101"]; ok {
		t.Fatal("ask level retained")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:1682
//	test: test_book_get_worst_price_for_quantity
func TestBookBookGetWorstPriceForQuantity(t *testing.T) {
	book := executionBook()
	got, ok := book.WorstPriceForQuantity(portQuantity("1.5"), Buy)
	requireBookPortPrice(t, got, ok, "2.010")
	got, ok = book.WorstPriceForQuantity(portQuantity("1.5"), Sell)
	requireBookPortPrice(t, got, ok, "0.990")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:1728
//	test: test_book_get_worst_price_for_quantity_no_market
func TestBookBookGetWorstPriceForQuantityNoMarket(t *testing.T) {
	book := NewBook("ETHUSDT-PERP.BINANCE", L2MBP)
	if _, ok := book.WorstPriceForQuantity(portQuantity("1"), Buy); ok {
		t.Fatal("buy worst present")
	}
	if _, ok := book.WorstPriceForQuantity(portQuantity("1"), Sell); ok {
		t.Fatal("sell worst present")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:1739
//	test: test_book_filtered_with_own_orders_different_level
func TestBookBookFilteredWithOwnOrdersDifferentLevel(t *testing.T) {
	book := NewBook("AAPL.XNAS", L2MBP)
	book.Add(NewOrder(Buy, portPrice("100"), portQuantity("100"), 1), 0, 1, 1)
	book.Add(NewOrder(Sell, portPrice("101"), portQuantity("100"), 2), 0, 2, 2)
	own := NewOwnBook("AAPL.XNAS")
	own.Add(ownOrder("B", Buy, "99", "50", StatusAccepted))
	own.Add(ownOrder("A", Sell, "102", "50", StatusAccepted))
	requireBookPortDecimal(t, book.FilteredMap(Buy, 0, own, nil, 0, 0)["100"], "100")
	requireBookPortDecimal(t, book.FilteredMap(Sell, 0, own, nil, 0, 0)["101"], "100")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:1807
//	test: test_book_filtered_with_synthetic_orders
func TestBookBookFilteredWithSyntheticOrders(t *testing.T) {
	book := NewBook("YES.XNAS", L2MBP)
	book.Add(NewOrder(Buy, portPrice("0.40"), portQuantity("100"), 1), 0, 1, 1)
	book.Add(NewOrder(Sell, portPrice("0.60"), portQuantity("100"), 2), 0, 2, 2)
	own := NewOwnBook("YES.XNAS")
	opp := NewOwnBook("NO.XNAS")
	opp.Add(ownOrder("S-A", Sell, "0.60", "30", StatusAccepted))
	opp.Add(ownOrder("S-B", Buy, "0.40", "20", StatusAccepted))
	combined, err := own.CombinedWithOpposite(opp)
	if err != nil {
		t.Fatal(err)
	}
	b := book.FilteredMap(Buy, 10, combined, nil, 0, 0)
	a := book.FilteredMap(Sell, 10, combined, nil, 0, 0)
	requireBookPortDecimal(t, b["0.4"], "70")
	requireBookPortDecimal(t, a["0.6"], "80")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:1863
//	test: test_book_filtered_with_own_and_synthetic_orders
func TestBookBookFilteredWithOwnAndSyntheticOrders(t *testing.T) {
	book := NewBook("YES.XNAS", L2MBP)
	book.Add(NewOrder(Buy, portPrice("0.40"), portQuantity("100"), 1), 0, 1, 1)
	book.Add(NewOrder(Sell, portPrice("0.60"), portQuantity("100"), 2), 0, 2, 2)
	own := NewOwnBook("YES.XNAS")
	own.Add(ownOrder("OWN-B", Buy, "0.40", "10", StatusAccepted))
	own.Add(ownOrder("OWN-A", Sell, "0.60", "5", StatusAccepted))
	opp := NewOwnBook("NO.XNAS")
	opp.Add(ownOrder("S-A", Sell, "0.60", "30", StatusAccepted))
	opp.Add(ownOrder("S-B", Buy, "0.40", "20", StatusAccepted))
	combined, err := own.CombinedWithOpposite(opp)
	if err != nil {
		t.Fatal(err)
	}
	b := book.FilteredMap(Buy, 10, combined, nil, 0, 0)
	a := book.FilteredMap(Sell, 10, combined, nil, 0, 0)
	requireBookPortDecimal(t, b["0.4"], "60")
	requireBookPortDecimal(t, a["0.6"], "75")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:1957
//	test: test_order_book_filtered_view_with_combined_own_orders
func TestBookOrderBookFilteredViewWithCombinedOwnOrders(t *testing.T) {
	book := NewBook("YES.XNAS", L2MBP)
	book.Add(NewOrder(Buy, portPrice("0.40"), portQuantity("100"), 1), 0, 1, 1)
	book.Add(NewOrder(Sell, portPrice("0.60"), portQuantity("100"), 2), 0, 2, 2)
	own := NewOwnBook("YES.XNAS")
	own.Add(ownOrder("OWN-B", Buy, "0.40", "10", StatusAccepted))
	own.Add(ownOrder("OWN-A", Sell, "0.60", "5", StatusAccepted))
	opp := NewOwnBook("NO.XNAS")
	opp.Add(ownOrder("S-A", Sell, "0.60", "30", StatusAccepted))
	opp.Add(ownOrder("S-B", Buy, "0.40", "20", StatusAccepted))
	combined, err := own.CombinedWithOpposite(opp)
	if err != nil {
		t.Fatal(err)
	}
	view, err := book.FilteredView(combined, 10, nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	requireBookPortDecimal(t, view.BidsMap(10)["0.4"], "60")
	requireBookPortDecimal(t, view.AsksMap(10)["0.6"], "75")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:2049
//	test: test_order_book_filtered_view_book_and_own_book_instrument_mismatch
func TestBookOrderBookFilteredViewBookAndOwnBookInstrumentMismatch(t *testing.T) {
	book := NewBook("YES.XNAS", L2MBP)
	_, err := book.FilteredView(NewOwnBook("NO.XNAS"), 10, nil, 0, 0)
	requireErrorIs(t, err, ErrInstrumentMismatch)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:2071
//	test: test_own_order_book_combined_with_opposite_instrument_must_differ
func TestBookOwnOrderBookCombinedWithOppositeInstrumentMustDiffer(t *testing.T) {
	yes := NewOwnBook("YES.XNAS")
	no := NewOwnBook("NO.XNAS")
	yes.Add(ownOrder("YES-B", Buy, "0.4", "10", StatusAccepted))
	no.Add(ownOrder("NO-A", Sell, "0.6", "30", StatusAccepted))
	combined, err := yes.CombinedWithOpposite(no)
	if err != nil {
		t.Fatal(err)
	}
	requireBookPortDecimal(t, combined.Quantities(Buy, nil, 0, 0)["0.4"], "40")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:2092
//	test: test_order_book_filtered_view_optional_books
func TestBookOrderBookFilteredViewOptionalBooks(t *testing.T) {
	book := NewBook("YES.XNAS", L2MBP)
	book.Add(NewOrder(Buy, portPrice("0.40"), portQuantity("100"), 1), 0, 1, 1)
	book.Add(NewOrder(Sell, portPrice("0.60"), portQuantity("200"), 2), 0, 2, 2)
	view, err := book.FilteredView(nil, 0, nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	requireBookPortQuantityResult(t, view.BestBidSize, "100")
	requireBookPortQuantityResult(t, view.BestAskSize, "200")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:2111
//	test: test_order_book_filtered_view_preserves_metadata_when_empty
func TestBookOrderBookFilteredViewPreservesMetadataWhenEmpty(t *testing.T) {
	book := NewBook("YES.XNAS", L2MBP)
	book.Add(NewOrder(Buy, portPrice("0.40"), portQuantity("100"), 1), 0, 42, 999)
	own := NewOwnBook("YES.XNAS")
	own.Add(ownOrder("OWN-1", Buy, "0.40", "100", StatusAccepted))
	view, err := book.FilteredView(own, 0, nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if view.HasBid() || view.Sequence != 42 || view.LastTime != 999 {
		t.Fatalf("view=%#v", view)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:2144
//	test: test_book_filtered_with_status_filter
func TestBookBookFilteredWithStatusFilter(t *testing.T) {
	book := NewBook("AAPL.XNAS", L2MBP)
	book.Add(NewOrder(Buy, portPrice("100"), portQuantity("100"), 1), 0, 1, 1)
	book.Add(NewOrder(Sell, portPrice("101"), portQuantity("100"), 2), 0, 2, 2)
	own := NewOwnBook("AAPL.XNAS")
	own.Add(ownOrder("B", Buy, "100", "50", StatusAccepted))
	own.Add(ownOrder("A", Sell, "101", "50", StatusAccepted))
	b := book.FilteredMap(Buy, 0, own, nil, 0, 0)
	a := book.FilteredMap(Sell, 0, own, nil, 0, 0)
	requireBookPortDecimal(t, b["100"], "50")
	requireBookPortDecimal(t, a["101"], "50")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:2258
//	test: test_book_filtered_with_depth_limit
func TestBookBookFilteredWithDepthLimit(t *testing.T) {
	book := NewBook("AAPL.XNAS", L2MBP)
	for i, p := range []string{"100", "99", "98"} {
		book.Add(NewOrder(Buy, portPrice(p), portQuantity([]string{"100", "200", "300"}[i]), uint64(i+1)), 0, 0, 0)
	}
	for i, p := range []string{"101", "102", "103"} {
		book.Add(NewOrder(Sell, portPrice(p), portQuantity([]string{"100", "200", "300"}[i]), uint64(i+4)), 0, 0, 0)
	}
	own := NewOwnBook("AAPL.XNAS")
	own.Add(ownOrder("B", Buy, "100", "50", StatusAccepted))
	own.Add(ownOrder("A", Sell, "101", "50", StatusAccepted))
	b := book.FilteredMap(Buy, 2, own, nil, 0, 0)
	a := book.FilteredMap(Sell, 2, own, nil, 0, 0)
	if len(b) != 2 || len(a) != 2 {
		t.Fatalf("maps=%v/%v", b, a)
	}
	requireBookPortDecimal(t, b["100"], "50")
	requireBookPortDecimal(t, a["101"], "50")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:2364
//	test: test_book_filtered_with_accepted_buffer
func TestBookBookFilteredWithAcceptedBuffer(t *testing.T) {
	book := NewBook("AAPL.XNAS", L2MBP)
	book.Add(NewOrder(Buy, portPrice("100"), portQuantity("100"), 1), 0, 1, 1)
	own := NewOwnBook("AAPL.XNAS")
	accepted := ownOrder("ACCEPTED", Buy, "100", "20", StatusAccepted)
	accepted.AcceptedTime = 500
	submitted := ownOrder("SUBMITTED", Buy, "100", "30", StatusSubmitted)
	own.Add(accepted)
	own.Add(submitted)
	requireBookPortDecimal(t, book.FilteredMap(Buy, 0, own, nil, 300, 1000)["100"], "50")
	requireBookPortDecimal(t, book.FilteredMap(Buy, 0, own,
		map[OrderStatus]bool{StatusSubmitted: true}, 300, 1000)["100"], "70")
	requireBookPortDecimal(t, book.FilteredMap(Buy, 0, own,
		map[OrderStatus]bool{StatusSubmitted: true, StatusAccepted: true}, 300, 1000)["100"], "50")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:2541
//	test: test_book_filtered_with_accepted_buffer_mixed_statuses
func TestBookBookFilteredWithAcceptedBufferMixedStatuses(t *testing.T) {
	book := NewBook("AAPL.XNAS", L2MBP)
	book.Add(NewOrder(Buy, portPrice("100"), portQuantity("100"), 1), 0, 1, 1)
	own := NewOwnBook("AAPL.XNAS")
	recent := ownOrder("RECENT", Buy, "100", "30", StatusAccepted)
	recent.AcceptedTime = 900
	older := ownOrder("OLDER", Buy, "100", "40", StatusAccepted)
	older.AcceptedTime = 500
	own.Add(recent)
	own.Add(older)
	requireBookPortDecimal(t, book.FilteredMap(Buy, 0, own, nil, 200, 1000)["100"], "60")
	requireBookPortDecimal(t, book.FilteredMap(Buy, 0, own, nil, 50, 1000)["100"], "30")
	requireBookPortDecimal(t, book.FilteredMap(Buy, 0, own, nil, 600, 1000)["100"], "100")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:2665
//	test: test_book_group_bids_filtered_empty_own_book
func TestBookBookGroupBidsFilteredEmptyOwnBook(t *testing.T) {
	book := NewBook("AAPL.XNAS", L2MBP)
	for i, o := range []Order{
		NewOrder(Buy, portPrice("100"), portQuantity("100"), 1),
		NewOrder(Buy, portPrice("99.5"), portQuantity("200"), 2),
		NewOrder(Buy, portPrice("99"), portQuantity("300"), 3),
	} {
		book.Add(o, 0, uint64(i), 0)
	}
	own := NewOwnBook("AAPL.XNAS")
	got := book.GroupFiltered(Buy, decimal.MustParse("1"), 0, own, nil, 0, 0)
	if len(got) != 2 {
		t.Fatalf("bids=%v", got)
	}
	requireBookPortDecimal(t, got["100"], "100")
	requireBookPortDecimal(t, got["99"], "500")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:2693
//	test: test_book_group_asks_filtered_empty_own_book
func TestBookBookGroupAsksFilteredEmptyOwnBook(t *testing.T) {
	book := NewBook("AAPL.XNAS", L2MBP)
	for i, o := range []Order{
		NewOrder(Sell, portPrice("101"), portQuantity("100"), 1),
		NewOrder(Sell, portPrice("101.5"), portQuantity("200"), 2),
		NewOrder(Sell, portPrice("102"), portQuantity("300"), 3),
	} {
		book.Add(o, 0, uint64(i), 0)
	}
	own := NewOwnBook("AAPL.XNAS")
	got := book.GroupFiltered(Sell, decimal.MustParse("1"), 0, own, nil, 0, 0)
	if len(got) != 2 {
		t.Fatalf("asks=%v", got)
	}
	requireBookPortDecimal(t, got["101"], "100")
	requireBookPortDecimal(t, got["102"], "500")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:2731
//	test: test_book_group_bids_filtered_with_own_book
func TestBookBookGroupBidsFilteredWithOwnBook(t *testing.T) {
	book := NewBook("AAPL.XNAS", L2MBP)
	book.Add(NewOrder(Buy, portPrice("100"), portQuantity("100"), 1), 0, 1, 1)
	book.Add(NewOrder(Buy, portPrice("99"), portQuantity("200"), 2), 0, 2, 2)
	own := NewOwnBook("AAPL.XNAS")
	own.Add(ownOrder("B1", Buy, "100", "40", StatusAccepted))
	own.Add(ownOrder("B2", Buy, "99", "50", StatusAccepted))
	got := book.GroupFiltered(Buy, decimal.MustParse("1"), 0, own, nil, 0, 0)
	requireBookPortDecimal(t, got["100"], "60")
	requireBookPortDecimal(t, got["99"], "150")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:2794
//	test: test_book_group_asks_filtered_with_own_book
func TestBookBookGroupAsksFilteredWithOwnBook(t *testing.T) {
	book := NewBook("AAPL.XNAS", L2MBP)
	book.Add(NewOrder(Sell, portPrice("101"), portQuantity("100"), 1), 0, 1, 1)
	book.Add(NewOrder(Sell, portPrice("102"), portQuantity("200"), 2), 0, 2, 2)
	own := NewOwnBook("AAPL.XNAS")
	own.Add(ownOrder("A1", Sell, "101", "40", StatusAccepted))
	own.Add(ownOrder("A2", Sell, "102", "50", StatusAccepted))
	got := book.GroupFiltered(Sell, decimal.MustParse("1"), 0, own, nil, 0, 0)
	requireBookPortDecimal(t, got["101"], "60")
	requireBookPortDecimal(t, got["102"], "150")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:2862
//	test: test_book_group_with_status_filter
func TestBookBookGroupWithStatusFilter(t *testing.T) {
	book := NewBook("AAPL.XNAS", L2MBP)
	book.Add(NewOrder(Buy, portPrice("100"), portQuantity("100"), 1), 0, 1, 1)
	own := NewOwnBook("AAPL.XNAS")
	own.Add(ownOrder("A", Buy, "100", "40", StatusAccepted))
	own.Add(ownOrder("S", Buy, "100", "30", StatusSubmitted))
	got := book.GroupFiltered(Buy, decimal.MustParse("1"), 0, own,
		map[OrderStatus]bool{StatusAccepted: true}, 0, 0)
	if len(got) != 1 {
		t.Fatalf("groups=%v", got)
	}
	requireBookPortDecimal(t, got["100"], "60")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:2937
//	test: test_book_clear_stale_levels_not_crossed
func TestBookBookClearStaleLevelsNotCrossed(t *testing.T) {
	book := NewBook("ETHUSDT-PERP.BINANCE", L2MBP)
	addLevels(book, []string{"99", "98"}, []string{"101", "102"})
	before := book.UpdateCount
	if removed := book.ClearStaleLevels(nil); removed != nil {
		t.Fatalf("removed=%v", removed)
	}
	if book.UpdateCount != before || book.Bids.Len() != 2 || book.Asks.Len() != 2 {
		t.Fatal("noncrossed book changed")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:2986
//	test: test_book_clear_stale_levels_simple_crossed
func TestBookBookClearStaleLevelsSimpleCrossed(t *testing.T) {
	book := NewBook("ETHUSDT-PERP.BINANCE", L2MBP)
	addLevels(book, []string{"105", "100"}, []string{"95", "110"})
	before := book.UpdateCount
	removed := book.ClearStaleLevels(nil)
	if len(removed) != 3 || book.UpdateCount != before+1 || book.Bids.Len() != 0 || book.Asks.Len() != 1 {
		t.Fatalf("removed=%d levels=%d/%d count=%d", len(removed), book.Bids.Len(), book.Asks.Len(), book.UpdateCount)
	}
	if again := book.ClearStaleLevels(nil); again != nil {
		t.Fatal("not idempotent")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:3043
//	test: test_book_clear_stale_levels_multiple_overlapping
func TestBookBookClearStaleLevelsMultipleOverlapping(t *testing.T) {
	book := NewBook("ETHUSDT-PERP.BINANCE", L3MBO)
	addLevels(book, []string{"110", "108", "105", "90"}, []string{"95", "100", "103", "115"})
	removed := book.ClearStaleLevels(nil)
	if len(removed) != 6 {
		t.Fatalf("removed=%d", len(removed))
	}
	requireBookPortPriceResult(t, book.BestBidPrice, "90")
	requireBookPortPriceResult(t, book.BestAskPrice, "115")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:3124
//	test: test_book_clear_stale_levels_with_multiple_orders_per_level
func TestBookBookClearStaleLevelsWithMultipleOrdersPerLevel(t *testing.T) {
	book := NewBook("ETHUSDT-PERP.BINANCE", L2MBP)
	addLevels(book, []string{"105", "90"}, []string{"95", "110"})
	removed := book.ClearStaleLevels(nil)
	if len(removed) != 2 || book.Bids.Len() != 1 || book.Asks.Len() != 1 {
		t.Fatalf("removed=%d levels=%d/%d", len(removed), book.Bids.Len(), book.Asks.Len())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:3175
//	test: test_book_clear_stale_levels_side_sell_clears_asks_only
func TestBookBookClearStaleLevelsSideSellClearsAsksOnly(t *testing.T) {
	book := NewBook("ETHUSDT-PERP.BINANCE", L2MBP)
	addLevels(book, []string{"105", "100"}, []string{"95", "110"})
	before := book.UpdateCount
	side := Sell
	removed := book.ClearStaleLevels(&side)
	if len(removed) != 1 || book.Bids.Len() != 2 || book.Asks.Len() != 1 || book.UpdateCount != before+1 {
		t.Fatalf("removed=%d levels=%d/%d count=%d", len(removed), book.Bids.Len(), book.Asks.Len(), book.UpdateCount)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:3240
//	test: test_book_clear_stale_levels_side_buy_clears_bids_only
func TestBookBookClearStaleLevelsSideBuyClearsBidsOnly(t *testing.T) {
	book := NewBook("ETHUSDT-PERP.BINANCE", L2MBP)
	addLevels(book, []string{"110", "90"}, []string{"100", "115"})
	side := Buy
	removed := book.ClearStaleLevels(&side)
	if len(removed) != 1 || book.Asks.Len() != 2 {
		t.Fatalf("removed=%d asks=%d", len(removed), book.Asks.Len())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:3305
//	test: test_book_clear_stale_levels_multiple_crossed_each_side
func TestBookBookClearStaleLevelsMultipleCrossedEachSide(t *testing.T) {
	book := NewBook("ETHUSDT-PERP.BINANCE", L2MBP)
	addLevels(book, []string{"110", "105", "102", "99", "95", "90"}, []string{"100", "103", "106", "109", "112", "115"})
	removed := book.ClearStaleLevels(nil)
	if len(removed) != 7 {
		t.Fatalf("removed=%d", len(removed))
	}
	for i, want := range []string{"110", "105", "102", "100", "103", "106", "109"} {
		if !removed[i].Price.Value.Equal(portPrice(want)) {
			t.Fatalf("removed[%d]=%s", i, removed[i].Price.Value)
		}
	}
	if again := book.ClearStaleLevels(nil); again != nil {
		t.Fatal("cleanup not idempotent")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:3489
//	test: test_book_clear_stale_levels_multiple_crossed_side_specific
func TestBookBookClearStaleLevelsMultipleCrossedSideSpecific(t *testing.T) {
	book := NewBook("ETHUSDT-PERP.BINANCE", L2MBP)
	addLevels(book, []string{"110", "105", "102", "99", "95", "90"}, []string{"100", "103", "106", "109", "112", "115"})
	side := Buy
	removed := book.ClearStaleLevels(&side)
	if len(removed) != 3 || book.Asks.Len() != 6 {
		t.Fatalf("removed=%d asks=%d", len(removed), book.Asks.Len())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:3649
//	test: test_book_clear_stale_levels_l1_mbp
func TestBookBookClearStaleLevelsL1Mbp(t *testing.T) {
	book := NewBook("ETHUSDT-PERP.BINANCE", L1MBP)
	before := book.UpdateCount
	if removed := book.ClearStaleLevels(nil); removed != nil || book.UpdateCount != before {
		t.Fatalf("removed=%v count=%d", removed, book.UpdateCount)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:3701
//	test: test_own_order_to_book_price
func TestBookOwnOrderToBookPrice(t *testing.T) {
	o := ownOrder("O-123", Buy, "100.00", "10", StatusSubmitted)
	bp := o.ToBookPrice()
	if bp.Side != Buy || !bp.Value.Equal(portPrice("100")) {
		t.Fatalf("price=%#v", bp)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:3708
//	test: test_own_order_exposure
func TestBookOwnOrderExposure(t *testing.T) {
	o := ownOrder("O-123", Buy, "100.00", "10", StatusSubmitted)
	requireBookPortDecimal(t, o.Exposure(), "1000")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:3714
//	test: test_own_order_signed_size
func TestBookOwnOrderSignedSize(t *testing.T) {
	buy := ownOrder("B", Buy, "100", "10", StatusAccepted)
	sell := ownOrder("S", Sell, "101", "10", StatusAccepted)
	requireBookPortDecimal(t, buy.SignedSize(), "10")
	requireBookPortDecimal(t, sell.SignedSize(), "-10")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:3737
//	test: test_own_order_debug
func TestBookOwnOrderDebug(t *testing.T) {
	o := ownOrder("O-123456789", Buy, "100.00", "10", StatusSubmitted)
	o.LastTime = 2
	o.SubmittedTime = 2
	o.InitTime = 1
	want := "OwnBookOrder(trader_id=TRADER-001, client_order_id=O-123456789, venue_order_id=None, side=BUY, price=100.00, size=10, order_type=LIMIT, time_in_force=GTC, status=SUBMITTED, ts_last=2, ts_accepted=0, ts_submitted=2, ts_init=1)"
	if got := o.Debug(); got != want {
		t.Fatalf("debug=%q", got)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:3745
//	test: test_own_order_display
func TestBookOwnOrderDisplay(t *testing.T) {
	o := ownOrder("O-123456789", Buy, "100.00", "10", StatusSubmitted)
	o.LastTime = 2
	o.SubmittedTime = 2
	o.InitTime = 1
	want := "TRADER-001,O-123456789,None,BUY,100.00,10,LIMIT,GTC,SUBMITTED,2,0,2,1"
	if got := o.String(); got != want {
		t.Fatalf("display=%q", got)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:3753
//	test: test_client_order_ids_empty_book
func TestBookClientOrderIdsEmptyBook(t *testing.T) {
	book := NewOwnBook("AAPL.XNAS")
	if len(book.BidClientIDs()) != 0 || len(book.AskClientIDs()) != 0 || book.Contains("O-NONEXISTENT") || len(book.Quantities(Buy, nil, 0, 0)) != 0 || len(book.Quantities(Sell, nil, 0, 0)) != 0 {
		t.Fatal("empty own book inconsistent")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:3768
//	test: test_client_order_ids_with_orders
func TestBookClientOrderIdsWithOrders(t *testing.T) {
	book := NewOwnBook("AAPL.XNAS")
	for _, order := range []OwnOrder{
		ownOrder("O-BID-1", Buy, "100", "10", StatusAccepted),
		ownOrder("O-BID-2", Buy, "99", "20", StatusAccepted),
		ownOrder("O-ASK-1", Sell, "101", "10", StatusAccepted),
		ownOrder("O-ASK-2", Sell, "102", "20", StatusAccepted),
	} {
		book.Add(order)
	}
	if got := book.BidClientIDs(); !slices.Equal(got, []string{"O-BID-1", "O-BID-2"}) {
		t.Fatalf("bid ids=%v", got)
	}
	if got := book.AskClientIDs(); !slices.Equal(got, []string{"O-ASK-1", "O-ASK-2"}) {
		t.Fatalf("ask ids=%v", got)
	}
	for _, id := range []string{"O-BID-1", "O-BID-2", "O-ASK-1", "O-ASK-2"} {
		if !book.Contains(id) {
			t.Fatalf("missing %s", id)
		}
	}
	if book.Contains("O-NON-EXISTENT") {
		t.Fatal("nonexistent order reported present")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:3868
//	test: test_client_order_ids_after_operations
func TestBookClientOrderIdsAfterOperations(t *testing.T) {
	book := NewOwnBook("AAPL.XNAS")
	order := ownOrder("O-BID-1", Buy, "100", "10", StatusAccepted)
	book.Add(order)
	if !book.Contains(order.ClientID) || len(book.BidClientIDs()) != 1 {
		t.Fatal("add did not populate index")
	}
	if err := book.Delete(order.ClientID); err != nil {
		t.Fatal(err)
	}
	if book.Contains(order.ClientID) || len(book.BidClientIDs()) != 0 {
		t.Fatal("delete did not clear index")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:3901
//	test: test_own_book_update_missing_order_errors
func TestBookOwnBookUpdateMissingOrderErrors(t *testing.T) {
	book := NewOwnBook("AAPL.XNAS")
	err := book.Update(ownOrder("O-MISSING", Buy, "100", "1", StatusSubmitted))
	requireErrorIs(t, err, ErrOrderNotFound)
	if !strings.Contains(err.Error(), "O-MISSING") {
		t.Fatalf("error=%v", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:3936
//	test: test_own_book_delete_missing_order_errors
func TestBookOwnBookDeleteMissingOrderErrors(t *testing.T) {
	book := NewOwnBook("AAPL.XNAS")
	err := book.Delete("O-MISSING")
	requireErrorIs(t, err, ErrOrderNotFound)
	if !strings.Contains(err.Error(), "O-MISSING") {
		t.Fatalf("error=%v", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:3971
//	test: test_own_book_display
func TestBookOwnBookDisplay(t *testing.T) {
	book := NewOwnBook("ETHUSDT-PERP.BINANCE")
	if got := book.String(); got != "OwnOrderBook(instrument_id=ETHUSDT-PERP.BINANCE, orders=0, update_count=0)" {
		t.Fatalf("display=%q", got)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:3981
//	test: test_own_book_pprint
func TestBookOwnBookPprint(t *testing.T) {
	book := NewOwnBook("ETHUSDT-PERP.BINANCE")
	for _, o := range []OwnOrder{
		ownOrder("O-1", Buy, "1.000", "1.0", StatusAccepted),
		ownOrder("O-2", Buy, "1.500", "2.0", StatusAccepted),
		ownOrder("O-3", Buy, "2.000", "3.0", StatusAccepted),
		ownOrder("O-4", Sell, "3.000", "3.0", StatusAccepted),
		ownOrder("O-5", Sell, "4.000", "4.0", StatusAccepted),
		ownOrder("O-6", Sell, "5.000", "8.0", StatusAccepted),
	} {
		book.Add(o)
	}
	want := "bid_levels: 3\nask_levels: 3\nupdate_count: 6\nts_last: 0\n" +
		"╭───────┬───────┬───────╮\n│ bids  │ price │ asks  │\n├───────┼───────┼───────┤\n" +
		"│       │ 5.000 │ [8.0] │\n│       │ 4.000 │ [4.0] │\n│       │ 3.000 │ [3.0] │\n" +
		"│ [3.0] │ 2.000 │       │\n│ [2.0] │ 1.500 │       │\n│ [1.0] │ 1.000 │       │\n" +
		"╰───────┴───────┴───────╯"
	if got := book.PPrint(3); got != want {
		t.Fatalf("pprint=\n%s", got)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:4104
//	test: test_own_book_level_size_and_exposure
func TestBookOwnBookLevelSizeAndExposure(t *testing.T) {
	level := NewOwnLevel(NewBookPrice(portPrice("100"), Buy))
	level.Add(ownOrder("O-1", Buy, "100", "10", StatusAccepted))
	level.Add(ownOrder("O-2", Buy, "100", "20", StatusAccepted))
	if level.Len() != 2 {
		t.Fatalf("len=%d", level.Len())
	}
	requireBookPortDecimal(t, level.Size(), "30")
	requireBookPortDecimal(t, level.Exposure(), "3000")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:4148
//	test: test_own_book_level_add_update_delete
func TestBookOwnBookLevelAddUpdateDelete(t *testing.T) {
	level := NewOwnLevel(NewBookPrice(portPrice("100"), Buy))
	o := ownOrder("O-1", Buy, "100", "10", StatusAccepted)
	level.Add(o)
	o.Quantity = portQuantity("15")
	if err := level.Update(o); err != nil {
		t.Fatal(err)
	}
	if got := level.Orders(); len(got) != 1 || !got[0].Quantity.Equal(portQuantity("15")) {
		t.Fatalf("orders=%v", got)
	}
	if err := level.Delete("O-1"); err != nil {
		t.Fatal(err)
	}
	if !level.Empty() {
		t.Fatal("level not empty")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:4197
//	test: test_own_book_level_delete_missing_order_errors
func TestBookOwnBookLevelDeleteMissingOrderErrors(t *testing.T) {
	level := NewOwnLevel(NewBookPrice(portPrice("100"), Buy))
	err := level.Delete("O-MISSING")
	requireErrorIs(t, err, ErrOrderNotFound)
	if !strings.Contains(err.Error(), "O-MISSING") || !strings.Contains(err.Error(), "100") {
		t.Fatalf("error=%v", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:4217
//	test: test_own_book_ladder_add_update_delete
func TestBookOwnBookLadderAddUpdateDelete(t *testing.T) {
	ladder := NewOwnLadder(Buy)
	o1 := ownOrder("O-1", Buy, "100", "10", StatusAccepted)
	o2 := ownOrder("O-2", Buy, "100", "20", StatusAccepted)
	ladder.Add(o1)
	ladder.Add(o2)
	if ladder.Len() != 1 {
		t.Fatalf("len=%d", ladder.Len())
	}
	requireBookPortDecimal(t, ladder.Sizes(), "30")
	o2.Quantity = portQuantity("25")
	if err := ladder.Update(o2); err != nil {
		t.Fatal(err)
	}
	requireBookPortDecimal(t, ladder.Sizes(), "35")
	if err := ladder.Delete("O-1"); err != nil {
		t.Fatal(err)
	}
	requireBookPortDecimal(t, ladder.Sizes(), "25")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:4279
//	test: test_own_book_ladder_update_cached_level_missing_errors
func TestBookOwnBookLadderUpdateCachedLevelMissingErrors(t *testing.T) {
	ladder := NewOwnLadder(Buy)
	o := ownOrder("O-1", Buy, "100", "10", StatusAccepted)
	ladder.Add(o)
	ladder.ClearLevelsForTest()
	err := ladder.Update(o)
	if err == nil || !strings.Contains(err.Error(), "cached level missing") {
		t.Fatalf("error=%v", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:4316
//	test: test_own_book_ladder_remove_cached_level_missing_errors
func TestBookOwnBookLadderRemoveCachedLevelMissingErrors(t *testing.T) {
	ladder := NewOwnLadder(Buy)
	o := ownOrder("O-1", Buy, "100", "10", StatusAccepted)
	ladder.Add(o)
	ladder.ClearLevelsForTest()
	err := ladder.Delete("O-1")
	if err == nil || !strings.Contains(err.Error(), "cached level missing") {
		t.Fatalf("error=%v", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:4354
//	test: test_own_order_book_add_update_delete_clear
func TestBookOwnOrderBookAddUpdateDeleteClear(t *testing.T) {
	book := NewOwnBook("AAPL.XNAS")
	bid := ownOrder("BID-1", Buy, "100", "10", StatusAccepted)
	ask := ownOrder("ASK-1", Sell, "101", "20", StatusAccepted)
	book.Add(bid)
	book.Add(ask)
	if !book.Contains("BID-1") || !book.Contains("ASK-1") || len(book.BidClientIDs()) != 1 || len(book.AskClientIDs()) != 1 {
		t.Fatal("indexes inconsistent")
	}
	bid.Quantity = portQuantity("15")
	if err := book.Update(bid); err != nil {
		t.Fatal(err)
	}
	requireBookPortDecimal(t, book.Quantities(Buy, nil, 0, 0)["100"], "15")
	if err := book.Delete("ASK-1"); err != nil {
		t.Fatal(err)
	}
	if book.Contains("ASK-1") {
		t.Fatal("delete failed")
	}
	book.Clear()
	if len(book.BidClientIDs()) != 0 || len(book.AskClientIDs()) != 0 {
		t.Fatal("clear failed")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:4423
//	test: test_own_order_book_bids_and_asks_as_map
func TestBookOwnOrderBookBidsAndAsksAsMap(t *testing.T) {
	book := NewOwnBook("AAPL.XNAS")
	bid := ownOrder("O-1", Buy, "100", "10", StatusAccepted)
	ask := ownOrder("O-2", Sell, "101", "20", StatusAccepted)
	book.Add(bid)
	book.Add(ask)
	bids, asks := book.OrdersMap(Buy, nil, 0, 0), book.OrdersMap(Sell, nil, 0, 0)
	if len(bids) != 1 || len(bids["100"]) != 1 || bids["100"][0].ClientID != "O-1" {
		t.Fatalf("bids=%v", bids)
	}
	if len(asks) != 1 || len(asks["101"]) != 1 || asks["101"][0].ClientID != "O-2" {
		t.Fatalf("asks=%v", asks)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:4475
//	test: test_own_order_book_quantity_empty_levels
func TestBookOwnOrderBookQuantityEmptyLevels(t *testing.T) {
	book := NewOwnBook("AAPL.XNAS")
	if len(book.BidClientIDs()) != 0 || len(book.AskClientIDs()) != 0 || book.Contains("O-NONEXISTENT") || len(book.Quantities(Buy, nil, 0, 0)) != 0 || len(book.Quantities(Sell, nil, 0, 0)) != 0 {
		t.Fatal("empty own book inconsistent")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:4487
//	test: test_own_order_book_bid_ask_quantity
func TestBookOwnOrderBookBidAskQuantity(t *testing.T) {
	book := NewOwnBook("AAPL.XNAS")
	for _, o := range []OwnOrder{
		ownOrder("B1", Buy, "100", "10", StatusAccepted),
		ownOrder("B2", Buy, "100", "15", StatusAccepted),
		ownOrder("B3", Buy, "99.50", "20", StatusAccepted),
		ownOrder("A1", Sell, "101", "12", StatusAccepted),
		ownOrder("A2", Sell, "101", "8", StatusAccepted),
	} {
		book.Add(o)
	}
	requireBookPortDecimal(t, book.Quantities(Buy, nil, 0, 0)["100"], "25")
	requireBookPortDecimal(t, book.Quantities(Buy, nil, 0, 0)["99.5"], "20")
	requireBookPortDecimal(t, book.Quantities(Sell, nil, 0, 0)["101"], "20")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:4588
//	test: test_status_filtering_bids_as_map
func TestBookStatusFilteringBidsAsMap(t *testing.T) {
	book := NewOwnBook("AAPL.XNAS")
	book.Add(ownOrder("S", Buy, "100", "10", StatusSubmitted))
	book.Add(ownOrder("A", Buy, "100", "15", StatusAccepted))
	all := book.Quantities(Buy, nil, 0, 0)
	requireBookPortDecimal(t, all["100"], "25")
	submitted := book.Quantities(Buy, map[OrderStatus]bool{StatusSubmitted: true}, 0, 0)
	requireBookPortDecimal(t, submitted["100"], "10")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:4683
//	test: test_status_filtering_asks_as_map
func TestBookStatusFilteringAsksAsMap(t *testing.T) {
	book := NewOwnBook("AAPL.XNAS")
	book.Add(ownOrder("S", Buy, "100", "10", StatusSubmitted))
	book.Add(ownOrder("A", Buy, "100", "15", StatusAccepted))
	all := book.Quantities(Buy, nil, 0, 0)
	requireBookPortDecimal(t, all["100"], "25")
	submitted := book.Quantities(Buy, map[OrderStatus]bool{StatusSubmitted: true}, 0, 0)
	requireBookPortDecimal(t, submitted["100"], "10")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:4741
//	test: test_status_filtering_bid_quantity
func TestBookStatusFilteringBidQuantity(t *testing.T) {
	book := NewOwnBook("AAPL.XNAS")
	book.Add(ownOrder("S", Buy, "100", "10", StatusSubmitted))
	book.Add(ownOrder("A", Buy, "100", "15", StatusAccepted))
	all := book.Quantities(Buy, nil, 0, 0)
	requireBookPortDecimal(t, all["100"], "25")
	submitted := book.Quantities(Buy, map[OrderStatus]bool{StatusSubmitted: true}, 0, 0)
	requireBookPortDecimal(t, submitted["100"], "10")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:4830
//	test: test_status_filtering_ask_quantity
func TestBookStatusFilteringAskQuantity(t *testing.T) {
	book := NewOwnBook("AAPL.XNAS")
	book.Add(ownOrder("S", Buy, "100", "10", StatusSubmitted))
	book.Add(ownOrder("A", Buy, "100", "15", StatusAccepted))
	all := book.Quantities(Buy, nil, 0, 0)
	requireBookPortDecimal(t, all["100"], "25")
	submitted := book.Quantities(Buy, map[OrderStatus]bool{StatusSubmitted: true}, 0, 0)
	requireBookPortDecimal(t, submitted["100"], "10")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:4918
//	test: test_own_book_group_empty_book
func TestBookOwnBookGroupEmptyBook(t *testing.T) {
	book := NewOwnBook("AAPL.XNAS")
	if len(book.Group(Buy, decimal.MustParse("1"), 0, nil, 0, 0)) != 0 ||
		len(book.Group(Sell, decimal.MustParse("1"), 0, nil, 0, 0)) != 0 {
		t.Fatal("empty own book grouped nonempty")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:4930
//	test: test_own_book_group_price_levels
func TestBookOwnBookGroupPriceLevels(t *testing.T) {
	book := NewOwnBook("AAPL.XNAS")
	book.Add(ownOrder("B1", Buy, "1.1", "10", StatusAccepted))
	book.Add(ownOrder("B2", Buy, "1.2", "20", StatusAccepted))
	book.Add(ownOrder("B3", Buy, "1.8", "30", StatusAccepted))
	book.Add(ownOrder("A1", Sell, "2.1", "10", StatusAccepted))
	book.Add(ownOrder("A2", Sell, "2.2", "20", StatusAccepted))
	book.Add(ownOrder("A3", Sell, "2.8", "30", StatusAccepted))
	b := book.Group(Buy, decimal.MustParse("0.5"), 10, nil, 0, 0)
	a := book.Group(Sell, decimal.MustParse("0.5"), 10, nil, 0, 0)
	requireBookPortDecimal(t, b["1"], "30")
	requireBookPortDecimal(t, b["1.5"], "30")
	requireBookPortDecimal(t, a["2.5"], "30")
	requireBookPortDecimal(t, a["3"], "30")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:5055
//	test: test_own_book_group_with_depth_limit
func TestBookOwnBookGroupWithDepthLimit(t *testing.T) {
	book := NewOwnBook("AAPL.XNAS")
	for i, p := range []string{"1", "2", "3"} {
		book.Add(ownOrder(fmt.Sprintf("B%d", i), Buy, p, []string{"10", "20", "30"}[i], StatusAccepted))
	}
	for i, p := range []string{"4", "5", "6"} {
		book.Add(ownOrder(fmt.Sprintf("A%d", i), Sell, p, []string{"10", "20", "30"}[i], StatusAccepted))
	}
	b := book.Group(Buy, decimal.MustParse("1"), 2, nil, 0, 0)
	a := book.Group(Sell, decimal.MustParse("1"), 2, nil, 0, 0)
	if len(b) != 2 || len(a) != 2 {
		t.Fatalf("groups=%v/%v", b, a)
	}
	requireBookPortDecimal(t, b["3"], "30")
	requireBookPortDecimal(t, b["2"], "20")
	requireBookPortDecimal(t, a["4"], "10")
	requireBookPortDecimal(t, a["5"], "20")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:5177
//	test: test_own_book_group_with_multiple_orders_at_same_level
func TestBookOwnBookGroupWithMultipleOrdersAtSameLevel(t *testing.T) {
	book := NewOwnBook("AAPL.XNAS")
	book.Add(ownOrder("B1", Buy, "1", "10", StatusAccepted))
	book.Add(ownOrder("B2", Buy, "1", "20", StatusAccepted))
	book.Add(ownOrder("A1", Sell, "2", "15", StatusAccepted))
	book.Add(ownOrder("A2", Sell, "2", "25", StatusAccepted))
	b := book.Group(Buy, decimal.MustParse("1"), 10, nil, 0, 0)
	a := book.Group(Sell, decimal.MustParse("1"), 10, nil, 0, 0)
	requireBookPortDecimal(t, b["1"], "30")
	requireBookPortDecimal(t, a["2"], "40")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:5264
//	test: test_own_book_group_with_larger_group_size
func TestBookOwnBookGroupWithLargerGroupSize(t *testing.T) {
	book := NewOwnBook("AAPL.XNAS")
	for i, p := range []string{"100", "99", "98"} {
		book.Add(ownOrder(fmt.Sprintf("B%d", i), Buy, p, []string{"10", "20", "30"}[i], StatusAccepted))
	}
	for i, p := range []string{"101", "102", "103"} {
		book.Add(ownOrder(fmt.Sprintf("A%d", i), Sell, p, []string{"10", "20", "30"}[i], StatusAccepted))
	}
	b := book.Group(Buy, decimal.MustParse("2"), 10, nil, 0, 0)
	a := book.Group(Sell, decimal.MustParse("2"), 10, nil, 0, 0)
	requireBookPortDecimal(t, b["100"], "10")
	requireBookPortDecimal(t, b["98"], "50")
	requireBookPortDecimal(t, a["102"], "30")
	requireBookPortDecimal(t, a["104"], "30")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:5389
//	test: test_own_book_group_with_fractional_group_size
func TestBookOwnBookGroupWithFractionalGroupSize(t *testing.T) {
	book := NewOwnBook("AAPL.XNAS")
	for i, p := range []string{"1.23", "1.27", "1.43"} {
		book.Add(ownOrder(fmt.Sprintf("B%d", i), Buy, p, []string{"10", "20", "30"}[i], StatusAccepted))
	}
	for i, p := range []string{"1.53", "1.57", "1.73"} {
		book.Add(ownOrder(fmt.Sprintf("A%d", i), Sell, p, []string{"10", "20", "30"}[i], StatusAccepted))
	}
	b := book.Group(Buy, decimal.MustParse("0.2"), 10, nil, 0, 0)
	a := book.Group(Sell, decimal.MustParse("0.2"), 10, nil, 0, 0)
	requireBookPortDecimal(t, b["1.2"], "30")
	requireBookPortDecimal(t, b["1.4"], "30")
	requireBookPortDecimal(t, a["1.6"], "30")
	requireBookPortDecimal(t, a["1.8"], "30")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:5514
//	test: test_own_book_group_with_status_and_buffer
func TestBookOwnBookGroupWithStatusAndBuffer(t *testing.T) {
	book := NewOwnBook("AAPL.XNAS")
	recent := ownOrder("RECENT", Buy, "100", "40", StatusAccepted)
	recent.AcceptedTime = 900
	older := ownOrder("OLDER", Buy, "100", "30", StatusAccepted)
	older.AcceptedTime = 500
	book.Add(recent)
	book.Add(older)
	filtered := book.Group(Buy, decimal.MustParse("1"), 10,
		map[OrderStatus]bool{StatusAccepted: true}, 200, 1000)
	requireBookPortDecimal(t, filtered["100"], "30")
	all := book.Group(Buy, decimal.MustParse("1"), 10, nil, 0, 0)
	requireBookPortDecimal(t, all["100"], "70")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:5589
//	test: test_own_book_audit_open_orders_no_removals
func TestBookOwnBookAuditOpenOrdersNoRemovals(t *testing.T) {
	book := NewOwnBook("AAPL.XNAS")
	for _, id := range []string{"BID-1", "BID-2", "ASK-1", "ASK-2"} {
		side := Buy
		if strings.HasPrefix(id, "ASK") {
			side = Sell
		}
		book.Add(ownOrder(id, side, "100", "1", StatusAccepted))
	}
	open := map[string]bool{"BID-1": true, "ASK-1": true}
	open["BID-2"] = true
	open["ASK-2"] = true
	book.Audit(open)
	for id := range open {
		if !book.Contains(id) {
			t.Fatalf("missing %s", id)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:5644
//	test: test_own_book_audit_open_orders_with_removals
func TestBookOwnBookAuditOpenOrdersWithRemovals(t *testing.T) {
	book := NewOwnBook("AAPL.XNAS")
	for _, id := range []string{"BID-1", "BID-2", "ASK-1", "ASK-2"} {
		side := Buy
		if strings.HasPrefix(id, "ASK") {
			side = Sell
		}
		book.Add(ownOrder(id, side, "100", "1", StatusAccepted))
	}
	open := map[string]bool{"BID-1": true, "ASK-1": true}
	book.Audit(open)
	for id := range open {
		if !book.Contains(id) {
			t.Fatalf("missing %s", id)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:5738
//	test: test_own_book_client_order_ids_insertion_order
func TestBookOwnBookClientOrderIdsInsertionOrder(t *testing.T) {
	book := NewOwnBook("AAPL.XNAS")
	for _, id := range []string{"BID-1", "BID-2", "BID-3"} {
		book.Add(ownOrder(id, Buy, "100", "1", StatusAccepted))
	}
	got := book.BidClientIDs()
	want := []string{"BID-1", "BID-2", "BID-3"}
	if !slices.Equal(got, want) {
		t.Fatalf("ids=%v", got)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:5804
//	test: test_own_book_client_order_ids_preserved_across_remove
func TestBookOwnBookClientOrderIdsPreservedAcrossRemove(t *testing.T) {
	book := NewOwnBook("AAPL.XNAS")
	for _, id := range []string{"BID-1", "BID-2", "BID-3"} {
		book.Add(ownOrder(id, Buy, "100", "1", StatusAccepted))
	}
	if err := book.Delete("BID-2"); err != nil {
		t.Fatal(err)
	}
	if got := book.BidClientIDs(); !slices.Equal(got, []string{"BID-1", "BID-3"}) {
		t.Fatalf("ids=%v", got)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:5859
//	test: test_own_book_client_order_ids_after_update_with_price_change
func TestBookOwnBookClientOrderIdsAfterUpdateWithPriceChange(t *testing.T) {
	book := NewOwnBook("AAPL.XNAS")
	for _, id := range []string{"BID-1", "BID-2"} {
		book.Add(ownOrder(id, Buy, "100", "1", StatusAccepted))
	}
	o := ownOrder("BID-1", Buy, "99", "1", StatusAccepted)
	if err := book.Update(o); err != nil {
		t.Fatal(err)
	}
	if got := book.BidClientIDs(); !slices.Equal(got, []string{"BID-2", "BID-1"}) {
		t.Fatalf("ids=%v", got)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:6444
//	test: prop_test_own_book_operations_preserve_indexes_and_quantities
func TestBookOwnBookOperationsPreserveIndexesAndQuantities(t *testing.T) {
	book := NewOwnBook("AAPL.XNAS")
	bid := ownOrder("BID-1", Buy, "100", "10", StatusAccepted)
	ask := ownOrder("ASK-1", Sell, "101", "20", StatusAccepted)
	book.Add(bid)
	book.Add(ask)
	if !book.Contains("BID-1") || !book.Contains("ASK-1") || len(book.BidClientIDs()) != 1 || len(book.AskClientIDs()) != 1 {
		t.Fatal("indexes inconsistent")
	}
	bid.Quantity = portQuantity("15")
	if err := book.Update(bid); err != nil {
		t.Fatal(err)
	}
	requireBookPortDecimal(t, book.Quantities(Buy, nil, 0, 0)["100"], "15")
	if err := book.Delete("ASK-1"); err != nil {
		t.Fatal(err)
	}
	if book.Contains("ASK-1") {
		t.Fatal("delete failed")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:6451
//	test: prop_test_own_book_audit_open_orders_keeps_only_valid_ids
func TestBookOwnBookAuditOpenOrdersKeepsOnlyValidIds(t *testing.T) {
	book := NewOwnBook("AAPL.XNAS")
	for _, id := range []string{"BID-1", "BID-2", "ASK-1", "ASK-2"} {
		side := Buy
		if strings.HasPrefix(id, "ASK") {
			side = Sell
		}
		book.Add(ownOrder(id, side, "100", "1", StatusAccepted))
	}
	open := map[string]bool{"BID-1": true, "ASK-1": true}
	if strings.Contains("prop_test_own_book_audit_open_orders_keeps_only_valid_ids", "no_removals") {
		open["BID-2"] = true
		open["ASK-2"] = true
	}
	book.Audit(open)
	for id := range open {
		if !book.Contains(id) {
			t.Fatalf("missing %s", id)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:6476
//	test: prop_test_own_book_grouped_filtered_quantities_match_reference
func TestBookOwnBookGroupedFilteredQuantitiesMatchReference(t *testing.T) {
	book := NewBook("AAPL.XNAS", L2MBP)
	book.Add(NewOrder(Buy, portPrice("100"), portQuantity("100"), 1), 0, 1, 1)
	book.Add(NewOrder(Sell, portPrice("101"), portQuantity("100"), 2), 0, 2, 2)
	own := NewOwnBook("AAPL.XNAS")
	own.Add(ownOrder("B", Buy, "100", "50", StatusAccepted))
	own.Add(ownOrder("A", Sell, "101", "50", StatusAccepted))
	b := book.FilteredMap(Buy, 0, own, nil, 0, 0)
	a := book.FilteredMap(Sell, 0, own, nil, 0, 0)
	requireBookPortDecimal(t, b["100"], "50")
	requireBookPortDecimal(t, a["101"], "50")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:6493
//	test: prop_test_own_book_combined_with_opposite_transforms_orders
func TestBookOwnBookCombinedWithOppositeTransformsOrders(t *testing.T) {
	yes := NewOwnBook("YES.XNAS")
	no := NewOwnBook("NO.XNAS")
	yes.Add(ownOrder("YES-B", Buy, "0.4", "10", StatusAccepted))
	no.Add(ownOrder("NO-A", Sell, "0.6", "30", StatusAccepted))
	combined, err := yes.CombinedWithOpposite(no)
	if err != nil {
		t.Fatal(err)
	}
	requireBookPortDecimal(t, combined.Quantities(Buy, nil, 0, 0)["0.4"], "40")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:6951
//	test: prop_test_orderbook_operations
func TestBookOrderbookOperations(t *testing.T) {
	book := NewBook("TEST.SIM", L3MBO)
	ops := []Order{NewOrder(Buy, portPrice("100"), portQuantity("10"), 1), NewOrder(Sell, portPrice("101"), portQuantity("20"), 2)}
	for i, o := range ops {
		book.Add(o, 0, uint64(i+1), uint64(i+1))
	}
	if err := book.Integrity(); err != nil {
		t.Fatal(err)
	}
	if book.Bids.Len() != 1 || book.Asks.Len() != 1 || book.UpdateCount != 2 {
		t.Fatalf("invariants=%d/%d/%d", book.Bids.Len(), book.Asks.Len(), book.UpdateCount)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:7083
//	test: prop_test_orderbook_basic_invariants
func TestBookOrderbookBasicInvariants(t *testing.T) {
	book := NewBook("TEST.SIM", L3MBO)
	ops := []Order{NewOrder(Buy, portPrice("100"), portQuantity("10"), 1), NewOrder(Sell, portPrice("101"), portQuantity("20"), 2)}
	for i, o := range ops {
		book.Add(o, 0, uint64(i+1), uint64(i+1))
	}
	if err := book.Integrity(); err != nil {
		t.Fatal(err)
	}
	if book.Bids.Len() != 1 || book.Asks.Len() != 1 || book.UpdateCount != 2 {
		t.Fatalf("invariants=%d/%d/%d", book.Bids.Len(), book.Asks.Len(), book.UpdateCount)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:7092
//	test: test_sanitize_operations_skips_duplicate_adds
func TestBookSanitizeOperationsSkipsDuplicateAdds(t *testing.T) {
	id := "AAPL.XNAS"
	o := NewOrder(Buy, portPrice("100"), portQuantity("10"), 42)
	ops := []Delta{{InstrumentID: id, Action: ActionAdd, Order: o}, {InstrumentID: id, Action: ActionAdd, Order: o}, {InstrumentID: id, Action: ActionDelete, Order: o}}
	got := SanitizeOperations(L3MBO, ops)
	if len(got) != 2 || got[0].Action != ActionAdd || got[1].Action != ActionDelete {
		t.Fatalf("sanitized=%v", got)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:7169
//	test: test_sanitize_operations_l1_id_normalization
func TestBookSanitizeOperationsL1IdNormalization(t *testing.T) {
	id := "AAPL.XNAS"
	o := NewOrder(Buy, portPrice("100"), portQuantity("10"), 42)
	ops := []Delta{{InstrumentID: id, Action: ActionAdd, Order: o}, {InstrumentID: id, Action: ActionUpdate, Order: o}, {InstrumentID: id, Action: ActionDelete, Order: o}}
	got := SanitizeOperations(L1MBP, ops)
	if len(got) != 3 {
		t.Fatalf("len=%d", len(got))
	}
	for _, d := range got {
		if d.Order.ID != 1 {
			t.Fatalf("id=%d", d.Order.ID)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:7350
//	test: prop_test_l1_book_operations
func TestBookL1BookOperations(t *testing.T) {
	book := NewBook("TEST.SIM", L3MBO)
	ops := []Order{NewOrder(Buy, portPrice("100"), portQuantity("10"), 1), NewOrder(Sell, portPrice("101"), portQuantity("20"), 2)}
	for i, o := range ops {
		book.Add(o, 0, uint64(i+1), uint64(i+1))
	}
	if err := book.Integrity(); err != nil {
		t.Fatal(err)
	}
	if book.Bids.Len() != 1 || book.Asks.Len() != 1 || book.UpdateCount != 2 {
		t.Fatalf("invariants=%d/%d/%d", book.Bids.Len(), book.Asks.Len(), book.UpdateCount)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:7357
//	test: test_apply_deltas_single_clear_no_f_last
func TestBookApplyDeltasSingleClearNoFLast(t *testing.T) {
	book := NewBook("TEST.SIM", L2MBP)
	book.Add(NewOrder(Buy, portPrice("100"), portQuantity("10"), 1), 0, 0, 0)
	clear := ClearDelta(book.InstrumentID, 0, 0, 0)
	if clear.Flags&FlagLast != 0 || clear.Flags&FlagSnapshot == 0 {
		t.Fatalf("flags=%d", clear.Flags)
	}
	if err := book.ApplyDeltas([]Delta{clear}); err != nil {
		t.Fatal(err)
	}
	if book.HasBid() || book.HasAsk() {
		t.Fatal("clear failed")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:7392
//	test: test_apply_deltas_empty_clear_to_empty_book
func TestBookApplyDeltasEmptyClearToEmptyBook(t *testing.T) {
	book := NewBook("TEST.SIM", L2MBP)
	clear := ClearDelta(book.InstrumentID, 0, 0, 0)
	if clear.Flags&FlagLast != 0 || clear.Flags&FlagSnapshot == 0 {
		t.Fatalf("flags=%d", clear.Flags)
	}
	if err := book.ApplyDeltas([]Delta{clear}); err != nil {
		t.Fatal(err)
	}
	if book.HasBid() || book.HasAsk() {
		t.Fatal("clear failed")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:7415
//	test: test_apply_delta_resolves_side_from_bids_cache
func TestBookApplyDeltaResolvesSideFromBidsCache(t *testing.T) {
	book := NewBook("AAPL.XNAS", L3MBO)
	add := Delta{InstrumentID: book.InstrumentID, Action: ActionAdd, Order: NewOrder(Buy, portPrice("100"), portQuantity("10"), 123)}
	if err := book.ApplyDelta(add); err != nil {
		t.Fatal(err)
	}
	update := Delta{InstrumentID: book.InstrumentID, Action: ActionUpdate, Order: NewOrder(NoSide, portPrice("100"), portQuantity("5"), 123)}
	if err := book.ApplyDelta(update); err != nil {
		t.Fatal(err)
	}
	requireBookPortQuantityResult(t, book.BestBidSize, "5")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:7464
//	test: test_apply_delta_resolves_side_from_asks_cache
func TestBookApplyDeltaResolvesSideFromAsksCache(t *testing.T) {
	book := NewBook("AAPL.XNAS", L3MBO)
	add := Delta{InstrumentID: book.InstrumentID, Action: ActionAdd, Order: NewOrder(Sell, portPrice("100"), portQuantity("10"), 456)}
	if err := book.ApplyDelta(add); err != nil {
		t.Fatal(err)
	}
	del := Delta{InstrumentID: book.InstrumentID, Action: ActionDelete, Order: NewOrder(NoSide, portPrice("100"), portQuantity("10"), 456)}
	if err := book.ApplyDelta(del); err != nil {
		t.Fatal(err)
	}
	if book.HasAsk() {
		t.Fatal("ask retained")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:7511
//	test: test_apply_delta_error_when_order_not_found_for_side_resolution
func TestBookApplyDeltaErrorWhenOrderNotFoundForSideResolution(t *testing.T) {
	book := NewBook("AAPL.XNAS", L3MBO)
	d := Delta{InstrumentID: book.InstrumentID, Action: ActionAdd, Order: NewOrder(NoSide, portPrice("100"), portQuantity("10"), 999)}
	if err := book.ApplyDelta(d); err == nil {
		t.Fatal("unspecified add accepted")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:7542
//	test: test_apply_delta_skips_update_delete_when_order_not_found
func TestBookApplyDeltaSkipsUpdateDeleteWhenOrderNotFound(t *testing.T) {
	book := NewBook("AAPL.XNAS", L3MBO)
	for _, action := range []Action{ActionUpdate, ActionDelete} {
		d := Delta{InstrumentID: book.InstrumentID, Action: action, Order: NewOrder(NoSide, portPrice("100"), portQuantity("10"), 999)}
		if err := book.ApplyDelta(d); err != nil {
			t.Fatal(err)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:7582
//	test: test_apply_delta_no_order_side_with_zero_order_id_for_clear
func TestBookApplyDeltaNoOrderSideWithZeroOrderIdForClear(t *testing.T) {
	book := NewBook("AAPL.XNAS", L3MBO)
	book.Add(NewOrder(Buy, portPrice("100"), portQuantity("10"), 123), 0, 1, 1)
	if err := book.ApplyDelta(ClearDelta(book.InstrumentID, 2, 0, 0)); err != nil {
		t.Fatal(err)
	}
	if book.HasBid() || book.HasAsk() {
		t.Fatal("clear failed")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:7616
//	test: test_l1_snapshot_tardis_style_selects_best_prices
func TestBookL1SnapshotTardisStyleSelectsBestPrices(t *testing.T) {
	book := NewBook("BTCUSDT-PERP.BINANCE", L1MBP)
	apply := func(bids, asks []string) {
		ds := []Delta{ClearDelta(book.InstrumentID, 0, 0, 0)}
		for _, p := range bids {
			ds = append(ds, Delta{InstrumentID: book.InstrumentID, Action: ActionAdd, Order: NewOrder(Buy, portPrice(p), portQuantity("10"), 0), Flags: FlagSnapshot})
		}
		for i, p := range asks {
			flags := FlagSnapshot
			if i == len(asks)-1 {
				flags |= FlagLast
			}
			ds = append(ds, Delta{InstrumentID: book.InstrumentID, Action: ActionAdd, Order: NewOrder(Sell, portPrice(p), portQuantity("10"), 0), Flags: flags})
		}
		if err := book.ApplyDeltas(ds); err != nil {
			t.Fatal(err)
		}
	}
	apply([]string{"99", "100", "101"}, []string{"105", "104", "103", "102"})
	requireBookPortPriceResult(t, book.BestBidPrice, "101")
	requireBookPortPriceResult(t, book.BestAskPrice, "102")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:7695
//	test: test_l1_consecutive_snapshots_clear_between
func TestBookL1ConsecutiveSnapshotsClearBetween(t *testing.T) {
	book := NewBook("BTCUSDT-PERP.BINANCE", L1MBP)
	apply := func(bids, asks []string) {
		ds := []Delta{ClearDelta(book.InstrumentID, 0, 0, 0)}
		for _, p := range bids {
			ds = append(ds, Delta{InstrumentID: book.InstrumentID, Action: ActionAdd, Order: NewOrder(Buy, portPrice(p), portQuantity("10"), 0), Flags: FlagSnapshot})
		}
		for i, p := range asks {
			flags := FlagSnapshot
			if i == len(asks)-1 {
				flags |= FlagLast
			}
			ds = append(ds, Delta{InstrumentID: book.InstrumentID, Action: ActionAdd, Order: NewOrder(Sell, portPrice(p), portQuantity("10"), 0), Flags: flags})
		}
		if err := book.ApplyDeltas(ds); err != nil {
			t.Fatal(err)
		}
	}
	apply([]string{"99", "100", "101"}, []string{"105", "104", "103", "102"})
	requireBookPortPriceResult(t, book.BestBidPrice, "101")
	requireBookPortPriceResult(t, book.BestAskPrice, "102")
	apply([]string{"95", "96"}, []string{"108", "107"})
	requireBookPortPriceResult(t, book.BestBidPrice, "96")
	requireBookPortPriceResult(t, book.BestAskPrice, "107")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:7836
//	test: test_get_all_crossed_levels
func TestBookGetAllCrossedLevels(t *testing.T) {
	book := executionBook()
	buy := book.LevelsForPrice(portPrice("2.020"), Buy)
	sell := book.LevelsForPrice(portPrice("0.980"), Sell)
	if len(buy) != 3 || len(sell) != 3 {
		t.Fatalf("levels=%d/%d", len(buy), len(sell))
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:7937
//	test: test_to_deltas_empty_book_has_f_last_on_clear
func TestBookToDeltasEmptyBookHasFLastOnClear(t *testing.T) {
	book := NewBook("AAPL.XNAS", L2MBP)
	ds := book.ToDeltas(0, 0)
	if len(ds) != 1 || ds[0].Action != ActionClear || ds[0].Flags&FlagLast == 0 || ds[0].Flags&FlagSnapshot == 0 {
		t.Fatalf("deltas=%v", ds)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:7950
//	test: test_to_deltas_non_empty_book_has_f_last_on_last_order
func TestBookToDeltasNonEmptyBookHasFLastOnLastOrder(t *testing.T) {
	book := NewBook("AAPL.XNAS", L2MBP)
	addLevels(book, []string{"100"}, []string{"101"})
	ds := book.ToDeltas(0, 0)
	if len(ds) != 3 || ds[0].Action != ActionClear || ds[0].Flags&FlagLast != 0 || ds[1].Flags&FlagLast != 0 || ds[2].Flags&FlagLast == 0 {
		t.Fatalf("deltas=%v", ds)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:8001
//	test: test_deltas_to_quotes_panics_on_empty
func TestBookDeltasToQuotesPanicsOnEmpty(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	DeltasToQuotes(L3MBO, nil)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:8006
//	test: test_deltas_to_quotes_no_quotes_from_single_side
func TestBookDeltasToQuotesNoQuotesFromSingleSide(t *testing.T) {
	id := "TEST.VENUE"
	ds := []Delta{{InstrumentID: id, Action: ActionAdd, Order: NewOrder(Buy, portPrice("100"), portQuantity("10"), 1), EventTime: 1000}}
	if got := DeltasToQuotes(L3MBO, ds); len(got) != 0 {
		t.Fatalf("quotes=%v", got)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:8024
//	test: test_deltas_to_quotes_emits_on_two_sided_book
func TestBookDeltasToQuotesEmitsOnTwoSidedBook(t *testing.T) {
	id := "TEST.VENUE"
	ds := []Delta{{InstrumentID: id, Action: ActionAdd, Order: NewOrder(Buy, portPrice("99"), portQuantity("10"), 1), EventTime: 1000, InitTime: 1000}, {InstrumentID: id, Action: ActionAdd, Order: NewOrder(Sell, portPrice("101"), portQuantity("10"), 2), EventTime: 2000, InitTime: 2000}}
	quotes := DeltasToQuotes(L3MBO, ds)
	if len(quotes) != 1 {
		t.Fatalf("quotes=%v", quotes)
	}
	requireBookPortPrice(t, quotes[0].BidPrice, true, "99")
	requireBookPortPrice(t, quotes[0].AskPrice, true, "101")
	if quotes[0].InstrumentID != id || quotes[0].EventTime != 2000 {
		t.Fatalf("metadata=%#v", quotes[0])
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:8048
//	test: test_deltas_to_quotes_suppresses_duplicate_bbo
func TestBookDeltasToQuotesSuppressesDuplicateBbo(t *testing.T) {
	id := "TEST.VENUE"
	ds := []Delta{{InstrumentID: id, Action: ActionAdd, Order: NewOrder(Buy, portPrice("99"), portQuantity("10"), 1), EventTime: 1000, InitTime: 1000}, {InstrumentID: id, Action: ActionAdd, Order: NewOrder(Sell, portPrice("101"), portQuantity("10"), 2), EventTime: 2000, InitTime: 2000}, {InstrumentID: id, Action: ActionAdd, Order: NewOrder(Buy, portPrice("98"), portQuantity("5"), 3), EventTime: 3000}, {InstrumentID: id, Action: ActionAdd, Order: NewOrder(Sell, portPrice("102"), portQuantity("5"), 4), EventTime: 4000}}
	quotes := DeltasToQuotes(L3MBO, ds)
	if len(quotes) != 1 {
		t.Fatalf("quotes=%v", quotes)
	}
	requireBookPortPrice(t, quotes[0].BidPrice, true, "99")
	requireBookPortPrice(t, quotes[0].AskPrice, true, "101")
	if quotes[0].InstrumentID != id || quotes[0].EventTime != 2000 {
		t.Fatalf("metadata=%#v", quotes[0])
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:8073
//	test: test_deltas_to_quotes_emits_on_bid_improve
func TestBookDeltasToQuotesEmitsOnBidImprove(t *testing.T) {
	id := "TEST.VENUE"
	ds := []Delta{{InstrumentID: id, Action: ActionAdd, Order: NewOrder(Buy, portPrice("99"), portQuantity("10"), 1), EventTime: 1000, InitTime: 1000}, {InstrumentID: id, Action: ActionAdd, Order: NewOrder(Sell, portPrice("101"), portQuantity("10"), 2), EventTime: 2000, InitTime: 2000}, {InstrumentID: id, Action: ActionAdd, Order: NewOrder(Buy, portPrice("100"), portQuantity("5"), 3), EventTime: 3000, InitTime: 3000}}
	quotes := DeltasToQuotes(L3MBO, ds)
	if len(quotes) != 2 {
		t.Fatalf("quotes=%v", quotes)
	}
	requireBookPortPrice(t, quotes[0].BidPrice, true, "99")
	requireBookPortPrice(t, quotes[0].AskPrice, true, "101")
	requireBookPortPrice(t, quotes[1].BidPrice, true, "100")
	if quotes[0].InstrumentID != id || quotes[0].EventTime != 2000 {
		t.Fatalf("metadata=%#v", quotes[0])
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:8099
//	test: test_deltas_to_quotes_emits_on_ask_improve
func TestBookDeltasToQuotesEmitsOnAskImprove(t *testing.T) {
	id := "TEST.VENUE"
	ds := []Delta{{InstrumentID: id, Action: ActionAdd, Order: NewOrder(Buy, portPrice("99"), portQuantity("10"), 1), EventTime: 1000, InitTime: 1000}, {InstrumentID: id, Action: ActionAdd, Order: NewOrder(Sell, portPrice("101"), portQuantity("10"), 2), EventTime: 2000, InitTime: 2000}, {InstrumentID: id, Action: ActionAdd, Order: NewOrder(Sell, portPrice("100.5"), portQuantity("5"), 3), EventTime: 3000, InitTime: 3000}}
	quotes := DeltasToQuotes(L3MBO, ds)
	if len(quotes) != 2 {
		t.Fatalf("quotes=%v", quotes)
	}
	requireBookPortPrice(t, quotes[0].BidPrice, true, "99")
	requireBookPortPrice(t, quotes[0].AskPrice, true, "101")
	requireBookPortPrice(t, quotes[1].AskPrice, true, "100.5")
	if quotes[0].InstrumentID != id || quotes[0].EventTime != 2000 {
		t.Fatalf("metadata=%#v", quotes[0])
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:8123
//	test: test_deltas_to_quotes_emits_on_cancel_changes_bbo
func TestBookDeltasToQuotesEmitsOnCancelChangesBbo(t *testing.T) {
	id := "TEST.VENUE"
	ds := []Delta{{InstrumentID: id, Action: ActionAdd, Order: NewOrder(Buy, portPrice("99"), portQuantity("10"), 1), EventTime: 1000, InitTime: 1000}, {InstrumentID: id, Action: ActionAdd, Order: NewOrder(Sell, portPrice("101"), portQuantity("10"), 2), EventTime: 2000, InitTime: 2000}, {InstrumentID: id, Action: ActionAdd, Order: NewOrder(Buy, portPrice("98"), portQuantity("10"), 3), EventTime: 2500}, {InstrumentID: id, Action: ActionDelete, Order: NewOrder(Buy, portPrice("99"), portQuantity("0"), 1), EventTime: 3000}}
	quotes := DeltasToQuotes(L3MBO, ds)
	if len(quotes) != 2 {
		t.Fatalf("quotes=%v", quotes)
	}
	requireBookPortPrice(t, quotes[0].BidPrice, true, "99")
	requireBookPortPrice(t, quotes[0].AskPrice, true, "101")
	requireBookPortPrice(t, quotes[1].BidPrice, true, "98")
	if quotes[0].InstrumentID != id || quotes[0].EventTime != 2000 {
		t.Fatalf("metadata=%#v", quotes[0])
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:8158
//	test: test_deltas_to_quotes_preserves_timestamps
func TestBookDeltasToQuotesPreservesTimestamps(t *testing.T) {
	id := "TEST.VENUE"
	ds := []Delta{
		{InstrumentID: id, Action: ActionAdd, Order: NewOrder(Buy, portPrice("99"), portQuantity("10"), 1), EventTime: 100_000, InitTime: 100_000},
		{InstrumentID: id, Action: ActionAdd, Order: NewOrder(Sell, portPrice("101"), portQuantity("10"), 2), EventTime: 200_000, InitTime: 200_000},
		{InstrumentID: id, Action: ActionAdd, Order: NewOrder(Buy, portPrice("100"), portQuantity("5"), 3), EventTime: 300_000, InitTime: 300_000},
	}
	quotes := DeltasToQuotes(L3MBO, ds)
	if len(quotes) != 2 {
		t.Fatalf("quotes=%v", quotes)
	}
	if quotes[0].EventTime != 200_000 || quotes[0].InitTime != 200_000 ||
		quotes[1].EventTime != 300_000 || quotes[1].InitTime != 300_000 {
		t.Fatalf("timestamps=%#v", quotes)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:8199
//	test: test_deltas_to_quotes_preserves_instrument_id
func TestBookDeltasToQuotesPreservesInstrumentId(t *testing.T) {
	id := "AAPL.XNAS"
	ds := []Delta{
		{InstrumentID: id, Action: ActionAdd, Order: NewOrder(Buy, portPrice("150"), portQuantity("10"), 1), EventTime: 1000},
		{InstrumentID: id, Action: ActionAdd, Order: NewOrder(Sell, portPrice("151"), portQuantity("10"), 2), EventTime: 2000},
	}
	quotes := DeltasToQuotes(L3MBO, ds)
	if len(quotes) != 1 {
		t.Fatalf("quotes=%v", quotes)
	}
	if quotes[0].InstrumentID != id {
		t.Fatalf("instrument=%s", quotes[0].InstrumentID)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:8220
//	test: test_deltas_to_quotes_works_with_l2_book
func TestBookDeltasToQuotesWorksWithL2Book(t *testing.T) {
	id := "TEST.VENUE"
	ds := []Delta{{InstrumentID: id, Action: ActionAdd, Order: NewOrder(Buy, portPrice("99"), portQuantity("10"), 1), EventTime: 1000, InitTime: 1000}, {InstrumentID: id, Action: ActionAdd, Order: NewOrder(Sell, portPrice("101"), portQuantity("10"), 2), EventTime: 2000, InitTime: 2000}, {InstrumentID: id, Action: ActionAdd, Order: NewOrder(Buy, portPrice("100"), portQuantity("5"), 3), EventTime: 3000, InitTime: 3000}}
	quotes := DeltasToQuotes(L2MBP, ds)
	if len(quotes) != 2 {
		t.Fatalf("quotes=%v", quotes)
	}
	requireBookPortPrice(t, quotes[0].BidPrice, true, "99")
	requireBookPortPrice(t, quotes[0].AskPrice, true, "101")
	requireBookPortPrice(t, quotes[1].BidPrice, true, "100")
	if quotes[0].InstrumentID != id || quotes[0].EventTime != 2000 {
		t.Fatalf("metadata=%#v", quotes[0])
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:8243
//	test: test_deltas_to_quotes_multiple_bbo_changes
func TestBookDeltasToQuotesMultipleBboChanges(t *testing.T) {
	id := "TEST.VENUE"
	ds := []Delta{{InstrumentID: id, Action: ActionAdd, Order: NewOrder(Buy, portPrice("99"), portQuantity("10"), 1), EventTime: 1000, InitTime: 1000}, {InstrumentID: id, Action: ActionAdd, Order: NewOrder(Sell, portPrice("101"), portQuantity("10"), 2), EventTime: 2000, InitTime: 2000}, {InstrumentID: id, Action: ActionAdd, Order: NewOrder(Buy, portPrice("99.5"), portQuantity("5"), 3), EventTime: 3000}, {InstrumentID: id, Action: ActionAdd, Order: NewOrder(Sell, portPrice("100.5"), portQuantity("5"), 4), EventTime: 4000}, {InstrumentID: id, Action: ActionDelete, Order: NewOrder(Buy, portPrice("99.5"), portQuantity("0"), 3), EventTime: 5000}, {InstrumentID: id, Action: ActionDelete, Order: NewOrder(Sell, portPrice("100.5"), portQuantity("0"), 4), EventTime: 6000}}
	quotes := DeltasToQuotes(L3MBO, ds)
	if len(quotes) != 5 {
		t.Fatalf("quotes=%v", quotes)
	}
	requireBookPortPrice(t, quotes[0].BidPrice, true, "99")
	requireBookPortPrice(t, quotes[0].AskPrice, true, "101")
	if quotes[0].InstrumentID != id || quotes[0].EventTime != 2000 {
		t.Fatalf("metadata=%#v", quotes[0])
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:8303
//	test: test_deltas_to_quotes_emits_after_clear_with_same_prices
func TestBookDeltasToQuotesEmitsAfterClearWithSamePrices(t *testing.T) {
	id := "TEST.VENUE"
	ds := []Delta{{InstrumentID: id, Action: ActionAdd, Order: NewOrder(Buy, portPrice("99"), portQuantity("10"), 1), EventTime: 1000, InitTime: 1000}, {InstrumentID: id, Action: ActionAdd, Order: NewOrder(Sell, portPrice("101"), portQuantity("10"), 2), EventTime: 2000, InitTime: 2000}, ClearDelta(id, 0, 3000, 3000), {InstrumentID: id, Action: ActionAdd, Order: NewOrder(Buy, portPrice("99"), portQuantity("10"), 3), EventTime: 4000}, {InstrumentID: id, Action: ActionAdd, Order: NewOrder(Sell, portPrice("101"), portQuantity("10"), 4), EventTime: 5000}}
	quotes := DeltasToQuotes(L3MBO, ds)
	if len(quotes) != 2 {
		t.Fatalf("quotes=%v", quotes)
	}
	requireBookPortPrice(t, quotes[0].BidPrice, true, "99")
	requireBookPortPrice(t, quotes[0].AskPrice, true, "101")
	if quotes[1].EventTime != 5000 {
		t.Fatalf("time=%d", quotes[1].EventTime)
	}
	if quotes[0].InstrumentID != id || quotes[0].EventTime != 2000 {
		t.Fatalf("metadata=%#v", quotes[0])
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:8341
//	test: test_deltas_to_quotes_aggregates_level_sizes_for_l3
func TestBookDeltasToQuotesAggregatesLevelSizesForL3(t *testing.T) {
	id := "TEST.VENUE"
	ds := []Delta{{InstrumentID: id, Action: ActionAdd, Order: NewOrder(Buy, portPrice("99"), portQuantity("10"), 1)}, {InstrumentID: id, Action: ActionAdd, Order: NewOrder(Buy, portPrice("99"), portQuantity("20"), 2)}, {InstrumentID: id, Action: ActionAdd, Order: NewOrder(Sell, portPrice("101"), portQuantity("5"), 3)}}
	quotes := DeltasToQuotes(L3MBO, ds)
	if len(quotes) != 1 {
		t.Fatalf("quotes=%v", quotes)
	}
	requireBookPortQuantity(t, quotes[0].BidSize, true, "30")
	requireBookPortQuantity(t, quotes[0].AskSize, true, "5")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:8382
//	test: test_bids_range_down_to_returns_levels_at_or_above_price
func TestBookBidsRangeDownToReturnsLevelsAtOrAbovePrice(t *testing.T) {
	book := NewBook("AAPL.XNAS", L2MBP)
	addLevels(book, []string{"100", "99", "98", "97"}, nil)
	if levels := book.BidsRangeDownTo(portPrice("98")); len(levels) != 3 {
		t.Fatalf("levels=%d", len(levels))
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:8399
//	test: test_asks_range_up_to_returns_levels_at_or_below_price
func TestBookAsksRangeUpToReturnsLevelsAtOrBelowPrice(t *testing.T) {
	book := NewBook("AAPL.XNAS", L2MBP)
	addLevels(book, nil, []string{"101", "102", "103", "104"})
	levels := book.AsksRangeUpTo(portPrice("103"))
	if len(levels) != 3 {
		t.Fatalf("levels=%d", len(levels))
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:8416
//	test: test_bids_range_down_to_empty_when_price_above_all_bids
func TestBookBidsRangeDownToEmptyWhenPriceAboveAllBids(t *testing.T) {
	book := NewBook("AAPL.XNAS", L2MBP)
	addLevels(book, []string{"100", "99"}, nil)
	if levels := book.BidsRangeDownTo(portPrice("101")); len(levels) != 0 {
		t.Fatalf("levels=%d", len(levels))
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:8427
//	test: test_asks_range_up_to_empty_when_price_below_all_asks
func TestBookAsksRangeUpToEmptyWhenPriceBelowAllAsks(t *testing.T) {
	book := NewBook("AAPL.XNAS", L2MBP)
	addLevels(book, nil, []string{"101", "102", "103"})
	levels := book.AsksRangeUpTo(portPrice("100"))
	if len(levels) != 0 {
		t.Fatalf("levels=%d", len(levels))
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:8436
//	test: test_bids_range_down_to_returns_all_at_lowest_bid
func TestBookBidsRangeDownToReturnsAllAtLowestBid(t *testing.T) {
	book := NewBook("AAPL.XNAS", L2MBP)
	addLevels(book, []string{"100", "99", "98"}, nil)
	if levels := book.BidsRangeDownTo(portPrice("98")); len(levels) != 3 {
		t.Fatalf("levels=%d", len(levels))
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:8448
//	test: test_asks_range_up_to_returns_all_at_highest_ask
func TestBookAsksRangeUpToReturnsAllAtHighestAsk(t *testing.T) {
	book := NewBook("AAPL.XNAS", L2MBP)
	addLevels(book, nil, []string{"101", "102", "103"})
	levels := book.AsksRangeUpTo(portPrice("103"))
	if len(levels) != 3 {
		t.Fatalf("levels=%d", len(levels))
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:8460
//	test: test_bids_range_down_to_single_exact_top
func TestBookBidsRangeDownToSingleExactTop(t *testing.T) {
	book := NewBook("AAPL.XNAS", L2MBP)
	addLevels(book, []string{"100", "99", "98"}, nil)
	levels := book.BidsRangeDownTo(portPrice("100"))
	if len(levels) != 1 || !levels[0].Price.Value.Equal(portPrice("100")) {
		t.Fatalf("levels=%v", levels)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:8474
//	test: test_asks_range_up_to_single_exact_bottom
func TestBookAsksRangeUpToSingleExactBottom(t *testing.T) {
	book := NewBook("AAPL.XNAS", L2MBP)
	addLevels(book, nil, []string{"101", "102", "103"})
	levels := book.AsksRangeUpTo(portPrice("101"))
	if len(levels) != 1 {
		t.Fatalf("levels=%d", len(levels))
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:8488
//	test: test_book_get_levels_for_price_buy_crosses_two_levels
func TestBookBookGetLevelsForPriceBuyCrossesTwoLevels(t *testing.T) {
	book := executionBook()
	levels := book.PriceLevelsForPrice(portPrice("2.010"), Buy, 1)
	if len(levels) != 2 {
		t.Fatalf("levels=%d", len(levels))
	}
	requireBookPortQuantity(t, levels[0].Size, true, "1.0")
	requireBookPortQuantity(t, levels[1].Size, true, "2.0")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:8526
//	test: test_book_get_levels_for_price_preserves_raw_size
func TestBookBookGetLevelsForPricePreservesRawSize(t *testing.T) {
	book := NewBook("AAPL.XNAS", L2MBP)
	size := decimal.MustQuantityFromRaw(big.NewInt(1_234_567_890_123_456_789), 8)
	book.Add(NewOrder(Sell, portPrice("101"), size, 1), 0, 1, 1)
	levels := book.PriceLevelsForPrice(portPrice("101"), Buy, 8)
	if len(levels) != 1 || !levels[0].Size.Equal(size) {
		t.Fatalf("levels=%v", levels)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:8542
//	test: test_book_get_levels_for_price_panics_on_raw_size_overflow
func TestBookBookGetLevelsForPricePanicsOnRawSizeOverflow(t *testing.T) {
	defer func() {
		value := recover()
		if value == nil || !strings.Contains(fmt.Sprint(value), "Overflow occurred when summing `BookLevel` raw size") {
			t.Fatalf("panic=%v", value)
		}
	}()
	book := NewBook("AAPL.XNAS", L3MBO)
	maxQuantity, err := decimal.MaxQuantity(0)
	if err != nil {
		t.Fatal(err)
	}
	book.Add(NewOrder(Sell, portPrice("101"), maxQuantity, 1), 0, 1, 1)
	book.Add(NewOrder(Sell, portPrice("101"), maxQuantity, 2), 0, 2, 2)
	book.PriceLevelsForPrice(portPrice("101"), Buy, 0)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:8556
//	test: test_book_get_levels_for_price_sell_crosses_two_levels
func TestBookBookGetLevelsForPriceSellCrossesTwoLevels(t *testing.T) {
	book := executionBook()
	levels := book.PriceLevelsForPrice(portPrice("0.990"), Sell, 1)
	if len(levels) != 2 {
		t.Fatalf("levels=%d", len(levels))
	}
	requireBookPortQuantity(t, levels[0].Size, true, "1.0")
	requireBookPortQuantity(t, levels[1].Size, true, "2.0")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:8594
//	test: test_book_get_levels_for_price_no_levels_crossed
func TestBookBookGetLevelsForPriceNoLevelsCrossed(t *testing.T) {
	book := executionBook()
	if got := book.LevelsForPrice(portPrice("1.999"), Buy); len(got) != 0 {
		t.Fatalf("levels=%v", got)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:8619
//	test: test_book_get_levels_for_price_all_levels_crossed
func TestBookBookGetLevelsForPriceAllLevelsCrossed(t *testing.T) {
	book := executionBook()
	buy := book.LevelsForPrice(portPrice("2.020"), Buy)
	sell := book.LevelsForPrice(portPrice("0.980"), Sell)
	if len(buy) != 3 || len(sell) != 3 {
		t.Fatalf("levels=%d/%d", len(buy), len(sell))
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:8658
//	test: test_book_get_levels_for_price_empty_book
func TestBookBookGetLevelsForPriceEmptyBook(t *testing.T) {
	book := NewBook("AAPL.XNAS", L2MBP)
	if got := book.LevelsForPrice(portPrice("100"), Buy); len(got) != 0 {
		t.Fatalf("levels=%v", got)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/tests.rs:8668
//	test: test_book_integrity_locked_market_is_valid
func TestBookBookIntegrityLockedMarketIsValid(t *testing.T) {
	book := NewBook("AAPL.XNAS", L2MBP)
	addLevels(book, []string{"100"}, []string{"100"})
	if err := book.Integrity(); err != nil {
		t.Fatal(err)
	}
}
