package account

import (
	"fmt"
	"math/big"
	"strings"
	"testing"

	"github.com/upcomers-org/platformgo/internal/currency"
	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
	"github.com/upcomers-org/platformgo/internal/money"
)

func euro() currency.Currency {
	return currency.MustNew("EUR", 2, 978, "Euro", currency.Fiat)
}

func accountBalanceFixture() AccountBalance {
	return MustAccountBalance(
		money.MustNew("1525000", currency.USD()),
		money.MustNew("25000", currency.USD()),
		money.MustNew("1500000", currency.USD()),
	)
}

func marginBalanceFixture() MarginBalance {
	instrumentID := ids.MustInstrumentID("BTCUSDT.COINBASE")
	return MustMarginBalance(
		money.MustNew("5000", currency.USD()),
		money.MustNew("20000", currency.USD()),
		&instrumentID,
	)
}

func requirePanicContains(t *testing.T, want string, action func()) {
	t.Helper()
	defer func() {
		value := recover()
		if value == nil || !strings.Contains(fmt.Sprint(value), want) {
			t.Fatalf("panic = %v, want substring %q", value, want)
		}
	}()
	action()
}

func requireBalanceAmounts(t *testing.T, balance AccountBalance, total, locked, free string) {
	t.Helper()
	for name, check := range map[string]struct {
		got  money.Money
		want string
	}{
		"total":  {balance.Total, total},
		"locked": {balance.Locked, locked},
		"free":   {balance.Free, free},
	} {
		expected := money.MustNew(check.want, balance.Currency)
		if !check.got.Equal(expected) {
			t.Fatalf("%s = %s, want %s", name, check.got, expected)
		}
	}
}

