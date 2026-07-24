package engine

import "testing"

// Ported from:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/trading/e2e_book_vwap.rs:104
//	test: market_fill_is_depth_weighted_vwap_over_real_l2
//
// Adaptations:
//   - The ignored live harness is replaced by a deterministic L2 snapshot.
//   - Beyond-depth remainder pricing at the deepest level is asserted exactly.
//
// Assertions preserved:
//   - Small order fills fully at top of book.
//   - Large order fills fully beyond displayed depth.
//   - Large VWAP is worse than top and better than the deepest price.
func TestTradingMarketFillIsDepthWeightedVWAPOverRealL2(t *testing.T) {
	fixture := newTradingFixture(t)
	fixture.updateBook(t, "60000",
		[]BookLevel{{Price: "59900", Quantity: "1"}},
		[]BookLevel{
			{Price: "60000", Quantity: "0.002"},
			{Price: "60100", Quantity: "0.002"},
			{Price: "60200", Quantity: "0.002"},
		},
	)

	small := fixture.submit(t, marketOrder(fixture.id(600), "account-1", SideBuy, "0.001", nil))
	assertOrderStatus(t, small, OrderStatusFilled)
	if small.FilledQuantity != "0.001" || small.AverageFillPrice != "60000" {
		t.Fatalf("small fill = %s @ %s, want 0.001 @ 60000", small.FilledQuantity, small.AverageFillPrice)
	}

	large := fixture.submit(t, marketOrder(fixture.id(601), "account-1", SideBuy, "0.01", nil))
	assertOrderStatus(t, large, OrderStatusFilled)
	if large.FilledQuantity != "0.01" {
		t.Fatalf("large filled quantity = %s, want 0.01", large.FilledQuantity)
	}
	if got, want := large.AverageFillPrice, "60140"; got != want {
		t.Fatalf("large depth VWAP = %s, want exact %s", got, want)
	}
}

// Ported from:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/trading/e2e_large_market_fill.rs:43
//	test: oversized_market_order_fills_fully_without_book_exhaustion_panic
//
// Adaptations:
//   - Retry/polling is replaced by one synchronous engine decision.
//
// Assertions preserved:
//   - Oversized market order fills its entire quantity.
//   - Displayed B-book depth does not cap the economic fill.
func TestTradingOversizedMarketOrderFillsFullyWithoutBookExhaustion(t *testing.T) {
	fixture := newTradingFixture(t)
	fixture.updateBook(t, "100",
		[]BookLevel{{Price: "99", Quantity: "1"}},
		[]BookLevel{{Price: "100", Quantity: "1"}},
	)
	order := fixture.submit(t, marketOrder(fixture.id(610), "account-1", SideBuy, "10", nil))
	assertOrderStatus(t, order, OrderStatusFilled)
	if order.FilledQuantity != "10" || order.AverageFillPrice != "100" {
		t.Fatalf("oversized fill = %s @ %s, want 10 @ 100", order.FilledQuantity, order.AverageFillPrice)
	}
}

// Ported from:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/trading/e2e_slippage_band.rs:495
//	test: market_fills_buy_at_ask_sell_at_bid
//
// Adaptations:
//   - Mark and quote injection are one ordered deterministic market input.
//   - Float tolerances are strengthened to exact prices.
//
// Assertions preserved:
//   - Market buy fills at ask.
//   - Market sell fills at bid.
//   - Buy fill exceeds sell fill on the same book.
func TestTradingMarketFillsBuyAtAskSellAtBid(t *testing.T) {
	fixture := newTradingFixture(t)
	fixture.updateBook(t, "60000",
		[]BookLevel{{Price: "59000", Quantity: "1"}},
		[]BookLevel{{Price: "61000", Quantity: "1"}},
	)
	wideBand := bps(1_000_000)
	buy := fixture.submit(t, marketOrder(fixture.id(620), "account-1", SideBuy, "0.001", wideBand))
	sell := fixture.submit(t, marketOrder(fixture.id(621), "account-1", SideSell, "0.001", wideBand))
	if buy.AverageFillPrice != "61000" || sell.AverageFillPrice != "59000" {
		t.Fatalf("buy/sell prices = %s/%s, want ask/bid 61000/59000", buy.AverageFillPrice, sell.AverageFillPrice)
	}
}

