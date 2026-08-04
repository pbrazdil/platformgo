package postgres_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
	"github.com/upcomers-org/platformgo/internal/edge"
)

const (
	predictionPublicVenue           = "polymarket"
	predictionPublicEventKey        = "test-cup-winner-2099"
	predictionPublicEventTitle      = "Test Cup Winner 2099"
	predictionPublicMarketKey       = "test-cup-winner-2099"
	predictionPublicMarketQuestion  = "Who wins the Test Cup 2099?"
	predictionPublicBinaryKey       = "0xtest-will-it-rain"
	predictionPublicBinaryQuestion  = "Will it rain on test day?"
	predictionTieKalshiKey          = "tie-kalshi-market"
	predictionTiePolymarketKey      = "tie-polymarket-market"
	predictionDisabledOnlyMarketKey = "disabled-only-market"
	predictionLegTieMarketKey       = "tie-leg-market"
	predictionLegTieLowInstrument   = "TIE-LEG-AA"
	predictionLegTieHighInstrument  = "TIE-LEG-ZZ"
	predictionCorruptMarketKey      = "corrupt-read-model"
)

var (
	predictionPublicCandidates = []struct {
		symbol string
		label  string
	}{
		{symbol: "TEST-CUP-WINNER-2099-TEAM-ALPHA", label: "Team Alpha"},
		{symbol: "TEST-CUP-WINNER-2099-TEAM-BRAVO", label: "Team Bravo"},
		{symbol: "TEST-CUP-WINNER-2099-TEAM-CHARLIE", label: "Team Charlie"},
	}
	predictionPublicBinaryOutcomes = []struct {
		symbol string
		label  string
	}{
		{symbol: "TEST-WILL-IT-RAIN-YES", label: "Yes"},
		{symbol: "TEST-WILL-IT-RAIN-NO", label: "No"},
	}
)

// Ported from:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/catalog/e2e_prediction_trader.rs:60
//	test: prediction_public_list_nests_legs_under_market_and_event
//
// Adaptations:
//   - The Rust application boot and query dispatcher are replaced by the
//     native PostgreSQL compatibility reader.
//   - Explicit UUIDs, timestamps, instrument scales, and SQL rows replace the
//     Rust testkit persistence helpers.
//   - Price and size increments are derived from authoritative instrument
//     scales; prediction metadata stores no economic values.
//
// Assertions preserved:
//   - Both markets surface because each has at least one enabled leg.
//   - The categorical market nests its event, excludes the disabled third leg,
//     and orders the remaining legs by outcome index.
//   - The binary market has no event or resolution time and exposes Yes + No.
//
// Strengthened deterministic assertions:
//   - Market order, optional field omission, display names, and exact
//     increments are checked against the static source fixture.
func TestPostgresPredictionPublicListNestsLegsUnderMarketAndEvent(t *testing.T) {
	ctx := context.Background()
	pool := currentStorePool(t)
	seedPredictionPublicCatalog(t, pool)

	markets, err := platformpostgres.NewCompatibilityStore(pool).PredictionMarkets(ctx)
	if err != nil {
		t.Fatalf("read prediction markets: %v", err)
	}
	if len(markets) != 2 {
		t.Fatalf("market count = %d, want 2", len(markets))
	}
	if markets[0].MarketKey != predictionPublicMarketKey ||
		markets[1].MarketKey != predictionPublicBinaryKey {
		t.Fatalf("source query market order = %#v", markets)
	}

	categorical := predictionMarketByKey(markets, predictionPublicMarketKey)
	if categorical == nil {
		t.Fatal("categorical market is absent")
	}
	if categorical.SourceVenue != predictionPublicVenue ||
		categorical.Question != predictionPublicMarketQuestion ||
		!categorical.MutuallyExclusive || categorical.Status != "open" ||
		categorical.ResolutionTime == nil || categorical.StageLabel != nil ||
		categorical.StageOrdinal == nil || *categorical.StageOrdinal != 0 {
		t.Fatalf("categorical market metadata = %#v", categorical)
	}
	if categorical.Event == nil {
		t.Fatal("categorical event is absent")
	}
	if categorical.Event.EventKey != predictionPublicEventKey ||
		categorical.Event.Title != predictionPublicEventTitle ||
		categorical.Event.Status != "open" || categorical.Event.Series != nil {
		t.Fatalf("categorical event = %#v", categorical.Event)
	}
	if len(categorical.Legs) != 2 {
		t.Fatalf("categorical leg count = %d, want 2", len(categorical.Legs))
	}
	for index, want := range predictionPublicCandidates[:2] {
		leg := categorical.Legs[index]
		if leg.Symbol != want.symbol || leg.DisplayName != want.symbol ||
			leg.OutcomeIndex != index || leg.OutcomeLabel != want.label ||
			leg.PriceIncrement != "0.01" || leg.SizeIncrement != "1" {
			t.Fatalf("categorical leg %d = %#v", index, leg)
		}
	}
	assertPredictionMarketOptionalJSON(t, categorical, true)

	binary := predictionMarketByKey(markets, predictionPublicBinaryKey)
	if binary == nil {
		t.Fatal("binary market is absent")
	}
	if binary.SourceVenue != predictionPublicVenue ||
		binary.Question != predictionPublicBinaryQuestion || binary.MutuallyExclusive ||
		binary.Status != "open" || binary.ResolutionTime != nil ||
		binary.StageLabel != nil || binary.StageOrdinal != nil || binary.Event != nil {
		t.Fatalf("binary market metadata = %#v", binary)
	}
	if len(binary.Legs) != 2 {
		t.Fatalf("binary leg count = %d, want 2", len(binary.Legs))
	}
	for index, want := range predictionPublicBinaryOutcomes {
		leg := binary.Legs[index]
		if leg.Symbol != want.symbol || leg.DisplayName != want.symbol ||
			leg.OutcomeIndex != index || leg.OutcomeLabel != want.label ||
			leg.PriceIncrement != "0.01" || leg.SizeIncrement != "1" {
			t.Fatalf("binary leg %d = %#v", index, leg)
		}
	}
	assertPredictionMarketOptionalJSON(t, binary, false)
}

