package account

import (
	"fmt"

	"github.com/upcomers-org/platformgo/internal/currency"
	"github.com/upcomers-org/platformgo/internal/ids"
	"github.com/upcomers-org/platformgo/internal/money"
)

// BaseAccountState contains the common account event fields used by BaseAccount.
type BaseAccountState struct {
	AccountID    ids.AccountID
	AccountType  AnyAccountType
	Balances     []AccountBalance
	Reported     bool
	BaseCurrency *currency.Currency
	EventTime    uint64
	InitTime     uint64
}

// BaseAccount owns common event, balance, and commission state.
type BaseAccount struct {
	id                    ids.AccountID
	accountType           AnyAccountType
	baseCurrency          *currency.Currency
	calculateAccountState bool
	events                []BaseAccountState
	commissions           map[string]money.Money
	balances              map[string]AccountBalance
	balanceOrder          []string
	startingBalances      map[string]money.Money
}

func NewBaseAccount(event BaseAccountState, calculateAccountState bool) *BaseAccount {
	account := &BaseAccount{
		id:                    event.AccountID,
		accountType:           event.AccountType,
		baseCurrency:          copyCurrency(event.BaseCurrency),
		calculateAccountState: calculateAccountState,
		commissions:           make(map[string]money.Money),
		balances:              make(map[string]AccountBalance),
		startingBalances:      make(map[string]money.Money),
	}
	for _, balance := range event.Balances {
		account.balanceOrder = append(account.balanceOrder, balance.Currency.Code)
		account.balances[balance.Currency.Code] = balance
		account.startingBalances[balance.Currency.Code] = balance.Total
	}
	account.events = append(account.events, copyBaseAccountState(event))
	return account
}

func (a *BaseAccount) EventCount() int {
	return len(a.events)
}

func (a *BaseAccount) Events() []BaseAccountState {
	result := make([]BaseAccountState, len(a.events))
	for index, event := range a.events {
		result[index] = copyBaseAccountState(event)
	}
	return result
}

func (a *BaseAccount) LastEvent() (BaseAccountState, bool) {
	if len(a.events) == 0 {
		return BaseAccountState{}, false
	}
	return copyBaseAccountState(a.events[len(a.events)-1]), true
}

func (a *BaseAccount) Apply(event BaseAccountState) {
	if event.AccountID != a.id {
		panic(fmt.Sprintf("event.account_id %s != self.id %s", event.AccountID, a.id))
	}
	for _, balance := range event.Balances {
		code := balance.Currency.Code
		if _, exists := a.balances[code]; !exists {
			a.balanceOrder = append(a.balanceOrder, code)
		}
		a.balances[code] = balance
	}
	a.events = append(a.events, copyBaseAccountState(event))
}

// PurgeAccountEvents removes events outside the lookback while always retaining
// the latest event when the account has event history.
func (a *BaseAccount) PurgeAccountEvents(nowNanos, lookbackSeconds uint64) {
	lookbackNanos := lookbackSeconds * 1_000_000_000
	retained := make([]BaseAccountState, 0, len(a.events))
	for _, event := range a.events {
		if event.EventTime+lookbackNanos > nowNanos {
			retained = append(retained, event)
		}
	}
	if len(retained) == 0 && len(a.events) > 0 {
		retained = append(retained, a.events[len(a.events)-1])
	}
	a.events = retained
}

func (a *BaseAccount) UpdateCommissions(commission money.Money) {
	if err := a.TryUpdateCommissions(commission); err != nil {
		panic("commission total exceeded Money bounds")
	}
}

// TryUpdateCommissions normalizes to currency precision before accumulating.
func (a *BaseAccount) TryUpdateCommissions(commission money.Money) error {
	normalized, err := money.FromDecimal(commission.Decimal(), commission.Currency())
	if err != nil {
		return err
	}
	if normalized.IsZero() {
		return nil
	}
	code := normalized.Currency().Code
	total := normalized
	if current, ok := a.commissions[code]; ok {
		var fits bool
		total, fits = current.CheckedAdd(normalized)
		if !fits {
			return fmt.Errorf("%s commission total exceeds Money bounds", code)
		}
	}
	a.commissions[code] = total
	return nil
}

func (a *BaseAccount) Commission(denomination currency.Currency) (money.Money, bool) {
	value, ok := a.commissions[denomination.Code]
	return value, ok
}

func copyBaseAccountState(value BaseAccountState) BaseAccountState {
	result := value
	result.BaseCurrency = copyCurrency(value.BaseCurrency)
	result.Balances = append([]AccountBalance(nil), value.Balances...)
	return result
}
