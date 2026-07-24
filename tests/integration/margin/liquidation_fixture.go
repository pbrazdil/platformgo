package margin

import (
	"fmt"
	"sort"

	"github.com/upcomers-org/platformgo/internal/decimal"
)

type liquidationClock struct {
	tick uint64
}

func (c *liquidationClock) advance() uint64 {
	c.tick++
	return c.tick
}

type liquidationPosition struct {
	Symbol   string
	Signed   decimal.Decimal
	Entry    decimal.Decimal
	Mark     decimal.Decimal
	Open     bool
	OpenedAt uint64
}

func (p liquidationPosition) magnitude() decimal.Decimal {
	if p.Signed.Sign() < 0 {
		return p.Signed.Neg()
	}
	return p.Signed
}

func (p liquidationPosition) notional() decimal.Decimal {
	return p.magnitude().Mul(p.Mark)
}

type liquidationOrder struct {
	IntentID   string
	Symbol     string
	Side       string
	Quantity   decimal.Decimal
	Status     string
	Close      decimal.Decimal
	ReduceOnly bool
	CreatedAt  uint64
}

type liquidationAccount struct {
	ID                string
	Status            string
	Balance           decimal.Decimal
	MarginInitialized bool
	Positions         map[string]*liquidationPosition
	Orders            []liquidationOrder
}

type liquidationFixture struct {
	clock    liquidationClock
	accounts map[string]*liquidationAccount
}

func newLiquidationFixture() *liquidationFixture {
	return &liquidationFixture{accounts: make(map[string]*liquidationAccount)}
}

func (f *liquidationFixture) addAccount(id string, hotAdded bool) *liquidationAccount {
	status := "active"
	marginInitialized := true
	if hotAdded {
		status = "pending"
		marginInitialized = false
	}
	account := &liquidationAccount{
		ID:                id,
		Status:            status,
		MarginInitialized: marginInitialized,
		Positions:         make(map[string]*liquidationPosition),
	}
	f.accounts[id] = account
	return account
}

func (f *liquidationFixture) activate(account *liquidationAccount) {
	account.Status = "active"
	// Reconciliation must seed margin state for accounts added after startup.
	account.MarginInitialized = true
	f.clock.advance()
}

func (f *liquidationFixture) setBalance(account *liquidationAccount, amount string) {
	account.Balance = decimal.MustParse(amount)
	f.clock.advance()
}

func (f *liquidationFixture) deposit(account *liquidationAccount, amount string) {
	account.Balance = account.Balance.Add(decimal.MustParse(amount))
	f.clock.advance()
}

func (f *liquidationFixture) open(
	account *liquidationAccount,
	symbol, side, quantity, entry string,
) *liquidationPosition {
	signed := decimal.MustParse(quantity)
	if side == "sell" {
		signed = signed.Neg()
	}
	position := &liquidationPosition{
		Symbol:   symbol,
		Signed:   signed,
		Entry:    decimal.MustParse(entry),
		Mark:     decimal.MustParse(entry),
		Open:     true,
		OpenedAt: f.clock.advance(),
	}
	account.Positions[symbol] = position
	return position
}

func (f *liquidationFixture) setMark(position *liquidationPosition, mark string) {
	position.Mark = decimal.MustParse(mark)
	f.clock.advance()
}

func (f *liquidationFixture) breach(account *liquidationAccount) error {
	if account.Status != "active" {
		return fmt.Errorf("account %s is not active", account.ID)
	}
	if !account.MarginInitialized {
		return fmt.Errorf("account %s has no initialized margin", account.ID)
	}
	f.setBalance(account, "0.01")
	return nil
}

func (f *liquidationFixture) liquidateAt(
	account *liquidationAccount,
	position *liquidationPosition,
	close decimal.Decimal,
) liquidationOrder {
	for _, order := range account.Orders {
		if order.Symbol == position.Symbol && order.IntentID == "stopout:"+position.Symbol {
			return order
		}
	}
	side := "sell"
	if position.Signed.Sign() < 0 {
		side = "buy"
	}
	order := liquidationOrder{
		IntentID:   "stopout:" + position.Symbol,
		Symbol:     position.Symbol,
		Side:       side,
		Quantity:   position.magnitude(),
		Status:     "filled",
		Close:      close,
		ReduceOnly: true,
		CreatedAt:  f.clock.advance(),
	}
	position.Open = false
	account.Orders = append(account.Orders, order)
	return order
}

func (f *liquidationFixture) liquidateMarket(
	account *liquidationAccount,
	position *liquidationPosition,
) liquidationOrder {
	return f.liquidateAt(account, position, position.Mark)
}

func liquidationFloor(mark decimal.Decimal, basisPoints int64) decimal.Decimal {
	numerator := mark.Mul(decimal.MustParse(fmt.Sprintf("%d", 10_000-basisPoints)))
	floor, err := numerator.Quo(decimal.MustParse("10000"), mark.Scale(), decimal.RoundTowardZero)
	if err != nil {
		panic(err)
	}
	return floor
}

func (f *liquidationFixture) liquidateBounded(
	account *liquidationAccount,
	position *liquidationPosition,
	adverseBid string,
	basisPoints int64,
) liquidationOrder {
	floor := liquidationFloor(position.Mark, basisPoints)
	bid := decimal.MustParse(adverseBid)
	close := bid
	if close.Cmp(floor) < 0 {
		// The reduce-only IOC limit is the worst permissible execution price.
		close = floor
	}
	return f.liquidateAt(account, position, close)
}

func (f *liquidationFixture) liquidateWithLadder(
	account *liquidationAccount,
	position *liquidationPosition,
	executableBid string,
) (liquidationOrder, []int64, error) {
	bid := decimal.MustParse(executableBid)
	var attempted []int64
	for _, basisPoints := range []int64{500, 1000, 1500, 2000} {
		attempted = append(attempted, basisPoints)
		f.clock.advance()
		if bid.Cmp(liquidationFloor(position.Mark, basisPoints)) >= 0 {
			return f.liquidateAt(account, position, bid), attempted, nil
		}
	}
	return liquidationOrder{}, attempted, fmt.Errorf("no executable bid inside liquidation ladder")
}

func (f *liquidationFixture) liquidateWorst(account *liquidationAccount) (liquidationOrder, error) {
	var open []*liquidationPosition
	for _, position := range account.Positions {
		if position.Open {
			open = append(open, position)
		}
	}
	if len(open) == 0 {
		return liquidationOrder{}, fmt.Errorf("account %s has no open positions", account.ID)
	}
	sort.Slice(open, func(i, j int) bool {
		comparison := open[i].notional().Cmp(open[j].notional())
		if comparison == 0 {
			return open[i].Symbol < open[j].Symbol
		}
		return comparison > 0
	})
	return f.liquidateMarket(account, open[0]), nil
}