// Ported from:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/catalog/e2e_prediction_trader.rs:192
//	test: prediction_legs_surface_as_pure_definitions_without_live_price
//
// Adaptations:
//   - The Rust application boot and query dispatcher are replaced by the
//     native PostgreSQL compatibility reader.
//   - Explicit UUIDs, instrument scales, and SQL rows replace the Rust
//     testkit persistence helpers.
//   - There is no live market feed or price fixture; legs are definitions
//     composed from prediction metadata and instrument precision.
//
// Assertions preserved:
//   - The binary market is present.
//   - Yes is outcome 0 and No is also present.
//   - Leg views contain definitions rather than a live price.
//
// Strengthened deterministic assertions:
//   - Both ordered leg definitions expose their exact symbol, display name,
//     label, outcome index, and increments, and the JSON has no price field.
func TestPostgresPredictionLegsSurfaceAsPureDefinitionsWithoutLivePrice(t *testing.T) {
	ctx := context.Background()
	pool := currentStorePool(t)
	seedPredictionPublicCatalog(t, pool)

	markets, err := platformpostgres.NewCompatibilityStore(pool).PredictionMarkets(ctx)
	if err != nil {
		t.Fatalf("read prediction markets: %v", err)
	}
	binary := predictionMarketByKey(markets, predictionPublicBinaryKey)
	if binary == nil {
		t.Fatal("binary market is absent")
	}
	if len(binary.Legs) != len(predictionPublicBinaryOutcomes) {
		t.Fatalf("binary legs = %#v", binary.Legs)
	}
	for index, want := range predictionPublicBinaryOutcomes {
		leg := binary.Legs[index]
		if leg.Symbol != want.symbol || leg.DisplayName != want.symbol ||
			leg.OutcomeIndex != index || leg.OutcomeLabel != want.label ||
			leg.PriceIncrement != "0.01" || leg.SizeIncrement != "1" {
			t.Fatalf("binary leg %d = %#v", index, leg)
		}
		assertPredictionLegHasNoLivePrice(t, leg)
	}
}

// Deterministic strengthening for:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/catalog/e2e_prediction_trader.rs:60
//	test: prediction_public_list_nests_legs_under_market_and_event
//
// Equal stage/time metadata is ordered by a total source-venue, market-key,
// and market-ID tuple instead of insertion order.
func TestPostgresPredictionMarketsUseTotalMarketTieBreak(t *testing.T) {
	ctx := context.Background()
	pool := currentStorePool(t)
	seedPredictionPublicCatalog(t, pool)
	seedPredictionMarketTieRows(t, pool)

	markets, err := platformpostgres.NewCompatibilityStore(pool).PredictionMarkets(ctx)
	if err != nil {
		t.Fatalf("read prediction market ties: %v", err)
	}
	tie := make([]edge.PredictionMarketView, 0, 2)
	for _, market := range markets {
		if market.MarketKey == predictionTieKalshiKey ||
			market.MarketKey == predictionTiePolymarketKey {
			tie = append(tie, market)
		}
	}
	if len(tie) != 2 {
		t.Fatalf("tie markets = %#v", tie)
	}
	if tie[0].SourceVenue != "kalshi" || tie[0].MarketKey != predictionTieKalshiKey ||
		tie[1].SourceVenue != "polymarket" || tie[1].MarketKey != predictionTiePolymarketKey {
		t.Fatalf("tie market order = %#v", tie)
	}
}

// Deterministic strengthening for:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/catalog/e2e_prediction_trader.rs:60
//	test: prediction_public_list_nests_legs_under_market_and_event
//
// A market with no enabled legs is not a public market, even when its parent
// metadata is otherwise valid.
func TestPostgresPredictionMarketsOmitDisabledOnlyMarkets(t *testing.T) {
	ctx := context.Background()
	pool := currentStorePool(t)
	seedPredictionPublicCatalog(t, pool)
	seedPredictionDisabledOnlyMarket(t, pool)

	markets, err := platformpostgres.NewCompatibilityStore(pool).PredictionMarkets(ctx)
	if err != nil {
		t.Fatalf("read disabled-only prediction market: %v", err)
	}
	if predictionMarketByKey(markets, predictionDisabledOnlyMarketKey) != nil {
		t.Fatalf("disabled-only market surfaced: %#v", markets)
	}
}

// Deterministic strengthening for:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/catalog/e2e_prediction_trader.rs:60
//	test: prediction_public_list_nests_legs_under_market_and_event
//
// Equal outcome indexes are ordered by the authoritative instrument ID. The
// fixture drops only a disposable uniqueness constraint on (market_id,
// outcome_index) when the planned schema has one, so the read-model tie-break
// remains testable without changing production schema or migrations.
func TestPostgresPredictionLegsUseTotalInstrumentTieBreak(t *testing.T) {
	ctx := context.Background()
	pool := currentStorePool(t)
	seedPredictionPublicCatalog(t, pool)
	dropPredictionOutcomeUniqueness(t, pool)
	seedPredictionLegTieRows(t, pool)

	markets, err := platformpostgres.NewCompatibilityStore(pool).PredictionMarkets(ctx)
	if err != nil {
		t.Fatalf("read prediction leg ties: %v", err)
	}
	market := predictionMarketByKey(markets, predictionLegTieMarketKey)
	if market == nil {
		t.Fatalf("leg-tie market is absent: %#v", markets)
	}
	if len(market.Legs) != 2 {
		t.Fatalf("leg-tie legs = %#v", market.Legs)
	}
	if market.Legs[0].Symbol != predictionLegTieLowInstrument ||
		market.Legs[1].Symbol != predictionLegTieHighInstrument ||
		market.Legs[0].OutcomeIndex != 0 || market.Legs[1].OutcomeIndex != 0 {
		t.Fatalf("leg tie order = %#v", market.Legs)
	}
}

