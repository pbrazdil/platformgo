package market

import (
	"encoding/json"
	"testing"

	"github.com/upcomers-org/platformgo/internal/decimal"
)

func testMarkPriceUpdate() MarkPriceUpdate {
	return NewMarkPriceUpdate("BTC-USDT.OKX", decimal.MustPrice("150_500.10"), 1, 2)
}

func testIndexPriceUpdate() IndexPriceUpdate {
	return NewIndexPriceUpdate("BTC-USDT.OKX", decimal.MustPrice("150_500.10"), 1, 2)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/prices.rs:219
//	test: test_mark_price_update_new
func TestMarkPriceUpdateNew(t *testing.T) {
	update := testMarkPriceUpdate()
	if update.InstrumentID != "BTC-USDT.OKX" ||
		update.Value.String() != "150500.10" ||
		update.TsEvent != 1 ||
		update.TsInit != 2 {
		t.Fatalf("unexpected mark price update: %+v", update)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/prices.rs:232
//	test: test_mark_price_update_display
func TestMarkPriceUpdateDisplay(t *testing.T) {
	const want = "BTC-USDT.OKX,150500.10,1,2"
	if got := testMarkPriceUpdate().String(); got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/prices.rs:242
//	test: test_mark_price_update_get_ts_init
func TestMarkPriceUpdateGetTsInit(t *testing.T) {
	if got := testMarkPriceUpdate().TsInit; got != 2 {
		t.Fatalf("TsInit = %d, want 2", got)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/prices.rs:252
//	test: test_mark_price_update_eq_hash
func TestMarkPriceUpdateEqHash(t *testing.T) {
	first := testMarkPriceUpdate()
	second := testMarkPriceUpdate()
	different := NewMarkPriceUpdate("BTC-USDT.OKX", decimal.MustPrice("143_500.50"), 1, 2)

	if !first.Equal(second) {
		t.Fatal("equal updates compare unequal")
	}
	if first.Equal(different) {
		t.Fatal("updates with different prices compare equal")
	}
	if first.Hash64() != second.Hash64() {
		t.Fatal("equal updates have different hashes")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/prices.rs:278
//	test: test_mark_price_update_json_serialization
func TestMarkPriceUpdateJSONSerialization(t *testing.T) {
	source := testMarkPriceUpdate()
	data, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	var decoded MarkPriceUpdate
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if !source.Equal(decoded) {
		t.Fatalf("round trip differs: got %+v, want %+v", decoded, source)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/prices.rs:291
//	test: test_mark_price_update_msgpack_serialization
//
// Adaptations:
//   - Rust MessagePack plumbing is replaced by a deterministic native Go binary codec.
func TestMarkPriceUpdateMsgpackSerialization(t *testing.T) {
	source := testMarkPriceUpdate()
	data, err := source.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	var decoded MarkPriceUpdate
	if err := decoded.UnmarshalBinary(data); err != nil {
		t.Fatal(err)
	}
	if !source.Equal(decoded) {
		t.Fatalf("round trip differs: got %+v, want %+v", decoded, source)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/prices.rs:304
//	test: test_mark_price_update_clone
func TestMarkPriceUpdateClone(t *testing.T) {
	source := testMarkPriceUpdate()
	cloned := source
	if !source.Equal(cloned) {
		t.Fatal("copied update differs from source")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/prices.rs:315
//	test: test_mark_price_update_serde_json
func TestMarkPriceUpdateSerdeJSON(t *testing.T) {
	source := testMarkPriceUpdate()
	data, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	var decoded MarkPriceUpdate
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if !source.Equal(decoded) {
		t.Fatalf("round trip differs: got %+v, want %+v", decoded, source)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/prices.rs:328
//	test: test_index_price_update_new
func TestIndexPriceUpdateNew(t *testing.T) {
	update := testIndexPriceUpdate()
	if update.InstrumentID != "BTC-USDT.OKX" ||
		update.Value.String() != "150500.10" ||
		update.TsEvent != 1 ||
		update.TsInit != 2 {
		t.Fatalf("unexpected index price update: %+v", update)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/prices.rs:341
//	test: test_index_price_update_display
func TestIndexPriceUpdateDisplay(t *testing.T) {
	const want = "BTC-USDT.OKX,150500.10,1,2"
	if got := testIndexPriceUpdate().String(); got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/prices.rs:351
//	test: test_index_price_update_get_ts_init
func TestIndexPriceUpdateGetTsInit(t *testing.T) {
	if got := testIndexPriceUpdate().TsInit; got != 2 {
		t.Fatalf("TsInit = %d, want 2", got)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/prices.rs:361
//	test: test_index_price_update_eq_hash
func TestIndexPriceUpdateEqHash(t *testing.T) {
	first := testIndexPriceUpdate()
	second := testIndexPriceUpdate()
	different := NewIndexPriceUpdate("BTC-USDT.OKX", decimal.MustPrice("150_500.10"), 3, 2)

	if !first.Equal(second) {
		t.Fatal("equal updates compare unequal")
	}
	if first.Equal(different) {
		t.Fatal("updates with different event timestamps compare equal")
	}
	if first.Hash64() != second.Hash64() {
		t.Fatal("equal updates have different hashes")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/prices.rs:380
//	test: test_index_price_update_json_serialization
func TestIndexPriceUpdateJSONSerialization(t *testing.T) {
	source := testIndexPriceUpdate()
	data, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	var decoded IndexPriceUpdate
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if !source.Equal(decoded) {
		t.Fatalf("round trip differs: got %+v, want %+v", decoded, source)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/prices.rs:393
//	test: test_index_price_update_msgpack_serialization
//
// Adaptations:
//   - Rust MessagePack plumbing is replaced by a deterministic native Go binary codec.
func TestIndexPriceUpdateMsgpackSerialization(t *testing.T) {
	source := testIndexPriceUpdate()
	data, err := source.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	var decoded IndexPriceUpdate
	if err := decoded.UnmarshalBinary(data); err != nil {
		t.Fatal(err)
	}
	if !source.Equal(decoded) {
		t.Fatalf("round trip differs: got %+v, want %+v", decoded, source)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/prices.rs:406
//	test: test_index_price_update_serde_json
func TestIndexPriceUpdateSerdeJSON(t *testing.T) {
	source := testIndexPriceUpdate()
	data, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	var decoded IndexPriceUpdate
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if !source.Equal(decoded) {
		t.Fatalf("round trip differs: got %+v, want %+v", decoded, source)
	}
}
