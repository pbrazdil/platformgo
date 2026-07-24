package catalog

import (
	"encoding/json"
	"errors"
	"sort"
)

const (
	statusOK         = 200
	statusBadRequest = 400
	statusForbidden  = 403
)

type fixFeedConfig struct {
	Host          string  `json:"host"`
	Port          uint16  `json:"port"`
	SenderCompID  string  `json:"sender_comp_id"`
	TargetCompID  string  `json:"target_comp_id"`
	HeartbeatSecs uint64  `json:"heartbeat_secs"`
	UseTLS        bool    `json:"use_tls"`
	UsernameEnv   *string `json:"username_env,omitempty"`
	PasswordEnv   *string `json:"password_env,omitempty"`
}

type feedRecord struct {
	Name          string `json:"name"`
	Protocol      string `json:"protocol"`
	Ingestion     string `json:"ingestion"`
	Enabled       bool   `json:"enabled"`
	TradingConfig string `json:"tradingConfig,omitempty"`
}

type feedFixture struct {
	feeds map[string]feedRecord
}

func newFeedFixture() *feedFixture {
	return &feedFixture{feeds: make(map[string]feedRecord)}
}

func standardFixConfig() fixFeedConfig {
	username, password := "LP_FIX_USERNAME", "LP_FIX_PASSWORD"
	return fixFeedConfig{
		Host: "fix.lp.example.com", Port: 9876,
		SenderCompID: "UZO", TargetCompID: "LP", HeartbeatSecs: 30,
		UseTLS: true, UsernameEnv: &username, PasswordEnv: &password,
	}
}

func (fixture *feedFixture) createFix(name string, tradingConfig *string, enabled bool) (feedRecord, error) {
	if tradingConfig == nil {
		return feedRecord{}, errors.New("FIX trading_config is required")
	}
	var config fixFeedConfig
	if err := json.Unmarshal([]byte(*tradingConfig), &config); err != nil ||
		config.Host == "" || config.Port == 0 || config.SenderCompID == "" ||
		config.TargetCompID == "" || config.HeartbeatSecs == 0 {
		return feedRecord{}, errors.New("invalid FIX trading_config")
	}
	row := feedRecord{
		Name: name, Protocol: "fix", Ingestion: "nautilus_adapter",
		Enabled: enabled, TradingConfig: *tradingConfig,
	}
	fixture.feeds[name] = row
	return row, nil
}

func (fixture *feedFixture) list() []feedRecord {
	rows := make([]feedRecord, 0, len(fixture.feeds))
	for _, row := range fixture.feeds {
		rows = append(rows, row)
	}
	sort.Slice(rows, func(left, right int) bool { return rows[left].Name < rows[right].Name })
	return rows
}

type adminCatalogFixture struct {
	instruments *instrumentFixture
	feeds       *feedFixture
}

func newAdminCatalogFixture() *adminCatalogFixture {
	return &adminCatalogFixture{
		instruments: newInstrumentFixture(),
		feeds:       newFeedFixture(),
	}
}

func (fixture *adminCatalogFixture) upsert(canCreate bool, row instrumentRecord) int {
	if !canCreate {
		return statusForbidden
	}
	if err := fixture.instruments.upsert(row); err != nil {
		return statusBadRequest
	}
	return statusOK
}

func (fixture *adminCatalogFixture) list(canRead bool) (int, []instrumentRecord) {
	if !canRead {
		return statusForbidden, nil
	}
	return statusOK, fixture.instruments.list()
}

func (fixture *adminCatalogFixture) get(canRead bool, symbol string) (int, instrumentRecord) {
	if !canRead {
		return statusForbidden, instrumentRecord{}
	}
	row, ok := fixture.instruments.bySymbol(symbol)
	if !ok {
		return 404, instrumentRecord{}
	}
	return statusOK, row
}

func (fixture *adminCatalogFixture) patchLeverage(symbol string, leverage int) int {
	row, ok := fixture.instruments.bySymbol(symbol)
	if !ok {
		return 404
	}
	row.MaxLeverage = leverage
	if err := fixture.instruments.upsert(row); err != nil {
		return statusBadRequest
	}
	return statusOK
}

