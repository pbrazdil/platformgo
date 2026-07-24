package account

import (
	"fmt"
	"math/big"

	"github.com/upcomers-org/platformgo/internal/currency"
	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
	"github.com/upcomers-org/platformgo/internal/money"
)

// AccountState is the balance-bearing portion of a cash-account event.
type AccountState struct {
	AccountID    ids.AccountID
	Balances     []AccountBalance
	Reported     bool
	BaseCurrency *currency.Currency
	Sequence     uint64
}

func (s AccountState) Equal(other AccountState) bool {
	if s.AccountID != other.AccountID || s.Reported != other.Reported ||
		s.Sequence != other.Sequence || !optionalCurrencyEqual(s.BaseCurrency, other.BaseCurrency) ||
		len(s.Balances) != len(other.Balances) {
		return false
	}
	for index := range s.Balances {
		if !s.Balances[index].Equal(other.Balances[index]) {
			return false
		}
	}
	return true
}

// BalanceAmount is an insertion-ordered currency/amount pair.
type BalanceAmount struct {
	Currency currency.Currency
	Amount   money.Money
}

// Instrument is the small pricing contract cash-account calculations need.
type Instrument struct {
	ID                 ids.InstrumentID
	BaseCurrency       *currency.Currency
	QuoteCurrency      currency.Currency
	SettlementCurrency currency.Currency
	Inverse            bool
	Multiplier         decimal.Decimal
	MakerFee           decimal.Decimal
	TakerFee           decimal.Decimal
}

type OrderSide uint8

const (
	OrderSideBuy OrderSide = iota + 1
	OrderSideSell
)

type LiquiditySide uint8

const (
	LiquiditySideMaker LiquiditySide = iota + 1
	LiquiditySideTaker
)

type lockKey struct {
	instrument ids.InstrumentID
	currency   string
}

// CashAccount is a deterministic cash-balance aggregate.
type CashAccount struct {
	id                    ids.AccountID
	baseCurrency          *currency.Currency
	calculateAccountState bool
	allowBorrowing        bool
	balances              map[string]AccountBalance
	balanceOrder          []string
	instrumentLocks       map[lockKey]money.Money
	events                []AccountState
}

func NewCashAccount(event AccountState, calculateAccountState, allowBorrowing bool) *CashAccount {
	account := &CashAccount{
		id:                    event.AccountID,
		baseCurrency:          copyCurrency(event.BaseCurrency),
		calculateAccountState: calculateAccountState,
		allowBorrowing:        allowBorrowing,
		balances:              make(map[string]AccountBalance),
		instrumentLocks:       make(map[lockKey]money.Money),
	}
	account.updateBalances(event.Balances)
	account.events = append(account.events, copyAccountState(event))
	return account
}

func (a *CashAccount) ID() ids.AccountID            { return a.id }
func (a *CashAccount) CalculatedAccountState() bool { return a.calculateAccountState }
func (a *CashAccount) EventCount() int              { return len(a.events) }
func (a *CashAccount) IsCashAccount() bool          { return true }
func (a *CashAccount) IsMarginAccount() bool        { return false }
func (a *CashAccount) IsUnleveraged() bool          { return true }

func (a *CashAccount) BaseCurrency() (currency.Currency, bool) {
	if a.baseCurrency == nil {
		return currency.Currency{}, false
	}
	return *a.baseCurrency, true
}

func (a *CashAccount) LastEvent() (AccountState, bool) {
	if len(a.events) == 0 {
		return AccountState{}, false
	}
	return copyAccountState(a.events[len(a.events)-1]), true
}

func (a *CashAccount) Events() []AccountState {
	result := make([]AccountState, len(a.events))
	for index, event := range a.events {
		result[index] = copyAccountState(event)
	}
	return result
}

func (a *CashAccount) Balance(denomination *currency.Currency) (AccountBalance, bool) {
	code := ""
	if denomination != nil {
		code = denomination.Code
	} else if a.baseCurrency != nil {
		code = a.baseCurrency.Code
	} else {
		panic("Currency must be specified")
	}
	value, ok := a.balances[code]
	return value, ok
}

func (a *CashAccount) BalanceTotal(denomination *currency.Currency) (money.Money, bool) {
	value, ok := a.Balance(denomination)
	return value.Total, ok
}

func (a *CashAccount) BalanceFree(denomination *currency.Currency) (money.Money, bool) {
	value, ok := a.Balance(denomination)
	return value.Free, ok
}

