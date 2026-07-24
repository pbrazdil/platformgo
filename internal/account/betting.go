package account

import (
	"fmt"
	"math/big"

	"github.com/upcomers-org/platformgo/internal/currency"
	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
	"github.com/upcomers-org/platformgo/internal/money"
)

// BettingInstrument is the pricing contract required by a betting account.
type BettingInstrument struct {
	ID            ids.InstrumentID
	QuoteCurrency currency.Currency
	SportsBetting bool
	MakerFee      decimal.Decimal
	TakerFee      decimal.Decimal
}

// BettingPosition provides the open side and quantity needed to cap closing fills.
type BettingPosition struct {
	EntrySide OrderSide
	Quantity  decimal.Decimal
}

// BettingAccount applies sports-betting liability, balance, and PnL rules.
type BettingAccount struct {
	id                    ids.AccountID
	baseCurrency          *currency.Currency
	calculateAccountState bool
	balances              map[string]AccountBalance
	balanceOrder          []string
	instrumentLocks       map[lockKey]money.Money
	events                []AccountState
}

func NewBettingAccount(event AccountState, calculateAccountState bool) *BettingAccount {
	account := &BettingAccount{
		id:                    event.AccountID,
		baseCurrency:          copyCurrency(event.BaseCurrency),
		calculateAccountState: calculateAccountState,
		balances:              make(map[string]AccountBalance),
		instrumentLocks:       make(map[lockKey]money.Money),
	}
	account.replaceBalances(event.Balances)
	account.events = append(account.events, copyAccountState(event))
	return account
}

func (a *BettingAccount) ID() ids.AccountID            { return a.id }
func (a *BettingAccount) AccountType() string          { return "BETTING" }
func (a *BettingAccount) CalculatedAccountState() bool { return a.calculateAccountState }
func (a *BettingAccount) EventCount() int              { return len(a.events) }
func (a *BettingAccount) IsCashAccount() bool          { return true }
func (a *BettingAccount) IsMarginAccount() bool        { return false }
func (a *BettingAccount) IsUnleveraged() bool          { return true }

func (a *BettingAccount) String() string {
	base := "None"
	if a.baseCurrency != nil {
		base = a.baseCurrency.Code
	}
	return fmt.Sprintf("BettingAccount(id=%s, type=BETTING, base=%s)", a.id, base)
}

func (a *BettingAccount) BaseCurrency() (currency.Currency, bool) {
	if a.baseCurrency == nil {
		return currency.Currency{}, false
	}
	return *a.baseCurrency, true
}

func (a *BettingAccount) LastEvent() (AccountState, bool) {
	if len(a.events) == 0 {
		return AccountState{}, false
	}
	return copyAccountState(a.events[len(a.events)-1]), true
}

func (a *BettingAccount) Events() []AccountState {
	result := make([]AccountState, len(a.events))
	for index, event := range a.events {
		result[index] = copyAccountState(event)
	}
	return result
}

