package position

import (
	"encoding/json"

	"github.com/upcomers-org/platformgo/internal/currency"
	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
	"github.com/upcomers-org/platformgo/internal/money"
)

func NewPositionAdjusted(
	traderID ids.TraderID,
	strategyID ids.StrategyID,
	instrumentID ids.InstrumentID,
	positionID ids.PositionID,
	accountID ids.AccountID,
	adjustmentType AdjustmentType,
	quantityChange *decimal.Decimal,
	pnlChange *money.Money,
	reason *string,
	eventID EventID,
	tsEvent uint64,
	tsInit uint64,
) PositionAdjusted {
	return PositionAdjusted{
		TraderID: traderID, StrategyID: strategyID, InstrumentID: instrumentID,
		PositionID: positionID, AccountID: accountID, AdjustmentType: adjustmentType,
		QuantityChange: cloneDecimal(quantityChange), PnLChange: cloneMoney(pnlChange),
		Reason: cloneString(reason), EventID: eventID, TsEvent: tsEvent, TsInit: tsInit,
	}
}

func (event PositionAdjusted) Equal(other PositionAdjusted) bool {
	return event.TraderID == other.TraderID &&
		event.StrategyID == other.StrategyID &&
		event.InstrumentID == other.InstrumentID &&
		event.PositionID == other.PositionID &&
		event.AccountID == other.AccountID &&
		event.AdjustmentType == other.AdjustmentType &&
		optionalDecimalEqual(event.QuantityChange, other.QuantityChange) &&
		optionalMoneyEqual(event.PnLChange, other.PnLChange) &&
		optionalStringEqual(event.Reason, other.Reason) &&
		event.EventID == other.EventID &&
		event.TsEvent == other.TsEvent &&
		event.TsInit == other.TsInit
}

type positionAdjustedWire struct {
	Type           string  `json:"type"`
	TraderID       string  `json:"trader_id"`
	StrategyID     string  `json:"strategy_id"`
	InstrumentID   string  `json:"instrument_id"`
	PositionID     string  `json:"position_id"`
	AccountID      string  `json:"account_id"`
	AdjustmentType string  `json:"adjustment_type"`
	QuantityChange *string `json:"quantity_change"`
	PnLChange      *string `json:"pnl_change"`
	Reason         *string `json:"reason"`
	EventID        string  `json:"event_id"`
	TsEvent        uint64  `json:"ts_event"`
	TsInit         uint64  `json:"ts_init"`
}

func (event PositionAdjusted) MarshalJSON() ([]byte, error) {
	var quantityChange, pnlChange *string
	if event.QuantityChange != nil {
		value := event.QuantityChange.String()
		quantityChange = &value
	}
	if event.PnLChange != nil {
		value := event.PnLChange.String()
		pnlChange = &value
	}
	return json.Marshal(positionAdjustedWire{
		Type: "PositionAdjusted", TraderID: string(event.TraderID),
		StrategyID: string(event.StrategyID), InstrumentID: event.InstrumentID.String(),
		PositionID: string(event.PositionID), AccountID: string(event.AccountID),
		AdjustmentType: string(event.AdjustmentType), QuantityChange: quantityChange,
		PnLChange: pnlChange, Reason: cloneString(event.Reason), EventID: string(event.EventID),
		TsEvent: event.TsEvent, TsInit: event.TsInit,
	})
}

func (event *PositionAdjusted) UnmarshalJSON(data []byte) error {
	var wire positionAdjustedWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	var quantityChange *decimal.Decimal
	if wire.QuantityChange != nil {
		value, err := decimal.Parse(*wire.QuantityChange)
		if err != nil {
			return err
		}
		quantityChange = &value
	}
	var pnlChange *money.Money
	if wire.PnLChange != nil {
		value, err := money.Parse(*wire.PnLChange, currency.NewDefaultRegistry())
		if err != nil {
			return err
		}
		pnlChange = &value
	}
	*event = NewPositionAdjusted(
		ids.MustTraderID(wire.TraderID), ids.MustStrategyID(wire.StrategyID),
		ids.MustInstrumentID(wire.InstrumentID), ids.MustPositionID(wire.PositionID),
		ids.MustAccountID(wire.AccountID), AdjustmentType(wire.AdjustmentType),
		quantityChange, pnlChange, wire.Reason, EventID(wire.EventID), wire.TsEvent, wire.TsInit,
	)
	return nil
}

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func optionalDecimalEqual(left, right *decimal.Decimal) bool {
	return left == nil && right == nil || left != nil && right != nil && left.Equal(*right)
}

func optionalMoneyEqual(left, right *money.Money) bool {
	return left == nil && right == nil || left != nil && right != nil && left.Equal(*right)
}

func optionalStringEqual(left, right *string) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}