// Deterministic strengthening for:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/catalog/e2e_prediction_trader.rs:60
//	test: prediction_public_list_nests_legs_under_market_and_event
//
// A malformed enabled leg is a corrupt read-model. The reader must fail the
// whole query and return no valid prefix rather than silently omitting or
// partially returning the remaining markets.
func TestPostgresPredictionMarketsFailClosedOnCorruptOutcomeMetadata(t *testing.T) {
	ctx := context.Background()
	pool := currentStorePool(t)
	seedPredictionPublicCatalog(t, pool)
	relaxPredictionOutcomeMetadataNullability(t, pool)
	seedPredictionCorruptLeg(t, pool)

	markets, err := platformpostgres.NewCompatibilityStore(pool).PredictionMarkets(ctx)
	if err == nil {
		t.Fatalf("corrupt prediction metadata unexpectedly succeeded: %#v", markets)
	}
	if markets != nil {
		t.Fatalf("corrupt prediction metadata returned valid prefix: %#v", markets)
	}
}

// Deterministic strengthening for:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/catalog/e2e_prediction_trader.rs:60
//	test: prediction_public_list_nests_legs_under_market_and_event
//	implementation source: apps/app/src/core/markets/queries/prediction.rs:26-93
//	function: PredictionMarketsHandler::handle
//
// Stage metadata is part of the public DTO. A corrupt negative ordinal or
// empty non-null label must fail the complete PostgreSQL read, including when
// a valid market was inserted earlier in the same disposable database.
func TestPostgresPredictionMarketsFailClosedOnCorruptStageMetadata(t *testing.T) {
	tests := []struct {
		name       string
		constraint string
	}{
		{
			name:       "negative stage ordinal",
			constraint: "prediction_markets_stage_ordinal_check",
		},
		{
			name:       "empty non-null stage label",
			constraint: "prediction_markets_stage_label_check",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			pool := currentStorePool(t)
			relaxPredictionStageMetadataConstraint(t, pool, test.constraint)
			seedPredictionStageMetadataRows(t, pool, test.name)

			markets, err := platformpostgres.NewCompatibilityStore(pool).PredictionMarkets(ctx)
			if err == nil {
				t.Fatalf("corrupt stage metadata unexpectedly succeeded: %#v", markets)
			}
			if markets != nil {
				t.Fatalf("corrupt stage metadata returned valid prefix: %#v", markets)
			}
		})
	}
}

// Deterministic strengthening for:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/catalog/e2e_prediction_trader.rs:60
//	test: prediction_public_list_nests_legs_under_market_and_event
//	implementation source: apps/app/src/core/markets/queries/prediction.rs:26-93
//	function: PredictionMarketsHandler::handle
//
// Chrono provenance for the pinned formatter contract:
//   - .sources/platform/Cargo.lock:2127-2138 resolves chrono 0.4.45.
//   - chrono-0.4.45/src/datetime/mod.rs:634-640 implements
//     DateTime::to_rfc3339 with SecondsFormat::AutoSi and use_z=false.
//   - chrono-0.4.45/src/format/formatting.rs:544-558 defines AutoSi as
//     no fraction for zero, three digits for milliseconds, and six digits for
//     microseconds.
//
// The exact JSON freezes the Rust DTO declaration order, omission of absent
// optional fields, and the UTC +00:00 (rather than Z) timestamp spelling.
func TestPostgresPredictionMarketsRustChronoTimestampGolden(t *testing.T) {
	ctx := context.Background()
	pool := currentStorePool(t)
	seedPredictionTimestampGoldenCatalog(t, pool)

	markets, err := platformpostgres.NewCompatibilityStore(pool).PredictionMarkets(ctx)
	if err != nil {
		t.Fatalf("read prediction timestamp golden: %v", err)
	}
	got, err := json.Marshal(markets)
	if err != nil {
		t.Fatalf("marshal prediction timestamp golden: %v", err)
	}
	want := `[{"sourceVenue":"polymarket","marketKey":"chrono-rfc3339-year-10000","question":"Chrono year 10000","resolutionTime":"+10000-01-01T00:00:00+00:00","mutuallyExclusive":false,"status":"open","legs":[{"symbol":"CHRONO-PREDICTION-YEAR-10000","displayName":"CHRONO-PREDICTION-YEAR-10000","outcomeIndex":0,"outcomeLabel":"Year 10000","priceIncrement":"0.01","sizeIncrement":"1"}]},{"sourceVenue":"polymarket","marketKey":"chrono-rfc3339-seconds","question":"Chrono seconds","resolutionTime":"2099-01-01T00:00:00+00:00","mutuallyExclusive":false,"status":"open","event":{"eventKey":"chrono-event","title":"Chrono event","status":"open"},"legs":[{"symbol":"CHRONO-PREDICTION-SECONDS","displayName":"CHRONO-PREDICTION-SECONDS","outcomeIndex":0,"outcomeLabel":"Seconds","priceIncrement":"0.01","sizeIncrement":"1"}]},{"sourceVenue":"polymarket","marketKey":"chrono-rfc3339-millis","question":"Chrono milliseconds","resolutionTime":"2099-01-01T00:00:00.123+00:00","mutuallyExclusive":false,"status":"open","legs":[{"symbol":"CHRONO-PREDICTION-MILLIS","displayName":"CHRONO-PREDICTION-MILLIS","outcomeIndex":0,"outcomeLabel":"Milliseconds","priceIncrement":"0.01","sizeIncrement":"1"}]},{"sourceVenue":"polymarket","marketKey":"chrono-rfc3339-micros","question":"Chrono microseconds","resolutionTime":"2099-01-01T00:00:00.000100+00:00","mutuallyExclusive":false,"status":"open","legs":[{"symbol":"CHRONO-PREDICTION-MICROS","displayName":"CHRONO-PREDICTION-MICROS","outcomeIndex":0,"outcomeLabel":"Microseconds","priceIncrement":"0.01","sizeIncrement":"1"}]},{"sourceVenue":"polymarket","marketKey":"chrono-rfc3339-absent","question":"Chrono absent resolution","mutuallyExclusive":false,"status":"open","legs":[{"symbol":"CHRONO-PREDICTION-ABSENT","displayName":"CHRONO-PREDICTION-ABSENT","outcomeIndex":0,"outcomeLabel":"Absent","priceIncrement":"0.01","sizeIncrement":"1"}]}]`
	if !bytes.Equal(got, []byte(want)) {
		t.Fatalf("prediction timestamp golden mismatch:\nwant %s\n got %s", want, got)
	}
}

