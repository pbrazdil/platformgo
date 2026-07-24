package trading

import (
	"encoding/base64"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/upcomers-org/platformgo/internal/decimal"
)

type scopedInstrument struct {
	Symbol, AssetClass, ProductType string
}

type armedSaga struct {
	Type, Status, CorrelationID string
}

type fundingSeed struct {
	ID, Account, Symbol, PositionID string
	SignedQuantity, OraclePrice     string
	Rate, Amount, Currency          string
	FundingTime                     time.Time
}

type fundingPage struct {
	Items      []fundingSeed
	Total      *int
	NextCursor string
}

type transportOrder struct {
	ID, AccountID, IntentID, Symbol, Status string
}

type transportResponse struct {
	Status, RetryAfter int
	Code, OrderID      string
}

type remainingTradingFixture struct {
	instruments  map[string]scopedInstrument
	owners       map[string]string
	venues       map[string]string
	tradingModes map[string]string
	sagas        map[string]armedSaga
	funding      []fundingSeed
	controlState map[string]string
	cancelEvents int
	rateMax      int
	rateCounts   map[string]int
	restOrders   []transportOrder
	balances     map[string][]string
	slippage     map[string]string
	nextOrderID  int
	now          time.Time
}

func newRemainingTradingFixture() *remainingTradingFixture {
	return &remainingTradingFixture{
		instruments: map[string]scopedInstrument{
			"BTC-PERP":      {Symbol: "BTC-PERP", AssetClass: "crypto", ProductType: "perp"},
			"XAU-PERP":      {Symbol: "XAU-PERP", AssetClass: "metals", ProductType: "perp"},
			"XAU-FUT":       {Symbol: "XAU-FUT", AssetClass: "metals", ProductType: "future"},
			"PRESIDENT-YES": {Symbol: "PRESIDENT-YES", AssetClass: "prediction", ProductType: "binary_option"},
		},
		owners:       map[string]string{"acct-1": "user-1", "acct-2": "user-2"},
		venues:       make(map[string]string),
		tradingModes: map[string]string{"BTC-PERP": "full"},
		sagas:        make(map[string]armedSaga),
		controlState: make(map[string]string),
		rateMax:      5,
		rateCounts:   make(map[string]int),
		balances:     map[string][]string{"acct-1": {"USDC"}},
		slippage:     make(map[string]string),
		now:          time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC),
	}
}

func (fixture *remainingTradingFixture) submitScoped(accountID, symbol string) error {
	venue := fixture.venues[accountID]
	instrument := fixture.instruments[symbol]
	allowed := false
	switch venue {
	case "hyperliquid":
		allowed = instrument.AssetClass == "crypto" && instrument.ProductType == "perp"
	case "fix_cfd":
		allowed = instrument.AssetClass == "metals" && instrument.ProductType == "perp"
	case "fix_futures":
		allowed = instrument.ProductType == "future"
	case "polymarket", "kalshi":
		allowed = instrument.ProductType == "binary_option"
	}
	if !allowed {
		return &tradingViewError{
			Code: "asset_scope_denied", Message: "account venue " + venue + " does not admit " + symbol,
		}
	}
	return nil
}

func (fixture *remainingTradingFixture) placeBracket(accountID, intentID string) (string, error) {
	if fixture.owners[accountID] == "" {
		return "", &tradingViewError{Code: "not_found", Message: "account not found"}
	}
	fixture.nextOrderID++
	entryID := "bracket-entry-" + strconv.Itoa(fixture.nextOrderID)
	fixture.sagas[entryID] = armedSaga{
		Type: "submit_order", Status: "running", CorrelationID: entryID,
	}
	return entryID, nil
}

func (fixture *remainingTradingFixture) submitByTradingMode(symbol string, opening bool) int {
	switch fixture.tradingModes[symbol] {
	case "full":
		return 202
	case "disabled":
		return 400
	case "close_only":
		if opening {
			return 400
		}
		return 202
	default:
		return 400
	}
}

func (fixture *remainingTradingFixture) seedFunding(account, symbol, positionID, id, amount string, secondsAgo int64) {
	fixture.funding = append(fixture.funding, fundingSeed{
		ID: id, Account: account, Symbol: symbol, PositionID: positionID,
		SignedQuantity: "1", OraclePrice: "1000", Rate: "0.0000125",
		Amount: amount, Currency: "USDC", FundingTime: fixture.now.Add(-time.Duration(secondsAgo) * time.Second),
	})
}

func encodeFundingCursor(offset int) string {
	return base64.RawURLEncoding.EncodeToString([]byte("funding:" + strconv.Itoa(offset)))
}

func decodeFundingCursor(cursor string) (int, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return 0, &tradingViewError{Code: "bad_request", Message: "invalid funding cursor"}
	}
	parts := strings.Split(string(raw), ":")
	if len(parts) != 2 || parts[0] != "funding" {
		return 0, &tradingViewError{Code: "bad_request", Message: "invalid funding cursor"}
	}
	offset, err := strconv.Atoi(parts[1])
	if err != nil || offset < 0 {
		return 0, &tradingViewError{Code: "bad_request", Message: "invalid funding cursor"}
	}
	return offset, nil
}

