package order

import (
	"fmt"
	"strings"

	"github.com/upcomers-org/platformgo/internal/currency"
	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
	"github.com/upcomers-org/platformgo/internal/money"
)

const OrderStatusVoided OrderStatus = 15

type TrailingOffsetType uint8

const (
	TrailingOffsetTypeNone TrailingOffsetType = iota
	TrailingOffsetTypePrice
)

type ErrorKind string

const (
	ErrorInvalidStateTransition ErrorKind = "invalid_state_transition"
	ErrorInvalidOrderEvent      ErrorKind = "invalid_order_event"
	ErrorDuplicateFill          ErrorKind = "duplicate_fill"
	ErrorDuplicateFillVoid      ErrorKind = "duplicate_fill_void"
	ErrorStaleFillVoid          ErrorKind = "stale_fill_void"
	ErrorOverVoid               ErrorKind = "over_void"
	ErrorInvariant              ErrorKind = "invariant"
)

type Error struct {
	Kind    ErrorKind
	TradeID ids.TradeID
	Message string
}

func (e *Error) Error() string {
	if e.Message != "" {
		return e.Message
	}
	if e.TradeID != "" {
		return fmt.Sprintf("%s: %s", e.Kind, e.TradeID)
	}
	return string(e.Kind)
}

type Config struct {
	ClientOrderID      ids.ClientOrderID
	InstrumentID       ids.InstrumentID
	Side               OrderSide
	Type               OrderType
	TimeInForce        TimeInForce
	Quantity           decimal.Quantity
	Price              *decimal.Price
	TriggerPrice       *decimal.Price
	TriggerType        TriggerType
	ExpireTime         *uint64
	DisplayQuantity    *decimal.Quantity
	PostOnly           bool
	ReduceOnly         bool
	QuoteQuantity      bool
	ContingencyType    ContingencyType
	OrderListID        *ids.OrderListID
	LinkedOrderIDs     []ids.ClientOrderID
	ParentOrderID      *ids.ClientOrderID
	ExecAlgorithmID    *ids.ExecAlgorithmID
	ExecSpawnID        *ids.ClientOrderID
	ActivationPrice    *decimal.Price
	LimitOffset        *decimal.Decimal
	TrailingOffset     *decimal.Decimal
	TrailingOffsetType TrailingOffsetType
	TimestampInit      uint64
}

type fillRecord struct {
	quantity   decimal.Quantity
	price      decimal.Price
	commission *money.Money
	voided     decimal.Quantity
}

type Core struct {
	config Config

	status            OrderStatus
	statusBefore      OrderStatus
	accountID         *ids.AccountID
	venueOrderID      *ids.VenueOrderID
	venueOrderIDs     []ids.VenueOrderID
	venuePositionID   *ids.PositionID
	filledQuantity    decimal.Quantity
	voidedQuantity    decimal.Quantity
	overfill          decimal.Quantity
	averagePrice      *decimal.Decimal
	commissions       map[string]money.Money
	fills             map[ids.TradeID]*fillRecord
	fillVoids         map[ids.TradeID]decimal.Quantity
	tradeIDs          []ids.TradeID
	rejection         string
	reopenedAfterVoid bool

	tsSubmitted uint64
	tsAccepted  uint64
	tsTriggered uint64
	tsLast      uint64
	tsClosed    uint64

	eventCount int
	lastEvent  string
}

func NewCore(config Config) (*Core, error) {
	if config.ClientOrderID == "" {
		config.ClientOrderID = ids.MustClientOrderID("O-19700101-000000-001-001-1")
	}
	if config.InstrumentID.String() == "." {
		config.InstrumentID = ids.MustInstrumentID("AUDUSD.SIM")
	}
	if config.Side == OrderSideNoOrderSide {
		config.Side = OrderSideBuy
	}
	if config.Type == 0 {
		config.Type = OrderTypeMarket
	}
	if config.TimeInForce == 0 {
		config.TimeInForce = TimeInForceDay
	}
	if config.Quantity.IsZero() {
		config.Quantity = decimal.MustQuantity("100000")
	}
	if err := CheckDisplayQuantity(config.DisplayQuantity, config.Quantity); err != nil {
		return nil, err
	}
	if err := CheckTimeInForce(config.TimeInForce, config.ExpireTime); err != nil {
		return nil, err
	}
	zero, _ := decimal.ZeroQuantity(config.Quantity.Precision())
	return &Core{
		config:         config,
		status:         OrderStatusInitialized,
		statusBefore:   OrderStatusInitialized,
		filledQuantity: zero,
		voidedQuantity: zero,
		overfill:       zero,
		commissions:    make(map[string]money.Money),
		fills:          make(map[ids.TradeID]*fillRecord),
		fillVoids:      make(map[ids.TradeID]decimal.Quantity),
		eventCount:     1,
		lastEvent:      "Initialized",
	}, nil
}

