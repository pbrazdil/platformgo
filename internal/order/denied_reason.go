package order

import (
	"fmt"
	"slices"
	"strings"

	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
	"github.com/upcomers-org/platformgo/internal/money"
)

type OrderDeniedCode string

const (
	DeniedQuantityExceedsMaximum          OrderDeniedCode = "QUANTITY_EXCEEDS_MAXIMUM"
	DeniedQuantityBelowMinimum            OrderDeniedCode = "QUANTITY_BELOW_MINIMUM"
	DeniedNotionalExceedsMaxPerOrder      OrderDeniedCode = "NOTIONAL_EXCEEDS_MAX_PER_ORDER"
	DeniedNotionalExceedsMaximum          OrderDeniedCode = "NOTIONAL_EXCEEDS_MAXIMUM"
	DeniedNotionalBelowMinimum            OrderDeniedCode = "NOTIONAL_BELOW_MINIMUM"
	DeniedNotionalExceedsFreeBalance      OrderDeniedCode = "NOTIONAL_EXCEEDS_FREE_BALANCE"
	DeniedCumNotionalExceedsFreeBalance   OrderDeniedCode = "CUM_NOTIONAL_EXCEEDS_FREE_BALANCE"
	DeniedMarginExceedsFreeBalance        OrderDeniedCode = "MARGIN_EXCEEDS_FREE_BALANCE"
	DeniedCumMarginExceedsFreeBalance     OrderDeniedCode = "CUM_MARGIN_EXCEEDS_FREE_BALANCE"
	DeniedInvalidMaxNotionalPerOrder      OrderDeniedCode = "INVALID_MAX_NOTIONAL_PER_ORDER"
	DeniedInvalidOrderSide                OrderDeniedCode = "INVALID_ORDER_SIDE"
	DeniedMissingExpireTime               OrderDeniedCode = "MISSING_EXPIRE_TIME"
	DeniedExpireTimeInPast                OrderDeniedCode = "EXPIRE_TIME_IN_PAST"
	DeniedMissingTriggerType              OrderDeniedCode = "MISSING_TRIGGER_TYPE"
	DeniedMissingTrailingOffset           OrderDeniedCode = "MISSING_TRAILING_OFFSET"
	DeniedMissingTrailingOffsetType       OrderDeniedCode = "MISSING_TRAILING_OFFSET_TYPE"
	DeniedUnsupportedTrailingOffsetType   OrderDeniedCode = "UNSUPPORTED_TRAILING_OFFSET_TYPE"
	DeniedTrailingStopCalcFailed          OrderDeniedCode = "TRAILING_STOP_CALC_FAILED"
	DeniedQuantityConversionFailed        OrderDeniedCode = "QUANTITY_CONVERSION_FAILED"
	DeniedInstrumentNotFound              OrderDeniedCode = "INSTRUMENT_NOT_FOUND"
	DeniedPositionNotFound                OrderDeniedCode = "POSITION_NOT_FOUND"
	DeniedReduceOnlyWouldIncreasePosition OrderDeniedCode = "REDUCE_ONLY_WOULD_INCREASE_POSITION"
	DeniedOrderListIncomplete             OrderDeniedCode = "ORDER_LIST_INCOMPLETE"
	DeniedOrderListDenied                 OrderDeniedCode = "ORDER_LIST_DENIED"
	DeniedTradingHalted                   OrderDeniedCode = "TRADING_HALTED"
	DeniedTradingStateReducing            OrderDeniedCode = "TRADING_STATE_REDUCING"
	DeniedRateLimitExceeded               OrderDeniedCode = "RATE_LIMIT_EXCEEDED"
	DeniedNoExecutionClient               OrderDeniedCode = "NO_EXECUTION_CLIENT"
	DeniedClientVenueMismatch             OrderDeniedCode = "CLIENT_VENUE_MISMATCH"
	DeniedSubmitFailed                    OrderDeniedCode = "SUBMIT_FAILED"
	DeniedInvalidPositionID               OrderDeniedCode = "INVALID_POSITION_ID"
	DeniedUnsupportedTimeInForce          OrderDeniedCode = "UNSUPPORTED_TIME_IN_FORCE"
	DeniedInvalidClientOrderID            OrderDeniedCode = "INVALID_CLIENT_ORDER_ID"
	DeniedUnsupportedOrderList            OrderDeniedCode = "UNSUPPORTED_ORDER_LIST"
	DeniedUnsupportedOrderType            OrderDeniedCode = "UNSUPPORTED_ORDER_TYPE"
	DeniedUnsupportedTPSL                 OrderDeniedCode = "UNSUPPORTED_TP_SL"
	DeniedValidationFailed                OrderDeniedCode = "VALIDATION_FAILED"
	DeniedStreamReconciling               OrderDeniedCode = "STREAM_RECONCILING"
)

