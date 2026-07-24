package account

import (
	"strings"
	"testing"

	"github.com/upcomers-org/platformgo/internal/currency"
	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
	"github.com/upcomers-org/platformgo/internal/money"
)

func gbpCurrency() currency.Currency {
	return currency.MustNew("GBP", 2, 826, "Pound sterling", currency.Fiat)
}

func bettingBalance(total, locked, free string) AccountBalance {
	gbp := gbpCurrency()
	return MustAccountBalance(
		money.MustNew(total, gbp),
		money.MustNew(locked, gbp),
		money.MustNew(free, gbp),
	)
}

func bettingStateFixture() AccountState {
	gbp := gbpCurrency()
	return AccountState{
		AccountID:    ids.MustAccountID("SIM-001"),
		Balances:     []AccountBalance{bettingBalance("1000", "0", "1000")},
		Reported:     true,
		BaseCurrency: &gbp,
	}
}

func changedBettingStateFixture() AccountState {
	gbp := gbpCurrency()
	return AccountState{
		AccountID:    ids.MustAccountID("SIM-001"),
		Balances:     []AccountBalance{bettingBalance("900", "50", "850")},
		Reported:     true,
		BaseCurrency: &gbp,
		Sequence:     1,
	}
}

func bettingAccountFixture() *BettingAccount {
	return NewBettingAccount(bettingStateFixture(), true)
}

func sportsBettingInstrumentFixture() BettingInstrument {
	return BettingInstrument{
		ID:            ids.MustInstrumentID("1-123456789.BETFAIR"),
		QuoteCurrency: gbpCurrency(),
		SportsBetting: true,
	}
}

