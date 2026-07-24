package catalog

import "testing"

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/catalog/e2e_prediction_schema.rs:12
//	test: prediction_event_market_and_leg_round_trip
func TestPredictionEventMarketAndLegRoundTrip(t *testing.T) {
	fixture := newPredictionFixture()
	eventID := fixture.upsertEvent(predictionEvent{
		SourceVenue: predictionVenue, EventKey: "lol-worlds-2025",
		Title: "League of Legends Worlds 2025", Status: "open",
	})
	stageLabel, stageOrdinal := "Tournament Winner", 0
	marketID := fixture.upsertMarket(predictionMarket{
		SourceVenue: predictionVenue, MarketKey: "0xcondition-tournament-winner",
		Question: "Who wins Worlds 2025?", MutuallyExclusive: true, Status: "open",
		EventID: &eventID, StageLabel: &stageLabel, StageOrdinal: &stageOrdinal,
	})
	fixture.upsertLeg(predictionLeg{
		Symbol: "WORLDS25-TEAM-A", MarketID: marketID, OutcomeIndex: 0,
		OutcomeLabel: "Team A", ProductType: "binary_option",
		AssetClass: "prediction", Enabled: false,
	})

	event, ok := fixture.getEvent(predictionVenue, "lol-worlds-2025")
	if !ok || event.ID != eventID || event.Title != "League of Legends Worlds 2025" ||
		event.Series != nil || event.Status != "open" {
		t.Fatalf("event = %#v, found=%v", event, ok)
	}
	market, ok := fixture.getMarket(predictionVenue, "0xcondition-tournament-winner")
	if !ok || market.ID != marketID || market.EventID == nil || *market.EventID != eventID ||
		!market.MutuallyExclusive || market.StageLabel == nil ||
		*market.StageLabel != "Tournament Winner" || market.StageOrdinal == nil ||
		*market.StageOrdinal != 0 {
		t.Fatalf("market = %#v, found=%v", market, ok)
	}
	leg, ok := fixture.getLeg("WORLDS25-TEAM-A")
	if !ok || leg.MarketID != marketID || leg.OutcomeIndex != 0 ||
		leg.OutcomeLabel != "Team A" || leg.ProductType != "binary_option" ||
		leg.AssetClass != "prediction" {
		t.Fatalf("leg = %#v, found=%v", leg, ok)
	}
}
