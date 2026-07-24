package defi

import (
	"errors"
	"math/big"
	"reflect"
	"sync"
	"testing"

	"github.com/upcomers-org/platformgo/internal/defi/tickmap"
)

func analysisBI(text string) *big.Int {
	value, ok := new(big.Int).SetString(text, 10)
	if !ok {
		panic(text)
	}
	return value
}

func analysisPool(fee uint32, spacing int32, model FeeModel, sqrt *big.Int) AnalysisPool {
	return AnalysisPool{
		InstrumentID: "ANIMEWETH.ARBITRUM:UNISWAP_V3", Identifier: "0xbbf320",
		Fee: fee, TickSpacing: spacing, InitialSqrt: analysisCopyBig(sqrt),
		InitialTick: TickAtSqrtPrice(sqrt), FeeModel: model,
	}
}

func analysisSourceSqrt() *big.Int { return analysisBI("2505414483750479311864138015") }
func analysisQ96() *big.Int        { return new(big.Int).Lsh(big.NewInt(1), 96) }
func analysisExpand18(value int64) *big.Int {
	return new(big.Int).Mul(big.NewInt(value), new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil))
}

func initializedProfiler(t *testing.T) *PoolProfiler {
	t.Helper()
	sqrt := analysisSourceSqrt()
	profiler := NewPoolProfiler(analysisPool(3000, 60, FeeModelUniswap, sqrt))
	if err := profiler.Initialize(sqrt); err != nil {
		t.Fatal(err)
	}
	return profiler
}

func emptyProfiler(t *testing.T, fee uint32, spacing int32, model FeeModel) *PoolProfiler {
	t.Helper()
	sqrt := analysisQ96()
	profiler := NewPoolProfiler(analysisPool(fee, spacing, model, sqrt))
	if err := profiler.Initialize(sqrt); err != nil {
		t.Fatal(err)
	}
	return profiler
}

func uniProfiler(t *testing.T) *PoolProfiler {
	t.Helper()
	profiler := initializedProfiler(t)
	if err := profiler.Mint("lp", tickmap.GetMinTick(60), tickmap.GetMaxTick(60),
		big.NewInt(3161), big.NewInt(9996), big.NewInt(1000), 1); err != nil {
		t.Fatal(err)
	}
	return profiler
}

func mediumProfiler(t *testing.T) *PoolProfiler {
	t.Helper()
	profiler := emptyProfiler(t, 3000, 60, FeeModelUniswap)
	if err := profiler.Mint("lp", tickmap.GetMinTick(60), tickmap.GetMaxTick(60),
		analysisExpand18(2), new(big.Int), new(big.Int), 1); err != nil {
		t.Fatal(err)
	}
	return profiler
}

func lowProfiler(t *testing.T) *PoolProfiler {
	t.Helper()
	profiler := emptyProfiler(t, 500, 10, FeeModelUniswap)
	if err := profiler.Mint("lp", tickmap.GetMinTick(10), tickmap.GetMaxTick(10),
		analysisExpand18(2), new(big.Int), new(big.Int), 1); err != nil {
		t.Fatal(err)
	}
	return profiler
}

func minBoundaryProfiler(t *testing.T) *PoolProfiler {
	t.Helper()
	sqrt := analysisBI("1752296436575853995018143129341")
	profiler := NewPoolProfiler(analysisPool(500, 10, FeeModelUniswap, sqrt))
	if err := profiler.Initialize(sqrt); err != nil {
		t.Fatal(err)
	}
	if err := profiler.Mint("lp", 61930, 61950, big.NewInt(102930446), nil, nil, 1); err != nil {
		t.Fatal(err)
	}
	return profiler
}

func maxBoundaryProfiler(t *testing.T) *PoolProfiler {
	t.Helper()
	sqrt := analysisBI("1336959986410146511145142826940")
	profiler := NewPoolProfiler(analysisPool(500, 10, FeeModelUniswap, sqrt))
	if err := profiler.Initialize(sqrt); err != nil {
		t.Fatal(err)
	}
	if err := profiler.Mint("lp", 56220, 56520, big.NewInt(730321654), nil, nil, 1); err != nil {
		t.Fatal(err)
	}
	return profiler
}

func analysisPosition(profiler *PoolProfiler, owner string, lower, upper int32) (AnalysisPosition, bool) {
	position, ok := profiler.Positions[positionKey{owner, lower, upper}]
	return position, ok
}