var allOrderDeniedCodes = []OrderDeniedCode{
	DeniedQuantityExceedsMaximum, DeniedQuantityBelowMinimum,
	DeniedNotionalExceedsMaxPerOrder, DeniedNotionalExceedsMaximum,
	DeniedNotionalBelowMinimum, DeniedNotionalExceedsFreeBalance,
	DeniedCumNotionalExceedsFreeBalance, DeniedMarginExceedsFreeBalance,
	DeniedCumMarginExceedsFreeBalance, DeniedInvalidMaxNotionalPerOrder,
	DeniedInvalidOrderSide, DeniedMissingExpireTime, DeniedExpireTimeInPast,
	DeniedMissingTriggerType, DeniedMissingTrailingOffset,
	DeniedMissingTrailingOffsetType, DeniedUnsupportedTrailingOffsetType,
	DeniedTrailingStopCalcFailed, DeniedQuantityConversionFailed,
	DeniedInstrumentNotFound, DeniedPositionNotFound,
	DeniedReduceOnlyWouldIncreasePosition, DeniedOrderListIncomplete,
	DeniedOrderListDenied, DeniedTradingHalted, DeniedTradingStateReducing,
	DeniedRateLimitExceeded, DeniedNoExecutionClient, DeniedClientVenueMismatch,
	DeniedSubmitFailed, DeniedInvalidPositionID, DeniedUnsupportedTimeInForce,
	DeniedInvalidClientOrderID, DeniedUnsupportedOrderList,
	DeniedUnsupportedOrderType, DeniedUnsupportedTPSL, DeniedValidationFailed,
	DeniedStreamReconciling,
}

// OrderDeniedReason carries the typed category plus its category-specific context.
type OrderDeniedReason struct {
	Code               OrderDeniedCode
	EffectiveQuantity  decimal.Quantity
	LimitQuantity      decimal.Quantity
	FirstMoney         money.Money
	SecondMoney        money.Money
	InstrumentID       ids.InstrumentID
	Value              decimal.Decimal
	OrderSide          OrderSide
	Text               string
	PositionID         ids.PositionID
	OrderListID        ids.OrderListID
	ClientID           *ids.ClientID
	RoutingContext     string
	OrderVenue         ids.Venue
	ClientVenue        ids.Venue
	TimeInForce        TimeInForce
	OrderType          OrderType
	TrailingOffsetType TrailingOffsetType
}

