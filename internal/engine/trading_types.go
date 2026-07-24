package engine

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// TradingActionKind identifies one deterministic trading state transition.
type TradingActionKind string

const (
	TradingActionConfigureInstrument TradingActionKind = "configure_instrument"
	TradingActionConfigureAccount    TradingActionKind = "configure_account"
	TradingActionConfigureRisk       TradingActionKind = "configure_risk"
	TradingActionAdjustBalance       TradingActionKind = "adjust_balance"
	TradingActionSettleFunding       TradingActionKind = "settle_funding"
	TradingActionLiquidateAccount    TradingActionKind = "liquidate_account"
	TradingActionUpdateBook          TradingActionKind = "update_book"
	TradingActionSubmitOrder         TradingActionKind = "submit_order"
	TradingActionPlaceBracket        TradingActionKind = "place_bracket"
	TradingActionAmendOrder          TradingActionKind = "amend_order"
	TradingActionCancelOrder         TradingActionKind = "cancel_order"
)

// Side is an explicit order direction. Its zero value is invalid.
type Side string

const (
	SideBuy  Side = "BUY"
	SideSell Side = "SELL"
)

func (side Side) valid() bool {
	return side == SideBuy || side == SideSell
}

// OrderType is an explicit execution instruction. Its zero value is invalid.
type OrderType string

const (
	OrderTypeMarket           OrderType = "MARKET"
	OrderTypeLimit            OrderType = "LIMIT"
	OrderTypeStopMarket       OrderType = "STOP_MARKET"
	OrderTypeStopLimit        OrderType = "STOP_LIMIT"
	OrderTypeTakeProfitMarket OrderType = "TAKE_PROFIT_MARKET"
	OrderTypeTakeProfitLimit  OrderType = "TAKE_PROFIT_LIMIT"
)

func (orderType OrderType) valid() bool {
	switch orderType {
	case OrderTypeMarket, OrderTypeLimit, OrderTypeStopMarket, OrderTypeStopLimit,
		OrderTypeTakeProfitMarket, OrderTypeTakeProfitLimit:
		return true
	default:
		return false
	}
}

// TimeInForce controls whether unfilled quantity may remain working.
type TimeInForce string

const (
	TimeInForceGTC TimeInForce = "GTC"
	TimeInForceIOC TimeInForce = "IOC"
	TimeInForceFOK TimeInForce = "FOK"
)

func (timeInForce TimeInForce) valid() bool {
	switch timeInForce {
	case TimeInForceGTC, TimeInForceIOC, TimeInForceFOK:
		return true
	default:
		return false
	}
}

// OmsMode controls whether fills net by account/instrument or remain as
// independently addressable position legs.
type OmsMode string

const (
	OmsModeNetting OmsMode = "NETTING"
	OmsModeHedging OmsMode = "HEDGING"
)

func (mode OmsMode) valid() bool {
	return mode == OmsModeNetting || mode == OmsModeHedging
}

// MarginMode controls whether collateral is shared across positions.
type MarginMode string

const (
	MarginModeCross    MarginMode = "CROSS"
	MarginModeIsolated MarginMode = "ISOLATED"
)

func (mode MarginMode) valid() bool {
	return mode == MarginModeCross || mode == MarginModeIsolated
}

// LiquiditySide records whether a fill added or removed resting liquidity.
type LiquiditySide string

const (
	LiquiditySideMaker LiquiditySide = "MAKER"
	LiquiditySideTaker LiquiditySide = "TAKER"
)

// BalanceOperation controls an explicit account-balance adjustment.
type BalanceOperation string

const (
	BalanceOperationDeposit BalanceOperation = "DEPOSIT"
	BalanceOperationSet     BalanceOperation = "SET"
)

func (operation BalanceOperation) valid() bool {
	return operation == BalanceOperationDeposit ||
		operation == BalanceOperationSet
}

// PositionSide is the economic direction of an open or closed position.
type PositionSide string

const (
	PositionSideLong  PositionSide = "LONG"
	PositionSideShort PositionSide = "SHORT"
)

// PositionStatus is the lifecycle state of a position.
type PositionStatus string

const (
	PositionStatusOpen   PositionStatus = "open"
	PositionStatusClosed PositionStatus = "closed"
)

// PositionEffect classifies how one fill changed its target position.
type PositionEffect string

const (
	PositionEffectOpen     PositionEffect = "open"
	PositionEffectIncrease PositionEffect = "increase"
	PositionEffectReduce   PositionEffect = "reduce"
	PositionEffectFlip     PositionEffect = "flip"
	PositionEffectClose    PositionEffect = "close"
)

// OrderStatus is the deterministic lifecycle state of an order.
type OrderStatus string