// Ported from:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/trading/e2e_slippage_band.rs:551
//	test: fill_pricer_rejects_out_of_band_and_admits_in_band
//
// Adaptations:
//   - Cache and quote polling are replaced by explicit ordered book/mark inputs.
//
// Assertions preserved:
//   - Out-of-band buy is rejected with slippage_exceeded and no fill price.
//   - In-band buy fills at the exact ask.
func TestTradingFillPricerRejectsOutOfBandAndAdmitsInBand(t *testing.T) {
	fixture := newTradingFixture(t)
	band := bps(50)

	fixture.updateBook(t, "60000",
		[]BookLevel{{Price: "60900", Quantity: "1"}},
		[]BookLevel{{Price: "61000", Quantity: "1"}},
	)
	rejected, decision := fixture.submitDecision(t, marketOrder(
		fixture.id(630), "account-1", SideBuy, "0.001", band,
	))
	if rejected.Status != OrderStatusRejected ||
		rejected.RejectReason != RejectionSlippageExceeded ||
		rejected.AverageFillPrice != "" ||
		len(decision.Fills) != 0 {
		t.Fatalf("out-of-band result = %+v fills=%+v", rejected, decision.Fills)
	}

	fixture.updateBook(t, "60000",
		[]BookLevel{{Price: "60000", Quantity: "1"}},
		[]BookLevel{{Price: "60100", Quantity: "1"}},
	)
	accepted := fixture.submit(t, marketOrder(
		fixture.id(631), "account-1", SideBuy, "0.001", band,
	))
	assertOrderStatus(t, accepted, OrderStatusFilled)
	if accepted.AverageFillPrice != "60100" {
		t.Fatalf("in-band fill price = %s, want 60100", accepted.AverageFillPrice)
	}
}

// Ported from:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/trading/e2e_slippage_band.rs:385
//	test: market_slippage_band_is_enforced_on_sell
//
// Adaptations:
//   - Live mark/quote updates are explicit synchronous market inputs.
//   - Float tolerances are strengthened to exact prices.
//
// Assertions preserved:
//   - Below-floor sell is rejected with slippage_exceeded and no fill.
//   - In-band and favorable sells fill at their exact bids.
func TestTradingMarketSlippageBandIsEnforcedOnSell(t *testing.T) {
	fixture := newTradingFixture(t)
	band := bps(50)

	fixture.updateBook(t, "60000",
		[]BookLevel{{Price: "59000", Quantity: "1"}},
		[]BookLevel{{Price: "59100", Quantity: "1"}},
	)
	rejected, decision := fixture.submitDecision(t, marketOrder(
		fixture.id(640), "account-1", SideSell, "0.001", band,
	))
	if rejected.Status != OrderStatusRejected ||
		rejected.RejectReason != RejectionSlippageExceeded ||
		len(decision.Fills) != 0 {
		t.Fatalf("below-floor sell result = %+v fills=%+v", rejected, decision.Fills)
	}

	fixture.updateBook(t, "60000",
		[]BookLevel{{Price: "59800", Quantity: "1"}},
		[]BookLevel{{Price: "59900", Quantity: "1"}},
	)
	inBand := fixture.submit(t, marketOrder(
		fixture.id(641), "account-1", SideSell, "0.001", band,
	))
	if inBand.AverageFillPrice != "59800" {
		t.Fatalf("in-band sell = %s, want 59800", inBand.AverageFillPrice)
	}

	fixture.updateBook(t, "60000",
		[]BookLevel{{Price: "60500", Quantity: "1"}},
		[]BookLevel{{Price: "60600", Quantity: "1"}},
	)
	favorable := fixture.submit(t, marketOrder(
		fixture.id(642), "account-1", SideSell, "0.001", band,
	))
	if favorable.AverageFillPrice != "60500" {
		t.Fatalf("favorable sell = %s, want 60500", favorable.AverageFillPrice)
	}
}

