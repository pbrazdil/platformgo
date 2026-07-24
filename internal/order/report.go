package order

import (
	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
)

type OwnBookOrder struct {
	Size               decimal.Quantity
	TimestampSubmitted uint64
	TimestampAccepted  uint64
	TimestampLast      uint64
}

func (o *Core) ToOwnBookOrder() OwnBookOrder {
	return OwnBookOrder{
		Size:               o.LeavesQuantity(),
		TimestampSubmitted: o.tsSubmitted,
		TimestampAccepted:  o.tsAccepted,
		TimestampLast:      o.tsLast,
	}
}

type StatusReport struct {
	ReportID           string
	AccountID          ids.AccountID
	InstrumentID       ids.InstrumentID
	ClientOrderID      *ids.ClientOrderID
	VenueOrderID       ids.VenueOrderID
	VenuePositionID    *ids.PositionID
	OrderSide          OrderSide
	OrderType          OrderType
	TimeInForce        TimeInForce
	OrderStatus        OrderStatus
	Quantity           decimal.Quantity
	FilledQuantity     decimal.Quantity
	Price              *decimal.Price
	AveragePrice       *decimal.Decimal
	TriggerPrice       *decimal.Price
	TriggerType        *TriggerType
	ExpireTime         *uint64
	DisplayQuantity    *decimal.Quantity
	PostOnly           bool
	ReduceOnly         bool
	ContingencyType    ContingencyType
	OrderListID        *ids.OrderListID
	LinkedOrderIDs     []ids.ClientOrderID
	ParentOrderID      *ids.ClientOrderID
	ActivationPrice    *decimal.Price
	LimitOffset        *decimal.Decimal
	TrailingOffset     *decimal.Decimal
	TrailingOffsetType TrailingOffsetType
	CancelReason       string
	TimestampTriggered *uint64
	TimestampAccepted  uint64
	TimestampLast      uint64
	TimestampInit      uint64
}

func (o *Core) ToStatusReport(reportID string) *StatusReport {
	if o.accountID == nil || o.venueOrderID == nil {
		return nil
	}
	accepted := o.tsAccepted
	if accepted == 0 {
		accepted = o.tsLast
	}
	var triggered *uint64
	if o.tsTriggered != 0 {
		triggered = copyPointer(o.tsTriggered)
	}
	var triggerType *TriggerType
	if o.config.TriggerType != TriggerTypeNoTrigger {
		triggerType = copyPointer(o.config.TriggerType)
	}
	return &StatusReport{
		ReportID:           reportID,
		AccountID:          *o.accountID,
		InstrumentID:       o.config.InstrumentID,
		ClientOrderID:      copyPointer(o.config.ClientOrderID),
		VenueOrderID:       *o.venueOrderID,
		VenuePositionID:    copyPointerValue(o.venuePositionID),
		OrderSide:          o.config.Side,
		OrderType:          o.config.Type,
		TimeInForce:        o.config.TimeInForce,
		OrderStatus:        o.status,
		Quantity:           o.config.Quantity,
		FilledQuantity:     o.filledQuantity,
		Price:              copyPointerValue(o.config.Price),
		AveragePrice:       copyDecimal(o.averagePrice),
		TriggerPrice:       copyPointerValue(o.config.TriggerPrice),
		TriggerType:        triggerType,
		ExpireTime:         copyPointerValue(o.config.ExpireTime),
		DisplayQuantity:    copyPointerValue(o.config.DisplayQuantity),
		PostOnly:           o.config.PostOnly,
		ReduceOnly:         o.config.ReduceOnly,
		ContingencyType:    o.config.ContingencyType,
		OrderListID:        copyPointerValue(o.config.OrderListID),
		LinkedOrderIDs:     append([]ids.ClientOrderID(nil), o.config.LinkedOrderIDs...),
		ParentOrderID:      copyPointerValue(o.config.ParentOrderID),
		ActivationPrice:    copyPointerValue(o.config.ActivationPrice),
		LimitOffset:        copyDecimal(o.config.LimitOffset),
		TrailingOffset:     copyDecimal(o.config.TrailingOffset),
		TrailingOffsetType: o.config.TrailingOffsetType,
		CancelReason:       o.rejection,
		TimestampTriggered: triggered,
		TimestampAccepted:  accepted,
		TimestampLast:      o.tsLast,
		TimestampInit:      o.config.TimestampInit,
	}
}