func (reason OrderDeniedReason) String() string {
	switch reason.Code {
	case DeniedQuantityExceedsMaximum:
		return fmt.Sprintf("%s: effective_quantity=%s, max_quantity=%s", reason.Code, reason.EffectiveQuantity, reason.LimitQuantity)
	case DeniedQuantityBelowMinimum:
		return fmt.Sprintf("%s: effective_quantity=%s, min_quantity=%s", reason.Code, reason.EffectiveQuantity, reason.LimitQuantity)
	case DeniedNotionalExceedsMaxPerOrder, DeniedNotionalExceedsMaximum:
		return fmt.Sprintf("%s: max_notional=%s, notional=%s", reason.Code, reason.FirstMoney.DebugString(), reason.SecondMoney.DebugString())
	case DeniedNotionalBelowMinimum:
		return fmt.Sprintf("%s: min_notional=%s, notional=%s", reason.Code, reason.FirstMoney.DebugString(), reason.SecondMoney.DebugString())
	case DeniedNotionalExceedsFreeBalance:
		return fmt.Sprintf("%s: free=%s, notional=%s", reason.Code, reason.FirstMoney.DebugString(), reason.SecondMoney.DebugString())
	case DeniedCumNotionalExceedsFreeBalance:
		return fmt.Sprintf("%s: free=%s, cum_notional=%s", reason.Code, reason.FirstMoney, reason.SecondMoney)
	case DeniedMarginExceedsFreeBalance:
		return fmt.Sprintf("%s: free=%s, margin_required=%s", reason.Code, reason.FirstMoney, reason.SecondMoney)
	case DeniedCumMarginExceedsFreeBalance:
		return fmt.Sprintf("%s: free=%s, cum_margin=%s", reason.Code, reason.FirstMoney, reason.SecondMoney)
	case DeniedInvalidMaxNotionalPerOrder:
		return fmt.Sprintf("%s: instrument_id=%s, value=%s", reason.Code, reason.InstrumentID, reason.Value)
	case DeniedInvalidOrderSide:
		return fmt.Sprintf("%s: %s", reason.Code, reason.OrderSide)
	case DeniedMissingExpireTime, DeniedMissingTriggerType, DeniedMissingTrailingOffset,
		DeniedMissingTrailingOffsetType, DeniedTradingHalted, DeniedRateLimitExceeded:
		return string(reason.Code)
	case DeniedExpireTimeInPast:
		return fmt.Sprintf("%s: expire_time=%s", reason.Code, reason.Text)
	case DeniedUnsupportedTrailingOffsetType:
		offset := "None"
		if reason.TrailingOffsetType == TrailingOffsetTypePrice {
			offset = "Price"
		}
		return fmt.Sprintf("%s: %s", reason.Code, offset)
	case DeniedTrailingStopCalcFailed, DeniedQuantityConversionFailed,
		DeniedSubmitFailed, DeniedInvalidClientOrderID, DeniedUnsupportedOrderList,
		DeniedUnsupportedTPSL, DeniedValidationFailed:
		return fmt.Sprintf("%s: %s", reason.Code, reason.Text)
	case DeniedInstrumentNotFound:
		return fmt.Sprintf("%s: instrument_id=%s", reason.Code, reason.InstrumentID)
	case DeniedPositionNotFound, DeniedReduceOnlyWouldIncreasePosition:
		return fmt.Sprintf("%s: position_id=%s", reason.Code, reason.PositionID)
	case DeniedOrderListIncomplete, DeniedOrderListDenied:
		return fmt.Sprintf("%s: order_list_id=%s", reason.Code, reason.OrderListID)
	case DeniedTradingStateReducing:
		return fmt.Sprintf("%s: order_side=%s, instrument_id=%s", reason.Code, reason.OrderSide, reason.InstrumentID)
	case DeniedNoExecutionClient:
		client := "None"
		if reason.ClientID != nil {
			client = fmt.Sprintf("Some(%q)", reason.ClientID.String())
		}
		return fmt.Sprintf("%s: client_id=%s, routing_context=%s", reason.Code, client, reason.RoutingContext)
	case DeniedClientVenueMismatch:
		return fmt.Sprintf("%s: client_id=%s, order_venue=%s, client_venue=%s", reason.Code, reason.ClientID, reason.OrderVenue, reason.ClientVenue)
	case DeniedInvalidPositionID:
		return fmt.Sprintf("%s: position_id=%s, detail=%s", reason.Code, reason.PositionID, reason.Text)
	case DeniedUnsupportedTimeInForce:
		return fmt.Sprintf("%s: %s", reason.Code, reason.TimeInForce)
	case DeniedUnsupportedOrderType:
		return fmt.Sprintf("%s: %s", reason.Code, reason.OrderType)
	case DeniedStreamReconciling:
		return string(reason.Code) + ": post-reconnect reconciliation in progress, retry once it completes"
	default:
		return string(reason.Code)
	}
}

func (code OrderDeniedCode) Description() string {
	return deniedReasonDescriptions[code]
}