func MustCore(config Config) *Core {
	order, err := NewCore(config)
	if err != nil {
		panic(err)
	}
	return order
}

func ClosingSide(side PositionSide) OrderSide {
	switch side {
	case PositionSideLong:
		return OrderSideSell
	case PositionSideShort:
		return OrderSideBuy
	default:
		return OrderSideNoOrderSide
	}
}

func (o *Core) SignedQuantity() decimal.Decimal {
	value := o.config.Quantity.Decimal()
	if o.config.Side == OrderSideSell {
		return value.Neg()
	}
	return value
}

func (o *Core) WouldReduceOnly(side PositionSide, positionQuantity decimal.Quantity) bool {
	closes := (o.config.Side == OrderSideBuy && side == PositionSideShort) ||
		(o.config.Side == OrderSideSell && side == PositionSideLong)
	return closes && o.config.Quantity.Cmp(positionQuantity) <= 0
}

func (o *Core) Deny(timestamp uint64) error {
	if o.status != OrderStatusInitialized {
		return stateError()
	}
	o.setStatus(OrderStatusDenied, "Denied", timestamp)
	return nil
}

func (o *Core) Submit(accountID ids.AccountID, timestamp uint64) error {
	if o.status != OrderStatusInitialized {
		return stateError()
	}
	o.accountID = copyPointer(accountID)
	o.setStatus(OrderStatusSubmitted, "Submitted", timestamp)
	o.tsSubmitted = timestamp
	return nil
}

func (o *Core) Accept(accountID ids.AccountID, venueOrderID ids.VenueOrderID, timestamp uint64) error {
	if o.status != OrderStatusInitialized && o.status != OrderStatusSubmitted {
		return stateError()
	}
	o.accountID = copyPointer(accountID)
	o.setVenueOrderID(venueOrderID)
	o.setStatus(OrderStatusAccepted, "Accepted", timestamp)
	o.tsAccepted = timestamp
	return nil
}

func (o *Core) Cancel(timestamp uint64) error {
	if o.status == OrderStatusVoided {
		return stateError()
	}
	switch o.status {
	case OrderStatusSubmitted, OrderStatusAccepted, OrderStatusPartiallyFilled,
		OrderStatusPendingCancel, OrderStatusPendingUpdate:
	default:
		return stateError()
	}
	o.setStatus(OrderStatusCanceled, "Canceled", timestamp)
	return nil
}

func (o *Core) Expire(timestamp uint64) error {
	if o.status != OrderStatusAccepted && o.status != OrderStatusPartiallyFilled {
		return stateError()
	}
	o.setStatus(OrderStatusExpired, "Expired", timestamp)
	return nil
}

func (o *Core) Reject(reason string, timestamp uint64) error {
	if o.status == OrderStatusVoided {
		return stateError()
	}
	o.rejection = reason
	o.setStatus(OrderStatusRejected, "Rejected", timestamp)
	return nil
}

func (o *Core) PendingUpdate(timestamp uint64) error {
	if !isWorkingStatus(o.status) {
		return stateError()
	}
	o.statusBefore = o.status
	o.setStatus(OrderStatusPendingUpdate, "PendingUpdate", timestamp)
	return nil
}

func (o *Core) PendingCancel(timestamp uint64) error {
	if !isWorkingStatus(o.status) {
		return stateError()
	}
	o.statusBefore = o.status
	o.setStatus(OrderStatusPendingCancel, "PendingCancel", timestamp)
	return nil
}

type Update struct {
	Quantity        *decimal.Quantity
	IsQuoteQuantity bool
	VenueOrderID    *ids.VenueOrderID
	Timestamp       uint64
}

