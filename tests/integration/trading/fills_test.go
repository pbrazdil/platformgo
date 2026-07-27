package trading

import (
	"fmt"
	"sort"
	"strings"
	"testing"
)

func seedDefaultFill(fixture *fillFixture, id, symbol, side, quantity, price string) {
	fixture.seedFill(fillSeed{
		FillID: id, AccountID: "acct-1", UserID: "user-1",
		OrderID: "00000000-0000-0000-0000-0000000000ff", PositionID: "pos-1",
		Symbol: symbol, Side: side, Quantity: quantity, Price: price,
		Commission: "0.5", Liquidity: "taker",
	})
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/trading/e2e_fills.rs:63
//	test: fills_history_reads_and_paginates
func TestFillsHistoryReadsAndPaginates(t *testing.T) {
	fixture := newFillFixture()
	seedDefaultFill(fixture, "t1", "BTC-PERP", "BUY", "0.01", "60000")
	seedDefaultFill(fixture, "t2", "BTC-PERP", "SELL", "0.01", "61000")
	seedDefaultFill(fixture, "t3", "ETH-PERP", "BUY", "0.1", "3000")

	page1 := fixture.queryFills(fillQuery{AccountID: "acct-1", Limit: 2})
	if len(page1.Items) != 2 || page1.Total != 3 || page1.NextCursor == "" {
		t.Fatalf("page1=%#v", page1)
	}
	row := page1.Items[0]
	if row.PositionID != positionURN("pos-1") || row.Reason != "manual" || row.Leverage != "" ||
		row.OrderType != nil || row.Commission != "0.5" || row.QuoteQuantity != "300" ||
		row.Liquidity != "taker" || row.AccountID != "acct-1" || row.UserID != "user-1" ||
		row.FeeRate != "0.0005" ||
		row.Exchange == "" || row.Base != "ETH" || row.Quote != "USDC" ||
		row.ProductType != "perp" || row.FeeAsset != "USDC" {
		t.Fatalf("row=%#v", row)
	}
	page2 := fixture.queryFills(fillQuery{AccountID: "acct-1", Limit: 2, Cursor: page1.NextCursor})
	if len(page2.Items) != 1 {
		t.Fatalf("page2=%#v", page2)
	}
	ids := []string{page1.Items[0].FillID, page1.Items[1].FillID, page2.Items[0].FillID}
	sort.Strings(ids)
	if fmt.Sprint(ids) != "[t1 t2 t3]" {
		t.Fatalf("ids=%v", ids)
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/trading/e2e_fills.rs:207
//	test: fill_filled_at_is_engine_execution_time_not_insert_now
func TestFillFilledAtIsEngineExecutionTimeNotInsertNow(t *testing.T) {
	fixture := newFillFixture()
	fill := fillSeed{
		FillID: "ts1", AccountID: "acct-1", UserID: "user-1", OrderID: "missing",
		PositionID: "pos-ts", Symbol: "BTC-PERP", Side: "BUY", Quantity: "0.01",
		Price: "60000", Commission: "0.5", Liquidity: "taker", ExecutedAtMS: 1_600_000_000_000,
	}
	fixture.seedFill(fill)
	row := fixture.queryFills(fillQuery{AccountID: "acct-1", Limit: 1}).Items[0]
	if !strings.HasPrefix(row.FilledAt, "2020-09-13") {
		t.Fatalf("filledAt=%s", row.FilledAt)
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/trading/e2e_fills.rs:252
//	test: fill_history_returns_side_and_trade_type
func TestFillHistoryReturnsSideAndTradeType(t *testing.T) {
	fixture := newFillFixture()
	for _, fill := range []fillSeed{
		{FillID: "open-1", Side: "BUY", Entry: "open"},
		{FillID: "increase-1", Side: "BUY", Entry: "increase"},
		{FillID: "reduce-1", Side: "SELL", Entry: "reduce"},
		{FillID: "flip-1", Side: "SELL", Entry: "flip"},
		{FillID: "close-1", Side: "SELL", Entry: "close"},
		{FillID: "plain-1", Side: "BUY"},
	} {
		fill.AccountID, fill.UserID, fill.OrderID, fill.PositionID = "acct-1", "user-1", "missing", "pos-1"
		fill.Symbol, fill.Quantity, fill.Price, fill.Commission, fill.Liquidity = "BTC-PERP", "0.01", "60000", "0.5", "taker"
		fixture.seedFill(fill)
	}
	page := fixture.queryFills(fillQuery{AccountID: "acct-1", Limit: 10})
	byID := make(map[string]fillView)
	for _, fill := range page.Items {
		byID[fill.FillID] = fill
	}
	if byID["open-1"].Side != "BUY" || byID["open-1"].TradeType != "open" ||
		byID["increase-1"].TradeType != "increase" || byID["reduce-1"].TradeType != "reduce" ||
		byID["flip-1"].TradeType != "flip" || byID["close-1"].Side != "SELL" ||
		byID["close-1"].TradeType != "close" || byID["plain-1"].TradeType != "" {
		t.Fatalf("fills=%#v", byID)
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/trading/e2e_fills.rs:328
//	test: fills_history_filters_by_side_and_trade_id
func TestFillsHistoryFiltersBySideAndTradeID(t *testing.T) {
	fixture := newFillFixture()
	seedDefaultFill(fixture, "f-buy", "BTC-PERP", "BUY", "0.01", "60000")
	seedDefaultFill(fixture, "f-sell", "BTC-PERP", "SELL", "0.01", "61000")
	buys := fixture.queryFills(fillQuery{AccountID: "acct-1", Side: "buy", Limit: 10})
	if len(buys.Items) != 1 || buys.Items[0].FillID != "f-buy" || buys.Total != 1 {
		t.Fatalf("buys=%#v", buys)
	}
	one := fixture.queryFills(fillQuery{AccountID: "acct-1", TradeID: "f-sell", Limit: 10})
	if len(one.Items) != 1 || one.Items[0].FillID != "f-sell" {
		t.Fatalf("one=%#v", one)
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/trading/e2e_fills.rs:410
//	test: fill_order_id_is_the_correlatable_order_urn
func TestFillOrderIDIsTheCorrelatableOrderURN(t *testing.T) {
	fixture := newFillFixture()
	orderID := "11111111-2222-3333-4444-555555555555"
	fill := fillSeed{
		FillID: "corr-1", AccountID: "acct-1", UserID: "user-1", OrderID: orderID,
		PositionID: "pos-1", Symbol: "BTC-PERP", Side: "BUY", Quantity: "0.01",
		Price: "60000", Commission: "0.5", Liquidity: "taker",
	}
	fixture.seedFill(fill)
	row := fixture.queryFills(fillQuery{AccountID: "acct-1", Limit: 10}).Items[0]
	if row.OrderID != orderURN(orderID) {
		t.Fatalf("orderID=%s", row.OrderID)
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/trading/e2e_fills.rs:459
//	test: saga_reconcile_settles_orphaned_order_from_fills
func TestSagaReconcileSettlesOrphanedOrderFromFills(t *testing.T) {
	fixture := newFillFixture()
	fixture.insertOrder(fillOrder{ID: "order-1", Intent: "saga-recon-1", OrderType: "MARKET", Quantity: "0.01"})
	if fixture.settleOneFromFills("order-1") {
		t.Fatal("order settled without fill")
	}
	fixture.seedFill(fillSeed{
		FillID: "saga-fill-1", AccountID: "acct-1", UserID: "user-1", OrderID: "order-1",
		Symbol: "BTC-PERP", Side: "BUY", Quantity: "0.01", Price: "60000",
		Commission: "0.5", Liquidity: "taker",
	})
	if !fixture.settleOneFromFills("order-1") {
		t.Fatal("fill did not settle orphan")
	}
	order := fixture.orders["order-1"]
	if order.Status != "filled" || order.FilledQuantity != "0.01" {
		t.Fatalf("order=%#v", order)
	}
	if fixture.settleOneFromFills("order-1") {
		t.Fatal("filled order re-settled")
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/trading/e2e_fills.rs:656
//	test: fill_reason_derives_from_bracket_leg_and_stopout
func TestFillReasonDerivesFromBracketLegAndStopout(t *testing.T) {
	fixture := newFillFixture()
	fixture.insertOrder(fillOrder{ID: "sl", Intent: "brk:sl", BracketLeg: "stop_loss", OrderType: "STOP_MARKET"})
	fixture.insertOrder(fillOrder{ID: "tp", Intent: "brk:tp", BracketLeg: "take_profit", OrderType: "TAKE_PROFIT_MARKET"})
	fixture.insertOrder(fillOrder{ID: "liq", Intent: "stopout:42:BTC-PERP", OrderType: "MARKET"})
	fixture.insertOrder(fillOrder{ID: "flat", Intent: "flatten:01JZTESTFLATTEN0000000000", OrderType: "MARKET"})
	fixture.insertOrder(fillOrder{ID: "manual", Intent: "manual-close", OrderType: "MARKET"})
	for fillID, orderID := range map[string]string{"f-sl": "sl", "f-tp": "tp", "f-liq": "liq", "f-flat": "flat", "f-user": "manual"} {
		fixture.seedFill(fillSeed{
			FillID: fillID, AccountID: "acct-1", UserID: "user-1", OrderID: orderID,
			PositionID: "pos-1", Symbol: "BTC-PERP", Side: "SELL", Quantity: "0.02",
			Price: "60000", Commission: "0", Liquidity: "taker", Entry: "close",
		})
	}
	page := fixture.queryFills(fillQuery{AccountID: "acct-1", Limit: 10})
	reasons := make(map[string]string)
	for _, fill := range page.Items {
		reasons[fill.FillID] = fill.Reason
	}
	want := map[string]string{"f-sl": "stop_loss", "f-tp": "take_profit", "f-liq": "liquidation", "f-flat": "flatten", "f-user": "manual"}
	for id, reason := range want {
		if reasons[id] != reason {
			t.Fatalf("%s reason=%q want=%q", id, reasons[id], reason)
		}
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/trading/e2e_fills.rs:849
//	test: fill_realized_isolates_hedged_legs_by_position
func TestFillRealizedIsolatesHedgedLegsByPosition(t *testing.T) {
	fixture := newFillFixture()
	for _, fill := range []fillSeed{
		{FillID: "hl-open", PositionID: "hedge-long", Side: "BUY", RealizedPnL: "0"},
		{FillID: "hl-close", PositionID: "hedge-long", Side: "SELL", RealizedPnL: "50"},
		{FillID: "hs-open", PositionID: "hedge-short", Side: "SELL", RealizedPnL: "0"},
		{FillID: "hs-close", PositionID: "hedge-short", Side: "BUY", RealizedPnL: "30"},
	} {
		fill.AccountID, fill.UserID, fill.OrderID = "acct-1", "user-1", "missing"
		fill.Symbol, fill.Quantity, fill.Price, fill.Commission, fill.Liquidity = "BTC-PERP", "0.02", "60000", "0", "taker"
		fixture.seedFill(fill)
	}
	page := fixture.queryFills(fillQuery{AccountID: "acct-1", Limit: 10})
	byID := make(map[string]fillView)
	for _, fill := range page.Items {
		byID[fill.FillID] = fill
	}
	if byID["hl-close"].RealizedPnL != "50" || byID["hs-close"].RealizedPnL != "30" ||
		byID["hl-open"].RealizedPnL != "0" || byID["hs-open"].RealizedPnL != "0" ||
		byID["hl-close"].PositionID != positionURN("hedge-long") ||
		byID["hs-close"].PositionID != positionURN("hedge-short") ||
		byID["hl-close"].PositionID == byID["hs-close"].PositionID {
		t.Fatalf("fills=%#v", byID)
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/trading/e2e_fills.rs:952
//	test: fill_surfaces_frozen_effective_leverage
func TestFillSurfacesFrozenEffectiveLeverage(t *testing.T) {
	fixture := newFillFixture()
	for _, fill := range []fillSeed{
		{FillID: "lev-10", Leverage: "10"},
		{FillID: "lev-5", Leverage: "5.00"},
		{FillID: "lev-none"},
	} {
		fill.AccountID, fill.UserID, fill.OrderID, fill.PositionID = "acct-1", "user-1", "missing", "pos-1"
		fill.Symbol, fill.Side, fill.Quantity, fill.Price, fill.Commission, fill.Liquidity, fill.Entry =
			"BTC-PERP", "BUY", "0.01", "60000", "0", "taker", "open"
		fixture.seedFill(fill)
	}
	page := fixture.queryFills(fillQuery{AccountID: "acct-1", Limit: 10})
	byID := make(map[string]fillView)
	for _, fill := range page.Items {
		byID[fill.FillID] = fill
	}
	if byID["lev-10"].Leverage != "10" || byID["lev-5"].Leverage != "5" || byID["lev-none"].Leverage != "" {
		t.Fatalf("fills=%#v", byID)
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/trading/e2e_fills.rs:1029
//	test: rejected_order_persists_reason
func TestRejectedOrderPersistsReason(t *testing.T) {
	fixture := newFillFixture()
	fixture.insertOrder(fillOrder{ID: "order-reject", Intent: "reject-1", OrderType: "MARKET", Quantity: "0.01"})
	before := fixture.orders["order-reject"]
	if before.Status != "pending" || before.RejectReason != "" {
		t.Fatalf("before=%#v", before)
	}
	if !fixture.markRejected("order-reject", "MARGIN_EXCEEDS_FREE_BALANCE") {
		t.Fatal("pending order was not rejected")
	}
	rejected := fixture.orders["order-reject"]
	if rejected.Status != "rejected" || rejected.RejectReason != "MARGIN_EXCEEDS_FREE_BALANCE" {
		t.Fatalf("rejected=%#v", rejected)
	}
	if fixture.markRejected("order-reject", "OTHER") ||
		fixture.orders["order-reject"].RejectReason != "MARGIN_EXCEEDS_FREE_BALANCE" {
		t.Fatalf("re-rejection changed order=%#v", fixture.orders["order-reject"])
	}
}
