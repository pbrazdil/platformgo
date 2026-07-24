package engine

import (
	"testing"
	"time"
)

// Ported from:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/trading/e2e_limit_order.rs:36
//	test: marketable_limit_fills_resting_limit_works
//
// Adaptations:
//   - LiveStack and Hyperliquid are replaced by an explicit deterministic book.
//   - SQL polling is replaced by synchronous production-engine snapshots.
//
// Assertions preserved:
//   - A marketable limit fills.
//   - A non-crossing limit remains working.
func TestTradingMarketableLimitFillsRestingLimitWorks(t *testing.T) {
	fixture := newTradingFixture(t)

	marketable := fixture.submit(t, SubmitOrder{
		OrderID:      fixture.id(10),
		AccountID:    "account-1",
		InstrumentID: "BTC-PERP",
		Side:         SideBuy,
		Type:         OrderTypeLimit,
		TimeInForce:  TimeInForceGTC,
		Quantity:     "0.001",
		Price:        "1000000",
	})
	assertOrderStatus(t, marketable, OrderStatusFilled)

	resting := fixture.submit(t, SubmitOrder{
		OrderID:      fixture.id(11),
		AccountID:    "account-1",
		InstrumentID: "BTC-PERP",
		Side:         SideBuy,
		Type:         OrderTypeLimit,
		TimeInForce:  TimeInForceGTC,
		Quantity:     "0.001",
		Price:        "1.00",
	})
	assertOrderStatus(t, resting, OrderStatusWorking)
}

// Ported from:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/trading/e2e_cancel_order.rs:37
//	test: resting_limit_can_be_cancelled
//
// Adaptations:
//   - Live runtime and SQL polling are replaced by synchronous engine inputs.
//
// Assertions preserved:
//   - The resting order reaches working.
//   - Cancelling it makes it cancelled.
func TestTradingRestingLimitCanBeCancelled(t *testing.T) {
	fixture := newTradingFixture(t)
	orderID := fixture.id(20)
	order := fixture.submit(t, SubmitOrder{
		OrderID:      orderID,
		AccountID:    "account-1",
		InstrumentID: "BTC-PERP",
		Side:         SideBuy,
		Type:         OrderTypeLimit,
		TimeInForce:  TimeInForceGTC,
		Quantity:     "0.001",
		Price:        "1.00",
	})
	assertOrderStatus(t, order, OrderStatusWorking)

	fixture.apply(t, TradingAction{
		Kind: TradingActionCancelOrder,
		CancelOrder: &CancelOrder{
			AccountID: "account-1",
			OrderID:   orderID,
		},
	})
	cancelled, ok := fixture.state.Order(orderID)
	if !ok {
		t.Fatal("cancelled order is missing")
	}
	assertOrderStatus(t, cancelled, OrderStatusCancelled)
}

// Ported from:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/trading/e2e_modify_order.rs:49
//	test: resting_limit_can_be_modified
//
// Adaptations:
//   - Float-backed SQL projection assertions are replaced by exact canonical text.
//
// Assertions preserved:
//   - Modification preserves working status and order identity.
//   - Price becomes 2 and quantity becomes 0.002.
func TestTradingRestingLimitCanBeModified(t *testing.T) {
	fixture := newTradingFixture(t)
	orderID := fixture.id(30)
	fixture.submit(t, SubmitOrder{
		OrderID:      orderID,
		AccountID:    "account-1",
		InstrumentID: "BTC-PERP",
		Side:         SideBuy,
		Type:         OrderTypeLimit,
		TimeInForce:  TimeInForceGTC,
		Quantity:     "0.001",
		Price:        "1.00",
	})

	fixture.apply(t, TradingAction{
		Kind: TradingActionAmendOrder,
		AmendOrder: &AmendOrder{
			AccountID: "account-1",
			OrderID:   orderID,
			Quantity:  "0.002",
			Price:     "2.00",
		},
	})
	modified, ok := fixture.state.Order(orderID)
	if !ok {
		t.Fatal("modified order is missing")
	}
	assertOrderStatus(t, modified, OrderStatusWorking)
	if modified.OrderID != orderID {
		t.Fatalf("modified order ID = %s, want preserved %s", modified.OrderID, orderID)
	}
	if modified.Price != "2" || modified.Quantity != "0.002" {
		t.Fatalf("modified price/quantity = %s/%s, want 2/0.002", modified.Price, modified.Quantity)
	}
}