const (
	OrderStatusHeld            OrderStatus = "held"
	OrderStatusWorking         OrderStatus = "working"
	OrderStatusPartiallyFilled OrderStatus = "partially_filled"
	OrderStatusFilled          OrderStatus = "filled"
	OrderStatusCancelled       OrderStatus = "cancelled"
	OrderStatusRejected        OrderStatus = "rejected"
)

// BracketLeg identifies one order's role in an OTO/OCO bracket.
type BracketLeg string

const (
	BracketLegNone       BracketLeg = ""
	BracketLegEntry      BracketLeg = "entry"
	BracketLegTakeProfit BracketLeg = "take_profit"
	BracketLegStopLoss   BracketLeg = "stop_loss"
)

// CommandStatus is the terminal processing result for an engine input.
type CommandStatus string

const (
	CommandStatusAccepted CommandStatus = "accepted"
	CommandStatusRejected CommandStatus = "rejected"
)

// RejectionReason is a stable business-rejection code.
type RejectionReason string

const (
	RejectionInvalidAction       RejectionReason = "invalid_action"
	RejectionInvalidInstrument   RejectionReason = "invalid_instrument"
	RejectionInvalidOrder        RejectionReason = "invalid_order"
	RejectionOrderNotFound       RejectionReason = "order_not_found"
	RejectionOrderOwnership      RejectionReason = "order_ownership"
	RejectionOrderTerminal       RejectionReason = "order_terminal"
	RejectionInsufficientMarket  RejectionReason = "insufficient_market"
	RejectionDuplicateOrderID    RejectionReason = "duplicate_order_id"
	RejectionSlippageExceeded    RejectionReason = "slippage_exceeded"
	RejectionReduceOnly          RejectionReason = "reduce_only"
	RejectionInsufficientFunds   RejectionReason = "insufficient_funds"
	RejectionInsufficientMargin  RejectionReason = "insufficient_margin"
	RejectionRiskConfigLocked    RejectionReason = "risk_config_locked"
	RejectionDuplicateSettlement RejectionReason = "duplicate_settlement"
	RejectionMarketDataStale     RejectionReason = "market_data_stale"
)

// CommandResult records whether a valid envelope produced or rejected a
// business transition. Rejections are committed decisions, not engine faults.
type CommandResult struct {
	Status CommandStatus
	Reason RejectionReason
}

// TradingAction is a closed tagged union. Exactly the member named by Kind
// must be present.
type TradingAction struct {
	Kind                TradingActionKind    `json:"kind"`
	ConfigureInstrument *ConfigureInstrument `json:"configureInstrument,omitempty"`
	ConfigureAccount    *ConfigureAccount    `json:"configureAccount,omitempty"`
	ConfigureRisk       *ConfigureRisk       `json:"configureRisk,omitempty"`
	AdjustBalance       *AdjustBalance       `json:"adjustBalance,omitempty"`
	SettleFunding       *SettleFunding       `json:"settleFunding,omitempty"`
	LiquidateAccount    *LiquidateAccount    `json:"liquidateAccount,omitempty"`
	UpdateBook          *UpdateBook          `json:"updateBook,omitempty"`
	SubmitOrder         *SubmitOrder         `json:"submitOrder,omitempty"`
	PlaceBracket        *PlaceBracket        `json:"placeBracket,omitempty"`
	AmendOrder          *AmendOrder          `json:"amendOrder,omitempty"`
	CancelOrder         *CancelOrder         `json:"cancelOrder,omitempty"`
}

// ConfigureInstrument installs one immutable instrument revision.
type ConfigureInstrument struct {
	InstrumentID            string `json:"instrumentId"`
	Revision                uint64 `json:"revision"`
	PriceScale              uint8  `json:"priceScale"`
	QuantityScale           uint8  `json:"quantityScale"`
	SettlementCurrency      string `json:"settlementCurrency"`
	SettlementCurrencyScale uint8  `json:"settlementCurrencyScale"`
	InitialMarginRate       string `json:"initialMarginRate"`
	MaintenanceMarginRate   string `json:"maintenanceMarginRate"`
	MaxLeverage             string `json:"maxLeverage"`
	MakerFeeRate            string `json:"makerFeeRate"`
	TakerFeeRate            string `json:"takerFeeRate"`
}

// ConfigureAccount installs the account's explicit OMS position mode.
type ConfigureAccount struct {
	AccountID string  `json:"accountId"`
	OmsMode   OmsMode `json:"omsMode"`
}