func (fixture *remainingTradingFixture) fundingPage(account string, limit int, cursor string) (fundingPage, error) {
	rows := make([]fundingSeed, 0)
	for _, row := range fixture.funding {
		if row.Account == account {
			copy := row
			copy.Amount = normalizedOptional(copy.Amount)
			copy.OraclePrice = normalizedOptional(copy.OraclePrice)
			copy.Rate = normalizedOptional(copy.Rate)
			copy.SignedQuantity = normalizedOptional(copy.SignedQuantity)
			copy.PositionID = positionURN(copy.PositionID)
			rows = append(rows, copy)
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].FundingTime.After(rows[j].FundingTime) })
	offset := 0
	if cursor != "" {
		var err error
		offset, err = decodeFundingCursor(cursor)
		if err != nil {
			return fundingPage{}, err
		}
	}
	if limit <= 0 {
		limit = 50
	}
	end := min(offset+limit, len(rows))
	page := fundingPage{Items: append([]fundingSeed(nil), rows[offset:end]...)}
	if cursor == "" {
		total := len(rows)
		page.Total = &total
	}
	if end < len(rows) {
		page.NextCursor = encodeFundingCursor(end)
	}
	return page, nil
}

func (fixture *remainingTradingFixture) fundingPaid(positionID string, since time.Time) string {
	total := decimal.Decimal{}
	for _, row := range fixture.funding {
		if row.PositionID == positionID && !row.FundingTime.Before(since) {
			total = total.Add(decimal.MustParse(row.Amount))
		}
	}
	return total.Normalize().String()
}

func (fixture *remainingTradingFixture) fleetFunding(symbol string) []fundingSeed {
	rows := make([]fundingSeed, 0)
	for _, row := range fixture.funding {
		if row.Symbol == symbol {
			rows = append(rows, row)
		}
	}
	return rows
}

func (fixture *remainingTradingFixture) insertControlOrder(orderID string) {
	fixture.controlState[orderID] = "pending"
}

func (fixture *remainingTradingFixture) reissueCancel(orderID string, finalAttempt bool) {
	if !finalAttempt && fixture.controlState[orderID] != "canceled" {
		fixture.cancelEvents++
	}
}

func (fixture *remainingTradingFixture) cancelOrder(orderID string) bool {
	if fixture.controlState[orderID] == "canceled" {
		return false
	}
	fixture.controlState[orderID] = "canceled"
	return true
}

func (fixture *remainingTradingFixture) protectedRequest(principal string) transportResponse {
	fixture.rateCounts[principal]++
	if fixture.rateCounts[principal] > fixture.rateMax {
		return transportResponse{Status: 429, RetryAfter: 60, Code: "too_many_requests"}
	}
	return transportResponse{Status: 200}
}

func (fixture *remainingTradingFixture) publicCatalogRequest() transportResponse {
	return transportResponse{Status: 200}
}

func (fixture *remainingTradingFixture) login(user, password string) transportResponse {
	if (user == "user-1" || user == "user-2") && password == "correct horse battery staple" {
		return transportResponse{Status: 200}
	}
	return transportResponse{Status: 401}
}

func (fixture *remainingTradingFixture) publicInstruments() []string {
	result := make([]string, 0, len(fixture.instruments))
	for symbol := range fixture.instruments {
		result = append(result, symbol)
	}
	sort.Strings(result)
	return result
}

func (fixture *remainingTradingFixture) restSubmit(principal, accountURN, intentID, symbol string) transportResponse {
	if principal == "" {
		return transportResponse{Status: 401}
	}
	if !strings.HasPrefix(accountURN, "urn:uzo:account:") {
		return transportResponse{Status: 400}
	}
	accountID := strings.TrimPrefix(accountURN, "urn:uzo:account:")
	if fixture.owners[accountID] != principal {
		return transportResponse{Status: 403}
	}
	fixture.nextOrderID++
	orderID := "urn:xb:order:" + strconv.Itoa(fixture.nextOrderID)
	fixture.restOrders = append(fixture.restOrders, transportOrder{
		ID: orderID, AccountID: accountID, IntentID: intentID, Symbol: symbol, Status: "pending",
	})
	return transportResponse{Status: 202, OrderID: orderID}
}

func (fixture *remainingTradingFixture) restOrdersFor(principal, accountID string) (int, []transportOrder) {
	if principal == "" {
		return 401, nil
	}
	if fixture.owners[accountID] != principal {
		return 403, nil
	}
	rows := make([]transportOrder, 0)
	for _, order := range fixture.restOrders {
		if order.AccountID == accountID {
			rows = append(rows, order)
		}
	}
	return 200, rows
}

func (fixture *remainingTradingFixture) restPositions(principal, accountURN string) (int, []string) {
	if principal == "" {
		return 401, nil
	}
	if !strings.HasPrefix(accountURN, "urn:uzo:account:") {
		return 400, nil
	}
	accountID := strings.TrimPrefix(accountURN, "urn:uzo:account:")
	if fixture.owners[accountID] != principal {
		return 403, nil
	}
	return 200, []string{}
}

func (fixture *remainingTradingFixture) restBalances(principal, accountID string) (int, []string) {
	if principal == "" {
		return 401, nil
	}
	if fixture.owners[accountID] != principal {
		return 403, nil
	}
	return 200, append([]string(nil), fixture.balances[accountID]...)
}

func (fixture *remainingTradingFixture) submitWithSlippage(intentID, orderType, requestedBPS string) {
	switch orderType {
	case "MARKET":
		if requestedBPS == "" {
			requestedBPS = "50"
		}
		fixture.slippage[intentID] = normalizedOptional(requestedBPS)
	default:
		fixture.slippage[intentID] = ""
	}
}
