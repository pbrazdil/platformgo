package position

import (
	"bytes"
	"encoding/json"

	"github.com/upcomers-org/platformgo/internal/currency"
	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
	"github.com/upcomers-org/platformgo/internal/money"
)

// PositionIdentity supplies the ownership fields which are external to the
// fill-accounting state represented by Position.
type PositionIdentity struct {
	TraderID   ids.TraderID
	StrategyID ids.StrategyID
	AccountID  ids.AccountID
}

// PositionSnapshot captures position state at a specific instant.
type PositionSnapshot struct {
	TraderID           ids.TraderID
	StrategyID         ids.StrategyID
	InstrumentID       ids.InstrumentID
	PositionID         ids.PositionID
	AccountID          ids.AccountID
	OpeningOrderID     ids.ClientOrderID
	ClosingOrderID     *ids.ClientOrderID
	Entry              OrderSide
	Side               Side
	SignedQuantity     decimal.Decimal
	Quantity           decimal.Quantity
	PeakQuantity       decimal.Quantity
	QuoteCurrency      currency.Currency
	BaseCurrency       *currency.Currency
	SettlementCurrency currency.Currency
	AverageOpen        decimal.Decimal
	AverageClose       *decimal.Decimal
	RealizedReturn     *decimal.Decimal
	RealizedPnL        *money.Money
	UnrealizedPnL      *money.Money
	Commissions        []money.Money
	Duration           *uint64
	TsOpened           uint64
	TsClosed           *uint64
	TsInit             uint64
	TsLast             uint64
	ReplayState        json.RawMessage
}

// SnapshotFromPosition copies the current accounting state without retaining
// references to mutable position collections.
func SnapshotFromPosition(
	position *Position,
	identity PositionIdentity,
	unrealizedPnL *money.Money,
) PositionSnapshot {
	closingOrderID := (*ids.ClientOrderID)(nil)
	if position.ClosingOrderID != nil {
		value := ids.MustClientOrderID(*position.ClosingOrderID)
		closingOrderID = &value
	}
	averageClose := cloneDecimal(position.AverageClose)
	realizedReturn := position.RealizedReturn
	duration := position.Duration
	return PositionSnapshot{
		TraderID:           identity.TraderID,
		StrategyID:         identity.StrategyID,
		InstrumentID:       ids.MustInstrumentID(position.Instrument.ID),
		PositionID:         ids.MustPositionID(position.ID),
		AccountID:          identity.AccountID,
		OpeningOrderID:     ids.MustClientOrderID(position.OpeningOrderID),
		ClosingOrderID:     closingOrderID,
		Entry:              position.Entry,
		Side:               position.Side,
		SignedQuantity:     position.SignedQuantity,
		Quantity:           decimal.MustQuantity(position.Quantity.String()),
		PeakQuantity:       decimal.MustQuantity(position.PeakQuantity.String()),
		QuoteCurrency:      position.Instrument.QuoteCurrency,
		BaseCurrency:       cloneCurrency(position.Instrument.BaseCurrency),
		SettlementCurrency: position.Instrument.SettlementCurrency,
		AverageOpen:        position.AverageOpen,
		AverageClose:       averageClose,
		RealizedReturn:     &realizedReturn,
		RealizedPnL:        cloneMoney(position.RealizedPnL),
		UnrealizedPnL:      cloneMoney(unrealizedPnL),
		Commissions:        append([]money.Money(nil), position.Commissions()...),
		Duration:           &duration,
		TsOpened:           position.TsOpened,
		TsClosed:           cloneUint64(position.TsClosed),
		TsInit:             position.TsInit,
		TsLast:             position.TsLast,
	}
}

// SnapshotFromReplayState also embeds the complete durable position replay
// boundary used to reconstruct corrections.
func SnapshotFromReplayState(
	position *Position,
	identity PositionIdentity,
	unrealizedPnL *money.Money,
) (PositionSnapshot, error) {
	snapshot := SnapshotFromPosition(position, identity, unrealizedPnL)
	replay, err := json.Marshal(position)
	if err != nil {
		return PositionSnapshot{}, err
	}
	snapshot.ReplayState = replay
	return snapshot, nil
}

