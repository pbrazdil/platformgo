package defi

import (
	"strings"
	"testing"

	"github.com/upcomers-org/platformgo/internal/ids"
)

func poolErrorLocation(t *testing.T) PoolEventLocation {
	t.Helper()
	instrument, err := ids.ParseInstrumentID("0xBBf3209130dF7d19356d72Eb8a193e2D9Ec5c234.Arbitrum:UniswapV3")
	if err != nil {
		t.Fatal(err)
	}
	return PoolEventLocation{
		InstrumentID:     instrument,
		PoolIdentifier:   MustPoolIdentifier("0xBBf3209130dF7d19356d72Eb8a193e2D9Ec5c234"),
		Block:            12345,
		TransactionIndex: 7,
		LogIndex:         42,
		EventKind:        PoolEventBurn,
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/error.rs:199
//	test: test_liquidity_error_with_location_maps_overflow
func TestLiquidityErrorWithLocationMapsOverflow(t *testing.T) {
	location := poolErrorLocation(t)
	err := LiquidityErrorWithLocation(LiquidityMathFailure{Kind: LiquidityOverflow, Current: 10, Delta: 20}, location)
	if err.Kind != ProfilerLiquidityOverflow || err.Current != 10 || err.Delta != 20 ||
		err.Location == nil || err.Location.InstrumentID != location.InstrumentID ||
		err.Location.PoolIdentifier != location.PoolIdentifier || err.Location.Block != location.Block ||
		err.Location.TransactionIndex != location.TransactionIndex ||
		err.Location.LogIndex != location.LogIndex || err.Location.EventKind != location.EventKind {
		t.Fatalf("mapped error = %#v", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/error.rs:228
//	test: test_liquidity_error_with_location_maps_underflow
func TestLiquidityErrorWithLocationMapsUnderflow(t *testing.T) {
	err := LiquidityErrorWithLocation(
		LiquidityMathFailure{Kind: LiquidityUnderflow, Current: 5, Delta: 9},
		poolErrorLocation(t),
	)
	if err.Kind != ProfilerLiquidityUnderflow || err.Current != 5 || err.Delta != 9 {
		t.Fatalf("mapped error = %#v", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/error.rs:247
//	test: test_pool_profiler_error_location_accessor
func TestPoolProfilerErrorLocationAccessor(t *testing.T) {
	location := poolErrorLocation(t)
	for _, kind := range []PoolProfilerErrorKind{ProfilerLiquidityOverflow, ProfilerLiquidityUnderflow} {
		err := PoolProfilerError{Kind: kind, Location: &location}
		if err.EventLocation() == nil {
			t.Errorf("%v has no location", kind)
		}
	}
	err := PoolProfilerError{
		Kind:           ProfilerNotInitialized,
		InstrumentID:   location.InstrumentID,
		PoolIdentifier: location.PoolIdentifier,
		EventKind:      PoolEventSwap,
	}
	if err.EventLocation() != nil {
		t.Fatal("not-initialized error unexpectedly has location")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/error.rs:281
//	test: test_pool_event_kind_display
func TestPoolEventKindDisplay(t *testing.T) {
	tests := map[PoolEventKind]string{
		PoolEventInitialize: "Initialize", PoolEventSwap: "Swap", PoolEventMint: "Mint",
		PoolEventBurn: "Burn", PoolEventCollect: "Collect", PoolEventFlash: "Flash",
	}
	for kind, want := range tests {
		if kind.String() != want {
			t.Errorf("%v = %q", kind, kind.String())
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/error.rs:286
//	test: test_pool_event_location_display_contains_required_fields
func TestPoolEventLocationDisplayContainsRequiredFields(t *testing.T) {
	text := poolErrorLocation(t).String()
	for _, want := range []string{
		"0xBBf3209130dF7d19356d72Eb8a193e2D9Ec5c234", "Arbitrum:UniswapV3",
		"block=12345", "tx_index=7", "log_index=42", "event=Burn",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("%q does not contain %q", text, want)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/error.rs:297
//	test: test_pool_profiler_error_display_carries_full_context
func TestPoolProfilerErrorDisplayCarriesFullContext(t *testing.T) {
	location := poolErrorLocation(t)
	underflow := PoolProfilerError{
		Kind: ProfilerLiquidityUnderflow, Location: &location, Current: 10, Delta: 99,
	}
	for _, want := range []string{
		"0xBBf3209130dF7d19356d72Eb8a193e2D9Ec5c234", "block=12345",
		"tx_index=7", "log_index=42", "event=Burn", "current=10", "delta=99",
	} {
		if !strings.Contains(underflow.Error(), want) {
			t.Errorf("%q does not contain %q", underflow.Error(), want)
		}
	}
	overflow := PoolProfilerError{
		Kind: ProfilerLiquidityOverflow, Location: &location, Current: 100, Delta: 200,
	}
	if !strings.Contains(overflow.Error(), "current=100") || !strings.Contains(overflow.Error(), "delta=200") {
		t.Fatalf("overflow = %q", overflow.Error())
	}
	notInitialized := PoolProfilerError{
		Kind: ProfilerNotInitialized, InstrumentID: location.InstrumentID,
		PoolIdentifier: location.PoolIdentifier, EventKind: PoolEventMint,
	}
	if !strings.Contains(notInitialized.Error(), "Arbitrum:UniswapV3") ||
		!strings.Contains(notInitialized.Error(), "Mint") ||
		!strings.Contains(notInitialized.Error(), "not initialized") {
		t.Fatalf("not initialized = %q", notInitialized.Error())
	}
	mismatch := PoolProfilerError{
		Kind: ProfilerInitialTickMismatch, InstrumentID: location.InstrumentID,
		PoolIdentifier: location.PoolIdentifier, InitialTick: -100, CalculatedTick: 200,
	}
	if !strings.Contains(mismatch.Error(), "initial_tick=-100") ||
		!strings.Contains(mismatch.Error(), "computed_from_sqrt_price=200") {
		t.Fatalf("mismatch = %q", mismatch.Error())
	}
}