func (o *Core) Update(update Update) error {
	if o.status == OrderStatusVoided || isClosedStatus(o.status) {
		return stateError()
	}
	restore := o.status
	if o.status == OrderStatusPendingUpdate || o.status == OrderStatusPendingCancel {
		restore = o.statusBefore
	}
	if update.Quantity != nil {
		o.config.Quantity = *update.Quantity
	}
	o.config.QuoteQuantity = update.IsQuoteQuantity
	if update.VenueOrderID != nil {
		o.setVenueOrderID(*update.VenueOrderID)
	}
	o.status = restore
	o.record("Updated", update.Timestamp)
	return nil
}

func (o *Core) Trigger(timestamp uint64) error {
	if o.config.Type != OrderTypeStopLimit || o.status != OrderStatusAccepted {
		return stateError()
	}
	o.setStatus(OrderStatusTriggered, "Triggered", timestamp)
	o.tsTriggered = timestamp
	return nil
}

type Fill struct {
	TradeID         ids.TradeID
	Quantity        decimal.Quantity
	Price           decimal.Price
	Commission      *money.Money
	AccountID       *ids.AccountID
	VenueOrderID    *ids.VenueOrderID
	VenuePositionID *ids.PositionID
	Timestamp       uint64
}

func (o *Core) Fill(fill Fill) error {
	if o.status == OrderStatusVoided {
		return stateError()
	}
	if _, exists := o.fills[fill.TradeID]; exists {
		return &Error{Kind: ErrorDuplicateFill, TradeID: fill.TradeID}
	}
	if fill.AccountID != nil {
		o.accountID = copyPointer(*fill.AccountID)
	}
	if fill.VenueOrderID != nil {
		o.setVenueOrderID(*fill.VenueOrderID)
	}
	if fill.VenuePositionID != nil {
		o.venuePositionID = copyPointer(*fill.VenuePositionID)
	}
	o.fills[fill.TradeID] = &fillRecord{
		quantity:   fill.Quantity,
		price:      fill.Price,
		commission: fill.Commission,
		voided:     zeroLike(fill.Quantity),
	}
	o.tradeIDs = append(o.tradeIDs, fill.TradeID)
	o.filledQuantity = mustAdd(o.filledQuantity, fill.Quantity)
	if fill.Commission != nil {
		o.addCommission(*fill.Commission)
	}
	o.recalculateAveragePrice()
	o.overfill = o.CalculateOverfill(zeroLike(fill.Quantity))
	if o.filledQuantity.Cmp(o.config.Quantity) >= 0 {
		o.status = OrderStatusFilled
	} else if !o.reopenedAfterVoid &&
		mustAdd(o.filledQuantity, o.voidedQuantity).Cmp(o.config.Quantity) >= 0 {
		o.status = OrderStatusVoided
	} else {
		o.status = OrderStatusPartiallyFilled
	}
	o.record("Filled", fill.Timestamp)
	return nil
}

type FillVoid struct {
	TradeID      ids.TradeID
	Quantity     decimal.Quantity
	Commission   *money.Money
	Reopened     bool
	InstrumentID *ids.InstrumentID
	AccountID    *ids.AccountID
	VenueOrderID *ids.VenueOrderID
	Timestamp    uint64
}

func (o *Core) VoidFill(void FillVoid) error {
	if void.InstrumentID != nil && void.InstrumentID.String() != o.config.InstrumentID.String() {
		return &Error{Kind: ErrorInvalidOrderEvent}
	}
	if void.AccountID != nil && o.accountID != nil && *void.AccountID != *o.accountID {
		return &Error{Kind: ErrorInvalidOrderEvent}
	}
	if void.VenueOrderID != nil && o.venueOrderID != nil && *void.VenueOrderID != *o.venueOrderID {
		return &Error{Kind: ErrorInvalidOrderEvent}
	}
	if prior, exists := o.fillVoids[void.TradeID]; exists {
		comparison := void.Quantity.Cmp(prior)
		if comparison == 0 {
			return &Error{Kind: ErrorDuplicateFillVoid, TradeID: void.TradeID}
		}
		if comparison < 0 {
			return &Error{Kind: ErrorStaleFillVoid, TradeID: void.TradeID}
		}
	}
	if void.Quantity.Cmp(o.config.Quantity) > 0 {
		return &Error{Kind: ErrorOverVoid, TradeID: void.TradeID}
	}

	fill := o.fills[void.TradeID]
	if void.Reopened && fill == nil {
		return &Error{Kind: ErrorInvalidOrderEvent}
	}
	if fill == nil && void.Commission != nil && !void.Commission.IsZero() {
		return &Error{Kind: ErrorOverVoid, TradeID: void.TradeID}
	}
	if fill != nil {
		remaining := fill.quantity.SaturatingSub(fill.voided)
		if void.Quantity.Cmp(remaining) > 0 {
			return &Error{Kind: ErrorOverVoid, TradeID: void.TradeID}
		}
		fill.voided = mustAdd(fill.voided, void.Quantity)
		o.filledQuantity = mustSub(o.filledQuantity, void.Quantity)
	}
	o.fillVoids[void.TradeID] = void.Quantity
	o.voidedQuantity = mustAdd(o.voidedQuantity, void.Quantity)
	if void.Commission != nil {
		o.subtractCommission(*void.Commission)
	}
	o.recalculateAveragePrice()

	was := o.status
	switch {
	case (was == OrderStatusCanceled || was == OrderStatusExpired) && void.Reopened:
	case was == OrderStatusVoided:
	case void.Reopened:
		o.reopenedAfterVoid = true
		if o.filledQuantity.IsZero() {
			o.status = OrderStatusAccepted
		} else {
			o.status = OrderStatusPartiallyFilled
		}
	case was == OrderStatusPartiallyFilled && fill != nil:
		if o.filledQuantity.IsZero() {
			o.status = OrderStatusAccepted
		} else {
			o.status = OrderStatusPartiallyFilled
		}
	default:
		o.status = OrderStatusVoided
	}
	o.record("FillVoided", void.Timestamp)
	return nil
}

