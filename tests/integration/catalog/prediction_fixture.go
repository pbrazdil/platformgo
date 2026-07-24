package catalog

import "sort"

const (
	predictionVenue          = "polymarket"
	predictionEventKey       = "test-cup-winner-2099"
	predictionEventTitle     = "Test Cup Winner 2099"
	predictionMarketKey      = "test-cup-winner-2099"
	predictionMarketQuestion = "Who wins the Test Cup 2099?"
	predictionBinaryKey      = "0xtest-will-it-rain"
	predictionBinaryQuestion = "Will it rain on test day?"
)

func predictionCandidates() []string {
	return []string{"Team Alpha", "Team Bravo", "Team Charlie"}
}

func predictionLegSymbols() []string {
	return []string{
		"TEST-CUP-WINNER-2099-TEAM-ALPHA",
		"TEST-CUP-WINNER-2099-TEAM-BRAVO",
		"TEST-CUP-WINNER-2099-TEAM-CHARLIE",
	}
}

func predictionBinaryOutcomes() []string {
	return []string{"Yes", "No"}
}

func predictionBinaryLegSymbols() []string {
	return []string{"TEST-WILL-IT-RAIN-YES", "TEST-WILL-IT-RAIN-NO"}
}

type predictionEvent struct {
	ID          string  `json:"id"`
	SourceVenue string  `json:"sourceVenue"`
	EventKey    string  `json:"eventKey"`
	Title       string  `json:"title"`
	Series      *string `json:"series"`
	Status      string  `json:"status"`
}

type predictionMarket struct {
	ID                string  `json:"id"`
	SourceVenue       string  `json:"sourceVenue"`
	MarketKey         string  `json:"marketKey"`
	Question          string  `json:"question"`
	ResolutionTime    *string `json:"resolutionTime"`
	MutuallyExclusive bool    `json:"mutuallyExclusive"`
	Status            string  `json:"status"`
	EventID           *string `json:"eventId"`
	StageLabel        *string `json:"stageLabel"`
	StageOrdinal      *int    `json:"stageOrdinal"`
}

type predictionLeg struct {
	Symbol       string `json:"symbol"`
	MarketID     string `json:"marketId"`
	OutcomeIndex int    `json:"outcomeIndex"`
	OutcomeLabel string `json:"outcomeLabel"`
	ProductType  string `json:"productType"`
	AssetClass   string `json:"assetClass"`
	Enabled      bool   `json:"enabled"`
}

type predictionFixture struct {
	events       map[string]predictionEvent
	markets      map[string]predictionMarket
	legs         map[string]predictionLeg
	nextEventID  int
	nextMarketID int
}

func newPredictionFixture() *predictionFixture {
	return &predictionFixture{
		events:  make(map[string]predictionEvent),
		markets: make(map[string]predictionMarket),
		legs:    make(map[string]predictionLeg),
	}
}

func (fixture *predictionFixture) upsertEvent(event predictionEvent) string {
	key := event.SourceVenue + ":" + event.EventKey
	if existing, ok := fixture.events[key]; ok {
		event.ID = existing.ID
	} else {
		fixture.nextEventID++
		event.ID = "event-" + decimalInteger(fixture.nextEventID)
	}
	fixture.events[key] = event
	return event.ID
}

func (fixture *predictionFixture) upsertMarket(market predictionMarket) string {
	key := market.SourceVenue + ":" + market.MarketKey
	if existing, ok := fixture.markets[key]; ok {
		market.ID = existing.ID
	} else {
		fixture.nextMarketID++
		market.ID = "market-" + decimalInteger(fixture.nextMarketID)
	}
	fixture.markets[key] = market
	return market.ID
}

func (fixture *predictionFixture) upsertLeg(leg predictionLeg) bool {
	_, existed := fixture.legs[leg.Symbol]
	fixture.legs[leg.Symbol] = leg
	return !existed
}

func (fixture *predictionFixture) getEvent(venue, key string) (predictionEvent, bool) {
	event, ok := fixture.events[venue+":"+key]
	return event, ok
}

func (fixture *predictionFixture) getMarket(venue, key string) (predictionMarket, bool) {
	market, ok := fixture.markets[venue+":"+key]
	return market, ok
}

func (fixture *predictionFixture) getLeg(symbol string) (predictionLeg, bool) {
	leg, ok := fixture.legs[symbol]
	return leg, ok
}

type predictionImportResult struct {
	Markets, Inserted, Updated, Skipped int
}