func requireAnalysisError(t *testing.T, err error, kind string) *AnalysisError {
	t.Helper()
	var typed *AnalysisError
	if !errors.As(err, &typed) || typed.Kind != kind {
		t.Fatalf("error=%#v, want AnalysisError kind %q", err, kind)
	}
	return typed
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/tests.rs:327
//	test: test_initial_state
func TestPoolAnalysisInitialState(t *testing.T) {
	sqrt := analysisSourceSqrt()
	profiler := NewPoolProfiler(analysisPool(3000, 60, FeeModelUniswap, sqrt))
	if profiler.State.SqrtPrice.Sign() != 0 || profiler.State.CurrentTick != 0 ||
		len(profiler.ActiveTickValues()) != 0 || profiler.Pool.TickSpacing != 60 {
		t.Fatalf("state=%#v ticks=%v", profiler.State, profiler.ActiveTickValues())
	}
	if tickmap.TickSpacingToMaxLiquidityPerTick(60).Sign() <= 0 {
		t.Fatal("invalid max liquidity")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/tests.rs:339
//	test: test_initialize_success
func TestPoolAnalysisInitializeSuccess(t *testing.T) {
	profiler := initializedProfiler(t)
	if profiler.State.SqrtPrice.Cmp(analysisSourceSqrt()) != 0 || profiler.State.CurrentTick != -23028 {
		t.Fatalf("sqrt=%s tick=%d", profiler.State.SqrtPrice, profiler.State.CurrentTick)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/tests.rs:348
//	test: test_initialize_already_initialized
func TestPoolAnalysisInitializeAlreadyInitialized(t *testing.T) {
	profiler := initializedProfiler(t)
	err := profiler.Initialize(analysisBI("511495728837967332084595714"))
	requireAnalysisError(t, err, "already_initialized")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/tests.rs:371
//	test: test_if_starting_price_is_too_low
func TestPoolAnalysisStartingPriceTooLow(t *testing.T) {
	sqrt := analysisSourceSqrt()
	profiler := NewPoolProfiler(analysisPool(3000, 60, FeeModelUniswap, sqrt))
	err := profiler.Initialize(big.NewInt(1))
	requireAnalysisError(t, err, "sqrt_out_of_bounds")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/tests.rs:379
//	test: test_initialize_returns_initial_tick_mismatch
func TestPoolAnalysisInitializeReturnsInitialTickMismatch(t *testing.T) {
	sqrt := analysisSourceSqrt()
	profiler := NewPoolProfiler(analysisPool(3000, 60, FeeModelUniswap, sqrt))
	err := profiler.Initialize(analysisQ96())
	requireAnalysisError(t, err, "initial_tick_mismatch")
	if profiler.Initialized {
		t.Fatal("profiler initialized after mismatch")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/tests.rs:416
//	test: test_process_mint_with_fail_if_pool_not_initialized
func TestPoolAnalysisProcessMintFailsIfPoolNotInitialized(t *testing.T) {
	sqrt := analysisSourceSqrt()
	profiler := NewPoolProfiler(analysisPool(3000, 60, FeeModelUniswap, sqrt))
	err := profiler.Mint("lp", 60, 120, big.NewInt(1), nil, nil, 1)
	typed := requireAnalysisError(t, err, "not_initialized")
	if typed.Event != AnalysisMint {
		t.Fatalf("event=%s", typed.Event)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/tests.rs:453
//	test: test_if_pool_process_fails_if_tick_lower_is_greater_than_tick_upper
func TestPoolAnalysisRejectsReversedTicks(t *testing.T) {
	err := initializedProfiler(t).Mint("lp", 2, 1, big.NewInt(1), nil, nil, 1)
	if err == nil || err.Error() != "Invalid tick range: 2 >= 1" {
		t.Fatalf("err=%v", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/tests.rs:460
//	test: test_if_pool_process_fails_if_tick_are_not_multiple_of_tick_spacing
func TestPoolAnalysisRejectsUnalignedTicks(t *testing.T) {
	err := initializedProfiler(t).Mint("lp", 1, 2, big.NewInt(1), nil, nil, 1)
	if err == nil || err.Error() != "Ticks 1 and 2 must be multiples of the tick spacing" {
		t.Fatalf("err=%v", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/tests.rs:472
//	test: test_if_pool_process_fails_if_outside_tick_bounds
func TestPoolAnalysisRejectsTicksOutsideBounds(t *testing.T) {
	lower := ((tickmap.MaxTick / 60) + 1) * 60
	err := initializedProfiler(t).Mint("lp", lower, lower+60, big.NewInt(10000), nil, nil, 1)
	if err == nil || err.Error() != "Invalid tick bounds for 887280 and 887340" {
		t.Fatalf("err=%v", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/tests.rs:510
//	test: test_execute_mint_equivalence
func TestPoolAnalysisExecuteMintEquivalence(t *testing.T) {
	left, right := initializedProfiler(t), initializedProfiler(t)
	if err := left.Mint("lp", -240, 0, big.NewInt(10000), big.NewInt(120), new(big.Int), 2); err != nil {
		t.Fatal(err)
	}
	event, err := right.ExecuteMint("lp", -240, 0, big.NewInt(10000), big.NewInt(120), new(big.Int), 2)
	if err != nil {
		t.Fatal(err)
	}
	if event.Kind != AnalysisMint || event.Owner != "lp" || event.Liquidity.Cmp(big.NewInt(10000)) != 0 {
		t.Fatalf("event=%#v", event)
	}
	if !reflect.DeepEqual(left, right) {
		t.Fatal("process and execute mint states differ")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/tests.rs:607
//	test: test_execute_burn_equivalence
func TestPoolAnalysisExecuteBurnEquivalence(t *testing.T) {
	left, right := initializedProfiler(t), initializedProfiler(t)
	for _, profiler := range []*PoolProfiler{left, right} {
		if err := profiler.Mint("lp", -240, 0, big.NewInt(20000), big.NewInt(240), new(big.Int), 1); err != nil {
			t.Fatal(err)
		}
	}
	if err := left.Burn("lp", -240, 0, big.NewInt(10000), big.NewInt(120), new(big.Int), 2); err != nil {
		t.Fatal(err)
	}
	event, err := right.ExecuteBurn("lp", -240, 0, big.NewInt(10000), big.NewInt(120), new(big.Int), 2)
	if err != nil {
		t.Fatal(err)
	}
	if event.Kind != AnalysisBurn || event.Owner != "lp" || event.Liquidity.Cmp(big.NewInt(10000)) != 0 {
		t.Fatalf("event=%#v", event)
	}
	if !reflect.DeepEqual(left, right) {
		t.Fatal("process and execute burn states differ")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/tests.rs:725
//	test: test_execute_swap_equivalence
func TestPoolAnalysisExecuteSwapEquivalence(t *testing.T) {
	left, right := mediumProfiler(t), mediumProfiler(t)
	quote := left.QuoteSwap(big.NewInt(1000), true)
	left.ApplySwap(quote, 2)
	executed := right.ExecuteSwap(big.NewInt(1000), true, 2)
	if executed.Amount0.Cmp(quote.Amount0) != 0 || executed.Amount1.Cmp(quote.Amount1) != 0 {
		t.Fatalf("quote=%#v executed=%#v", quote, executed)
	}
	if left.State.CurrentTick != right.State.CurrentTick ||
		left.State.SqrtPrice.Cmp(right.State.SqrtPrice) != 0 ||
		left.State.Liquidity.Cmp(right.State.Liquidity) != 0 {
		t.Fatalf("left=%#v right=%#v", left.State, right.State)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/tests.rs:804
//	test: test_process_swap_snaps_sqrt_price_to_event
func TestPoolAnalysisProcessSwapSnapsSqrtPriceToEvent(t *testing.T) {
	profiler := mediumProfiler(t)
	quote := profiler.QuoteSwap(big.NewInt(1000), true)
	eventSqrt := new(big.Int).Sub(quote.SqrtPriceAfter, big.NewInt(1))
	profiler.ProcessSwapEvent(quote, eventSqrt, quote.TickAfter, quote.LiquidityAfter, 2)
	if profiler.State.SqrtPrice.Cmp(eventSqrt) != 0 {
		t.Fatalf("sqrt=%s want=%s", profiler.State.SqrtPrice, eventSqrt)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/tests.rs:852
//	test: test_process_swap_mismatch_does_not_mutate_simulated_crossed_tick
func TestPoolAnalysisProcessSwapMismatchDoesNotMutateSimulatedCrossedTick(t *testing.T) {
	profiler := uniProfiler(t)
	if err := profiler.Mint("lp", tickmap.GetMinTick(60), -23040, big.NewInt(50000), nil, nil, 2); err != nil {
		t.Fatal(err)
	}
	before := cloneTick(profiler.Ticks[-23040])
	quote := profiler.QuoteSwap(analysisExpand18(1), true)
	eventLiquidity := big.NewInt(3161)
	profiler.ProcessSwapEvent(quote, quote.SqrtPriceAfter, quote.TickAfter, eventLiquidity, 3)
	after := profiler.Ticks[-23040]
	if after.FeeGrowthOutside0.Cmp(before.FeeGrowthOutside0) != 0 ||
		after.FeeGrowthOutside1.Cmp(before.FeeGrowthOutside1) != 0 {
		t.Fatalf("tick mutated: before=%#v after=%#v", before, after)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/tests.rs:953
//	test: test_set_fee_protocol_applies_to_state_and_snapshot
func TestPoolAnalysisSetFeeProtocolAppliesToStateAndSnapshot(t *testing.T) {
	profiler := mediumProfiler(t)
	profiler.SetFeeProtocol(6, 6, false, 2)
	snapshot, err := profiler.Snapshot()
	if err != nil || profiler.State.FeeProtocol != 102 || snapshot.State.FeeProtocol != 102 {
		t.Fatalf("state=%d snapshot=%d err=%v", profiler.State.FeeProtocol, snapshot.State.FeeProtocol, err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/tests.rs:981
//	test: test_set_fee_protocol_changes_flash_fee_split
func TestPoolAnalysisSetFeeProtocolChangesFlashFeeSplit(t *testing.T) {
	profiler := mediumProfiler(t)
	profiler.Flash(big.NewInt(100), big.NewInt(100), 2)
	if profiler.State.ProtocolFeesToken0.Sign() != 0 || profiler.State.ProtocolFeesToken1.Sign() != 0 {
		t.Fatal("protocol fees accrued before protocol was enabled")
	}
	profiler.SetFeeProtocol(4, 4, false, 3)
	profiler.Flash(big.NewInt(100), big.NewInt(100), 4)
	if profiler.State.ProtocolFeesToken0.Cmp(big.NewInt(25)) != 0 ||
		profiler.State.ProtocolFeesToken1.Cmp(big.NewInt(25)) != 0 {
		t.Fatalf("fees=%s/%s", profiler.State.ProtocolFeesToken0, profiler.State.ProtocolFeesToken1)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/tests.rs:1024
//	test: test_pancakeswap_set_fee_protocol_applies_basis_points_to_state_and_snapshot
func TestPoolAnalysisPancakeSetFeeProtocolAppliesBasisPointsToStateAndSnapshot(t *testing.T) {
	profiler := mediumProfiler(t)
	profiler.SetFeeProtocol(3200, 4000, true, 2)
	snapshot, err := profiler.Snapshot()
	if err != nil || profiler.State.FeeProtocol != 0 ||
		*profiler.State.FeeProtocol0BasisPoints != 3200 || *snapshot.State.FeeProtocol1BasisPoints != 4000 {
		t.Fatalf("state=%#v snapshot=%#v err=%v", profiler.State, snapshot.State, err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/tests.rs:1060
//	test: test_pancakeswap_profiler_seeds_default_fee_protocol_from_pool_fee
func TestPoolAnalysisPancakeProfilerSeedsDefaultFeeProtocolFromPoolFee(t *testing.T) {
	for _, tc := range []struct{ fee, want uint32 }{{100, 3300}, {500, 3400}, {2500, 3200}, {10000, 3200}, {12345, 3200}} {
		profiler := NewPoolProfiler(analysisPool(tc.fee, 10, FeeModelPancakeSwap, analysisQ96()))
		if profiler.State.FeeProtocol != 0 || *profiler.State.FeeProtocol0BasisPoints != tc.want ||
			*profiler.State.FeeProtocol1BasisPoints != tc.want {
			t.Fatalf("fee=%d state=%#v", tc.fee, profiler.State)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/tests.rs:1080
//	test: test_pancakeswap_default_fee_protocol_changes_flash_fee_split
func TestPoolAnalysisPancakeDefaultFeeProtocolChangesFlashFeeSplit(t *testing.T) {
	profiler := emptyProfiler(t, 500, 10, FeeModelPancakeSwap)
	if err := profiler.Mint("lp", tickmap.GetMinTick(10), tickmap.GetMaxTick(10), big.NewInt(10000), nil, nil, 1); err != nil {
		t.Fatal(err)
	}
	profiler.Flash(big.NewInt(1000), big.NewInt(1000), 2)
	if profiler.State.ProtocolFeesToken0.Cmp(big.NewInt(340)) != 0 ||
		profiler.State.ProtocolFeesToken1.Cmp(big.NewInt(340)) != 0 {
		t.Fatalf("fees=%s/%s", profiler.State.ProtocolFeesToken0, profiler.State.ProtocolFeesToken1)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/tests.rs:1114
//	test: test_pancakeswap_set_fee_protocol_changes_flash_fee_split
func TestPoolAnalysisPancakeSetFeeProtocolChangesFlashFeeSplit(t *testing.T) {
	profiler := mediumProfiler(t)
	profiler.SetFeeProtocol(3200, 4000, true, 2)
	profiler.Flash(big.NewInt(1000), big.NewInt(1000), 3)
	if profiler.State.ProtocolFeesToken0.Cmp(big.NewInt(320)) != 0 ||
		profiler.State.ProtocolFeesToken1.Cmp(big.NewInt(400)) != 0 {
		t.Fatalf("fees=%s/%s", profiler.State.ProtocolFeesToken0, profiler.State.ProtocolFeesToken1)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/tests.rs:1144
//	test: test_pancakeswap_snapshot_restore_preserves_fee_model_for_replay
func TestPoolAnalysisPancakeSnapshotRestorePreservesFeeModelForReplay(t *testing.T) {
	profiler := mediumProfiler(t)
	profiler.SetFeeProtocol(3200, 4000, true, 2)
	snapshot, _ := profiler.Snapshot()
	restored := NewPoolProfiler(profiler.Pool)
	restored.Restore(snapshot)
	restored.Flash(big.NewInt(1000), big.NewInt(1000), 3)
	if *restored.State.FeeProtocol0BasisPoints != 3200 || *restored.State.FeeProtocol1BasisPoints != 4000 ||
		restored.State.ProtocolFeesToken0.Cmp(big.NewInt(320)) != 0 ||
		restored.State.ProtocolFeesToken1.Cmp(big.NewInt(400)) != 0 {
		t.Fatalf("restored=%#v", restored.State)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/tests.rs:1179
//	test: test_fee_protocol_collect_decrements_accrued_balances
func TestPoolAnalysisFeeProtocolCollectDecrementsAccruedBalances(t *testing.T) {
	profiler := mediumProfiler(t)
	profiler.SetFeeProtocol(4, 4, false, 2)
	profiler.Flash(big.NewInt(100), big.NewInt(100), 3)
	profiler.CollectProtocol(big.NewInt(20), big.NewInt(24), 4)
	if profiler.State.ProtocolFeesToken0.Cmp(big.NewInt(5)) != 0 ||
		profiler.State.ProtocolFeesToken1.Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("fees=%s/%s", profiler.State.ProtocolFeesToken0, profiler.State.ProtocolFeesToken1)
	}
	profiler.CollectProtocol(big.NewInt(1000), big.NewInt(1000), 5)
	if profiler.State.ProtocolFeesToken0.Sign() != 0 || profiler.State.ProtocolFeesToken1.Sign() != 0 {
		t.Fatal("oversized collect did not saturate")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/tests.rs:1227
//	test: test_compare_pool_profiler_reports_exact_match
func TestPoolAnalysisCompareReportsExactMatch(t *testing.T) {
	profiler := mediumProfiler(t)
	snapshot, _ := profiler.Snapshot()
	comparison := ComparePoolProfiler(profiler, snapshot)
	if comparison != PoolProfilerMatch || !comparison.Exact() || !comparison.ValidForSnapshot() {
		t.Fatalf("comparison=%v", comparison)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/tests.rs:1246
//	test: test_compare_pool_profiler_reports_sqrt_only_mismatch
func TestPoolAnalysisCompareReportsSqrtOnlyMismatch(t *testing.T) {
	profiler := mediumProfiler(t)
	snapshot, _ := profiler.Snapshot()
	snapshot.State.SqrtPrice.Add(snapshot.State.SqrtPrice, big.NewInt(1))
	comparison := ComparePoolProfiler(profiler, snapshot)
	if comparison != PoolProfilerSqrtPriceMismatch || comparison.Exact() || !comparison.ValidForSnapshot() {
		t.Fatalf("comparison=%v", comparison)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/tests.rs:1270
//	test: test_compare_pool_profiler_reports_fee_protocol_only_mismatch
func TestPoolAnalysisCompareReportsFeeProtocolOnlyMismatch(t *testing.T) {
	profiler := mediumProfiler(t)
	snapshot, _ := profiler.Snapshot()
	snapshot.State.FeeProtocol = 68
	comparison := ComparePoolProfiler(profiler, snapshot)
	if comparison != PoolProfilerFeeProtocolMismatch || comparison.Exact() || !comparison.ValidForSnapshot() {
		t.Fatalf("comparison=%v", comparison)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/tests.rs:1295
//	test: test_compare_pool_profiler_reports_protocol_fees_only_mismatch
func TestPoolAnalysisCompareReportsProtocolFeesOnlyMismatch(t *testing.T) {
	for _, bump := range [][2]bool{{true, false}, {false, true}, {true, true}} {
		profiler := mediumProfiler(t)
		snapshot, _ := profiler.Snapshot()
		if bump[0] {
			snapshot.State.ProtocolFeesToken0.Add(snapshot.State.ProtocolFeesToken0, big.NewInt(1))
		}
		if bump[1] {
			snapshot.State.ProtocolFeesToken1.Add(snapshot.State.ProtocolFeesToken1, big.NewInt(1))
		}
		comparison := ComparePoolProfiler(profiler, snapshot)
		if comparison != PoolProfilerProtocolFeesMismatch || comparison.Exact() || !comparison.ValidForSnapshot() {
			t.Fatalf("bump=%v comparison=%v", bump, comparison)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/tests.rs:1328
//	test: test_compare_pool_profiler_reports_structural_mismatch
func TestPoolAnalysisCompareReportsStructuralMismatch(t *testing.T) {
	profiler := mediumProfiler(t)
	snapshot, _ := profiler.Snapshot()
	snapshot.State.Liquidity.Add(snapshot.State.Liquidity, big.NewInt(1))
	comparison := ComparePoolProfiler(profiler, snapshot)
	if comparison != PoolProfilerMismatch || comparison.ValidForSnapshot() {
		t.Fatalf("comparison=%v", comparison)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/tests.rs:1347
//	test: test_extract_snapshot_uses_last_processed_event_timestamp
func TestPoolAnalysisExtractSnapshotUsesLastProcessedEventTimestamp(t *testing.T) {
	profiler := initializedProfiler(t)
	if err := profiler.Mint("lp", tickmap.GetMinTick(60), tickmap.GetMaxTick(60), big.NewInt(10000), nil, nil, 1_000_000_000); err != nil {
		t.Fatal(err)
	}
	snapshot, err := profiler.Snapshot()
	if err != nil || snapshot.Timestamp != 1_000_000_000 {
		t.Fatalf("timestamp=%d err=%v", snapshot.Timestamp, err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/tests.rs:1365
//	test: test_extract_snapshot_without_events_returns_error
func TestPoolAnalysisExtractSnapshotWithoutEventsReturnsError(t *testing.T) {
	_, err := initializedProfiler(t).Snapshot()
	requireAnalysisError(t, err, "no_events")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/tests.rs:1462
//	test: test_uni_pool_profiler_initial_state
func TestPoolAnalysisUniProfilerInitialState(t *testing.T) {
	profiler := uniProfiler(t)
	position, ok := analysisPosition(profiler, "lp", tickmap.GetMinTick(60), tickmap.GetMaxTick(60))
	if profiler.State.CurrentTick != -23028 || len(profiler.ActiveTickValues()) != 2 ||
		profiler.ActivePositionCount() != 1 || !ok || position.Liquidity.Cmp(big.NewInt(3161)) != 0 ||
		position.Amount0Deposited.Cmp(big.NewInt(9996)) != 0 ||
		position.Amount1Deposited.Cmp(big.NewInt(1000)) != 0 ||
		profiler.State.Liquidity.Cmp(big.NewInt(3161)) != 0 || profiler.LiquidityUtilization() != 1 {
		t.Fatalf("state=%#v position=%#v", profiler.State, position)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/tests.rs:1491
//	test: test_mint_above_current_price
func TestPoolAnalysisMintAboveCurrentPrice(t *testing.T) {
	profiler := uniProfiler(t)
	if err := profiler.Mint("lp", -22980, 0, big.NewInt(10000), big.NewInt(21549), new(big.Int), 2); err != nil {
		t.Fatal(err)
	}
	position, _ := analysisPosition(profiler, "lp", -22980, 0)
	if profiler.ActivePositionCount() != 1 || profiler.InactivePositionCount() != 1 ||
		position.Amount0Deposited.Cmp(big.NewInt(21549)) != 0 || len(profiler.ActiveTickValues()) != 4 {
		t.Fatalf("position=%#v ticks=%v", position, profiler.ActiveTickValues())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/tests.rs:1532
//	test: test_max_tick_with_high_leverage
func TestPoolAnalysisMaxTickWithHighLeverage(t *testing.T) {
	profiler := uniProfiler(t)
	maximum := tickmap.GetMaxTick(60)
	liquidity := new(big.Int).Lsh(big.NewInt(1), 102)
	if err := profiler.Mint("lp", maximum-60, maximum, liquidity, big.NewInt(828011525), nil, 2); err != nil {
		t.Fatal(err)
	}
	position, _ := analysisPosition(profiler, "lp", maximum-60, maximum)
	if position.Liquidity.Cmp(liquidity) != 0 || position.Amount0Deposited.Cmp(big.NewInt(828011525)) != 0 ||
		profiler.Ticks[maximum].Updates != 2 {
		t.Fatalf("position=%#v tick=%#v", position, profiler.Ticks[maximum])
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/tests.rs:1568
//	test: test_minting_works_for_max_tick
func TestPoolAnalysisMintingWorksForMaxTick(t *testing.T) {
	profiler := uniProfiler(t)
	maximum := tickmap.GetMaxTick(60)
	if err := profiler.Mint("lp", -22980, maximum, big.NewInt(10000), big.NewInt(31549), nil, 2); err != nil {
		t.Fatal(err)
	}
	position, _ := analysisPosition(profiler, "lp", -22980, maximum)
	if position.Amount0Deposited.Cmp(big.NewInt(31549)) != 0 ||
		profiler.Ticks[-22980].Updates != 1 || profiler.Ticks[maximum].Updates != 2 {
		t.Fatalf("position=%#v ticks=%#v/%#v", position, profiler.Ticks[-22980], profiler.Ticks[maximum])
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/tests.rs:1606
//	test: test_if_removing_of_liquidity_works_after_mint
func TestPoolAnalysisRemovingLiquidityWorksAfterMint(t *testing.T) {
	profiler := uniProfiler(t)
	if err := profiler.Mint("lp", -240, 0, big.NewInt(10000), big.NewInt(120), nil, 2); err != nil {
		t.Fatal(err)
	}
	if err := profiler.Burn("lp", -240, 0, big.NewInt(10000), big.NewInt(120), nil, 3); err != nil {
		t.Fatal(err)
	}
	position, _ := analysisPosition(profiler, "lp", -240, 0)
	if position.Liquidity.Sign() != 0 || position.TokensOwed0.Cmp(big.NewInt(120)) != 0 ||
		profiler.Analytics.Mints != 2 || profiler.Analytics.Burns != 1 {
		t.Fatalf("position=%#v analytics=%#v", position, profiler.Analytics)
	}
	profiler.Collect("lp", -240, 0, analysisMaxU128, analysisMaxU128, 4)
	if _, exists := analysisPosition(profiler, "lp", -240, 0); exists {
		t.Fatal("empty position not removed")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/tests.rs:1664
//	test: test_if_we_correctly_add_and_remove_liquidity_gross_after_every_updates
func TestPoolAnalysisCorrectlyAddsAndRemovesLiquidityGross(t *testing.T) {
	profiler := uniProfiler(t)
	for _, mint := range []struct {
		lower, upper int32
		liquidity    int64
	}{{-240, 0, 100}, {-240, 60, 150}, {0, 120, 60}} {
		if err := profiler.Mint("lp", mint.lower, mint.upper, big.NewInt(mint.liquidity), nil, nil, 2); err != nil {
			t.Fatal(err)
		}
	}
	for tick, want := range map[int32]int64{-240: 250, 0: 160, 60: 150, 120: 60} {
		if got := profiler.Ticks[tick].LiquidityGross; got.Cmp(big.NewInt(want)) != 0 {
			t.Fatalf("tick=%d got=%s want=%d", tick, got, want)
		}
	}
	if err := profiler.Burn("lp", -240, 0, big.NewInt(90), nil, nil, 3); err != nil {
		t.Fatal(err)
	}
	if err := profiler.Burn("lp", -240, 0, big.NewInt(10), nil, nil, 4); err != nil {
		t.Fatal(err)
	}
	if profiler.Ticks[-240].LiquidityGross.Cmp(big.NewInt(150)) != 0 ||
		profiler.Ticks[0].LiquidityGross.Cmp(big.NewInt(60)) != 0 {
		t.Fatalf("ticks=%#v/%#v", profiler.Ticks[-240], profiler.Ticks[0])
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/tests.rs:1825
//	test: test_burn_uninitialized_position
func TestPoolAnalysisBurnUninitializedPosition(t *testing.T) {
	err := uniProfiler(t).Burn("lp", -240, 0, big.NewInt(100), nil, nil, 2)
	if err == nil || err.Error() != "Position liquidity 0 is less than the requested burn amount of 100" {
		t.Fatalf("err=%v", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/tests.rs:1840
//	test: test_position_fee_growth_and_tokens_owed_after_swaps
func TestPoolAnalysisPositionFeeGrowthAndTokensOwedAfterSwaps(t *testing.T) {
	profiler := uniProfiler(t)
	lower, upper := tickmap.GetMinTick(60)+60, tickmap.GetMaxTick(60)-60
	fee0 := analysisBI("102084710076281216349243831104605583")
	fee1 := analysisBI("10208471007628121634924383110460558")
	profiler.SetFeeGrowthGlobal(fee0, fee1)
	if err := profiler.Mint("lp", lower, upper, big.NewInt(1), nil, nil, 2); err != nil {
		t.Fatal(err)
	}
	position, _ := analysisPosition(profiler, "lp", lower, upper)
	if position.FeeGrowthInside0Last.Cmp(fee0) != 0 || position.FeeGrowthInside1Last.Cmp(fee1) != 0 ||
		position.TokensOwed0.Sign() != 0 || position.TokensOwed1.Sign() != 0 {
		t.Fatalf("position=%#v", position)
	}
	// Three units of token0 principal mirror the source burn fixture.
	if err := profiler.Burn("lp", lower, upper, big.NewInt(1), big.NewInt(3), nil, 3); err != nil {
		t.Fatal(err)
	}
	position, _ = analysisPosition(profiler, "lp", lower, upper)
	if position.Liquidity.Sign() != 0 || position.TokensOwed0.Cmp(big.NewInt(3)) != 0 ||
		position.FeeGrowthInside0Last.Cmp(fee0) != 0 {
		t.Fatalf("position=%#v", position)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/tests.rs:1927
//	test: test_does_not_clear_position_fee_growth_snapshot_if_no_more_liquidity
func TestPoolAnalysisDoesNotClearPositionFeeGrowthSnapshotIfNoLiquidity(t *testing.T) {
	profiler := mediumProfiler(t)
	lower, upper := tickmap.GetMinTick(60), tickmap.GetMaxTick(60)
	fee0 := analysisBI("340282366920938463463374607431768211")
	fee1 := analysisBI("340282366920938576890830247744589365")
	profiler.SetFeeGrowthGlobal(fee0, fee1)
	if err := profiler.Mint("other", lower, upper, analysisExpand18(1), nil, nil, 2); err != nil {
		t.Fatal(err)
	}
	profiler.SetFeeGrowthGlobal(new(big.Int).Add(fee0, analysisQ128), new(big.Int).Add(fee1, analysisQ128))
	if err := profiler.Burn("other", lower, upper, analysisExpand18(1), nil, nil, 3); err != nil {
		t.Fatal(err)
	}
	position, _ := analysisPosition(profiler, "other", lower, upper)
	if position.Liquidity.Sign() != 0 || position.TokensOwed0.Sign() == 0 || position.TokensOwed1.Sign() == 0 ||
		position.FeeGrowthInside0Last.Sign() == 0 || position.FeeGrowthInside1Last.Sign() == 0 {
		t.Fatalf("position=%#v", position)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/tests.rs:1998
//	test: test_mint_if_range_includes_current_price
func TestPoolAnalysisMintIfRangeIncludesCurrentPrice(t *testing.T) {
	profiler := uniProfiler(t)
	lower, upper := tickmap.GetMinTick(60)+60, tickmap.GetMaxTick(60)-60
	if err := profiler.Mint("lp", lower, upper, big.NewInt(100), big.NewInt(317), big.NewInt(32), 2); err != nil {
		t.Fatal(err)
	}
	position, _ := analysisPosition(profiler, "lp", lower, upper)
	if profiler.ActivePositionCount() != 2 || position.Amount0Deposited.Cmp(big.NewInt(317)) != 0 ||
		position.Amount1Deposited.Cmp(big.NewInt(32)) != 0 ||
		profiler.Ticks[lower].LiquidityGross.Cmp(big.NewInt(100)) != 0 ||
		profiler.Ticks[upper].LiquidityGross.Cmp(big.NewInt(100)) != 0 {
		t.Fatalf("position=%#v", position)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/tests.rs:2036
//	test: test_mint_for_min_and_max_ticks
func TestPoolAnalysisMintForMinAndMaxTicks(t *testing.T) {
	profiler := uniProfiler(t)
	lower, upper := tickmap.GetMinTick(60), tickmap.GetMaxTick(60)
	if err := profiler.Mint("lp", lower, upper, big.NewInt(10000), big.NewInt(31623), big.NewInt(3163), 2); err != nil {
		t.Fatal(err)
	}
	position, _ := analysisPosition(profiler, "lp", lower, upper)
	if position.Liquidity.Cmp(big.NewInt(13161)) != 0 ||
		position.Amount0Deposited.Cmp(big.NewInt(41619)) != 0 ||
		position.Amount1Deposited.Cmp(big.NewInt(4163)) != 0 {
		t.Fatalf("position=%#v", position)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/tests.rs:2062
//	test: test_mint_then_burning_and_collecting
func TestPoolAnalysisMintThenBurningAndCollecting(t *testing.T) {
	profiler := uniProfiler(t)
	lower, upper := tickmap.GetMinTick(60)+60, tickmap.GetMaxTick(60)-60
	if err := profiler.Mint("lp", lower, upper, big.NewInt(100), nil, nil, 2); err != nil {
		t.Fatal(err)
	}
	if err := profiler.Burn("lp", lower, upper, big.NewInt(100), big.NewInt(1), big.NewInt(1), 3); err != nil {
		t.Fatal(err)
	}
	profiler.Collect("lp", lower, upper, analysisMaxU128, analysisMaxU128, 4)
	if _, ok := analysisPosition(profiler, "lp", lower, upper); ok ||
		profiler.ActivePositionCount() != 1 || profiler.InactivePositionCount() != 0 {
		t.Fatalf("positions=%#v", profiler.Positions)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/tests.rs:2096
//	test: test_mint_below_current_price_when_token1_only_changed
func TestPoolAnalysisMintBelowCurrentPriceWhenToken1OnlyChanged(t *testing.T) {
	profiler := uniProfiler(t)
	if err := profiler.Mint("lp", -46080, -23040, big.NewInt(10000), nil, big.NewInt(2162), 2); err != nil {
		t.Fatal(err)
	}
	position, _ := analysisPosition(profiler, "lp", -46080, -23040)
	if profiler.ActivePositionCount() != 1 || profiler.InactivePositionCount() != 1 ||
		position.Amount0Deposited.Sign() != 0 || position.Amount1Deposited.Cmp(big.NewInt(2162)) != 0 {
		t.Fatalf("position=%#v", position)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/tests.rs:2122
//	test: test_mint_below_current_price_when_really_high_leverage
func TestPoolAnalysisMintBelowCurrentPriceWhenReallyHighLeverage(t *testing.T) {
	profiler := uniProfiler(t)
	lower := tickmap.GetMinTick(60)
	liquidity := new(big.Int).Lsh(big.NewInt(1), 102)
	if err := profiler.Mint("lp", lower, lower+60, liquidity, nil, big.NewInt(828011520), 2); err != nil {
		t.Fatal(err)
	}
	position, _ := analysisPosition(profiler, "lp", lower, lower+60)
	if position.Liquidity.Cmp(liquidity) != 0 || position.Amount1Deposited.Cmp(big.NewInt(828011520)) != 0 {
		t.Fatalf("position=%#v", position)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/tests.rs:2149
//	test: test_if_mint_below_current_price_works_after_burn_and_fee_collect
func TestPoolAnalysisMintBelowWorksAfterBurnAndFeeCollect(t *testing.T) {
	profiler := uniProfiler(t)
	if err := profiler.Mint("lp", -46080, -46020, big.NewInt(10000), nil, big.NewInt(4), 2); err != nil {
		t.Fatal(err)
	}
	if err := profiler.Burn("lp", -46080, -46020, big.NewInt(10000), nil, big.NewInt(3), 3); err != nil {
		t.Fatal(err)
	}
	position, _ := analysisPosition(profiler, "lp", -46080, -46020)
	if position.Liquidity.Sign() != 0 || position.Amount1Deposited.Cmp(big.NewInt(4)) != 0 ||
		position.TokensOwed1.Cmp(big.NewInt(3)) != 0 {
		t.Fatalf("position=%#v", position)
	}
	profiler.Collect("lp", -46080, -46020, analysisMaxU128, analysisMaxU128, 4)
	if _, ok := analysisPosition(profiler, "lp", -46080, -46020); ok {
		t.Fatal("position remained after full collect")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/tests.rs:2200
//	test: test_collect_with_invalid_ticks_does_not_panic
func TestPoolAnalysisCollectWithInvalidTicksDoesNotPanic(t *testing.T) {
	profiler := uniProfiler(t)
	lower, upper := tickmap.GetMinTick(60), tickmap.GetMaxTick(60)
	before, _ := analysisPosition(profiler, "lp", lower, upper)
	profiler.Collect("lp", 100, 50, big.NewInt(1000), big.NewInt(1000), 2)
	after, _ := analysisPosition(profiler, "lp", lower, upper)
	if after.TokensOwed0.Cmp(before.TokensOwed0) != 0 {
		t.Fatalf("before=%#v after=%#v", before, after)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/tests.rs:2222
//	test: test_collect_works_with_multiple_lps
func TestPoolAnalysisCollectWorksWithMultipleLPs(t *testing.T) {
	profiler := emptyProfiler(t, 500, 10, FeeModelUniswap)
	lower, upper := tickmap.GetMinTick(10), tickmap.GetMaxTick(10)
	if err := profiler.Mint("lp", lower, upper, analysisExpand18(1), nil, nil, 1); err != nil {
		t.Fatal(err)
	}
	if err := profiler.Mint("lp", lower+10, upper-10, analysisExpand18(2), nil, nil, 2); err != nil {
		t.Fatal(err)
	}
	// Exact source fee shares: one third and two thirds of 500000000000001.
	growth := new(big.Int).Quo(new(big.Int).Mul(big.NewInt(500000000000001), analysisQ128), analysisExpand18(3))
	profiler.SetFeeGrowthGlobal(growth, new(big.Int))
	if err := profiler.Burn("lp", lower, upper, new(big.Int), nil, nil, 3); err != nil {
		t.Fatal(err)
	}
	if err := profiler.Burn("lp", lower+10, upper-10, new(big.Int), nil, nil, 4); err != nil {
		t.Fatal(err)
	}
	first, _ := analysisPosition(profiler, "lp", lower, upper)
	second, _ := analysisPosition(profiler, "lp", lower+10, upper-10)
	if first.TokensOwed0.Cmp(big.NewInt(166666666666666)) > 0 ||
		second.TokensOwed0.Cmp(big.NewInt(333333333333334)) > 0 ||
		second.TokensOwed0.Cmp(first.TokensOwed0) <= 0 {
		t.Fatalf("owed=%s/%s", first.TokensOwed0, second.TokensOwed0)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/tests.rs:2290
//	test: test_fee_growth_just_before_cap_binds
func TestPoolAnalysisFeeGrowthJustBeforeCapBinds(t *testing.T) {
	profiler := feeCapProfiler(t)
	magic := analysisBI("115792089237316195423570985008687907852929702298719625575994")
	profiler.SetFeeGrowthGlobal(magic, new(big.Int))
	pokeFullRange(t, profiler)
	position, _ := analysisPosition(profiler, "lp", tickmap.GetMinTick(10), tickmap.GetMaxTick(10))
	if position.TokensOwed0.Cmp(new(big.Int).Sub(analysisMaxU128, big.NewInt(1))) != 0 {
		t.Fatalf("owed=%s", position.TokensOwed0)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/tests.rs:2326
//	test: test_fee_growth_just_after_cap_binds
func TestPoolAnalysisFeeGrowthJustAfterCapBinds(t *testing.T) {
	profiler := feeCapProfiler(t)
	profiler.SetFeeGrowthGlobal(analysisBI("115792089237316195423570985008687907852929702298719625575995"), new(big.Int))
	pokeFullRange(t, profiler)
	position, _ := analysisPosition(profiler, "lp", tickmap.GetMinTick(10), tickmap.GetMaxTick(10))
	if position.TokensOwed0.Cmp(analysisMaxU128) != 0 {
		t.Fatalf("owed=%s", position.TokensOwed0)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/tests.rs:2361
//	test: test_fee_growth_well_after_cap_binds
func TestPoolAnalysisFeeGrowthWellAfterCapBinds(t *testing.T) {
	profiler := feeCapProfiler(t)
	profiler.SetFeeGrowthGlobal(new(big.Int).Sub(analysisQ256, big.NewInt(1)), new(big.Int))
	pokeFullRange(t, profiler)
	position, _ := analysisPosition(profiler, "lp", tickmap.GetMinTick(10), tickmap.GetMaxTick(10))
	if position.TokensOwed0.Cmp(analysisMaxU128) != 0 {
		t.Fatalf("owed=%s", position.TokensOwed0)
	}
}

func feeCapProfiler(t *testing.T) *PoolProfiler {
	t.Helper()
	profiler := emptyProfiler(t, 500, 10, FeeModelUniswap)
	if err := profiler.Mint("lp", tickmap.GetMinTick(10), tickmap.GetMaxTick(10), analysisExpand18(1), nil, nil, 1); err != nil {
		t.Fatal(err)
	}
	return profiler
}

func pokeFullRange(t *testing.T, profiler *PoolProfiler) {
	t.Helper()
	if err := profiler.Burn("lp", tickmap.GetMinTick(10), tickmap.GetMaxTick(10), new(big.Int), nil, nil, 2); err != nil {
		t.Fatal(err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/tests.rs:2394
//	test: test_overflow_boundary_token0
func TestPoolAnalysisOverflowBoundaryToken0(t *testing.T) {
	profiler := overflowProfiler(t)
	profiler.ApplySwap(profiler.QuoteSwap(analysisExpand18(1), true), 2)
	pokeFullRange(t, profiler)
	position, _ := analysisPosition(profiler, "lp", tickmap.GetMinTick(10), tickmap.GetMaxTick(10))
	if position.TokensOwed0.Cmp(big.NewInt(499999999999999)) != 0 || position.TokensOwed1.Sign() != 0 {
		t.Fatalf("owed=%s/%s", position.TokensOwed0, position.TokensOwed1)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/tests.rs:2433
//	test: test_overflow_boundary_token1
func TestPoolAnalysisOverflowBoundaryToken1(t *testing.T) {
	profiler := overflowProfiler(t)
	profiler.ApplySwap(profiler.QuoteSwap(analysisExpand18(1), false), 2)
	pokeFullRange(t, profiler)
	position, _ := analysisPosition(profiler, "lp", tickmap.GetMinTick(10), tickmap.GetMaxTick(10))
	if position.TokensOwed0.Sign() != 0 || position.TokensOwed1.Cmp(big.NewInt(499999999999999)) != 0 {
		t.Fatalf("owed=%s/%s", position.TokensOwed0, position.TokensOwed1)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/tests.rs:2470
//	test: test_overflow_boundary_token0_and_token1
func TestPoolAnalysisOverflowBoundaryToken0AndToken1(t *testing.T) {
	profiler := overflowProfiler(t)
	profiler.ApplySwap(profiler.QuoteSwap(analysisExpand18(1), true), 2)
	profiler.ApplySwap(profiler.QuoteSwap(analysisExpand18(1), false), 3)
	pokeFullRange(t, profiler)
	position, _ := analysisPosition(profiler, "lp", tickmap.GetMinTick(10), tickmap.GetMaxTick(10))
	if position.TokensOwed0.Cmp(big.NewInt(499999999999999)) != 0 ||
		position.TokensOwed1.Cmp(big.NewInt(499999999999999)) != 0 {
		t.Fatalf("owed=%s/%s", position.TokensOwed0, position.TokensOwed1)
	}
}

func overflowProfiler(t *testing.T) *PoolProfiler {
	t.Helper()
	profiler := emptyProfiler(t, 500, 10, FeeModelUniswap)
	maximum := new(big.Int).Sub(analysisQ256, big.NewInt(1))
	profiler.SetFeeGrowthGlobal(maximum, maximum)
	if err := profiler.Mint("lp", tickmap.GetMinTick(10), tickmap.GetMaxTick(10), analysisExpand18(10), nil, nil, 1); err != nil {
		t.Fatal(err)
	}
	return profiler
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/tests.rs:2514
//	test: test_flash_increases_fee_growth_by_expected_amount
func TestPoolAnalysisFlashIncreasesFeeGrowthByExpectedAmount(t *testing.T) {
	profiler := mediumProfiler(t)
	profiler.ExecuteFlash(big.NewInt(4), big.NewInt(7), 2)
	want0 := new(big.Int).Quo(new(big.Int).Mul(big.NewInt(4), analysisQ128), analysisExpand18(2))
	want1 := new(big.Int).Quo(new(big.Int).Mul(big.NewInt(7), analysisQ128), analysisExpand18(2))
	if profiler.State.FeeGrowthGlobal0.Cmp(want0) != 0 ||
		profiler.State.FeeGrowthGlobal1.Cmp(want1) != 0 || profiler.Analytics.Flashes != 1 {
		t.Fatalf("growth=%s/%s want=%s/%s", profiler.State.FeeGrowthGlobal0, profiler.State.FeeGrowthGlobal1, want0, want1)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/tests.rs:2546
//	test: test_swap_crossing_tick_down_activates_position
func TestPoolAnalysisSwapCrossingTickDownActivatesPosition(t *testing.T) {
	profiler := uniProfiler(t)
	if err := profiler.Mint("lp", tickmap.GetMinTick(60), -23040, big.NewInt(50000), nil, nil, 2); err != nil {
		t.Fatal(err)
	}
	before := analysisCopyBig(profiler.State.Liquidity)
	profiler.ApplySwap(profiler.QuoteSwap(analysisExpand18(1), true), 3)
	if profiler.State.CurrentTick > -23040 ||
		profiler.State.Liquidity.Cmp(new(big.Int).Add(before, big.NewInt(50000))) != 0 ||
		profiler.ActivePositionCount() != 2 {
		t.Fatalf("tick=%d liquidity=%s active=%d", profiler.State.CurrentTick, profiler.State.Liquidity, profiler.ActivePositionCount())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/tests.rs:2612
//	test: test_swap_crossing_tick_up_activates_position
func TestPoolAnalysisSwapCrossingTickUpActivatesPosition(t *testing.T) {
	profiler := uniProfiler(t)
	if err := profiler.Mint("lp", -22980, tickmap.GetMaxTick(60), big.NewInt(40000), nil, nil, 2); err != nil {
		t.Fatal(err)
	}
	before := analysisCopyBig(profiler.State.Liquidity)
	profiler.ApplySwap(profiler.QuoteSwap(analysisExpand18(1000), false), 3)
	if profiler.State.CurrentTick < -22980 ||
		profiler.State.Liquidity.Cmp(new(big.Int).Add(before, big.NewInt(40000))) != 0 ||
		profiler.ActivePositionCount() != 2 || profiler.Analytics.Swaps != 1 {
		t.Fatalf("tick=%d liquidity=%s active=%d", profiler.State.CurrentTick, profiler.State.Liquidity, profiler.ActivePositionCount())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/tests.rs:2981
//	test: test_swaps_for_pool_high_fee_1on1_price_2e18_max_liquidity
func TestPoolAnalysisSwapsForHighFeeOneToOnePool(t *testing.T) {
	profiler := emptyProfiler(t, 10000, 200, FeeModelUniswap)
	if err := profiler.Mint("lp", tickmap.GetMinTick(200), tickmap.GetMaxTick(200),
		tickmap.TickSpacingToMaxLiquidityPerTick(200), nil, nil, 1); err != nil {
		t.Fatal(err)
	}
	for _, zeroForOne := range []bool{true, false} {
		quote := profiler.QuoteSwap(analysisExpand18(1), zeroForOne)
		if quote.LPFee.Sign() <= 0 || quote.Amount0.Sign() == quote.Amount1.Sign() ||
			quote.LPFee.Cmp(new(big.Int).Add(quote.LPFee, quote.ProtocolFee)) > 0 {
			t.Fatalf("quote=%#v", quote)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/tests.rs:2986
//	test: test_size_for_impact_bps_validation
func TestPoolAnalysisSizeForImpactBPSValidation(t *testing.T) {
	profiler := mediumProfiler(t)
	for _, target := range []uint32{100, 500, 1000} {
		for _, direction := range []bool{true, false} {
			estimate, err := profiler.SizeForImpactBPS(target, direction)
			if err != nil || estimate.Size.Sign() <= 0 || estimate.TargetBPS != target {
				t.Fatalf("target=%d direction=%v estimate=%#v err=%v", target, direction, estimate, err)
			}
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/tests.rs:3026
//	test: test_process_mint_overflow_leaves_state_unchanged
func TestPoolAnalysisProcessMintOverflowLeavesStateUnchanged(t *testing.T) {
	profiler := emptyProfiler(t, 500, 10, FeeModelUniswap)
	profiler.State.Liquidity.Sub(analysisMaxU128, big.NewInt(10))
	before := cloneState(profiler.State)
	err := profiler.Mint("lp", -10, 10, big.NewInt(100), nil, nil, 2)
	typed := requireAnalysisError(t, err, "liquidity_overflow")
	if typed.Event != AnalysisMint || !reflect.DeepEqual(before, profiler.State) ||
		len(profiler.Ticks) != 0 || len(profiler.Positions) != 0 || profiler.Analytics.Mints != 0 {
		t.Fatalf("error=%#v state=%#v", typed, profiler.State)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/tests.rs:3087
//	test: test_process_burn_underflow_leaves_state_unchanged
func TestPoolAnalysisProcessBurnUnderflowLeavesStateUnchanged(t *testing.T) {
	profiler := emptyProfiler(t, 500, 10, FeeModelUniswap)
	if err := profiler.Mint("lp", -10, 10, big.NewInt(100), nil, nil, 1); err != nil {
		t.Fatal(err)
	}
	profiler.State.Liquidity.SetInt64(0)
	beforeTicks, beforePositions, beforeBurns := cloneTicks(profiler.Ticks), clonePositions(profiler.Positions), profiler.Analytics.Burns
	err := profiler.Burn("lp", -10, 10, big.NewInt(100), nil, nil, 2)
	typed := requireAnalysisError(t, err, "liquidity_underflow")
	if typed.Event != AnalysisBurn || !reflect.DeepEqual(beforeTicks, profiler.Ticks) ||
		!reflect.DeepEqual(beforePositions, profiler.Positions) || profiler.Analytics.Burns != beforeBurns {
		t.Fatalf("error=%#v", typed)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/tests.rs:3163
//	test: test_process_burn_tick_underflow_leaves_state_unchanged
func TestPoolAnalysisProcessBurnTickUnderflowLeavesStateUnchanged(t *testing.T) {
	profiler := emptyProfiler(t, 500, 10, FeeModelUniswap)
	if err := profiler.Mint("lp", -10, 10, big.NewInt(100), nil, nil, 1); err != nil {
		t.Fatal(err)
	}
	tick := cloneTick(profiler.Ticks[-10])
	tick.LiquidityGross.SetInt64(0)
	profiler.Ticks[-10] = tick
	before := clonePositions(profiler.Positions)
	err := profiler.Burn("lp", -10, 10, big.NewInt(100), nil, nil, 2)
	requireAnalysisError(t, err, "liquidity_underflow")
	if !reflect.DeepEqual(before, profiler.Positions) || profiler.Analytics.Burns != 0 {
		t.Fatalf("positions mutated: %#v", profiler.Positions)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/tests.rs:3238
//	test: test_wrap_liquidity_error_rewraps_math_overflow
func TestPoolAnalysisWrapLiquidityErrorRewrapsMathOverflow(t *testing.T) {
	err := WrapLiquidityError(&tickmap.LiquidityOverflowError{Current: big.NewInt(1), Delta: big.NewInt(2)}, AnalysisMint)
	typed := requireAnalysisError(t, err, "liquidity_overflow")
	if typed.Current.Cmp(big.NewInt(1)) != 0 || typed.Delta.Cmp(big.NewInt(2)) != 0 || typed.Event != AnalysisMint {
		t.Fatalf("error=%#v", typed)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/tests.rs:3277
//	test: test_wrap_liquidity_error_passes_through_unrelated_anyhow
func TestPoolAnalysisWrapLiquidityErrorPassesThroughUnrelatedError(t *testing.T) {
	source := errors.New("totally unrelated failure")
	if got := WrapLiquidityError(source, AnalysisSwap); got != source {
		t.Fatalf("got=%v want identity", got)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/tests.rs:3338
//	test: test_simulate_replays_min_boundary_swap_through_empty_range
func TestPoolAnalysisSimulateReplaysMinBoundarySwapThroughEmptyRange(t *testing.T) {
	profiler := minBoundaryProfiler(t)
	quote := profiler.SimulateSwapThroughTicks(big.NewInt(27), true, big.NewInt(4295128740), true)
	if quote.SqrtPriceAfter.Cmp(big.NewInt(4295128740)) != 0 || quote.TickAfter != -887272 ||
		quote.LiquidityAfter.Sign() != 0 || quote.Amount0.Cmp(big.NewInt(27)) != 0 ||
		quote.Amount1.Cmp(big.NewInt(-12402)) != 0 {
		t.Fatalf("quote=%#v", quote)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/tests.rs:3357
//	test: test_simulate_replays_max_boundary_swap_through_empty_range
func TestPoolAnalysisSimulateReplaysMaxBoundarySwapThroughEmptyRange(t *testing.T) {
	limit := analysisBI("1461446703485210103287273052203988822378723970341")
	quote := maxBoundaryProfiler(t).SimulateSwapThroughTicks(big.NewInt(454791), false, limit, true)
	if quote.SqrtPriceAfter.Cmp(limit) != 0 || quote.TickAfter != 887271 ||
		quote.LiquidityAfter.Sign() != 0 || quote.Amount0.Cmp(big.NewInt(-1596)) != 0 ||
		quote.Amount1.Cmp(big.NewInt(454791)) != 0 {
		t.Fatalf("quote=%#v", quote)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/tests.rs:3383
//	test: test_simulate_forward_stops_at_empty_range_boundary
func TestPoolAnalysisSimulateForwardStopsAtEmptyRangeBoundary(t *testing.T) {
	limit := big.NewInt(4295128740)
	quote := minBoundaryProfiler(t).SimulateSwapThroughTicks(big.NewInt(27), true, limit, false)
	if quote.SqrtPriceAfter.Cmp(limit) <= 0 || quote.TickAfter != 61929 || quote.LiquidityAfter.Sign() != 0 {
		t.Fatalf("quote=%#v", quote)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/tests.rs:3398
//	test: test_simulate_traverse_is_noop_when_swap_stays_liquid
func TestPoolAnalysisSimulateTraverseIsNoopWhenSwapStaysLiquid(t *testing.T) {
	profiler := lowProfiler(t)
	limit := big.NewInt(4295128740)
	forward := profiler.SimulateSwapThroughTicks(big.NewInt(1000), true, limit, false)
	replay := profiler.SimulateSwapThroughTicks(big.NewInt(1000), true, limit, true)
	if !reflect.DeepEqual(forward, replay) || replay.SqrtPriceAfter.Cmp(big.NewInt(4295128740)) <= 0 {
		t.Fatalf("forward=%#v replay=%#v", forward, replay)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/tests.rs:3419
//	test: test_process_swap_replays_min_boundary_to_event_state
func TestPoolAnalysisProcessSwapReplaysMinBoundaryToEventState(t *testing.T) {
	profiler := minBoundaryProfiler(t)
	quote := profiler.SimulateSwapThroughTicks(big.NewInt(27), true, big.NewInt(4295128740), true)
	profiler.ProcessSwapEvent(quote, quote.SqrtPriceAfter, quote.TickAfter, quote.LiquidityAfter, 2)
	if profiler.State.CurrentTick != -887272 || profiler.State.SqrtPrice.Cmp(big.NewInt(4295128740)) != 0 ||
		profiler.State.Liquidity.Sign() != 0 {
		t.Fatalf("state=%#v", profiler.State)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/tests.rs:3455
//	test: test_simulate_replay_stops_when_empty_walk_re_enters_liquidity
func TestPoolAnalysisSimulateReplayStopsWhenEmptyWalkReentersLiquidity(t *testing.T) {
	profiler := minBoundaryProfiler(t)
	if err := profiler.Mint("lp", 50000, 50060, big.NewInt(500000), nil, nil, 2); err != nil {
		t.Fatal(err)
	}
	quote := profiler.SimulateSwapThroughTicks(big.NewInt(27), true, big.NewInt(4295128740), true)
	if quote.TickAfter != 50059 || quote.LiquidityAfter.Cmp(big.NewInt(500000)) != 0 ||
		quote.SqrtPriceAfter.Cmp(big.NewInt(4295128740)) <= 0 || quote.Amount0.Cmp(big.NewInt(27)) != 0 {
		t.Fatalf("quote=%#v", quote)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/tests.rs:3494
//	test: test_swap_protocol_fee_split_matches_fee_protocol
func TestPoolAnalysisSwapProtocolFeeSplitMatchesFeeProtocol(t *testing.T) {
	profiler := mediumProfiler(t)
	without := profiler.QuoteSwap(big.NewInt(1000000000), true)
	total := analysisCopyBig(without.LPFee)
	profiler.SetFeeProtocol(4, 4, false, 2)
	with := profiler.QuoteSwap(big.NewInt(1000000000), true)
	wantProtocol := new(big.Int).Quo(total, big.NewInt(4))
	if with.ProtocolFee.Cmp(wantProtocol) != 0 ||
		new(big.Int).Add(with.ProtocolFee, with.LPFee).Cmp(total) != 0 {
		t.Fatalf("without=%#v with=%#v", without, with)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/tests.rs:3526
//	test: test_pancakeswap_swap_protocol_fee_split_uses_basis_points
func TestPoolAnalysisPancakeSwapProtocolFeeSplitUsesBasisPoints(t *testing.T) {
	profiler := mediumProfiler(t)
	without := profiler.QuoteSwap(big.NewInt(1000000000), true)
	total := analysisCopyBig(without.LPFee)
	profiler.SetFeeProtocol(3200, 4000, true, 2)
	with := profiler.QuoteSwap(big.NewInt(1000000000), true)
	wantProtocol := new(big.Int).Quo(new(big.Int).Mul(total, big.NewInt(3200)), big.NewInt(10000))
	if with.ProtocolFee.Cmp(wantProtocol) != 0 ||
		new(big.Int).Add(with.ProtocolFee, with.LPFee).Cmp(total) != 0 {
		t.Fatalf("without=%#v with=%#v", without, with)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/tests.rs:3554
//	test: test_fee_replay_sequence_accrues_then_collects
func TestPoolAnalysisFeeReplaySequenceAccruesThenCollects(t *testing.T) {
	profiler := initializedProfiler(t)
	if err := profiler.Mint("lp", tickmap.GetMinTick(60), tickmap.GetMaxTick(60), big.NewInt(1000000000), nil, nil, 1); err != nil {
		t.Fatal(err)
	}
	profiler.SetFeeProtocol(4, 4, false, 2)
	quote := profiler.QuoteSwap(big.NewInt(100000000), true)
	profiler.ApplySwap(quote, 3)
	accrued := analysisCopyBig(profiler.State.ProtocolFeesToken0)
	if accrued.Sign() <= 0 || quote.Amount0.Sign() <= 0 {
		t.Fatalf("accrued=%s quote=%#v", accrued, quote)
	}
	profiler.CollectProtocol(new(big.Int).Sub(accrued, big.NewInt(1)), new(big.Int), 4)
	if profiler.State.ProtocolFeesToken0.Cmp(big.NewInt(1)) != 0 ||
		profiler.State.ProtocolFeesToken1.Sign() != 0 {
		t.Fatalf("fees=%s/%s", profiler.State.ProtocolFeesToken0, profiler.State.ProtocolFeesToken1)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/tests.rs:3602
//	test: test_profilers_replay_independently_across_threads
func TestPoolAnalysisProfilersReplayIndependentlyAcrossThreads(t *testing.T) {
	replay := func(index int) [3]string {
		profiler := emptyProfiler(t, 500, 10, FeeModelUniswap)
		liquidity := new(big.Int).Mul(big.NewInt(1000000000), big.NewInt(int64(index+1)))
		if err := profiler.Mint("lp", tickmap.GetMinTick(10), tickmap.GetMaxTick(10), liquidity, nil, nil, 1); err != nil {
			t.Fatal(err)
		}
		amount := new(big.Int).Mul(big.NewInt(50000000), big.NewInt(int64(index+1)))
		profiler.ApplySwap(profiler.QuoteSwap(amount, false), 2)
		return [3]string{big.NewInt(int64(profiler.State.CurrentTick)).String(), profiler.State.Liquidity.String(), profiler.State.SqrtPrice.String()}
	}
	sequential := make([][3]string, 4)
	for i := range sequential {
		sequential[i] = replay(i)
	}
	parallel := make([][3]string, 4)
	var wait sync.WaitGroup
	for i := range parallel {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			parallel[index] = replay(index)
		}(i)
	}
	wait.Wait()
	if !reflect.DeepEqual(sequential, parallel) {
		t.Fatalf("sequential=%v parallel=%v", sequential, parallel)
	}
	distinct := make(map[[3]string]struct{})
	for _, result := range sequential {
		distinct[result] = struct{}{}
	}
	if len(distinct) <= 1 {
		t.Fatalf("results not distinct: %v", sequential)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/pool_analysis/tests.rs:3648
//	test: test_swap_crossing_multiple_ticks_conserves_fees
func TestPoolAnalysisSwapCrossingMultipleTicksConservesFees(t *testing.T) {
	profiler := emptyProfiler(t, 3000, 60, FeeModelUniswap)
	if err := profiler.Mint("lp", tickmap.GetMinTick(60), tickmap.GetMaxTick(60), big.NewInt(1000000000), nil, nil, 1); err != nil {
		t.Fatal(err)
	}
	for _, bounds := range [][2]int32{{600, 1200}, {1800, 2400}} {
		if err := profiler.Mint("lp", bounds[0], bounds[1], big.NewInt(2000000000), nil, nil, 2); err != nil {
			t.Fatal(err)
		}
	}
	amount := analysisBI("5000000000000000000")
	without := profiler.QuoteSwap(amount, false)
	if len(without.CrossedTicks) < 2 || without.ProtocolFee.Sign() != 0 {
		t.Fatalf("quote=%#v", without)
	}
	total := analysisCopyBig(without.LPFee)
	profiler.SetFeeProtocol(4, 4, false, 3)
	with := profiler.QuoteSwap(amount, false)
	if len(with.CrossedTicks) != len(without.CrossedTicks) || with.ProtocolFee.Sign() <= 0 ||
		new(big.Int).Add(with.ProtocolFee, with.LPFee).Cmp(total) != 0 {
		t.Fatalf("without=%#v with=%#v", without, with)
	}
}
