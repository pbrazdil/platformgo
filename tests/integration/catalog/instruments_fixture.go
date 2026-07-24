package catalog

import (
	"errors"
	"sort"
	"strings"
)

type instrumentRecord struct {
	Symbol          string `json:"symbol"`
	DisplayName     string `json:"displayName"`
	BaseAsset       string `json:"baseAsset"`
	QuoteAsset      string `json:"quoteAsset"`
	SettlementAsset string `json:"settlementAsset"`
	AssetClass      string `json:"assetClass"`
	ProductType     string `json:"productType"`
	ProductClass    string `json:"productClass"`
	Provenance      string `json:"provenance"`
	IsInverse       bool   `json:"isInverse"`
	Multiplier      string `json:"multiplier"`
	LotSize         string `json:"lotSize"`
	PriceIncrement  string `json:"priceIncrement"`
	SizeIncrement   string `json:"sizeIncrement"`
	MinQuantity     string `json:"minQuantity"`
	MaxQuantity     string `json:"maxQuantity"`
	MinNotional     string `json:"minNotional"`
	MaxNotional     string `json:"maxNotional"`
	PositionCap     string `json:"positionCap"`
	MaxLeverage     int    `json:"maxLeverage"`
	MakerFee        string `json:"makerFee"`
	TakerFee        string `json:"takerFee"`
	MarginInit      string `json:"marginInit"`
	MarginMaint     string `json:"marginMaint"`
	TradingMode     string `json:"tradingMode"`
	CanOpen         bool   `json:"canOpen"`
	Enabled         bool   `json:"enabled"`
}

type leverageLimits struct {
	DefaultMax *int           `json:"defaultMax"`
	Overrides  map[string]int `json:"overrides"`
}

type cachedInstrument struct {
	value    instrumentRecord
	cachedAt uint64
}

type instrumentFixture struct {
	rows      map[string]instrumentRecord
	cache     map[string]cachedInstrument
	nowMillis uint64
}

func newInstrumentFixture() *instrumentFixture {
	return &instrumentFixture{
		rows:  make(map[string]instrumentRecord),
		cache: make(map[string]cachedInstrument),
	}
}

func solPerp() instrumentRecord {
	return instrumentRecord{
		Symbol: "SOL-PERP", DisplayName: "Solana Perpetual",
		BaseAsset: "SOL", QuoteAsset: "USDC", SettlementAsset: "USDC",
		AssetClass: "crypto", ProductType: "perp", IsInverse: false,
		Multiplier: "1", LotSize: "1", PriceIncrement: "0.01", SizeIncrement: "0.01",
		MinQuantity: "0.01", MaxQuantity: "100000",
		MinNotional: "10", MaxNotional: "10000000", PositionCap: "50000",
		MaxLeverage: 20, MakerFee: "0.0002", TakerFee: "0.0005",
		MarginInit: "0.05", MarginMaint: "0.025",
		TradingMode: "full", Enabled: true,
	}
}

func (fixture *instrumentFixture) seedInstrument(symbol, base string) error {
	row := solPerp()
	row.Symbol, row.DisplayName, row.BaseAsset = symbol, base+" Perpetual", base
	row.PriceIncrement, row.MaxLeverage = "0.1", 50
	return fixture.upsert(row)
}

func (fixture *instrumentFixture) upsert(row instrumentRecord) error {
	if strings.Contains(row.Symbol, ".") {
		return errors.New("symbol must not contain '.'")
	}
	row.ProductClass = deriveProductClass(row.AssetClass, row.ProductType)
	row.Provenance = "manual"
	row.CanOpen = row.Enabled && row.TradingMode == "full"
	fixture.rows[row.Symbol] = row
	return nil
}

func (fixture *instrumentFixture) patchAssetClass(symbol, assetClass string) error {
	row, ok := fixture.rows[symbol]
	if !ok {
		return errors.New("instrument not found")
	}
	row.AssetClass = assetClass
	row.ProductClass = deriveProductClass(row.AssetClass, row.ProductType)
	fixture.rows[symbol] = row
	return nil
}

func (fixture *instrumentFixture) list() []instrumentRecord {
	rows := make([]instrumentRecord, 0, len(fixture.rows))
	for _, row := range fixture.rows {
		rows = append(rows, row)
	}
	sort.Slice(rows, func(left, right int) bool {
		return rows[left].Symbol < rows[right].Symbol
	})
	return rows
}

func (fixture *instrumentFixture) bySymbol(symbol string) (instrumentRecord, bool) {
	row, ok := fixture.rows[symbol]
	return row, ok
}

func (fixture *instrumentFixture) symbols() []string {
	rows := fixture.list()
	symbols := make([]string, len(rows))
	for index, row := range rows {
		symbols[index] = row.Symbol
	}
	return symbols
}

func (fixture *instrumentFixture) limits() leverageLimits {
	limits := leverageLimits{Overrides: make(map[string]int)}
	for symbol, row := range fixture.rows {
		if row.ProductType == "perp" {
			limits.Overrides[symbol] = row.MaxLeverage
		}
	}
	return limits
}

func (fixture *instrumentFixture) importHyperliquid(symbols []string) error {
	for _, symbol := range symbols {
		row := solPerp()
		row.Symbol, row.BaseAsset, row.DisplayName = symbol+"-PERP", symbol, symbol+" Perpetual"
		row.MakerFee, row.TakerFee, row.MinNotional = "0.00015", "0.00045", "0"
		if err := fixture.upsert(row); err != nil {
			return err
		}
	}
	return nil
}

func (fixture *instrumentFixture) updateLeverageDirect(symbol string, leverage int) {
	row := fixture.rows[symbol]
	row.MaxLeverage = leverage
	fixture.rows[symbol] = row
}

func (fixture *instrumentFixture) getSymbolCached(symbol string, ttlMillis uint64) (instrumentRecord, bool) {
	if ttlMillis == 0 {
		row, ok := fixture.rows[symbol]
		return row, ok
	}
	if cached, ok := fixture.cache[symbol]; ok && fixture.nowMillis-cached.cachedAt <= ttlMillis {
		return cached.value, true
	}
	row, ok := fixture.rows[symbol]
	if ok {
		fixture.cache[symbol] = cachedInstrument{value: row, cachedAt: fixture.nowMillis}
	}
	return row, ok
}

func (fixture *instrumentFixture) advance(milliseconds uint64) {
	fixture.nowMillis += milliseconds
}

func deriveProductClass(assetClass, productType string) string {
	if assetClass == "crypto" && productType == "perp" {
		return "perps"
	}
	return assetClass
}
