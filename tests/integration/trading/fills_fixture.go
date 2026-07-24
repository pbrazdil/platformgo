package trading

import (
	"strconv"
	"strings"
	"time"

	"github.com/upcomers-org/platformgo/internal/decimal"
)

type fillInstrument struct {
	Symbol, Exchange, Base, Quote, ProductType, Settlement string
	TakerFee                                               decimal.Decimal
}

type fillSeed struct {
	FillID, AccountID, UserID, OrderID, PositionID string
	Symbol, Side, Quantity, Price, Commission      string
	Liquidity, Entry, RealizedPnL, Leverage        string
	ExecutedAtMS                                   int64
}

type fillOrder struct {
	ID, Intent, BracketLeg, OrderType, Quantity string
	Status, FilledQuantity, RejectReason        string
}

type fillView struct {
	FillID, AccountID, UserID, OrderID, PositionID       string
	Symbol, Side, Quantity, Price, Commission            string
	QuoteQuantity, Reason, FilledAt                      string
	Exchange, Base, Quote, ProductType, FeeAsset         string
	Liquidity, FeeRate, TradeType, RealizedPnL, Leverage string
	OrderType                                            *string
}

type fillQuery struct {
	AccountID, Side, TradeID string
	Limit                    int
	Cursor                   string
}

type fillPage struct {
	Items      []fillView
	Total      int
	NextCursor string
}

type sagaRecord struct {
	ID, Type, Correlation, State, Status string
	History                              []string
}

// fillFixture replaces Postgres, the application container, and runtime
// catalog loading with one synchronous authoritative writer.
type fillFixture struct {
	instruments map[string]fillInstrument
	fills       []fillSeed
	orders      map[string]*fillOrder
	sagas       map[string]*sagaRecord
}

func newFillFixture() *fillFixture {
	return &fillFixture{
		instruments: map[string]fillInstrument{
			"BTC-PERP": {
				Symbol: "BTC-PERP", Exchange: "BBOOK", Base: "BTC", Quote: "USDC",
				ProductType: "perp", Settlement: "USDC", TakerFee: decimal.MustParse("0.0005"),
			},
			"ETH-PERP": {
				Symbol: "ETH-PERP", Exchange: "BBOOK", Base: "ETH", Quote: "USDC",
				ProductType: "perp", Settlement: "USDC", TakerFee: decimal.MustParse("0.0005"),
			},
		},
		orders: make(map[string]*fillOrder),
		sagas:  make(map[string]*sagaRecord),
	}
}

func (fixture *fillFixture) seedFill(fill fillSeed) {
	fixture.fills = append(fixture.fills, fill)
}

func (fixture *fillFixture) queryFills(query fillQuery) fillPage {
	filtered := make([]fillSeed, 0)
	for index := len(fixture.fills) - 1; index >= 0; index-- {
		fill := fixture.fills[index]
		if fill.AccountID != query.AccountID {
			continue
		}
		if query.Side != "" && !strings.EqualFold(fill.Side, query.Side) {
			continue
		}
		if query.TradeID != "" && fill.FillID != query.TradeID {
			continue
		}
		filtered = append(filtered, fill)
	}
	offset := 0
	if query.Cursor != "" {
		offset, _ = strconv.Atoi(query.Cursor)
	}
	limit := query.Limit
	if limit <= 0 {
		limit = 100
	}
	end := min(offset+limit, len(filtered))
	items := make([]fillView, 0, end-offset)
	for _, fill := range filtered[offset:end] {
		items = append(items, fixture.view(fill))
	}
	next := ""
	if end < len(filtered) {
		next = strconv.Itoa(end)
	}
	return fillPage{Items: items, Total: len(filtered), NextCursor: next}
}

func (fixture *fillFixture) view(fill fillSeed) fillView {
	instrument := fixture.instruments[fill.Symbol]
	quantity := decimal.MustParse(fill.Quantity)
	price := decimal.MustParse(fill.Price)
	order := fixture.orders[fill.OrderID]
	reason, orderType := "manual", (*string)(nil)
	if order != nil {
		orderType = &order.OrderType
		switch {
		case order.BracketLeg == "stop_loss":
			reason = "stop_loss"
		case order.BracketLeg == "take_profit":
			reason = "take_profit"
		case strings.HasPrefix(order.Intent, "stopout:"):
			reason = "liquidation"
		case strings.HasPrefix(order.Intent, "flatten:"):
			reason = "flatten"
		}
	}
	feeAsset, feeRate := "", ""
	if fill.Liquidity == "taker" {
		feeAsset = instrument.Settlement
		feeRate = instrument.TakerFee.Normalize().String()
	}
	return fillView{
		FillID: fill.FillID, AccountID: fill.AccountID, UserID: fill.UserID,
		OrderID: orderURN(fill.OrderID), PositionID: positionURN(fill.PositionID),
		Symbol: fill.Symbol, Side: fill.Side, Quantity: fill.Quantity, Price: fill.Price,
		Commission:    decimal.MustParse(fill.Commission).Normalize().String(),
		QuoteQuantity: quantity.Mul(price).Normalize().String(),
		Reason:        reason, FilledAt: time.UnixMilli(fill.ExecutedAtMS).UTC().Format(time.RFC3339Nano),
		Exchange: instrument.Exchange, Base: instrument.Base, Quote: instrument.Quote,
		ProductType: instrument.ProductType, FeeAsset: feeAsset, Liquidity: fill.Liquidity,
		FeeRate:   feeRate,
		TradeType: fill.Entry, RealizedPnL: normalizedOptional(fill.RealizedPnL),
		Leverage: normalizedOptional(fill.Leverage), OrderType: orderType,
	}
}

func normalizedOptional(value string) string {
	if value == "" {
		return ""
	}
	return decimal.MustParse(value).Normalize().String()
}

func orderURN(value string) string {
	if value == "" {
		return ""
	}
	return "urn:uzo:order:" + value
}

func positionURN(value string) string {
	if value == "" {
		return ""
	}
	return "urn:uzo:position:" + value
}

func (fixture *fillFixture) insertOrder(order fillOrder) {
	copy := order
	if copy.Status == "" {
		copy.Status = "pending"
	}
	if copy.FilledQuantity == "" {
		copy.FilledQuantity = "0"
	}
	fixture.orders[order.ID] = &copy
}

func (fixture *fillFixture) settleOneFromFills(orderID string) bool {
	order := fixture.orders[orderID]
	if order == nil || order.Status == "filled" {
		return false
	}
	total := decimal.Decimal{}
	for _, fill := range fixture.fills {
		if fill.OrderID == orderID {
			total = total.Add(decimal.MustParse(fill.Quantity))
		}
	}
	if total.IsZero() {
		return false
	}
	order.Status, order.FilledQuantity = "filled", total.Normalize().String()
	return true
}

func (fixture *fillFixture) markRejected(orderID, reason string) bool {
	order := fixture.orders[orderID]
	if order == nil || order.Status != "pending" {
		return false
	}
	order.Status, order.RejectReason = "rejected", reason
	return true
}

func (fixture *fillFixture) startSaga(record sagaRecord) {
	copy := record
	copy.Status = "running"
	fixture.sagas[record.ID] = &copy
}

func (fixture *fillFixture) advanceSaga(id, state, status string) {
	saga := fixture.sagas[id]
	saga.State, saga.Status = state, status
	if status != "running" {
		saga.History = append(saga.History, state)
	}
}