func requireInvariant(t *testing.T, balance AccountBalance) {
	t.Helper()
	sum := new(big.Int).Add(balance.Locked.Raw(), balance.Free.Raw())
	if balance.Total.Raw().Cmp(sum) != 0 {
		t.Fatalf("invariant failed: total raw %s, locked+free raw %s", balance.Total.Raw(), sum)
	}
	for name, amount := range map[string]money.Money{
		"total": balance.Total, "locked": balance.Locked, "free": balance.Free,
	} {
		if !amount.Currency().Equal(balance.Currency) {
			t.Fatalf("%s currency = %s, want %s", name, amount.Currency(), balance.Currency)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/balance.rs:329
//	test: test_account_balance_equality
func TestAccountBalanceEquality(t *testing.T) {
	if !accountBalanceFixture().Equal(accountBalanceFixture()) {
		t.Fatal("identical account balances are not equal")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/balance.rs:336
//	test: test_account_balance_debug
func TestAccountBalanceDebug(t *testing.T) {
	want := "AccountBalance(total=1525000.00 USD, locked=25000.00 USD, free=1500000.00 USD)"
	if got := accountBalanceFixture().DebugString(); got != want {
		t.Fatalf("DebugString() = %q, want %q", got, want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/balance.rs:344
//	test: test_account_balance_display
func TestAccountBalanceDisplay(t *testing.T) {
	want := "AccountBalance(total=1525000.00 USD, locked=25000.00 USD, free=1500000.00 USD)"
	if got := accountBalanceFixture().String(); got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/balance.rs:352
//	test: test_account_balance_new_checked_with_currency_mismatch_returns_error
func TestAccountBalanceNewWithCurrencyMismatchReturnsError(t *testing.T) {
	_, err := NewAccountBalance(
		money.MustNew("1000", currency.USD()),
		money.MustNew("250", euro()),
		money.MustNew("750", currency.USD()),
	)
	if err == nil {
		t.Fatal("currency mismatch was accepted")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/balance.rs:365
//	test: test_account_balance_new_with_currency_mismatch_panics
func TestAccountBalanceMustNewWithCurrencyMismatchPanics(t *testing.T) {
	requirePanicContains(t, "`total` currency (USD) != `locked` currency (EUR)", func() {
		MustAccountBalance(
			money.MustNew("1000", currency.USD()),
			money.MustNew("250", euro()),
			money.MustNew("750", currency.USD()),
		)
	})
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/balance.rs:398
//	test: test_from_total_and_locked_preserves_invariant
func TestFromTotalAndLockedPreservesInvariant(t *testing.T) {
	cases := [][2]string{
		{"0", "0"}, {"0", "5"}, {"1000", "250"}, {"1000", "1000"}, {"1000", "0"},
		{"1234.56", "789.01"}, {"10.12345678", "2.87654321"}, {"0.00000001", "0"},
		{"1000000000.00", "123.45"}, {"10.000000035", "10.000000031"},
		{"10.000000034999", "0.000000004999"}, {"100", "150"}, {"1.50000000", "5.00000000"},
		{"100", "-5"}, {"0.50000000", "-0.00000001"}, {"-10", "5"}, {"-10", "-5"}, {"-100", "50"},
	}
	for _, values := range cases {
		for _, denomination := range []currency.Currency{currency.USD(), currency.BTC()} {
			balance, err := AccountBalanceFromTotalAndLocked(
				decimal.MustParse(values[0]),
				decimal.MustParse(values[1]),
				denomination,
			)
			if err != nil {
				t.Fatalf("%v %s: %v", values, denomination, err)
			}
			requireInvariant(t, balance)
			if balance.Total.Raw().Sign() >= 0 && balance.Locked.Raw().Sign() < 0 {
				t.Fatalf("non-negative total left locked negative: %v", values)
			}
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/balance.rs:441
//	test: test_from_total_and_free_preserves_invariant
func TestFromTotalAndFreePreservesInvariant(t *testing.T) {
	cases := [][2]string{
		{"0", "0"}, {"1000", "750"}, {"1000", "1000"}, {"1000", "0"}, {"1234.56", "444.55"},
		{"10.12345678", "7.24691356"}, {"10.000000034999", "9.999999994999"},
		{"100", "120"}, {"0.50000000", "0.99999999"}, {"100", "-5"}, {"-10", "0"}, {"-10", "5"},
	}
	for _, values := range cases {
		for _, denomination := range []currency.Currency{currency.USD(), currency.BTC()} {
			balance, err := AccountBalanceFromTotalAndFree(
				decimal.MustParse(values[0]),
				decimal.MustParse(values[1]),
				denomination,
			)
			if err != nil {
				t.Fatalf("%v %s: %v", values, denomination, err)
			}
			requireInvariant(t, balance)
			if balance.Total.Raw().Sign() >= 0 && balance.Free.Raw().Sign() < 0 {
				t.Fatalf("non-negative total left free negative: %v", values)
			}
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/balance.rs:476
//	test: test_from_total_and_locked_exact_usd
func TestFromTotalAndLockedExactUSD(t *testing.T) {
	cases := [][5]string{
		{"1000.00", "250.00", "1000.00", "250.00", "750.00"},
		{"500.00", "0.00", "500.00", "0.00", "500.00"},
		{"500.00", "500.00", "500.00", "500.00", "0.00"},
		{"100.00", "150.00", "100.00", "100.00", "0.00"},
		{"100.00", "-5.00", "100.00", "0.00", "100.00"},
	}
	for _, values := range cases {
		balance, err := AccountBalanceFromTotalAndLocked(
			decimal.MustParse(values[0]),
			decimal.MustParse(values[1]),
			currency.USD(),
		)
		if err != nil {
			t.Fatal(err)
		}
		requireBalanceAmounts(t, balance, values[2], values[3], values[4])
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/balance.rs:506
//	test: test_from_total_and_free_exact_usd
func TestFromTotalAndFreeExactUSD(t *testing.T) {
	cases := [][5]string{
		{"1000.00", "750.00", "1000.00", "250.00", "750.00"},
		{"500.00", "500.00", "500.00", "0.00", "500.00"},
		{"500.00", "0.00", "500.00", "500.00", "0.00"},
		{"100.00", "120.00", "100.00", "0.00", "100.00"},
		{"100.00", "-5.00", "100.00", "100.00", "0.00"},
	}
	for _, values := range cases {
		balance, err := AccountBalanceFromTotalAndFree(
			decimal.MustParse(values[0]),
			decimal.MustParse(values[1]),
			currency.USD(),
		)
		if err != nil {
			t.Fatal(err)
		}
		requireBalanceAmounts(t, balance, values[2], values[3], values[4])
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/balance.rs:534
//	test: test_from_total_and_locked_issue_3867_drift
func TestFromTotalAndLockedIssue3867Drift(t *testing.T) {
	availableFunds := decimal.MustParse("0.000000035")
	amount := decimal.MustParse("10").Add(availableFunds)
	locked := amount.Sub(availableFunds)
	balance, err := AccountBalanceFromTotalAndLocked(amount, locked, currency.BTC())
	if err != nil {
		t.Fatal(err)
	}
	requireInvariant(t, balance)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/balance.rs:549
//	test: test_from_total_and_locked_non_negative_total_never_leaves_free_negative
func TestFromTotalAndLockedNonNegativeTotalNeverLeavesFreeNegative(t *testing.T) {
	for _, values := range [][2]string{{"0", "100"}, {"1", "1000000"}, {"500", "500000"}} {
		balance, err := AccountBalanceFromTotalAndLocked(
			decimal.MustParse(values[0]),
			decimal.MustParse(values[1]),
			currency.USD(),
		)
		if err != nil {
			t.Fatal(err)
		}
		if balance.Free.Raw().Sign() < 0 {
			t.Fatalf("free went negative for %v", values)
		}
		requireInvariant(t, balance)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/balance.rs:567
//	test: test_locked_and_free_forms_agree_when_consistent
func TestLockedAndFreeFormsAgreeWhenConsistent(t *testing.T) {
	for _, values := range [][3]string{
		{"1000.00", "250.00", "750.00"},
		{"0.00", "0.00", "0.00"},
		{"500.00", "500.00", "0.00"},
		{"500.00", "0.00", "500.00"},
	} {
		fromLocked, err := AccountBalanceFromTotalAndLocked(
			decimal.MustParse(values[0]), decimal.MustParse(values[1]), currency.USD(),
		)
		if err != nil {
			t.Fatal(err)
		}
		fromFree, err := AccountBalanceFromTotalAndFree(
			decimal.MustParse(values[0]), decimal.MustParse(values[2]), currency.USD(),
		)
		if err != nil {
			t.Fatal(err)
		}
		if !fromLocked.Equal(fromFree) {
			t.Fatalf("forms differ for %v", values)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/balance.rs:582
//	test: test_from_total_and_locked_preserves_reserved_on_negative_total
func TestFromTotalAndLockedPreservesReservedOnNegativeTotal(t *testing.T) {
	cases := [][5]string{
		{"-100", "50", "-100", "50", "-150"},
		{"-10", "0", "-10", "0", "-10"},
		{"-10", "-5", "-10", "-5", "-5"},
	}
	for _, values := range cases {
		balance, err := AccountBalanceFromTotalAndLocked(
			decimal.MustParse(values[0]), decimal.MustParse(values[1]), currency.USD(),
		)
		if err != nil {
			t.Fatal(err)
		}
		requireBalanceAmounts(t, balance, values[2], values[3], values[4])
		requireInvariant(t, balance)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/balance.rs:610
//	test: test_from_total_and_free_preserves_available_on_negative_total
func TestFromTotalAndFreePreservesAvailableOnNegativeTotal(t *testing.T) {
	cases := [][5]string{
		{"-100", "-150", "-100", "50", "-150"},
		{"-100", "0", "-100", "-100", "0"},
	}
	for _, values := range cases {
		balance, err := AccountBalanceFromTotalAndFree(
			decimal.MustParse(values[0]), decimal.MustParse(values[1]), currency.USD(),
		)
		if err != nil {
			t.Fatal(err)
		}
		requireBalanceAmounts(t, balance, values[2], values[3], values[4])
		requireInvariant(t, balance)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/balance.rs:636
//	test: test_from_total_and_locked_invalid_decimal_returns_error
func TestFromTotalAndLockedInvalidDecimalReturnsError(t *testing.T) {
	_, err := AccountBalanceFromTotalAndLocked(
		decimal.MustParse("79228162514264337593543950335"),
		decimal.MustParse("0"),
		currency.BTC(),
	)
	if err == nil {
		t.Fatal("unrepresentable total was accepted")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/balance.rs:646
//	test: test_new_checked_extreme_values_returns_error_without_panicking
func TestNewAccountBalanceExtremeValuesReturnsErrorWithoutPanicking(t *testing.T) {
	maximum := money.MustNew("17014118346046", currency.USD())
	_, err := NewAccountBalance(maximum, maximum, maximum)
	if err == nil || !strings.Contains(err.Error(), "`total`") {
		t.Fatalf("error = %v", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/balance.rs:662
//	test: test_from_total_and_locked_extreme_bounds_returns_error
func TestFromTotalAndLockedExtremeBoundsReturnsError(t *testing.T) {
	_, err := AccountBalanceFromTotalAndLocked(
		decimal.MustParse("-17014118346046"),
		decimal.MustParse("17014118346046"),
		currency.USD(),
	)
	if err == nil || !strings.Contains(err.Error(), "Money") {
		t.Fatalf("error = %v", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/balance.rs:679
//	test: test_from_total_and_free_extreme_bounds_returns_error
func TestFromTotalAndFreeExtremeBoundsReturnsError(t *testing.T) {
	_, err := AccountBalanceFromTotalAndFree(
		decimal.MustParse("-17014118346046"),
		decimal.MustParse("17014118346046"),
		currency.USD(),
	)
	if err == nil || !strings.Contains(err.Error(), "Money") {
		t.Fatalf("error = %v", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/balance.rs:694
//	test: test_margin_balance_equality
func TestMarginBalanceEquality(t *testing.T) {
	if !marginBalanceFixture().Equal(marginBalanceFixture()) {
		t.Fatal("identical margin balances are not equal")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/balance.rs:701
//	test: test_margin_balance_debug
func TestMarginBalanceDebug(t *testing.T) {
	want := "MarginBalance(initial=5000.00 USD, maintenance=20000.00 USD, instrument_id=BTCUSDT.COINBASE)"
	if got := marginBalanceFixture().DebugString(); got != want {
		t.Fatalf("DebugString() = %q, want %q", got, want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/balance.rs:710
//	test: test_margin_balance_display
func TestMarginBalanceDisplay(t *testing.T) {
	want := "MarginBalance(initial=5000.00 USD, maintenance=20000.00 USD, instrument_id=BTCUSDT.COINBASE)"
	if got := marginBalanceFixture().String(); got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/balance.rs:719
//	test: test_margin_balance_new_checked_with_currency_mismatch_returns_error
func TestMarginBalanceNewWithCurrencyMismatchReturnsError(t *testing.T) {
	instrumentID := ids.MustInstrumentID("BTCUSDT.COINBASE")
	_, err := NewMarginBalance(
		money.MustNew("5000", currency.USD()),
		money.MustNew("20000", euro()),
		&instrumentID,
	)
	if err == nil {
		t.Fatal("currency mismatch was accepted")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/balance.rs:733
//	test: test_margin_balance_new_with_currency_mismatch_panics
func TestMarginBalanceMustNewWithCurrencyMismatchPanics(t *testing.T) {
	instrumentID := ids.MustInstrumentID("BTCUSDT.COINBASE")
	requirePanicContains(t, "`initial` currency (USD) != `maintenance` currency (EUR)", func() {
		MustMarginBalance(
			money.MustNew("5000", currency.USD()),
			money.MustNew("20000", euro()),
			&instrumentID,
		)
	})
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/balance.rs:745
//	test: test_margin_balance_account_scope_display
func TestMarginBalanceAccountScopeDisplay(t *testing.T) {
	balance := MustMarginBalance(
		money.MustNew("500", currency.USD()),
		money.MustNew("200", currency.USD()),
		nil,
	)
	want := "MarginBalance(initial=500.00 USD, maintenance=200.00 USD, currency=USD)"
	if got := balance.String(); got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}
