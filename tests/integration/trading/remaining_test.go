package trading

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func requireScopeResult(t *testing.T, fixture *remainingTradingFixture, accountID, venue string, cases map[string]bool) {
	t.Helper()
	fixture.venues[accountID] = venue
	for symbol, accepted := range cases {
		err := fixture.submitScoped(accountID, symbol)
		if accepted && err != nil {
			t.Fatalf("%s should admit %s: %v", venue, symbol, err)
		}
		if !accepted {
			viewErr, ok := err.(*tradingViewError)
			if !ok || viewErr.Code != "asset_scope_denied" ||
				!strings.Contains(viewErr.Message, "account venue "+venue) {
				t.Fatalf("%s should deny %s with scoped error: %#v", venue, symbol, err)
			}
		}
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/trading/e2e_asset_scope.rs:149
//	test: submit_enforces_account_venue_scope
func TestSubmitEnforcesAccountVenueScope(t *testing.T) {
	fixture := newRemainingTradingFixture()
	requireScopeResult(t, fixture, "hl", "hyperliquid", map[string]bool{
		"BTC-PERP": true, "XAU-PERP": false, "XAU-FUT": false, "PRESIDENT-YES": false,
	})
	requireScopeResult(t, fixture, "cfd", "fix_cfd", map[string]bool{
		"XAU-PERP": true, "XAU-FUT": false, "BTC-PERP": false, "PRESIDENT-YES": false,
	})
	requireScopeResult(t, fixture, "fut", "fix_futures", map[string]bool{
		"XAU-FUT": true, "XAU-PERP": false, "BTC-PERP": false, "PRESIDENT-YES": false,
	})
	requireScopeResult(t, fixture, "poly", "polymarket", map[string]bool{
		"PRESIDENT-YES": true, "BTC-PERP": false, "XAU-PERP": false, "XAU-FUT": false,
	})
	requireScopeResult(t, fixture, "kalshi", "kalshi", map[string]bool{
		"PRESIDENT-YES": true, "BTC-PERP": false,
	})
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/trading/e2e_bracket_saga.rs:40
//	test: place_bracket_arms_the_submit_saga_on_the_entry_leg
func TestPlaceBracketArmsTheSubmitSagaOnTheEntryLeg(t *testing.T) {
	fixture := newRemainingTradingFixture()
	entryID, err := fixture.placeBracket("acct-1", "br-1")
	if err != nil {
		t.Fatal(err)
	}
	saga, ok := fixture.sagas[entryID]
	if !ok || saga.Type != "submit_order" || saga.Status != "running" || saga.CorrelationID != entryID {
		t.Fatalf("entry=%q saga=%#v present=%v", entryID, saga, ok)
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/trading/e2e_close_only.rs:34
//	test: submit_enforces_trading_mode
func TestSubmitEnforcesTradingMode(t *testing.T) {
	fixture := newRemainingTradingFixture()
	if response := fixture.login("user-1", "correct horse battery staple"); response.Status != 200 {
		t.Fatalf("login=%#v", response)
	}
	if status := fixture.submitByTradingMode("BTC-PERP", true); status < 200 || status >= 300 {
		t.Fatalf("full status=%d", status)
	}
	fixture.tradingModes["BTC-PERP"] = "disabled"
	if status := fixture.submitByTradingMode("BTC-PERP", true); status != 400 {
		t.Fatalf("disabled status=%d", status)
	}
	fixture.tradingModes["BTC-PERP"] = "close_only"
	if status := fixture.submitByTradingMode("BTC-PERP", true); status != 400 {
		t.Fatalf("close-only opening status=%d", status)
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/trading/e2e_funding.rs:60
//	test: funding_history_reads_paginates_and_aggregates
func TestFundingHistoryReadsPaginatesAndAggregates(t *testing.T) {
	fixture := newRemainingTradingFixture()
	fixture.seedFunding("acct-1", "BTC-PERP", "pos-1", "pos-1:300", "-10.000", 300)
	fixture.seedFunding("acct-1", "BTC-PERP", "pos-1", "pos-1:200", "5.0", 200)
	fixture.seedFunding("acct-1", "BTC-PERP", "pos-1", "pos-1:100", "-2.00", 100)
	page1, err := fixture.fundingPage("acct-1", 2, "")
	if err != nil || len(page1.Items) != 2 || intValue(page1.Total) != 3 || page1.NextCursor == "" {
		t.Fatalf("page1=%#v err=%v", page1, err)
	}
	newest := page1.Items[0]
	if newest.Amount != "-2" || newest.PositionID != positionURN("pos-1") ||
		newest.Symbol != "BTC-PERP" || newest.OraclePrice != "1000" ||
		newest.Rate != "0.0000125" || newest.SignedQuantity != "1" ||
		newest.Currency != "USDC" || !strings.Contains(newest.FundingTime.Format(time.RFC3339), "T") ||
		page1.Items[1].Amount != "5" {
		t.Fatalf("page1=%#v", page1)
	}
	page2, err := fixture.fundingPage("acct-1", 2, page1.NextCursor)
	if err != nil || len(page2.Items) != 1 || page2.Items[0].Amount != "-10" {
		t.Fatalf("page2=%#v err=%v", page2, err)
	}
	if sum := fixture.fundingPaid("pos-1", time.Time{}); sum != "-7" {
		t.Fatalf("all-time funding=%s", sum)
	}
	if sum := fixture.fundingPaid("pos-1", fixture.now.Add(-250*time.Second)); sum != "3" {
		t.Fatalf("cycle funding=%s", sum)
	}
	fixture.seedFunding("acct-2", "BTC-PERP", "pos-2", "pos-2:100", "-3", 100)
	fleet := fixture.fleetFunding("BTC-PERP")
	accounts := make(map[string]bool)
	for _, row := range fleet {
		accounts[row.Account] = true
	}
	if len(fleet) != 4 || !accounts["acct-1"] || !accounts["acct-2"] {
		t.Fatalf("fleet=%#v accounts=%#v", fleet, accounts)
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/trading/e2e_order_control.rs:45
//	test: control_saga_reissues_only_while_the_order_is_live
func TestControlSagaReissuesOnlyWhileTheOrderIsLive(t *testing.T) {
	fixture := newRemainingTradingFixture()
	fixture.insertControlOrder("order-1")
	base := fixture.cancelEvents
	fixture.reissueCancel("order-1", false)
	if fixture.cancelEvents != base+1 {
		t.Fatalf("live cancel events=%d", fixture.cancelEvents)
	}
	fixture.reissueCancel("order-1", true)
	if fixture.cancelEvents != base+1 {
		t.Fatalf("final-attempt cancel events=%d", fixture.cancelEvents)
	}
	if !fixture.cancelOrder("order-1") {
		t.Fatal("order did not reach canceled")
	}
	fixture.reissueCancel("order-1", false)
	if fixture.cancelEvents != base+1 {
		t.Fatalf("terminal cancel events=%d", fixture.cancelEvents)
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/trading/e2e_rate_limit.rs:12
//	test: protected_surface_is_per_principal_rate_limited
func TestProtectedSurfaceIsPerPrincipalRateLimited(t *testing.T) {
	fixture := newRemainingTradingFixture()
	if response := fixture.login("user-1", "correct horse battery staple"); response.Status != 200 {
		t.Fatalf("first login=%#v", response)
	}
	for i := 0; i < fixture.rateMax; i++ {
		if response := fixture.protectedRequest("user-1"); response.Status != 200 {
			t.Fatalf("under-limit response=%#v", response)
		}
	}
	limited := fixture.protectedRequest("user-1")
	if limited.Status != 429 || limited.RetryAfter == 0 || limited.Code != "too_many_requests" {
		t.Fatalf("limited=%#v", limited)
	}
	if response := fixture.login("user-2", "correct horse battery staple"); response.Status != 200 {
		t.Fatalf("second login=%#v", response)
	}
	if response := fixture.protectedRequest("user-2"); response.Status != 200 {
		t.Fatalf("second principal response=%#v", response)
	}
	for i := 0; i < fixture.rateMax+5; i++ {
		if response := fixture.publicCatalogRequest(); response.Status == 429 {
			t.Fatalf("public response=%#v", response)
		}
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/trading/e2e_rest.rs:10
//	test: trader_trading_flow_transport
func TestTraderTradingFlowTransport(t *testing.T) {
	fixture := newRemainingTradingFixture()
	if !slices.Contains(fixture.publicInstruments(), "BTC-PERP") {
		t.Fatal("public catalog omitted BTC-PERP")
	}
	if response := fixture.login("user-1", "correct horse battery staple"); response.Status != 200 {
		t.Fatalf("login=%#v", response)
	}
	accepted := fixture.restSubmit("user-1", "urn:uzo:account:acct-1", "intent-1", "BTC-PERP")
	if accepted.Status != 202 || !strings.HasPrefix(accepted.OrderID, "urn:xb:order:") {
		t.Fatalf("accepted=%#v", accepted)
	}
	status, orders := fixture.restOrdersFor("user-1", "acct-1")
	if status != 200 || len(orders) != 1 || orders[0].Status != "pending" || orders[0].IntentID != "intent-1" {
		t.Fatalf("status=%d orders=%#v", status, orders)
	}
	status, positions := fixture.restPositions("user-1", "urn:uzo:account:acct-1")
	if status != 200 || len(positions) != 0 {
		t.Fatalf("positions status=%d rows=%#v", status, positions)
	}
	status, balances := fixture.restBalances("user-1", "acct-1")
	if status != 200 || len(balances) == 0 {
		t.Fatalf("balances status=%d rows=%#v", status, balances)
	}
	if response := fixture.restSubmit("user-1", "urn:uzo:account:acct-2", "intent-1", "BTC-PERP"); response.Status != 403 {
		t.Fatalf("foreign account response=%#v", response)
	}
	if status, _ := fixture.restPositions("", "urn:uzo:account:acct-1"); status != 401 {
		t.Fatalf("unauthenticated status=%d", status)
	}
	if status, _ := fixture.restPositions("user-1", "not-a-urn"); status != 400 {
		t.Fatalf("malformed account status=%d", status)
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/trading/e2e_slippage.rs:47
//	test: form_market_band_resolves_override_default_and_limit_carries_none
func TestFormMarketBandResolvesOverrideDefaultAndLimitCarriesNone(t *testing.T) {
	fixture := newRemainingTradingFixture()
	fixture.submitWithSlippage("slip-override", "MARKET", "25")
	fixture.submitWithSlippage("slip-default", "MARKET", "")
	fixture.submitWithSlippage("slip-limit", "LIMIT", "25")
	if fixture.slippage["slip-override"] != "25" ||
		fixture.slippage["slip-default"] != "50" ||
		fixture.slippage["slip-limit"] != "" {
		t.Fatalf("slippage=%#v", fixture.slippage)
	}
}
