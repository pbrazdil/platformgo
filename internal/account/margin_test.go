package account

import (
	"testing"

	"github.com/upcomers-org/platformgo/internal/currency"
	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
	"github.com/upcomers-org/platformgo/internal/money"
)

func marginState() MarginAccountState {
	usd := currency.USD()
	instrumentID := ids.MustInstrumentID("BTCUSDT.COINBASE")
	return MarginAccountState{
		AccountID:    ids.MustAccountID("SIM-001"),
		Balances:     []AccountBalance{balance("1525000", "25000", "1500000", usd)},
		Margins:      []MarginBalance{MustMarginBalance(money.MustNew("5000", usd), money.MustNew("20000", usd), &instrumentID)},
		Reported:     true,
		BaseCurrency: &usd,
	}
}

func marginAccount() *MarginAccount {
	return NewMarginAccount(marginState(), true)
}

func audUSDID() ids.InstrumentID {
	return ids.MustInstrumentID("AUD/USD.SIM")
}

func audUSDMarginInstrument() MarginInstrument {
	base := aud()
	return MarginInstrument{
		ID:                audUSDID(),
		BaseCurrency:      &base,
		QuoteCurrency:     currency.USD(),
		Multiplier:        decimal.MustParse("1"),
		InitialMarginRate: decimal.MustParse("0.03"),
		MaintenanceRate:   decimal.MustParse("0.03"),
	}
}

func inverseMarginInstrument() MarginInstrument {
	base := currency.BTC()
	return MarginInstrument{
		ID:                ids.MustInstrumentID("BTCUSDT.BITMEX"),
		BaseCurrency:      &base,
		QuoteCurrency:     currency.USD(),
		Inverse:           true,
		Multiplier:        decimal.MustParse("1"),
		InitialMarginRate: decimal.MustParse("0.01"),
		MaintenanceRate:   decimal.MustParse("0.0035"),
	}
}

func btcUSDTMarginInstrument() MarginInstrument {
	base := currency.BTC()
	return MarginInstrument{
		ID:            ids.MustInstrumentID("BTCUSDT.BINANCE"),
		BaseCurrency:  &base,
		QuoteCurrency: currency.USDT(),
		Multiplier:    decimal.MustParse("1"),
	}
}

func optionMarginInstrument() MarginInstrument {
	return MarginInstrument{
		ID:            ids.MustInstrumentID("AAPL240621C00150000.OPRA"),
		QuoteCurrency: currency.USD(),
		Multiplier:    decimal.MustParse("1"),
		Premium:       true,
	}
}

func binaryMarginInstrument() MarginInstrument {
	return MarginInstrument{
		ID:            ids.MustInstrumentID("BINARY.POLYMARKET"),
		QuoteCurrency: currency.USD(),
		Multiplier:    decimal.MustParse("1"),
		Premium:       true,
	}
}

func requireMarginMoney(t *testing.T, got money.Money, amount string, denomination currency.Currency) {
	t.Helper()
	want := money.MustNew(amount, denomination)
	if !got.Equal(want) {
		t.Fatalf("money = %s, want %s", got, want)
	}
}