// PostgreSQL accepts years outside Chrono's NaiveDate range. The reader must
// reject such a row and discard an earlier valid prefix rather than exposing
// a partially assembled catalog.
func TestPostgresPredictionMarketsFailClosedOnChronoOutOfRangeResolutionTime(
	t *testing.T,
) {
	ctx := context.Background()
	pool := currentStorePool(t)
	seedPredictionPublicCatalog(t, pool)
	seedPredictionOutOfChronoRangeTimestamp(t, pool)

	markets, err := platformpostgres.NewCompatibilityStore(pool).PredictionMarkets(ctx)
	if err == nil {
		t.Fatalf("out-of-Chrono-range prediction market unexpectedly succeeded: %#v", markets)
	}
	if markets != nil {
		t.Fatalf("out-of-Chrono-range prediction market returned valid prefix: %#v", markets)
	}
}

// Deterministic strengthening for:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/catalog/e2e_prediction_trader.rs:192
//	test: prediction_legs_surface_as_pure_definitions_without_live_price
//	implementation source: apps/app/src/core/markets/queries/prediction.rs:44-63
//	function: PredictionMarketsHandler::handle
//
// The pinned catalog contract permits the NUMERIC(38,18) boundary. This test
// keeps the scale-18 increment exact and rejects any float-mediated formatting.
func TestPostgresPredictionMarketsPreserveScale18ExactIncrement(t *testing.T) {
	ctx := context.Background()
	pool := currentStorePool(t)
	seedPredictionScale18Catalog(t, pool)

	markets, err := platformpostgres.NewCompatibilityStore(pool).PredictionMarkets(ctx)
	if err != nil {
		t.Fatalf("read scale-18 prediction market: %v", err)
	}
	if len(markets) != 1 || len(markets[0].Legs) != 1 {
		t.Fatalf("scale-18 prediction markets = %#v", markets)
	}
	leg := markets[0].Legs[0]
	const want = "0.000000000000000001"
	if leg.PriceIncrement != want || leg.SizeIncrement != want {
		t.Fatalf("scale-18 increments = %#v, want %q for both", leg, want)
	}
}

// Deterministic strengthening for:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/catalog/e2e_prediction_trader.rs:60
//	test: prediction_public_list_nests_legs_under_market_and_event
//
// A fixed PostgreSQL state must produce byte-identical DTO JSON across
// repeated reads, including nested event and leg ordering.
func TestPostgresPredictionMarketsRepeatIdentically(t *testing.T) {
	ctx := context.Background()
	pool := currentStorePool(t)
	seedPredictionPublicCatalog(t, pool)

	var want []byte
	for attempt := 0; attempt < 20; attempt++ {
		store := platformpostgres.NewCompatibilityStore(pool)
		markets, err := store.PredictionMarkets(ctx)
		if err != nil {
			t.Fatalf("repeat prediction read %d: %v", attempt, err)
		}
		got, err := json.Marshal(markets)
		if err != nil {
			t.Fatalf("marshal repeat prediction read %d: %v", attempt, err)
		}
		if attempt == 0 {
			want = got
			continue
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("prediction read %d changed bytes:\nwant %s\n got %s", attempt, want, got)
		}
	}
}

// Statement-snapshot note: PredictionMarkets reads markets, events, legs, and
// instruments through one SQL statement, so PostgreSQL supplies one statement
// snapshot. There is no multi-read interleaving test here; an artificial sleep
// or goroutine would not prove a stronger guarantee.

func predictionMarketByKey(
	markets []edge.PredictionMarketView,
	key string,
) *edge.PredictionMarketView {
	for index := range markets {
		if markets[index].MarketKey == key {
			return &markets[index]
		}
	}
	return nil
}

func assertPredictionMarketOptionalJSON(
	t *testing.T,
	market *edge.PredictionMarketView,
	wantEvent bool,
) {
	t.Helper()
	raw, err := json.Marshal(market)
	if err != nil {
		t.Fatalf("marshal prediction market: %v", err)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatalf("decode prediction market JSON: %v", err)
	}
	if market.StageLabel == nil {
		assertJSONFieldOmitted(t, object, "stageLabel")
	}
	if market.StageOrdinal == nil {
		assertJSONFieldOmitted(t, object, "stageOrdinal")
	} else if _, ok := object["stageOrdinal"]; !ok {
		t.Fatal("stageOrdinal is missing from JSON")
	}
	if market.ResolutionTime == nil {
		assertJSONFieldOmitted(t, object, "resolutionTime")
	} else if _, ok := object["resolutionTime"]; !ok {
		t.Fatal("resolutionTime is missing from JSON")
	}
	if !wantEvent {
		assertJSONFieldOmitted(t, object, "event")
		return
	}
	eventRaw, ok := object["event"]
	if !ok {
		t.Fatal("event is missing from JSON")
	}
	var event map[string]json.RawMessage
	if err := json.Unmarshal(eventRaw, &event); err != nil {
		t.Fatalf("decode prediction event JSON: %v", err)
	}
	assertJSONFieldOmitted(t, event, "series")
}

func assertJSONFieldOmitted(
	t *testing.T,
	object map[string]json.RawMessage,
	field string,
) {
	t.Helper()
	if _, ok := object[field]; ok {
		t.Fatalf("optional JSON field %q was not omitted: %s", field, object[field])
	}
}

func assertPredictionLegHasNoLivePrice(t *testing.T, leg edge.PredictionLegView) {
	t.Helper()
	raw, err := json.Marshal(leg)
	if err != nil {
		t.Fatalf("marshal prediction leg %q: %v", leg.Symbol, err)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatalf("decode prediction leg %q JSON: %v", leg.Symbol, err)
	}
	for _, field := range []string{"price", "size", "lastPrice", "markPrice"} {
		assertJSONFieldOmitted(t, object, field)
	}
}

