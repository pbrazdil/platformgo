package market

import (
	"encoding/json"
	"testing"

	"github.com/upcomers-org/platformgo/internal/decimal"
)

func appleClose() InstrumentClose {
	return NewInstrumentClose("AAPL.XNAS", decimal.MustPrice("150.20"), EndOfSession, 1, 2)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/close.rs:126
//	test: test_new
//
// Assertions preserved:
//   - Every constructor argument is retained exactly.
func TestInstrumentCloseNew(t *testing.T) {
	close := appleClose()
	if close.InstrumentID != "AAPL.XNAS" ||
		close.ClosePrice.String() != "150.20" ||
		close.CloseType != EndOfSession ||
		close.TsEvent != 1 ||
		close.TsInit != 2 {
		t.Fatalf("unexpected instrument close: %+v", close)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/close.rs:144
//	test: test_to_string
//
// Assertions preserved:
//   - Display output is exactly AAPL.XNAS,150.20,END_OF_SESSION,1.
func TestInstrumentCloseToString(t *testing.T) {
	const want = "AAPL.XNAS,150.20,END_OF_SESSION,1"
	if got := appleClose().String(); got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/close.rs:161
//	test: test_json_serialization
//
// Assertions preserved:
//   - JSON serialization followed by deserialization preserves the value.
func TestInstrumentCloseJSONSerialization(t *testing.T) {
	source := appleClose()
	data, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	var decoded InstrumentClose
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if !decoded.Equal(source) {
		t.Fatalf("round trip differs: got %+v, want %+v", decoded, source)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/close.rs:178
//	test: test_msgpack_serialization
//
// Adaptations:
//   - Rust MessagePack plumbing is replaced by a deterministic native Go binary codec.
//
// Assertions preserved:
//   - Binary serialization followed by deserialization preserves the value.
func TestInstrumentCloseMsgpackSerialization(t *testing.T) {
	source := appleClose()
	data, err := source.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	var decoded InstrumentClose
	if err := decoded.UnmarshalBinary(data); err != nil {
		t.Fatal(err)
	}
	if !decoded.Equal(source) {
		t.Fatalf("round trip differs: got %+v, want %+v", decoded, source)
	}
}