func requireMarginDecimal(t *testing.T, got decimal.Decimal, want string) {
	t.Helper()
	expected := decimal.MustParse(want)
	if !got.Equal(expected) {
		t.Fatalf("decimal = %s, want %s", got, expected)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/margin.rs:761
//	test: test_display
func TestMarginAccountDisplay(t *testing.T) {
	if got := marginAccount().String(); got != "MarginAccount(id=SIM-001, type=MARGIN, base=USD)" {
		t.Fatalf("String() = %q", got)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/margin.rs:769
//	test: test_calculated_account_state_returns_field_value
func TestMarginAccountCalculatedAccountStateReturnsFieldValue(t *testing.T) {
	state := marginState()
	if !NewMarginAccount(state, true).CalculatedAccountState() {
		t.Fatal("true calculated-account-state flag was lost")
	}
	if NewMarginAccount(state, false).CalculatedAccountState() {
		t.Fatal("false calculated-account-state flag was lost")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/margin.rs:775
//	test: test_base_account_properties
func TestMarginAccountBaseAccountProperties(t *testing.T) {
	state := marginState()
	account := NewMarginAccount(state, true)
	base, ok := account.BaseCurrency()
	if !ok || !base.Equal(currency.USD()) || account.EventCount() != 1 ||
		len(account.Events()) != 1 {
		t.Fatalf("base/events = %s/%v/%d", base, ok, account.EventCount())
	}
	last, ok := account.LastEvent()
	if !ok || last.Sequence != state.Sequence || len(last.Margins) != 1 {
		t.Fatal("last event was not retained")
	}
	total, _ := account.BalanceTotal(nil)
	free, _ := account.BalanceFree(nil)
	locked, _ := account.BalanceLocked(nil)
	requireMarginMoney(t, total, "1525000", currency.USD())
	requireMarginMoney(t, free, "1500000", currency.USD())
	requireMarginMoney(t, locked, "25000", currency.USD())
	if len(account.BalancesTotal()) != 1 || len(account.BalancesFree()) != 1 ||
		len(account.BalancesLocked()) != 1 {
		t.Fatal("balance views lost USD")
	}
	instrumentID := *state.Margins[0].InstrumentID
	requireMarginMoney(t, account.InitialMargin(instrumentID), "5000", currency.USD())
	requireMarginMoney(t, account.MaintenanceMargin(instrumentID), "20000", currency.USD())
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/margin.rs:823
//	test: test_set_default_leverage
func TestMarginAccountSetDefaultLeverage(t *testing.T) {
	account := marginAccount()
	requireMarginDecimal(t, account.DefaultLeverage(), "1")
	account.SetDefaultLeverage(decimal.MustParse("10"))
	requireMarginDecimal(t, account.DefaultLeverage(), "10")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/margin.rs:830
//	test: test_get_leverage_default_leverage
func TestMarginAccountGetLeverageDefaultLeverage(t *testing.T) {
	requireMarginDecimal(t, marginAccount().Leverage(audUSDID()), "1")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/margin.rs:841
//	test: test_set_leverage
func TestMarginAccountSetLeverage(t *testing.T) {
	account := marginAccount()
	if account.LeverageCount() != 0 {
		t.Fatal("new account has instrument leverage")
	}
	account.SetLeverage(audUSDID(), decimal.MustParse("10"))
	if account.LeverageCount() != 1 {
		t.Fatalf("leverage count = %d", account.LeverageCount())
	}
	requireMarginDecimal(t, account.Leverage(audUSDID()), "10")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/margin.rs:855
//	test: test_is_unleveraged_with_leverage_returns_false
func TestMarginAccountIsUnleveragedWithLeverageReturnsFalse(t *testing.T) {
	account := marginAccount()
	account.SetLeverage(audUSDID(), decimal.MustParse("10"))
	if account.IsUnleveraged(audUSDID()) {
		t.Fatal("10x leverage reported unleveraged")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/margin.rs:864
//	test: test_is_unleveraged_with_no_leverage_returns_true
func TestMarginAccountIsUnleveragedWithNoLeverageReturnsTrue(t *testing.T) {
	account := marginAccount()
	account.SetLeverage(audUSDID(), decimal.MustParse("1"))
	if !account.IsUnleveraged(audUSDID()) {
		t.Fatal("1x leverage reported leveraged")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/margin.rs:873
//	test: test_is_unleveraged_with_default_leverage_of_1_returns_true
func TestMarginAccountIsUnleveragedWithDefaultLeverageOneReturnsTrue(t *testing.T) {
	if !marginAccount().IsUnleveraged(audUSDID()) {
		t.Fatal("default 1x leverage reported leveraged")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/margin.rs:881
//	test: test_update_margin_init
func TestMarginAccountUpdateInitialMargin(t *testing.T) {
	account := marginAccount()
	initialCount := account.MarginCount()
	value := money.MustNew("10000", currency.USD())
	account.UpdateInitialMargin(audUSDID(), value)
	if account.MarginCount() != initialCount+1 {
		t.Fatalf("margin count = %d", account.MarginCount())
	}
	requireMarginMoney(t, account.InitialMargin(audUSDID()), "10000", currency.USD())
	margin, ok := account.Margin(audUSDID())
	if !ok || !margin.Initial.Equal(value) || !margin.Maintenance.IsZero() {
		t.Fatalf("margin = %+v, ok=%v", margin, ok)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/margin.rs:912
//	test: test_update_margin_maintenance
func TestMarginAccountUpdateMaintenanceMargin(t *testing.T) {
	account := marginAccount()
	initialCount := account.MarginCount()
	value := money.MustNew("10000", currency.USD())
	account.UpdateMaintenanceMargin(audUSDID(), value)
	if account.MarginCount() != initialCount+1 {
		t.Fatalf("margin count = %d", account.MarginCount())
	}
	margin, ok := account.Margin(audUSDID())
	if !ok || !margin.Maintenance.Equal(value) || !margin.Initial.IsZero() {
		t.Fatalf("margin = %+v, ok=%v", margin, ok)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/margin.rs:942
//	test: test_clear_initial_margin_preserves_maintenance
func TestMarginAccountClearInitialMarginPreservesMaintenance(t *testing.T) {
	account := marginAccount()
	id := audUSDID()
	account.UpdateMargin(MustMarginBalance(
		money.MustNew("1000", currency.USD()),
		money.MustNew("500", currency.USD()),
		&id,
	))
	account.ClearInitialMargin(id)
	value, ok := account.Margin(id)
	if !ok || !value.Initial.IsZero() {
		t.Fatalf("margin = %+v, ok=%v", value, ok)
	}
	requireMarginMoney(t, value.Maintenance, "500", currency.USD())
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/margin.rs:962
//	test: test_clear_maintenance_margin_removes_empty_entry
func TestMarginAccountClearMaintenanceMarginRemovesEmptyEntry(t *testing.T) {
	account := marginAccount()
	id := audUSDID()
	account.UpdateMargin(MustMarginBalance(
		money.Zero(currency.USD()),
		money.MustNew("500", currency.USD()),
		&id,
	))
	account.ClearMaintenanceMargin(id)
	if _, ok := account.Margin(id); ok {
		t.Fatal("empty margin entry was retained")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/margin.rs:978
//	test: test_clear_maintenance_margin_preserves_initial
func TestMarginAccountClearMaintenanceMarginPreservesInitial(t *testing.T) {
	account := marginAccount()
	id := audUSDID()
	account.UpdateMargin(MustMarginBalance(
		money.MustNew("1000", currency.USD()),
		money.MustNew("500", currency.USD()),
		&id,
	))
	account.ClearMaintenanceMargin(id)
	value, ok := account.Margin(id)
	if !ok || !value.Maintenance.IsZero() {
		t.Fatalf("margin = %+v, ok=%v", value, ok)
	}
	requireMarginMoney(t, value.Initial, "1000", currency.USD())
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/margin.rs:998
//	test: test_apply_replaces_margin_balances_from_event
func TestMarginAccountApplyReplacesMarginBalancesFromEvent(t *testing.T) {
	state := marginState()
	account := NewMarginAccount(state, true)
	oldID := *state.Margins[0].InstrumentID
	newID := ids.MustInstrumentID("USDJPY.SIM")
	event := marginState()
	event.Sequence = 1
	event.Margins = []MarginBalance{MustMarginBalance(
		money.MustNew("12500", currency.USD()),
		money.MustNew("25000", currency.USD()),
		&newID,
	)}
	if err := account.Apply(event); err != nil {
		t.Fatal(err)
	}
	requireMarginMoney(t, account.InitialMargin(newID), "12500", currency.USD())
	requireMarginMoney(t, account.MaintenanceMargin(newID), "25000", currency.USD())
	if _, ok := account.Margin(oldID); ok {
		t.Fatal("old margin was retained")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/margin.rs:1036
//	test: test_apply_routes_account_margins_by_currency
func TestMarginAccountApplyRoutesAccountMarginsByCurrency(t *testing.T) {
	account := marginAccount()
	event := marginState()
	event.Sequence = 1
	event.Margins = []MarginBalance{MustMarginBalance(
		money.MustNew("12500", currency.USD()),
		money.MustNew("25000", currency.USD()),
		nil,
	)}
	if err := account.Apply(event); err != nil {
		t.Fatal(err)
	}
	if account.MarginCount() != 0 || account.AccountMarginCount() != 1 {
		t.Fatalf("margin counts = %d/%d", account.MarginCount(), account.AccountMarginCount())
	}
	initial, ok := account.AccountInitialMargin(currency.USD())
	if !ok {
		t.Fatal("account initial margin missing")
	}
	requireMarginMoney(t, initial, "12500", currency.USD())
	maintenance, ok := account.AccountMaintenanceMargin(currency.USD())
	if !ok {
		t.Fatal("account maintenance margin missing")
	}
	requireMarginMoney(t, maintenance, "25000", currency.USD())
	requireMarginMoney(t, account.TotalInitialMargin(currency.USD()), "12500", currency.USD())
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/margin.rs:1076
//	test: test_apply_empty_event_preserves_margin_balances
func TestMarginAccountApplyEmptyEventPreservesMarginBalances(t *testing.T) {
	state := marginState()
	account := NewMarginAccount(state, true)
	id := *state.Margins[0].InstrumentID
	initial := account.InitialMargin(id)
	maintenance := account.MaintenanceMargin(id)
	empty := MarginAccountState{
		AccountID:    state.AccountID,
		Reported:     true,
		BaseCurrency: state.BaseCurrency,
		Sequence:     1,
	}
	if err := account.Apply(empty); err != nil {
		t.Fatal(err)
	}
	if !account.InitialMargin(id).Equal(initial) ||
		!account.MaintenanceMargin(id).Equal(maintenance) ||
		account.EventCount() != 2 {
		t.Fatal("empty event changed margins or event count")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/margin.rs:1109
//	test: test_calculate_margin_init_with_leverage
func TestCalculateInitialMarginWithLeverage(t *testing.T) {
	account := marginAccount()
	account.SetLeverage(audUSDID(), decimal.MustParse("50"))
	got, err := account.CalculateInitialMargin(
		audUSDMarginInstrument(),
		decimal.MustParse("100000"),
		decimal.MustParse("0.8000"),
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	requireMarginMoney(t, got, "48.00", currency.USD())
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/margin.rs:1126
//	test: test_calculate_margin_init_with_default_leverage
func TestCalculateInitialMarginWithDefaultLeverage(t *testing.T) {
	account := marginAccount()
	account.SetDefaultLeverage(decimal.MustParse("10"))
	got, err := account.CalculateInitialMargin(
		audUSDMarginInstrument(),
		decimal.MustParse("100000"),
		decimal.MustParse("0.8"),
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	requireMarginMoney(t, got, "240.00", currency.USD())
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/margin.rs:1143
//	test: test_calculate_margin_init_with_no_leverage_for_inverse
func TestCalculateInitialMarginWithNoLeverageForInverse(t *testing.T) {
	account := marginAccount()
	instrument := inverseMarginInstrument()
	base, err := account.CalculateInitialMargin(
		instrument, decimal.MustParse("100000"), decimal.MustParse("11493.60"), false,
	)
	if err != nil {
		t.Fatal(err)
	}
	requireMarginMoney(t, base, "0.08700494", currency.BTC())
	quote, err := account.CalculateInitialMargin(
		instrument, decimal.MustParse("100000"), decimal.MustParse("11493.60"), true,
	)
	if err != nil {
		t.Fatal(err)
	}
	requireMarginMoney(t, quote, "1000", currency.USD())
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/margin.rs:1168
//	test: test_calculate_margin_maintenance_with_no_leverage
func TestCalculateMaintenanceMarginWithNoLeverage(t *testing.T) {
	got, err := marginAccount().CalculateMaintenanceMargin(
		inverseMarginInstrument(),
		decimal.MustParse("100000"),
		decimal.MustParse("11493.60"),
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	requireMarginMoney(t, got, "0.03045173", currency.BTC())
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/margin.rs:1184
//	test: test_calculate_margin_maintenance_with_leverage_fx_instrument
func TestCalculateMaintenanceMarginWithLeverageFXInstrument(t *testing.T) {
	account := marginAccount()
	account.SetDefaultLeverage(decimal.MustParse("50"))
	got, err := account.CalculateMaintenanceMargin(
		audUSDMarginInstrument(),
		decimal.MustParse("1000000"),
		decimal.MustParse("1"),
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	requireMarginMoney(t, got, "600.00", currency.USD())
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/margin.rs:1201
//	test: test_calculate_margin_maintenance_with_leverage_inverse_instrument
func TestCalculateMaintenanceMarginWithLeverageInverseInstrument(t *testing.T) {
	account := marginAccount()
	account.SetDefaultLeverage(decimal.MustParse("10"))
	got, err := account.CalculateMaintenanceMargin(
		inverseMarginInstrument(),
		decimal.MustParse("100000"),
		decimal.MustParse("100000.00"),
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	requireMarginMoney(t, got, "0.00035000", currency.BTC())
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/margin.rs:1218
//	test: test_calculate_pnls_github_issue_2657
func TestCalculatePnLsGitHubIssue2657(t *testing.T) {
	position := &MarginPosition{
		EntrySide: OrderSideBuy,
		Quantity:  decimal.MustParse("0.001"),
		AvgPrice:  decimal.MustParse("50000.00"),
	}
	got, err := marginAccount().CalculatePnLs(
		btcUSDTMarginInstrument(),
		OrderSideSell,
		decimal.MustParse("0.002"),
		decimal.MustParse("50075.00"),
		position,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("PnL count = %d", len(got))
	}
	requireMarginMoney(t, got[0], "0.075", currency.USDT())
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/margin.rs:1272
//	test: test_set_leverage_zero_panics
func TestMarginAccountSetLeverageZeroPanics(t *testing.T) {
	requirePanicContains(t, "not positive", func() {
		marginAccount().SetLeverage(audUSDID(), decimal.MustParse("0"))
	})
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/margin.rs:1278
//	test: test_set_default_leverage_zero_panics
func TestMarginAccountSetDefaultLeverageZeroPanics(t *testing.T) {
	requirePanicContains(t, "not positive", func() {
		marginAccount().SetDefaultLeverage(decimal.MustParse("0"))
	})
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/margin.rs:1284
//	test: test_set_leverage_negative_panics
func TestMarginAccountSetLeverageNegativePanics(t *testing.T) {
	requirePanicContains(t, "not positive", func() {
		marginAccount().SetLeverage(audUSDID(), decimal.MustParse("-1"))
	})
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/margin.rs:1292
//	test: test_calculate_pnls_with_same_side_fill_returns_empty
func TestCalculatePnLsWithSameSideFillReturnsEmpty(t *testing.T) {
	position := &MarginPosition{
		EntrySide: OrderSideBuy,
		Quantity:  decimal.MustParse("1.0"),
		AvgPrice:  decimal.MustParse("50000.00"),
	}
	got, err := marginAccount().CalculatePnLs(
		btcUSDTMarginInstrument(),
		OrderSideBuy,
		decimal.MustParse("0.5"),
		decimal.MustParse("51000.00"),
		position,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("PnLs = %+v", got)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/margin.rs:1349
//	test: test_margin_accessor
func TestMarginAccountMarginAccessor(t *testing.T) {
	account := marginAccount()
	id := audUSDID()
	account.UpdateMargin(MustMarginBalance(
		money.MustNew("1000", currency.USD()),
		money.MustNew("500", currency.USD()),
		&id,
	))
	value, ok := account.Margin(id)
	if !ok || value.InstrumentID == nil || *value.InstrumentID != id {
		t.Fatalf("margin = %+v, ok=%v", value, ok)
	}
	requireMarginMoney(t, value.Initial, "1000", currency.USD())
	requireMarginMoney(t, value.Maintenance, "500", currency.USD())
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/margin.rs:1370
//	test: test_clear_margin
func TestMarginAccountClearMargin(t *testing.T) {
	account := marginAccount()
	id := audUSDID()
	account.UpdateMargin(MustMarginBalance(
		money.MustNew("1000", currency.USD()),
		money.MustNew("500", currency.USD()),
		&id,
	))
	if _, ok := account.Margin(id); !ok {
		t.Fatal("margin was not stored")
	}
	account.ClearMargin(id)
	if _, ok := account.Margin(id); ok {
		t.Fatal("margin was not cleared")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/margin.rs:1388
//	test: test_update_margin_routes_account_wide
func TestMarginAccountUpdateMarginRoutesAccountWide(t *testing.T) {
	account := marginAccount()
	value := MustMarginBalance(
		money.MustNew("200", currency.USD()),
		money.MustNew("100", currency.USD()),
		nil,
	)
	account.UpdateMargin(value)
	got, ok := account.AccountMargin(currency.USD())
	if !ok || !got.Equal(value) {
		t.Fatalf("account margin = %+v, ok=%v", got, ok)
	}
	initial, _ := account.AccountInitialMargin(currency.USD())
	maintenance, _ := account.AccountMaintenanceMargin(currency.USD())
	requireMarginMoney(t, initial, "200", currency.USD())
	requireMarginMoney(t, maintenance, "100", currency.USD())
	account.ClearAccountMargin(currency.USD())
	if _, ok := account.AccountMargin(currency.USD()); ok {
		t.Fatal("account margin was not cleared")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/margin.rs:1410
//	test: test_total_margin_sums_per_instrument_and_account_wide
func TestMarginAccountTotalMarginSumsPerInstrumentAndAccountWide(t *testing.T) {
	account := marginAccount()
	baselineInitial := account.TotalInitialMargin(currency.USD())
	baselineMaintenance := account.TotalMaintenanceMargin(currency.USD())
	id := audUSDID()
	account.UpdateMargin(MustMarginBalance(
		money.MustNew("100", currency.USD()),
		money.MustNew("50", currency.USD()),
		&id,
	))
	account.UpdateMargin(MustMarginBalance(
		money.MustNew("200", currency.USD()),
		money.MustNew("150", currency.USD()),
		nil,
	))
	requireMarginMoney(t, account.TotalInitialMargin(currency.USD()), baselineInitial.Add(money.MustNew("300", currency.USD())).Decimal().String(), currency.USD())
	requireMarginMoney(t, account.TotalMaintenanceMargin(currency.USD()), baselineMaintenance.Add(money.MustNew("200", currency.USD())).Decimal().String(), currency.USD())
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/margin.rs:1440
//	test: test_calculate_pnls_for_option_buy_realizes_premium
func TestCalculatePnLsForOptionBuyRealizesPremium(t *testing.T) {
	got, err := marginAccount().CalculatePnLs(
		optionMarginInstrument(),
		OrderSideBuy,
		decimal.MustParse("10"),
		decimal.MustParse("5.50"),
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("PnL count = %d", len(got))
	}
	requireMarginMoney(t, got[0], "-55", currency.USD())
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/margin.rs:1475
//	test: test_calculate_pnls_for_option_sell_realizes_premium
func TestCalculatePnLsForOptionSellRealizesPremium(t *testing.T) {
	got, err := marginAccount().CalculatePnLs(
		optionMarginInstrument(),
		OrderSideSell,
		decimal.MustParse("10"),
		decimal.MustParse("5.50"),
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("PnL count = %d", len(got))
	}
	requireMarginMoney(t, got[0], "55", currency.USD())
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/margin.rs:1510
//	test: test_calculate_pnls_for_binary_option
func TestCalculatePnLsForBinaryOption(t *testing.T) {
	got, err := marginAccount().CalculatePnLs(
		binaryMarginInstrument(),
		OrderSideBuy,
		decimal.MustParse("100"),
		decimal.MustParse("0.65"),
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !got[0].Neg().IsPositive() {
		t.Fatalf("PnLs = %+v", got)
	}
}
