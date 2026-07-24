package market

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/upcomers-org/platformgo/internal/decimal"
)

func createDepthBookOrder(side OrderSide, price, size string, orderID uint64) BookOrder {
	return NewBookOrder(side, decimal.MustPrice(price), decimal.MustQuantity(size), orderID)
}

func createTestDepth10() OrderBookDepth10 {
	instrumentID := InstrumentID("EURUSD.SIM")
	bids := [Depth10Len]BookOrder{
		createDepthBookOrder(OrderSideBuy, "1.0500", "100000", 1),
		createDepthBookOrder(OrderSideBuy, "1.0499", "150000", 2),
		createDepthBookOrder(OrderSideBuy, "1.0498", "200000", 3),
		createDepthBookOrder(OrderSideBuy, "1.0497", "125000", 4),
		createDepthBookOrder(OrderSideBuy, "1.0496", "175000", 5),
		createDepthBookOrder(OrderSideBuy, "1.0495", "100000", 6),
		createDepthBookOrder(OrderSideBuy, "1.0494", "225000", 7),
		createDepthBookOrder(OrderSideBuy, "1.0493", "150000", 8),
		createDepthBookOrder(OrderSideBuy, "1.0492", "300000", 9),
		createDepthBookOrder(OrderSideBuy, "1.0491", "175000", 10),
	}
	asks := [Depth10Len]BookOrder{
		createDepthBookOrder(OrderSideSell, "1.0501", "100000", 11),
		createDepthBookOrder(OrderSideSell, "1.0502", "125000", 12),
		createDepthBookOrder(OrderSideSell, "1.0503", "150000", 13),
		createDepthBookOrder(OrderSideSell, "1.0504", "175000", 14),
		createDepthBookOrder(OrderSideSell, "1.0505", "200000", 15),
		createDepthBookOrder(OrderSideSell, "1.0506", "100000", 16),
		createDepthBookOrder(OrderSideSell, "1.0507", "250000", 17),
		createDepthBookOrder(OrderSideSell, "1.0508", "125000", 18),
		createDepthBookOrder(OrderSideSell, "1.0509", "300000", 19),
		createDepthBookOrder(OrderSideSell, "1.0510", "175000", 20),
	}
	bidCounts := [Depth10Len]uint32{1, 2, 1, 3, 1, 2, 1, 4, 1, 2}
	askCounts := [Depth10Len]uint32{2, 1, 3, 1, 2, 1, 4, 1, 2, 3}
	return NewOrderBookDepth10(
		instrumentID,
		bids,
		asks,
		bidCounts,
		askCounts,
		32,
		12345,
		1_000_000_000,
		2_000_000_000,
	)
}

func createEmptyDepth10() OrderBookDepth10 {
	emptyBid := createDepthBookOrder(OrderSideBuy, "0.0", "0", 0)
	emptyAsk := createDepthBookOrder(OrderSideSell, "0.0", "0", 0)
	var bids, asks [Depth10Len]BookOrder
	for i := range Depth10Len {
		bids[i] = emptyBid
		asks[i] = emptyAsk
	}
	return NewOrderBookDepth10(
		"EMPTY.TEST",
		bids,
		asks,
		[Depth10Len]uint32{},
		[Depth10Len]uint32{},
		0,
		0,
		0,
		0,
	)
}

func stubDepth10() OrderBookDepth10 {
	var bids, asks [Depth10Len]BookOrder
	var bidCounts, askCounts [Depth10Len]uint32
	for i := range Depth10Len {
		bids[i] = createDepthBookOrder(OrderSideBuy, fmt.Sprintf("%d.0", 99-i), "100", uint64(i+1))
		asks[i] = createDepthBookOrder(OrderSideSell, fmt.Sprintf("%d.0", 100+i), "100", uint64(i+11))
		bidCounts[i] = 1
		askCounts[i] = 1
	}
	return NewOrderBookDepth10("AAPL.XNAS", bids, asks, bidCounts, askCounts, 0, 0, 1, 2)
}

func depthRequirePrice(t *testing.T, got decimal.Price, want string) {
	t.Helper()
	if got.Decimal().Cmp(decimal.MustParse(want)) != 0 {
		t.Fatalf("price = %s, want %s", got, want)
	}
}

