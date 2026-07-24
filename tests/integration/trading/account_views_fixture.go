package trading

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"

	"github.com/upcomers-org/platformgo/internal/decimal"
)

type marginInstrument struct {
	Symbol, MarginInit, MarginMaint string
	MaxLeverage                     int
}

type marginConfigView struct {
	Asset, Leverage, MarginMode, MarginInit, MarginMaint string
	MaxLeverage                                          int
}

type marginOverride struct {
	Leverage, Mode string
}

type tradingViewError struct {
	Code, Message string
}

func (err *tradingViewError) Error() string {
	return err.Code + ": " + err.Message
}

type fleetPage struct {
	Items []string
	Total *int
}

type cursorPage struct {
	Items                  []string
	Total                  *int
	NextCursor, PrevCursor string
}

type balanceView struct {
	Currency, Total, Equity, Free, Locked string
	CrossEquity, UnrealizedPnL            string
	MarginRatio                           *string
	MaintenanceMargin                     string
}

type accountViewsFixture struct {
	owners      map[string]string
	instruments map[string]marginInstrument
	overrides   map[string]marginOverride
	orders      *orderFixture
	orderIDs    []string
	cash        map[string]string
}

func newAccountViewsFixture() *accountViewsFixture {
	return &accountViewsFixture{
		owners: map[string]string{"acct-1": "user-1"},
		instruments: map[string]marginInstrument{
			"BTC-PERP": {Symbol: "BTC-PERP", MarginInit: "0.02", MarginMaint: "0.01", MaxLeverage: 50},
		},
		overrides: make(map[string]marginOverride),
		orders:    newOrderFixture(),
		orderIDs:  make([]string, 0),
		cash:      make(map[string]string),
	}
}

func marginKey(accountID, symbol string) string {
	return accountID + ":" + symbol
}

func (fixture *accountViewsFixture) marginConfig(actor, accountID, symbol string) (marginConfigView, error) {
	if fixture.owners[accountID] != actor {
		return marginConfigView{}, &tradingViewError{Code: "forbidden", Message: "account belongs to another user"}
	}
	instrument, ok := fixture.instruments[symbol]
	if !ok {
		return marginConfigView{}, &tradingViewError{Code: "not_found", Message: "instrument not found"}
	}
	override := fixture.overrides[marginKey(accountID, symbol)]
	leverage := strconv.Itoa(instrument.MaxLeverage)
	if override.Leverage != "" {
		value := decimal.MustParse(override.Leverage)
		cap := decimal.MustParse(strconv.Itoa(instrument.MaxLeverage))
		if value.Cmp(cap) > 0 {
			value = cap
		}
		leverage = value.Normalize().String()
	}
	mode := override.Mode
	if mode == "" {
		mode = "cross"
	}
	return marginConfigView{
		Asset: symbol, Leverage: leverage, MaxLeverage: instrument.MaxLeverage,
		MarginMode: mode, MarginInit: normalizedOptional(instrument.MarginInit),
		MarginMaint: normalizedOptional(instrument.MarginMaint),
	}, nil
}

func (fixture *accountViewsFixture) setLeverage(actor, accountID, symbol, leverage string) error {
	if fixture.owners[accountID] != actor {
		return &tradingViewError{Code: "forbidden", Message: "account belongs to another user"}
	}
	instrument, ok := fixture.instruments[symbol]
	if !ok {
		return &tradingViewError{Code: "not_found", Message: "instrument not found"}
	}
	value := decimal.MustParse(leverage)
	integer := value.Quantize(0, decimal.RoundTowardZero)
	if value.Cmp(decimal.MustParse("1")) < 0 ||
		value.Cmp(decimal.MustParse(strconv.Itoa(instrument.MaxLeverage))) > 0 ||
		!integer.Equal(value) {
		return &tradingViewError{Code: "bad_request", Message: "leverage must be a whole number within the catalog cap"}
	}
	key := marginKey(accountID, symbol)
	override := fixture.overrides[key]
	override.Leverage = value.Normalize().String()
	fixture.overrides[key] = override
	return nil
}

func (fixture *accountViewsFixture) setMarginMode(actor, accountID, symbol, mode string) error {
	if fixture.owners[accountID] != actor {
		return &tradingViewError{Code: "forbidden", Message: "account belongs to another user"}
	}
	if _, ok := fixture.instruments[symbol]; !ok {
		return &tradingViewError{Code: "not_found", Message: "instrument not found"}
	}
	if mode != "cross" && mode != "isolated" {
		return &tradingViewError{Code: "bad_request", Message: "invalid margin mode"}
	}
	key := marginKey(accountID, symbol)
	override := fixture.overrides[key]
	override.Mode = mode
	fixture.overrides[key] = override
	return nil
}

func (fixture *accountViewsFixture) putMarginConfig(actor, accountID, symbol, mode, leverage string) error {
	if mode != "cross" && mode != "isolated" {
		return &tradingViewError{Code: "bad_request", Message: "invalid margin mode"}
	}
	if fixture.owners[accountID] != actor {
		return &tradingViewError{Code: "forbidden", Message: "account belongs to another user"}
	}
	instrument, ok := fixture.instruments[symbol]
	if !ok {
		return &tradingViewError{Code: "not_found", Message: "instrument not found"}
	}
	value := decimal.MustParse(leverage)
	integer := value.Quantize(0, decimal.RoundTowardZero)
	if value.Cmp(decimal.MustParse("1")) < 0 ||
		value.Cmp(decimal.MustParse(strconv.Itoa(instrument.MaxLeverage))) > 0 ||
		!integer.Equal(value) {
		return &tradingViewError{Code: "bad_request", Message: "invalid leverage"}
	}
	fixture.overrides[marginKey(accountID, symbol)] = marginOverride{
		Mode: mode, Leverage: value.Normalize().String(),
	}
	return nil
}