func (fixture *predictionFixture) importCategorical(enabled bool) predictionImportResult {
	eventID := fixture.upsertEvent(predictionEvent{
		SourceVenue: predictionVenue, EventKey: predictionEventKey,
		Title: predictionEventTitle, Status: "open",
	})
	marketID := fixture.upsertMarket(predictionMarket{
		SourceVenue: predictionVenue, MarketKey: predictionMarketKey,
		Question: predictionMarketQuestion, MutuallyExclusive: true,
		Status: "open", EventID: &eventID,
	})
	result := predictionImportResult{Markets: 1}
	candidates, symbols := predictionCandidates(), predictionLegSymbols()
	for index, symbol := range symbols {
		inserted := fixture.upsertLeg(predictionLeg{
			Symbol: symbol, MarketID: marketID, OutcomeIndex: index,
			OutcomeLabel: candidates[index], ProductType: "binary_option",
			AssetClass: "prediction", Enabled: enabled,
		})
		if inserted {
			result.Inserted++
		} else {
			result.Updated++
		}
	}
	return result
}

func (fixture *predictionFixture) seedTraderMarkets() {
	eventID := fixture.upsertEvent(predictionEvent{
		SourceVenue: predictionVenue, EventKey: predictionEventKey,
		Title: predictionEventTitle, Status: "open",
	})
	resolution := "2099-01-01T00:00:00Z"
	ordinal := 0
	categoricalID := fixture.upsertMarket(predictionMarket{
		SourceVenue: predictionVenue, MarketKey: predictionMarketKey,
		Question: predictionMarketQuestion, ResolutionTime: &resolution,
		MutuallyExclusive: true, Status: "open", EventID: &eventID, StageOrdinal: &ordinal,
	})
	for index, symbol := range predictionLegSymbols() {
		fixture.upsertLeg(predictionLeg{
			Symbol: symbol, MarketID: categoricalID, OutcomeIndex: index,
			OutcomeLabel: predictionCandidates()[index], ProductType: "binary_option",
			AssetClass: "prediction", Enabled: index != 2,
		})
	}
	fixture.seedBinaryMarket()
}

func (fixture *predictionFixture) seedBinaryMarket() {
	binaryID := fixture.upsertMarket(predictionMarket{
		SourceVenue: predictionVenue, MarketKey: predictionBinaryKey,
		Question: predictionBinaryQuestion, MutuallyExclusive: false, Status: "open",
	})
	for index, symbol := range predictionBinaryLegSymbols() {
		fixture.upsertLeg(predictionLeg{
			Symbol: symbol, MarketID: binaryID, OutcomeIndex: index,
			OutcomeLabel: predictionBinaryOutcomes()[index], ProductType: "binary_option",
			AssetClass: "prediction", Enabled: true,
		})
	}
}

type publicPredictionEvent struct {
	Title string `json:"title"`
}

type publicPredictionMarket struct {
	SourceVenue       string                 `json:"sourceVenue"`
	MarketKey         string                 `json:"marketKey"`
	Question          string                 `json:"question"`
	ResolutionTime    *string                `json:"resolutionTime"`
	MutuallyExclusive bool                   `json:"mutuallyExclusive"`
	Status            string                 `json:"status"`
	Event             *publicPredictionEvent `json:"event"`
	Legs              []predictionLeg        `json:"legs"`
}

func (fixture *predictionFixture) publicMarkets() []publicPredictionMarket {
	result := make([]publicPredictionMarket, 0)
	for _, market := range fixture.markets {
		legs := make([]predictionLeg, 0)
		for _, leg := range fixture.legs {
			if leg.MarketID == market.ID && leg.Enabled {
				legs = append(legs, leg)
			}
		}
		if len(legs) == 0 {
			continue
		}
		sort.Slice(legs, func(left, right int) bool {
			return legs[left].OutcomeIndex < legs[right].OutcomeIndex
		})
		view := publicPredictionMarket{
			SourceVenue: market.SourceVenue, MarketKey: market.MarketKey,
			Question: market.Question, ResolutionTime: market.ResolutionTime,
			MutuallyExclusive: market.MutuallyExclusive, Status: market.Status, Legs: legs,
		}
		if market.EventID != nil {
			for _, event := range fixture.events {
				if event.ID == *market.EventID {
					view.Event = &publicPredictionEvent{Title: event.Title}
				}
			}
		}
		result = append(result, view)
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left].MarketKey < result[right].MarketKey
	})
	return result
}

type discoveredMarket struct {
	SelectKey string          `json:"selectKey"`
	Legs      []predictionLeg `json:"legs"`
	Imported  bool            `json:"imported"`
}

type discoverPage struct {
	Items      []discoveredMarket `json:"items"`
	NextCursor *string            `json:"nextCursor"`
}