func seedPredictionPublicCatalog(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		INSERT INTO trading.instruments (
			instrument_id, revision, price_scale, quantity_scale,
			settlement_currency, settlement_currency_scale,
			initial_margin_rate, maintenance_margin_rate, max_leverage,
			maker_fee_rate, taker_fee_rate
		) VALUES
			('TEST-CUP-WINNER-2099-TEAM-ALPHA', 1, 2, 0, 'USDC', 6, 1, 1, 1, 0, 0),
			('TEST-CUP-WINNER-2099-TEAM-BRAVO', 1, 2, 0, 'USDC', 6, 1, 1, 1, 0, 0),
			('TEST-CUP-WINNER-2099-TEAM-CHARLIE', 1, 2, 0, 'USDC', 6, 1, 1, 1, 0, 0),
			('TEST-WILL-IT-RAIN-YES', 1, 2, 0, 'USDC', 6, 1, 1, 1, 0, 0),
			('TEST-WILL-IT-RAIN-NO', 1, 2, 0, 'USDC', 6, 1, 1, 1, 0, 0);

		INSERT INTO trading.prediction_events (
			event_id, source_venue, event_key, title, series, status,
			created_at, updated_at
		) VALUES (
			'00000000-0000-0000-0000-000000000001', 'polymarket',
			'test-cup-winner-2099', 'Test Cup Winner 2099', NULL, 'open',
			'2098-01-01T00:00:00Z', '2098-01-01T00:00:00Z'
		);

		INSERT INTO trading.prediction_markets (
			market_id, source_venue, market_key, question, resolution_time,
			mutually_exclusive, status, event_id, stage_label, stage_ordinal,
			created_at, updated_at
		) VALUES
			(
				'00000000-0000-0000-0000-000000000011', 'polymarket',
				'test-cup-winner-2099', 'Who wins the Test Cup 2099?',
				'2099-01-01T00:00:00Z', true, 'open',
				'00000000-0000-0000-0000-000000000001', NULL, 0,
				'2098-01-01T00:00:01Z', '2098-01-01T00:00:01Z'
			),
			(
				'00000000-0000-0000-0000-000000000012', 'polymarket',
				'0xtest-will-it-rain', 'Will it rain on test day?',
				NULL, false, 'open', NULL, NULL, NULL,
				'2098-01-01T00:00:02Z', '2098-01-01T00:00:02Z'
			);

		INSERT INTO trading.prediction_legs (
			instrument_id, market_id, display_name, outcome_index,
			outcome_label, enabled
		) VALUES
			('TEST-CUP-WINNER-2099-TEAM-ALPHA',
			 '00000000-0000-0000-0000-000000000011',
			 'TEST-CUP-WINNER-2099-TEAM-ALPHA', 0, 'Team Alpha', true),
			('TEST-CUP-WINNER-2099-TEAM-BRAVO',
			 '00000000-0000-0000-0000-000000000011',
			 'TEST-CUP-WINNER-2099-TEAM-BRAVO', 1, 'Team Bravo', true),
			('TEST-CUP-WINNER-2099-TEAM-CHARLIE',
			 '00000000-0000-0000-0000-000000000011',
			 'TEST-CUP-WINNER-2099-TEAM-CHARLIE', 2, 'Team Charlie', false),
			('TEST-WILL-IT-RAIN-YES',
			 '00000000-0000-0000-0000-000000000012',
			 'TEST-WILL-IT-RAIN-YES', 0, 'Yes', true),
			('TEST-WILL-IT-RAIN-NO',
			 '00000000-0000-0000-0000-000000000012',
			 'TEST-WILL-IT-RAIN-NO', 1, 'No', true)
	`); err != nil {
		t.Fatalf("seed prediction catalog: %v", err)
	}
}

func seedPredictionMarketTieRows(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		INSERT INTO trading.instruments (
			instrument_id, revision, price_scale, quantity_scale,
			settlement_currency, settlement_currency_scale,
			initial_margin_rate, maintenance_margin_rate, max_leverage,
			maker_fee_rate, taker_fee_rate
		) VALUES
			('TIE-MARKET-KALSHI', 1, 2, 0, 'USDC', 6, 1, 1, 1, 0, 0),
			('TIE-MARKET-POLYMARKET', 1, 2, 0, 'USDC', 6, 1, 1, 1, 0, 0);

		-- Insert in reverse tuple order; the reader must not preserve insertion order.
		INSERT INTO trading.prediction_markets (
			market_id, source_venue, market_key, question, resolution_time,
			mutually_exclusive, status, event_id, stage_label, stage_ordinal,
			created_at, updated_at
		) VALUES
			(
				'00000000-0000-0000-0000-000000000022', 'polymarket',
				'tie-polymarket-market', 'tie polymarket', NULL, false, 'open',
				NULL, NULL, 9, '2098-03-01T00:00:00Z', '2098-03-01T00:00:00Z'
			),
			(
				'00000000-0000-0000-0000-000000000021', 'kalshi',
				'tie-kalshi-market', 'tie kalshi', NULL, false, 'open',
				NULL, NULL, 9, '2098-03-01T00:00:00Z', '2098-03-01T00:00:00Z'
			);

		INSERT INTO trading.prediction_legs (
			instrument_id, market_id, display_name, outcome_index,
			outcome_label, enabled
		) VALUES
			('TIE-MARKET-POLYMARKET',
			 '00000000-0000-0000-0000-000000000022',
			 'TIE-MARKET-POLYMARKET', 0, 'Poly', true),
			('TIE-MARKET-KALSHI',
			 '00000000-0000-0000-0000-000000000021',
			 'TIE-MARKET-KALSHI', 0, 'Kalshi', true)
	`); err != nil {
		t.Fatalf("seed prediction market ties: %v", err)
	}
}

