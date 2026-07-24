package account

import (
	"math/big"
	"testing"

	"github.com/upcomers-org/platformgo/internal/currency"
	"github.com/upcomers-org/platformgo/internal/ids"
	"github.com/upcomers-org/platformgo/internal/money"
)

func baseAccountBalance(total, locked, free string, denomination currency.Currency) AccountBalance {
	return MustAccountBalance(
		money.MustNew(total, denomination),
		money.MustNew(locked, denomination),
		money.MustNew(free, denomination),
	)
}

func baseCashAccountState() BaseAccountState {
	usd := currency.USD()
	return BaseAccountState{
		AccountID:    ids.MustAccountID("SIM-001"),
		AccountType:  AnyAccountTypeCash,
		Balances:     []AccountBalance{baseAccountBalance("1525000", "25000", "1500000", usd)},
		Reported:     true,
		BaseCurrency: &usd,
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/base.rs:389
//	test: test_base_purge_account_events_retains_latest_when_all_purged
func TestBasePurgeAccountEventsRetainsLatestWhenAllPurged(t *testing.T) {
	account := NewBaseAccount(baseCashAccountState(), true)
	event1 := baseCashAccountState()
	event1.EventTime = 100_000_000
	event1.InitTime = 100_000_000
	event2 := baseCashAccountState()
	event2.EventTime = 200_000_000
	event2.InitTime = 200_000_000
	event3 := baseCashAccountState()
	event3.EventTime = 300_000_000
	event3.InitTime = 300_000_000

	account.Apply(event1)
	account.Apply(event2)
	account.Apply(event3)
	if account.EventCount() != 4 {
		t.Fatalf("event count before purge = %d", account.EventCount())
	}

	account.PurgeAccountEvents(1_000_000_000, 0)

	events := account.Events()
	if len(events) != 1 {
		t.Fatalf("event count after purge = %d", len(events))
	}
	if events[0].EventTime != event3.EventTime {
		t.Fatalf("retained event time = %d, want %d", events[0].EventTime, event3.EventTime)
	}
	last, ok := account.LastEvent()
	if !ok || last.EventTime != event3.EventTime {
		t.Fatalf("LastEvent() = %#v, %v", last, ok)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/base.rs:448
//	test: test_update_commissions_sub_canonical_raw_skipped
func TestBaseUpdateCommissionsSubCanonicalRawSkipped(t *testing.T) {
	account := NewBaseAccount(baseCashAccountState(), true)
	usd := currency.USD()
	subCanonical := money.MustFromRaw(big.NewInt(1), usd)

	account.UpdateCommissions(subCanonical)

	if commission, ok := account.Commission(usd); ok {
		t.Fatalf("Commission(USD) = %s", commission)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/base.rs:464
//	test: test_try_update_commissions_overflow_preserves_total
func TestBaseTryUpdateCommissionsOverflowPreservesTotal(t *testing.T) {
	account := NewBaseAccount(baseCashAccountState(), true)
	usd := currency.USD()
	maximum := money.MustFromRaw(money.MaxRaw(), usd)

	if err := account.TryUpdateCommissions(maximum); err != nil {
		t.Fatalf("first TryUpdateCommissions() error = %v", err)
	}
	err := account.TryUpdateCommissions(money.MustNew("0.01", usd))

	if err == nil {
		t.Fatal("second TryUpdateCommissions() error = nil")
	}
	got, ok := account.Commission(usd)
	if !ok || !got.Equal(maximum) {
		t.Fatalf("Commission(USD) = %s, %v; want %s", got, ok, maximum)
	}
}
