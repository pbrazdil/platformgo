package account

import (
	"strings"
	"testing"

	"github.com/upcomers-org/platformgo/internal/currency"
	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
	"github.com/upcomers-org/platformgo/internal/money"
)

func aud() currency.Currency {
	return currency.MustNew("AUD", 2, 36, "Australian dollar", currency.Fiat)
}

func eth() currency.Currency {
	return currency.MustNew("ETH", 8, 0, "Ether", currency.Crypto)
}

func jpy() currency.Currency {
	return currency.MustNew("JPY", 0, 392, "Japanese yen", currency.Fiat)
}

func balance(total, locked, free string, denomination currency.Currency) AccountBalance {
	return MustAccountBalance(
		money.MustNew(total, denomination),
		money.MustNew(locked, denomination),
		money.MustNew(free, denomination),
	)
}

func singleState() AccountState {
	usd := currency.USD()
	return AccountState{
		AccountID:    ids.MustAccountID("SIM-001"),
		Balances:     []AccountBalance{balance("1525000", "25000", "1500000", usd)},
		Reported:     true,
		BaseCurrency: &usd,
	}
}

func millionUSDState() AccountState {
	usd := currency.USD()
	return AccountState{
		AccountID:    ids.MustAccountID("SIM-001"),
		Balances:     []AccountBalance{balance("1000000", "0", "1000000", usd)},
		Reported:     true,
		BaseCurrency: &usd,
	}
}

func multiState() AccountState {
	return AccountState{
		AccountID: ids.MustAccountID("SIM-001"),
		Balances: []AccountBalance{
			balance("10", "0", "10", currency.BTC()),
			balance("20", "0", "20", eth()),
		},
		Reported: true,
	}
}

func changedMultiState() AccountState {
	return AccountState{
		AccountID: ids.MustAccountID("SIM-001"),
		Balances: []AccountBalance{
			balance("9", "0.5", "8.5", currency.BTC()),
			balance("20", "0", "20", eth()),
		},
		Reported: true,
		Sequence: 1,
	}
}

func cashSingle() *CashAccount {
	return NewCashAccount(singleState(), true, false)
}

func cashMillionUSD() *CashAccount {
	return NewCashAccount(millionUSDState(), true, false)
}

func cashMulti() *CashAccount {
	return NewCashAccount(multiState(), true, false)
}

func audUSD() Instrument {
	base := aud()
	return Instrument{
		ID:            ids.MustInstrumentID("AUD/USD.SIM"),
		BaseCurrency:  &base,
		QuoteCurrency: currency.USD(),
		Multiplier:    decimal.MustParse("1"),
		MakerFee:      decimal.MustParse("0.00001"),
		TakerFee:      decimal.MustParse("0.00002"),
	}
}

func btcUSDT() Instrument {
	base := currency.BTC()
	return Instrument{
		ID:            ids.MustInstrumentID("BTCUSDT.BINANCE"),
		BaseCurrency:  &base,
		QuoteCurrency: currency.USDT(),
		Multiplier:    decimal.MustParse("1"),
	}
}

func ethBTCQuanto() Instrument {
	base := eth()
	return Instrument{
		ID:                 ids.MustInstrumentID("ETHBTC-123.BINANCE"),
		BaseCurrency:       &base,
		QuoteCurrency:      currency.BTC(),
		SettlementCurrency: currency.USDT(),
		Multiplier:         decimal.MustParse("1"),
	}
}

func xbtUSDInverse() Instrument {
	base := currency.BTC()
	return Instrument{
		ID:                 ids.MustInstrumentID("XBTUSD-PERP.BITMEX"),
		BaseCurrency:       &base,
		QuoteCurrency:      currency.USD(),
		SettlementCurrency: currency.BTC(),
		Inverse:            true,
		Multiplier:         decimal.MustParse("1"),
		MakerFee:           decimal.MustParse("-0.00025"),
		TakerFee:           decimal.MustParse("0.00075"),
	}
}

func equityAAPL() Instrument {
	return Instrument{
		ID:            ids.MustInstrumentID("AAPL.XNAS"),
		QuoteCurrency: currency.USD(),
		Multiplier:    decimal.MustParse("1"),
	}
}