func (fixture *accountViewsFixture) fleetBlotter(actor, resource string) (fleetPage, error) {
	if actor != "admin" {
		return fleetPage{}, &tradingViewError{Code: "forbidden", Message: resource + " requires admin"}
	}
	total := 0
	return fleetPage{Items: []string{}, Total: &total}, nil
}

func (fixture *accountViewsFixture) riskMonitor(actor string) ([]string, error) {
	if actor != "admin" {
		return nil, &tradingViewError{Code: "forbidden", Message: "risk monitor requires admin"}
	}
	return []string{}, nil
}

func (fixture *accountViewsFixture) addPagedOrder(intentID string) {
	command := limitBuyFixture(intentID)
	order, err := fixture.orders.submit(command)
	if err != nil {
		panic(err)
	}
	fixture.orderIDs = append([]string{orderURN(order.ID)}, fixture.orderIDs...)
}

func limitBuyFixture(intentID string) orderCommand {
	return orderCommand{
		AccountID: "acct-1", UserID: "user-1", IntentID: intentID,
		Symbol: "BTC-PERP", Side: "buy", OrderType: "MARKET",
		Quantity: "0.001", TimeInForce: "GTC",
	}
}

func encodeOrderCursor(offset int) string {
	return base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf("orders:%d", offset)))
}

func decodeOrderCursor(cursor string) (int, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return 0, &tradingViewError{Code: "bad_request", Message: "invalid cursor"}
	}
	parts := strings.Split(string(decoded), ":")
	if len(parts) != 2 || parts[0] != "orders" {
		return 0, &tradingViewError{Code: "bad_request", Message: "invalid cursor"}
	}
	offset, err := strconv.Atoi(parts[1])
	if err != nil || offset < 0 {
		return 0, &tradingViewError{Code: "bad_request", Message: "invalid cursor"}
	}
	return offset, nil
}

func (fixture *accountViewsFixture) ordersPage(limit int, cursor, direction string) (cursorPage, error) {
	if limit <= 0 {
		limit = 50
	}
	offset := 0
	if cursor != "" {
		var err error
		offset, err = decodeOrderCursor(cursor)
		if err != nil {
			return cursorPage{}, err
		}
	}
	if direction != "" && direction != "next" && direction != "prev" {
		return cursorPage{}, &tradingViewError{Code: "bad_request", Message: "invalid direction"}
	}
	if offset > len(fixture.orderIDs) {
		offset = len(fixture.orderIDs)
	}
	end := min(offset+limit, len(fixture.orderIDs))
	page := cursorPage{Items: append([]string(nil), fixture.orderIDs[offset:end]...)}
	if cursor == "" {
		total := len(fixture.orderIDs)
		page.Total = &total
	}
	if end < len(fixture.orderIDs) {
		page.NextCursor = encodeOrderCursor(end)
	}
	if offset > 0 {
		page.PrevCursor = encodeOrderCursor(max(0, offset-limit))
	}
	return page, nil
}

func (fixture *accountViewsFixture) positionsPage(status string, limit int, direction string) (cursorPage, error) {
	switch status {
	case "", "open", "closed", "liquidated":
	default:
		return cursorPage{}, &tradingViewError{Code: "bad_request", Message: "invalid position status"}
	}
	total := 0
	return cursorPage{Items: []string{}, Total: &total}, nil
}

func (fixture *accountViewsFixture) seedCash(accountID, total, _locked, _free string) {
	fixture.cash[accountID] = total
}

func (fixture *accountViewsFixture) balance(accountID string) balanceView {
	total := decimal.MustParse(fixture.cash[accountID])
	locked := decimal.Decimal{}
	for _, order := range fixture.orders.orders {
		if order.AccountID != accountID || order.Status != "working" || order.ReduceOnly || order.LimitPrice == "" {
			continue
		}
		instrument := fixture.instruments[order.Symbol]
		numerator := decimal.MustParse(order.Quantity).
			Mul(decimal.MustParse(order.LimitPrice)).
			Mul(decimal.MustParse(instrument.MarginInit))
		config, _ := fixture.marginConfig(fixture.owners[accountID], accountID, order.Symbol)
		reservation, err := numerator.Quo(decimal.MustParse(config.Leverage), 16, decimal.RoundHalfEven)
		if err != nil {
			panic(err)
		}
		locked = locked.Add(reservation)
	}
	equity := total
	free := equity.Sub(locked)
	return balanceView{
		Currency: "USDC", Total: total.Normalize().String(), Equity: equity.Normalize().String(),
		Free: free.Normalize().String(), Locked: locked.Normalize().String(),
		CrossEquity: equity.Normalize().String(), UnrealizedPnL: "0",
		MarginRatio: nil, MaintenanceMargin: "0",
	}
}