func cloneDecimal(value *decimal.Decimal) *decimal.Decimal {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneCurrency(value *currency.Currency) *currency.Currency {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneMoney(value *money.Money) *money.Money {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneUint64(value *uint64) *uint64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

type snapshotWire struct {
	TraderID           ids.TraderID
	StrategyID         ids.StrategyID
	InstrumentID       ids.InstrumentID
	PositionID         ids.PositionID
	AccountID          ids.AccountID
	OpeningOrderID     ids.ClientOrderID
	ClosingOrderID     *ids.ClientOrderID
	Entry              OrderSide
	Side               Side
	SignedQuantity     string
	Quantity           decimal.Quantity
	PeakQuantity       decimal.Quantity
	QuoteCurrency      currencyWire
	BaseCurrency       *currencyWire
	SettlementCurrency currencyWire
	AverageOpen        string
	AverageClose       *string
	RealizedReturn     *string
	RealizedPnL        *moneyWire
	UnrealizedPnL      *moneyWire
	Commissions        []moneyWire
	Duration           *uint64
	TsOpened           uint64
	TsClosed           *uint64
	TsInit             uint64
	TsLast             uint64
	ReplayState        json.RawMessage `json:",omitempty"`
}

func (snapshot PositionSnapshot) MarshalJSON() ([]byte, error) {
	wire := snapshotWire{
		TraderID:           snapshot.TraderID,
		StrategyID:         snapshot.StrategyID,
		InstrumentID:       snapshot.InstrumentID,
		PositionID:         snapshot.PositionID,
		AccountID:          snapshot.AccountID,
		OpeningOrderID:     snapshot.OpeningOrderID,
		ClosingOrderID:     snapshot.ClosingOrderID,
		Entry:              snapshot.Entry,
		Side:               snapshot.Side,
		SignedQuantity:     snapshot.SignedQuantity.String(),
		Quantity:           snapshot.Quantity,
		PeakQuantity:       snapshot.PeakQuantity,
		QuoteCurrency:      currencyToWire(snapshot.QuoteCurrency),
		SettlementCurrency: currencyToWire(snapshot.SettlementCurrency),
		AverageOpen:        snapshot.AverageOpen.String(),
		Duration:           snapshot.Duration,
		TsOpened:           snapshot.TsOpened,
		TsClosed:           snapshot.TsClosed,
		TsInit:             snapshot.TsInit,
		TsLast:             snapshot.TsLast,
		ReplayState:        snapshot.ReplayState,
	}
	if snapshot.BaseCurrency != nil {
		value := currencyToWire(*snapshot.BaseCurrency)
		wire.BaseCurrency = &value
	}
	if snapshot.AverageClose != nil {
		value := snapshot.AverageClose.String()
		wire.AverageClose = &value
	}
	if snapshot.RealizedReturn != nil {
		value := snapshot.RealizedReturn.String()
		wire.RealizedReturn = &value
	}
	wire.RealizedPnL = moneyToWire(snapshot.RealizedPnL)
	wire.UnrealizedPnL = moneyToWire(snapshot.UnrealizedPnL)
	for index := range snapshot.Commissions {
		wire.Commissions = append(wire.Commissions, *moneyToWire(&snapshot.Commissions[index]))
	}
	return json.Marshal(wire)
}

func (snapshot *PositionSnapshot) UnmarshalJSON(data []byte) error {
	var wire snapshotWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	*snapshot = PositionSnapshot{
		TraderID:           wire.TraderID,
		StrategyID:         wire.StrategyID,
		InstrumentID:       wire.InstrumentID,
		PositionID:         wire.PositionID,
		AccountID:          wire.AccountID,
		OpeningOrderID:     wire.OpeningOrderID,
		ClosingOrderID:     wire.ClosingOrderID,
		Entry:              wire.Entry,
		Side:               wire.Side,
		SignedQuantity:     decimal.MustParse(wire.SignedQuantity),
		Quantity:           wire.Quantity,
		PeakQuantity:       wire.PeakQuantity,
		QuoteCurrency:      currencyFromWire(wire.QuoteCurrency),
		SettlementCurrency: currencyFromWire(wire.SettlementCurrency),
		AverageOpen:        decimal.MustParse(wire.AverageOpen),
		RealizedPnL:        moneyFromWire(wire.RealizedPnL),
		UnrealizedPnL:      moneyFromWire(wire.UnrealizedPnL),
		Duration:           wire.Duration,
		TsOpened:           wire.TsOpened,
		TsClosed:           wire.TsClosed,
		TsInit:             wire.TsInit,
		TsLast:             wire.TsLast,
		ReplayState:        append(json.RawMessage(nil), wire.ReplayState...),
	}
	if wire.BaseCurrency != nil {
		value := currencyFromWire(*wire.BaseCurrency)
		snapshot.BaseCurrency = &value
	}
	if wire.AverageClose != nil {
		value := decimal.MustParse(*wire.AverageClose)
		snapshot.AverageClose = &value
	}
	if wire.RealizedReturn != nil {
		value := decimal.MustParse(*wire.RealizedReturn)
		snapshot.RealizedReturn = &value
	}
	for index := range wire.Commissions {
		snapshot.Commissions = append(snapshot.Commissions, *moneyFromWire(&wire.Commissions[index]))
	}
	return nil
}

func (snapshot PositionSnapshot) Equal(other PositionSnapshot) bool {
	left, leftErr := json.Marshal(snapshot)
	right, rightErr := json.Marshal(other)
	return leftErr == nil && rightErr == nil && bytes.Equal(left, right)
}
