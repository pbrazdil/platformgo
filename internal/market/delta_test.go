package market

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/upcomers-org/platformgo/internal/decimal"
)

func createSingleTestDelta() OrderBookDelta {
	order := NewBookOrder(
		OrderSideBuy,
		decimal.MustPrice("1.0500"),
		decimal.MustQuantity("100000"),
		12345,
	)
	return MustNewOrderBookDelta(
		"EURUSD.SIM",
		BookActionAdd,
		order,
		0,
		123,
		1_000_000_000,
		2_000_000_000,
	)
}

func deltaFieldByName(fields []Field, name string) (Field, bool) {
	for _, field := range fields {
		if field.Name == name {
			return field, true
		}
	}
	return Field{}, false
}

func deltaRequirePrice(t *testing.T, got decimal.Price, want string) {
	t.Helper()
	if got.Decimal().Cmp(decimal.MustParse(want)) != 0 {
		t.Fatalf("price = %s, want %s", got, want)
	}
}

func deltaRequireQuantity(t *testing.T, got decimal.Quantity, want string) {
	t.Helper()
	if got.Decimal().Cmp(decimal.MustParse(want)) != 0 {
		t.Fatalf("quantity = %s, want %s", got, want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/delta.rs:240
//	test: test_order_book_delta_new
func TestOrderBookDeltaNew(t *testing.T) {
	delta := createSingleTestDelta()
	if delta.InstrumentID != "EURUSD.SIM" || delta.Action != BookActionAdd ||
		delta.Order.Side != OrderSideBuy {
		t.Fatalf("unexpected identity/action/side: %+v", delta)
	}
	deltaRequirePrice(t, delta.Order.Price, "1.0500")
	deltaRequireQuantity(t, delta.Order.Size, "100000")
	if delta.Order.OrderID != 12345 || delta.Flags != 0 || delta.Sequence != 123 ||
		delta.TsEvent != 1_000_000_000 || delta.TsInit != 2_000_000_000 {
		t.Fatalf("unexpected order or metadata: %+v", delta)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/delta.rs:256
//	test: test_order_book_delta_new_checked_valid
func TestOrderBookDeltaNewCheckedValid(t *testing.T) {
	order := NewBookOrder(
		OrderSideSell,
		decimal.MustPrice("1.0505"),
		decimal.MustQuantity("50000"),
		67890,
	)
	delta, err := NewOrderBookDeltaChecked(
		"GBPUSD.SIM",
		BookActionUpdate,
		order,
		16,
		456,
		500_000_000,
		1_500_000_000,
	)
	if err != nil {
		t.Fatalf("new checked: %v", err)
	}
	if delta.InstrumentID != "GBPUSD.SIM" || delta.Action != BookActionUpdate ||
		delta.Order.Side != OrderSideSell || delta.Flags != 16 {
		t.Fatalf("unexpected checked delta: %+v", delta)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/delta.rs:283
//	test: test_order_book_delta_new_with_zero_size_panics
func TestOrderBookDeltaNewWithZeroSizePanics(t *testing.T) {
	instrumentID := InstrumentID("AAPL.XNAS")
	action := BookActionAdd
	price := decimal.MustPrice("100.00")
	zeroSize := decimal.MustQuantity("0")
	side := OrderSideBuy
	orderID := uint64(123_456)
	flags := uint8(0)
	sequence := uint64(1)
	tsEvent := UnixNanos(0)
	tsInit := UnixNanos(1)
	order := NewBookOrder(side, price, zeroSize, orderID)

	defer func() {
		recovered := recover()
		const want = "invalid `Quantity` for 'order.size' not positive, was 0"
		if recovered == nil || !strings.Contains(fmt.Sprint(recovered), want) {
			t.Fatalf("panic = %v, want text %q", recovered, want)
		}
	}()
	_ = MustNewOrderBookDelta(instrumentID, action, order, flags, sequence, tsEvent, tsInit)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/delta.rs:309
//	test: test_order_book_delta_new_checked_with_zero_size_error
func TestOrderBookDeltaNewCheckedWithZeroSizeError(t *testing.T) {
	instrumentID := InstrumentID("AAPL.XNAS")
	action := BookActionAdd
	price := decimal.MustPrice("100.00")
	zeroSize := decimal.MustQuantity("0")
	side := OrderSideBuy
	orderID := uint64(123_456)
	flags := uint8(0)
	sequence := uint64(1)
	tsEvent := UnixNanos(0)
	tsInit := UnixNanos(1)
	order := NewBookOrder(side, price, zeroSize, orderID)

	_, err := NewOrderBookDeltaChecked(instrumentID, action, order, flags, sequence, tsEvent, tsInit)
	if err == nil {
		t.Fatal("expected zero-size validation error")
	}
	if !strings.Contains(err.Error(), "invalid `Quantity` for 'order.size' not positive") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/delta.rs:343
//	test: test_order_book_delta_new_checked_delete_with_zero_size_ok
func TestOrderBookDeltaNewCheckedDeleteWithZeroSizeOK(t *testing.T) {
	order := NewBookOrder(
		OrderSideBuy,
		decimal.MustPrice("100.00"),
		decimal.MustQuantity("0"),
		123_456,
	)
	_, err := NewOrderBookDeltaChecked(
		"TEST.SIM",
		BookActionDelete,
		order,
		0,
		1,
		0,
		1,
	)
	if err != nil {
		t.Fatalf("delete with zero size: %v", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/delta.rs:364
//	test: test_order_book_delta_clear
func TestOrderBookDeltaClear(t *testing.T) {
	instrumentID := InstrumentID("BTCUSD.CRYPTO")
	sequence := uint64(999)
	tsEvent := UnixNanos(3_000_000_000)
	tsInit := UnixNanos(4_000_000_000)
	delta := NewClearOrderBookDelta(instrumentID, sequence, tsEvent, tsInit)
	if delta.InstrumentID != instrumentID || delta.Action != BookActionClear ||
		!delta.Order.Price.IsZero() || !delta.Order.Size.IsZero() ||
		delta.Order.Side != OrderSideNoOrderSide || delta.Order.OrderID != 0 ||
		delta.Flags != RecordFlagSnapshot || delta.Sequence != sequence ||
		delta.TsEvent != tsEvent || delta.TsInit != tsInit {
		t.Fatalf("unexpected clear delta: %+v", delta)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/delta.rs:385
//	test: test_get_metadata
func TestOrderBookDeltaGetMetadata(t *testing.T) {
	instrumentID := InstrumentID("EURUSD.SIM")
	metadata := OrderBookDeltaMetadata(instrumentID, 5, 8)
	if len(metadata) != 3 || metadata["instrument_id"] != "EURUSD.SIM" ||
		metadata["price_precision"] != "5" || metadata["size_precision"] != "8" {
		t.Fatalf("unexpected metadata: %#v", metadata)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/delta.rs:399
//	test: test_get_fields
func TestOrderBookDeltaGetFields(t *testing.T) {
	fields := OrderBookDeltaFields()
	want := map[string]string{
		"action": "UInt8", "side": "UInt8",
		"price": fixedSizeBinary, "size": fixedSizeBinary,
		"order_id": "UInt64", "flags": "UInt8", "sequence": "UInt64",
		"ts_event": "UInt64", "ts_init": "UInt64",
	}
	if len(fields) != 9 {
		t.Fatalf("field count = %d", len(fields))
	}
	for name, fieldType := range want {
		field, ok := deltaFieldByName(fields, name)
		if !ok || field.Type != fieldType {
			t.Fatalf("field %q = %#v, present=%v", name, field, ok)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/delta.rs:432
//	test: test_order_book_delta_with_different_actions
func TestOrderBookDeltaWithDifferentActions(t *testing.T) {
	for _, action := range []BookAction{
		BookActionAdd,
		BookActionUpdate,
		BookActionDelete,
		BookActionClear,
	} {
		size := "1000"
		if action == BookActionDelete || action == BookActionClear {
			size = "0"
		}
		order := NewBookOrder(
			OrderSideBuy,
			decimal.MustPrice("100.00"),
			decimal.MustQuantity(size),
			123_456,
		)
		var (
			delta OrderBookDelta
			err   error
		)
		if action == BookActionClear {
			delta = NewClearOrderBookDelta("TEST.SIM", 1, 1_000_000_000, 2_000_000_000)
		} else {
			delta, err = NewOrderBookDeltaChecked(
				"TEST.SIM",
				action,
				order,
				0,
				1,
				1_000_000_000,
				2_000_000_000,
			)
		}
		if err != nil {
			t.Fatalf("%s delta: %v", action, err)
		}
		if delta.Action != action {
			t.Fatalf("action = %s, want %s", delta.Action, action)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/delta.rs:471
//	test: test_order_book_delta_with_different_sides
func TestOrderBookDeltaWithDifferentSides(t *testing.T) {
	for _, side := range []OrderSide{OrderSideBuy, OrderSideSell} {
		order := NewBookOrder(
			side,
			decimal.MustPrice("100.00"),
			decimal.MustQuantity("1000"),
			123_456,
		)
		delta := MustNewOrderBookDelta(
			"TEST.SIM",
			BookActionAdd,
			order,
			0,
			1,
			1_000_000_000,
			2_000_000_000,
		)
		if delta.Order.Side != side {
			t.Fatalf("side = %s, want %s", delta.Order.Side, side)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/delta.rs:488
//	test: test_order_book_delta_has_ts_init
func TestOrderBookDeltaHasTsInit(t *testing.T) {
	delta := createSingleTestDelta()
	if delta.TsInit != 2_000_000_000 {
		t.Fatalf("ts_init = %d", delta.TsInit)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/delta.rs:494
//	test: test_order_book_delta_display
func TestOrderBookDeltaDisplay(t *testing.T) {
	display := createSingleTestDelta().String()
	for _, want := range []string{
		"EURUSD.SIM",
		"ADD",
		"BUY",
		"1.0500",
		"100000",
		"12345",
		"123",
	} {
		if !strings.Contains(display, want) {
			t.Fatalf("%q does not contain %q", display, want)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/delta.rs:508
//	test: test_order_book_delta_with_zero_timestamps
func TestOrderBookDeltaWithZeroTimestamps(t *testing.T) {
	order := NewBookOrder(
		OrderSideBuy,
		decimal.MustPrice("100.00"),
		decimal.MustQuantity("1000"),
		123_456,
	)
	delta := MustNewOrderBookDelta("TEST.SIM", BookActionAdd, order, 0, 0, 0, 0)
	if delta.Sequence != 0 || delta.TsEvent != 0 || delta.TsInit != 0 {
		t.Fatalf("unexpected zero metadata: %+v", delta)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/delta.rs:531
//	test: test_order_book_delta_with_max_values
func TestOrderBookDeltaWithMaxValues(t *testing.T) {
	order := NewBookOrder(
		OrderSideSell,
		decimal.MustPrice("999999.9999"),
		decimal.MustQuantity("999999999.9999"),
		math.MaxUint64,
	)
	delta := MustNewOrderBookDelta(
		"TEST.SIM",
		BookActionUpdate,
		order,
		math.MaxUint8,
		math.MaxUint64,
		UnixNanos(math.MaxUint64),
		UnixNanos(math.MaxUint64),
	)
	if delta.Flags != math.MaxUint8 || delta.Sequence != math.MaxUint64 ||
		delta.Order.OrderID != math.MaxUint64 ||
		delta.TsEvent != UnixNanos(math.MaxUint64) ||
		delta.TsInit != UnixNanos(math.MaxUint64) {
		t.Fatalf("unexpected maximum delta: %+v", delta)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/delta.rs:556
//	test: test_new
func TestDeltaNew(t *testing.T) {
	instrumentID := InstrumentID("AAPL.XNAS")
	action := BookActionAdd
	price := decimal.MustPrice("100.00")
	size := decimal.MustQuantity("10")
	side := OrderSideBuy
	orderID := uint64(123_456)
	flags := uint8(0)
	sequence := uint64(1)
	tsEvent := UnixNanos(1)
	tsInit := UnixNanos(2)
	order := NewBookOrder(side, price, size, orderID)
	delta := MustNewOrderBookDelta(instrumentID, action, order, flags, sequence, tsEvent, tsInit)
	if delta.InstrumentID != instrumentID || delta.Action != action ||
		delta.Order.Side != side || delta.Order.OrderID != orderID ||
		delta.Flags != flags || delta.Sequence != sequence ||
		delta.TsEvent != tsEvent || delta.TsInit != tsInit {
		t.Fatalf("constructor did not preserve values: %+v", delta)
	}
	deltaRequirePrice(t, delta.Order.Price, "100.00")
	deltaRequireQuantity(t, delta.Order.Size, "10")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/delta.rs:593
//	test: test_clear
func TestDeltaClear(t *testing.T) {
	instrumentID := InstrumentID("AAPL.XNAS")
	sequence := uint64(1)
	tsEvent := UnixNanos(2)
	tsInit := UnixNanos(3)
	delta := NewClearOrderBookDelta(instrumentID, sequence, tsEvent, tsInit)
	if delta.InstrumentID != instrumentID || delta.Action != BookActionClear ||
		!delta.Order.Price.IsZero() || !delta.Order.Size.IsZero() ||
		delta.Order.Side != OrderSideNoOrderSide || delta.Order.OrderID != 0 ||
		delta.Flags != 32 || delta.Sequence != sequence ||
		delta.TsEvent != tsEvent || delta.TsInit != tsInit {
		t.Fatalf("unexpected clear delta: %+v", delta)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/delta.rs:614
//	test: test_order_book_delta_hash
func TestOrderBookDeltaHash(t *testing.T) {
	delta1 := createSingleTestDelta()
	delta2 := createSingleTestDelta()
	if delta1.Hash() != delta2.Hash() {
		t.Fatalf("equal deltas have unequal hashes: %d != %d", delta1.Hash(), delta2.Hash())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/delta.rs:628
//	test: test_order_book_delta_hash_different_deltas
func TestOrderBookDeltaHashDifferentDeltas(t *testing.T) {
	delta1 := createSingleTestDelta()
	order2 := NewBookOrder(
		OrderSideSell,
		decimal.MustPrice("1.0505"),
		decimal.MustQuantity("50000"),
		67890,
	)
	delta2 := MustNewOrderBookDelta(
		"EURUSD.SIM",
		BookActionAdd,
		order2,
		0,
		123,
		1_000_000_000,
		2_000_000_000,
	)
	if delta1.Hash() == delta2.Hash() {
		t.Fatalf("different deltas have equal hash: %d", delta1.Hash())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/delta.rs:656
//	test: test_order_book_delta_partial_eq
func TestOrderBookDeltaPartialEq(t *testing.T) {
	delta1 := createSingleTestDelta()
	delta2 := createSingleTestDelta()
	if !delta1.Equal(delta2) {
		t.Fatal("equal deltas compare unequal")
	}
	order3 := NewBookOrder(
		OrderSideBuy,
		decimal.MustPrice("1.0500"),
		decimal.MustQuantity("100000"),
		12345,
	)
	delta3 := MustNewOrderBookDelta(
		"GBPUSD.SIM",
		BookActionAdd,
		order3,
		0,
		123,
		1_000_000_000,
		2_000_000_000,
	)
	if delta1.Equal(delta3) {
		t.Fatal("deltas with different instruments compare equal")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/delta.rs:684
//	test: test_order_book_delta_clone
func TestOrderBookDeltaClone(t *testing.T) {
	delta1 := createSingleTestDelta()
	delta2 := delta1
	if !delta1.Equal(delta2) || delta1.InstrumentID != delta2.InstrumentID ||
		delta1.Action != delta2.Action || !delta1.Order.Equal(delta2.Order) ||
		delta1.Flags != delta2.Flags || delta1.Sequence != delta2.Sequence ||
		delta1.TsEvent != delta2.TsEvent || delta1.TsInit != delta2.TsInit {
		t.Fatalf("copy differs: %+v != %+v", delta1, delta2)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/delta.rs:699
//	test: test_order_book_delta_debug
func TestOrderBookDeltaDebug(t *testing.T) {
	debug := fmt.Sprintf("%#v", createSingleTestDelta())
	for _, want := range []string{"OrderBookDelta", "EURUSD.SIM", "Add", "BUY", "1.0500"} {
		if !strings.Contains(debug, want) {
			t.Fatalf("%q does not contain %q", debug, want)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/delta.rs:711
//	test: test_order_book_delta_serialization
func TestOrderBookDeltaSerialization(t *testing.T) {
	delta := createSingleTestDelta()
	data, err := json.Marshal(delta)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var deserialized OrderBookDelta
	if err := json.Unmarshal(data, &deserialized); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !delta.Equal(deserialized) {
		t.Fatalf("round-trip differs: %+v != %+v", delta, deserialized)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/delta.rs:721
//	test: test_json_serialization
func TestOrderBookDeltaJSONSerialization(t *testing.T) {
	delta := createSingleTestDelta()
	serialized, err := json.Marshal(delta)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var deserialized OrderBookDelta
	if err := json.Unmarshal(serialized, &deserialized); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !deserialized.Equal(delta) {
		t.Fatalf("round-trip differs: %+v != %+v", deserialized, delta)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/delta.rs:729
//	test: test_msgpack_serialization
//
// Adaptation: Rust MessagePack is replaced by a deterministic native Go binary codec.
func TestOrderBookDeltaMsgpackSerialization(t *testing.T) {
	delta := createSingleTestDelta()
	serialized, err := delta.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal binary: %v", err)
	}
	var deserialized OrderBookDelta
	if err := deserialized.UnmarshalBinary(serialized); err != nil {
		t.Fatalf("unmarshal binary: %v", err)
	}
	if !deserialized.Equal(delta) {
		t.Fatalf("round-trip differs: %+v != %+v", deserialized, delta)
	}
}
