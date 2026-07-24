package order

import "fmt"

type ContingencyType uint8

const (
	ContingencyTypeNoContingency ContingencyType = iota
	ContingencyTypeOCO
	ContingencyTypeOTO
	ContingencyTypeOUO
)

func (v ContingencyType) String() string {
	switch v {
	case ContingencyTypeNoContingency:
		return "NO_CONTINGENCY"
	case ContingencyTypeOCO:
		return "OCO"
	case ContingencyTypeOTO:
		return "OTO"
	case ContingencyTypeOUO:
		return "OUO"
	default:
		return fmt.Sprintf("ContingencyType(%d)", v)
	}
}

func ParseContingencyType(value string) (ContingencyType, error) {
	switch value {
	case "NO_CONTINGENCY":
		return ContingencyTypeNoContingency, nil
	case "OCO":
		return ContingencyTypeOCO, nil
	case "OTO":
		return ContingencyTypeOTO, nil
	case "OUO":
		return ContingencyTypeOUO, nil
	default:
		return 0, invalidEnumValue("ContingencyType", value)
	}
}

type LiquiditySide uint8

const (
	LiquiditySideNoLiquiditySide LiquiditySide = iota
	LiquiditySideMaker
	LiquiditySideTaker
)

func (v LiquiditySide) String() string {
	switch v {
	case LiquiditySideNoLiquiditySide:
		return "NO_LIQUIDITY_SIDE"
	case LiquiditySideMaker:
		return "MAKER"
	case LiquiditySideTaker:
		return "TAKER"
	default:
		return fmt.Sprintf("LiquiditySide(%d)", v)
	}
}

func ParseLiquiditySide(value string) (LiquiditySide, error) {
	switch value {
	case "NO_LIQUIDITY_SIDE":
		return LiquiditySideNoLiquiditySide, nil
	case "MAKER":
		return LiquiditySideMaker, nil
	case "TAKER":
		return LiquiditySideTaker, nil
	default:
		return 0, invalidEnumValue("LiquiditySide", value)
	}
}

type OrderSide uint8

const (
	OrderSideNoOrderSide OrderSide = iota
	OrderSideBuy
	OrderSideSell
)

func (v OrderSide) String() string {
	switch v {
	case OrderSideNoOrderSide:
		return "NO_ORDER_SIDE"
	case OrderSideBuy:
		return "BUY"
	case OrderSideSell:
		return "SELL"
	default:
		return fmt.Sprintf("OrderSide(%d)", v)
	}
}

func ParseOrderSide(value string) (OrderSide, error) {
	switch value {
	case "NO_ORDER_SIDE":
		return OrderSideNoOrderSide, nil
	case "BUY":
		return OrderSideBuy, nil
	case "SELL":
		return OrderSideSell, nil
	default:
		return 0, invalidEnumValue("OrderSide", value)
	}
}

func (v OrderSide) Opposite() OrderSide {
	switch v {
	case OrderSideBuy:
		return OrderSideSell
	case OrderSideSell:
		return OrderSideBuy
	default:
		return v
	}
}

type OrderStatus uint8

const (
	OrderStatusInitialized     OrderStatus = 1
	OrderStatusDenied          OrderStatus = 2
	OrderStatusSubmitted       OrderStatus = 5
	OrderStatusAccepted        OrderStatus = 6
	OrderStatusRejected        OrderStatus = 7
	OrderStatusCanceled        OrderStatus = 8
	OrderStatusExpired         OrderStatus = 9
	OrderStatusTriggered       OrderStatus = 10
	OrderStatusPendingUpdate   OrderStatus = 11
	OrderStatusPendingCancel   OrderStatus = 12
	OrderStatusPartiallyFilled OrderStatus = 13
	OrderStatusFilled          OrderStatus = 14
)

func (v OrderStatus) String() string {
	switch v {
	case OrderStatusInitialized:
		return "INITIALIZED"
	case OrderStatusDenied:
		return "DENIED"
	case OrderStatusSubmitted:
		return "SUBMITTED"
	case OrderStatusAccepted:
		return "ACCEPTED"
	case OrderStatusRejected:
		return "REJECTED"
	case OrderStatusCanceled:
		return "CANCELED"
	case OrderStatusExpired:
		return "EXPIRED"
	case OrderStatusTriggered:
		return "TRIGGERED"
	case OrderStatusPendingUpdate:
		return "PENDING_UPDATE"
	case OrderStatusPendingCancel:
		return "PENDING_CANCEL"
	case OrderStatusPartiallyFilled:
		return "PARTIALLY_FILLED"
	case OrderStatusFilled:
		return "FILLED"
	default:
		return fmt.Sprintf("OrderStatus(%d)", v)
	}
}