func seedPredictionDisabledOnlyMarket(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		INSERT INTO trading.instruments (
			instrument_id, revision, price_scale, quantity_scale,
			settlement_currency, settlement_currency_scale,
			initial_margin_rate, maintenance_margin_rate, max_leverage,
			maker_fee_rate, taker_fee_rate
		) VALUES ('DISABLED-ONLY-LEG', 1, 2, 0, 'USDC', 6, 1, 1, 1, 0, 0);

		INSERT INTO trading.prediction_markets (
			market_id, source_venue, market_key, question, resolution_time,
			mutually_exclusive, status, event_id, stage_label, stage_ordinal,
			created_at, updated_at
		) VALUES (
			'00000000-0000-0000-0000-000000000031', 'polymarket',
			'disabled-only-market', 'disabled only', NULL, false, 'open',
			NULL, NULL, 10, '2098-04-01T00:00:00Z', '2098-04-01T00:00:00Z'
		);

		INSERT INTO trading.prediction_legs (
			instrument_id, market_id, display_name, outcome_index,
			outcome_label, enabled
		) VALUES (
			'DISABLED-ONLY-LEG',
			'00000000-0000-0000-0000-000000000031',
			'DISABLED-ONLY-LEG', 0, 'Disabled', false
		)
	`); err != nil {
		t.Fatalf("seed disabled-only prediction market: %v", err)
	}
}

func dropPredictionOutcomeUniqueness(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	rows, err := pool.Query(ctx, `
		SELECT constraint_name
		  FROM (
			SELECT
				c.oid,
				c.conname AS constraint_name,
				array_agg(a.attname ORDER BY a.attname) AS constraint_columns
			  FROM pg_constraint AS c
			  JOIN pg_class AS r ON r.oid = c.conrelid
			  JOIN pg_namespace AS n ON n.oid = r.relnamespace
			  JOIN pg_attribute AS a
				ON a.attrelid = r.oid
				AND a.attnum = ANY (c.conkey)
			 WHERE n.nspname = 'trading'
			   AND r.relname = 'prediction_legs'
			   AND c.contype IN ('p', 'u')
			 GROUP BY c.oid, c.conname
		  ) AS constraints
		 WHERE constraint_columns = ARRAY['market_id', 'outcome_index']::name[]
		 ORDER BY constraint_name`)
	if err != nil {
		t.Fatalf("inspect prediction leg uniqueness: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var constraintName string
		if err := rows.Scan(&constraintName); err != nil {
			t.Fatalf("scan prediction leg uniqueness: %v", err)
		}
		if _, err := pool.Exec(
			ctx,
			"ALTER TABLE trading.prediction_legs DROP CONSTRAINT "+
				pgx.Identifier{constraintName}.Sanitize(),
		); err != nil {
			t.Fatalf("drop prediction leg uniqueness %q: %v", constraintName, err)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("inspect prediction leg uniqueness: %v", err)
	}
}

func seedPredictionLegTieRows(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		INSERT INTO trading.instruments (
			instrument_id, revision, price_scale, quantity_scale,
			settlement_currency, settlement_currency_scale,
			initial_margin_rate, maintenance_margin_rate, max_leverage,
			maker_fee_rate, taker_fee_rate
		) VALUES
			('TIE-LEG-ZZ', 1, 2, 0, 'USDC', 6, 1, 1, 1, 0, 0),
			('TIE-LEG-AA', 1, 2, 0, 'USDC', 6, 1, 1, 1, 0, 0);

		INSERT INTO trading.prediction_markets (
			market_id, source_venue, market_key, question, resolution_time,
			mutually_exclusive, status, event_id, stage_label, stage_ordinal,
			created_at, updated_at
		) VALUES (
			'00000000-0000-0000-0000-000000000041', 'polymarket',
			'tie-leg-market', 'tie leg outcomes', NULL, false, 'open',
			NULL, NULL, 11, '2098-05-01T00:00:00Z', '2098-05-01T00:00:00Z'
		);

		-- Insert the higher lexical instrument first to prove the reader tie-break.
		INSERT INTO trading.prediction_legs (
			instrument_id, market_id, display_name, outcome_index,
			outcome_label, enabled
		) VALUES
			('TIE-LEG-ZZ',
			 '00000000-0000-0000-0000-000000000041',
			 'TIE-LEG-ZZ', 0, 'Z', true),
			('TIE-LEG-AA',
			 '00000000-0000-0000-0000-000000000041',
			 'TIE-LEG-AA', 0, 'A', true)
	`); err != nil {
		t.Fatalf("seed prediction leg ties: %v", err)
	}
}

func relaxPredictionOutcomeMetadataNullability(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	var nullability string
	if err := pool.QueryRow(ctx, `
		SELECT is_nullable
		  FROM information_schema.columns
		 WHERE table_schema = 'trading'
		   AND table_name = 'prediction_legs'
		   AND column_name = 'outcome_label'`).Scan(&nullability); err != nil {
		t.Fatalf("inspect prediction outcome-label nullability: %v", err)
	}
	if nullability == "NO" {
		if _, err := pool.Exec(
			ctx,
			"ALTER TABLE trading.prediction_legs ALTER COLUMN "+
				pgx.Identifier{"outcome_label"}.Sanitize()+" DROP NOT NULL",
		); err != nil {
			t.Fatalf("drop prediction outcome-label NOT NULL: %v", err)
		}
	}
}

