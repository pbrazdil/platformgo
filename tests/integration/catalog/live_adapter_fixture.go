package catalog

import (
	"fmt"
	"sort"
	"strconv"
)

type liveProtocol struct {
	Protocol, DefaultVenue, DefaultPricingConfig string
	ProductTypes                                 []string
}

type liveFeed struct {
	Name, Protocol, PricingStatus string
	Enabled                       bool
}

type liveProbeReport struct {
	OK              bool
	InstrumentCount int
	PricingStatus   string
}

type livePredictionLeg struct {
	VenueSymbol, SuggestedSymbol, QuoteCurrency string
	OutcomeIndex                                *int
	OutcomeLabel                                *string
}

type liveDiscoveredMarket struct {
	SelectKey, Parent                 string
	VenueSymbol, SuggestedSymbol      string
	QuoteCurrency, SettlementCurrency string
	Imported                          bool
	Legs                              []livePredictionLeg
}

type liveImportResult struct {
	Inserted, Skipped int
}

type liveInstrument struct {
	Symbol, Provenance string
	MaxLeverage        int
	Enabled            bool
}

// liveCatalogFixture is the deterministic adapter seam for the live catalog
// tests. Its venue universes are fixed and it performs no network access.
type liveCatalogFixture struct {
	feeds       map[string]liveFeed
	instruments map[string]liveInstrument
	overrides   map[string]bool
	hyperliquid []liveDiscoveredMarket
	polymarket  []liveDiscoveredMarket
}

func newLiveCatalogFixture() *liveCatalogFixture {
	hyperliquid := make([]liveDiscoveredMarket, 0, 64)
	symbols := []string{"BTC", "ETH"}
	for index := 0; index < 62; index++ {
		symbols = append(symbols, fmt.Sprintf("ASSET%02d", index))
	}
	for _, symbol := range symbols {
		hyperliquid = append(hyperliquid, liveDiscoveredMarket{
			SelectKey: symbol, VenueSymbol: symbol, SuggestedSymbol: symbol + "-PERP",
			QuoteCurrency: "USD", SettlementCurrency: "USDC",
		})
	}

	polymarket := make([]liveDiscoveredMarket, 0, 4)
	for marketIndex := 0; marketIndex < 4; marketIndex++ {
		parent := "condition-" + strconv.Itoa(marketIndex)
		legs := make([]livePredictionLeg, 0, 2)
		for outcomeIndex, label := range []string{"Yes", "No"} {
			indexCopy, labelCopy := outcomeIndex, label
			legs = append(legs, livePredictionLeg{
				VenueSymbol:     parent + "-" + strconv.Itoa(outcomeIndex),
				SuggestedSymbol: "PRED-" + strconv.Itoa(marketIndex) + "-" + label,
				QuoteCurrency:   "USDC", OutcomeIndex: &indexCopy, OutcomeLabel: &labelCopy,
			})
		}
		polymarket = append(polymarket, liveDiscoveredMarket{
			SelectKey: "market-" + strconv.Itoa(marketIndex), Parent: parent, Legs: legs,
		})
	}

	return &liveCatalogFixture{
		feeds:       make(map[string]liveFeed),
		instruments: make(map[string]liveInstrument),
		overrides:   make(map[string]bool),
		hyperliquid: hyperliquid,
		polymarket:  polymarket,
	}
}

func (fixture *liveCatalogFixture) protocol(name string) liveProtocol {
	if name != "hyperliquid_ws" {
		return liveProtocol{}
	}
	return liveProtocol{
		Protocol: "hyperliquid_ws", DefaultVenue: "hyperliquid",
		ProductTypes:         []string{"perp"},
		DefaultPricingConfig: `{"info_url":"https://api.hyperliquid.invalid/info"}`,
	}
}

func (fixture *liveCatalogFixture) listFeeds() []liveFeed {
	names := make([]string, 0, len(fixture.feeds))
	for name := range fixture.feeds {
		names = append(names, name)
	}
	sort.Strings(names)
	feeds := make([]liveFeed, 0, len(names))
	for _, name := range names {
		feeds = append(feeds, fixture.feeds[name])
	}
	return feeds
}

func (fixture *liveCatalogFixture) createFeed(name, protocol string, enabled, hasTradingConfig bool) (liveFeed, error) {
	if protocol == "fix" && !hasTradingConfig {
		return liveFeed{}, fmt.Errorf("FIX feed requires trading config")
	}
	if protocol != "hyperliquid_ws" && protocol != "fix" {
		return liveFeed{}, fmt.Errorf("unsupported protocol %q", protocol)
	}
	feed := liveFeed{Name: name, Protocol: protocol, Enabled: enabled, PricingStatus: "disconnected"}
	fixture.feeds[name] = feed
	return feed, nil
}

func (fixture *liveCatalogFixture) probeFeed(name string) (liveProbeReport, error) {
	feed, ok := fixture.feeds[name]
	if !ok {
		return liveProbeReport{}, fmt.Errorf("feed %q not found", name)
	}
	if feed.Protocol != "hyperliquid_ws" {
		return liveProbeReport{}, fmt.Errorf("feed %q is not probeable", name)
	}
	feed.PricingStatus = "connected"
	fixture.feeds[name] = feed
	return liveProbeReport{
		OK: true, InstrumentCount: len(fixture.hyperliquid), PricingStatus: "connected",
	}, nil
}

func (fixture *liveCatalogFixture) discoverHyperliquid() []liveDiscoveredMarket {
	discovered := make([]liveDiscoveredMarket, len(fixture.hyperliquid))
	for index, market := range fixture.hyperliquid {
		copy := market
		_, copy.Imported = fixture.instruments[market.SuggestedSymbol]
		discovered[index] = copy
	}
	return discovered
}

func (fixture *liveCatalogFixture) importHyperliquid(symbols []string, leverageCap int) liveImportResult {
	result := liveImportResult{}
	for _, venueSymbol := range symbols {
		symbol := venueSymbol + "-PERP"
		if fixture.overrides[symbol] {
			result.Skipped++
			continue
		}
		if _, exists := fixture.instruments[symbol]; exists {
			result.Skipped++
			continue
		}
		fixture.instruments[symbol] = liveInstrument{
			Symbol: symbol, Provenance: "synced", MaxLeverage: leverageCap, Enabled: true,
		}
		result.Inserted++
	}
	return result
}

func (fixture *liveCatalogFixture) instrument(symbol string) liveInstrument {
	return fixture.instruments[symbol]
}

func (fixture *liveCatalogFixture) operatorOverride(symbol string, leverage int) {
	instrument := fixture.instruments[symbol]
	instrument.MaxLeverage = leverage
	instrument.Provenance = "synced_with_overrides"
	fixture.instruments[symbol] = instrument
	fixture.overrides[symbol] = true
}

func (fixture *liveCatalogFixture) discoverPolymarket(cursor string) ([]liveDiscoveredMarket, string) {
	offset := 0
	if cursor != "" {
		_, _ = fmt.Sscanf(cursor, "poly:%d", &offset)
	}
	const limit = 2
	if offset > len(fixture.polymarket) {
		offset = len(fixture.polymarket)
	}
	end := min(offset+limit, len(fixture.polymarket))
	page := append([]liveDiscoveredMarket(nil), fixture.polymarket[offset:end]...)
	next := ""
	if end < len(fixture.polymarket) {
		next = fmt.Sprintf("poly:%d", end)
	}
	return page, next
}
