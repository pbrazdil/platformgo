package ids

import "testing"

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/account_id.rs:151
//	test: test_account_id_new_invalid_string
func TestAccountIDEmptyPanics(t *testing.T) {
	requirePanicContains(t, "invalid string for 'value', was empty", func() {
		MustAccountID("")
	})
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/account_id.rs:157
//	test: test_account_id_new_missing_hyphen
func TestAccountIDMissingHyphenPanics(t *testing.T) {
	requirePanicContains(t, "did not contain '-'", func() {
		MustAccountID("123456789")
	})
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/account_id.rs:162
//	test: test_account_id_fmt
func TestAccountIDFormat(t *testing.T) {
	if got := MustAccountID("IB-U123456789").String(); got != "IB-U123456789" {
		t.Fatalf("String() = %q", got)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/account_id.rs:170
//	test: test_string_reprs
func TestAccountIDStringRepresentation(t *testing.T) {
	if got := MustAccountID("IB-1234567890").String(); got != "IB-1234567890" {
		t.Fatalf("String() = %q", got)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/account_id.rs:175
//	test: test_get_issuer
func TestAccountIDIssuer(t *testing.T) {
	if got := MustAccountID("IB-1234567890").Issuer(); got != "IB" {
		t.Fatalf("Issuer() = %q", got)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/account_id.rs:180
//	test: test_get_issuers_id
func TestAccountIDIssuersID(t *testing.T) {
	if got := MustAccountID("IB-1234567890").IssuersID(); got != "1234567890" {
		t.Fatalf("IssuersID() = %q", got)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/account_id.rs:186
//	test: test_new_with_empty_issuer_panics
func TestAccountIDEmptyIssuerPanics(t *testing.T) {
	const want = "`value` issuer part (before '-') cannot be empty"
	requirePanicContains(t, want, func() { MustAccountID("-123456") })
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/account_id.rs:197
//	test: test_new_checked_with_empty_issuer_returns_error
func TestAccountIDParseEmptyIssuerReturnsError(t *testing.T) {
	_, err := ParseAccountID("-123456")
	if err == nil {
		t.Fatal("expected error")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/account_id.rs:207
//	test: test_new_checked_with_empty_issuer_returns_typed_error_with_stable_display
func TestAccountIDParseEmptyIssuerReturnsTypedErrorWithStableDisplay(t *testing.T) {
	const want = "`value` issuer part (before '-') cannot be empty"
	_, err := ParseAccountID("-123456")
	if err == nil {
		t.Fatal("expected error")
	}
	if got := validationKind(err); got != "predicate_violation" {
		t.Fatalf("error kind = %q", got)
	}
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err, want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/account_id.rs:192
//	test: test_new_with_empty_account_panics
func TestAccountIDEmptyAccountPanics(t *testing.T) {
	const want = "`value` account part (after '-') cannot be empty"
	requirePanicContains(t, want, func() { MustAccountID("IB-") })
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/account_id.rs:202
//	test: test_new_checked_with_empty_account_returns_error
func TestAccountIDParseEmptyAccountReturnsError(t *testing.T) {
	_, err := ParseAccountID("IB-")
	if err == nil {
		t.Fatal("expected error")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/account_id.rs:224
//	test: test_new_checked_with_empty_account_returns_typed_error_with_stable_display
func TestAccountIDParseEmptyAccountReturnsTypedErrorWithStableDisplay(t *testing.T) {
	const want = "`value` account part (after '-') cannot be empty"
	_, err := ParseAccountID("IB-")
	if err == nil {
		t.Fatal("expected error")
	}
	if got := validationKind(err); got != "predicate_violation" {
		t.Fatalf("error kind = %q", got)
	}
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err, want)
	}
}
