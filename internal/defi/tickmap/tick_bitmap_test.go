package tickmap

import "testing"

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/tick_bitmap.rs:164
//	test: test_tick_to_positions
func TestTickToPositions(t *testing.T) {
	for _, test := range []struct {
		tick int32
		word int16
		bit  uint8
	}{{256, 1, 0}, {-256, -1, 0}, {100, 0, 100}} {
		word, bit := TickPosition(test.tick)
		if word != test.word || bit != test.bit {
			t.Errorf("TickPosition(%d) = (%d,%d)", test.tick, word, bit)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/tick_bitmap.rs:176
//	test: test_flip_tick_toggle
func TestFlipTickToggle(t *testing.T) {
	bitmap := NewTickBitmap(1)
	if bitmap.IsInitialized(100) {
		t.Fatal("tick initially initialized")
	}
	bitmap.FlipTick(100)
	if !bitmap.IsInitialized(100) {
		t.Fatal("tick was not initialized")
	}
	bitmap.FlipTick(100)
	if bitmap.IsInitialized(100) || bitmap.IsInitialized(99) || bitmap.IsInitialized(101) {
		t.Fatal("toggle affected unexpected ticks")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/tick_bitmap.rs:194
//	test: test_multiple_ticks_same_word
func TestMultipleTicksSameWord(t *testing.T) {
	bitmap := NewTickBitmap(1)
	for _, tick := range []int32{50, 100, 200} {
		bitmap.FlipTick(tick)
	}
	if !bitmap.IsInitialized(50) || !bitmap.IsInitialized(100) ||
		!bitmap.IsInitialized(200) || bitmap.IsInitialized(51) {
		t.Fatal("same-word initialization mismatch")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/tick_bitmap.rs:207
//	test: test_multiple_ticks_different_words
func TestMultipleTicksDifferentWords(t *testing.T) {
	bitmap := NewTickBitmap(1)
	for _, tick := range []int32{100, 300, -100} {
		bitmap.FlipTick(tick)
	}
	for _, tick := range []int32{100, 300, -100} {
		if !bitmap.IsInitialized(tick) {
			t.Errorf("tick %d is not initialized", tick)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/tick_bitmap.rs:219
//	test: test_next_initialized_tick_within_one_word_basic
func TestNextInitializedTickWithinOneWordBasic(t *testing.T) {
	bitmap := NewTickBitmap(1)
	bitmap.FlipTick(2)
	bitmap.FlipTick(3)
	next, initialized := bitmap.NextInitializedTickWithinOneWord(1, false)
	if next != 2 || !initialized {
		t.Fatalf("next = %d,%v", next, initialized)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/tick_bitmap.rs:231
//	test: test_next_initialized_tick_within_one_word_backward
func TestNextInitializedTickWithinOneWordBackward(t *testing.T) {
	bitmap := NewTickBitmap(1)
	bitmap.FlipTick(1)
	bitmap.FlipTick(2)
	next, initialized := bitmap.NextInitializedTickWithinOneWord(3, true)
	if next != 2 || !initialized {
		t.Fatalf("next = %d,%v", next, initialized)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/tick_bitmap.rs:243
//	test: test_next_initialized_tick_within_one_word_no_match
func TestNextInitializedTickWithinOneWordNoMatch(t *testing.T) {
	bitmap := NewTickBitmap(1)
	if _, initialized := bitmap.NextInitializedTickWithinOneWord(60, false); initialized {
		t.Fatal("forward search found empty tick")
	}
	if _, initialized := bitmap.NextInitializedTickWithinOneWord(60, true); initialized {
		t.Fatal("backward search found empty tick")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/tick_bitmap.rs:253
//	test: test_next_initialized_tick_with_negative_ticks
func TestNextInitializedTickWithNegativeTicks(t *testing.T) {
	bitmap := NewTickBitmap(1)
	bitmap.FlipTick(-2)
	bitmap.FlipTick(-1)
	next, initialized := bitmap.NextInitializedTickWithinOneWord(-3, false)
	if next != -2 || !initialized {
		t.Fatalf("next = %d,%v", next, initialized)
	}
}

func uniswapTickBitmap() *TickBitmap {
	bitmap := NewTickBitmap(1)
	for _, tick := range []int32{-200, -55, -4, 70, 78, 84, 139, 240, 535} {
		bitmap.FlipTick(tick)
	}
	return bitmap
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/tick_bitmap.rs:282
//	test: test_uniswapv3_test_cases_lte_false
func TestUniswapV3TestCasesLTEFalse(t *testing.T) {
	bitmap := uniswapTickBitmap()
	for _, test := range []struct {
		input int32
		want  int32
		init  bool
	}{{78, 84, true}, {-55, -4, true}, {77, 78, true}, {-56, -55, true},
		{255, 511, false}, {-257, -200, true}, {508, 511, false}, {383, 511, false}} {
		got, initialized := bitmap.NextInitializedTickWithinOneWord(test.input, false)
		if got != test.want || initialized != test.init {
			t.Errorf("next(%d,false) = %d,%v", test.input, got, initialized)
		}
	}
	bitmap.FlipTick(340)
	if got, initialized := bitmap.NextInitializedTickWithinOneWord(328, false); got != 340 || !initialized {
		t.Fatalf("next after flip = %d,%v", got, initialized)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/tick_map/tick_bitmap.rs:322
//	test: test_uniswapv3_test_cases_lte_true
func TestUniswapV3TestCasesLTETrue(t *testing.T) {
	bitmap := uniswapTickBitmap()
	for _, test := range []struct {
		input int32
		want  int32
		init  bool
	}{{78, 78, true}, {79, 78, true}, {258, 256, false}, {256, 256, false},
		{255, 240, true}, {72, 70, true}, {-257, -512, false}, {1023, 768, false},
		{900, 768, false}} {
		got, initialized := bitmap.NextInitializedTickWithinOneWord(test.input, true)
		if got != test.want || initialized != test.init {
			t.Errorf("next(%d,true) = %d,%v", test.input, got, initialized)
		}
	}
	bitmap.FlipTick(768)
	if got, initialized := bitmap.NextInitializedTickWithinOneWord(900, true); got != 768 || !initialized {
		t.Fatalf("initialized boundary = %d,%v", got, initialized)
	}
}
