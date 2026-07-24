package nautilus_accounts

import (
	"errors"
	"fmt"

	"github.com/upcomers-org/platformgo/internal/decimal"
)

type AccountStatus string

const (
	Pending AccountStatus = "pending"
	Active  AccountStatus = "active"
)

type Saga struct{ Status, LedgerStatus, LastError string }
type Account struct {
	ID, Login, Currency string
	Status              AccountStatus
	Total, Equity       decimal.Decimal
	OpenPositions       int
}
type Fixture struct {
	Accounts      map[string]*Account
	Ops           map[string]bool
	Sagas         map[string]Saga
	SeededRates   map[string]bool
	RuntimeBooted bool
}

func NewFixture() *Fixture {
	return &Fixture{Accounts: map[string]*Account{}, Ops: map[string]bool{}, Sagas: map[string]Saga{}, SeededRates: map[string]bool{"USDC": true, "USD": true}}
}
func (f *Fixture) Boot() error {
	for _, a := range f.Accounts {
		if !f.SeededRates[a.Currency] {
			return fmt.Errorf("account currency %s has no seeded rate to venue that settles in USDC", a.Currency)
		}
	}
	f.RuntimeBooted = true
	for _, a := range f.Accounts {
		f.Provision(a.ID)
	}
	return nil
}
func (f *Fixture) CreateAccount(id, login, currency string) (*Account, error) {
	if currency == "" {
		currency = "USDC"
	}
	if !f.SeededRates[currency] {
		return nil, fmt.Errorf("account currency %s incompatible with venue that settles in USDC", currency)
	}
	a := &Account{ID: id, Login: login, Currency: currency, Status: Pending}
	f.Accounts[id] = a
	if f.RuntimeBooted {
		f.Provision(id)
	}
	return a, nil
}
func (f *Fixture) Provision(id string) {
	a := f.Accounts[id]
	if a == nil {
		return
	}
	a.Status = Active
	f.Sagas["provision:"+id] = Saga{Status: "completed"}
}
func (f *Fixture) Adjust(opID, accountID, kind, amount string) error {
	a := f.Accounts[accountID]
	if a == nil {
		return errors.New("account absent")
	}
	if f.Ops[opID] {
		return nil
	}
	value, err := decimal.Parse(amount)
	if err != nil {
		return err
	}
	if kind == "withdraw" && a.Total.Cmp(value) < 0 {
		f.Ops[opID] = true
		f.Sagas[opID] = Saga{Status: "compensated", LedgerStatus: "reversed", LastError: "withdraw exceeds free balance"}
		return nil
	}
	if kind == "withdraw" {
		a.Total = a.Total.Sub(value)
	} else {
		a.Total = a.Total.Add(value)
	}
	a.Equity = a.Total
	f.Ops[opID] = true
	f.Sagas[opID] = Saga{Status: "completed", LedgerStatus: "settled"}
	return nil
}
func (f *Fixture) Trade(accountID, quantity string) error {
	a := f.Accounts[accountID]
	if a == nil || a.Status != Active {
		return errors.New("account not active")
	}
	if a.Total.Sign() <= 0 {
		return errors.New("unfunded account")
	}
	if _, err := decimal.Parse(quantity); err != nil {
		return err
	}
	a.OpenPositions++
	a.Equity = a.Total
	return nil
}
func (f *Fixture) BalanceRows(accountID string) map[string]decimal.Decimal {
	a := f.Accounts[accountID]
	if a == nil {
		return nil
	}
	return map[string]decimal.Decimal{a.Currency: a.Total}
}
