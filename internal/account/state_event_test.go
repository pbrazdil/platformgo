package account

import (
	"testing"

	"github.com/upcomers-org/platformgo/internal/currency"
	"github.com/upcomers-org/platformgo/internal/ids"
	"github.com/upcomers-org/platformgo/internal/money"
)

const accountStateEventID = "16578139-a945-4b65-b46c-bc131a15d8e7"

func stateEventBalance(total, locked, free string, denomination currency.Currency) AccountBalance {
	return MustAccountBalance(
		money.MustNew(total, denomination),
		money.MustNew(locked, denomination),
		money.MustNew(free, denomination),
	)
}

func cashStateEventFixture() AccountStateEvent {
	usd := currency.USD()
	return AccountStateEvent{
		AccountID:    ids.MustAccountID("SIM-001"),
		AccountType:  AnyAccountTypeCash,
		BaseCurrency: &usd,
		Balances:     []AccountBalance{stateEventBalance("1525000", "25000", "1500000", usd)},
		Reported:     true,
		EventID:      accountStateEventID,
	}
}

func marginStateEventFixture() AccountStateEvent {
	state := cashStateEventFixture()
	state.AccountType = AnyAccountTypeMargin
	instrumentID := ids.MustInstrumentID("BTCUSDT.COINBASE")
	state.Margins = []MarginBalance{MustMarginBalance(
		money.MustNew("5000", currency.USD()),
		money.MustNew("20000", currency.USD()),
		&instrumentID,
	)}
	return state
}