func ParseOrderStatus(value string) (OrderStatus, error) {
	switch value {
	case "INITIALIZED":
		return OrderStatusInitialized, nil
	case "DENIED":
		return OrderStatusDenied, nil
	case "SUBMITTED":
		return OrderStatusSubmitted, nil
	case "ACCEPTED":
		return OrderStatusAccepted, nil
	case "REJECTED":
		return OrderStatusRejected, nil
	case "CANCELED":
		return OrderStatusCanceled, nil
	case "EXPIRED":
		return OrderStatusExpired, nil
	case "TRIGGERED":
		return OrderStatusTriggered, nil
	case "PENDING_UPDATE":
		return OrderStatusPendingUpdate, nil
	case "PENDING_CANCEL":
		return OrderStatusPendingCancel, nil
	case "PARTIALLY_FILLED":
		return OrderStatusPartiallyFilled, nil
	case "FILLED":
		return OrderStatusFilled, nil
	default:
		return 0, invalidEnumValue("OrderStatus", value)
	}
}

type OrderType uint8

const (
	OrderTypeMarket OrderType = iota + 1
	OrderTypeLimit
	OrderTypeStopMarket
	OrderTypeStopLimit
	OrderTypeMarketToLimit
	OrderTypeMarketIfTouched
	OrderTypeLimitIfTouched
	OrderTypeTrailingStopMarket
	OrderTypeTrailingStopLimit
)

func (v OrderType) String() string {
	switch v {
	case OrderTypeMarket:
		return "MARKET"
	case OrderTypeLimit:
		return "LIMIT"
	case OrderTypeStopMarket:
		return "STOP_MARKET"
	case OrderTypeStopLimit:
		return "STOP_LIMIT"
	case OrderTypeMarketToLimit:
		return "MARKET_TO_LIMIT"
	case OrderTypeMarketIfTouched:
		return "MARKET_IF_TOUCHED"
	case OrderTypeLimitIfTouched:
		return "LIMIT_IF_TOUCHED"
	case OrderTypeTrailingStopMarket:
		return "TRAILING_STOP_MARKET"
	case OrderTypeTrailingStopLimit:
		return "TRAILING_STOP_LIMIT"
	default:
		return fmt.Sprintf("OrderType(%d)", v)
	}
}

func ParseOrderType(value string) (OrderType, error) {
	switch value {
	case "MARKET":
		return OrderTypeMarket, nil
	case "LIMIT":
		return OrderTypeLimit, nil
	case "STOP_MARKET":
		return OrderTypeStopMarket, nil
	case "STOP_LIMIT":
		return OrderTypeStopLimit, nil
	case "MARKET_TO_LIMIT":
		return OrderTypeMarketToLimit, nil
	case "MARKET_IF_TOUCHED":
		return OrderTypeMarketIfTouched, nil
	case "LIMIT_IF_TOUCHED":
		return OrderTypeLimitIfTouched, nil
	case "TRAILING_STOP_MARKET":
		return OrderTypeTrailingStopMarket, nil
	case "TRAILING_STOP_LIMIT":
		return OrderTypeTrailingStopLimit, nil
	default:
		return 0, invalidEnumValue("OrderType", value)
	}
}

type PositionSide uint8

const (
	PositionSideNoPositionSide PositionSide = iota
	PositionSideFlat
	PositionSideLong
	PositionSideShort
)

func (v PositionSide) String() string {
	switch v {
	case PositionSideNoPositionSide:
		return "NO_POSITION_SIDE"
	case PositionSideFlat:
		return "FLAT"
	case PositionSideLong:
		return "LONG"
	case PositionSideShort:
		return "SHORT"
	default:
		return fmt.Sprintf("PositionSide(%d)", v)
	}
}

