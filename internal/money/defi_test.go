package money

import (
	"fmt"
	"math/big"
	"strings"
	"testing"

	"github.com/upcomers-org/platformgo/internal/currency"
	"github.com/upcomers-org/platformgo/internal/decimal"
)

func ethWeiCurrency() currency.Currency {
	return currency.MustNew("ETH", 18, 0, "Ethereum", currency.Crypto)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/types/money.rs:86
//	test: test_from_wei_one_eth
func TestMoneyFromWeiOneETH(t *testing.T) {
	value := MustFromWei(big.NewInt(1_000_000_000_000_000_000), ethWeiCurrency())
	if !value.Decimal().Equal(decimal.MustParse("1")) || value.Currency().Precision != 18 {
		t.Fatalf("money = %s precision %d", value.Decimal(), value.Currency().Precision)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/types/money.rs:97
//	test: test_from_wei_small_amount
func TestMoneyFromWeiSmallAmount(t *testing.T) {
	value := MustFromWei(big.NewInt(1_000_000_000_000), ethWeiCurrency())
	if !value.Decimal().Equal(decimal.MustParse("0.000001")) {
		t.Fatalf("money = %s", value.Decimal())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/types/money.rs:107
//	test: test_to_wei_one_eth
func TestMoneyToWeiOneETH(t *testing.T) {
	value := MustFromWei(big.NewInt(1_000_000_000_000_000_000), ethWeiCurrency())
	if value.Wei().Cmp(big.NewInt(1_000_000_000_000_000_000)) != 0 {
		t.Fatalf("wei = %s", value.Wei())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/types/money.rs:116
//	test: test_to_wei_small_amount
func TestMoneyToWeiSmallAmount(t *testing.T) {
	value := MustFromWei(big.NewInt(1_000_000_000_000), ethWeiCurrency())
	if value.Wei().Cmp(big.NewInt(1_000_000_000_000)) != 0 {
		t.Fatalf("wei = %s", value.Wei())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/types/money.rs:125
//	test: test_wei_roundtrip
func TestMoneyWeiRoundTrip(t *testing.T) {
	raw, _ := new(big.Int).SetString("1234567890123456789", 10)
	if MustFromWei(raw, ethWeiCurrency()).Wei().Cmp(raw) != 0 {
		t.Fatal("wei round-trip changed the raw value")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/types/money.rs:135
//	test: test_checked_arith_rejects_mixed_scale_same_code
func TestMoneyCheckedArithRejectsMixedScaleSameCode(t *testing.T) {
	standard := MustNew("1.0", currency.MustNew("ETH", 8, 0, "Ethereum", currency.Crypto))
	wei := MustFromWei(big.NewInt(1_000_000_000_000_000_000), ethWeiCurrency())
	for name, ok := range map[string]bool{
		"wei+standard": func() bool { _, ok := wei.CheckedAdd(standard); return ok }(),
		"standard+wei": func() bool { _, ok := standard.CheckedAdd(wei); return ok }(),
		"wei-standard": func() bool { _, ok := wei.CheckedSub(standard); return ok }(),
		"standard-wei": func() bool { _, ok := standard.CheckedSub(wei); return ok }(),
	} {
		if ok {
			t.Errorf("%s unexpectedly succeeded", name)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/types/money.rs:153
//	test: test_from_wei_rejects_non_18_precision
func TestMoneyFromWeiRejectsNon18Precision(t *testing.T) {
	defer expectMoneyPanicContains(t, "`from_wei` requires a currency with precision 18")
	_ = MustFromWei(big.NewInt(1), currency.MustNew("ETH", 8, 0, "Ethereum", currency.Crypto))
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/types/money.rs:160
//	test: test_to_wei_rejects_non_18_precision
func TestMoneyToWeiRejectsNon18Precision(t *testing.T) {
	defer expectMoneyPanicContains(t, "requires precision 18")
	_ = MustNew("1.0", currency.USD()).Wei()
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/types/money.rs:167
//	test: test_from_wei_zero
func TestMoneyFromWeiZero(t *testing.T) {
	value := MustFromWei(new(big.Int), ethWeiCurrency())
	if !value.IsZero() || !value.Decimal().IsZero() || value.Wei().Sign() != 0 {
		t.Fatalf("zero = %s/%s", value.Decimal(), value.Wei())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/types/money.rs:180
//	test: test_from_wei_maximum_u128
func TestMoneyFromWeiMaximumU128(t *testing.T) {
	maxU128 := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 128), big.NewInt(1))
	defer expectMoneyPanicContains(t, "raw wei value exceeds signed 128-bit range")
	_ = MustFromWei(maxU128, ethWeiCurrency())
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/types/money.rs:188
//	test: test_from_wei_overflow
func TestMoneyFromWeiOverflow(t *testing.T) {
	overflow := new(big.Int).Lsh(big.NewInt(1), 128)
	defer expectMoneyPanicContains(t, "raw wei value exceeds 128-bit range")
	_ = MustFromWei(overflow, ethWeiCurrency())
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/types/money.rs:195
//	test: test_from_wei_different_tokens
func TestMoneyFromWeiDifferentTokens(t *testing.T) {
	usdc := currency.MustNew("USDC", 18, 0, "USD Coin", currency.Crypto)
	dai := currency.MustNew("DAI", 18, 0, "Dai Stablecoin", currency.Crypto)
	raw := big.NewInt(500_000_000_000_000_000)
	usdcMoney, daiMoney := MustFromWei(raw, usdc), MustFromWei(raw, dai)
	if !usdcMoney.Decimal().Equal(daiMoney.Decimal()) || usdcMoney.Wei().Cmp(daiMoney.Wei()) != 0 ||
		usdcMoney.Currency().Equal(daiMoney.Currency()) {
		t.Fatal("different token wei values lost amount or currency identity")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/types/money.rs:209
//	test: test_arithmetic_with_wei_values
func TestMoneyArithmeticWithWeiValues(t *testing.T) {
	one := MustFromWei(big.NewInt(1_000_000_000_000_000_000), ethWeiCurrency())
	half := MustFromWei(big.NewInt(500_000_000_000_000_000), ethWeiCurrency())
	sum := one.Add(half)
	if !sum.Decimal().Equal(decimal.MustParse("1.5")) ||
		sum.Wei().Cmp(big.NewInt(1_500_000_000_000_000_000)) != 0 {
		t.Fatalf("sum = %s/%s", sum.Decimal(), sum.Wei())
	}
	diff := one.Sub(half)
	if !diff.Decimal().Equal(decimal.MustParse("0.5")) ||
		diff.Wei().Cmp(big.NewInt(500_000_000_000_000_000)) != 0 {
		t.Fatalf("diff = %s/%s", diff.Decimal(), diff.Wei())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/defi/types/money.rs:224
//	test: test_comparison_with_wei_values
func TestMoneyComparisonWithWeiValues(t *testing.T) {
	one := MustFromWei(big.NewInt(1_000_000_000_000_000_000), ethWeiCurrency())
	two := MustFromWei(big.NewInt(2_000_000_000_000_000_000), ethWeiCurrency())
	otherOne := MustFromWei(big.NewInt(1_000_000_000_000_000_000), ethWeiCurrency())
	if one.Cmp(two) >= 0 || two.Cmp(one) <= 0 || !one.Equal(otherOne) ||
		one.Cmp(otherOne) != 0 {
		t.Fatal("wei money ordering is incorrect")
	}
}

func expectMoneyPanicContains(t *testing.T, want string) {
	t.Helper()
	recovered := recover()
	if recovered == nil {
		t.Fatalf("expected panic containing %q", want)
	}
	if got := fmt.Sprint(recovered); !strings.Contains(got, want) {
		t.Fatalf("panic = %q, want substring %q", got, want)
	}
}
