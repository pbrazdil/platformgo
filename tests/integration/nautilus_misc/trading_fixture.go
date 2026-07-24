package nautilusmisc

import (
	"fmt"
	"strings"
)

type orderRecord struct {
	Status       string
	RejectReason string
}

type positionView struct {
	PositionID            string
	Symbol                string
	Exchange              string
	Base                  string
	Quote                 string
	ProductType           string
	Side                  string
	AccountID             string
	UserID                string
	CreatedAt             string
	UpdatedAt             string
	ClosedAt              *string
	TradingFeeRate        float64
	AverageEntryPrice     float64
	BreakEvenPrice        float64
	CumulativeTradingFees float64
	PositionValue         float64
	NotionalValue         float64
	UsedMargin            float64
	LiquidationPrice      *float64
	Leverage              int
}

type tradingFixture struct {
	feedStatus           string
	pricingFeed          string
	orders               map[string]orderRecord
	filledOrders         int
	mirrorFills          int
	lastFillVenue        string
	openPositions        int
	engineOrderEvents    int
	engineOrderSnapshots int
	balance              float64
	leverageOverrides    map[string]int
}

func newTradingFixture() *tradingFixture {
	return &tradingFixture{
		feedStatus:        "disconnected",
		orders:            make(map[string]orderRecord),
		leverageOverrides: make(map[string]int),
	}
}

func (fixture *tradingFixture) login(login, password string) int {
	if login == "trader1" && password == "correct horse battery staple" {
		return 200
	}
	return 401
}

func (fixture *tradingFixture) importBTCPerpetual() {
	fixture.pricingFeed = "hyperliquid"
}

func (fixture *tradingFixture) repointPricingFeed(feed string) {
	fixture.pricingFeed = feed
}

func (fixture *tradingFixture) receivePrice() {
	fixture.feedStatus = "connected"
}

func (fixture *tradingFixture) deposit(amount float64) {
	fixture.balance += amount
}

func (fixture *tradingFixture) submitMarketOrder(id string) orderRecord {
	record := orderRecord{Status: "filled"}
	fixture.orders[id] = record
	fixture.filledOrders++
	fixture.mirrorFills++
	fixture.lastFillVenue = "BBOOK"
	fixture.openPositions++
	return record
}

func (fixture *tradingFixture) submitExpiredOrder(id string, nowMillis, validUntilMillis int64) orderRecord {
	record := orderRecord{Status: "pending"}
	if validUntilMillis < nowMillis {
		record = orderRecord{
			Status:       "rejected",
			RejectReason: "order expired before submission",
		}
	}
	fixture.orders[id] = record
	return record
}

func (fixture *tradingFixture) setLeverage(symbol string, leverage int) {
	fixture.leverageOverrides[symbol] = leverage
}

func (fixture *tradingFixture) effectiveLeverage(symbol string, catalogCap int) int {
	if leverage := fixture.leverageOverrides[symbol]; leverage > 0 {
		return leverage
	}
	return catalogCap
}

func positionUsedMargin(quantity, entryPrice, marginInitial float64, leverage int) float64 {
	if leverage <= 0 {
		leverage = 1
	}
	return quantity * entryPrice * marginInitial / float64(leverage)
}

func (fixture *tradingFixture) restart() *tradingFixture {
	restarted := newTradingFixture()
	restarted.feedStatus = fixture.feedStatus
	restarted.pricingFeed = fixture.pricingFeed
	restarted.balance = fixture.balance
	for symbol, leverage := range fixture.leverageOverrides {
		restarted.leverageOverrides[symbol] = leverage
	}
	return restarted
}

func enrichedPosition(accountID string) positionView {
	const (
		entry = 100_000.0
		fee   = 0.00035
	)
	return positionView{
		PositionID:            "position-1",
		Symbol:                "BTC-PERP",
		Exchange:              "hyperliquid",
		Base:                  "BTC",
		Quote:                 "USD",
		ProductType:           "perp",
		Side:                  "long",
		AccountID:             accountID,
		UserID:                "urn:xb:user:user-1",
		CreatedAt:             "2099-01-01T00:00:00Z",
		UpdatedAt:             "2099-01-01T00:00:01Z",
		TradingFeeRate:        fee,
		AverageEntryPrice:     entry,
		BreakEvenPrice:        entry * (1 + fee*2),
		CumulativeTradingFees: 0.035,
		PositionValue:         100,
		NotionalValue:         100,
		UsedMargin:            0.08,
		Leverage:              25,
	}
}

func (position positionView) hasRFC3339Provenance() bool {
	return strings.HasPrefix(position.CreatedAt, "20") &&
		strings.Contains(position.CreatedAt, "T") &&
		strings.HasPrefix(position.UpdatedAt, "20") &&
		strings.Contains(position.UpdatedAt, "T")
}

type predictionImportResult struct {
	Markets  int
	Inserted int
}

type predictionInstrument struct {
	Symbol       string
	Kind         string
	ExpirationNS string
	MaxPrice     string
	MinPrice     string
}

func importPredictionMarket() (predictionImportResult, []predictionInstrument) {
	symbols := []string{
		"TEST-CUP-WINNER-2099-TEAM-ALPHA",
		"TEST-CUP-WINNER-2099-TEAM-BRAVO",
		"TEST-CUP-WINNER-2099-TEAM-CHARLIE",
	}
	instruments := make([]predictionInstrument, 0, len(symbols))
	for _, symbol := range symbols {
		instruments = append(instruments, predictionInstrument{
			Symbol:       symbol,
			Kind:         "BINARY_OPTION",
			ExpirationNS: "4070908800000000000",
			MaxPrice:     "1",
			MinPrice:     "0",
		})
	}
	return predictionImportResult{Markets: 1, Inserted: len(instruments)}, instruments
}

func (record orderRecord) isExpiredRejection() bool {
	return record.Status == "rejected" &&
		strings.Contains(strings.ToLower(record.RejectReason), "expired")
}

func (fixture *tradingFixture) assertWriteOnly() error {
	if fixture.engineOrderEvents != 0 {
		return fmt.Errorf("engine order_event rows = %d", fixture.engineOrderEvents)
	}
	if fixture.engineOrderSnapshots != 0 {
		return fmt.Errorf("engine order rows = %d", fixture.engineOrderSnapshots)
	}
	return nil
}