func (a *BettingAccount) Balance(denomination *currency.Currency) (AccountBalance, bool) {
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

func (a *BettingAccount) BalanceTotal(denomination *currency.Currency) (money.Money, bool) {
	value, ok := a.Balance(denomination)
	return value.Total, ok
}

func (a *BettingAccount) BalanceFree(denomination *currency.Currency) (money.Money, bool) {
	value, ok := a.Balance(denomination)
	return value.Free, ok
}

func (a *BettingAccount) BalanceLocked(denomination *currency.Currency) (money.Money, bool) {
	value, ok := a.Balance(denomination)
	return value.Locked, ok
}

func (a *BettingAccount) BalancesTotal() []BalanceAmount {
	result := make([]BalanceAmount, 0, len(a.balanceOrder))
	for _, code := range a.balanceOrder {
		balance := a.balances[code]
		result = append(result, BalanceAmount{Currency: balance.Currency, Amount: balance.Total})
	}
	return result
}

func (a *BettingAccount) Apply(event AccountState) error {
	for _, balance := range event.Balances {
		if balance.Total.Raw().Sign() < 0 {
			return fmt.Errorf(
				"Cannot apply betting account state: balance would be negative %s %s (%s)",
				balance.Total.Decimal(),
				balance.Currency.Code,
				a.id,
			)
		}
	}
	if event.Reported {
		clear(a.instrumentLocks)
	}
	a.replaceBalances(event.Balances)
	a.events = append(a.events, copyAccountState(event))
	return nil
}

// UpdateBalances validates the whole update before changing account state.
func (a *BettingAccount) UpdateBalances(balances []AccountBalance) error {
	for _, balance := range balances {
		if balance.Total.Raw().Sign() < 0 {
			return fmt.Errorf(
				"Betting account balance would become negative: %s %s (%s)",
				balance.Total.Decimal(),
				balance.Currency.Code,
				a.id,
			)
		}
	}
	a.replaceBalances(balances)
	return nil
}

func (a *BettingAccount) UpdateBalanceLocked(instrumentID ids.InstrumentID, locked money.Money) {
	if locked.Raw().Sign() < 0 {
		panic(fmt.Sprintf("locked balance was negative: %s", locked))
	}
	a.instrumentLocks[lockKey{instrument: instrumentID, currency: locked.Currency().Code}] = locked
	a.recalculateBalance(locked.Currency())
}

func (a *BettingAccount) ClearBalanceLocked(instrumentID ids.InstrumentID) {
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

func (a *BettingAccount) CalculateBalanceLocked(
	instrument BettingInstrument,
	side OrderSide,
	quantity, price decimal.Decimal,
	useQuoteForInverse bool,
) (money.Money, error) {
	if !instrument.SportsBetting {
		return money.Money{}, fmt.Errorf("BettingAccount requires a sports betting instrument")
	}
	if useQuoteForInverse {
		return money.Money{}, fmt.Errorf("`use_quote_for_inverse` is not applicable for betting accounts")
	}

	var locked decimal.Decimal
	switch side {
	case OrderSideSell:
		locked = quantity
	case OrderSideBuy:
		locked = quantity.Mul(price.Sub(decimal.MustParse("1")))
	default:
		return money.Money{}, fmt.Errorf("Invalid `OrderSide` in `calculate_balance_locked`")
	}
	return money.FromDecimal(locked, instrument.QuoteCurrency)
}

func (a *BettingAccount) BalanceImpact(
	instrument BettingInstrument,
	quantity, price decimal.Decimal,
	side OrderSide,
) (money.Money, error) {
	var impact decimal.Decimal
	switch side {
	case OrderSideSell:
		impact = quantity.Neg()
	case OrderSideBuy:
		impact = quantity.Mul(price.Sub(decimal.MustParse("1"))).Neg()
	default:
		return money.Money{}, fmt.Errorf("invalid `OrderSide`")
	}
	return money.FromDecimal(impact, instrument.QuoteCurrency)
}

func (a *BettingAccount) CalculatePnLs(
	instrument BettingInstrument,
	fillSide OrderSide,
	fillQuantity, fillPrice decimal.Decimal,
	position *BettingPosition,
) ([]money.Money, error) {
	if !instrument.SportsBetting {
		return nil, fmt.Errorf("BettingAccount requires a sports betting instrument")
	}
	quantity := fillQuantity
	if position != nil && !position.Quantity.IsZero() && position.EntrySide != fillSide &&
		position.Quantity.Cmp(quantity) < 0 {
		quantity = position.Quantity
	}
	quotePnL, err := money.FromDecimal(fillPrice.Mul(quantity), instrument.QuoteCurrency)
	if err != nil {
		return nil, err
	}
	switch fillSide {
	case OrderSideBuy:
		return []money.Money{quotePnL.Neg()}, nil
	case OrderSideSell:
		return []money.Money{quotePnL}, nil
	default:
		return nil, fmt.Errorf("Invalid `OrderSide` in calculate_pnls")
	}
}

func (a *BettingAccount) CalculateCommission(
	instrument BettingInstrument,
	quantity, price decimal.Decimal,
	liquidity LiquiditySide,
) (money.Money, error) {
	var fee decimal.Decimal
	switch liquidity {
	case LiquiditySideMaker:
		fee = instrument.MakerFee
	case LiquiditySideTaker:
		fee = instrument.TakerFee
	default:
		return money.Money{}, fmt.Errorf("Invalid `LiquiditySide`: NO_LIQUIDITY_SIDE")
	}
	return money.FromDecimal(quantity.Mul(price).Mul(fee), instrument.QuoteCurrency)
}

func (a *BettingAccount) recalculateBalance(denomination currency.Currency) {
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

func (a *BettingAccount) replaceBalances(balances []AccountBalance) {
	for _, balance := range balances {
		code := balance.Currency.Code
		if _, exists := a.balances[code]; !exists {
			a.balanceOrder = append(a.balanceOrder, code)
		}
		a.balances[code] = balance
	}
}
