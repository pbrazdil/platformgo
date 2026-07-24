// Package account contains small account-domain value types.
package account

import (
	"fmt"
	"math/big"

	"github.com/upcomers-org/platformgo/internal/currency"
	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
	"github.com/upcomers-org/platformgo/internal/money"
)

// BalanceError reports an invariant violation while constructing a balance.
type BalanceError struct {
	Message string
}

func (e BalanceError) Error() string {
	return e.Message
}

// AccountBalance partitions a total balance into locked and free amounts.
type AccountBalance struct {
	Currency currency.Currency
	Total    money.Money
	Locked   money.Money
	Free     money.Money
}

// NewAccountBalance validates currency consistency and total == locked + free.
func NewAccountBalance(total, locked, free money.Money) (AccountBalance, error) {
	if !total.Currency().Equal(locked.Currency()) {
		return AccountBalance{}, BalanceError{Message: fmt.Sprintf(
			"`total` currency (%s) != `locked` currency (%s)",
			total.Currency(),
			locked.Currency(),
		)}
	}
	if !total.Currency().Equal(free.Currency()) {
		return AccountBalance{}, BalanceError{Message: fmt.Sprintf(
			"`total` currency (%s) != `free` currency (%s)",
			total.Currency(),
			free.Currency(),
		)}
	}
	sum, ok := locked.CheckedAdd(free)
	if !ok || !sum.Equal(total) {
		return AccountBalance{}, BalanceError{Message: fmt.Sprintf(
			"`total` (%s) - `locked` (%s) != `free` (%s)",
			total,
			locked,
			free,
		)}
	}
	return AccountBalance{
		Currency: total.Currency(),
		Total:    total,
		Locked:   locked,
		Free:     free,
	}, nil
}

// MustAccountBalance is NewAccountBalance for invariant-safe composition.
func MustAccountBalance(total, locked, free money.Money) AccountBalance {
	value, err := NewAccountBalance(total, locked, free)
	if err != nil {
		panic(err)
	}
	return value
}

// AccountBalanceFromTotalAndLocked derives free after rounding inputs to the
// denomination precision. For non-negative totals, locked is clamped to
// [0,total]; negative totals preserve venue-reported locked amounts.
func AccountBalanceFromTotalAndLocked(
	totalValue, lockedValue decimal.Decimal,
	denomination currency.Currency,
) (AccountBalance, error) {
	total, err := money.FromDecimal(totalValue, denomination)
	if err != nil {
		return AccountBalance{}, err
	}
	locked, err := money.FromDecimal(lockedValue, denomination)
	if err != nil {
		return AccountBalance{}, err
	}

	lockedRaw := locked.Raw()
	if total.Raw().Sign() >= 0 {
		lockedRaw = clamp(lockedRaw, new(big.Int), total.Raw())
	}
	locked, err = money.FromRawChecked(lockedRaw, denomination)
	if err != nil {
		return AccountBalance{}, err
	}
	freeRaw := new(big.Int).Sub(total.Raw(), locked.Raw())
	free, err := money.FromRawChecked(freeRaw, denomination)
	if err != nil {
		return AccountBalance{}, BalanceError{Message: fmt.Sprintf(
			"Derived `free` exceeds Money bounds for `total` %s and `locked` %s: %v",
			total,
			locked,
			err,
		)}
	}
	return NewAccountBalance(total, locked, free)
}

// AccountBalanceFromTotalAndFree derives locked after rounding inputs to the
// denomination precision. For non-negative totals, free is clamped to
// [0,total]; negative totals preserve venue-reported free amounts.
func AccountBalanceFromTotalAndFree(
	totalValue, freeValue decimal.Decimal,
	denomination currency.Currency,
) (AccountBalance, error) {
	total, err := money.FromDecimal(totalValue, denomination)
	if err != nil {
		return AccountBalance{}, err
	}
	free, err := money.FromDecimal(freeValue, denomination)
	if err != nil {
		return AccountBalance{}, err
	}

	freeRaw := free.Raw()
	if total.Raw().Sign() >= 0 {
		freeRaw = clamp(freeRaw, new(big.Int), total.Raw())
	}
	free, err = money.FromRawChecked(freeRaw, denomination)
	if err != nil {
		return AccountBalance{}, err
	}
	lockedRaw := new(big.Int).Sub(total.Raw(), free.Raw())
	locked, err := money.FromRawChecked(lockedRaw, denomination)
	if err != nil {
		return AccountBalance{}, BalanceError{Message: fmt.Sprintf(
			"Derived `locked` exceeds Money bounds for `total` %s and `free` %s: %v",
			total,
			free,
			err,
		)}
	}
	return NewAccountBalance(total, locked, free)
}

func (b AccountBalance) Equal(other AccountBalance) bool {
	return b.Total.Equal(other.Total) &&
		b.Locked.Equal(other.Locked) &&
		b.Free.Equal(other.Free)
}

func (b AccountBalance) String() string {
	return fmt.Sprintf(
		"AccountBalance(total=%s, locked=%s, free=%s)",
		b.Total,
		b.Locked,
		b.Free,
	)
}

func (b AccountBalance) DebugString() string {
	return b.String()
}

// MarginBalance represents either instrument-scoped or account-wide margin.
type MarginBalance struct {
	Initial      money.Money
	Maintenance  money.Money
	Currency     currency.Currency
	InstrumentID *ids.InstrumentID
}

func NewMarginBalance(
	initial, maintenance money.Money,
	instrumentID *ids.InstrumentID,
) (MarginBalance, error) {
	if !initial.Currency().Equal(maintenance.Currency()) {
		return MarginBalance{}, BalanceError{Message: fmt.Sprintf(
			"`initial` currency (%s) != `maintenance` currency (%s)",
			initial.Currency(),
			maintenance.Currency(),
		)}
	}
	return MarginBalance{
		Initial:      initial,
		Maintenance:  maintenance,
		Currency:     initial.Currency(),
		InstrumentID: copyInstrumentID(instrumentID),
	}, nil
}

func MustMarginBalance(
	initial, maintenance money.Money,
	instrumentID *ids.InstrumentID,
) MarginBalance {
	value, err := NewMarginBalance(initial, maintenance, instrumentID)
	if err != nil {
		panic(err)
	}
	return value
}

func (b MarginBalance) Equal(other MarginBalance) bool {
	if !b.Initial.Equal(other.Initial) || !b.Maintenance.Equal(other.Maintenance) {
		return false
	}
	switch {
	case b.InstrumentID == nil && other.InstrumentID == nil:
		return true
	case b.InstrumentID == nil || other.InstrumentID == nil:
		return false
	default:
		return *b.InstrumentID == *other.InstrumentID
	}
}

func (b MarginBalance) String() string {
	if b.InstrumentID != nil {
		return fmt.Sprintf(
			"MarginBalance(initial=%s, maintenance=%s, instrument_id=%s)",
			b.Initial,
			b.Maintenance,
			b.InstrumentID,
		)
	}
	return fmt.Sprintf(
		"MarginBalance(initial=%s, maintenance=%s, currency=%s)",
		b.Initial,
		b.Maintenance,
		b.Currency,
	)
}

func (b MarginBalance) DebugString() string {
	return b.String()
}

func clamp(value, minimum, maximum *big.Int) *big.Int {
	switch {
	case value.Cmp(minimum) < 0:
		return new(big.Int).Set(minimum)
	case value.Cmp(maximum) > 0:
		return new(big.Int).Set(maximum)
	default:
		return new(big.Int).Set(value)
	}
}

func copyInstrumentID(value *ids.InstrumentID) *ids.InstrumentID {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}