func ParsePositionSide(value string) (PositionSide, error) {
	switch value {
	case "NO_POSITION_SIDE":
		return PositionSideNoPositionSide, nil
	case "FLAT":
		return PositionSideFlat, nil
	case "LONG":
		return PositionSideLong, nil
	case "SHORT":
		return PositionSideShort, nil
	default:
		return 0, invalidEnumValue("PositionSide", value)
	}
}

type TimeInForce uint8

const (
	TimeInForceGTC TimeInForce = iota + 1
	TimeInForceIOC
	TimeInForceFOK
	TimeInForceGTD
	TimeInForceDay
	TimeInForceAtTheOpen
	TimeInForceAtTheClose
)

func (v TimeInForce) String() string {
	switch v {
	case TimeInForceGTC:
		return "GTC"
	case TimeInForceIOC:
		return "IOC"
	case TimeInForceFOK:
		return "FOK"
	case TimeInForceGTD:
		return "GTD"
	case TimeInForceDay:
		return "DAY"
	case TimeInForceAtTheOpen:
		return "AT_THE_OPEN"
	case TimeInForceAtTheClose:
		return "AT_THE_CLOSE"
	default:
		return fmt.Sprintf("TimeInForce(%d)", v)
	}
}

func ParseTimeInForce(value string) (TimeInForce, error) {
	switch value {
	case "GTC":
		return TimeInForceGTC, nil
	case "IOC":
		return TimeInForceIOC, nil
	case "FOK":
		return TimeInForceFOK, nil
	case "GTD":
		return TimeInForceGTD, nil
	case "DAY":
		return TimeInForceDay, nil
	case "AT_THE_OPEN":
		return TimeInForceAtTheOpen, nil
	case "AT_THE_CLOSE":
		return TimeInForceAtTheClose, nil
	default:
		return 0, invalidEnumValue("TimeInForce", value)
	}
}

type TriggerType uint8

const (
	TriggerTypeNoTrigger TriggerType = iota
	TriggerTypeDefault
	TriggerTypeLastPrice
	TriggerTypeMarkPrice
	TriggerTypeIndexPrice
	TriggerTypeBidAsk
	TriggerTypeDoubleLast
	TriggerTypeDoubleBidAsk
	TriggerTypeLastOrBidAsk
	TriggerTypeMidPoint
)

func (v TriggerType) String() string {
	switch v {
	case TriggerTypeNoTrigger:
		return "NO_TRIGGER"
	case TriggerTypeDefault:
		return "DEFAULT"
	case TriggerTypeLastPrice:
		return "LAST_PRICE"
	case TriggerTypeMarkPrice:
		return "MARK_PRICE"
	case TriggerTypeIndexPrice:
		return "INDEX_PRICE"
	case TriggerTypeBidAsk:
		return "BID_ASK"
	case TriggerTypeDoubleLast:
		return "DOUBLE_LAST"
	case TriggerTypeDoubleBidAsk:
		return "DOUBLE_BID_ASK"
	case TriggerTypeLastOrBidAsk:
		return "LAST_OR_BID_ASK"
	case TriggerTypeMidPoint:
		return "MID_POINT"
	default:
		return fmt.Sprintf("TriggerType(%d)", v)
	}
}

func ParseTriggerType(value string) (TriggerType, error) {
	switch value {
	case "NO_TRIGGER":
		return TriggerTypeNoTrigger, nil
	case "DEFAULT":
		return TriggerTypeDefault, nil
	case "LAST_PRICE":
		return TriggerTypeLastPrice, nil
	case "MARK_PRICE":
		return TriggerTypeMarkPrice, nil
	case "INDEX_PRICE":
		return TriggerTypeIndexPrice, nil
	case "BID_ASK":
		return TriggerTypeBidAsk, nil
	case "DOUBLE_LAST":
		return TriggerTypeDoubleLast, nil
	case "DOUBLE_BID_ASK":
		return TriggerTypeDoubleBidAsk, nil
	case "LAST_OR_BID_ASK":
		return TriggerTypeLastOrBidAsk, nil
	case "MID_POINT":
		return TriggerTypeMidPoint, nil
	default:
		return 0, invalidEnumValue("TriggerType", value)
	}
}

func invalidEnumValue(typeName, value string) error {
	return fmt.Errorf("invalid %s %q", typeName, value)
}
