package engine

import (
	"sort"

	"github.com/upcomers-org/platformgo/internal/domain"
)

type tradingState struct {
	instruments []instrumentRecord
	accounts    []accountRecord
	risks       []riskRecord
	balances    []balanceRecord
	funding     []fundingRecord
	settlements []fundingSettlementRecord
	books       []bookRecord
	orders      []orderRecord
	fills       []fillRecord
	positions   []positionRecord
}

type instrumentRecord struct {
	revision              domain.InstrumentRevision
	settlementCurrency    domain.Currency
	initialMarginRate     domain.Rate
	maintenanceMarginRate domain.Rate
	maxLeverage           domain.Ratio
	makerFeeRate          domain.Rate
	takerFeeRate          domain.Rate
}

type accountRecord struct {
	accountID string
	omsMode   OmsMode
}

type riskRecord struct {
	accountID    string
	instrumentID string
	marginMode   MarginMode
	leverage     domain.Ratio
}

type balanceRecord struct {
	accountID string
	total     domain.Money
}

type fundingRecord struct {
	fundingID      ID
	settlementID   ID
	positionID     ID
	accountID      string
	instrument     domain.InstrumentRevision
	signedQuantity string
	oraclePrice    domain.Price
	rate           domain.Rate
	amount         domain.Money
}

type fundingSettlementRecord struct {
	settlementID ID
	instrument   domain.InstrumentRevision
	oraclePrice  domain.Price
	rate         domain.Rate
}

type bookRecord struct {
	instrumentID string
	markPrice    domain.Price
	hasMark      bool
	bids         []levelRecord
	asks         []levelRecord
}

type levelRecord struct {
	price    domain.Price
	quantity domain.Quantity
}

type orderRecord struct {
	orderID           ID
	accountID         string
	instrument        domain.InstrumentRevision
	side              Side
	orderType         OrderType
	timeInForce       TimeInForce
	status            OrderStatus
	quantity          domain.Quantity
	filledQuantity    domain.Quantity
	averagePrice      domain.Price
	hasAverage        bool
	price             domain.Price
	hasPrice          bool
	triggerPrice      domain.Price
	hasTrigger        bool
	triggered         bool
	triggeredAt       LogicalTime
	reduceOnly        bool
	positionID        ID
	bracketID         ID
	bracketLeg        BracketLeg
	bracketLegIndex   uint32
	originalQuantity  domain.Quantity
	hasRested         bool
	hasSlippageBand   bool
	maxSlippageBPS    uint32
	slippageReference domain.Price
	rejectReason      RejectionReason
	version           uint64
}

type fillRecord struct {
	fillID         ID
	orderID        ID
	accountID      string
	instrument     domain.InstrumentRevision
	side           Side
	price          domain.Price
	quantity       domain.Quantity
	positionID     ID
	positionEffect PositionEffect
	realizedPnL    domain.Money
	hasRealizedPnL bool
	liquiditySide  LiquiditySide
	fee            domain.Money
	hasFee         bool
	logicalTime    LogicalTime
}

type positionRecord struct {
	positionID         ID
	accountID          string
	instrument         domain.InstrumentRevision
	settlementCurrency domain.Currency
	side               PositionSide
	status             PositionStatus
	quantity           domain.Quantity
	averageOpenPrice   domain.Price
	realizedPnL        domain.Money
	marginMode         MarginMode
	isolatedCollateral domain.Money
	version            uint64
}

func (state tradingState) clone() tradingState {
	cloned := tradingState{
		instruments: append([]instrumentRecord(nil), state.instruments...),
		accounts:    append([]accountRecord(nil), state.accounts...),
		risks:       append([]riskRecord(nil), state.risks...),
		balances:    append([]balanceRecord(nil), state.balances...),
		funding:     append([]fundingRecord(nil), state.funding...),
		settlements: append([]fundingSettlementRecord(nil), state.settlements...),
		orders:      append([]orderRecord(nil), state.orders...),
		fills:       append([]fillRecord(nil), state.fills...),
		positions:   append([]positionRecord(nil), state.positions...),
		books:       make([]bookRecord, len(state.books)),
	}
	for index, book := range state.books {
		cloned.books[index] = book
		cloned.books[index].bids = append([]levelRecord(nil), book.bids...)
		cloned.books[index].asks = append([]levelRecord(nil), book.asks...)
	}
	return cloned
}