func TestTradingIOCPartiallyFillsUntilSlippageBoundary(t *testing.T) {
	fixture := newTradingFixture(t)
	fixture.updateBook(t, "60000",
		[]BookLevel{{Price: "59900", Quantity: "1"}},
		[]BookLevel{
			{Price: "60000", Quantity: "0.5"},
			{Price: "60100", Quantity: "5"},
		},
	)
	command := marketOrder(
		fixture.id(650),
		"account-1",
		SideBuy,
		"2",
		bps(10),
	)
	command.TimeInForce = TimeInForceIOC
	order, decision := fixture.submitDecision(t, command)
	if decision.CommandResult.Status != CommandStatusAccepted {
		t.Fatalf("IOC decision = %+v, want accepted partial execution", decision.CommandResult)
	}
	if order.Status != OrderStatusCancelled ||
		order.FilledQuantity != "0.5" ||
		order.AverageFillPrice != "60000" {
		t.Fatalf("IOC order = %+v, want cancelled after 0.5 @ 60000", order)
	}
	if len(decision.Fills) != 1 ||
		decision.Fills[0].Price != "60000" ||
		decision.Fills[0].Quantity != "0.5" {
		t.Fatalf("IOC fills = %+v, want one 0.5 @ 60000 fill", decision.Fills)
	}
}

func TestTradingSlippageReferenceIsBoundAtAdmission(t *testing.T) {
	fixture := newTradingFixture(t)
	command := SubmitOrder{
		OrderID:        fixture.id(660),
		AccountID:      "account-1",
		InstrumentID:   "BTC-PERP",
		Side:           SideBuy,
		Type:           OrderTypeStopMarket,
		TimeInForce:    TimeInForceGTC,
		Quantity:       "1",
		TriggerPrice:   "110",
		MaxSlippageBPS: bps(50),
	}
	order := fixture.submit(t, command)
	if order.SlippageReference != "100" {
		t.Fatalf("admission slippage reference = %s, want 100", order.SlippageReference)
	}

	decision := fixture.updateBook(t, "200",
		[]BookLevel{{Price: "109", Quantity: "1"}},
		[]BookLevel{{Price: "110", Quantity: "1"}},
	)
	order, ok := fixture.state.Order(command.OrderID)
	if !ok {
		t.Fatalf("triggered stop order %s is missing", command.OrderID)
	}
	if order.Status != OrderStatusRejected ||
		order.RejectReason != RejectionSlippageExceeded ||
		order.SlippageReference != "100" {
		t.Fatalf("triggered stop order = %+v, want rejection against bound reference 100", order)
	}
	if len(decision.Fills) != 0 {
		t.Fatalf("triggered stop fills = %+v, want none", decision.Fills)
	}
}

func marketOrder(
	orderID ID,
	accountID string,
	side Side,
	quantity string,
	maxSlippageBPS *uint32,
) SubmitOrder {
	return SubmitOrder{
		OrderID:        orderID,
		AccountID:      accountID,
		InstrumentID:   "BTC-PERP",
		Side:           side,
		Type:           OrderTypeMarket,
		TimeInForce:    TimeInForceGTC,
		Quantity:       quantity,
		MaxSlippageBPS: maxSlippageBPS,
	}
}

func bps(value uint32) *uint32 {
	return &value
}

func (fixture *tradingFixture) updateBook(
	t *testing.T,
	markPrice string,
	bids []BookLevel,
	asks []BookLevel,
) Decision {
	t.Helper()
	return fixture.apply(t, TradingAction{
		Kind: TradingActionUpdateBook,
		UpdateBook: &UpdateBook{
			InstrumentID: "BTC-PERP",
			MarkPrice:    markPrice,
			Bids:         bids,
			Asks:         asks,
		},
	})
}

func (fixture *tradingFixture) submitDecision(
	t *testing.T,
	command SubmitOrder,
) (OrderSnapshot, Decision) {
	t.Helper()
	decision := fixture.applyDecision(t, TradingAction{
		Kind:        TradingActionSubmitOrder,
		SubmitOrder: &command,
	})
	order, ok := fixture.state.Order(command.OrderID)
	if !ok {
		t.Fatalf("processed order %s is missing", command.OrderID)
	}
	return order, decision
}
