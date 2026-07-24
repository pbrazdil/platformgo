package account

import (
	"fmt"
	"math/big"

	"github.com/upcomers-org/platformgo/internal/currency"
	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
	"github.com/upcomers-org/platformgo/internal/money"
)

// MarginAccountState is the balance and margin portion of a margin-account event.
type MarginAccountState struct {
	AccountID    ids.AccountID
	Balances     []AccountBalance
	Margins      []MarginBalance
	Reported     bool
	BaseCurrency *currency.Currency
	Sequence     uint64
}

// MarginInstrument is the exact pricing and margin contract used by the
// account's default leveraged margin model.
type MarginInstrument struct {
	ID                ids.InstrumentID
	BaseCurrency      *currency.Currency
	QuoteCurrency     currency.Currency
	Inverse           bool
	Multiplier        decimal.Decimal
	InitialMarginRate decimal.Decimal
	MaintenanceRate   decimal.Decimal
	Premium           bool
}

// MarginPosition contains the observable position state required for realized
// PnL calculation.
type MarginPosition struct {
	EntrySide OrderSide
	Quantity  decimal.Decimal
	AvgPrice  decimal.Decimal
}

// MarginAccount tracks exact per-instrument and account-wide margin.
type MarginAccount struct {
	id                    ids.AccountID
	baseCurrency          *currency.Currency
	calculateAccountState bool
	balances              map[string]AccountBalance
	balanceOrder          []string
	events                []MarginAccountState
	leverages             map[ids.InstrumentID]decimal.Decimal
	defaultLeverage       decimal.Decimal
	margins               map[ids.InstrumentID]MarginBalance
	accountMargins        map[string]MarginBalance
}

func NewMarginAccount(event MarginAccountState, calculateAccountState bool) *MarginAccount {
	account := &MarginAccount{
		id:                    event.AccountID,
		baseCurrency:          copyCurrency(event.BaseCurrency),
		calculateAccountState: calculateAccountState,
		balances:              make(map[string]AccountBalance),
		leverages:             make(map[ids.InstrumentID]decimal.Decimal),
		defaultLeverage:       decimal.MustParse("1"),
		margins:               make(map[ids.InstrumentID]MarginBalance),
		accountMargins:        make(map[string]MarginBalance),
	}
	account.updateBalances(event.Balances)
	account.routeMargins(event.Margins)
	account.events = append(account.events, copyMarginAccountState(event))
	return account
}

func (a *MarginAccount) ID() ids.AccountID            { return a.id }
func (a *MarginAccount) CalculatedAccountState() bool { return a.calculateAccountState }
func (a *MarginAccount) EventCount() int              { return len(a.events) }
func (a *MarginAccount) IsCashAccount() bool          { return false }
func (a *MarginAccount) IsMarginAccount() bool        { return true }
func (a *MarginAccount) LeverageCount() int           { return len(a.leverages) }
func (a *MarginAccount) MarginCount() int             { return len(a.margins) }
func (a *MarginAccount) AccountMarginCount() int      { return len(a.accountMargins) }

func (a *MarginAccount) String() string {
	base := "None"
	if a.baseCurrency != nil {
		base = a.baseCurrency.Code
	}
	return fmt.Sprintf("MarginAccount(id=%s, type=MARGIN, base=%s)", a.id, base)
}

func (a *MarginAccount) SetDefaultLeverage(leverage decimal.Decimal) {
	requirePositiveLeverage(leverage)
	a.defaultLeverage = leverage
}

func (a *MarginAccount) DefaultLeverage() decimal.Decimal {
	return a.defaultLeverage
}

func (a *MarginAccount) SetLeverage(instrumentID ids.InstrumentID, leverage decimal.Decimal) {
	requirePositiveLeverage(leverage)
	a.leverages[instrumentID] = leverage
}

func (a *MarginAccount) Leverage(instrumentID ids.InstrumentID) decimal.Decimal {
	if value, ok := a.leverages[instrumentID]; ok {
		return value
	}
	return a.defaultLeverage
}

func (a *MarginAccount) IsUnleveraged(instrumentID ids.InstrumentID) bool {
	return a.Leverage(instrumentID).Equal(decimal.MustParse("1"))
}

func (a *MarginAccount) BaseCurrency() (currency.Currency, bool) {
	if a.baseCurrency == nil {
		return currency.Currency{}, false
	}
	return *a.baseCurrency, true
}