// ConfigureRisk installs the account/instrument margin mode and leverage.
type ConfigureRisk struct {
	AccountID    string     `json:"accountId"`
	InstrumentID string     `json:"instrumentId"`
	MarginMode   MarginMode `json:"marginMode"`
	Leverage     string     `json:"leverage"`
}

// AdjustBalance applies one explicit settlement-currency balance operation.
type AdjustBalance struct {
	AccountID     string           `json:"accountId"`
	Currency      string           `json:"currency"`
	CurrencyScale uint8            `json:"currencyScale"`
	Operation     BalanceOperation `json:"operation"`
	Amount        string           `json:"amount"`
}

// SettleFunding applies one stable funding interval across an instrument.
type SettleFunding struct {
	SettlementID ID     `json:"settlementId"`
	InstrumentID string `json:"instrumentId"`
	OraclePrice  string `json:"oraclePrice"`
	Rate         string `json:"rate"`
}

// LiquidateAccount evaluates and, when breached, closes an account's positions
// in deterministic worst-notional-first order.
type LiquidateAccount struct {
	AccountID string `json:"accountId"`
}

// BookLevel is an exact price and available quantity.
type BookLevel struct {
	Price    string `json:"price"`
	Quantity string `json:"quantity"`
}

// UpdateBook atomically replaces the deterministic L2 snapshot.
type UpdateBook struct {
	InstrumentID string      `json:"instrumentId"`
	MarkPrice    string      `json:"markPrice,omitempty"`
	Bids         []BookLevel `json:"bids"`
	Asks         []BookLevel `json:"asks"`
}

// SubmitOrder creates one order with a caller-supplied stable identity.
type SubmitOrder struct {
	OrderID        ID          `json:"orderId"`
	AccountID      string      `json:"accountId"`
	InstrumentID   string      `json:"instrumentId"`
	Side           Side        `json:"side"`
	Type           OrderType   `json:"type"`
	TimeInForce    TimeInForce `json:"timeInForce"`
	Quantity       string      `json:"quantity"`
	Price          string      `json:"price,omitempty"`
	TriggerPrice   string      `json:"triggerPrice,omitempty"`
	ReduceOnly     bool        `json:"reduceOnly"`
	PositionID     ID          `json:"positionId,omitempty"`
	MaxSlippageBPS *uint32     `json:"maxSlippageBps,omitempty"`
}

// ProtectiveLeg defines one exact take-profit slice.
type ProtectiveLeg struct {
	OrderID  ID     `json:"orderId"`
	Price    string `json:"price"`
	Quantity string `json:"quantity"`
}

// PlaceBracket creates an entry and its held OTO/OCO protection atomically.
// Every business identity is caller supplied so retries are deterministic.
type PlaceBracket struct {
	BracketID       ID              `json:"bracketId"`
	EntryOrderID    ID              `json:"entryOrderId"`
	AccountID       string          `json:"accountId"`
	InstrumentID    string          `json:"instrumentId"`
	Side            Side            `json:"side"`
	EntryType       OrderType       `json:"entryType"`
	TimeInForce     TimeInForce     `json:"timeInForce"`
	Quantity        string          `json:"quantity"`
	EntryPrice      string          `json:"entryPrice,omitempty"`
	TakeProfits     []ProtectiveLeg `json:"takeProfits"`
	StopLossOrderID ID              `json:"stopLossOrderId"`
	StopLoss        string          `json:"stopLoss"`
}

// AmendOrder changes the exact price and quantity of a working order.
type AmendOrder struct {
	AccountID string `json:"accountId"`
	OrderID   ID     `json:"orderId"`
	Quantity  string `json:"quantity"`
	Price     string `json:"price"`
}

// CancelOrder cancels one working order owned by the account.
type CancelOrder struct {
	AccountID string `json:"accountId"`
	OrderID   ID     `json:"orderId"`
}

// EncodeTradingAction returns the canonical bytes bound into InputEnvelope.
func EncodeTradingAction(action TradingAction) (CanonicalPayload, error) {
	payload, err := NewCanonicalJSONPayload(action)
	if err != nil {
		return CanonicalPayload{}, fmt.Errorf("encode trading action: %w", err)
	}
	return payload, nil
}

// DecodeTradingActionPayload restores a persisted canonical action while
// rejecting unknown fields, trailing values, and non-canonical encodings.
func DecodeTradingActionPayload(
	encoded []byte,
) (TradingAction, CanonicalPayload, error) {
	var action TradingAction
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&action); err != nil {
		return TradingAction{}, CanonicalPayload{}, fmt.Errorf(
			"decode trading action: %w",
			err,
		)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return TradingAction{}, CanonicalPayload{}, err
	}
	canonical, err := EncodeTradingAction(action)
	if err != nil {
		return TradingAction{}, CanonicalPayload{}, err
	}
	if !bytes.Equal(encoded, canonical.value) {
		return TradingAction{}, CanonicalPayload{}, fmt.Errorf(
			"decode trading action: payload is not canonical",
		)
	}
	return action, canonical, nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	var trailing any
	err := decoder.Decode(&trailing)
	if err == io.EOF {
		return nil
	}
	if err != nil {
		return fmt.Errorf("decode trading action trailing data: %w", err)
	}
	return fmt.Errorf("decode trading action: multiple JSON values")
}