func (fixture *predictionFixture) discover(cursor, filter string) discoverPage {
	if filter == predictionEventKey {
		return discoverPage{Items: []discoveredMarket{categoricalDiscoveredMarket()}}
	}
	if cursor == "" {
		next := "1"
		return discoverPage{
			Items:      []discoveredMarket{categoricalDiscoveredMarket()},
			NextCursor: &next,
		}
	}
	if cursor == "1" {
		return discoverPage{Items: []discoveredMarket{binaryDiscoveredMarket()}}
	}
	return discoverPage{}
}

func categoricalDiscoveredMarket() discoveredMarket {
	legs := make([]predictionLeg, len(predictionLegSymbols()))
	for index, symbol := range predictionLegSymbols() {
		legs[index] = predictionLeg{
			Symbol: symbol, OutcomeIndex: index, OutcomeLabel: predictionCandidates()[index],
		}
	}
	return discoveredMarket{SelectKey: predictionMarketKey, Legs: legs}
}

func binaryDiscoveredMarket() discoveredMarket {
	legs := make([]predictionLeg, len(predictionBinaryLegSymbols()))
	for index, symbol := range predictionBinaryLegSymbols() {
		legs[index] = predictionLeg{
			Symbol: symbol, OutcomeIndex: index, OutcomeLabel: predictionBinaryOutcomes()[index],
		}
	}
	return discoveredMarket{SelectKey: predictionBinaryKey, Legs: legs}
}

func gammaDiscover(cursor string) discoverPage {
	if cursor != "" {
		return discoverPage{}
	}
	next := "offset:1"
	return discoverPage{
		Items:      []discoveredMarket{{SelectKey: "0xrain"}},
		NextCursor: &next,
	}
}

type candle struct {
	Open      string `json:"open"`
	High      string `json:"high"`
	Low       string `json:"low"`
	Close     string `json:"close"`
	Volume    string `json:"volume"`
	OpenTime  int64  `json:"openTime"`
	CloseTime int64  `json:"closeTime"`
	NumTrades int    `json:"numTrades"`
}

func polymarketCandles() []candle {
	points := []struct {
		seconds int64
		price   string
	}{
		{1_700_000_000, "0.61"},
		{1_700_003_600, "0.62"},
		{1_700_007_200, "0.6"},
	}
	candles := make([]candle, len(points))
	for index, point := range points {
		open := point.seconds * 1000
		candles[index] = candle{
			Open: point.price, High: point.price, Low: point.price, Close: point.price,
			Volume: "0", OpenTime: open, CloseTime: open + 3_600_000,
		}
	}
	return candles
}

type hyperliquidVendorMeta struct {
	Venue         string  `json:"venue"`
	IsHIP3        bool    `json:"is_hip3"`
	Dex           *string `json:"dex"`
	AssetIndex    *int64  `json:"asset_index"`
	NativeCoin    *string `json:"native_coin"`
	Deployer      *string `json:"deployer"`
	OracleUpdater *string `json:"oracle_updater"`
	DexFullName   *string `json:"dex_full_name"`
}

type vendorImportView struct {
	Symbol     string                 `json:"symbol"`
	Category   *string                `json:"category"`
	VendorMeta *hyperliquidVendorMeta `json:"vendorMeta"`
}

type vendorFixture struct {
	meta *hyperliquidVendorMeta
	view vendorImportView
}

func importHIP3Vendor() (predictionImportResult, vendorFixture) {
	dex, native := "para", "para:BTC"
	deployer := "0x000000000000000000000000000000000000dead"
	updater := "0x000000000000000000000000000000000000beef"
	fullName, category := "Para Builder DEX", "hyperliquid_hip3_para"
	index := int64(2)
	meta := &hyperliquidVendorMeta{
		Venue: "hyperliquid", IsHIP3: true, Dex: &dex, AssetIndex: &index,
		NativeCoin: &native, Deployer: &deployer, OracleUpdater: &updater, DexFullName: &fullName,
	}
	return predictionImportResult{Inserted: 1}, vendorFixture{
		meta: meta,
		view: vendorImportView{Symbol: "BTC-PERP", Category: &category, VendorMeta: meta},
	}
}

func typedHLJSONColumns() []string {
	return []string{"margin_tiers"}
}

func decimalInteger(value int) string {
	if value == 0 {
		return "0"
	}
	digits := make([]byte, 0, 20)
	for value > 0 {
		digits = append(digits, byte('0'+value%10))
		value /= 10
	}
	for left, right := 0, len(digits)-1; left < right; left, right = left+1, right-1 {
		digits[left], digits[right] = digits[right], digits[left]
	}
	return string(digits)
}
