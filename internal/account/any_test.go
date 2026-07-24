package account

import (
	"testing"

	"github.com/upcomers-org/platformgo/internal/currency"
	"github.com/upcomers-org/platformgo/internal/ids"
	"github.com/upcomers-org/platformgo/internal/money"
)

func anyAccountBalance(total, locked, free string, denomination currency.Currency) AccountBalance {
	return MustAccountBalance(
		money.MustNew(total, denomination),
		money.MustNew(locked, denomination),
		money.MustNew(free, denomination),
	)
}

func anyCashAccountState() AnyAccountState {
	usd := currency.USD()
	return AnyAccountState{
		AccountID:    ids.MustAccountID("SIM-001"),
		AccountType:  AnyAccountTypeCash,
		Balances:     []AccountBalance{anyAccountBalance("1525000", "25000", "1500000", usd)},
		Reported:     true,
		BaseCurrency: &usd,
	}
}

func anyMarginAccountState() AnyAccountState {
	usd := currency.USD()
	instrumentID := ids.MustInstrumentID("BTCUSDT.COINBASE")
	return AnyAccountState{
		AccountID:   ids.MustAccountID("SIM-001"),
		AccountType: AnyAccountTypeMargin,
		Balances:    []AccountBalance{anyAccountBalance("1525000", "25000", "1500000", usd)},
		Margins: []MarginBalance{MustMarginBalance(
			money.MustNew("5000", usd),
			money.MustNew("20000", usd),
			&instrumentID,
		)},
		Reported:     true,
		BaseCurrency: &usd,
	}
}

func anyBettingAccountState() AnyAccountState {
	gbp := currency.MustNew("GBP", 2, 826, "Pound sterling", currency.Fiat)
	return AnyAccountState{
		AccountID:    ids.MustAccountID("SIM-001"),
		AccountType:  AnyAccountTypeBetting,
		Balances:     []AccountBalance{anyAccountBalance("1000", "0", "1000", gbp)},
		Reported:     true,
		BaseCurrency: &gbp,
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/any.rs:250
//	test: test_from_events_empty_returns_error
func TestAccountAnyFromEventsEmptyReturnsError(t *testing.T) {
	events := []AnyAccountState{}

	_, err := AccountAnyFromEvents(events)
	if err == nil {
		t.Fatal("AccountAnyFromEvents() error = nil")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/any.rs:257
//	test: test_from_events_single_cash_event
func TestAccountAnyFromEventsSingleCashEvent(t *testing.T) {
	state := anyCashAccountState()

	account, err := AccountAnyFromEvents([]AnyAccountState{state})
	if err != nil {
		t.Fatalf("AccountAnyFromEvents() error = %v", err)
	}
	if !account.IsCash() {
		t.Fatal("AccountAnyFromEvents() did not return the cash variant")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/any.rs:264
//	test: test_from_events_single_margin_event
func TestAccountAnyFromEventsSingleMarginEvent(t *testing.T) {
	state := anyMarginAccountState()

	account, err := AccountAnyFromEvents([]AnyAccountState{state})
	if err != nil {
		t.Fatalf("AccountAnyFromEvents() error = %v", err)
	}
	if !account.IsMargin() {
		t.Fatal("AccountAnyFromEvents() did not return the margin variant")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/any.rs:271
//	test: test_try_from_state_cash
func TestTryAccountAnyFromStateCash(t *testing.T) {
	state := anyCashAccountState()

	account, err := TryAccountAnyFromState(state)
	if err != nil {
		t.Fatalf("TryAccountAnyFromState() error = %v", err)
	}
	if !account.IsCash() {
		t.Fatal("TryAccountAnyFromState() did not return the cash variant")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/any.rs:278
//	test: test_try_from_state_margin
func TestTryAccountAnyFromStateMargin(t *testing.T) {
	state := anyMarginAccountState()

	account, err := TryAccountAnyFromState(state)
	if err != nil {
		t.Fatalf("TryAccountAnyFromState() error = %v", err)
	}
	if !account.IsMargin() {
		t.Fatal("TryAccountAnyFromState() did not return the margin variant")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/any.rs:285
//	test: test_try_from_state_betting
func TestTryAccountAnyFromStateBetting(t *testing.T) {
	state := anyBettingAccountState()

	account, err := TryAccountAnyFromState(state)
	if err != nil {
		t.Fatalf("TryAccountAnyFromState() error = %v", err)
	}
	if !account.IsBetting() {
		t.Fatal("TryAccountAnyFromState() did not return the betting variant")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/any.rs:292
//	test: test_try_from_state_wallet_returns_error
func TestTryAccountAnyFromStateWalletReturnsError(t *testing.T) {
	state := AnyAccountState{
		AccountID:   ids.MustAccountID("WALLET-001"),
		AccountType: AnyAccountTypeWallet,
		Reported:    true,
	}

	_, err := TryAccountAnyFromState(state)
	if err == nil {
		t.Fatal("TryAccountAnyFromState() error = nil")
	}
}
