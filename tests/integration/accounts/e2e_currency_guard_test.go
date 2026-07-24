package accounts

import "testing"

func btcPerpetual(symbol string, quote, settlement Currency) Instrument {
	return Instrument{Symbol: symbol, QuoteCurrency: quote, SettlementCurrency: settlement}
}

// Ported from:
//
//	platform: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/accounts/e2e_currency_guard.rs:51
//	test: account_create_rejects_unsupported_base_currency
//
// Adaptations:
//   - Seeded-rate support is represented by a fixture-owned currency allowlist.
//
// Assertions preserved:
//   - Default USDC and explicit USD succeed; unsupported EUR fails with BadRequest.
func TestAccountCreateRejectsUnsupportedBaseCurrency(t *testing.T) {
	harness := NewHarness()
	user := harness.CreateUser("ccyguard1")

	usdc, err := harness.CreateAccount(user.ID, nil, nil)
	if err != nil || usdc.BaseCurrency != CurrencyUSDC {
		t.Fatalf("USDC account = %+v, error = %v", usdc, err)
	}
	usd, venue := CurrencyUSD, VenueFixCFD
	usdAccount, err := harness.CreateAccount(user.ID, &usd, &venue)
	if err != nil || usdAccount.BaseCurrency != CurrencyUSD {
		t.Fatalf("USD account = %+v, error = %v", usdAccount, err)
	}
	eur := CurrencyEUR
	if _, err := harness.CreateAccount(user.ID, &eur, &venue); !IsAppError(err, ErrorBadRequest) {
		t.Fatalf("EUR create error = %v, want BadRequest", err)
	}
}

// Ported from:
//
//	platform: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/accounts/e2e_currency_guard.rs:96
//	test: instrument_upsert_rejects_unsupported_settlement_currency
//
// Adaptations:
//   - Instrument persistence is an instance-owned synchronous catalog.
//
// Assertions preserved:
//   - USDC instrument succeeds; EUR settlement and EUR quote each fail with BadRequest.
func TestInstrumentUpsertRejectsUnsupportedSettlementCurrency(t *testing.T) {
	harness := NewHarness()
	if err := harness.UpsertInstrument(btcPerpetual("BTC-PERP", CurrencyUSDC, CurrencyUSDC)); err != nil {
		t.Fatal(err)
	}
	if err := harness.UpsertInstrument(btcPerpetual("BTC-PERP-EUR", CurrencyUSDC, CurrencyEUR)); !IsAppError(err, ErrorBadRequest) {
		t.Fatalf("EUR-settled error = %v, want BadRequest", err)
	}
	if err := harness.UpsertInstrument(btcPerpetual("BTC-PERP-EURQ", CurrencyEUR, CurrencyUSDC)); !IsAppError(err, ErrorBadRequest) {
		t.Fatalf("EUR-quoted error = %v, want BadRequest", err)
	}
}

// Ported from:
//
//	platform: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/accounts/e2e_currency_guard.rs:129
//	test: door_open_extending_allowlist_accepts_new_currency
//
// Adaptations:
//   - Composition rebuild uses a fresh Harness with the extended allowlist.
//
// Assertions preserved:
//   - Default list is [USDC, USD]; adding EUR permits an EUR account and EUR instrument.
func TestDoorOpenExtendingAllowlistAcceptsNewCurrency(t *testing.T) {
	defaultHarness := NewHarness()
	supported := defaultHarness.SupportedCurrencies()
	if len(supported) != 2 || supported[0] != CurrencyUSDC || supported[1] != CurrencyUSD {
		t.Fatalf("default supported currencies = %v", supported)
	}

	harness := NewHarnessWithCurrencies(CurrencyUSDC, CurrencyUSD, CurrencyEUR)
	user := harness.CreateUser("ccyguard-dooropen")
	eur, venue := CurrencyEUR, VenueFixCFD
	account, err := harness.CreateAccount(user.ID, &eur, &venue)
	if err != nil {
		t.Fatal(err)
	}
	if account.BaseCurrency != CurrencyEUR {
		t.Fatalf("base currency = %s, want EUR", account.BaseCurrency)
	}
	if err := harness.UpsertInstrument(btcPerpetual("BTC-PERP-EUR", CurrencyEUR, CurrencyEUR)); err != nil {
		t.Fatal(err)
	}
}