func seedPredictionCorruptLeg(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		INSERT INTO trading.instruments (
			instrument_id, revision, price_scale, quantity_scale,
			settlement_currency, settlement_currency_scale,
			initial_margin_rate, maintenance_margin_rate, max_leverage,
			maker_fee_rate, taker_fee_rate
		) VALUES ('CORRUPT-PREDICTION-LEG', 1, 2, 0, 'USDC', 6, 1, 1, 1, 0, 0);

		INSERT INTO trading.prediction_markets (
			market_id, source_venue, market_key, question, resolution_time,
			mutually_exclusive, status, event_id, stage_label, stage_ordinal,
			created_at, updated_at
		) VALUES (
			'00000000-0000-0000-0000-000000000051', 'polymarket',
			'corrupt-read-model', 'corrupt read model', NULL, false, 'open',
			NULL, NULL, 12, '2098-06-01T00:00:00Z', '2098-06-01T00:00:00Z'
		);

		INSERT INTO trading.prediction_legs (
			instrument_id, market_id, display_name, outcome_index,
			outcome_label, enabled
		) VALUES (
			'CORRUPT-PREDICTION-LEG',
			'00000000-0000-0000-0000-000000000051',
			'CORRUPT-PREDICTION-LEG', 0, NULL, true
		)
	`); err != nil {
		t.Fatalf("seed corrupt prediction leg: %v", err)
	}
}

// relaxPredictionStageMetadataConstraint drops one catalog check only on the
// disposable database owned by the current test. Production migrations remain
// unchanged; the dropped constraint is restored by currentStorePool's next
// reset or clone.
func relaxPredictionStageMetadataConstraint(
	t *testing.T,
	pool *pgxpool.Pool,
	constraintName string,
) {
	t.Helper()
	switch constraintName {
	case "prediction_markets_stage_label_check", "prediction_markets_stage_ordinal_check":
	default:
		t.Fatalf("unexpected prediction stage constraint %q", constraintName)
	}
	ctx := context.Background()
	if _, err := pool.Exec(
		ctx,
		"ALTER TABLE trading.prediction_markets DROP CONSTRAINT "+
			pgx.Identifier{constraintName}.Sanitize(),
	); err != nil {
		t.Fatalf("drop prediction stage constraint %q: %v", constraintName, err)
	}
}

func seedPredictionStageMetadataRows(
	t *testing.T,
	pool *pgxpool.Pool,
	corruptionCase string,
) {
	t.Helper()
	ctx := context.Background()
	corruptStageLabel := ""
	corruptStageOrdinal := 0
	switch corruptionCase {
	case "negative stage ordinal":
		corruptStageLabel = "Round"
		corruptStageOrdinal = -1
	case "empty non-null stage label":
		corruptStageOrdinal = 1
	default:
		t.Fatalf("unexpected prediction stage corruption case %q", corruptionCase)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO trading.instruments (
			instrument_id, revision, price_scale, quantity_scale,
			settlement_currency, settlement_currency_scale,
			initial_margin_rate, maintenance_margin_rate, max_leverage,
			maker_fee_rate, taker_fee_rate
		) VALUES
			('STAGE-METADATA-VALID', 1, 2, 0, 'USDC', 6, 1, 1, 1, 0, 0),
			('STAGE-METADATA-CORRUPT', 1, 2, 0, 'USDC', 6, 1, 1, 1, 0, 0);

		-- Insert a valid market first so a successful implementation cannot pass
		-- vacuously without proving that a later corrupt row aborts the read.
		INSERT INTO trading.prediction_markets (
			market_id, source_venue, market_key, question, resolution_time,
			mutually_exclusive, status, event_id, stage_label, stage_ordinal,
			created_at, updated_at
		) VALUES (
			'00000000-0000-0000-0000-000000000071', 'polymarket',
			'stage-metadata-valid', 'valid stage metadata', NULL, false, 'open',
			NULL, NULL, 0, '2098-07-01T00:00:01Z', '2098-07-01T00:00:01Z'
		)
	`); err != nil {
		t.Fatalf("seed valid prediction stage metadata: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO trading.prediction_markets (
			market_id, source_venue, market_key, question, resolution_time,
			mutually_exclusive, status, event_id, stage_label, stage_ordinal,
			created_at, updated_at
		) VALUES (
			'00000000-0000-0000-0000-000000000072', 'polymarket',
			'stage-metadata-corrupt', 'corrupt stage metadata', NULL, false, 'open',
			NULL, $1, $2,
			'2098-07-01T00:00:02Z', '2098-07-01T00:00:02Z'
		)
	`, corruptStageLabel, corruptStageOrdinal); err != nil {
		t.Fatalf("seed corrupt prediction stage market: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO trading.prediction_legs (
			instrument_id, market_id, display_name, outcome_index,
			outcome_label, enabled
		) VALUES
			('STAGE-METADATA-VALID',
			 '00000000-0000-0000-0000-000000000071',
			 'STAGE-METADATA-VALID', 0, 'Valid', true),
			('STAGE-METADATA-CORRUPT',
			 '00000000-0000-0000-0000-000000000072',
			 'STAGE-METADATA-CORRUPT', 0, 'Corrupt', true)
	`); err != nil {
		t.Fatalf("seed prediction stage metadata legs: %v", err)
	}
}