func usdJPY() Instrument {
	base := currency.USD()
	return Instrument{
		ID:            ids.MustInstrumentID("USD/JPY.IDEALPRO"),
		BaseCurrency:  &base,
		QuoteCurrency: jpy(),
		Multiplier:    decimal.MustParse("1"),
		TakerFee:      decimal.MustParse("0.00002"),
	}
}

func currencyPointer(value currency.Currency) *currency.Currency {
	return &value
}

func requireMoneyEqual(t *testing.T, got money.Money, amount string, denomination currency.Currency) {
	t.Helper()
	want := money.MustNew(amount, denomination)
	if !got.Equal(want) {
		t.Fatalf("money = %s, want %s", got, want)
	}
}

func requireBalanceValue(t *testing.T, account *CashAccount, denomination currency.Currency, total, locked, free string) {
	t.Helper()
	value, ok := account.Balance(&denomination)
	if !ok {
		t.Fatalf("missing %s balance", denomination)
	}
	requireMoneyEqual(t, value.Total, total, denomination)
	requireMoneyEqual(t, value.Locked, locked, denomination)
	requireMoneyEqual(t, value.Free, free, denomination)
}

func requireMoneySet(t *testing.T, got []money.Money, want ...money.Money) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	used := make([]bool, len(want))
	for _, value := range got {
		found := false
		for index, expected := range want {
			if !used[index] && value.Equal(expected) {
				used[index], found = true, true
				break
			}
		}
		if !found {
			t.Fatalf("unexpected money %s", value)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/cash.rs:393
//	test: test_display
func TestCashAccountDisplay(t *testing.T) {
	if got := cashSingle().String(); got != "CashAccount(id=SIM-001, type=CASH, base=USD)" {
		t.Fatalf("String() = %q", got)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/cash.rs:401
//	test: test_calculated_account_state_returns_field_value
func TestCashAccountCalculatedAccountStateReturnsFieldValue(t *testing.T) {
	state := singleState()
	if !NewCashAccount(state, true, false).CalculatedAccountState() {
		t.Fatal("true calculated-account-state flag was lost")
	}
	if NewCashAccount(state, false, false).CalculatedAccountState() {
		t.Fatal("false calculated-account-state flag was lost")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/cash.rs:409
//	test: test_instantiate_single_asset_cash_account
func TestInstantiateSingleAssetCashAccount(t *testing.T) {
	state := singleState()
	account := NewCashAccount(state, true, false)
	if account.ID() != ids.MustAccountID("SIM-001") || !account.IsCashAccount() {
		t.Fatalf("account id/type = %s/%v", account.ID(), account.IsCashAccount())
	}
	base, ok := account.BaseCurrency()
	if !ok || !base.Equal(currency.USD()) {
		t.Fatalf("base = %s, ok=%v", base, ok)
	}
	last, ok := account.LastEvent()
	if !ok || !last.Equal(state) || len(account.Events()) != 1 || account.EventCount() != 1 {
		t.Fatal("initial event state was not retained")
	}
	requireBalanceValue(t, account, currency.USD(), "1525000", "25000", "1500000")
	for name, amounts := range map[string][]BalanceAmount{
		"total":  account.BalancesTotal(),
		"free":   account.BalancesFree(),
		"locked": account.BalancesLocked(),
	} {
		if len(amounts) != 1 || !amounts[0].Currency.Equal(currency.USD()) {
			t.Fatalf("%s balances = %+v", name, amounts)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/cash.rs:443
//	test: test_instantiate_multi_asset_cash_account
func TestInstantiateMultiAssetCashAccount(t *testing.T) {
	state := multiState()
	account := NewCashAccount(state, true, false)
	if account.ID() != ids.MustAccountID("SIM-001") || account.EventCount() != 1 {
		t.Fatalf("account id/events = %s/%d", account.ID(), account.EventCount())
	}
	if _, ok := account.BaseCurrency(); ok {
		t.Fatal("multi-asset account unexpectedly has a base currency")
	}
	last, ok := account.LastEvent()
	if !ok || !last.Equal(state) {
		t.Fatal("initial state was not retained")
	}
	requireBalanceValue(t, account, currency.BTC(), "10", "0", "10")
	requireBalanceValue(t, account, eth(), "20", "0", "20")
	if len(account.BalancesTotal()) != 2 ||
		len(account.BalancesFree()) != 2 ||
		len(account.BalancesLocked()) != 2 {
		t.Fatal("multi-asset balance views lost entries")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/cash.rs:498
//	test: test_cash_account_balances_preserve_insertion_order
func TestCashAccountBalancesPreserveInsertionOrder(t *testing.T) {
	account := cashMulti()
	currencies := account.Currencies()
	if len(currencies) != 2 || currencies[0].Code != "BTC" || currencies[1].Code != "ETH" {
		t.Fatalf("currencies = %+v", currencies)
	}
	totals := account.BalancesTotal()
	if len(totals) != 2 || totals[0].Currency.Code != "BTC" || totals[1].Currency.Code != "ETH" {
		t.Fatalf("totals = %+v", totals)
	}
	requireMoneyEqual(t, totals[0].Amount, "10", currency.BTC())
	requireMoneyEqual(t, totals[1].Amount, "20", eth())
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/cash.rs:518
//	test: test_apply_given_new_state_event_updates_correctly
func TestCashAccountApplyNewStateUpdatesCorrectly(t *testing.T) {
	initial := multiState()
	changed := changedMultiState()
	account := NewCashAccount(initial, true, false)
	if err := account.Apply(changed); err != nil {
		t.Fatal(err)
	}
	last, ok := account.LastEvent()
	events := account.Events()
	if !ok || !last.Equal(changed) || len(events) != 2 ||
		!events[0].Equal(initial) || !events[1].Equal(changed) {
		t.Fatal("event history was not updated")
	}
	requireBalanceValue(t, account, currency.BTC(), "9", "0.5", "8.5")
	requireBalanceValue(t, account, eth(), "20", "0", "20")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/cash.rs:566
//	test: test_calculate_balance_locked_buy
func TestCalculateBalanceLockedBuy(t *testing.T) {
	got, err := cashMillionUSD().CalculateBalanceLocked(
		audUSD(), OrderSideBuy, decimal.MustParse("1000000"), decimal.MustParse("0.8"), false,
	)
	if err != nil {
		t.Fatal(err)
	}
	requireMoneyEqual(t, got, "800000", currency.USD())
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/cash.rs:583
//	test: test_calculate_balance_locked_buy_returns_error_for_unrepresentable_notional
func TestCalculateBalanceLockedBuyReturnsErrorForUnrepresentableNotional(t *testing.T) {
	_, err := cashMillionUSD().CalculateBalanceLocked(
		audUSD(), OrderSideBuy, decimal.MustParse("100000000"), decimal.MustParse("100000000"), false,
	)
	if err == nil {
		t.Fatal("unrepresentable notional was accepted")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/cash.rs:599
//	test: test_calculate_balance_locked_buy_quanto_uses_quote_currency
func TestCalculateBalanceLockedBuyQuantoUsesQuoteCurrency(t *testing.T) {
	got, err := cashMillionUSD().CalculateBalanceLocked(
		ethBTCQuanto(), OrderSideBuy, decimal.MustParse("5"), decimal.MustParse("0.036"), false,
	)
	if err != nil {
		t.Fatal(err)
	}
	requireMoneyEqual(t, got, "0.18", currency.BTC())
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/cash.rs:618
//	test: test_calculate_balance_locked_buy_inverse_respects_quote_flag
func TestCalculateBalanceLockedBuyInverseRespectsQuoteFlag(t *testing.T) {
	for _, test := range []struct {
		useQuote     bool
		want         string
		denomination currency.Currency
	}{
		{false, "0.002", currency.BTC()},
		{true, "100", currency.USD()},
	} {
		got, err := cashMillionUSD().CalculateBalanceLocked(
			xbtUSDInverse(), OrderSideBuy, decimal.MustParse("100"), decimal.MustParse("50000"), test.useQuote,
		)
		if err != nil {
			t.Fatal(err)
		}
		requireMoneyEqual(t, got, test.want, test.denomination)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/cash.rs:637
//	test: test_calculate_balance_locked_sell
func TestCalculateBalanceLockedSell(t *testing.T) {
	got, err := cashMillionUSD().CalculateBalanceLocked(
		audUSD(), OrderSideSell, decimal.MustParse("1000000"), decimal.MustParse("0.8"), false,
	)
	if err != nil {
		t.Fatal(err)
	}
	requireMoneyEqual(t, got, "1000000", aud())
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/cash.rs:654
//	test: test_calculate_balance_locked_sell_no_base_currency
func TestCalculateBalanceLockedSellNoBaseCurrency(t *testing.T) {
	got, err := cashMillionUSD().CalculateBalanceLocked(
		equityAAPL(), OrderSideSell, decimal.MustParse("100"), decimal.MustParse("1500.0"), false,
	)
	if err != nil {
		t.Fatal(err)
	}
	requireMoneyEqual(t, got, "100", currency.USD())
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/cash.rs:671
//	test: test_calculate_pnls_for_single_currency_cash_account
func TestCalculatePnLsForSingleCurrencyCashAccount(t *testing.T) {
	got, err := cashMillionUSD().CalculatePnLs(
		audUSD(), OrderSideBuy, decimal.MustParse("1000000"), decimal.MustParse("0.8"),
	)
	if err != nil {
		t.Fatal(err)
	}
	requireMoneySet(t, got, money.MustNew("-800000", currency.USD()))
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/cash.rs:702
//	test: test_calculate_pnls_for_multi_currency_cash_account_btcusdt
func TestCalculatePnLsForMultiCurrencyCashAccountBTCUSDT(t *testing.T) {
	account := cashMulti()
	sell, err := account.CalculatePnLs(
		btcUSDT(), OrderSideSell, decimal.MustParse("0.5"), decimal.MustParse("45500.00"),
	)
	if err != nil {
		t.Fatal(err)
	}
	buy, err := account.CalculatePnLs(
		btcUSDT(), OrderSideBuy, decimal.MustParse("0.5"), decimal.MustParse("45500.00"),
	)
	if err != nil {
		t.Fatal(err)
	}
	requireMoneySet(t, sell,
		money.MustNew("22750", currency.USDT()),
		money.MustNew("-0.5", currency.BTC()),
	)
	requireMoneySet(t, buy,
		money.MustNew("-22750", currency.USDT()),
		money.MustNew("0.5", currency.BTC()),
	)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/cash.rs:772
//	test: test_calculate_commission_for_inverse_maker_crypto
func TestCalculateCommissionForInverseMakerCrypto(t *testing.T) {
	for _, test := range []struct {
		useQuote     bool
		want         string
		denomination currency.Currency
	}{
		{false, "-0.00218331", currency.BTC()},
		{true, "-25.0", currency.USD()},
	} {
		got, err := cashMillionUSD().CalculateCommission(
			xbtUSDInverse(),
			decimal.MustParse("100000"),
			decimal.MustParse("11450.50"),
			LiquiditySideMaker,
			test.useQuote,
		)
		if err != nil {
			t.Fatal(err)
		}
		requireMoneyEqual(t, got, test.want, test.denomination)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/cash.rs:791
//	test: test_calculate_commission_for_taker_fx
func TestCalculateCommissionForTakerFX(t *testing.T) {
	got, err := cashMillionUSD().CalculateCommission(
		audUSD(),
		decimal.MustParse("1500000"),
		decimal.MustParse("0.8005"),
		LiquiditySideTaker,
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	requireMoneyEqual(t, got, "24.02", currency.USD())
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/cash.rs:808
//	test: test_calculate_commission_crypto_taker
func TestCalculateCommissionCryptoTaker(t *testing.T) {
	got, err := cashMillionUSD().CalculateCommission(
		xbtUSDInverse(),
		decimal.MustParse("100000"),
		decimal.MustParse("11450.50"),
		LiquiditySideTaker,
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	requireMoneyEqual(t, got, "0.00654993", currency.BTC())
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/cash.rs:825
//	test: test_calculate_commission_fx_taker
func TestCalculateCommissionFXTaker(t *testing.T) {
	got, err := cashMillionUSD().CalculateCommission(
		usdJPY(),
		decimal.MustParse("2200000"),
		decimal.MustParse("120.310"),
		LiquiditySideTaker,
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	requireMoneyEqual(t, got, "5294", jpy())
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/cash.rs:840
//	test: test_update_balance_locked_per_instrument_currency
func TestUpdateBalanceLockedPerInstrumentCurrency(t *testing.T) {
	account := cashMulti()
	if account.LockCount() != 0 {
		t.Fatal("new account has locks")
	}
	instrumentID := btcUSDT().ID
	usdtLock := money.MustNew("1000", currency.USDT())
	btcLock := money.MustNew("0.5", currency.BTC())
	account.UpdateBalanceLocked(instrumentID, usdtLock)
	account.UpdateBalanceLocked(instrumentID, btcLock)
	if account.LockCount() != 2 {
		t.Fatalf("lock count = %d", account.LockCount())
	}
	if got, ok := account.InstrumentLock(instrumentID, currency.USDT()); !ok || !got.Equal(usdtLock) {
		t.Fatalf("USDT lock = %s, ok=%v", got, ok)
	}
	if got, ok := account.InstrumentLock(instrumentID, currency.BTC()); !ok || !got.Equal(btcLock) {
		t.Fatalf("BTC lock = %s, ok=%v", got, ok)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/cash.rs:869
//	test: test_clear_balance_locked_removes_all_currencies_for_instrument
func TestClearBalanceLockedRemovesAllCurrenciesForInstrument(t *testing.T) {
	account := cashMulti()
	instrumentID := btcUSDT().ID
	account.UpdateBalanceLocked(instrumentID, money.MustNew("1000", currency.USDT()))
	account.UpdateBalanceLocked(instrumentID, money.MustNew("0.5", currency.BTC()))
	if account.LockCount() != 2 {
		t.Fatalf("lock count = %d", account.LockCount())
	}
	account.ClearBalanceLocked(instrumentID)
	if account.LockCount() != 0 {
		t.Fatalf("lock count after clear = %d", account.LockCount())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/cash.rs:885
//	test: test_clear_balance_locked_only_removes_target_instrument
func TestClearBalanceLockedOnlyRemovesTargetInstrument(t *testing.T) {
	account := cashMulti()
	btcID := btcUSDT().ID
	ethID := ids.MustInstrumentID("ETHUSDT.BINANCE")
	account.UpdateBalanceLocked(btcID, money.MustNew("1000", currency.USDT()))
	account.UpdateBalanceLocked(ethID, money.MustNew("500", currency.USDT()))
	account.ClearBalanceLocked(btcID)
	if account.LockCount() != 1 {
		t.Fatalf("lock count = %d", account.LockCount())
	}
	got, ok := account.InstrumentLock(ethID, currency.USDT())
	if !ok || !got.Equal(money.MustNew("500", currency.USDT())) {
		t.Fatalf("remaining lock = %s, ok=%v", got, ok)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/cash.rs:907
//	test: test_recalculate_balance_clamps_when_locked_exceeds_total
func TestRecalculateBalanceClampsWhenLockedExceedsTotal(t *testing.T) {
	account := cashMulti()
	account.UpdateBalanceLocked(btcUSDT().ID, money.MustNew("15", currency.BTC()))
	requireBalanceValue(t, account, currency.BTC(), "10", "10", "0")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/cash.rs:925
//	test: test_recalculate_balance_sums_multiple_instrument_locks
func TestRecalculateBalanceSumsMultipleInstrumentLocks(t *testing.T) {
	account := cashMulti()
	account.UpdateBalanceLocked(ids.MustInstrumentID("BTCUSDT.BINANCE"), money.MustNew("3", currency.BTC()))
	account.UpdateBalanceLocked(ids.MustInstrumentID("BTCETH.BINANCE"), money.MustNew("2", currency.BTC()))
	requireBalanceValue(t, account, currency.BTC(), "10", "5", "5")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/cash.rs:941
//	test: test_recalculate_balance_no_clamp_when_total_negative_borrowing
func TestRecalculateBalanceNoClampWhenTotalNegativeBorrowing(t *testing.T) {
	usd := currency.USD()
	state := AccountState{
		AccountID:    ids.MustAccountID("SIM-001"),
		Balances:     []AccountBalance{balance("-1000", "0", "-1000", usd)},
		Reported:     true,
		BaseCurrency: &usd,
	}
	account := NewCashAccount(state, false, true)
	account.UpdateBalanceLocked(ids.MustInstrumentID("EURUSD.SIM"), money.MustNew("500", usd))
	requireBalanceValue(t, account, usd, "-1000", "500", "-1500")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/cash.rs:972
//	test: test_apply_returns_error_when_negative_balance_and_borrowing_disabled
func TestApplyReturnsErrorWhenNegativeBalanceAndBorrowingDisabled(t *testing.T) {
	usd := currency.USD()
	account := NewCashAccount(AccountState{
		AccountID:    ids.MustAccountID("SIM-001"),
		Balances:     []AccountBalance{balance("1000", "0", "1000", usd)},
		Reported:     true,
		BaseCurrency: &usd,
	}, false, false)
	err := account.Apply(AccountState{
		AccountID:    ids.MustAccountID("SIM-001"),
		Balances:     []AccountBalance{balance("-500", "0", "-500", usd)},
		Reported:     true,
		BaseCurrency: &usd,
		Sequence:     1,
	})
	if err == nil || !strings.Contains(err.Error(), "negative") ||
		!strings.Contains(err.Error(), "borrowing not allowed") {
		t.Fatalf("error = %v", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/cash.rs:1016
//	test: test_apply_succeeds_when_negative_balance_and_borrowing_enabled
func TestApplySucceedsWhenNegativeBalanceAndBorrowingEnabled(t *testing.T) {
	usd := currency.USD()
	account := NewCashAccount(AccountState{
		AccountID:    ids.MustAccountID("SIM-001"),
		Balances:     []AccountBalance{balance("1000", "0", "1000", usd)},
		Reported:     true,
		BaseCurrency: &usd,
	}, false, true)
	err := account.Apply(AccountState{
		AccountID:    ids.MustAccountID("SIM-001"),
		Balances:     []AccountBalance{balance("-500", "0", "-500", usd)},
		Reported:     true,
		BaseCurrency: &usd,
		Sequence:     1,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, ok := account.BalanceTotal(&usd)
	if !ok {
		t.Fatal("USD balance missing")
	}
	requireMoneyEqual(t, got, "-500", usd)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/cash.rs:1061
//	test: test_apply_clears_per_instrument_locks
func TestApplyClearsPerInstrumentLocks(t *testing.T) {
	usd := currency.USD()
	account := NewCashAccount(AccountState{
		AccountID:    ids.MustAccountID("SIM-001"),
		Balances:     []AccountBalance{balance("10000", "0", "10000", usd)},
		Reported:     true,
		BaseCurrency: &usd,
	}, false, false)
	account.UpdateBalanceLocked(ids.MustInstrumentID("AAPL.NASDAQ"), money.MustNew("5000", usd))
	if account.LockCount() != 1 {
		t.Fatalf("lock count = %d", account.LockCount())
	}
	err := account.Apply(AccountState{
		AccountID:    ids.MustAccountID("SIM-001"),
		Balances:     []AccountBalance{balance("8000", "0", "8000", usd)},
		Reported:     true,
		BaseCurrency: &usd,
		Sequence:     1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if account.LockCount() != 0 {
		t.Fatalf("lock count after apply = %d", account.LockCount())
	}
	got, ok := account.BalanceTotal(&usd)
	if !ok {
		t.Fatal("USD balance missing")
	}
	requireMoneyEqual(t, got, "8000", usd)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/cash.rs:1112
//	test: test_apply_empty_balances_preserves_per_instrument_locks
func TestApplyEmptyBalancesPreservesPerInstrumentLocks(t *testing.T) {
	usd := currency.USD()
	account := NewCashAccount(AccountState{
		AccountID:    ids.MustAccountID("SIM-001"),
		Balances:     []AccountBalance{balance("10000", "0", "10000", usd)},
		Reported:     true,
		BaseCurrency: &usd,
	}, false, false)
	account.UpdateBalanceLocked(ids.MustInstrumentID("AAPL.NASDAQ"), money.MustNew("5000", usd))
	err := account.Apply(AccountState{
		AccountID:    ids.MustAccountID("SIM-001"),
		Reported:     true,
		BaseCurrency: &usd,
		Sequence:     1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if account.LockCount() != 1 || account.EventCount() != 2 {
		t.Fatalf("locks/events = %d/%d", account.LockCount(), account.EventCount())
	}
	got, ok := account.BalanceTotal(&usd)
	if !ok {
		t.Fatal("USD balance missing")
	}
	requireMoneyEqual(t, got, "10000", usd)
}
