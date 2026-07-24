package account

import (
	"fmt"
	"strings"

	"github.com/upcomers-org/platformgo/internal/currency"
	"github.com/upcomers-org/platformgo/internal/ids"
)

// AccountStateEvent is the complete identity and financial state of an account event.
type AccountStateEvent struct {
	AccountID    ids.AccountID
	AccountType  AnyAccountType
	BaseCurrency *currency.Currency
	Balances     []AccountBalance
	Margins      []MarginBalance
	Reported     bool
	EventID      string
	EventTime    uint64
	InitTime     uint64
}

func (s AccountStateEvent) Equal(other AccountStateEvent) bool {
	return s.AccountID == other.AccountID &&
		s.AccountType == other.AccountType &&
		s.EventID == other.EventID
}

// HasSameBalancesAndMargins compares financial state independent of event metadata.
func (s AccountStateEvent) HasSameBalancesAndMargins(other AccountStateEvent) bool {
	if len(s.Balances) != len(other.Balances) || len(s.Margins) != len(other.Margins) {
		return false
	}

	balances := make(map[string]AccountBalance, len(s.Balances))
	for _, balance := range s.Balances {
		balances[balance.Currency.Code] = balance
	}
	otherBalances := make(map[string]AccountBalance, len(other.Balances))
	for _, balance := range other.Balances {
		otherBalances[balance.Currency.Code] = balance
	}
	for code, balance := range balances {
		otherBalance, ok := otherBalances[code]
		if !ok || !balance.Equal(otherBalance) {
			return false
		}
	}

	type marginKey struct {
		instrument    ids.InstrumentID
		hasInstrument bool
		currency      string
	}
	key := func(margin MarginBalance) marginKey {
		result := marginKey{currency: margin.Currency.Code}
		if margin.InstrumentID != nil {
			result.instrument = *margin.InstrumentID
			result.hasInstrument = true
		}
		return result
	}
	margins := make(map[marginKey]MarginBalance, len(s.Margins))
	for _, margin := range s.Margins {
		margins[key(margin)] = margin
	}
	otherMargins := make(map[marginKey]MarginBalance, len(other.Margins))
	for _, margin := range other.Margins {
		otherMargins[key(margin)] = margin
	}
	for marginIdentity, margin := range margins {
		otherMargin, ok := otherMargins[marginIdentity]
		if !ok || !margin.Equal(otherMargin) {
			return false
		}
	}
	return true
}

func (s AccountStateEvent) String() string {
	base := "None"
	if s.BaseCurrency != nil {
		base = s.BaseCurrency.Code
	}
	balances := make([]string, len(s.Balances))
	for index, balance := range s.Balances {
		balances[index] = balance.String()
	}
	margins := make([]string, len(s.Margins))
	for index, margin := range s.Margins {
		margins[index] = margin.String()
	}
	return fmt.Sprintf(
		"AccountState(account_id=%s, account_type=%s, base_currency=%s, is_reported=%t, balances=[%s], margins=[%s], event_id=%s)",
		s.AccountID,
		s.AccountType,
		base,
		s.Reported,
		strings.Join(balances, ", "),
		strings.Join(margins, ", "),
		s.EventID,
	)
}

func (t AnyAccountType) String() string {
	switch t {
	case AnyAccountTypeCash:
		return "CASH"
	case AnyAccountTypeMargin:
		return "MARGIN"
	case AnyAccountTypeBetting:
		return "BETTING"
	case AnyAccountTypeWallet:
		return "WALLET"
	default:
		return fmt.Sprintf("AnyAccountType(%d)", t)
	}
}
