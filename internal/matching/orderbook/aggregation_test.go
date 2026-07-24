package orderbook

import (
	"math/big"
	"testing"
)

func aggregationInt(text string) *big.Int {
	value, ok := new(big.Int).SetString(text, 10)
	if !ok {
		panic("invalid test integer " + text)
	}
	return value
}

func aggregationPow2(power uint) *big.Int {
	return new(big.Int).Lsh(big.NewInt(1), power)
}

func requireUniqueAggregationID(t *testing.T, seen map[uint64]struct{}, price *big.Int) {
	t.Helper()
	id := PriceToOrderID(price)
	if _, exists := seen[id]; exists {
		t.Fatalf("collision detected for raw price %s (order ID %d)", price, id)
	}
	seen[id] = struct{}{}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/aggregation.rs:92
//	test: test_price_to_order_id_deterministic
func TestAggregationPriceToOrderIDDeterministic(t *testing.T) {
	price1 := aggregationInt("123456789012345678901234567890")
	price2 := aggregationInt("987654321098765432109876543210")
	id1 := PriceToOrderID(price1)
	if got := PriceToOrderID(price1); got != id1 {
		t.Fatalf("same price changed ID: %d then %d", id1, got)
	}
	id2 := PriceToOrderID(price2)
	if id1 == id2 {
		t.Fatalf("different prices shared ID %d", id1)
	}
	for range 100 {
		if PriceToOrderID(price1) != id1 || PriceToOrderID(price2) != id2 {
			t.Fatal("hash was not deterministic across repeated calls")
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/aggregation.rs:116
//	test: test_price_to_order_id_no_collisions
func TestAggregationPriceToOrderIDNoCollisions(t *testing.T) {
	seen := make(map[uint64]struct{}, 1000)
	base := big.NewInt(1_000_000_000)
	for i := int64(0); i < 1000; i++ {
		requireUniqueAggregationID(t, seen, new(big.Int).Add(base, big.NewInt(i)))
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/aggregation.rs:129
//	test: test_price_to_order_id_no_collision_across_64bit_boundary
func TestAggregationPriceToOrderIDNoCollisionAcross64BitBoundary(t *testing.T) {
	price1 := big.NewInt(1)
	price2 := aggregationPow2(64)
	if PriceToOrderID(price1) == PriceToOrderID(price2) {
		t.Fatal("raw prices 1 and 2^64 collided")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/aggregation.rs:144
//	test: test_price_to_order_id_handles_negative_prices
func TestAggregationPriceToOrderIDHandlesNegativePrices(t *testing.T) {
	seen := make(map[uint64]struct{}, 11)
	for _, price := range []*big.Int{
		big.NewInt(-1), big.NewInt(-2), big.NewInt(-100), big.NewInt(-1_000_000_000),
		new(big.Int).Set(minSigned128), new(big.Int).Add(minSigned128, big.NewInt(1)),
		big.NewInt(1), big.NewInt(2), big.NewInt(100), big.NewInt(1_000_000_000),
		new(big.Int).Set(maxSigned128),
	} {
		requireUniqueAggregationID(t, seen, price)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/aggregation.rs:178
//	test: test_price_to_order_id_handles_large_values
func TestAggregationPriceToOrderIDHandlesLargeValues(t *testing.T) {
	u64Max := new(big.Int).Sub(aggregationPow2(64), big.NewInt(1))
	seen := make(map[uint64]struct{}, 7)
	for _, price := range []*big.Int{
		u64Max,
		aggregationPow2(64),
		new(big.Int).Add(u64Max, big.NewInt(1000)),
		aggregationPow2(65),
		aggregationPow2(100),
		new(big.Int).Sub(maxSigned128, big.NewInt(1)),
		new(big.Int).Set(maxSigned128),
	} {
		requireUniqueAggregationID(t, seen, price)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/aggregation.rs:203
//	test: test_price_to_order_id_multiples_of_2_pow_64
func TestAggregationPriceToOrderIDMultiplesOf2Pow64(t *testing.T) {
	seen := make(map[uint64]struct{}, 10)
	unit := aggregationPow2(64)
	for i := int64(0); i < 10; i++ {
		requireUniqueAggregationID(t, seen, new(big.Int).Mul(unit, big.NewInt(i)))
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/aggregation.rs:219
//	test: test_price_to_order_id_realistic_orderbook_prices
func TestAggregationPriceToOrderIDRealisticOrderbookPrices(t *testing.T) {
	seen := make(map[uint64]struct{}, 222_000)
	for i := int64(-1000); i < 1000; i++ {
		requireUniqueAggregationID(t, seen, big.NewInt(50_000_000_000_000+i))
	}
	for i := int64(-10_000); i < 10_000; i++ {
		requireUniqueAggregationID(t, seen, big.NewInt(1_100_000_000+i))
	}
	for i := int64(-100_000); i < 100_000; i++ {
		requireUniqueAggregationID(t, seen, big.NewInt(100_000_000+i))
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/aggregation.rs:258
//	test: test_price_to_order_id_edge_case_patterns
func TestAggregationPriceToOrderIDEdgeCasePatterns(t *testing.T) {
	seen := make(map[uint64]struct{}, 255)
	for power := uint(0); power < 128; power++ {
		price := aggregationPow2(power)
		if power == 127 {
			price = new(big.Int).Set(minSigned128)
		}
		requireUniqueAggregationID(t, seen, price)
	}
	for power := uint(0); power < 127; power++ {
		requireUniqueAggregationID(t, seen, new(big.Int).Neg(aggregationPow2(power)))
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/aggregation.rs:285
//	test: test_price_to_order_id_sequential_negative_values
func TestAggregationPriceToOrderIDSequentialNegativeValues(t *testing.T) {
	seen := make(map[uint64]struct{}, 10_001)
	for i := int64(-10_000); i <= 0; i++ {
		requireUniqueAggregationID(t, seen, big.NewInt(i))
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/aggregation.rs:310
//	test: test_price_to_order_id_extreme_values_no_collision
func TestAggregationPriceToOrderIDExtremeValuesNoCollision(t *testing.T) {
	u64Max := new(big.Int).Sub(aggregationPow2(64), big.NewInt(1))
	seen := make(map[uint64]struct{}, 13)
	for _, price := range []*big.Int{
		new(big.Int).Set(maxSigned128),
		new(big.Int).Sub(maxSigned128, big.NewInt(1)),
		new(big.Int).Set(minSigned128),
		new(big.Int).Add(minSigned128, big.NewInt(1)),
		u64Max,
		new(big.Int).Sub(u64Max, big.NewInt(1)),
		new(big.Int).Add(u64Max, big.NewInt(1)),
		new(big.Int).Neg(u64Max),
		new(big.Int).Sub(new(big.Int).Neg(u64Max), big.NewInt(1)),
		new(big.Int).Add(new(big.Int).Neg(u64Max), big.NewInt(1)),
		big.NewInt(0), big.NewInt(1), big.NewInt(-1),
	} {
		requireUniqueAggregationID(t, seen, price)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/aggregation.rs:324
//	test: test_price_to_order_id_avalanche_effect
func TestAggregationPriceToOrderIDAvalancheEffect(t *testing.T) {
	id1 := PriceToOrderID(big.NewInt(1_000_000_000_000))
	id2 := PriceToOrderID(big.NewInt(1_000_000_000_001))
	if differing := (id1 ^ id2); bitsSet64(differing) < 12 {
		t.Fatalf("poor avalanche: only %d/64 bits differ", bitsSet64(differing))
	}
}

func bitsSet64(value uint64) int {
	count := 0
	for value != 0 {
		value &= value - 1
		count++
	}
	return count
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orderbook/aggregation.rs:344
//	test: test_price_to_order_id_comprehensive_collision_check
func TestAggregationPriceToOrderIDComprehensiveCollisionCheck(t *testing.T) {
	const totalTests = 500_000
	seen := make(map[uint64]struct{}, 210_000)
	collisions := 0
	insert := func(price *big.Int) {
		id := PriceToOrderID(price)
		if _, exists := seen[id]; exists {
			collisions++
		}
		seen[id] = struct{}{}
	}

	for i := int64(-100_000); i < 100_000; i++ {
		insert(big.NewInt(i))
	}
	for power := uint(0); power < 64; power++ {
		base := aggregationPow2(power)
		for offset := int64(-10); offset <= 10; offset++ {
			insert(new(big.Int).Add(base, big.NewInt(offset)))
		}
	}
	for _, base := range []int64{100, 1000, 10_000, 100_000, 1_000_000, 10_000_000} {
		scaled := new(big.Int).Mul(big.NewInt(base), big.NewInt(1_000_000_000))
		for i := int64(0); i < 1000; i++ {
			insert(new(big.Int).Add(scaled, big.NewInt(i)))
		}
	}

	// Compare the exact ratio against 1/1000 without introducing floating point
	// into the deterministic matching package.
	if collisions*1000 >= totalTests {
		t.Fatalf("high collision rate: %d/%d (must be less than 1/1000)", collisions, totalTests)
	}
}
