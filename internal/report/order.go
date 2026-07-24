package report

import (
	"fmt"
	"reflect"

	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
	"github.com/upcomers-org/platformgo/internal/order"
)

type TrailingOffsetType uint8

const (
	TrailingOffsetTypeNone TrailingOffsetType = iota
	TrailingOffsetTypeBasisPoints
)

func (value TrailingOffsetType) String() string {
	if value == TrailingOffsetTypeBasisPoints {
		return "BASIS_POINTS"
	}
	return "NO_TRAILING_OFFSET"
}

type OrderStatusReportConfig struct {
	AccountID      ids.AccountID
	InstrumentID   ids.InstrumentID
	ClientOrderID  *ids.ClientOrderID
	VenueOrderID   ids.VenueOrderID
	OrderSide      order.OrderSide
	OrderType      order.OrderType
	TimeInForce    order.TimeInForce
	OrderStatus    order.OrderStatus
	Quantity       decimal.Quantity
	FilledQuantity decimal.Quantity
	TsAccepted     uint64
	TsLast         uint64
	TsInit         uint64
	ReportID       string
}

// OrderStatusReport represents an order's venue-reported state.
type OrderStatusReport struct {
	AccountID          ids.AccountID
	InstrumentID       ids.InstrumentID
	ClientOrderID      *ids.ClientOrderID
	VenueOrderID       ids.VenueOrderID
	OrderSide          order.OrderSide
	OrderType          order.OrderType
	TimeInForce        order.TimeInForce
	OrderStatus        order.OrderStatus
	Quantity           decimal.Quantity
	FilledQuantity     decimal.Quantity
	ReportID           string
	TsAccepted         uint64
	TsLast             uint64
	TsInit             uint64
	OrderListID        *ids.OrderListID
	VenuePositionID    *ids.PositionID
	LinkedOrderIDs     []ids.ClientOrderID
	ParentOrderID      *ids.ClientOrderID
	ContingencyType    order.ContingencyType
	ExpireTime         *uint64
	Price              *decimal.Price
	ActivationPrice    *decimal.Price
	TriggerPrice       *decimal.Price
	TriggerType        *order.TriggerType
	LimitOffset        *decimal.Decimal
	TrailingOffset     *decimal.Decimal
	TrailingOffsetType TrailingOffsetType
	AveragePrice       *decimal.Decimal
	DisplayQuantity    *decimal.Quantity
	PostOnly           bool
	ReduceOnly         bool
	CancelReason       *string
	TsTriggered        *uint64
}

func NewOrderStatusReport(config OrderStatusReportConfig) OrderStatusReport {
	reportID := config.ReportID
	if reportID == "" {
		reportID = "00000000-0000-4000-8000-000000000001"
	}
	return OrderStatusReport{
		AccountID:      config.AccountID,
		InstrumentID:   config.InstrumentID,
		ClientOrderID:  copyPointer(config.ClientOrderID),
		VenueOrderID:   config.VenueOrderID,
		OrderSide:      config.OrderSide,
		OrderType:      config.OrderType,
		TimeInForce:    config.TimeInForce,
		OrderStatus:    config.OrderStatus,
		Quantity:       config.Quantity,
		FilledQuantity: config.FilledQuantity,
		ReportID:       reportID,
		TsAccepted:     config.TsAccepted,
		TsLast:         config.TsLast,
		TsInit:         config.TsInit,
	}
}