var deniedReasonDescriptions = map[OrderDeniedCode]string{
	DeniedQuantityExceedsMaximum:          "The effective order quantity exceeds the instrument maximum.",
	DeniedQuantityBelowMinimum:            "The effective order quantity is below the instrument minimum.",
	DeniedNotionalExceedsMaxPerOrder:      "The order notional exceeds the configured maximum per order.",
	DeniedNotionalExceedsMaximum:          "The order notional exceeds the instrument maximum.",
	DeniedNotionalBelowMinimum:            "The order notional is below the instrument minimum.",
	DeniedNotionalExceedsFreeBalance:      "The order notional exceeds the account free balance.",
	DeniedCumNotionalExceedsFreeBalance:   "The cumulative order notional exceeds the account free balance.",
	DeniedMarginExceedsFreeBalance:        "The order initial margin exceeds the account free balance.",
	DeniedCumMarginExceedsFreeBalance:     "The cumulative initial margin exceeds the account free balance.",
	DeniedInvalidMaxNotionalPerOrder:      "The configured maximum notional per order is invalid.",
	DeniedInvalidOrderSide:                "The order side is invalid for this operation.",
	DeniedMissingExpireTime:               "A GTD order is missing its expire time.",
	DeniedExpireTimeInPast:                "The order's expire time is in the past.",
	DeniedMissingTriggerType:              "The order is missing a required trigger type.",
	DeniedMissingTrailingOffset:           "The order is missing a required trailing offset.",
	DeniedMissingTrailingOffsetType:       "The order is missing a required trailing offset type.",
	DeniedUnsupportedTrailingOffsetType:   "The order's trailing offset type is not supported.",
	DeniedTrailingStopCalcFailed:          "The trailing stop trigger price could not be calculated.",
	DeniedQuantityConversionFailed:        "The order quantity could not be converted for risk checks.",
	DeniedInstrumentNotFound:              "The instrument was not found in the cache.",
	DeniedPositionNotFound:                "The position for a reduce‑only order was not found.",
	DeniedReduceOnlyWouldIncreasePosition: "A reduce‑only order would increase the position.",
	DeniedOrderListIncomplete:             "The order list is missing orders in the cache.",
	DeniedOrderListDenied:                 "The order was denied because its order list failed risk checks.",
	DeniedTradingHalted:                   "Trading is halted; new orders are denied.",
	DeniedTradingStateReducing:            "Trading is reducing; the order would increase exposure.",
	DeniedRateLimitExceeded:               "The order submission rate limit was exceeded.",
	DeniedNoExecutionClient:               "No execution client was found for the routed command.",
	DeniedClientVenueMismatch:             "The execution client does not handle the order venue.",
	DeniedSubmitFailed:                    "Submitting the order to the execution client failed.",
	DeniedInvalidPositionID:               "The supplied position ID is invalid for the order submission.",
	DeniedUnsupportedTimeInForce:          "The order's time in force is not supported.",
	DeniedInvalidClientOrderID:            "The client order ID is invalid for the venue.",
	DeniedUnsupportedOrderList:            "The venue does not support the requested order list.",
	DeniedUnsupportedOrderType:            "The order type is not supported by the venue.",
	DeniedUnsupportedTPSL:                 "The venue does not support the requested take‑profit/stop‑loss parameters.",
	DeniedValidationFailed:                "The order failed adapter validation before submission.",
	DeniedStreamReconciling:               "A post‑reconnect stream reconciliation is in progress; retry once it completes.",
}

const (
	deniedReasonsBlockBegin = "<!-- BEGIN GENERATED: order-denied-reasons -->"
	deniedReasonsBlockEnd   = "<!-- END GENERATED: order-denied-reasons -->"
)

func GeneratedOrderDeniedReasonsBlock() string {
	return deniedReasonsBlockBegin + "\n\n" + OrderDeniedReasonsMarkdownTable() + "\n\n" + deniedReasonsBlockEnd
}

func OrderDeniedReasonsMarkdownTable() string {
	codes := append([]OrderDeniedCode(nil), allOrderDeniedCodes...)
	slices.Sort(codes)
	codeWidth := len("Code")
	descriptionWidth := len("Description")
	for _, code := range codes {
		codeLength := len("`" + string(code) + "`")
		if codeLength > codeWidth {
			codeWidth = codeLength
		}
		if len(code.Description()) > descriptionWidth {
			descriptionWidth = len(code.Description())
		}
	}
	lines := []string{
		fmt.Sprintf("| %-*s | %-*s |", codeWidth, "Code", descriptionWidth, "Description"),
		fmt.Sprintf("| %-*s | %-*s |", codeWidth, strings.Repeat("-", codeWidth), descriptionWidth, strings.Repeat("-", descriptionWidth)),
	}
	for _, code := range codes {
		lines = append(lines, fmt.Sprintf(
			"| %-*s | %-*s |",
			codeWidth,
			"`"+string(code)+"`",
			descriptionWidth,
			code.Description(),
		))
	}
	return strings.Join(lines, "\n")
}

func OrderDeniedReasonsDocumentInSync(document string) bool {
	return strings.Contains(document, GeneratedOrderDeniedReasonsBlock())
}

func RegenerateOrderDeniedReasonsDocument(document string) (string, error) {
	start := strings.Index(document, deniedReasonsBlockBegin)
	end := strings.Index(document, deniedReasonsBlockEnd)
	if start < 0 || end < start {
		return "", fmt.Errorf("order-denied-reasons markers not found")
	}
	end += len(deniedReasonsBlockEnd)
	return document[:start] + GeneratedOrderDeniedReasonsBlock() + document[end:], nil
}
