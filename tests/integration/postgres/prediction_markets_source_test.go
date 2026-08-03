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

// Snapshot interleaving remains deferred to implementation-boundary review.
// PredictionMarkets currently has no injectable transaction/connection hook
// that can deterministically pause between its market, event, and leg reads;
// a goroutine race or arbitrary sleep here would not prove a one-snapshot
// guarantee. Once the production reader exposes its transaction boundary, add
// a channel-coordinated before-or-after metadata toggle regression there.

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
		 WHERE constraint_columns = ARRAY['market_id', 'outcome_index']::text[]
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
