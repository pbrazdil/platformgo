package decimal

import (
	"encoding/json"
	"fmt"
	"math/big"
	"math/rand/v2"
	"strings"
	"testing"
)

const propertyCases = 4096

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:1732
//	test: prop_price_serde_round_trip
//
// Adaptations:
//   - Seeded exact-decimal generation replaces floating-point proptest input.
func TestPricePropertySerializationRoundTrip(t *testing.T) {
	random := propertyRandom(1732)
	for range propertyCases {
		original := randomPrice(random)
		fromString := MustPrice(original.String())
		if fromString.Precision() != original.Precision() || !fromString.Equal(original) {
			t.Fatalf("string round trip: %s -> %s", original, fromString)
		}
		data, err := json.Marshal(original)
		if err != nil {
			t.Fatal(err)
		}
		var fromJSON Price
		if err := json.Unmarshal(data, &fromJSON); err != nil {
			t.Fatal(err)
		}
		if fromJSON.Precision() != original.Precision() || !fromJSON.Equal(original) {
			t.Fatalf("JSON round trip: %s -> %s", original, fromJSON)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:1753
//	test: prop_price_arithmetic_associative
func TestPricePropertyArithmeticAssociative(t *testing.T) {
	random := propertyRandom(1753)
	for range propertyCases {
		first, second, third := randomPrice(random), randomPrice(random), randomPrice(random)
		leftPair, ok := first.Add(second)
		if !ok {
			continue
		}
		left, leftOK := leftPair.Add(third)
		rightPair, ok := second.Add(third)
		if !ok {
			continue
		}
		right, rightOK := first.Add(rightPair)
		if !leftOK || !rightOK || !left.Equal(right) {
			t.Fatalf("associativity failed: (%s + %s) + %s", first, second, third)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:1779
//	test: prop_price_addition_subtraction_inverse
func TestPricePropertyAdditionSubtractionInverse(t *testing.T) {
	random := propertyRandom(1779)
	for range propertyCases {
		base, delta := randomPrice(random), randomPrice(random)
		sum, ok := base.Add(delta)
		if !ok {
			continue
		}
		reconstructed, ok := sum.Sub(delta)
		if !ok || !reconstructed.Equal(base) {
			t.Fatalf("(%s + %s) - %s = %s", base, delta, delta, reconstructed)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:1798
//	test: prop_price_ordering_transitive
func TestPricePropertyOrderingTransitive(t *testing.T) {
	random := propertyRandom(1798)
	for range propertyCases {
		first, second, third := randomPrice(random), randomPrice(random), randomPrice(random)
		if first.Cmp(second) <= 0 && second.Cmp(third) <= 0 && first.Cmp(third) > 0 {
			t.Fatalf("transitivity failed: %s <= %s <= %s", first, second, third)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:1817
//	test: prop_price_string_parsing_precision
func TestPricePropertyStringParsingPrecision(t *testing.T) {
	random := propertyRandom(1817)
	for range propertyCases {
		text, precision := randomFixedString(random, true)
		price := MustPrice(text)
		if price.Precision() != precision || price.String() != text {
			t.Fatalf("ParsePrice(%s) = %s precision %d", text, price, price.Precision())
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:1839
//	test: prop_price_arithmetic_bounds
func TestPricePropertyArithmeticBounds(t *testing.T) {
	random := propertyRandom(1839)
	for range propertyCases {
		first, second := randomPrice(random), randomPrice(random)
		if sum, ok := first.Add(second); ok && (sum.Decimal().Cmp(minPrice) < 0 || sum.Decimal().Cmp(maxPrice) > 0) {
			t.Fatalf("sum escaped bounds: %s + %s = %s", first, second, sum)
		}
		if difference, ok := first.Sub(second); ok &&
			(difference.Decimal().Cmp(minPrice) < 0 || difference.Decimal().Cmp(maxPrice) > 0) {
			t.Fatalf("difference escaped bounds: %s - %s = %s", first, second, difference)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:1867
//	test: prop_price_checked_add_matches_spec
func TestPricePropertyCheckedAddMatchesSpec(t *testing.T) {
	random := propertyRandom(1867)
	for range propertyCases {
		first, second := randomPrice(random), randomPrice(random)
		expected := first.Decimal().Add(second.Decimal())
		wantOK := expected.Cmp(minPrice) >= 0 && expected.Cmp(maxPrice) <= 0
		got, gotOK := first.Add(second)
		if gotOK != wantOK || (gotOK && !got.Decimal().Equal(expected)) {
			t.Fatalf("%s + %s = %s,%v; expected %s,%v", first, second, got, gotOK, expected, wantOK)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:1885
//	test: prop_price_checked_sub_matches_spec
func TestPricePropertyCheckedSubMatchesSpec(t *testing.T) {
	random := propertyRandom(1885)
	for range propertyCases {
		first, second := randomPrice(random), randomPrice(random)
		expected := first.Decimal().Sub(second.Decimal())
		wantOK := expected.Cmp(minPrice) >= 0 && expected.Cmp(maxPrice) <= 0
		got, gotOK := first.Sub(second)
		if gotOK != wantOK || (gotOK && !got.Decimal().Equal(expected)) {
			t.Fatalf("%s - %s = %s,%v; expected %s,%v", first, second, got, gotOK, expected, wantOK)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:1904
//	test: prop_price_as_decimal_preserves_precision
func TestPricePropertyDecimalPreservesPrecision(t *testing.T) {
	random := propertyRandom(1904)
	for range propertyCases {
		price := randomExtremePrice(random)
		if price.Decimal().Scale() != price.Precision() {
			t.Fatalf("%s decimal scale %d != precision %d", price, price.Decimal().Scale(), price.Precision())
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:1915
//	test: prop_price_as_decimal_matches_display
func TestPricePropertyDecimalMatchesDisplay(t *testing.T) {
	random := propertyRandom(1915)
	for range propertyCases {
		price := randomPrice(random)
		if price.Decimal().String() != price.String() {
			t.Fatalf("decimal %s != display %s", price.Decimal(), price)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:1928
//	test: prop_price_from_decimal_roundtrip
func TestPricePropertyFromDecimalRoundTrip(t *testing.T) {
	random := propertyRandom(1928)
	for range propertyCases {
		original := randomExtremePrice(random)
		reconstructed, err := priceFromDecimal(original.Decimal())
		if err != nil || reconstructed.Precision() != original.Precision() || !reconstructed.Equal(original) {
			t.Fatalf("decimal round trip: %s -> %s, %v", original, reconstructed, err)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/price.rs:1941
//	test: prop_price_from_raw_round_trip
//
// Adaptations:
//   - Go stores the exact coefficient directly instead of exposing a fixed raw integer.
func TestPricePropertyFromRawRoundTrip(t *testing.T) {
	random := propertyRandom(1941)
	for range propertyCases {
		original := randomExtremePrice(random)
		reconstructed, err := priceFromDecimal(newDecimal(original.value.coefficientCopy(), original.Precision()))
		if err != nil || reconstructed.Precision() != original.Precision() || !reconstructed.Equal(original) {
			t.Fatalf("coefficient round trip: %s -> %s, %v", original, reconstructed, err)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:1936
//	test: prop_quantity_serde_round_trip
func TestQuantityPropertySerializationRoundTrip(t *testing.T) {
	random := propertyRandom(1936)
	for range propertyCases {
		original := randomExtremeQuantity(random)
		fromString := MustQuantity(original.String())
		if fromString.Precision() != original.Precision() || !fromString.Equal(original) {
			t.Fatalf("string round trip: %s -> %s", original, fromString)
		}
		data, err := json.Marshal(original)
		if err != nil {
			t.Fatal(err)
		}
		var fromJSON Quantity
		if err := json.Unmarshal(data, &fromJSON); err != nil {
			t.Fatal(err)
		}
		if fromJSON.Precision() != original.Precision() || !fromJSON.Equal(original) {
			t.Fatalf("JSON round trip: %s -> %s", original, fromJSON)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:1959
//	test: prop_quantity_arithmetic_associative
func TestQuantityPropertyArithmeticAssociative(t *testing.T) {
	random := propertyRandom(1959)
	for range propertyCases {
		first, second, third := randomQuantity(random), randomQuantity(random), randomQuantity(random)
		leftPair, ok := first.Add(second)
		if !ok {
			continue
		}
		left, leftOK := leftPair.Add(third)
		rightPair, ok := second.Add(third)
		if !ok {
			continue
		}
		right, rightOK := first.Add(rightPair)
		if !leftOK || !rightOK || !left.Equal(right) {
			t.Fatalf("quantity associativity failed")
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:1985
//	test: prop_quantity_addition_subtraction_inverse
func TestQuantityPropertyAdditionSubtractionInverse(t *testing.T) {
	random := propertyRandom(1985)
	for range propertyCases {
		base, delta := randomQuantity(random), randomQuantity(random)
		sum, ok := base.Add(delta)
		if !ok {
			continue
		}
		reconstructed, ok := sum.Sub(delta)
		if !ok || !reconstructed.Equal(base) {
			t.Fatalf("(%s + %s) - %s = %s", base, delta, delta, reconstructed)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:2006
//	test: prop_quantity_checked_add_matches_spec
func TestQuantityPropertyCheckedAddMatchesSpec(t *testing.T) {
	random := propertyRandom(2006)
	for range propertyCases {
		first, second := randomQuantity(random), randomQuantity(random)
		expected := first.Decimal().Add(second.Decimal())
		wantOK := expected.Cmp(maxQuantity) <= 0
		got, gotOK := first.Add(second)
		if gotOK != wantOK || (gotOK && !got.Decimal().Equal(expected)) {
			t.Fatalf("%s + %s = %s,%v; expected %s,%v", first, second, got, gotOK, expected, wantOK)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:2024
//	test: prop_quantity_checked_sub_matches_spec
func TestQuantityPropertyCheckedSubMatchesSpec(t *testing.T) {
	random := propertyRandom(2024)
	for range propertyCases {
		first, second := randomQuantity(random), randomQuantity(random)
		expected := first.Decimal().Sub(second.Decimal())
		wantOK := expected.Sign() >= 0
		got, gotOK := first.Sub(second)
		if gotOK != wantOK || (gotOK && !got.Decimal().Equal(expected)) {
			t.Fatalf("%s - %s = %s,%v; expected %s,%v", first, second, got, gotOK, expected, wantOK)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:2040
//	test: prop_quantity_ordering_transitive
func TestQuantityPropertyOrderingTransitive(t *testing.T) {
	random := propertyRandom(2040)
	for range propertyCases {
		first, second, third := randomQuantity(random), randomQuantity(random), randomQuantity(random)
		if first.Cmp(second) <= 0 && second.Cmp(third) <= 0 && first.Cmp(third) > 0 {
			t.Fatalf("transitivity failed: %s <= %s <= %s", first, second, third)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:2059
//	test: prop_quantity_string_parsing_precision
func TestQuantityPropertyStringParsingPrecision(t *testing.T) {
	random := propertyRandom(2059)
	for range propertyCases {
		text, precision := randomFixedString(random, false)
		quantity := MustQuantity(text)
		if quantity.Precision() != precision || quantity.String() != text {
			t.Fatalf("ParseQuantity(%s) = %s precision %d", text, quantity, quantity.Precision())
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:2081
//	test: prop_quantity_arithmetic_bounds
func TestQuantityPropertyArithmeticBounds(t *testing.T) {
	random := propertyRandom(2081)
	for range propertyCases {
		first, second := randomQuantity(random), randomQuantity(random)
		if sum, ok := first.Add(second); ok && (sum.Decimal().Sign() < 0 || sum.Decimal().Cmp(maxQuantity) > 0) {
			t.Fatalf("sum escaped bounds: %s + %s = %s", first, second, sum)
		}
		if difference, ok := first.Sub(second); ok &&
			(difference.Decimal().Sign() < 0 || difference.Decimal().Cmp(maxQuantity) > 0) {
			t.Fatalf("difference escaped bounds: %s - %s = %s", first, second, difference)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:2108
//	test: prop_quantity_multiplication_non_negative
func TestQuantityPropertyMultiplicationNonNegative(t *testing.T) {
	random := propertyRandom(2108)
	for range propertyCases {
		first := smallQuantity(random)
		second := smallQuantity(random)
		product, ok := first.Mul(second)
		if !ok || product.Decimal().Sign() < 0 {
			t.Fatalf("%s * %s = %s,%v", first, second, product, ok)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:2132
//	test: prop_quantity_zero_addition_identity
func TestQuantityPropertyZeroAdditionIdentity(t *testing.T) {
	random := propertyRandom(2132)
	for range propertyCases {
		quantity := randomQuantity(random)
		zero, _ := ZeroQuantity(quantity.Precision())
		left, leftOK := quantity.Add(zero)
		right, rightOK := zero.Add(quantity)
		if !leftOK || !rightOK || !left.Equal(quantity) || !right.Equal(quantity) {
			t.Fatalf("zero identity failed for %s", quantity)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:2148
//	test: prop_quantity_as_decimal_preserves_precision
func TestQuantityPropertyDecimalPreservesPrecision(t *testing.T) {
	random := propertyRandom(2148)
	for range propertyCases {
		quantity := randomExtremeQuantity(random)
		if quantity.Decimal().Scale() != quantity.Precision() {
			t.Fatalf("%s decimal scale %d != precision %d", quantity, quantity.Decimal().Scale(), quantity.Precision())
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:2159
//	test: prop_quantity_as_decimal_matches_display
func TestQuantityPropertyDecimalMatchesDisplay(t *testing.T) {
	random := propertyRandom(2159)
	for range propertyCases {
		quantity := randomExtremeQuantity(random)
		if quantity.Decimal().String() != quantity.String() {
			t.Fatalf("decimal %s != display %s", quantity.Decimal(), quantity)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:2171
//	test: prop_quantity_from_decimal_roundtrip
func TestQuantityPropertyFromDecimalRoundTrip(t *testing.T) {
	random := propertyRandom(2171)
	for range propertyCases {
		original := randomExtremeQuantity(random)
		reconstructed, err := quantityFromDecimal(original.Decimal())
		if err != nil || reconstructed.Precision() != original.Precision() || !reconstructed.Equal(original) {
			t.Fatalf("decimal round trip: %s -> %s, %v", original, reconstructed, err)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/quantity.rs:2184
//	test: prop_quantity_from_raw_round_trip
//
// Adaptations:
//   - Go stores the exact coefficient directly instead of exposing a fixed raw integer.
func TestQuantityPropertyFromRawRoundTrip(t *testing.T) {
	random := propertyRandom(2184)
	for range propertyCases {
		original := randomExtremeQuantity(random)
		reconstructed, err := quantityFromDecimal(newDecimal(original.value.coefficientCopy(), original.Precision()))
		if err != nil || reconstructed.Precision() != original.Precision() || !reconstructed.Equal(original) {
			t.Fatalf("coefficient round trip: %s -> %s, %v", original, reconstructed, err)
		}
	}
}

func propertyRandom(seed uint64) *rand.Rand {
	return rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
}

func randomPrice(random *rand.Rand) Price {
	scale := uint8(random.Uint64N(uint64(MaxPrecision) + 1))
	coefficient := randomReasonableCoefficient(random, scale)
	if random.Uint64N(2) == 1 {
		coefficient.Neg(coefficient)
	}
	price, err := priceFromDecimal(newDecimal(coefficient, scale))
	if err != nil {
		panic(err)
	}
	return price
}

func randomQuantity(random *rand.Rand) Quantity {
	scale := uint8(random.Uint64N(uint64(MaxPrecision) + 1))
	quantity, err := quantityFromDecimal(newDecimal(randomReasonableCoefficient(random, scale), scale))
	if err != nil {
		panic(err)
	}
	return quantity
}

func smallQuantity(random *rand.Rand) Quantity {
	scale := uint8(random.Uint64N(uint64(MaxPrecision) + 1))
	scaleFactor := powerOfTen(uint32(scale))
	coefficient := new(big.Int).SetUint64(1 + random.Uint64N(9_000_000))
	if scale > 6 {
		coefficient.Mul(coefficient, powerOfTen(uint32(scale-6)))
	} else if scale < 6 {
		coefficient.Quo(coefficient, powerOfTen(uint32(6-scale)))
	}
	coefficient.Mod(coefficient, new(big.Int).Mul(big.NewInt(10), scaleFactor))
	if coefficient.Sign() == 0 {
		coefficient.SetInt64(1)
	}
	quantity, err := quantityFromDecimal(newDecimal(coefficient, scale))
	if err != nil {
		panic(err)
	}
	return quantity
}

func randomReasonableCoefficient(random *rand.Rand, scale uint8) *big.Int {
	integral := new(big.Int).SetUint64(random.Uint64N(1_000_001))
	coefficient := new(big.Int).Mul(integral, powerOfTen(uint32(scale)))
	if scale == 0 {
		return coefficient
	}
	fractional := new(big.Int).SetUint64(random.Uint64())
	fractional.Mod(fractional, powerOfTen(uint32(scale)))
	return coefficient.Add(coefficient, fractional)
}

func randomExtremePrice(random *rand.Rand) Price {
	scale := uint8(random.Uint64N(uint64(MaxPrecision) + 1))
	bound := new(big.Int).Mul(maxPrice.coefficientCopy(), powerOfTen(uint32(scale)))
	coefficient := randomBelow(random, new(big.Int).Add(bound, big.NewInt(1)))
	if random.Uint64N(2) == 1 {
		coefficient.Neg(coefficient)
	}
	price, err := priceFromDecimal(newDecimal(coefficient, scale))
	if err != nil {
		panic(err)
	}
	return price
}

func randomExtremeQuantity(random *rand.Rand) Quantity {
	scale := uint8(random.Uint64N(uint64(MaxPrecision) + 1))
	bound := new(big.Int).Mul(maxQuantity.coefficientCopy(), powerOfTen(uint32(scale)))
	coefficient := randomBelow(random, new(big.Int).Add(bound, big.NewInt(1)))
	quantity, err := quantityFromDecimal(newDecimal(coefficient, scale))
	if err != nil {
		panic(err)
	}
	return quantity
}

func randomBelow(random *rand.Rand, limit *big.Int) *big.Int {
	value := new(big.Int).SetUint64(random.Uint64())
	value.Lsh(value, 64)
	value.Or(value, new(big.Int).SetUint64(random.Uint64()))
	return value.Mod(value, limit)
}

func randomFixedString(random *rand.Rand, signed bool) (string, uint8) {
	precision := uint8(1 + random.Uint64N(uint64(MaxPrecision)))
	integral := random.Uint64N(1_000_000)
	fractional := new(big.Int).SetUint64(random.Uint64())
	fractional.Mod(fractional, powerOfTen(uint32(precision)))
	fraction := fractional.String()
	text := fmt.Sprintf("%d.%s", integral, strings.Repeat("0", int(precision)-len(fraction))+fraction)
	if signed && random.Uint64N(2) == 1 {
		text = "-" + text
	}
	return text, precision
}
