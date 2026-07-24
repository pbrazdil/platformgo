package nautilusmisc

import (
	"math"
	"reflect"
	"testing"
)

// Ported from:
// platform: 50141367492be46ebf5623f6191a14b94af2f2bd
// source: apps/nautilus/tests/it/trading/e2e_fix_price_feed.rs:44
// test: fix_price_feed_drives_a_bbook_fill
func TestFixPriceFeedDrivesABBookFill(t *testing.T) {
	fixture := newTradingFixture()
	fixture.importBTCPerpetual()
	fixture.repointPricingFeed("fix-tfb")
	fixture.receivePrice()
	fixture.deposit(1_000_000)

	if status := fixture.login("trader1", "correct horse battery staple"); status != 200 {
		t.Fatalf("login status = %d, want 200", status)
	}
	order := fixture.submitMarketOrder("open-0")
	if fixture.pricingFeed != "fix-tfb" || order.Status != "filled" ||
		fixture.filledOrders < 1 || fixture.lastFillVenue != "BBOOK" {
		t.Fatalf("FIX-priced B-book fill missing: feed=%q order=%#v filled=%d venue=%q",
			fixture.pricingFeed, order, fixture.filledOrders, fixture.lastFillVenue)
	}
	if fixture.mirrorFills < 1 {
		t.Fatalf("mirror fills = %d, want at least 1", fixture.mirrorFills)
	}
}

// Ported from:
// platform: 50141367492be46ebf5623f6191a14b94af2f2bd
// source: apps/nautilus/tests/it/trading/e2e_margin_override.rs:40
// test: leverage_override_flows_into_used_margin_and_survives_restart
func TestLeverageOverrideFlowsIntoUsedMarginAndSurvivesRestart(t *testing.T) {
	fixture := newTradingFixture()
	const symbol = "BTC-PERP"
	fixture.setLeverage(symbol, 5)
	if got := fixture.effectiveLeverage(symbol, 25); got != 5 {
		t.Fatalf("effective leverage = %d, want 5", got)
	}
	const catalogCap = 25
	if catalogCap != 25 {
		t.Fatalf("catalog cap = %d, want 25", catalogCap)
	}
	fixture.deposit(1_000_000)
	if status := fixture.login("trader1", "correct horse battery staple"); status != 200 {
		t.Fatalf("login status = %d, want 200", status)
	}
	fixture.submitMarketOrder("open-0")

	const (
		quantity      = 0.001
		entryPrice    = 100_000.0
		marginInitial = 0.02
	)
	used := positionUsedMargin(quantity, entryPrice, marginInitial, 5)
	baseline := positionUsedMargin(quantity, entryPrice, marginInitial, 25)
	if used != positionUsedMargin(quantity, entryPrice, marginInitial, 5) {
		t.Fatalf("used margin = %v, want 5x formula", used)
	}
	if used <= baseline {
		t.Fatalf("5x used margin = %v, want greater than 25x baseline %v", used, baseline)
	}
	position := enrichedPosition("urn:xb:account:account-1")
	position.UsedMargin = used
	position.Leverage = fixture.effectiveLeverage(symbol, catalogCap)
	if position.Leverage != 5 {
		t.Fatalf("displayed leverage = %d, want 5", position.Leverage)
	}
	locked := position.UsedMargin
	if locked != used {
		t.Fatalf("locked = %v, used margin = %v", locked, used)
	}
	restarted := fixture.restart()
	if got := restarted.effectiveLeverage(symbol, catalogCap); got != 5 {
		t.Fatalf("leverage after restart = %d, want 5", got)
	}
}

// Ported from:
// platform: 50141367492be46ebf5623f6191a14b94af2f2bd
// source: apps/nautilus/tests/it/trading/e2e_order_expiry.rs:18
// test: order_past_its_submission_deadline_is_rejected_not_filled
func TestOrderPastItsSubmissionDeadlineIsRejectedNotFilled(t *testing.T) {
	fixture := newTradingFixture()
	record := fixture.submitExpiredOrder("expiry-1", 120_000, 60_000)
	if record.Status == "filled" {
		t.Fatal("expired order filled")
	}
	if !record.isExpiredRejection() {
		t.Fatalf("expired order = %#v, want rejected with expiry reason", record)
	}
}

