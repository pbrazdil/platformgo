package account

import (
	"fmt"

	"github.com/upcomers-org/platformgo/internal/currency"
	"github.com/upcomers-org/platformgo/internal/ids"
)

// AnyAccountType identifies the concrete account represented by an account event.
type AnyAccountType uint8

const (
	AnyAccountTypeCash AnyAccountType = iota + 1
	AnyAccountTypeMargin
	AnyAccountTypeBetting
	AnyAccountTypeWallet
)

// AnyAccountState is the common event shape used to construct heterogeneous accounts.
type AnyAccountState struct {
	AccountID    ids.AccountID
	AccountType  AnyAccountType
	Balances     []AccountBalance
	Margins      []MarginBalance
	Reported     bool
	BaseCurrency *currency.Currency
	Sequence     uint64
}

// AccountAny is a type-erased view over the concrete account implementations.
type AccountAny struct {
	cash    *CashAccount
	margin  *MarginAccount
	betting *BettingAccount
}

func TryAccountAnyFromState(event AnyAccountState) (AccountAny, error) {
	switch event.AccountType {
	case AnyAccountTypeCash:
		return AccountAny{cash: NewCashAccount(event.cashState(), false, false)}, nil
	case AnyAccountTypeMargin:
		return AccountAny{margin: NewMarginAccount(event.marginState(), false)}, nil
	case AnyAccountTypeBetting:
		return AccountAny{betting: NewBettingAccount(event.cashState(), false)}, nil
	case AnyAccountTypeWallet:
		return AccountAny{}, fmt.Errorf("Wallet accounts are not yet implemented in Rust")
	default:
		return AccountAny{}, fmt.Errorf("unsupported account type %d", event.AccountType)
	}
}

func AccountAnyFromEvents(events []AnyAccountState) (AccountAny, error) {
	if len(events) == 0 {
		return AccountAny{}, fmt.Errorf("No order events provided to create `AccountAny`")
	}
	account, err := TryAccountAnyFromState(events[0])
	if err != nil {
		return AccountAny{}, err
	}
	for _, event := range events[1:] {
		if err := account.Apply(event); err != nil {
			return AccountAny{}, err
		}
	}
	return account, nil
}

func (a AccountAny) IsCash() bool    { return a.cash != nil }
func (a AccountAny) IsMargin() bool  { return a.margin != nil }
func (a AccountAny) IsBetting() bool { return a.betting != nil }

func (a *AccountAny) Apply(event AnyAccountState) error {
	switch {
	case a.cash != nil:
		return a.cash.Apply(event.cashState())
	case a.margin != nil:
		return a.margin.Apply(event.marginState())
	case a.betting != nil:
		return a.betting.Apply(event.cashState())
	default:
		return fmt.Errorf("account variant is not configured")
	}
}

func (s AnyAccountState) cashState() AccountState {
	return AccountState{
		AccountID:    s.AccountID,
		Balances:     append([]AccountBalance(nil), s.Balances...),
		Reported:     s.Reported,
		BaseCurrency: copyCurrency(s.BaseCurrency),
		Sequence:     s.Sequence,
	}
}

func (s AnyAccountState) marginState() MarginAccountState {
	return MarginAccountState{
		AccountID:    s.AccountID,
		Balances:     append([]AccountBalance(nil), s.Balances...),
		Margins:      append([]MarginBalance(nil), s.Margins...),
		Reported:     s.Reported,
		BaseCurrency: copyCurrency(s.BaseCurrency),
		Sequence:     s.Sequence,
	}
}