func (a *CashAccount) BalanceLocked(denomination *currency.Currency) (money.Money, bool) {
	value, ok := a.Balance(denomination)
	return value.Locked, ok
}

func (a *CashAccount) BalancesTotal() []BalanceAmount {
	return a.balanceAmounts(func(value AccountBalance) money.Money { return value.Total })
}

func (a *CashAccount) BalancesFree() []BalanceAmount {
	return a.balanceAmounts(func(value AccountBalance) money.Money { return value.Free })
}

func (a *CashAccount) BalancesLocked() []BalanceAmount {
	return a.balanceAmounts(func(value AccountBalance) money.Money { return value.Locked })
}

func (a *CashAccount) Currencies() []currency.Currency {
	result := make([]currency.Currency, 0, len(a.balanceOrder))
	for _, code := range a.balanceOrder {
		result = append(result, a.balances[code].Currency)
	}
	return result
}

func (a *CashAccount) String() string {
	base := "None"
	if a.baseCurrency != nil {
		base = a.baseCurrency.Code
	}
	return fmt.Sprintf("CashAccount(id=%s, type=CASH, base=%s)", a.id, base)
}

// Apply validates borrowing, optionally clears transient venue locks, updates
// reported balances, and records the event.
func (a *CashAccount) Apply(event AccountState) error {
	if !a.allowBorrowing {
		for _, balance := range event.Balances {
			if balance.Total.Raw().Sign() < 0 {
				return fmt.Errorf(
					"Cannot apply account state: balance would be negative %s %s (borrowing not allowed for %s)",
					balance.Total.Decimal(),
					balance.Currency.Code,
					a.id,
				)
			}
		}
	}
	if event.Reported && len(event.Balances) > 0 {
		clear(a.instrumentLocks)
	}
	a.updateBalances(event.Balances)
	a.events = append(a.events, copyAccountState(event))
	return nil
}

// UpdateBalanceLocked replaces one instrument/currency lock and recalculates
// that currency's aggregate balance.
func (a *CashAccount) UpdateBalanceLocked(instrumentID ids.InstrumentID, locked money.Money) {
	if locked.Raw().Sign() < 0 {
		panic(fmt.Sprintf("locked balance was negative: %s", locked))
	}
	a.instrumentLocks[lockKey{instrument: instrumentID, currency: locked.Currency().Code}] = locked
	a.recalculateBalance(locked.Currency())
}

// ClearBalanceLocked removes every currency lock belonging to instrumentID.
func (a *CashAccount) ClearBalanceLocked(instrumentID ids.InstrumentID) {
	currencies := make(map[string]currency.Currency)
	for key, locked := range a.instrumentLocks {
		if key.instrument == instrumentID {
			currencies[key.currency] = locked.Currency()
			delete(a.instrumentLocks, key)
		}
	}
	for _, denomination := range currencies {
		a.recalculateBalance(denomination)
	}
}

func (a *CashAccount) LockCount() int {
	return len(a.instrumentLocks)
}

func (a *CashAccount) InstrumentLock(
	instrumentID ids.InstrumentID,
	denomination currency.Currency,
) (money.Money, bool) {
	value, ok := a.instrumentLocks[lockKey{instrument: instrumentID, currency: denomination.Code}]
	return value, ok
}

// CalculateBalanceLocked returns the exact asset required to back an order.
func (a *CashAccount) CalculateBalanceLocked(
	instrument Instrument,
	side OrderSide,
	quantity, price decimal.Decimal,
	useQuoteForInverse bool,
) (money.Money, error) {
	base := instrument.QuoteCurrency
	if instrument.BaseCurrency != nil {
		base = *instrument.BaseCurrency
	}
	if side == OrderSideSell {
		return money.FromDecimal(quantity, base)
	}
	if side != OrderSideBuy {
		return money.Money{}, fmt.Errorf("invalid order side")
	}
	notional, err := instrument.notional(quantity, price, useQuoteForInverse)
	if err != nil {
		return money.Money{}, err
	}
	return notional, nil
}

