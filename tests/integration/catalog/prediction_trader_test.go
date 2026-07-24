package catalog

import "testing"

func publicPredictionByKey(markets []publicPredictionMarket, key string) *publicPredictionMarket {
	for index := range markets {
		if markets[index].MarketKey == key {
			return &markets[index]
		}
	}
	return nil
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/catalog/e2e_prediction_trader.rs:60
//	test: prediction_public_list_nests_legs_under_market_and_event
func TestPredictionPublicListNestsLegsUnderMarketAndEvent(t *testing.T) {
	fixture := newPredictionFixture()
	fixture.seedTraderMarkets()
	markets := fixture.publicMarkets()
	if len(markets) != 2 {
		t.Fatalf("market count = %d, want 2", len(markets))
	}
	categorical := publicPredictionByKey(markets, predictionMarketKey)
	if categorical == nil || categorical.SourceVenue != predictionVenue ||
		categorical.Question != predictionMarketQuestion || !categorical.MutuallyExclusive ||
		categorical.Status != "open" || categorical.ResolutionTime == nil ||
		categorical.Event == nil || categorical.Event.Title != predictionEventTitle ||
		len(categorical.Legs) != 2 {
		t.Fatalf("categorical market = %#v", categorical)
	}
	if categorical.Legs[0].OutcomeIndex != 0 ||
		categorical.Legs[0].OutcomeLabel != predictionCandidates()[0] ||
		categorical.Legs[0].Symbol != predictionLegSymbols()[0] ||
		categorical.Legs[1].OutcomeIndex != 1 ||
		categorical.Legs[1].OutcomeLabel != predictionCandidates()[1] {
		t.Fatalf("categorical legs = %#v", categorical.Legs)
	}
	binary := publicPredictionByKey(markets, predictionBinaryKey)
	if binary == nil || binary.Event != nil || binary.MutuallyExclusive ||
		binary.ResolutionTime != nil || len(binary.Legs) != 2 {
		t.Fatalf("binary market = %#v", binary)
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/catalog/e2e_prediction_trader.rs:192
//	test: prediction_legs_surface_as_pure_definitions_without_live_price
func TestPredictionLegsSurfaceAsPureDefinitionsWithoutLivePrice(t *testing.T) {
	fixture := newPredictionFixture()
	fixture.seedBinaryMarket()
	binary := publicPredictionByKey(fixture.publicMarkets(), predictionBinaryKey)
	if binary == nil {
		t.Fatal("binary market is absent")
	}
	var yesFound, noFound bool
	for _, leg := range binary.Legs {
		if leg.Symbol == predictionBinaryLegSymbols()[0] {
			yesFound = leg.OutcomeIndex == 0
		}
		if leg.Symbol == predictionBinaryLegSymbols()[1] {
			noFound = true
		}
	}
	if !yesFound || !noFound {
		t.Fatalf("binary legs = %#v", binary.Legs)
	}
}