func requireBettingMoney(t *testing.T, got money.Money, amount string) {
	t.Helper()
	want := money.MustNew(amount, gbpCurrency())
	if !got.Equal(want) {
		t.Fatalf("money = %s, want %s", got, want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/betting.rs:439
//	test: test_display
func TestBettingDisplay(t *testing.T) {
	account := bettingAccountFixture()

	if got, want := account.String(), "BettingAccount(id=SIM-001, type=BETTING, base=GBP)"; got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/betting.rs:447
//	test: test_instantiate_single_asset_betting_account
func TestInstantiateSingleAssetBettingAccount(t *testing.T) {
	state := bettingStateFixture()
	account := NewBettingAccount(state, true)

	if account.ID() != ids.MustAccountID("SIM-001") {
		t.Fatalf("ID() = %s", account.ID())
	}
	if account.AccountType() != "BETTING" {
		t.Fatalf("AccountType() = %s", account.AccountType())
	}
	base, ok := account.BaseCurrency()
	if !ok || !base.Equal(gbpCurrency()) {
		t.Fatalf("BaseCurrency() = %v, %v", base, ok)
	}
	last, ok := account.LastEvent()
	if !ok || !last.Equal(state) {
		t.Fatalf("LastEvent() = %#v, %v", last, ok)
	}
	events := account.Events()
	if len(events) != 1 || !events[0].Equal(state) {
		t.Fatalf("Events() = %#v", events)
	}
	if account.EventCount() != 1 {
		t.Fatalf("EventCount() = %d", account.EventCount())
	}
	total, ok := account.BalanceTotal(nil)
	if !ok {
		t.Fatal("BalanceTotal(nil) was absent")
	}
	requireBettingMoney(t, total, "1000")
	free, ok := account.BalanceFree(nil)
	if !ok {
		t.Fatal("BalanceFree(nil) was absent")
	}
	requireBettingMoney(t, free, "1000")
	locked, ok := account.BalanceLocked(nil)
	if !ok {
		t.Fatal("BalanceLocked(nil) was absent")
	}
	requireBettingMoney(t, locked, "0")
	totals := account.BalancesTotal()
	if len(totals) != 1 || !totals[0].Currency.Equal(gbpCurrency()) {
		t.Fatalf("BalancesTotal() = %#v", totals)
	}
	requireBettingMoney(t, totals[0].Amount, "1000")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/betting.rs:479
//	test: test_apply_given_new_state_event_updates_correctly
func TestBettingApplyGivenNewStateEventUpdatesCorrectly(t *testing.T) {
	initial := bettingStateFixture()
	changed := changedBettingStateFixture()
	account := NewBettingAccount(initial, true)

	if err := account.Apply(changed); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	last, ok := account.LastEvent()
	if !ok || !last.Equal(changed) {
		t.Fatalf("LastEvent() = %#v, %v", last, ok)
	}
	events := account.Events()
	if len(events) != 2 || !events[0].Equal(initial) || !events[1].Equal(changed) {
		t.Fatalf("Events() = %#v", events)
	}
	if account.EventCount() != 2 {
		t.Fatalf("EventCount() = %d", account.EventCount())
	}
	total, _ := account.BalanceTotal(nil)
	free, _ := account.BalanceFree(nil)
	locked, _ := account.BalanceLocked(nil)
	requireBettingMoney(t, total, "900")
	requireBettingMoney(t, free, "850")
	requireBettingMoney(t, locked, "50")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/betting.rs:518
//	test: test_calculate_balance_locked
func TestBettingCalculateBalanceLocked(t *testing.T) {
	account := bettingAccountFixture()
	instrument := sportsBettingInstrumentFixture()
	cases := []struct {
		side            OrderSide
		price, quantity string
		expected        string
	}{
		{OrderSideSell, "1.60", "10", "10"},
		{OrderSideSell, "2.00", "10", "10"},
		{OrderSideSell, "10.00", "20", "20"},
		{OrderSideBuy, "1.25", "10", "2.5"},
		{OrderSideBuy, "2.00", "10", "10"},
		{OrderSideBuy, "10.00", "10", "90"},
	}
	for _, testCase := range cases {
		got, err := account.CalculateBalanceLocked(
			instrument,
			testCase.side,
			decimal.MustParse(testCase.quantity),
			decimal.MustParse(testCase.price),
			false,
		)
		if err != nil {
			t.Fatalf("CalculateBalanceLocked() error = %v", err)
		}
		requireBettingMoney(t, got, testCase.expected)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/betting.rs:539
//	test: test_calculate_pnls_single_currency_account
func TestBettingCalculatePnLsSingleCurrencyAccount(t *testing.T) {
	account := bettingAccountFixture()
	instrument := sportsBettingInstrumentFixture()
	position := &BettingPosition{EntrySide: OrderSideBuy, Quantity: decimal.MustParse("100")}

	got, err := account.CalculatePnLs(
		instrument,
		OrderSideBuy,
		decimal.MustParse("100"),
		decimal.MustParse("0.8"),
		position,
	)
	if err != nil {
		t.Fatalf("CalculatePnLs() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(CalculatePnLs()) = %d", len(got))
	}
	requireBettingMoney(t, got[0], "-80")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/betting.rs:572
//	test: test_calculate_pnls_partially_closed
func TestBettingCalculatePnLsPartiallyClosed(t *testing.T) {
	account := bettingAccountFixture()
	instrument := sportsBettingInstrumentFixture()
	position := &BettingPosition{EntrySide: OrderSideBuy, Quantity: decimal.MustParse("100")}

	got, err := account.CalculatePnLs(
		instrument,
		OrderSideSell,
		decimal.MustParse("50"),
		decimal.MustParse("0.8"),
		position,
	)
	if err != nil {
		t.Fatalf("CalculatePnLs() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(CalculatePnLs()) = %d", len(got))
	}
	requireBettingMoney(t, got[0], "40")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/betting.rs:623
//	test: test_calculate_commission_invalid_liquidity_side_raises
func TestBettingCalculateCommissionInvalidLiquiditySideRaises(t *testing.T) {
	account := bettingAccountFixture()
	instrument := sportsBettingInstrumentFixture()

	_, err := account.CalculateCommission(
		instrument,
		decimal.MustParse("1"),
		decimal.MustParse("1"),
		LiquiditySide(0),
	)
	if err == nil || !strings.Contains(err.Error(), "Invalid `LiquiditySide`: NO_LIQUIDITY_SIDE") {
		t.Fatalf("CalculateCommission() error = %v", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/betting.rs:647
//	test: test_balance_impact
func TestBettingBalanceImpact(t *testing.T) {
	account := bettingAccountFixture()
	instrument := sportsBettingInstrumentFixture()
	cases := []struct {
		side            OrderSide
		price, quantity string
		expected        string
	}{
		{OrderSideBuy, "5.0", "100", "-400"},
		{OrderSideBuy, "1.5", "100", "-50"},
		{OrderSideSell, "5.0", "100", "-100"},
		{OrderSideSell, "10.0", "100", "-100"},
	}
	for _, testCase := range cases {
		got, err := account.BalanceImpact(
			instrument,
			decimal.MustParse(testCase.quantity),
			decimal.MustParse(testCase.price),
			testCase.side,
		)
		if err != nil {
			t.Fatalf("BalanceImpact() error = %v", err)
		}
		requireBettingMoney(t, got, testCase.expected)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/betting.rs:666
//	test: test_apply_rejects_negative_balance
func TestBettingApplyRejectsNegativeBalance(t *testing.T) {
	account := bettingAccountFixture()
	gbp := gbpCurrency()
	negative := AccountState{
		AccountID:    ids.MustAccountID("SIM-001"),
		Balances:     []AccountBalance{bettingBalance("-50", "0", "-50")},
		Reported:     false,
		BaseCurrency: &gbp,
	}

	err := account.Apply(negative)
	if err == nil || !strings.Contains(err.Error(), "balance would be negative") {
		t.Fatalf("Apply() error = %v", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/betting.rs:694
//	test: test_update_balances_rejects_negative_total
func TestBettingUpdateBalancesRejectsNegativeTotal(t *testing.T) {
	account := bettingAccountFixture()

	err := account.UpdateBalances([]AccountBalance{bettingBalance("-10", "0", "-10")})
	if err == nil {
		t.Fatal("UpdateBalances() error = nil")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/betting.rs:705
//	test: test_recalculate_balance_clamps_locked_to_total
func TestBettingRecalculateBalanceClampsLockedToTotal(t *testing.T) {
	account := bettingAccountFixture()
	instrumentID := ids.MustInstrumentID("BETFAIR-1.2345678-12345678-0.0.NONE")

	account.UpdateBalanceLocked(instrumentID, money.MustNew("1500", gbpCurrency()))

	balance, ok := account.Balance(func() *currency.Currency {
		gbp := gbpCurrency()
		return &gbp
	}())
	if !ok {
		t.Fatal("Balance(GBP) was absent")
	}
	requireBettingMoney(t, balance.Locked, "1000")
	requireBettingMoney(t, balance.Free, "0")
	requireBettingMoney(t, balance.Total, "1000")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/betting.rs:718
//	test: test_calculate_pnls_sell_fill
func TestBettingCalculatePnLsSellFill(t *testing.T) {
	account := bettingAccountFixture()
	instrument := sportsBettingInstrumentFixture()
	position := &BettingPosition{EntrySide: OrderSideSell, Quantity: decimal.MustParse("100")}

	got, err := account.CalculatePnLs(
		instrument,
		OrderSideSell,
		decimal.MustParse("100"),
		decimal.MustParse("0.8"),
		position,
	)
	if err != nil {
		t.Fatalf("CalculatePnLs() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(CalculatePnLs()) = %d", len(got))
	}
	requireBettingMoney(t, got[0], "80")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/betting.rs:751
//	test: test_calculate_balance_locked_rejects_non_betting_instrument
func TestBettingCalculateBalanceLockedRejectsNonBettingInstrument(t *testing.T) {
	account := bettingAccountFixture()
	instrument := BettingInstrument{
		ID:            ids.MustInstrumentID("AUD/USD.SIM"),
		QuoteCurrency: currency.USD(),
		SportsBetting: false,
	}

	_, err := account.CalculateBalanceLocked(
		instrument,
		OrderSideBuy,
		decimal.MustParse("100"),
		decimal.MustParse("1.5"),
		false,
	)
	if err == nil || !strings.Contains(err.Error(), "sports betting") {
		t.Fatalf("CalculateBalanceLocked() error = %v", err)
	}
}
