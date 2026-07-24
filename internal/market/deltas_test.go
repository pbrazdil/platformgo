package market

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"math"
	"strings"
	"testing"

	"github.com/upcomers-org/platformgo/internal/decimal"
)

func createTestDelta() OrderBookDelta {
	return NewOrderBookDelta(
		"EURUSD.SIM",
		BookActionAdd,
		NewBookOrder(
			OrderSideBuy,
			decimal.MustPrice("1.0500"),
			decimal.MustQuantity("100000"),
			1,
		),
		0,
		123,
		1_000_000_000,
		2_000_000_000,
	)
}

func createTestDeltas() OrderBookDeltas {
	instrumentID := InstrumentID("EURUSD.SIM")
	flags := uint8(32)
	sequence := uint64(123)
	tsEvent := UnixNanos(1_000_000_000)
	tsInit := UnixNanos(2_000_000_000)
	delta1 := NewOrderBookDelta(
		instrumentID,
		BookActionAdd,
		NewBookOrder(
			OrderSideSell,
			decimal.MustPrice("1.0520"),
			decimal.MustQuantity("50000"),
			1,
		),
		flags,
		sequence,
		tsEvent,
		tsInit,
	)
	delta2 := NewOrderBookDelta(
		instrumentID,
		BookActionAdd,
		NewBookOrder(
			OrderSideBuy,
			decimal.MustPrice("1.0500"),
			decimal.MustQuantity("75000"),
			2,
		),
		flags,
		sequence,
		tsEvent,
		tsInit,
	)
	return NewOrderBookDeltas(instrumentID, []OrderBookDelta{delta1, delta2})
}

func createTestDeltasMultiple() OrderBookDeltas {
	instrumentID := InstrumentID("GBPUSD.SIM")
	flags := uint8(16)
	sequence := uint64(456)
	tsEvent := UnixNanos(3_000_000_000)
	tsInit := UnixNanos(4_000_000_000)
	deltas := []OrderBookDelta{
		ClearOrderBookDelta(instrumentID, sequence, tsEvent, tsInit),
		NewOrderBookDelta(
			instrumentID,
			BookActionAdd,
			NewBookOrder(
				OrderSideSell,
				decimal.MustPrice("1.2550"),
				decimal.MustQuantity("100000"),
				1,
			),
			flags,
			sequence,
			tsEvent,
			tsInit,
		),
		NewOrderBookDelta(
			instrumentID,
			BookActionUpdate,
			NewBookOrder(
				OrderSideBuy,
				decimal.MustPrice("1.2530"),
				decimal.MustQuantity("200000"),
				2,
			),
			flags,
			sequence,
			tsEvent,
			tsInit,
		),
		NewOrderBookDelta(
			instrumentID,
			BookActionDelete,
			NewBookOrder(
				OrderSideSell,
				decimal.MustPrice("1.2560"),
				decimal.MustQuantity("0"),
				3,
			),
			flags,
			sequence,
			tsEvent,
			tsInit,
		),
	}
	return NewOrderBookDeltas(instrumentID, deltas)
}