func depthRequireQuantity(t *testing.T, got decimal.Quantity, want string) {
	t.Helper()
	if got.Decimal().Cmp(decimal.MustParse(want)) != 0 {
		t.Fatalf("quantity = %s, want %s", got, want)
	}
}

func fieldByName(fields []Field, name string) (Field, bool) {
	for _, field := range fields {
		if field.Name == name {
			return field, true
		}
	}
	return Field{}, false
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/depth.rs:299
//	test: test_order_book_depth10_new
func TestOrderBookDepth10New(t *testing.T) {
	depth := createTestDepth10()
	if depth.InstrumentID != "EURUSD.SIM" {
		t.Fatalf("instrument ID = %q", depth.InstrumentID)
	}
	if len(depth.Bids) != Depth10Len || len(depth.Asks) != Depth10Len ||
		len(depth.BidCounts) != Depth10Len || len(depth.AskCounts) != Depth10Len {
		t.Fatalf("unexpected array sizes")
	}
	if depth.Flags != 32 || depth.Sequence != 12345 ||
		depth.TsEvent != 1_000_000_000 || depth.TsInit != 2_000_000_000 {
		t.Fatalf("unexpected metadata: %+v", depth)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/depth.rs:314
//	test: test_order_book_depth10_new_with_all_parameters
func TestOrderBookDepth10NewWithAllParameters(t *testing.T) {
	instrumentID := InstrumentID("GBPUSD.SIM")
	bid := createDepthBookOrder(OrderSideBuy, "1.2500", "50000", 1)
	ask := createDepthBookOrder(OrderSideSell, "1.2501", "75000", 2)
	flags := uint8(64)
	sequence := uint64(999)
	tsEvent := UnixNanos(5_000_000_000)
	tsInit := UnixNanos(6_000_000_000)
	var bids, asks [Depth10Len]BookOrder
	var bidCounts, askCounts [Depth10Len]uint32
	for i := range Depth10Len {
		bids[i], asks[i] = bid, ask
		bidCounts[i], askCounts[i] = 5, 3
	}
	depth := NewOrderBookDepth10(instrumentID, bids, asks, bidCounts, askCounts, flags, sequence, tsEvent, tsInit)
	if depth.InstrumentID != instrumentID || !bookOrdersEqual(depth.Bids[0], bid) ||
		!bookOrdersEqual(depth.Asks[0], ask) || depth.BidCounts[0] != 5 ||
		depth.AskCounts[0] != 3 || depth.Flags != flags ||
		depth.Sequence != sequence || depth.TsEvent != tsEvent || depth.TsInit != tsInit {
		t.Fatalf("constructor did not preserve all parameters: %+v", depth)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/depth.rs:347
//	test: test_order_book_depth10_fixed_array_sizes
func TestOrderBookDepth10FixedArraySizes(t *testing.T) {
	depth := createTestDepth10()
	if len(depth.Bids) != 10 || len(depth.Asks) != 10 ||
		len(depth.BidCounts) != 10 || len(depth.AskCounts) != 10 {
		t.Fatalf("unexpected fixed sizes")
	}
	if Depth10Len != 10 {
		t.Fatalf("Depth10Len = %d", Depth10Len)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/depth.rs:361
//	test: test_order_book_depth10_array_indexing
func TestOrderBookDepth10ArrayIndexing(t *testing.T) {
	depth := createTestDepth10()
	depthRequirePrice(t, depth.Bids[0].Price, "1.0500")
	depthRequirePrice(t, depth.Bids[9].Price, "1.0491")
	depthRequirePrice(t, depth.Asks[0].Price, "1.0501")
	depthRequirePrice(t, depth.Asks[9].Price, "1.0510")
	if depth.BidCounts[0] != 1 || depth.BidCounts[9] != 2 ||
		depth.AskCounts[0] != 2 || depth.AskCounts[9] != 3 {
		t.Fatalf("unexpected edge counts")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/depth.rs:376
//	test: test_order_book_depth10_bid_ask_ordering
func TestOrderBookDepth10BidAskOrdering(t *testing.T) {
	depth := createTestDepth10()
	for i := 0; i < 9; i++ {
		if depth.Bids[i].Price.Decimal().Cmp(depth.Bids[i+1].Price.Decimal()) < 0 {
			t.Fatalf("bid prices are not descending: %s < %s", depth.Bids[i].Price, depth.Bids[i+1].Price)
		}
	}
	for i := 0; i < 9; i++ {
		if depth.Asks[i].Price.Decimal().Cmp(depth.Asks[i+1].Price.Decimal()) > 0 {
			t.Fatalf("ask prices are not ascending: %s > %s", depth.Asks[i].Price, depth.Asks[i+1].Price)
		}
	}
	if depth.Bids[0].Price.Decimal().Cmp(depth.Asks[0].Price.Decimal()) >= 0 {
		t.Fatalf("best bid %s is not below best ask %s", depth.Bids[0].Price, depth.Asks[0].Price)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/depth.rs:409
//	test: test_order_book_depth10_clone
func TestOrderBookDepth10Clone(t *testing.T) {
	depth1 := createTestDepth10()
	depth2 := depth1
	if depth1.InstrumentID != depth2.InstrumentID ||
		depth1.BidCounts != depth2.BidCounts || depth1.AskCounts != depth2.AskCounts ||
		depth1.Flags != depth2.Flags || depth1.Sequence != depth2.Sequence ||
		depth1.TsEvent != depth2.TsEvent || depth1.TsInit != depth2.TsInit {
		t.Fatalf("copy differs: %+v != %+v", depth1, depth2)
	}
	for i := range Depth10Len {
		if !bookOrdersEqual(depth1.Bids[i], depth2.Bids[i]) ||
			!bookOrdersEqual(depth1.Asks[i], depth2.Asks[i]) {
			t.Fatalf("order[%d] differs", i)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/depth.rs:425
//	test: test_order_book_depth10_copy
func TestOrderBookDepth10Copy(t *testing.T) {
	depth1 := createTestDepth10()
	depth2 := depth1
	if !depth1.Equal(depth2) {
		t.Fatalf("value copy differs: %+v != %+v", depth1, depth2)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/depth.rs:435
//	test: test_order_book_depth10_debug
func TestOrderBookDepth10Debug(t *testing.T) {
	depth := createTestDepth10()
	debug := fmt.Sprintf("%#v", depth)
	for _, want := range []string{"OrderBookDepth10", "EURUSD.SIM", "flags: 32", "sequence: 12345"} {
		if !strings.Contains(debug, want) {
			t.Fatalf("%q does not contain %q", debug, want)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/depth.rs:446
//	test: test_order_book_depth10_partial_eq
func TestOrderBookDepth10PartialEq(t *testing.T) {
	depth1 := createTestDepth10()
	depth2 := createTestDepth10()
	depth3 := createEmptyDepth10()
	if !depth1.Equal(depth2) || depth1.Equal(depth3) || depth2.Equal(depth3) {
		t.Fatal("partial equality semantics differ")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/depth.rs:457
//	test: test_order_book_depth10_eq_consistency
func TestOrderBookDepth10EqConsistency(t *testing.T) {
	depth1 := createTestDepth10()
	depth2 := createTestDepth10()
	if !depth1.Equal(depth2) || !depth2.Equal(depth1) || !depth1.Equal(depth1) {
		t.Fatal("equality is not symmetric and reflexive")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/depth.rs:467
//	test: test_order_book_depth10_hash
func TestOrderBookDepth10Hash(t *testing.T) {
	depth1 := createTestDepth10()
	depth2 := createTestDepth10()
	if depth1.Hash() != depth2.Hash() {
		t.Fatalf("equal depths have unequal hashes: %d != %d", depth1.Hash(), depth2.Hash())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/depth.rs:481
//	test: test_order_book_depth10_hash_different_objects
func TestOrderBookDepth10HashDifferentObjects(t *testing.T) {
	depth1 := createTestDepth10()
	depth2 := createEmptyDepth10()
	if depth1.Hash() == depth2.Hash() {
		t.Fatalf("different depths have equal hash: %d", depth1.Hash())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/depth.rs:495
//	test: test_order_book_depth10_display
func TestOrderBookDepth10Display(t *testing.T) {
	display := createTestDepth10().String()
	for _, want := range []string{
		"EURUSD.SIM",
		"flags=32",
		"sequence=12345",
		"ts_event=1000000000",
		"ts_init=2000000000",
	} {
		if !strings.Contains(display, want) {
			t.Fatalf("%q does not contain %q", display, want)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/depth.rs:507
//	test: test_order_book_depth10_display_format
func TestOrderBookDepth10DisplayFormat(t *testing.T) {
	const want = "EURUSD.SIM,flags=32,sequence=12345,ts_event=1000000000,ts_init=2000000000"
	if got := createTestDepth10().String(); got != want {
		t.Fatalf("display = %q, want %q", got, want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/depth.rs:515
//	test: test_order_book_depth10_serialization
func TestOrderBookDepth10Serialization(t *testing.T) {
	depth := createTestDepth10()
	data, err := json.Marshal(depth)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var deserialized OrderBookDepth10
	if err := json.Unmarshal(data, &deserialized); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !depth.Equal(deserialized) {
		t.Fatalf("round-trip differs: %+v != %+v", depth, deserialized)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/depth.rs:526
//	test: test_order_book_depth10_serializable_trait
func TestOrderBookDepth10SerializableTrait(t *testing.T) {
	assertSerializable := func(value DepthSerializable) {
		if value == nil {
			t.Fatal("serializable value is nil")
		}
	}
	depth := createTestDepth10()
	assertSerializable(depth)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/depth.rs:536
//	test: test_order_book_depth10_has_ts_init
func TestOrderBookDepth10HasTsInit(t *testing.T) {
	depth := createTestDepth10()
	if depth.TsInit != 2_000_000_000 {
		t.Fatalf("ts_init = %d", depth.TsInit)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/depth.rs:543
//	test: test_order_book_depth10_get_metadata
func TestOrderBookDepth10GetMetadata(t *testing.T) {
	instrumentID := InstrumentID("EURUSD.SIM")
	pricePrecision := uint8(5)
	sizePrecision := uint8(0)
	metadata := OrderBookDepth10Metadata(instrumentID, pricePrecision, sizePrecision)
	if metadata["instrument_id"] != "EURUSD.SIM" ||
		metadata["price_precision"] != "5" ||
		metadata["size_precision"] != "0" ||
		len(metadata) != 3 {
		t.Fatalf("unexpected metadata: %#v", metadata)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/depth.rs:561
//	test: test_order_book_depth10_get_fields
func TestOrderBookDepth10GetFields(t *testing.T) {
	fields := OrderBookDepth10Fields()
	for i := range Depth10Len {
		for _, name := range []string{
			fmt.Sprintf("bid_price_%d", i),
			fmt.Sprintf("ask_price_%d", i),
			fmt.Sprintf("bid_size_%d", i),
			fmt.Sprintf("ask_size_%d", i),
		} {
			field, ok := fieldByName(fields, name)
			if !ok || field.Type != fixedSizeBinary {
				t.Fatalf("field %q = %#v, present=%v", name, field, ok)
			}
		}
		for _, name := range []string{
			fmt.Sprintf("bid_count_%d", i),
			fmt.Sprintf("ask_count_%d", i),
		} {
			field, ok := fieldByName(fields, name)
			if !ok || field.Type != "UInt32" {
				t.Fatalf("field %q = %#v, present=%v", name, field, ok)
			}
		}
	}
	for name, fieldType := range map[string]string{
		"flags": "UInt8", "sequence": "UInt64", "ts_event": "UInt64", "ts_init": "UInt64",
	} {
		field, ok := fieldByName(fields, name)
		if !ok || field.Type != fieldType {
			t.Fatalf("field %q = %#v, present=%v", name, field, ok)
		}
	}
	if len(fields) != 64 {
		t.Fatalf("field count = %d", len(fields))
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/depth.rs:612
//	test: test_order_book_depth10_get_fields_order
func TestOrderBookDepth10GetFieldsOrder(t *testing.T) {
	fields := OrderBookDepth10Fields()
	want := map[int]string{
		0: "bid_price_0", 9: "bid_price_9",
		10: "ask_price_0", 19: "ask_price_9",
		20: "bid_size_0", 29: "bid_size_9",
		30: "ask_size_0", 39: "ask_size_9",
		40: "bid_count_0", 41: "bid_count_1",
	}
	for index, name := range want {
		if fields[index].Name != name {
			t.Fatalf("field[%d] = %q, want %q", index, fields[index].Name, name)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/depth.rs:630
//	test: test_order_book_depth10_empty_values
func TestOrderBookDepth10EmptyValues(t *testing.T) {
	depth := createEmptyDepth10()
	if depth.InstrumentID != "EMPTY.TEST" || depth.Flags != 0 ||
		depth.Sequence != 0 || depth.TsEvent != 0 || depth.TsInit != 0 {
		t.Fatalf("unexpected empty metadata: %+v", depth)
	}
	for _, bid := range depth.Bids {
		depthRequirePrice(t, bid.Price, "0.0")
		depthRequireQuantity(t, bid.Size, "0")
		if bid.OrderID != 0 {
			t.Fatalf("bid order ID = %d", bid.OrderID)
		}
	}
	for _, ask := range depth.Asks {
		depthRequirePrice(t, ask.Price, "0.0")
		depthRequireQuantity(t, ask.Size, "0")
		if ask.OrderID != 0 {
			t.Fatalf("ask order ID = %d", ask.OrderID)
		}
	}
	for _, count := range depth.BidCounts {
		if count != 0 {
			t.Fatalf("bid count = %d", count)
		}
	}
	for _, count := range depth.AskCounts {
		if count != 0 {
			t.Fatalf("ask count = %d", count)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/depth.rs:663
//	test: test_order_book_depth10_max_values
func TestOrderBookDepth10MaxValues(t *testing.T) {
	maxBid := createDepthBookOrder(OrderSideBuy, "999999.99", "999999999", math.MaxUint64)
	maxAsk := createDepthBookOrder(OrderSideSell, "1000000.00", "999999999", math.MaxUint64)
	var bids, asks [Depth10Len]BookOrder
	var bidCounts, askCounts [Depth10Len]uint32
	for i := range Depth10Len {
		bids[i], asks[i] = maxBid, maxAsk
		bidCounts[i], askCounts[i] = math.MaxUint32, math.MaxUint32
	}
	depth := NewOrderBookDepth10(
		"MAX.TEST",
		bids,
		asks,
		bidCounts,
		askCounts,
		math.MaxUint8,
		math.MaxUint64,
		UnixNanos(math.MaxUint64),
		UnixNanos(math.MaxUint64),
	)
	if depth.Flags != math.MaxUint8 || depth.Sequence != math.MaxUint64 ||
		depth.TsEvent != UnixNanos(math.MaxUint64) || depth.TsInit != UnixNanos(math.MaxUint64) {
		t.Fatalf("unexpected maximum metadata: %+v", depth)
	}
	for _, count := range depth.BidCounts {
		if count != math.MaxUint32 {
			t.Fatalf("bid count = %d", count)
		}
	}
	for _, count := range depth.AskCounts {
		if count != math.MaxUint32 {
			t.Fatalf("ask count = %d", count)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/depth.rs:695
//	test: test_order_book_depth10_different_instruments
func TestOrderBookDepth10DifferentInstruments(t *testing.T) {
	instruments := []InstrumentID{"EURUSD.SIM", "GBPUSD.SIM", "USDJPY.SIM", "AUDUSD.SIM", "USDCHF.SIM"}
	bid := createDepthBookOrder(OrderSideBuy, "1.0000", "100000", 1)
	ask := createDepthBookOrder(OrderSideSell, "1.0001", "100000", 2)
	var bids, asks [Depth10Len]BookOrder
	var counts [Depth10Len]uint32
	for i := range Depth10Len {
		bids[i], asks[i], counts[i] = bid, ask, 1
	}
	for _, instrumentID := range instruments {
		depth := NewOrderBookDepth10(instrumentID, bids, asks, counts, counts, 0, 1, 1_000_000_000, 2_000_000_000)
		if depth.InstrumentID != instrumentID || !strings.Contains(depth.String(), string(instrumentID)) {
			t.Fatalf("instrument %q was not preserved: %+v", instrumentID, depth)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/depth.rs:727
//	test: test_order_book_depth10_realistic_forex_spread
func TestOrderBookDepth10RealisticForexSpread(t *testing.T) {
	bestBid := createDepthBookOrder(OrderSideBuy, "1.08500", "1000000", 1)
	bestAsk := createDepthBookOrder(OrderSideSell, "1.08501", "1000000", 2)
	var bids, asks [Depth10Len]BookOrder
	var bidCounts, askCounts [Depth10Len]uint32
	for i := range Depth10Len {
		bids[i], asks[i] = bestBid, bestAsk
		bidCounts[i], askCounts[i] = 5, 3
	}
	depth := NewOrderBookDepth10(
		"EURUSD.SIM",
		bids,
		asks,
		bidCounts,
		askCounts,
		16,
		123_456,
		1_672_531_200_000_000_000,
		1_672_531_200_000_100_000,
	)
	depthRequirePrice(t, depth.Bids[0].Price, "1.08500")
	depthRequirePrice(t, depth.Asks[0].Price, "1.08501")
	if depth.Bids[0].Price.Decimal().Cmp(depth.Asks[0].Price.Decimal()) >= 0 {
		t.Fatal("spread is not positive")
	}
	depthRequireQuantity(t, depth.Bids[0].Size, "1000000")
	if depth.BidCounts[0] != 5 || depth.AskCounts[0] != 3 {
		t.Fatalf("counts = %d/%d", depth.BidCounts[0], depth.AskCounts[0])
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/depth.rs:757
//	test: test_order_book_depth10_with_stub
func TestOrderBookDepth10WithStub(t *testing.T) {
	depth := stubDepth10()
	if depth.InstrumentID != "AAPL.XNAS" || len(depth.Bids) != 10 || len(depth.Asks) != 10 {
		t.Fatalf("unexpected stub identity or sizes: %+v", depth)
	}
	depthRequirePrice(t, depth.Asks[9].Price, "109.0")
	depthRequirePrice(t, depth.Asks[0].Price, "100.0")
	depthRequirePrice(t, depth.Bids[0].Price, "99.0")
	depthRequirePrice(t, depth.Bids[9].Price, "90.0")
	if len(depth.BidCounts) != 10 || len(depth.AskCounts) != 10 ||
		depth.BidCounts[0] != 1 || depth.AskCounts[0] != 1 ||
		depth.Flags != 0 || depth.Sequence != 0 || depth.TsEvent != 1 || depth.TsInit != 2 {
		t.Fatalf("unexpected stub metadata: %+v", depth)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/depth.rs:778
//	test: test_new
func TestDepthNew(t *testing.T) {
	depth := stubDepth10()
	instrumentID := InstrumentID("AAPL.XNAS")
	flags := uint8(0)
	sequence := uint64(0)
	tsEvent := UnixNanos(1)
	tsInit := UnixNanos(2)
	if depth.InstrumentID != instrumentID || len(depth.Bids) != 10 || len(depth.Asks) != 10 {
		t.Fatalf("unexpected identity or sizes: %+v", depth)
	}
	depthRequirePrice(t, depth.Asks[9].Price, "109.0")
	depthRequirePrice(t, depth.Asks[0].Price, "100.0")
	depthRequirePrice(t, depth.Bids[0].Price, "99.0")
	depthRequirePrice(t, depth.Bids[9].Price, "90.0")
	if len(depth.BidCounts) != 10 || len(depth.AskCounts) != 10 ||
		depth.BidCounts[0] != 1 || depth.AskCounts[0] != 1 ||
		depth.Flags != flags || depth.Sequence != sequence ||
		depth.TsEvent != tsEvent || depth.TsInit != tsInit {
		t.Fatalf("unexpected stub metadata: %+v", depth)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/depth.rs:804
//	test: test_display
func TestDepthDisplay(t *testing.T) {
	const want = "AAPL.XNAS,flags=0,sequence=0,ts_event=1,ts_init=2"
	if got := stubDepth10().String(); got != want {
		t.Fatalf("display = %q, want %q", got, want)
	}
}