func (state tradingState) instrument(instrumentID string) (domain.InstrumentRevision, bool) {
	for _, instrument := range state.instruments {
		if instrument.revision.ID() == instrumentID {
			return instrument.revision, true
		}
	}
	return domain.InstrumentRevision{}, false
}

func (state tradingState) instrumentRecord(instrumentID string) (instrumentRecord, bool) {
	for _, instrument := range state.instruments {
		if instrument.revision.ID() == instrumentID {
			return instrument, true
		}
	}
	return instrumentRecord{}, false
}

func (state *tradingState) replaceInstrument(record instrumentRecord) {
	for index := range state.instruments {
		if state.instruments[index].revision.ID() == record.revision.ID() {
			state.instruments[index] = record
			return
		}
	}
	state.instruments = append(state.instruments, record)
	sort.Slice(state.instruments, func(left, right int) bool {
		return state.instruments[left].revision.ID() < state.instruments[right].revision.ID()
	})
}

func (state tradingState) accountMode(accountID string) OmsMode {
	for _, account := range state.accounts {
		if account.accountID == accountID {
			return account.omsMode
		}
	}
	return OmsModeNetting
}

func (state *tradingState) replaceAccount(account accountRecord) {
	for index := range state.accounts {
		if state.accounts[index].accountID == account.accountID {
			state.accounts[index] = account
			return
		}
	}
	state.accounts = append(state.accounts, account)
	sort.Slice(state.accounts, func(left, right int) bool {
		return state.accounts[left].accountID < state.accounts[right].accountID
	})
}

func (state tradingState) hasOpenPositions(accountID string) bool {
	for _, position := range state.positions {
		if position.accountID == accountID &&
			position.status == PositionStatusOpen {
			return true
		}
	}
	return false
}

func (state tradingState) hasActiveOrders(accountID string) bool {
	for _, order := range state.orders {
		if order.accountID == accountID &&
			(order.status == OrderStatusHeld ||
				order.status == OrderStatusWorking ||
				order.status == OrderStatusPartiallyFilled) {
			return true
		}
	}
	return false
}

func (state *tradingState) book(instrumentID string) (*bookRecord, bool) {
	for index := range state.books {
		if state.books[index].instrumentID == instrumentID {
			return &state.books[index], true
		}
	}
	return nil, false
}

func (state *tradingState) replaceBook(book bookRecord) {
	for index := range state.books {
		if state.books[index].instrumentID == book.instrumentID {
			state.books[index] = book
			return
		}
	}
	state.books = append(state.books, book)
	sort.Slice(state.books, func(left, right int) bool {
		return state.books[left].instrumentID < state.books[right].instrumentID
	})
}

func (state tradingState) orderIndex(orderID ID) (int, bool) {
	for index := range state.orders {
		if state.orders[index].orderID == orderID {
			return index, true
		}
	}
	return 0, false
}

// Order returns a detached snapshot of one order.
func (state State) Order(orderID ID) (OrderSnapshot, bool) {
	index, ok := state.trading.orderIndex(orderID)
	if !ok {
		return OrderSnapshot{}, false
	}
	return state.trading.orders[index].snapshot(), true
}

// Orders returns canonical order-ID-sorted snapshots.
func (state State) Orders() []OrderSnapshot {
	orders := make([]OrderSnapshot, len(state.trading.orders))
	for index, order := range state.trading.orders {
		orders[index] = order.snapshot()
	}
	sort.Slice(orders, func(left, right int) bool {
		return orders[left].OrderID.String() < orders[right].OrderID.String()
	})
	return orders
}

// FillsForOrder returns stable fill-ID-sorted snapshots for one order.
func (state State) FillsForOrder(orderID ID) []FillSnapshot {
	var fills []FillSnapshot
	for _, fill := range state.trading.fills {
		if fill.orderID == orderID {
			fills = append(fills, fill.snapshot())
		}
	}
	sort.Slice(fills, func(left, right int) bool {
		return fills[left].FillID.String() < fills[right].FillID.String()
	})
	return fills
}

// Position returns a detached snapshot of one position.
func (state State) Position(positionID ID) (PositionSnapshot, bool) {
	for _, position := range state.trading.positions {
		if position.positionID == positionID {
			return position.snapshot(), true
		}
	}
	return PositionSnapshot{}, false
}