func (o *Core) CalculateOverfill(incoming decimal.Quantity) decimal.Quantity {
	total := mustAdd(o.filledQuantity, incoming)
	return total.SaturatingSub(o.config.Quantity)
}

func (o *Core) Quantity() decimal.Quantity       { return o.config.Quantity }
func (o *Core) FilledQuantity() decimal.Quantity { return o.filledQuantity }
func (o *Core) VoidedQuantity() decimal.Quantity { return o.voidedQuantity }
func (o *Core) OverfillQuantity() decimal.Quantity {
	return o.overfill
}
func (o *Core) LeavesQuantity() decimal.Quantity {
	if o.status == OrderStatusVoided || o.filledQuantity.Cmp(o.config.Quantity) >= 0 {
		return zeroLike(o.config.Quantity)
	}
	remaining := o.config.Quantity.SaturatingSub(o.filledQuantity)
	if !o.reopenedAfterVoid {
		remaining = remaining.SaturatingSub(o.voidedQuantity)
	}
	return remaining
}
func (o *Core) Status() OrderStatus            { return o.status }
func (o *Core) AveragePrice() *decimal.Decimal { return copyDecimal(o.averagePrice) }
func (o *Core) IsOpen() bool                   { return isWorkingStatus(o.status) }
func (o *Core) IsClosed() bool                 { return isClosedStatus(o.status) }
func (o *Core) EventCount() int                { return o.eventCount }
func (o *Core) LastEvent() string              { return o.lastEvent }
func (o *Core) AccountID() *ids.AccountID      { return copyPointerValue(o.accountID) }
func (o *Core) TimestampAccepted() uint64      { return o.tsAccepted }
func (o *Core) TimestampLast() uint64          { return o.tsLast }
func (o *Core) TimestampClosed() uint64        { return o.tsClosed }
func (o *Core) VenueOrderIDs() []ids.VenueOrderID {
	return append([]ids.VenueOrderID(nil), o.venueOrderIDs...)
}
func (o *Core) TradeIDs() []ids.TradeID { return append([]ids.TradeID(nil), o.tradeIDs...) }
func (o *Core) IsQuoteQuantity() bool   { return o.config.QuoteQuantity }

func (o *Core) Commission(denomination currency.Currency) (money.Money, bool) {
	value, ok := o.commissions[denomination.Code]
	return value, ok
}

func (o *Core) Commissions() []money.Money {
	values := make([]money.Money, 0, len(o.commissions))
	for _, value := range o.commissions {
		values = append(values, value)
	}
	return values
}

func (o *Core) IsPrimary() bool {
	return o.config.ExecAlgorithmID != nil && o.config.ExecSpawnID != nil &&
		*o.config.ExecSpawnID == o.config.ClientOrderID
}

func (o *Core) IsSpawned() bool {
	return o.config.ExecAlgorithmID != nil && o.config.ExecSpawnID != nil &&
		*o.config.ExecSpawnID != o.config.ClientOrderID
}

func (o *Core) IsContingency() bool {
	return o.config.ContingencyType != ContingencyTypeNoContingency
}