func seedPredictionTimestampGoldenCatalog(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		INSERT INTO trading.instruments (
			instrument_id, revision, price_scale, quantity_scale,
			settlement_currency, settlement_currency_scale,
			initial_margin_rate, maintenance_margin_rate, max_leverage,
			maker_fee_rate, taker_fee_rate
		) VALUES
			('CHRONO-PREDICTION-YEAR-10000', 1, 2, 0, 'USDC', 6, 1, 1, 1, 0, 0),
			('CHRONO-PREDICTION-SECONDS', 1, 2, 0, 'USDC', 6, 1, 1, 1, 0, 0),
			('CHRONO-PREDICTION-MILLIS', 1, 2, 0, 'USDC', 6, 1, 1, 1, 0, 0),
			('CHRONO-PREDICTION-MICROS', 1, 2, 0, 'USDC', 6, 1, 1, 1, 0, 0),
			('CHRONO-PREDICTION-ABSENT', 1, 2, 0, 'USDC', 6, 1, 1, 1, 0, 0);

		INSERT INTO trading.prediction_events (
			event_id, source_venue, event_key, title, series, status,
			created_at, updated_at
		) VALUES (
			'00000000-0000-0000-0000-000000000061', 'polymarket',
			'chrono-event', 'Chrono event', NULL, 'open',
			'2098-08-01T00:00:00Z', '2098-08-01T00:00:00Z'
		);

		INSERT INTO trading.prediction_markets (
			market_id, source_venue, market_key, question, resolution_time,
			mutually_exclusive, status, event_id, stage_label, stage_ordinal,
			created_at, updated_at
		) VALUES
			(
				'00000000-0000-0000-0000-000000000066', 'polymarket',
				'chrono-rfc3339-year-10000', 'Chrono year 10000',
				'10000-01-01T00:00:00Z', false, 'open',
				NULL, NULL, NULL,
				'2098-08-01T00:00:04Z', '2098-08-01T00:00:04Z'
			),
			(
				'00000000-0000-0000-0000-000000000062', 'polymarket',
				'chrono-rfc3339-seconds', 'Chrono seconds',
				'2099-01-01T00:00:00Z', false, 'open',
				'00000000-0000-0000-0000-000000000061', NULL, NULL,
				'2098-08-01T00:00:03Z', '2098-08-01T00:00:03Z'
			),
			(
				'00000000-0000-0000-0000-000000000063', 'polymarket',
				'chrono-rfc3339-millis', 'Chrono milliseconds',
				'2099-01-01T00:00:00.123Z', false, 'open',
				NULL, NULL, NULL,
				'2098-08-01T00:00:02Z', '2098-08-01T00:00:02Z'
			),
			(
				'00000000-0000-0000-0000-000000000064', 'polymarket',
				'chrono-rfc3339-micros', 'Chrono microseconds',
				'2099-01-01T00:00:00.000100Z', false, 'open',
				NULL, NULL, NULL,
				'2098-08-01T00:00:01Z', '2098-08-01T00:00:01Z'
			),
			(
				'00000000-0000-0000-0000-000000000065', 'polymarket',
				'chrono-rfc3339-absent', 'Chrono absent resolution',
				NULL, false, 'open',
				NULL, NULL, NULL,
				'2098-08-01T00:00:00Z', '2098-08-01T00:00:00Z'
			);

		INSERT INTO trading.prediction_legs (
			instrument_id, market_id, display_name, outcome_index,
			outcome_label, enabled
		) VALUES
			('CHRONO-PREDICTION-YEAR-10000',
			 '00000000-0000-0000-0000-000000000066',
			 'CHRONO-PREDICTION-YEAR-10000', 0, 'Year 10000', true),
			('CHRONO-PREDICTION-SECONDS',
			 '00000000-0000-0000-0000-000000000062',
			 'CHRONO-PREDICTION-SECONDS', 0, 'Seconds', true),
			('CHRONO-PREDICTION-MILLIS',
			 '00000000-0000-0000-0000-000000000063',
			 'CHRONO-PREDICTION-MILLIS', 0, 'Milliseconds', true),
			('CHRONO-PREDICTION-MICROS',
			 '00000000-0000-0000-0000-000000000064',
			 'CHRONO-PREDICTION-MICROS', 0, 'Microseconds', true),
			('CHRONO-PREDICTION-ABSENT',
			 '00000000-0000-0000-0000-000000000065',
			 'CHRONO-PREDICTION-ABSENT', 0, 'Absent', true)
	`); err != nil {
		t.Fatalf("seed prediction timestamp golden catalog: %v", err)
	}
}

func seedPredictionOutOfChronoRangeTimestamp(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		INSERT INTO trading.instruments (
			instrument_id, revision, price_scale, quantity_scale,
			settlement_currency, settlement_currency_scale,
			initial_margin_rate, maintenance_margin_rate, max_leverage,
			maker_fee_rate, taker_fee_rate
		) VALUES (
			'CHRONO-PREDICTION-OUT-OF-RANGE', 1, 2, 0, 'USDC', 6, 1, 1, 1, 0, 0
		);

		INSERT INTO trading.prediction_markets (
			market_id, source_venue, market_key, question, resolution_time,
			mutually_exclusive, status, event_id, stage_label, stage_ordinal,
			created_at, updated_at
		) VALUES (
			'00000000-0000-0000-0000-000000000067', 'polymarket',
			'chrono-rfc3339-out-of-range', 'Chrono out of range',
			'262143-01-01T00:00:00Z', false, 'open', NULL, NULL, 1,
			'2098-01-01T00:00:03Z', '2098-01-01T00:00:03Z'
		);

		INSERT INTO trading.prediction_legs (
			instrument_id, market_id, display_name, outcome_index,
			outcome_label, enabled
		) VALUES (
			'CHRONO-PREDICTION-OUT-OF-RANGE',
			'00000000-0000-0000-0000-000000000067',
			'CHRONO-PREDICTION-OUT-OF-RANGE', 0, 'Out of range', true
		)
	`); err != nil {
		t.Fatalf("seed out-of-Chrono-range prediction timestamp: %v", err)
	}
}

func seedPredictionScale18Catalog(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		INSERT INTO trading.instruments (
			instrument_id, revision, price_scale, quantity_scale,
			settlement_currency, settlement_currency_scale,
			initial_margin_rate, maintenance_margin_rate, max_leverage,
			maker_fee_rate, taker_fee_rate
		) VALUES (
			'SCALE18-PREDICTION-LEG', 1, 18, 18, 'USDC', 6, 1, 1, 1, 0, 0
		);

		INSERT INTO trading.prediction_markets (
			market_id, source_venue, market_key, question, resolution_time,
			mutually_exclusive, status, event_id, stage_label, stage_ordinal,
			created_at, updated_at
		) VALUES (
			'00000000-0000-0000-0000-000000000081', 'polymarket',
			'scale18-prediction', 'Scale eighteen prediction', NULL, false, 'open',
			NULL, NULL, 0, '2098-09-01T00:00:00Z', '2098-09-01T00:00:00Z'
		);

		INSERT INTO trading.prediction_legs (
			instrument_id, market_id, display_name, outcome_index,
			outcome_label, enabled
		) VALUES (
			'SCALE18-PREDICTION-LEG',
			'00000000-0000-0000-0000-000000000081',
			'SCALE18-PREDICTION-LEG', 0, 'Scale eighteen', true
		)
	`); err != nil {
		t.Fatalf("seed scale-18 prediction catalog: %v", err)
	}
}