// Ported from:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/trading/e2e_order_semantics.rs:98
//	test: order_semantics_tif_and_cumulative_fill
//
// Adaptations:
//   - SQL and Nautilus fill polling are replaced by production-engine snapshots.
//   - Float tolerance assertions are strengthened to exact decimal equality.
//
// Assertions preserved:
//   - Deep non-crossing GTC rests and retains GTC.
//   - Market order fills.
//   - Cumulative filled quantity equals both order quantity and fill sum.
func TestTradingOrderSemanticsTIFAndCumulativeFill(t *testing.T) {
	fixture := newTradingFixture(t)
	gtc := fixture.submit(t, SubmitOrder{
		OrderID:      fixture.id(40),
		AccountID:    "account-1",
		InstrumentID: "BTC-PERP",
		Side:         SideBuy,
		Type:         OrderTypeLimit,
		TimeInForce:  TimeInForceGTC,
		Quantity:     "0.001",
		Price:        "1.00",
	})
	assertOrderStatus(t, gtc, OrderStatusWorking)
	if gtc.TimeInForce != TimeInForceGTC {
		t.Fatalf("time in force = %s, want GTC", gtc.TimeInForce)
	}

	market := fixture.submit(t, SubmitOrder{
		OrderID:      fixture.id(41),
		AccountID:    "account-1",
		InstrumentID: "BTC-PERP",
		Side:         SideBuy,
		Type:         OrderTypeMarket,
		TimeInForce:  TimeInForceGTC,
		Quantity:     "0.001",
	})
	assertOrderStatus(t, market, OrderStatusFilled)
	if market.FilledQuantity != "0.001" || market.FilledQuantity != market.Quantity {
		t.Fatalf("filled/ordered quantity = %s/%s, want 0.001/0.001", market.FilledQuantity, market.Quantity)
	}
	fills := fixture.state.FillsForOrder(market.OrderID)
	if len(fills) != 1 || fills[0].Quantity != market.FilledQuantity {
		t.Fatalf("fills = %+v, want one exact cumulative fill of %s", fills, market.FilledQuantity)
	}
}

// Ported from:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/trading/e2e_stop_order.rs:77
//	test: breached_stop_fills_resting_stops_work
//
// Adaptations:
//   - The live venue is replaced by an explicit ask of 100.
//
// Assertions preserved:
//   - A breached buy stop-market fills.
//   - Non-breached stop-market and stop-limit orders remain working.
func TestTradingBreachedStopFillsRestingStopsWork(t *testing.T) {
	fixture := newTradingFixture(t)

	inMarket := fixture.submit(t, SubmitOrder{
		OrderID:      fixture.id(50),
		AccountID:    "account-1",
		InstrumentID: "BTC-PERP",
		Side:         SideBuy,
		Type:         OrderTypeStopMarket,
		TimeInForce:  TimeInForceGTC,
		Quantity:     "0.001",
		TriggerPrice: "1.00",
	})
	assertOrderStatus(t, inMarket, OrderStatusFilled)

	restingMarket := fixture.submit(t, SubmitOrder{
		OrderID:      fixture.id(51),
		AccountID:    "account-1",
		InstrumentID: "BTC-PERP",
		Side:         SideBuy,
		Type:         OrderTypeStopMarket,
		TimeInForce:  TimeInForceGTC,
		Quantity:     "0.001",
		TriggerPrice: "1000000",
	})
	assertOrderStatus(t, restingMarket, OrderStatusWorking)

	restingLimit := fixture.submit(t, SubmitOrder{
		OrderID:      fixture.id(52),
		AccountID:    "account-1",
		InstrumentID: "BTC-PERP",
		Side:         SideBuy,
		Type:         OrderTypeStopLimit,
		TimeInForce:  TimeInForceGTC,
		Quantity:     "0.001",
		Price:        "1.00",
		TriggerPrice: "1000000",
	})
	assertOrderStatus(t, restingLimit, OrderStatusWorking)
}

// Ported from:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/trading/e2e_ioc_fok_never_rest.rs:61
//	test: ioc_and_fok_never_rest
//
// Adaptations:
//   - Venue polling is replaced by one synchronous deterministic attempt.
//
// Assertions preserved:
//   - An unfillable IOC is cancelled and never rests.
//   - An unfillable FOK is cancelled and never rests.
func TestTradingIOCAndFOKNeverRest(t *testing.T) {
	fixture := newTradingFixture(t)
	orderIDs := []uint64{60, 61}
	for index, timeInForce := range []TimeInForce{TimeInForceIOC, TimeInForceFOK} {
		order := fixture.submit(t, SubmitOrder{
			OrderID:      fixture.id(orderIDs[index]),
			AccountID:    "account-1",
			InstrumentID: "BTC-PERP",
			Side:         SideBuy,
			Type:         OrderTypeLimit,
			TimeInForce:  timeInForce,
			Quantity:     "0.001",
			Price:        "1.00",
		})
		assertOrderStatus(t, order, OrderStatusCancelled)
		if order.FilledQuantity != "0" {
			t.Fatalf("%s filled quantity = %s, want zero", timeInForce, order.FilledQuantity)
		}
	}
}