func (r OrderStatusReport) WithClientOrderID(value ids.ClientOrderID) OrderStatusReport {
	r.ClientOrderID = &value
	return r
}
func (r OrderStatusReport) WithOrderListID(value ids.OrderListID) OrderStatusReport {
	r.OrderListID = &value
	return r
}
func (r OrderStatusReport) WithVenuePositionID(value ids.PositionID) OrderStatusReport {
	r.VenuePositionID = &value
	return r
}
func (r OrderStatusReport) WithLinkedOrderIDs(values []ids.ClientOrderID) OrderStatusReport {
	r.LinkedOrderIDs = append([]ids.ClientOrderID(nil), values...)
	return r
}
func (r OrderStatusReport) WithParentOrderID(value ids.ClientOrderID) OrderStatusReport {
	r.ParentOrderID = &value
	return r
}
func (r OrderStatusReport) WithPrice(value decimal.Price) OrderStatusReport {
	r.Price = &value
	return r
}
func (r OrderStatusReport) WithAveragePrice(value decimal.Decimal) OrderStatusReport {
	r.AveragePrice = &value
	return r
}
func (r OrderStatusReport) WithActivationPrice(value decimal.Price) OrderStatusReport {
	r.ActivationPrice = &value
	return r
}
func (r OrderStatusReport) WithTriggerPrice(value decimal.Price) OrderStatusReport {
	r.TriggerPrice = &value
	return r
}
func (r OrderStatusReport) WithTriggerType(value order.TriggerType) OrderStatusReport {
	r.TriggerType = &value
	return r
}
func (r OrderStatusReport) WithLimitOffset(value decimal.Decimal) OrderStatusReport {
	r.LimitOffset = &value
	return r
}
func (r OrderStatusReport) WithTrailingOffset(value decimal.Decimal) OrderStatusReport {
	r.TrailingOffset = &value
	return r
}
func (r OrderStatusReport) WithTrailingOffsetType(value TrailingOffsetType) OrderStatusReport {
	r.TrailingOffsetType = value
	return r
}
func (r OrderStatusReport) WithDisplayQuantity(value decimal.Quantity) OrderStatusReport {
	r.DisplayQuantity = &value
	return r
}
func (r OrderStatusReport) WithExpireTime(value uint64) OrderStatusReport {
	r.ExpireTime = &value
	return r
}
func (r OrderStatusReport) WithPostOnly(value bool) OrderStatusReport {
	r.PostOnly = value
	return r
}
func (r OrderStatusReport) WithReduceOnly(value bool) OrderStatusReport {
	r.ReduceOnly = value
	return r
}
func (r OrderStatusReport) WithCancelReason(value string) OrderStatusReport {
	r.CancelReason = &value
	return r
}
func (r OrderStatusReport) WithTsTriggered(value uint64) OrderStatusReport {
	r.TsTriggered = &value
	return r
}
func (r OrderStatusReport) WithContingencyType(value order.ContingencyType) OrderStatusReport {
	r.ContingencyType = value
	return r
}

func (r OrderStatusReport) Clone() OrderStatusReport {
	result := r
	result.ClientOrderID = copyPointer(r.ClientOrderID)
	result.OrderListID = copyPointer(r.OrderListID)
	result.VenuePositionID = copyPointer(r.VenuePositionID)
	result.LinkedOrderIDs = append([]ids.ClientOrderID(nil), r.LinkedOrderIDs...)
	result.ParentOrderID = copyPointer(r.ParentOrderID)
	result.ExpireTime = copyPointer(r.ExpireTime)
	result.Price = copyPointer(r.Price)
	result.ActivationPrice = copyPointer(r.ActivationPrice)
	result.TriggerPrice = copyPointer(r.TriggerPrice)
	result.TriggerType = copyPointer(r.TriggerType)
	result.LimitOffset = copyPointer(r.LimitOffset)
	result.TrailingOffset = copyPointer(r.TrailingOffset)
	result.AveragePrice = copyPointer(r.AveragePrice)
	result.DisplayQuantity = copyPointer(r.DisplayQuantity)
	result.CancelReason = copyPointer(r.CancelReason)
	result.TsTriggered = copyPointer(r.TsTriggered)
	return result
}

func (r OrderStatusReport) Equal(other OrderStatusReport) bool {
	return reflect.DeepEqual(r, other)
}

func (r OrderStatusReport) String() string {
	return fmt.Sprintf(
		"OrderStatusReport(account_id=%s, instrument_id=%s, venue_order_id=%s, order_side=%s, order_type=%s, time_in_force=%s, order_status=%s, quantity=%s, filled_qty=%s, report_id=%s, ts_accepted=%d, ts_last=%d, ts_init=%d)",
		r.AccountID,
		r.InstrumentID,
		r.VenueOrderID,
		r.OrderSide,
		r.OrderType,
		r.TimeInForce,
		r.OrderStatus,
		r.Quantity,
		r.FilledQuantity,
		r.ReportID,
		r.TsAccepted,
		r.TsLast,
		r.TsInit,
	)
}

type OrderSnapshot struct {
	Quantity     decimal.Quantity
	Price        *decimal.Price
	TriggerPrice *decimal.Price
}

func (r OrderStatusReport) IsOrderUpdated(value OrderSnapshot) bool {
	if value.Price != nil && r.Price != nil && !value.Price.Equal(*r.Price) {
		return true
	}
	if value.TriggerPrice != nil && r.TriggerPrice != nil &&
		!value.TriggerPrice.Equal(*r.TriggerPrice) {
		return true
	}
	return !value.Quantity.Equal(r.Quantity)
}

func copyPointer[T any](value *T) *T {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}