func (fixture *adminCatalogFixture) retire(symbol string) int {
	row, ok := fixture.instruments.bySymbol(symbol)
	if !ok {
		return 404
	}
	row.Enabled, row.CanOpen = false, false
	fixture.instruments.rows[symbol] = row
	return statusOK
}

func (fixture *adminCatalogFixture) setTradingMode(symbol, mode string) int {
	row, ok := fixture.instruments.bySymbol(symbol)
	if !ok {
		return 404
	}
	row.TradingMode, row.CanOpen = mode, row.Enabled && mode == "full"
	fixture.instruments.rows[symbol] = row
	return statusOK
}

func (fixture *adminCatalogFixture) setEnabled(symbol string, enabled bool) int {
	row, ok := fixture.instruments.bySymbol(symbol)
	if !ok {
		return 404
	}
	row.Enabled, row.CanOpen = enabled, enabled && row.TradingMode == "full"
	fixture.instruments.rows[symbol] = row
	return statusOK
}

func (fixture *adminCatalogFixture) feedListStatus() int   { return statusOK }
func (fixture *adminCatalogFixture) feedHealthStatus() int { return statusOK }
func (fixture *adminCatalogFixture) discoverFeedStatus(venue string) int {
	if venue != "hyperliquid" && venue != "polymarket" {
		return statusBadRequest
	}
	return statusOK
}

type inventoryVendorMeta struct {
	Venue  string `json:"venue"`
	IsHIP3 bool   `json:"is_hip3"`
}

type inventoryRow struct {
	Symbol           string               `json:"symbol"`
	VenueKind        string               `json:"venueKind"`
	ProductClass     string               `json:"productClass"`
	MarketDataStatus string               `json:"marketDataStatus"`
	Enabled          bool                 `json:"enabled"`
	VendorMeta       *inventoryVendorMeta `json:"vendorMeta"`
}

type inventoryCounts struct {
	Total    uint64 `json:"total"`
	Live     uint64 `json:"live"`
	Stale    uint64 `json:"stale"`
	Closed   uint64 `json:"closed"`
	Enabled  uint64 `json:"enabled"`
	Disabled uint64 `json:"disabled"`
}

type inventoryResult struct {
	Rows   []inventoryRow  `json:"rows"`
	Counts inventoryCounts `json:"counts"`
}

type inventoryFixture struct {
	rows []inventoryRow
}

func newInventoryFixture() *inventoryFixture {
	return &inventoryFixture{rows: []inventoryRow{
		{
			Symbol: "BTC-PERP", VenueKind: "dex", ProductClass: "perps",
			MarketDataStatus: "fresh", Enabled: true,
			VendorMeta: &inventoryVendorMeta{Venue: "hyperliquid", IsHIP3: true},
		},
		{
			Symbol: "ETH-PERP", VenueKind: "dex", ProductClass: "perps",
			MarketDataStatus: "fresh", Enabled: true,
		},
		{
			Symbol: predictionBinaryLegSymbols()[0], VenueKind: "dex", ProductClass: "predictions",
			MarketDataStatus: "closed", Enabled: false,
		},
		{
			Symbol: predictionBinaryLegSymbols()[1], VenueKind: "dex", ProductClass: "predictions",
			MarketDataStatus: "stale", Enabled: true,
		},
	}}
}

func (fixture *inventoryFixture) query(canRead bool, venueKind, status string) (int, inventoryResult) {
	if !canRead {
		return statusForbidden, inventoryResult{}
	}
	if status != "" && status != "fresh" && status != "stale" && status != "closed" {
		return statusBadRequest, inventoryResult{}
	}
	rows := make([]inventoryRow, 0)
	for _, row := range fixture.rows {
		if venueKind != "" && row.VenueKind != venueKind {
			continue
		}
		if status != "" && row.MarketDataStatus != status {
			continue
		}
		rows = append(rows, row)
	}
	result := inventoryResult{Rows: rows}
	result.Counts.Total = uint64(len(rows))
	for _, row := range rows {
		switch row.MarketDataStatus {
		case "fresh":
			result.Counts.Live++
		case "stale":
			result.Counts.Stale++
		case "closed":
			result.Counts.Closed++
		}
		if row.Enabled {
			result.Counts.Enabled++
		} else {
			result.Counts.Disabled++
		}
	}
	return statusOK, result
}