type tradingFixture struct {
	state          State
	namespace      ID
	sequence       uint64
	marketSequence uint64
	logicalTime    LogicalTime
	lastInput      InputEnvelope
	lastAction     TradingAction
	lastDecision   Decision
}

func newTradingFixture(t *testing.T) *tradingFixture {
	t.Helper()

	fixture := newTradingFixtureWithoutBook(t)
	fixture.apply(t, TradingAction{
		Kind: TradingActionUpdateBook,
		UpdateBook: &UpdateBook{
			InstrumentID: "BTC-PERP",
			MarkPrice:    "100",
			Bids:         []BookLevel{{Price: "99", Quantity: "10"}},
			Asks:         []BookLevel{{Price: "100", Quantity: "10"}},
		},
	})
	return fixture
}

func newTradingFixtureWithoutBook(t *testing.T) *tradingFixture {
	t.Helper()

	fixture := &tradingFixture{
		state:     NewState(1),
		namespace: mustID(t, "019f9460-4b36-4e9b-8f44-682611f7ee01"),
		sequence:  1,
		logicalTime: NewLogicalTime(
			time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC),
		),
	}
	fixture.apply(t, TradingAction{
		Kind: TradingActionConfigureInstrument,
		ConfigureInstrument: &ConfigureInstrument{
			InstrumentID:            "BTC-PERP",
			Revision:                1,
			PriceScale:              2,
			QuantityScale:           3,
			SettlementCurrency:      "USDC",
			SettlementCurrencyScale: 8,
			InitialMarginRate:       "1",
			MaintenanceMarginRate:   "0.05",
			MaxLeverage:             "10",
		},
	})
	fixture.apply(t, TradingAction{
		Kind: TradingActionAdjustBalance,
		AdjustBalance: &AdjustBalance{
			AccountID:     "account-1",
			Currency:      "USDC",
			CurrencyScale: 8,
			Operation:     BalanceOperationSet,
			Amount:        "1000000",
		},
	})
	fixture.apply(t, TradingAction{
		Kind: TradingActionAdjustBalance,
		AdjustBalance: &AdjustBalance{
			AccountID:     "account-2",
			Currency:      "USDC",
			CurrencyScale: 8,
			Operation:     BalanceOperationSet,
			Amount:        "1000000",
		},
	})
	return fixture
}

func (fixture *tradingFixture) id(sequence uint64) ID {
	return IDFromSequence(fixture.namespace, sequence)
}

func (fixture *tradingFixture) submit(t *testing.T, command SubmitOrder) OrderSnapshot {
	t.Helper()

	fixture.apply(t, TradingAction{
		Kind:        TradingActionSubmitOrder,
		SubmitOrder: &command,
	})
	order, ok := fixture.state.Order(command.OrderID)
	if !ok {
		t.Fatalf("submitted order %s is missing", command.OrderID)
	}
	return order
}

func (fixture *tradingFixture) apply(t *testing.T, action TradingAction) Decision {
	t.Helper()

	decision := fixture.applyDecision(t, action)
	if decision.CommandResult.Status == CommandStatusRejected {
		t.Fatalf("ApplyTrading(%s) rejected: %s", action.Kind, decision.CommandResult.Reason)
	}
	return decision
}

func (fixture *tradingFixture) applyDecision(t *testing.T, action TradingAction) Decision {
	t.Helper()

	payload, err := EncodeTradingAction(action)
	if err != nil {
		t.Fatalf("EncodeTradingAction: %v", err)
	}
	kind := InputKindCommand
	if action.Kind == TradingActionUpdateBook {
		kind = InputKindMarket
		fixture.marketSequence++
	}
	input := InputEnvelope{
		InputID:              fixture.id(1_000 + fixture.sequence),
		SchemaVersion:        CurrentSchemaVersion,
		ShardID:              1,
		Kind:                 kind,
		SourceID:             "phase1-order-fixture",
		SourceSequence:       fixture.sequence,
		StreamSequence:       fixture.sequence,
		MarketSequence:       fixture.marketSequence,
		LogicalTime:          fixture.logicalTime,
		ConfigurationVersion: 1,
		InstrumentVersion:    1,
		Payload:              payload,
	}
	fixture.sequence++
	fixture.logicalTime++

	var decision Decision
	fixture.state, decision, err = ApplyTrading(fixture.state, input, action)
	if err != nil {
		t.Fatalf("ApplyTrading(%s): %v", action.Kind, err)
	}
	fixture.lastInput = input
	fixture.lastAction = action
	fixture.lastDecision = cloneDecision(decision)
	return decision
}

func assertOrderStatus(t *testing.T, order OrderSnapshot, want OrderStatus) {
	t.Helper()
	if order.Status != want {
		t.Fatalf("order %s status = %s, want %s", order.OrderID, order.Status, want)
	}
}
