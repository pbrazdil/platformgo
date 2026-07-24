package market

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
)

type MarketStatusAction uint8

const (
	MarketStatusNone MarketStatusAction = iota
	MarketStatusPreOpen
	MarketStatusPreCross
	MarketStatusQuoting
	MarketStatusCross
	MarketStatusRotation
	MarketStatusNewPriceIndication
	MarketStatusTrading
	MarketStatusHalt
	MarketStatusPause
	MarketStatusSuspend
	MarketStatusPreClose
	MarketStatusClose
	MarketStatusPostClose
	MarketStatusShortSellRestrictionChange
	MarketStatusNotAvailableForTrading
)

var marketStatusActionNames = [...]string{
	"NONE",
	"PRE_OPEN",
	"PRE_CROSS",
	"QUOTING",
	"CROSS",
	"ROTATION",
	"NEW_PRICE_INDICATION",
	"TRADING",
	"HALT",
	"PAUSE",
	"SUSPEND",
	"PRE_CLOSE",
	"CLOSE",
	"POST_CLOSE",
	"SHORT_SELL_RESTRICTION_CHANGE",
	"NOT_AVAILABLE_FOR_TRADING",
}

func (action MarketStatusAction) String() string {
	if int(action) < len(marketStatusActionNames) {
		return marketStatusActionNames[action]
	}
	return fmt.Sprintf("MarketStatusAction(%d)", action)
}

func (action MarketStatusAction) debugString() string {
	names := [...]string{
		"None", "PreOpen", "PreCross", "Quoting", "Cross", "Rotation",
		"NewPriceIndication", "Trading", "Halt", "Pause", "Suspend",
		"PreClose", "Close", "PostClose", "ShortSellRestrictionChange",
		"NotAvailableForTrading",
	}
	if int(action) < len(names) {
		return names[action]
	}
	return action.String()
}

func (action MarketStatusAction) MarshalJSON() ([]byte, error) {
	return json.Marshal(action.String())
}

func (action *MarketStatusAction) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	for candidate, name := range marketStatusActionNames {
		if value == name {
			*action = MarketStatusAction(candidate)
			return nil
		}
	}
	return fmt.Errorf("invalid market status action %q", value)
}

// InstrumentStatus represents a change in an instrument's market status.
type InstrumentStatus struct {
	InstrumentID          InstrumentID       `json:"instrument_id"`
	Action                MarketStatusAction `json:"action"`
	TsEvent               UnixNanos          `json:"ts_event"`
	TsInit                UnixNanos          `json:"ts_init"`
	Reason                *string            `json:"reason"`
	TradingEvent          *string            `json:"trading_event"`
	IsTrading             *bool              `json:"is_trading"`
	IsQuoting             *bool              `json:"is_quoting"`
	IsShortSellRestricted *bool              `json:"is_short_sell_restricted"`
}

func NewInstrumentStatus(
	instrumentID InstrumentID,
	action MarketStatusAction,
	tsEvent, tsInit UnixNanos,
	reason, tradingEvent *string,
	isTrading, isQuoting, isShortSellRestricted *bool,
) InstrumentStatus {
	return InstrumentStatus{
		InstrumentID:          instrumentID,
		Action:                action,
		TsEvent:               tsEvent,
		TsInit:                tsInit,
		Reason:                reason,
		TradingEvent:          tradingEvent,
		IsTrading:             isTrading,
		IsQuoting:             isQuoting,
		IsShortSellRestricted: isShortSellRestricted,
	}
}

func InstrumentStatusMetadata(instrumentID InstrumentID) map[string]string {
	return map[string]string{"instrument_id": string(instrumentID)}
}

func (status InstrumentStatus) String() string {
	return fmt.Sprintf(
		"%s,%s,%d,%d",
		status.InstrumentID,
		status.Action,
		status.TsEvent,
		status.TsInit,
	)
}

func (status InstrumentStatus) DebugString() string {
	return fmt.Sprintf(
		"InstrumentStatus { instrument_id: %q, action: %s, ts_event: %d, ts_init: %d, reason: %s, trading_event: %s, is_trading: %s, is_quoting: %s, is_short_sell_restricted: %s }",
		status.InstrumentID,
		status.Action.debugString(),
		status.TsEvent,
		status.TsInit,
		debugOptionalString(status.Reason),
		debugOptionalString(status.TradingEvent),
		debugOptionalBool(status.IsTrading),
		debugOptionalBool(status.IsQuoting),
		debugOptionalBool(status.IsShortSellRestricted),
	)
}