// InstrumentSnapshot is the externally inspectable configured revision.
type InstrumentSnapshot struct {
	InstrumentID            string
	Revision                uint64
	PriceScale              uint8
	QuantityScale           uint8
	SettlementCurrency      string
	SettlementCurrencyScale uint8
	InitialMarginRate       string
	MaintenanceMarginRate   string
	MaxLeverage             string
	MakerFeeRate            string
	TakerFeeRate            string
}

// AccountSnapshot is the configured deterministic OMS mode for one account.
type AccountSnapshot struct {
	AccountID string
	OmsMode   OmsMode
}

// RiskSnapshot is the configured margin mode and leverage for one instrument.
type RiskSnapshot struct {
	AccountID    string
	InstrumentID string
	MarginMode   MarginMode
	Leverage     string
}

// BalanceSnapshot is one exact account currency state.
type BalanceSnapshot struct {
	AccountID string
	Currency  string
	Total     string
	Used      string
	Free      string
	Equity    string
}

// SystemClearingAccount is the counterparty for externally introduced or
// removed account currency. Every ledger transaction remains zero-sum.
const SystemClearingAccount = "system:clearing"

// LedgerEntrySnapshot is one exact immutable debit or credit.
type LedgerEntrySnapshot struct {
	EntryID   ID
	AccountID string
	Currency  string
	Amount    string
}

// LedgerTransactionSnapshot is one balanced economic effect decided by the
// deterministic core. Adapters persist it without deriving money behavior.
type LedgerTransactionSnapshot struct {
	TransactionID ID
	BusinessKey   string
	InputID       ID
	LogicalTime   LogicalTime
	Entries       []LedgerEntrySnapshot
}

// FundingSnapshot is one append-only position funding effect.
type FundingSnapshot struct {
	FundingID          ID
	SettlementID       ID
	PositionID         ID
	AccountID          string
	InstrumentID       string
	SignedQuantity     string
	OraclePrice        string
	Rate               string
	Amount             string
	SettlementCurrency string
}

// BookSnapshot is the canonical price-sorted market state.
type BookSnapshot struct {
	InstrumentID string
	MarkPrice    string
	Bids         []BookLevel
	Asks         []BookLevel
}

// OrderSnapshot is the immutable query representation of one order version.
type OrderSnapshot struct {
	OrderID           ID
	AccountID         string
	InstrumentID      string
	Side              Side
	Type              OrderType
	TimeInForce       TimeInForce
	Status            OrderStatus
	Quantity          string
	FilledQuantity    string
	AverageFillPrice  string
	Price             string
	TriggerPrice      string
	Triggered         bool
	TriggeredAt       LogicalTime
	ReduceOnly        bool
	PositionID        ID
	BracketID         ID
	BracketLeg        BracketLeg
	BracketLegIndex   uint32
	HasRested         bool
	HasSlippageBand   bool
	MaxSlippageBPS    uint32
	SlippageReference string
	RejectReason      RejectionReason
	Version           uint64
}

// FillSnapshot is one exact, stable execution fact.
type FillSnapshot struct {
	FillID             ID
	OrderID            ID
	AccountID          string
	InstrumentID       string
	Side               Side
	Price              string
	Quantity           string
	PositionID         ID
	PositionEffect     PositionEffect
	RealizedPnL        string
	SettlementCurrency string
	LiquiditySide      LiquiditySide
	Fee                string
	FeeCurrency        string
	LogicalTime        LogicalTime
}

// PositionSnapshot is the immutable query representation of one position
// version. SignedQuantity is negative only for short positions.
type PositionSnapshot struct {
	PositionID         ID
	AccountID          string
	InstrumentID       string
	Side               PositionSide
	Status             PositionStatus
	SignedQuantity     string
	AverageOpenPrice   string
	RealizedPnL        string
	SettlementCurrency string
	MarginMode         MarginMode
	IsolatedCollateral string
	Version            uint64
}

// DomainEvent records one canonical aggregate transition.
type DomainEvent struct {
	EventID          ID
	Kind             string
	AggregateID      ID
	AggregateVersion uint64
	LogicalTime      LogicalTime
}