func (o *Core) IsParentOrder() bool { return o.IsContingency() && o.config.ParentOrderID == nil }
func (o *Core) IsChildOrder() bool  { return o.config.ParentOrderID != nil }

func CheckDisplayQuantity(display *decimal.Quantity, quantity decimal.Quantity) error {
	if display != nil && display.Cmp(quantity) > 0 {
		return &Error{Kind: ErrorInvariant, Message: "`display_qty` may not exceed `quantity`"}
	}
	return nil
}

func CheckTimeInForce(timeInForce TimeInForce, expireTime *uint64) error {
	if timeInForce == TimeInForceGTD && expireTime == nil {
		return &Error{Kind: ErrorInvariant, Message: "`expire_time` is required for `GTD` order"}
	}
	return nil
}

func (o *Core) record(event string, timestamp uint64) {
	o.eventCount++
	o.lastEvent = event
	o.tsLast = timestamp
	if isClosedStatus(o.status) {
		o.tsClosed = timestamp
	}
}

func (o *Core) setStatus(status OrderStatus, event string, timestamp uint64) {
	o.status = status
	o.record(event, timestamp)
}

func (o *Core) setVenueOrderID(value ids.VenueOrderID) {
	o.venueOrderID = copyPointer(value)
	for _, existing := range o.venueOrderIDs {
		if existing == value {
			return
		}
	}
	o.venueOrderIDs = append(o.venueOrderIDs, value)
}

func (o *Core) addCommission(value money.Money) {
	code := value.Currency().Code
	if current, ok := o.commissions[code]; ok {
		o.commissions[code] = current.Add(value)
	} else {
		o.commissions[code] = value
	}
}

func (o *Core) subtractCommission(value money.Money) {
	code := value.Currency().Code
	if current, ok := o.commissions[code]; ok {
		o.commissions[code] = current.Sub(value)
	}
}

func (o *Core) recalculateAveragePrice() {
	total := decimal.MustParse("0")
	quantity := decimal.MustParse("0")
	for _, fill := range o.fills {
		surviving := fill.quantity.SaturatingSub(fill.voided)
		if surviving.IsZero() {
			continue
		}
		total = total.Add(fill.price.Decimal().Mul(surviving.Decimal()))
		quantity = quantity.Add(surviving.Decimal())
	}
	if quantity.IsZero() {
		o.averagePrice = nil
		return
	}
	value, err := total.Quo(quantity, decimal.MaxPrecision, decimal.RoundHalfEven)
	if err != nil {
		panic(err)
	}
	normalized := value.Normalize()
	o.averagePrice = &normalized
}

func isWorkingStatus(status OrderStatus) bool {
	switch status {
	case OrderStatusSubmitted, OrderStatusAccepted, OrderStatusTriggered,
		OrderStatusPendingUpdate, OrderStatusPendingCancel, OrderStatusPartiallyFilled:
		return true
	default:
		return false
	}
}

func isClosedStatus(status OrderStatus) bool {
	switch status {
	case OrderStatusDenied, OrderStatusRejected, OrderStatusCanceled,
		OrderStatusExpired, OrderStatusFilled, OrderStatusVoided:
		return true
	default:
		return false
	}
}

func stateError() error { return &Error{Kind: ErrorInvalidStateTransition} }

func zeroLike(value decimal.Quantity) decimal.Quantity {
	zero, err := decimal.ZeroQuantity(value.Precision())
	if err != nil {
		panic(err)
	}
	return zero
}

func mustAdd(left, right decimal.Quantity) decimal.Quantity {
	value, ok := left.Add(right)
	if !ok {
		panic("quantity addition failed")
	}
	return value
}

func mustSub(left, right decimal.Quantity) decimal.Quantity {
	value, ok := left.Sub(right)
	if !ok {
		panic("quantity subtraction failed")
	}
	return value
}

func copyPointer[T any](value T) *T { return &value }
func copyPointerValue[T any](value *T) *T {
	if value == nil {
		return nil
	}
	return copyPointer(*value)
}

func copyDecimal(value *decimal.Decimal) *decimal.Decimal {
	if value == nil {
		return nil
	}
	copied := decimal.MustParse(value.String())
	return &copied
}

func (o *Core) DebugState() string {
	return strings.Join([]string{o.status.String(), o.filledQuantity.String(), o.LeavesQuantity().String()}, ":")
}