func euroStateEventCurrency() currency.Currency {
	return currency.MustNew("EUR", 2, 978, "Euro", currency.Fiat)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/account/state.rs:214
//	test: test_equality
func TestAccountStateEventEquality(t *testing.T) {
	state1 := cashStateEventFixture()
	state2 := cashStateEventFixture()

	if !state1.Equal(state2) {
		t.Fatal("equal cash account states compared unequal")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/account/state.rs:221
//	test: test_display_cash_account_state
func TestDisplayCashAccountStateEvent(t *testing.T) {
	state := cashStateEventFixture()

	want := "AccountState(account_id=SIM-001, account_type=CASH, base_currency=USD, is_reported=true, balances=[AccountBalance(total=1525000.00 USD, locked=25000.00 USD, free=1500000.00 USD)], margins=[], event_id=16578139-a945-4b65-b46c-bc131a15d8e7)"
	if got := state.String(); got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/account/state.rs:232
//	test: test_display_margin_account_state
func TestDisplayMarginAccountStateEvent(t *testing.T) {
	state := marginStateEventFixture()

	want := "AccountState(account_id=SIM-001, account_type=MARGIN, base_currency=USD, is_reported=true, balances=[AccountBalance(total=1525000.00 USD, locked=25000.00 USD, free=1500000.00 USD)], margins=[MarginBalance(initial=5000.00 USD, maintenance=20000.00 USD, instrument_id=BTCUSDT.COINBASE)], event_id=16578139-a945-4b65-b46c-bc131a15d8e7)"
	if got := state.String(); got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/account/state.rs:244
//	test: test_has_same_balances_and_margins_when_identical
func TestAccountStateEventHasSameBalancesAndMarginsWhenIdentical(t *testing.T) {
	state1 := cashStateEventFixture()
	state2 := cashStateEventFixture()

	if !state1.HasSameBalancesAndMargins(state2) {
		t.Fatal("identical financial state compared different")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/account/state.rs:251
//	test: test_has_same_balances_and_margins_when_different_balance_amounts
func TestAccountStateEventHasSameBalancesAndMarginsWhenDifferentBalanceAmounts(t *testing.T) {
	state1 := cashStateEventFixture()
	state2 := cashStateEventFixture()
	state2.Balances = []AccountBalance{
		stateEventBalance("2000000", "50000", "1950000", currency.USD()),
	}

	if state1.HasSameBalancesAndMargins(state2) {
		t.Fatal("different balance amounts compared equal")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/account/state.rs:266
//	test: test_has_same_balances_and_margins_when_different_balance_currencies
func TestAccountStateEventHasSameBalancesAndMarginsWhenDifferentBalanceCurrencies(t *testing.T) {
	state1 := cashStateEventFixture()
	state2 := cashStateEventFixture()
	eur := euroStateEventCurrency()
	state2.Balances = []AccountBalance{
		stateEventBalance("1525000", "25000", "1500000", eur),
	}

	if state1.HasSameBalancesAndMargins(state2) {
		t.Fatal("different balance currencies compared equal")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/account/state.rs:281
//	test: test_has_same_balances_and_margins_when_missing_balance
func TestAccountStateEventHasSameBalancesAndMarginsWhenMissingBalance(t *testing.T) {
	state1 := cashStateEventFixture()
	state2 := cashStateEventFixture()
	eur := euroStateEventCurrency()
	state2.Balances = append(
		state2.Balances,
		stateEventBalance("1000000", "0", "1000000", eur),
	)

	if state1.HasSameBalancesAndMargins(state2) {
		t.Fatal("additional balance compared equal")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/account/state.rs:296
//	test: test_has_same_balances_and_margins_when_different_margin_amounts
func TestAccountStateEventHasSameBalancesAndMarginsWhenDifferentMarginAmounts(t *testing.T) {
	state1 := marginStateEventFixture()
	state2 := marginStateEventFixture()
	instrumentID := ids.MustInstrumentID("BTCUSDT.COINBASE")
	state2.Margins = []MarginBalance{MustMarginBalance(
		money.MustNew("10000", currency.USD()),
		money.MustNew("40000", currency.USD()),
		&instrumentID,
	)}

	if state1.HasSameBalancesAndMargins(state2) {
		t.Fatal("different margin amounts compared equal")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/account/state.rs:312
//	test: test_has_same_balances_and_margins_when_different_margin_instruments
func TestAccountStateEventHasSameBalancesAndMarginsWhenDifferentMarginInstruments(t *testing.T) {
	state1 := marginStateEventFixture()
	state2 := marginStateEventFixture()
	instrumentID := ids.MustInstrumentID("ETHUSDT.BINANCE")
	state2.Margins = []MarginBalance{MustMarginBalance(
		money.MustNew("5000", currency.USD()),
		money.MustNew("20000", currency.USD()),
		&instrumentID,
	)}

	if state1.HasSameBalancesAndMargins(state2) {
		t.Fatal("different margin instruments compared equal")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/account/state.rs:328
//	test: test_has_same_balances_and_margins_when_missing_margin
func TestAccountStateEventHasSameBalancesAndMarginsWhenMissingMargin(t *testing.T) {
	state1 := marginStateEventFixture()
	state2 := marginStateEventFixture()
	instrumentID := ids.MustInstrumentID("ETHUSDT.BINANCE")
	state2.Margins = append(state2.Margins, MustMarginBalance(
		money.MustNew("3000", currency.USD()),
		money.MustNew("15000", currency.USD()),
		&instrumentID,
	))

	if state1.HasSameBalancesAndMargins(state2) {
		t.Fatal("additional margin compared equal")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/account/state.rs:344
//	test: test_has_same_balances_and_margins_with_empty_collections
func TestAccountStateEventHasSameBalancesAndMarginsWithEmptyCollections(t *testing.T) {
	usd := currency.USD()
	state1 := AccountStateEvent{
		AccountID:    ids.MustAccountID("TEST-001"),
		AccountType:  AnyAccountTypeCash,
		BaseCurrency: &usd,
		Reported:     true,
		EventID:      "00000000-0000-4000-8000-000000000001",
		EventTime:    1,
		InitTime:     2,
	}
	state2 := AccountStateEvent{
		AccountID:    ids.MustAccountID("TEST-001"),
		AccountType:  AnyAccountTypeCash,
		BaseCurrency: &usd,
		Reported:     true,
		EventID:      "00000000-0000-4000-8000-000000000002",
		EventTime:    3,
		InitTime:     4,
	}

	if !state1.HasSameBalancesAndMargins(state2) {
		t.Fatal("empty financial collections compared different")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/account/state.rs:378
//	test: test_has_same_balances_and_margins_with_multiple_balances_and_margins
func TestAccountStateEventHasSameBalancesAndMarginsWithMultipleBalancesAndMargins(t *testing.T) {
	usd := currency.USD()
	eur := euroStateEventCurrency()
	btcInstrument := ids.MustInstrumentID("BTCUSDT.COINBASE")
	ethInstrument := ids.MustInstrumentID("ETHUSDT.BINANCE")
	balances := []AccountBalance{
		stateEventBalance("1000000", "0", "1000000", usd),
		stateEventBalance("500000", "10000", "490000", eur),
	}
	margins := []MarginBalance{
		MustMarginBalance(
			money.MustNew("5000", usd),
			money.MustNew("20000", usd),
			&btcInstrument,
		),
		MustMarginBalance(
			money.MustNew("3000", usd),
			money.MustNew("15000", usd),
			&ethInstrument,
		),
	}
	state1 := AccountStateEvent{
		AccountID:    ids.MustAccountID("TEST-001"),
		AccountType:  AnyAccountTypeMargin,
		BaseCurrency: &usd,
		Balances:     balances,
		Margins:      margins,
		Reported:     true,
		EventID:      "00000000-0000-4000-8000-000000000001",
		EventTime:    1,
		InitTime:     2,
	}
	state2 := AccountStateEvent{
		AccountID:    ids.MustAccountID("TEST-001"),
		AccountType:  AnyAccountTypeMargin,
		BaseCurrency: &usd,
		Balances:     append([]AccountBalance(nil), balances...),
		Margins:      append([]MarginBalance(nil), margins...),
		Reported:     true,
		EventID:      "00000000-0000-4000-8000-000000000002",
		EventTime:    3,
		InitTime:     4,
	}

	if !state1.HasSameBalancesAndMargins(state2) {
		t.Fatal("matching multi-currency financial state compared different")
	}
}