func (status InstrumentStatus) Equal(other InstrumentStatus) bool {
	return status.InstrumentID == other.InstrumentID &&
		status.Action == other.Action &&
		status.TsEvent == other.TsEvent &&
		status.TsInit == other.TsInit &&
		equalOptionalString(status.Reason, other.Reason) &&
		equalOptionalString(status.TradingEvent, other.TradingEvent) &&
		equalOptionalBool(status.IsTrading, other.IsTrading) &&
		equalOptionalBool(status.IsQuoting, other.IsQuoting) &&
		equalOptionalBool(status.IsShortSellRestricted, other.IsShortSellRestricted)
}

func (status InstrumentStatus) Hash() uint64 {
	return hashStrings(
		string(status.InstrumentID),
		strconv.Itoa(int(status.Action)),
		strconv.FormatUint(uint64(status.TsEvent), 10),
		strconv.FormatUint(uint64(status.TsInit), 10),
		debugOptionalString(status.Reason),
		debugOptionalString(status.TradingEvent),
		debugOptionalBool(status.IsTrading),
		debugOptionalBool(status.IsQuoting),
		debugOptionalBool(status.IsShortSellRestricted),
	)
}

func (status InstrumentStatus) TimestampInit() UnixNanos { return status.TsInit }

func (status InstrumentStatus) MarshalJSON() ([]byte, error) {
	type wire InstrumentStatus
	return json.Marshal(struct {
		Type string `json:"type"`
		wire
	}{Type: "InstrumentStatus", wire: wire(status)})
}

func (status *InstrumentStatus) UnmarshalJSON(data []byte) error {
	type wire InstrumentStatus
	var decoded struct {
		Type string `json:"type"`
		wire
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	if decoded.Type != "" && decoded.Type != "InstrumentStatus" {
		return fmt.Errorf("invalid market data type %q", decoded.Type)
	}
	*status = InstrumentStatus(decoded.wire)
	return nil
}

// Data is the minimal native tagged union required by the status conversion contract.
type Data struct {
	Status *InstrumentStatus
	Close  *InstrumentClose
}

func DataFromInstrumentStatus(status InstrumentStatus) Data {
	return Data{Status: &status}
}

func (data Data) InstrumentID() InstrumentID {
	switch {
	case data.Status != nil:
		return data.Status.InstrumentID
	case data.Close != nil:
		return data.Close.InstrumentID
	default:
		return ""
	}
}

func (data Data) TimestampInit() UnixNanos {
	switch {
	case data.Status != nil:
		return data.Status.TsInit
	case data.Close != nil:
		return data.Close.TsInit
	default:
		return 0
	}
}

func (data Data) Equal(other Data) bool {
	switch {
	case data.Status != nil && other.Status != nil:
		return data.Status.Equal(*other.Status)
	case data.Close != nil && other.Close != nil:
		return data.Close.Equal(*other.Close)
	default:
		return data.Status == nil && data.Close == nil &&
			other.Status == nil && other.Close == nil
	}
}

func (data Data) InstrumentStatus() (InstrumentStatus, error) {
	if data.Status == nil {
		return InstrumentStatus{}, errors.New("data is not InstrumentStatus")
	}
	return *data.Status, nil
}

func (data Data) ToFFI() error {
	if data.Status != nil {
		return errors.New("Cannot convert Data::InstrumentStatus to DataFFI")
	}
	return nil
}

func (data Data) MarshalJSON() ([]byte, error) {
	switch {
	case data.Status != nil:
		return json.Marshal(data.Status)
	case data.Close != nil:
		return json.Marshal(data.Close)
	default:
		return nil, errors.New("empty market data")
	}
}

func (data *Data) UnmarshalJSON(payload []byte) error {
	var header struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(payload, &header); err != nil {
		return err
	}
	switch header.Type {
	case "InstrumentStatus":
		var status InstrumentStatus
		if err := json.Unmarshal(payload, &status); err != nil {
			return err
		}
		*data = DataFromInstrumentStatus(status)
		return nil
	case "InstrumentClose":
		var close InstrumentClose
		if err := json.Unmarshal(payload, &close); err != nil {
			return err
		}
		*data = Data{Close: &close}
		return nil
	default:
		return fmt.Errorf("unknown market data type %q", header.Type)
	}
}

func equalOptionalString(left, right *string) bool {
	return left == nil && right == nil ||
		left != nil && right != nil && *left == *right
}

func equalOptionalBool(left, right *bool) bool {
	return left == nil && right == nil ||
		left != nil && right != nil && *left == *right
}

func debugOptionalString(value *string) string {
	if value == nil {
		return "None"
	}
	return fmt.Sprintf("Some(%q)", *value)
}

func debugOptionalBool(value *bool) string {
	if value == nil {
		return "None"
	}
	return fmt.Sprintf("Some(%t)", *value)
}
