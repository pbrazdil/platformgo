package order

import (
	"github.com/upcomers-org/platformgo/internal/currency"
	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/money"
)

type TestOrderBuilder struct {
	orderType       OrderType
	instrumentID    string
	quantity        decimal.Quantity
	price           *decimal.Price
	contingencyType *ContingencyType
	submit          bool
}

func NewTestOrderBuilder(orderType OrderType) *TestOrderBuilder {
	return &TestOrderBuilder{orderType: orderType}
}

func (b *TestOrderBuilder) Instrument(value string) *TestOrderBuilder {
	b.instrumentID = value
	return b
}
func (b *TestOrderBuilder) Quantity(value decimal.Quantity) *TestOrderBuilder {
	b.quantity = value
	return b
}
func (b *TestOrderBuilder) Price(value decimal.Price) *TestOrderBuilder {
	b.price = &value
	return b
}
func (b *TestOrderBuilder) Contingency(value ContingencyType) *TestOrderBuilder {
	b.contingencyType = &value
	return b
}
func (b *TestOrderBuilder) Submit(value bool) *TestOrderBuilder {
	b.submit = value
	return b
}

type BuiltTestOrder struct {
	OrderType       OrderType
	InstrumentID    string
	Quantity        decimal.Quantity
	Price           *decimal.Price
	ContingencyType ContingencyType
	AccountID       *string
	contingencySet  bool
}

func (o BuiltTestOrder) IsContingency() bool { return o.contingencySet }

func (b *TestOrderBuilder) Build() BuiltTestOrder {
	contingency := ContingencyTypeNoContingency
	if b.contingencyType != nil {
		contingency = *b.contingencyType
	}
	var account *string
	if b.submit {
		value := "ACCOUNT-001"
		account = &value
	}
	return BuiltTestOrder{
		OrderType: b.orderType, InstrumentID: b.instrumentID, Quantity: b.quantity,
		Price: b.price, ContingencyType: contingency, AccountID: account, contingencySet: true,
	}
}

type StubFill struct {
	PositionID    *string
	Commission    *money.Money
	LiquiditySide LiquiditySide
}

type OrderFilledStub struct {
	positionID bool
	commission bool
}

func NewOrderFilledStub(_ BuiltTestOrder) *OrderFilledStub {
	return &OrderFilledStub{positionID: true, commission: true}
}
func (b *OrderFilledStub) WithoutPositionID() *OrderFilledStub {
	b.positionID = false
	return b
}
func (b *OrderFilledStub) WithoutCommission() *OrderFilledStub {
	b.commission = false
	return b
}
func (b *OrderFilledStub) Build() StubFill {
	fill := StubFill{LiquiditySide: LiquiditySideMaker}
	if b.positionID {
		value := "1"
		fill.PositionID = &value
	}
	if b.commission {
		value := money.MustNew("2", currency.USD())
		fill.Commission = &value
	}
	return fill
}