// OpenPositions returns stable position-ID-sorted open snapshots for an
// account.
func (state State) OpenPositions(accountID string) []PositionSnapshot {
	var positions []PositionSnapshot
	for _, position := range state.trading.positions {
		if position.accountID == accountID &&
			position.status == PositionStatusOpen {
			positions = append(positions, position.snapshot())
		}
	}
	sort.Slice(positions, func(left, right int) bool {
		return positions[left].PositionID.String() < positions[right].PositionID.String()
	})
	return positions
}

func (order orderRecord) snapshot() OrderSnapshot {
	snapshot := OrderSnapshot{
		OrderID:         order.orderID,
		AccountID:       order.accountID,
		InstrumentID:    order.instrument.ID(),
		Side:            order.side,
		Type:            order.orderType,
		TimeInForce:     order.timeInForce,
		Status:          order.status,
		Quantity:        order.quantity.Decimal().String(),
		FilledQuantity:  order.filledQuantity.Decimal().String(),
		ReduceOnly:      order.reduceOnly,
		PositionID:      order.positionID,
		BracketID:       order.bracketID,
		BracketLeg:      order.bracketLeg,
		BracketLegIndex: order.bracketLegIndex,
		HasRested:       order.hasRested,
		HasSlippageBand: order.hasSlippageBand,
		MaxSlippageBPS:  order.maxSlippageBPS,
		RejectReason:    order.rejectReason,
		Version:         order.version,
	}
	snapshot.Triggered = order.triggered
	snapshot.TriggeredAt = order.triggeredAt
	if order.hasSlippageBand {
		snapshot.SlippageReference = order.slippageReference.Decimal().String()
	}
	if order.hasAverage {
		snapshot.AverageFillPrice = order.averagePrice.Decimal().String()
	}
	if order.hasPrice {
		snapshot.Price = order.price.Decimal().String()
	}
	if order.hasTrigger {
		snapshot.TriggerPrice = order.triggerPrice.Decimal().String()
	}
	return snapshot
}

func (fill fillRecord) snapshot() FillSnapshot {
	snapshot := FillSnapshot{
		FillID:         fill.fillID,
		OrderID:        fill.orderID,
		AccountID:      fill.accountID,
		InstrumentID:   fill.instrument.ID(),
		Side:           fill.side,
		Price:          fill.price.Decimal().String(),
		Quantity:       fill.quantity.Decimal().String(),
		PositionID:     fill.positionID,
		PositionEffect: fill.positionEffect,
		LogicalTime:    fill.logicalTime,
		LiquiditySide:  fill.liquiditySide,
	}
	if fill.hasFee {
		snapshot.Fee = fill.fee.Decimal().String()
		snapshot.FeeCurrency = fill.fee.Currency().Code()
	}
	if fill.hasRealizedPnL {
		snapshot.RealizedPnL = fill.realizedPnL.Decimal().String()
		snapshot.SettlementCurrency = fill.realizedPnL.Currency().Code()
	}
	return snapshot
}

func (position positionRecord) snapshot() PositionSnapshot {
	signedQuantity := position.quantity.Decimal().String()
	if position.side == PositionSideShort &&
		!position.quantity.Decimal().IsZero() {
		signedQuantity = "-" + signedQuantity
	}
	return PositionSnapshot{
		PositionID:         position.positionID,
		AccountID:          position.accountID,
		InstrumentID:       position.instrument.ID(),
		Side:               position.side,
		Status:             position.status,
		SignedQuantity:     signedQuantity,
		AverageOpenPrice:   position.averageOpenPrice.Decimal().String(),
		RealizedPnL:        position.realizedPnL.Decimal().String(),
		SettlementCurrency: position.settlementCurrency.Code(),
		MarginMode:         position.marginMode,
		IsolatedCollateral: position.isolatedCollateral.Decimal().String(),
		Version:            position.version,
	}
}

func (book bookRecord) snapshot() BookSnapshot {
	snapshot := BookSnapshot{
		InstrumentID: book.instrumentID,
		Bids:         levelSnapshots(book.bids),
		Asks:         levelSnapshots(book.asks),
	}
	if book.hasMark {
		snapshot.MarkPrice = book.markPrice.Decimal().String()
	}
	return snapshot
}

func levelSnapshots(levels []levelRecord) []BookLevel {
	snapshots := make([]BookLevel, len(levels))
	for index, level := range levels {
		snapshots[index] = BookLevel{
			Price:    level.price.Decimal().String(),
			Quantity: level.quantity.Decimal().String(),
		}
	}
	return snapshots
}