func (a *MarginAccount) LastEvent() (MarginAccountState, bool) {
	if len(a.events) == 0 {
		return MarginAccountState{}, false
	}
	return copyMarginAccountState(a.events[len(a.events)-1]), true
}

func (a *MarginAccount) Events() []MarginAccountState {
	result := make([]MarginAccountState, len(a.events))
	for index, event := range a.events {
		result[index] = copyMarginAccountState(event)
	}
	return result
}

func (a *MarginAccount) Balance(denomination *currency.Currency) (AccountBalance, bool) {
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

func (a *MarginAccount) BalanceTotal(denomination *currency.Currency) (money.Money, bool) {
	value, ok := a.Balance(denomination)
	return value.Total, ok
}

func (a *MarginAccount) BalanceFree(denomination *currency.Currency) (money.Money, bool) {
	value, ok := a.Balance(denomination)
	return value.Free, ok
}

func (a *MarginAccount) BalanceLocked(denomination *currency.Currency) (money.Money, bool) {
	value, ok := a.Balance(denomination)
	return value.Locked, ok
}

func (a *MarginAccount) BalancesTotal() []BalanceAmount {
	return a.balanceAmounts(func(value AccountBalance) money.Money { return value.Total })
}

func (a *MarginAccount) BalancesFree() []BalanceAmount {
	return a.balanceAmounts(func(value AccountBalance) money.Money { return value.Free })
}

func (a *MarginAccount) BalancesLocked() []BalanceAmount {
	return a.balanceAmounts(func(value AccountBalance) money.Money { return value.Locked })
}

func (a *MarginAccount) Apply(event MarginAccountState) error {
	if event.AccountID != a.id {
		return fmt.Errorf("event account ID %s does not match %s", event.AccountID, a.id)
	}
	skipMarginRouting := len(event.Balances) == 0 && len(event.Margins) == 0
	a.updateBalances(event.Balances)
	if !skipMarginRouting {
		clear(a.margins)
		clear(a.accountMargins)
		a.routeMargins(event.Margins)
	}
	a.events = append(a.events, copyMarginAccountState(event))
	return nil
}

func (a *MarginAccount) UpdateInitialMargin(instrumentID ids.InstrumentID, initial money.Money) {
	current, ok := a.margins[instrumentID]
	if ok {
		current.Initial = initial
		a.margins[instrumentID] = current
	} else {
		id := instrumentID
		a.margins[instrumentID] = MustMarginBalance(initial, money.Zero(initial.Currency()), &id)
	}
	a.recalculateBalance(initial.Currency())
}

func (a *MarginAccount) ClearInitialMargin(instrumentID ids.InstrumentID) {
	current, ok := a.margins[instrumentID]
	if !ok {
		return
	}
	if current.Maintenance.IsZero() {
		delete(a.margins, instrumentID)
	} else {
		current.Initial = money.Zero(current.Currency)
		a.margins[instrumentID] = current
	}
	a.recalculateBalance(current.Currency)
}

func (a *MarginAccount) InitialMargin(instrumentID ids.InstrumentID) money.Money {
	value, ok := a.margins[instrumentID]
	if !ok {
		panic("Cannot get margin_init when no margin_balance")
	}
	return value.Initial
}

func (a *MarginAccount) UpdateMaintenanceMargin(instrumentID ids.InstrumentID, maintenance money.Money) {
	current, ok := a.margins[instrumentID]
	if ok {
		current.Maintenance = maintenance
		a.margins[instrumentID] = current
	} else {
		id := instrumentID
		a.margins[instrumentID] = MustMarginBalance(money.Zero(maintenance.Currency()), maintenance, &id)
	}
	a.recalculateBalance(maintenance.Currency())
}

func (a *MarginAccount) ClearMaintenanceMargin(instrumentID ids.InstrumentID) {
	current, ok := a.margins[instrumentID]
	if !ok {
		return
	}
	if current.Initial.IsZero() {
		delete(a.margins, instrumentID)
	} else {
		current.Maintenance = money.Zero(current.Currency)
		a.margins[instrumentID] = current
	}
	a.recalculateBalance(current.Currency)
}

func (a *MarginAccount) MaintenanceMargin(instrumentID ids.InstrumentID) money.Money {
	value, ok := a.margins[instrumentID]
	if !ok {
		panic("Cannot get maintenance_margin when no margin_balance")
	}
	return value.Maintenance
}

func (a *MarginAccount) Margin(instrumentID ids.InstrumentID) (MarginBalance, bool) {
	value, ok := a.margins[instrumentID]
	return value, ok
}

func (a *MarginAccount) AccountMargin(denomination currency.Currency) (MarginBalance, bool) {
	value, ok := a.accountMargins[denomination.Code]
	return value, ok
}

func (a *MarginAccount) AccountInitialMargin(denomination currency.Currency) (money.Money, bool) {
	value, ok := a.AccountMargin(denomination)
	return value.Initial, ok
}

func (a *MarginAccount) AccountMaintenanceMargin(denomination currency.Currency) (money.Money, bool) {
	value, ok := a.AccountMargin(denomination)
	return value.Maintenance, ok
}

func (a *MarginAccount) UpdateMargin(value MarginBalance) {
	if value.InstrumentID == nil {
		a.accountMargins[value.Currency.Code] = value
	} else {
		a.margins[*value.InstrumentID] = value
	}
	a.recalculateBalance(value.Currency)
}

func (a *MarginAccount) ClearMargin(instrumentID ids.InstrumentID) {
	value, ok := a.margins[instrumentID]
	if !ok {
		return
	}
	delete(a.margins, instrumentID)
	a.recalculateBalance(value.Currency)
}

func (a *MarginAccount) ClearAccountMargin(denomination currency.Currency) {
	if _, ok := a.accountMargins[denomination.Code]; !ok {
		return
	}
	delete(a.accountMargins, denomination.Code)
	a.recalculateBalance(denomination)
}

func (a *MarginAccount) TotalInitialMargin(denomination currency.Currency) money.Money {
	raw := new(big.Int)
	for _, value := range a.margins {
		if value.Currency.Equal(denomination) {
			raw.Add(raw, value.Initial.Raw())
		}
	}
	for _, value := range a.accountMargins {
		if value.Currency.Equal(denomination) {
			raw.Add(raw, value.Initial.Raw())
		}
	}
	return money.MustFromRaw(raw, denomination)
}

func (a *MarginAccount) TotalMaintenanceMargin(denomination currency.Currency) money.Money {
	raw := new(big.Int)
	for _, value := range a.margins {
		if value.Currency.Equal(denomination) {
			raw.Add(raw, value.Maintenance.Raw())
		}
	}
	for _, value := range a.accountMargins {
		if value.Currency.Equal(denomination) {
			raw.Add(raw, value.Maintenance.Raw())
		}
	}
	return money.MustFromRaw(raw, denomination)
}

func (a *MarginAccount) CalculateInitialMargin(
	instrument MarginInstrument,
	quantity, price decimal.Decimal,
	useQuoteForInverse bool,
) (money.Money, error) {
	return a.calculateMargin(
		instrument,
		quantity,
		price,
		useQuoteForInverse,
		instrument.InitialMarginRate,
	)
}

func (a *MarginAccount) CalculateMaintenanceMargin(
	instrument MarginInstrument,
	quantity, price decimal.Decimal,
	useQuoteForInverse bool,
) (money.Money, error) {
	return a.calculateMargin(
		instrument,
		quantity,
		price,
		useQuoteForInverse,
		instrument.MaintenanceRate,
	)
}

// CalculatePnLs realizes premium cash flow for premium instruments and, for
// other instruments, PnL only when a fill reduces an existing position.
func (a *MarginAccount) CalculatePnLs(
	instrument MarginInstrument,
	fillSide OrderSide,
	fillQuantity, fillPrice decimal.Decimal,
	position *MarginPosition,
) ([]money.Money, error) {
	if instrument.Premium {
		notional, err := marginNotional(instrument, fillQuantity, fillPrice, false)
		if err != nil {
			return nil, err
		}
		if fillSide == OrderSideBuy {
			notional = notional.Neg()
		}
		return []money.Money{notional}, nil
	}
	if position == nil || position.Quantity.Sign() <= 0 || position.EntrySide == fillSide {
		return nil, nil
	}
	quantity := fillQuantity
	if position.Quantity.Cmp(quantity) < 0 {
		quantity = position.Quantity
	}
	priceDifference := fillPrice.Sub(position.AvgPrice)
	if position.EntrySide == OrderSideSell {
		priceDifference = priceDifference.Neg()
	}
	pnl := quantity.Mul(priceDifference)
	value, err := money.FromDecimal(pnl, instrument.QuoteCurrency)
	if err != nil {
		return nil, err
	}
	return []money.Money{value}, nil
}

func (a *MarginAccount) calculateMargin(
	instrument MarginInstrument,
	quantity, price decimal.Decimal,
	useQuoteForInverse bool,
	rate decimal.Decimal,
) (money.Money, error) {
	leverage := a.Leverage(instrument.ID)
	notional, err := marginNotional(instrument, quantity, price, useQuoteForInverse)
	if err != nil {
		return money.Money{}, err
	}
	adjusted, err := notional.Decimal().Quo(leverage, decimal.MaxPrecision, decimal.RoundHalfEven)
	if err != nil {
		return money.Money{}, err
	}
	return money.FromDecimal(adjusted.Mul(rate), notional.Currency())
}

func (a *MarginAccount) recalculateBalance(denomination currency.Currency) {
	current, ok := a.balances[denomination.Code]
	if !ok {
		current = MustAccountBalance(
			money.Zero(denomination),
			money.Zero(denomination),
			money.Zero(denomination),
		)
		if _, exists := a.balances[denomination.Code]; !exists {
			a.balanceOrder = append(a.balanceOrder, denomination.Code)
		}
	}
	totalMargin := new(big.Int).Add(
		a.TotalInitialMargin(denomination).Raw(),
		a.TotalMaintenanceMargin(denomination).Raw(),
	)
	totalRaw := current.Total.Raw()
	if totalMargin.Cmp(totalRaw) > 0 {
		if totalRaw.Sign() > 0 {
			totalMargin.Set(totalRaw)
		} else {
			totalMargin.SetInt64(0)
		}
	}
	freeRaw := new(big.Int).Sub(totalRaw, totalMargin)
	a.balances[denomination.Code] = MustAccountBalance(
		current.Total,
		money.MustFromRaw(totalMargin, denomination),
		money.MustFromRaw(freeRaw, denomination),
	)
}

func (a *MarginAccount) routeMargins(values []MarginBalance) {
	for _, value := range values {
		if value.InstrumentID == nil {
			a.accountMargins[value.Currency.Code] = value
		} else {
			a.margins[*value.InstrumentID] = value
		}
	}
}

func (a *MarginAccount) updateBalances(values []AccountBalance) {
	for _, value := range values {
		code := value.Currency.Code
		if _, ok := a.balances[code]; !ok {
			a.balanceOrder = append(a.balanceOrder, code)
		}
		a.balances[code] = value
	}
}

func (a *MarginAccount) balanceAmounts(selectAmount func(AccountBalance) money.Money) []BalanceAmount {
	result := make([]BalanceAmount, 0, len(a.balanceOrder))
	for _, code := range a.balanceOrder {
		value := a.balances[code]
		result = append(result, BalanceAmount{Currency: value.Currency, Amount: selectAmount(value)})
	}
	return result
}

func marginNotional(
	instrument MarginInstrument,
	quantity, price decimal.Decimal,
	useQuoteForInverse bool,
) (money.Money, error) {
	multiplier := instrument.Multiplier
	if multiplier.IsZero() {
		multiplier = decimal.MustParse("1")
	}
	quantity = quantity.Mul(multiplier)
	if instrument.Inverse && !useQuoteForInverse {
		if instrument.BaseCurrency == nil {
			return money.Money{}, fmt.Errorf("inverse instrument has no base currency")
		}
		value, err := quantity.Quo(price, instrument.BaseCurrency.Precision, decimal.RoundHalfEven)
		if err != nil {
			return money.Money{}, err
		}
		return money.FromDecimal(value, *instrument.BaseCurrency)
	}
	if instrument.Inverse {
		return money.FromDecimal(quantity, instrument.QuoteCurrency)
	}
	return money.FromDecimal(quantity.Mul(price), instrument.QuoteCurrency)
}

func requirePositiveLeverage(value decimal.Decimal) {
	if value.Sign() <= 0 {
		panic(fmt.Sprintf("invalid Decimal for 'leverage' not positive, was %s", value))
	}
}

func copyMarginAccountState(value MarginAccountState) MarginAccountState {
	result := value
	result.BaseCurrency = copyCurrency(value.BaseCurrency)
	result.Balances = append([]AccountBalance(nil), value.Balances...)
	result.Margins = append([]MarginBalance(nil), value.Margins...)
	return result
}