func stubOrderBookDeltas() OrderBookDeltas {
	instrumentID := InstrumentID("AAPL.XNAS")
	flags := uint8(32)
	sequence := uint64(0)
	tsEvent := UnixNanos(1)
	tsInit := UnixNanos(2)
	deltas := []OrderBookDelta{
		ClearOrderBookDelta(instrumentID, sequence, tsEvent, tsInit),
		NewOrderBookDelta(instrumentID, BookActionAdd, NewBookOrder(OrderSideSell, decimal.MustPrice("102.00"), decimal.MustQuantity("300"), 1), flags, sequence, tsEvent, tsInit),
		NewOrderBookDelta(instrumentID, BookActionAdd, NewBookOrder(OrderSideSell, decimal.MustPrice("101.00"), decimal.MustQuantity("200"), 2), flags, sequence, tsEvent, tsInit),
		NewOrderBookDelta(instrumentID, BookActionAdd, NewBookOrder(OrderSideSell, decimal.MustPrice("100.00"), decimal.MustQuantity("100"), 3), flags, sequence, tsEvent, tsInit),
		NewOrderBookDelta(instrumentID, BookActionAdd, NewBookOrder(OrderSideBuy, decimal.MustPrice("99.00"), decimal.MustQuantity("100"), 4), flags, sequence, tsEvent, tsInit),
		NewOrderBookDelta(instrumentID, BookActionAdd, NewBookOrder(OrderSideBuy, decimal.MustPrice("98.00"), decimal.MustQuantity("200"), 5), flags, sequence, tsEvent, tsInit),
		NewOrderBookDelta(instrumentID, BookActionAdd, NewBookOrder(OrderSideBuy, decimal.MustPrice("97.00"), decimal.MustQuantity("300"), 6), flags, sequence, tsEvent, tsInit),
	}
	return NewOrderBookDeltas(instrumentID, deltas)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/deltas.rs:317
//	test: test_order_book_deltas_new
func TestOrderBookDeltasNew(t *testing.T) {
	deltas := createTestDeltas()
	if deltas.InstrumentID != "EURUSD.SIM" {
		t.Fatalf("instrument ID = %q", deltas.InstrumentID)
	}
	if len(deltas.Deltas) != 2 {
		t.Fatalf("delta count = %d", len(deltas.Deltas))
	}
	if deltas.Flags != 32 || deltas.Sequence != 123 {
		t.Fatalf("flags/sequence = %d/%d", deltas.Flags, deltas.Sequence)
	}
	if deltas.TsEvent != 1_000_000_000 || deltas.TsInit != 2_000_000_000 {
		t.Fatalf("timestamps = %d/%d", deltas.TsEvent, deltas.TsInit)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/deltas.rs:329
//	test: test_order_book_deltas_new_checked_valid
func TestOrderBookDeltasNewCheckedValid(t *testing.T) {
	instrumentID := InstrumentID("EURUSD.SIM")
	delta := createTestDelta()
	deltas, err := NewOrderBookDeltasChecked(instrumentID, []OrderBookDelta{delta})
	if err != nil {
		t.Fatalf("new checked: %v", err)
	}
	if deltas.InstrumentID != instrumentID || len(deltas.Deltas) != 1 {
		t.Fatalf("unexpected checked deltas: %+v", deltas)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/deltas.rs:342
//	test: test_order_book_deltas_new_checked_empty_deltas
func TestOrderBookDeltasNewCheckedEmptyDeltas(t *testing.T) {
	_, err := NewOrderBookDeltasChecked("EURUSD.SIM", nil)
	if err == nil {
		t.Fatal("expected empty deltas error")
	}
	if !strings.Contains(err.Error(), "`deltas` cannot be empty") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/deltas.rs:358
//	test: test_order_book_deltas_new_empty_deltas_panics
func TestOrderBookDeltasNewEmptyDeltasPanics(t *testing.T) {
	defer func() {
		recovered := recover()
		if recovered == nil || !strings.Contains(fmt.Sprint(recovered), "Condition failed") {
			t.Fatalf("panic = %v", recovered)
		}
	}()
	_ = NewOrderBookDeltas("EURUSD.SIM", nil)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/deltas.rs:364
//	test: test_order_book_deltas_uses_last_delta_properties
func TestOrderBookDeltasUsesLastDeltaProperties(t *testing.T) {
	instrumentID := InstrumentID("EURUSD.SIM")
	delta1 := NewOrderBookDelta(
		instrumentID,
		BookActionAdd,
		NewBookOrder(OrderSideBuy, decimal.MustPrice("1.0500"), decimal.MustQuantity("100000"), 1),
		16,
		100,
		500_000_000,
		1_000_000_000,
	)
	delta2 := NewOrderBookDelta(
		instrumentID,
		BookActionAdd,
		NewBookOrder(OrderSideSell, decimal.MustPrice("1.0520"), decimal.MustQuantity("50000"), 2),
		32,
		200,
		1_500_000_000,
		2_000_000_000,
	)
	deltas := NewOrderBookDeltas(instrumentID, []OrderBookDelta{delta1, delta2})
	if deltas.Flags != 32 || deltas.Sequence != 200 ||
		deltas.TsEvent != 1_500_000_000 || deltas.TsInit != 2_000_000_000 {
		t.Fatalf("metadata was not taken from last delta: %+v", deltas)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/deltas.rs:407
//	test: test_order_book_deltas_hash_different_objects
func TestOrderBookDeltasHashDifferentObjects(t *testing.T) {
	deltas1 := createTestDeltas()
	deltas2 := createTestDeltasMultiple()
	if deltas1.Hash() == deltas2.Hash() {
		t.Fatalf("different batches had equal hashes: %d", deltas1.Hash())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/deltas.rs:421
//	test: test_order_book_deltas_hash_uses_instrument_id_and_sequence
func TestOrderBookDeltasHashUsesInstrumentIDAndSequence(t *testing.T) {
	instrumentID := InstrumentID("EURUSD.SIM")
	sequence := uint64(123)
	expectedHasher := fnv.New64a()
	_, _ = expectedHasher.Write([]byte(instrumentID))
	var encodedSequence [8]byte
	binary.LittleEndian.PutUint64(encodedSequence[:], sequence)
	_, _ = expectedHasher.Write(encodedSequence[:])
	expectedHash := expectedHasher.Sum64()

	delta := NewOrderBookDelta(
		instrumentID,
		BookActionAdd,
		NewBookOrder(OrderSideBuy, decimal.MustPrice("1.0500"), decimal.MustQuantity("100000"), 1),
		0,
		sequence,
		1_000_000_000,
		2_000_000_000,
	)
	deltas := NewOrderBookDeltas(instrumentID, []OrderBookDelta{delta})
	if deltas.Hash() != expectedHash {
		t.Fatalf("hash = %d, want %d", deltas.Hash(), expectedHash)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/deltas.rs:455
//	test: test_order_book_deltas_display
func TestOrderBookDeltasDisplay(t *testing.T) {
	display := createTestDeltas().String()
	for _, want := range []string{
		"EURUSD.SIM",
		"len=2",
		"flags=32",
		"sequence=123",
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
//	source: crates/model/src/data/deltas.rs:468
//	test: test_order_book_deltas_display_format
func TestOrderBookDeltasDisplayFormat(t *testing.T) {
	const want = "EURUSD.SIM,len=2,flags=32,sequence=123,ts_event=1000000000,ts_init=2000000000"
	if got := createTestDeltas().String(); got != want {
		t.Fatalf("display = %q, want %q", got, want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/deltas.rs:477
//	test: test_order_book_deltas_has_ts_init
func TestOrderBookDeltasHasTsInit(t *testing.T) {
	deltas := createTestDeltas()
	if deltas.TsInit != 2_000_000_000 {
		t.Fatalf("ts_init = %d", deltas.TsInit)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/deltas.rs:484
//	test: test_order_book_deltas_clone
func TestOrderBookDeltasClone(t *testing.T) {
	deltas1 := createTestDeltas()
	deltas2 := deltas1.Clone()
	if deltas1.InstrumentID != deltas2.InstrumentID ||
		len(deltas1.Deltas) != len(deltas2.Deltas) ||
		deltas1.Flags != deltas2.Flags ||
		deltas1.Sequence != deltas2.Sequence ||
		deltas1.TsEvent != deltas2.TsEvent ||
		deltas1.TsInit != deltas2.TsInit ||
		!deltas1.Equal(deltas2) {
		t.Fatalf("clone differs: %+v != %+v", deltas1, deltas2)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/deltas.rs:498
//	test: test_order_book_deltas_debug
func TestOrderBookDeltasDebug(t *testing.T) {
	debug := fmt.Sprintf("%#v", createTestDeltas())
	for _, want := range []string{"OrderBookDeltas", "EURUSD.SIM", "Flags:0x20", "Sequence:0x7b"} {
		if !strings.Contains(debug, want) {
			t.Fatalf("%q does not contain %q", debug, want)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/deltas.rs:509
//	test: test_order_book_deltas_serialization
func TestOrderBookDeltasSerialization(t *testing.T) {
	deltas := createTestDeltas()
	data, err := json.Marshal(deltas)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var deserialized OrderBookDeltas
	if err := json.Unmarshal(data, &deserialized); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if deltas.InstrumentID != deserialized.InstrumentID ||
		len(deltas.Deltas) != len(deserialized.Deltas) ||
		deltas.Flags != deserialized.Flags ||
		deltas.Sequence != deserialized.Sequence ||
		deltas.TsEvent != deserialized.TsEvent ||
		deltas.TsInit != deserialized.TsInit {
		t.Fatalf("round-trip differs: %+v != %+v", deltas, deserialized)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/deltas.rs:525
//	test: test_order_book_deltas_single_delta
func TestOrderBookDeltasSingleDelta(t *testing.T) {
	instrumentID := InstrumentID("BTCUSD.CRYPTO")
	delta := createTestDelta()
	deltas := NewOrderBookDeltas(instrumentID, []OrderBookDelta{delta})
	if deltas.InstrumentID != instrumentID || len(deltas.Deltas) != 1 ||
		deltas.Flags != delta.Flags || deltas.Sequence != delta.Sequence ||
		deltas.TsEvent != delta.TsEvent || deltas.TsInit != delta.TsInit {
		t.Fatalf("single-delta batch differs: %+v", deltas)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/deltas.rs:540
//	test: test_order_book_deltas_large_number_of_deltas
func TestOrderBookDeltasLargeNumberOfDeltas(t *testing.T) {
	instrumentID := InstrumentID("ETHUSD.CRYPTO")
	deltaVector := make([]OrderBookDelta, 0, 100)
	for i := range 100 {
		deltaVector = append(deltaVector, NewOrderBookDelta(
			instrumentID,
			BookActionAdd,
			NewBookOrder(
				OrderSideBuy,
				decimal.MustPrice(fmt.Sprintf("1000.%02d", i)),
				decimal.MustQuantity("1000"),
				uint64(i),
			),
			0,
			uint64(i),
			UnixNanos(1_000_000_000+i),
			UnixNanos(2_000_000_000+i),
		))
	}
	deltas := NewOrderBookDeltas(instrumentID, deltaVector)
	if len(deltas.Deltas) != 100 || deltas.Sequence != 99 ||
		deltas.TsEvent != 1_000_000_099 || deltas.TsInit != 2_000_000_099 {
		t.Fatalf("unexpected large batch: %+v", deltas)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/deltas.rs:572
//	test: test_order_book_deltas_different_action_types
func TestOrderBookDeltasDifferentActionTypes(t *testing.T) {
	deltas := createTestDeltasMultiple()
	if len(deltas.Deltas) != 4 {
		t.Fatalf("delta count = %d", len(deltas.Deltas))
	}
	want := []BookAction{BookActionClear, BookActionAdd, BookActionUpdate, BookActionDelete}
	for i, action := range want {
		if deltas.Deltas[i].Action != action {
			t.Fatalf("action[%d] = %s, want %s", i, deltas.Deltas[i].Action, action)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/deltas.rs:585
//	test: test_order_book_deltas_api_new
func TestOrderBookDeltasAPINew(t *testing.T) {
	deltas := createTestDeltas()
	apiWrapper := NewOrderBookDeltasAPI(deltas.Clone())
	if apiWrapper.InstrumentID != deltas.InstrumentID ||
		len(apiWrapper.Deltas) != len(deltas.Deltas) ||
		apiWrapper.Flags != deltas.Flags ||
		apiWrapper.Sequence != deltas.Sequence {
		t.Fatalf("wrapper differs: %+v", apiWrapper)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/deltas.rs:596
//	test: test_order_book_deltas_api_into_inner
func TestOrderBookDeltasAPIIntoInner(t *testing.T) {
	deltas := createTestDeltas()
	apiWrapper := NewOrderBookDeltasAPI(deltas.Clone())
	innerDeltas := apiWrapper.IntoInner()
	if !innerDeltas.Equal(deltas) {
		t.Fatalf("inner differs: %+v != %+v", innerDeltas, deltas)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/deltas.rs:605
//	test: test_order_book_deltas_api_deref
func TestOrderBookDeltasAPIDeref(t *testing.T) {
	deltas := createTestDeltas()
	apiWrapper := NewOrderBookDeltasAPI(deltas.Clone())
	if apiWrapper.InstrumentID != deltas.InstrumentID || apiWrapper.TsInit != deltas.TsInit {
		t.Fatalf("promoted access differs: %+v", apiWrapper)
	}
	display := apiWrapper.String()
	if !strings.Contains(display, "EURUSD.SIM") {
		t.Fatalf("display %q lacks instrument", display)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/deltas.rs:619
//	test: test_order_book_deltas_api_deref_mut
func TestOrderBookDeltasAPIDerefMut(t *testing.T) {
	apiWrapper := NewOrderBookDeltasAPI(createTestDeltas())
	originalFlags := apiWrapper.Flags
	apiWrapper.Flags = 64
	if apiWrapper.Flags == originalFlags || apiWrapper.Flags != 64 {
		t.Fatalf("flags = %d, original = %d", apiWrapper.Flags, originalFlags)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/deltas.rs:632
//	test: test_order_book_deltas_api_clone
func TestOrderBookDeltasAPIClone(t *testing.T) {
	apiWrapper1 := NewOrderBookDeltasAPI(createTestDeltas())
	apiWrapper2 := apiWrapper1.Clone()
	if apiWrapper1.InstrumentID != apiWrapper2.InstrumentID ||
		apiWrapper1.Sequence != apiWrapper2.Sequence ||
		!apiWrapper1.Equal(apiWrapper2) {
		t.Fatalf("wrapper clone differs: %+v != %+v", apiWrapper1, apiWrapper2)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/deltas.rs:643
//	test: test_order_book_deltas_api_debug
func TestOrderBookDeltasAPIDebug(t *testing.T) {
	apiWrapper := NewOrderBookDeltasAPI(createTestDeltas())
	debug := fmt.Sprintf("%T%#v", apiWrapper, *apiWrapper.OrderBookDeltas)
	for _, want := range []string{"OrderBookDeltasAPI", "EURUSD.SIM"} {
		if !strings.Contains(debug, want) {
			t.Fatalf("%q does not contain %q", debug, want)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/deltas.rs:653
//	test: test_order_book_deltas_api_serialization
func TestOrderBookDeltasAPISerialization(t *testing.T) {
	apiWrapper := NewOrderBookDeltasAPI(createTestDeltas())
	data, err := json.Marshal(apiWrapper)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var deserialized OrderBookDeltasAPI
	if err := json.Unmarshal(data, &deserialized); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if apiWrapper.InstrumentID != deserialized.InstrumentID ||
		apiWrapper.Sequence != deserialized.Sequence ||
		!apiWrapper.Equal(deserialized) {
		t.Fatalf("round-trip differs: %+v != %+v", apiWrapper, deserialized)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/deltas.rs:667
//	test: test_order_book_deltas_with_stub
func TestOrderBookDeltasWithStub(t *testing.T) {
	deltas := stubOrderBookDeltas()
	if deltas.InstrumentID != "AAPL.XNAS" || len(deltas.Deltas) != 7 ||
		deltas.Flags != 32 || deltas.Sequence != 0 ||
		deltas.TsEvent != 1 || deltas.TsInit != 2 {
		t.Fatalf("unexpected stub: %+v", deltas)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/deltas.rs:679
//	test: test_display_with_stub
func TestDisplayWithStub(t *testing.T) {
	const want = "AAPL.XNAS,len=7,flags=32,sequence=0,ts_event=1,ts_init=2"
	if got := stubOrderBookDeltas().String(); got != want {
		t.Fatalf("display = %q, want %q", got, want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/deltas.rs:688
//	test: test_order_book_deltas_zero_sequence
func TestOrderBookDeltasZeroSequence(t *testing.T) {
	instrumentID := InstrumentID("ZERO.TEST")
	delta := NewOrderBookDelta(
		instrumentID,
		BookActionAdd,
		NewBookOrder(
			OrderSideBuy,
			decimal.MustPrice("100.0"),
			decimal.MustQuantity("1000"),
			1,
		),
		0,
		0,
		0,
		0,
	)
	deltas := NewOrderBookDeltas(instrumentID, []OrderBookDelta{delta})
	if deltas.Sequence != 0 || deltas.TsEvent != 0 || deltas.TsInit != 0 {
		t.Fatalf("unexpected zero metadata: %+v", deltas)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/deltas.rs:713
//	test: test_order_book_deltas_max_values
func TestOrderBookDeltasMaxValues(t *testing.T) {
	instrumentID := InstrumentID("MAX.TEST")
	delta := NewOrderBookDelta(
		instrumentID,
		BookActionAdd,
		NewBookOrder(
			OrderSideBuy,
			decimal.MustPrice("999999.99"),
			decimal.MustQuantity("999999999"),
			math.MaxUint64,
		),
		math.MaxUint8,
		math.MaxUint64,
		UnixNanos(math.MaxUint64),
		UnixNanos(math.MaxUint64),
	)
	deltas := NewOrderBookDeltas(instrumentID, []OrderBookDelta{delta})
	if deltas.Flags != math.MaxUint8 || deltas.Sequence != math.MaxUint64 ||
		deltas.TsEvent != UnixNanos(math.MaxUint64) || deltas.TsInit != UnixNanos(math.MaxUint64) {
		t.Fatalf("unexpected maximum metadata: %+v", deltas)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/deltas.rs:739
//	test: test_new
func TestNew(t *testing.T) {
	instrumentID := InstrumentID("AAPL.XNAS")
	flags := uint8(32)
	sequence := uint64(0)
	tsEvent := UnixNanos(1)
	tsInit := UnixNanos(2)
	delta0 := ClearOrderBookDelta(instrumentID, sequence, tsEvent, tsInit)
	delta1 := NewOrderBookDelta(instrumentID, BookActionAdd, NewBookOrder(OrderSideSell, decimal.MustPrice("102.00"), decimal.MustQuantity("300"), 1), flags, sequence, tsEvent, tsInit)
	delta2 := NewOrderBookDelta(instrumentID, BookActionAdd, NewBookOrder(OrderSideSell, decimal.MustPrice("101.00"), decimal.MustQuantity("200"), 2), flags, sequence, tsEvent, tsInit)
	delta3 := NewOrderBookDelta(instrumentID, BookActionAdd, NewBookOrder(OrderSideSell, decimal.MustPrice("100.00"), decimal.MustQuantity("100"), 3), flags, sequence, tsEvent, tsInit)
	delta4 := NewOrderBookDelta(instrumentID, BookActionAdd, NewBookOrder(OrderSideBuy, decimal.MustPrice("99.00"), decimal.MustQuantity("100"), 4), flags, sequence, tsEvent, tsInit)
	delta5 := NewOrderBookDelta(instrumentID, BookActionAdd, NewBookOrder(OrderSideBuy, decimal.MustPrice("98.00"), decimal.MustQuantity("200"), 5), flags, sequence, tsEvent, tsInit)
	delta6 := NewOrderBookDelta(instrumentID, BookActionAdd, NewBookOrder(OrderSideBuy, decimal.MustPrice("97.00"), decimal.MustQuantity("300"), 6), flags, sequence, tsEvent, tsInit)
	deltas := NewOrderBookDeltas(instrumentID, []OrderBookDelta{delta0, delta1, delta2, delta3, delta4, delta5, delta6})
	if deltas.InstrumentID != instrumentID || len(deltas.Deltas) != 7 ||
		deltas.Flags != flags || deltas.Sequence != sequence ||
		deltas.TsEvent != tsEvent || deltas.TsInit != tsInit {
		t.Fatalf("unexpected batch: %+v", deltas)
	}
}