// CalculatePnLs returns the balance effects of one fill.
func (a *CashAccount) CalculatePnLs(
	instrument Instrument,
	side OrderSide,
	quantity, price decimal.Decimal,
) ([]money.Money, error) {
	notional, err := instrument.notional(quantity, price, false)
	if err != nil {
		return nil, err
	}
	result := make([]money.Money, 0, 2)
	if instrument.BaseCurrency != nil && a.baseCurrency == nil {
		baseAmount, err := money.FromDecimal(quantity, *instrument.BaseCurrency)
		if err != nil {
			return nil, err
		}
		if side == OrderSideSell {
			baseAmount = baseAmount.Neg()
		}
		result = append(result, baseAmount)
	}
	switch side {
	case OrderSideBuy:
		result = append(result, notional.Neg())
	case OrderSideSell:
		result = append(result, notional)
	default:
		return nil, fmt.Errorf("invalid order side")
	}
	return result, nil
}

// CalculateCommission applies the instrument liquidity fee to its notional.
func (a *CashAccount) CalculateCommission(
	instrument Instrument,
	quantity, price decimal.Decimal,
	liquidity LiquiditySide,
	useQuoteForInverse bool,
) (money.Money, error) {
	notional, err := instrument.notional(quantity, price, useQuoteForInverse)
	if err != nil {
		return money.Money{}, err
	}
	var rate decimal.Decimal
	switch liquidity {
	case LiquiditySideMaker:
		rate = instrument.MakerFee
	case LiquiditySideTaker:
		rate = instrument.TakerFee
	default:
		return money.Money{}, fmt.Errorf("invalid liquidity side")
	}
	return money.FromDecimal(notional.Decimal().Mul(rate), notional.Currency())
}

func (a *CashAccount) recalculateBalance(denomination currency.Currency) {
	current, ok := a.balances[denomination.Code]
	if !ok {
		return
	}
	totalLocked := new(big.Int)
	for _, locked := range a.instrumentLocks {
		if locked.Currency().Equal(denomination) {
			totalLocked.Add(totalLocked, locked.Raw())
		}
	}
	totalRaw := current.Total.Raw()
	lockedRaw := new(big.Int).Set(totalLocked)
	freeRaw := new(big.Int).Sub(totalRaw, lockedRaw)
	if totalRaw.Sign() >= 0 && lockedRaw.Cmp(totalRaw) > 0 {
		lockedRaw.Set(totalRaw)
		freeRaw.SetInt64(0)
	}
	locked := money.MustFromRaw(lockedRaw, denomination)
	free := money.MustFromRaw(freeRaw, denomination)
	a.balances[denomination.Code] = MustAccountBalance(current.Total, locked, free)
}

func (a *CashAccount) updateBalances(balances []AccountBalance) {
	for _, balance := range balances {
		code := balance.Currency.Code
		if _, exists := a.balances[code]; !exists {
			a.balanceOrder = append(a.balanceOrder, code)
		}
		a.balances[code] = balance
	}
}

func (a *CashAccount) balanceAmounts(selectAmount func(AccountBalance) money.Money) []BalanceAmount {
	result := make([]BalanceAmount, 0, len(a.balanceOrder))
	for _, code := range a.balanceOrder {
		balance := a.balances[code]
		result = append(result, BalanceAmount{
			Currency: balance.Currency,
			Amount:   selectAmount(balance),
		})
	}
	return result
}

func (i Instrument) notional(
	quantity, price decimal.Decimal,
	useQuoteForInverse bool,
) (money.Money, error) {
	multiplier := i.Multiplier
	if multiplier.IsZero() {
		multiplier = decimal.MustParse("1")
	}
	quantity = quantity.Mul(multiplier)
	if i.Inverse && !useQuoteForInverse {
		if i.BaseCurrency == nil {
			return money.Money{}, fmt.Errorf("inverse instrument has no base currency")
		}
		value, err := quantity.Quo(price, i.BaseCurrency.Precision, decimal.RoundHalfEven)
		if err != nil {
			return money.Money{}, err
		}
		return money.FromDecimal(value, *i.BaseCurrency)
	}
	if i.Inverse && useQuoteForInverse {
		return money.FromDecimal(quantity, i.QuoteCurrency)
	}
	return money.FromDecimal(quantity.Mul(price), i.QuoteCurrency)
}

func optionalCurrencyEqual(left, right *currency.Currency) bool {
	switch {
	case left == nil && right == nil:
		return true
	case left == nil || right == nil:
		return false
	default:
		return left.Equal(*right)
	}
}

func copyCurrency(value *currency.Currency) *currency.Currency {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}

func copyAccountState(value AccountState) AccountState {
	result := value
	result.BaseCurrency = copyCurrency(value.BaseCurrency)
	result.Balances = append([]AccountBalance(nil), value.Balances...)
	return result
}
