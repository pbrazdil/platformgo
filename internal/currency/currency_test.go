package currency

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/currency.rs:351
//	test: test_debug
func TestCurrencyDebugString(t *testing.T) {
	got := AUD().DebugString()
	want := "Currency(code='AUD', precision=2, iso4217=36, name='Australian dollar', currency_type=FIAT)"
	if got != want {
		t.Fatalf("DebugString() = %q, want %q", got, want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/currency.rs:360
//	test: test_display
func TestCurrencyString(t *testing.T) {
	if got := AUD().String(); got != "AUD" {
		t.Fatalf("String() = %q, want AUD", got)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/currency.rs:367
//	test: test_invalid_currency_code
func TestCurrencyRejectsInvalidCode(t *testing.T) {
	defer func() {
		value := recover()
		if value == nil || !strings.Contains(fmt.Sprint(value), "code") {
			t.Fatalf("panic = %v, want message containing code", value)
		}
	}()
	_ = MustNew("", 2, 840, "United States dollar", Fiat)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/currency.rs:374
//	test: test_invalid_precision
func TestCurrencyRejectsInvalidPrecision(t *testing.T) {
	defer func() {
		value := recover()
		if value == nil || !strings.Contains(fmt.Sprint(value), "precision exceeded maximum FIXED_PRECISION") {
			t.Fatalf("panic = %v, want FIXED_PRECISION failure", value)
		}
	}()
	_ = MustNew("USD", 19, 840, "United States dollar", Fiat)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/currency.rs:382
//	test: test_invalid_precision
//
// Adaptations:
//   - Go has one native fixed-precision mode; this preserves the defi variant's
//     independently inventoried rejection of precision 19.
func TestCurrencyRejectsInvalidPrecisionDefiVariant(t *testing.T) {
	defer func() {
		value := recover()
		if value == nil || !strings.Contains(fmt.Sprint(value), "precision exceeded maximum FIXED_PRECISION") {
			t.Fatalf("panic = %v, want FIXED_PRECISION failure", value)
		}
	}()
	_ = MustNew("ETH", 19, 0, "Ethereum", Crypto)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/currency.rs:388
//	test: test_register_no_overwrite
func TestRegistryRegisterWithoutOverwrite(t *testing.T) {
	registry := NewRegistry()
	registry.Register(MustNew("TEST1", 2, 999, "Test Currency 1", Fiat), false)
	registry.Register(MustNew("TEST1", 2, 999, "Test Currency 2 Updated", Fiat), false)

	found, ok := registry.TryLookup("TEST1")
	if !ok {
		t.Fatal("TEST1 was not registered")
	}
	if found.Name != "Test Currency 1" {
		t.Fatalf("name = %q, want Test Currency 1", found.Name)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/currency.rs:406
//	test: test_register_with_overwrite
func TestRegistryRegisterWithOverwrite(t *testing.T) {
	registry := NewRegistry()
	registry.Register(MustNew("TEST2", 2, 998, "Test Currency 2", Fiat), false)
	registry.Register(MustNew("TEST2", 2, 998, "Test Currency 2 Overwritten", Fiat), true)

	found, ok := registry.TryLookup("TEST2")
	if !ok {
		t.Fatal("TEST2 was not registered")
	}
	if found.Name != "Test Currency 2 Overwritten" {
		t.Fatalf("name = %q, want Test Currency 2 Overwritten", found.Name)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/currency.rs:424
//	test: test_new_for_fiat
func TestNewFiatCurrency(t *testing.T) {
	value := MustNew("AUD", 2, 36, "Australian dollar", Fiat)
	if !value.Equal(value) {
		t.Fatal("currency does not equal itself")
	}
	if value.Code != "AUD" || value.Precision != 2 || value.ISO4217 != 36 ||
		value.Name != "Australian dollar" || value.Type != Fiat {
		t.Fatalf("unexpected currency: %+v", value)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/currency.rs:435
//	test: test_new_for_crypto
func TestNewCryptoCurrency(t *testing.T) {
	value := MustNew("ETH", 8, 0, "Ether", Crypto)
	if !value.Equal(value) {
		t.Fatal("currency does not equal itself")
	}
	if value.Code != "ETH" || value.Precision != 8 || value.ISO4217 != 0 ||
		value.Name != "Ether" || value.Type != Crypto {
		t.Fatalf("unexpected currency: %+v", value)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/currency.rs:446
//	test: test_try_from_str_valid
func TestRegistryTryLookupValid(t *testing.T) {
	registry := NewRegistry()
	testCurrency := MustNew("TEST", 2, 999, "Test Currency", Fiat)
	registry.Register(testCurrency, true)

	found, ok := registry.TryLookup("TEST")
	if !ok {
		t.Fatal("TryLookup(TEST) did not find registered currency")
	}
	if !found.Equal(testCurrency) {
		t.Fatalf("TryLookup(TEST) = %+v, want %+v", found, testCurrency)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/currency.rs:456
//	test: test_try_from_str_invalid
func TestRegistryTryLookupInvalid(t *testing.T) {
	if _, ok := NewRegistry().TryLookup("INVALID"); ok {
		t.Fatal("TryLookup(INVALID) found a currency")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/currency.rs:462
//	test: test_equality
func TestCurrencyEquality(t *testing.T) {
	first := MustNew("USD", 2, 840, "United States dollar", Fiat)
	second := MustNew("USD", 2, 840, "United States dollar", Fiat)
	if !first.Equal(second) {
		t.Fatalf("%+v does not equal %+v", first, second)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/currency.rs:469
//	test: test_currency_partial_eq_only_checks_code
func TestCurrencyEqualityOnlyChecksCode(t *testing.T) {
	first := MustNew("ABC", 2, 999, "Currency ABC", Fiat)
	second := MustNew("ABC", 8, 100, "Completely Different", Crypto)
	if !first.Equal(second) {
		t.Fatal("currencies with the same code should be equal")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/currency.rs:477
//	test: test_is_fiat
func TestRegistryIsFiat(t *testing.T) {
	registry := NewRegistry(MustNew("TESTFIAT", 2, 840, "Test Fiat", Fiat))
	result, err := registry.IsFiat("TESTFIAT")
	if err != nil {
		t.Fatalf("IsFiat(TESTFIAT): %v", err)
	}
	if !result {
		t.Fatal("expected TESTFIAT to be recognized as fiat")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/currency.rs:490
//	test: test_is_crypto
func TestRegistryIsCrypto(t *testing.T) {
	registry := NewRegistry(MustNew("TESTCRYPTO", 8, 0, "Test Crypto", Crypto))
	result, err := registry.IsCrypto("TESTCRYPTO")
	if err != nil {
		t.Fatalf("IsCrypto(TESTCRYPTO): %v", err)
	}
	if !result {
		t.Fatal("expected TESTCRYPTO to be recognized as crypto")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/currency.rs:503
//	test: test_is_commodity_backed
func TestRegistryIsCommodityBacked(t *testing.T) {
	registry := NewRegistry(MustNew("TESTGOLD", 5, 0, "Test Gold", CommodityBacked))
	result, err := registry.IsCommodityBacked("TESTGOLD")
	if err != nil {
		t.Fatalf("IsCommodityBacked(TESTGOLD): %v", err)
	}
	if !result {
		t.Fatal("expected TESTGOLD to be recognized as commodity-backed")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/currency.rs:516
//	test: test_is_fiat_unknown_currency
func TestRegistryIsFiatUnknownCurrency(t *testing.T) {
	_, err := NewRegistry().IsFiat("NON_EXISTENT")
	want := UnknownCodeError{Code: "NON_EXISTENT"}
	if !errors.Is(err, want) {
		t.Fatalf("error = %#v, want %#v", err, want)
	}
	if err.Error() != "Unknown currency: NON_EXISTENT" {
		t.Fatalf("error text = %q", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/currency.rs:531
//	test: test_currency_classification_unknown_code_returns_typed_error
func TestCurrencyClassificationUnknownCodeReturnsTypedError(t *testing.T) {
	registry := NewRegistry()
	classifiers := map[string]func(string) (bool, error){
		"fiat":             registry.IsFiat,
		"crypto":           registry.IsCrypto,
		"commodity-backed": registry.IsCommodityBacked,
	}
	for name, classify := range classifiers {
		t.Run(name, func(t *testing.T) {
			_, err := classify("UNKNOWN_CLASSIFICATION")
			want := UnknownCodeError{Code: "UNKNOWN_CLASSIFICATION"}
			if !errors.Is(err, want) {
				t.Fatalf("error = %#v, want %#v", err, want)
			}
			if err.Error() != "Unknown currency: UNKNOWN_CLASSIFICATION" {
				t.Fatalf("error text = %q", err)
			}
		})
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/currency.rs:546
//	test: test_from_str_unknown_code_returns_typed_error
func TestRegistryLookupUnknownCodeReturnsTypedError(t *testing.T) {
	_, err := NewRegistry().Lookup("UNKNOWN_FROM_STR")
	want := UnknownCodeError{Code: "UNKNOWN_FROM_STR"}
	if !errors.Is(err, want) {
		t.Fatalf("error = %#v, want %#v", err, want)
	}
	if err.Error() != "Unknown currency: UNKNOWN_FROM_STR" {
		t.Fatalf("error text = %q", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/currency.rs:559
//	test: test_currency_lookup_error_lock_failure_display
//
// Adaptations:
//   - Go mutexes do not poison; the source error variant is constructed directly.
func TestLockFailureErrorDisplay(t *testing.T) {
	err := LockFailureError{Reason: "poisoned lock"}
	if err != (LockFailureError{Reason: "poisoned lock"}) {
		t.Fatalf("error = %#v", err)
	}
	want := "Failed to acquire lock on `CURRENCY_MAP`: poisoned lock"
	if err.Error() != want {
		t.Fatalf("error text = %q, want %q", err, want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/currency.rs:578
//	test: test_from_unknown_code_panics_with_display_error
func TestRegistryMustLookupUnknownCodePanicsWithDisplayError(t *testing.T) {
	defer func() {
		value := recover()
		if value == nil || !strings.Contains(fmt.Sprint(value), "Unknown currency: UNKNOWN_FROM_PANIC") {
			t.Fatalf("panic = %v", value)
		}
	}()
	_ = NewRegistry().MustLookup("UNKNOWN_FROM_PANIC")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/currency.rs:583
//	test: test_serialization_deserialization
//
// Adaptations:
//   - Deserialization resolves through an explicit per-test Registry.
func TestCurrencySerializationDeserialization(t *testing.T) {
	registry := NewDefaultRegistry()
	serialized, err := json.Marshal(USD())
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	deserialized, err := registry.CurrencyFromJSON(serialized)
	if err != nil {
		t.Fatalf("CurrencyFromJSON: %v", err)
	}
	if !USD().Equal(deserialized) {
		t.Fatalf("round trip = %+v, want %+v", deserialized, USD())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/currency.rs:591
//	test: test_get_or_create_crypto_existing
func TestGetOrCreateCryptoExisting(t *testing.T) {
	value := NewDefaultRegistry().GetOrCreateCrypto("BTC")
	if value.Code != "BTC" || value.Type != Crypto {
		t.Fatalf("currency = %+v", value)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/currency.rs:599
//	test: test_get_or_create_crypto_new
func TestGetOrCreateCryptoNew(t *testing.T) {
	registry := NewDefaultRegistry()
	value := registry.GetOrCreateCrypto("NEWCOIN")
	if value.Code != "NEWCOIN" || value.Precision != 8 || value.ISO4217 != 0 ||
		value.Name != "NEWCOIN" || value.Type != Crypto {
		t.Fatalf("currency = %+v", value)
	}
	retrieved, ok := registry.TryLookup("NEWCOIN")
	if !ok || !retrieved.Equal(value) {
		t.Fatalf("retrieved = %+v, ok = %v", retrieved, ok)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/currency.rs:615
//	test: test_get_or_create_crypto_idempotent
func TestGetOrCreateCryptoIdempotent(t *testing.T) {
	registry := NewDefaultRegistry()
	first := registry.GetOrCreateCrypto("TESTCOIN")
	second := registry.GetOrCreateCrypto("TESTCOIN")
	if !first.Equal(second) {
		t.Fatalf("first = %+v, second = %+v", first, second)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/currency.rs:626
//	test: test_get_or_create_crypto_with_ustr
//
// Adaptations:
//   - Rust's interned Ustr input becomes the equivalent Go string.
func TestGetOrCreateCryptoWithString(t *testing.T) {
	value := NewDefaultRegistry().GetOrCreateCrypto("USTRCOIN")
	if value.Code != "USTRCOIN" || value.Type != Crypto {
		t.Fatalf("currency = %+v", value)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/currency.rs:637
//	test: test_get_or_create_crypto_with_context_valid
func TestGetOrCreateCryptoWithContextValid(t *testing.T) {
	result := NewDefaultRegistry().GetOrCreateCryptoWithContext("BTC", "test context")
	if !result.Equal(BTC()) {
		t.Fatalf("result = %+v, want %+v", result, BTC())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/currency.rs:643
//	test: test_get_or_create_crypto_with_context_empty
func TestGetOrCreateCryptoWithContextEmpty(t *testing.T) {
	result := NewDefaultRegistry().GetOrCreateCryptoWithContext("", "test context")
	if !result.Equal(USDT()) {
		t.Fatalf("result = %+v, want %+v", result, USDT())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/currency.rs:649
//	test: test_get_or_create_crypto_with_context_whitespace
func TestGetOrCreateCryptoWithContextWhitespace(t *testing.T) {
	result := NewDefaultRegistry().GetOrCreateCryptoWithContext("  ", "test context")
	if !result.Equal(USDT()) {
		t.Fatalf("result = %+v, want %+v", result, USDT())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/types/currency.rs:655
//	test: test_get_or_create_crypto_with_context_unknown
func TestGetOrCreateCryptoWithContextUnknown(t *testing.T) {
	result := NewDefaultRegistry().GetOrCreateCryptoWithContext("NEWCOIN", "test context")
	if result.Code != "NEWCOIN" || result.Precision != 8 {
		t.Fatalf("result = %+v", result)
	}
}