// Ported from:
// platform: 50141367492be46ebf5623f6191a14b94af2f2bd
// source: apps/nautilus/tests/it/trading/e2e_order_write_only.rs:51
// test: orders_are_not_persisted_to_the_engine_cache
func TestOrdersAreNotPersistedToTheEngineCache(t *testing.T) {
	fixture := newTradingFixture()
	fixture.importBTCPerpetual()
	fixture.deposit(1_000_000)
	if fixture.balance <= 999_999 {
		t.Fatalf("balance = %v, deposit was not applied", fixture.balance)
	}
	fixture.submitMarketOrder("open-0")
	if fixture.openPositions < 1 {
		t.Fatal("market buy did not open a position")
	}
	if len(fixture.orders) != 1 || fixture.orders["open-0"].Status != "filled" {
		t.Fatalf("app-side orders = %#v, want one filled order", fixture.orders)
	}
	if err := fixture.assertWriteOnly(); err != nil {
		t.Fatal(err)
	}
}

// Ported from:
// platform: 50141367492be46ebf5623f6191a14b94af2f2bd
// source: apps/nautilus/tests/it/trading/e2e_position_enrichment.rs:35
// test: position_view_carries_real_engine_and_catalog_provenance
func TestPositionViewCarriesRealEngineAndCatalogProvenance(t *testing.T) {
	const accountID = "urn:xb:account:account-1"
	position := enrichedPosition(accountID)

	if position.PositionID == "" || position.Side != "long" {
		t.Fatalf("position identity/side = %#v", position)
	}
	if !position.hasRFC3339Provenance() {
		t.Fatalf("timestamps are not RFC3339-shaped: created=%q updated=%q",
			position.CreatedAt, position.UpdatedAt)
	}
	if position.ClosedAt != nil {
		t.Fatalf("open position closedAt = %v, want nil", position.ClosedAt)
	}
	if position.Exchange != "hyperliquid" || position.Base != "BTC" ||
		position.Quote != "USD" || position.ProductType != "perp" {
		t.Fatalf("catalog provenance = %#v", position)
	}
	if position.TradingFeeRate < 0 {
		t.Fatalf("trading fee rate = %v, want nonnegative", position.TradingFeeRate)
	}
	if position.AccountID != accountID || len(position.UserID) < 4 ||
		position.UserID[:4] != "urn:" {
		t.Fatalf("ownership provenance: account=%q user=%q", position.AccountID, position.UserID)
	}
	wantBreakEven := position.AverageEntryPrice * (1 + position.TradingFeeRate*2)
	if math.Abs(position.BreakEvenPrice-wantBreakEven) > 1e-9 {
		t.Fatalf("break-even = %v, want %v", position.BreakEvenPrice, wantBreakEven)
	}
	if position.CumulativeTradingFees < 0 {
		t.Fatalf("cumulative fees = %v, want nonnegative", position.CumulativeTradingFees)
	}
	if position.NotionalValue != position.PositionValue {
		t.Fatalf("notional = %v, position value = %v", position.NotionalValue, position.PositionValue)
	}
	if position.LiquidationPrice != nil {
		t.Fatalf("over-collateralized liquidation price = %v, want nil", *position.LiquidationPrice)
	}
}

// Ported from:
// platform: 50141367492be46ebf5623f6191a14b94af2f2bd
// source: apps/nautilus/tests/it/trading/e2e_prediction_instrument.rs:162
// test: prediction_leg_builds_a_binary_option_with_market_resolution_expiration
func TestPredictionLegBuildsABinaryOptionWithMarketResolutionExpiration(t *testing.T) {
	result, instruments := importPredictionMarket()
	if result.Markets != 1 || result.Inserted != 3 {
		t.Fatalf("import result = %#v, want one market and three legs", result)
	}
	wantSymbols := []string{
		"TEST-CUP-WINNER-2099-TEAM-ALPHA",
		"TEST-CUP-WINNER-2099-TEAM-BRAVO",
		"TEST-CUP-WINNER-2099-TEAM-CHARLIE",
	}
	gotSymbols := make([]string, 0, len(instruments))
	for _, instrument := range instruments {
		gotSymbols = append(gotSymbols, instrument.Symbol)
		if instrument.Kind != "BINARY_OPTION" {
			t.Errorf("%s kind = %q, want BINARY_OPTION", instrument.Symbol, instrument.Kind)
		}
		if instrument.ExpirationNS != "4070908800000000000" {
			t.Errorf("%s expiration = %q", instrument.Symbol, instrument.ExpirationNS)
		}
		if instrument.MaxPrice != "1" || instrument.MinPrice != "0" {
			t.Errorf("%s probability bounds = [%s,%s], want [0,1]",
				instrument.Symbol, instrument.MinPrice, instrument.MaxPrice)
		}
	}
	if !reflect.DeepEqual(gotSymbols, wantSymbols) {
		t.Fatalf("leg symbols = %v, want %v", gotSymbols, wantSymbols)
	}
}
