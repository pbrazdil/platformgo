package trading

import (
	"fmt"
	"strings"

	"github.com/upcomers-org/platformgo/internal/decimal"
)

type orderInstrument struct {
	Symbol, ProductType, TakerFee         string
	SizeIncrement, PriceIncrement         string
	MinQuantity, MaxQuantity, PositionCap string
	MaxNotional                           string
}

type orderCommand struct {
	AccountID, UserID, IntentID, Symbol string
	Side, OrderType, Quantity           string
	LimitPrice, TriggerPrice            string
	TrailingOffset                      string
	ReduceOnly                          bool
	TimeInForce                         string
}

type orderRecord struct {
	ID, AccountID, UserID, IntentID, Symbol   string
	Side, OrderType, Quantity, LimitPrice     string
	TriggerPrice, TrailingOffset, TimeInForce string
	Status, FilledQuantity, AveragePrice      string
	BracketGroupID, BracketLeg                string
	ReduceOnly, Liquidation                   bool
	TriggeredAtMS                             *int64
}

type orderView struct {
	ID, AccountID, UserID, IntentID, Symbol   string
	Side, OrderType, Quantity, LimitPrice     string
	TriggerPrice, TrailingOffset, TimeInForce string
	Status, ProductType, TradingFeeRate       string
	CumulativeTradingFees, CumulativeQuote    string
	PositionID, FilledAt, BracketGroupID      string
	BracketLeg                                string
	ReduceOnly, Liquidation                   bool
	TriggeredAtMS                             *int64
}

type bracketLegSeed struct {
	ID, IntentID, Side, OrderType, Quantity string
	LimitPrice, TriggerPrice, BracketLeg    string
}

type orderFixtureError struct {
	Code, Message string
}

func (err *orderFixtureError) Error() string {
	return err.Code + ": " + err.Message
}

type orderFixture struct {
	fills       *fillFixture
	instrument  orderInstrument
	orders      map[string]*orderRecord
	brackets    map[string]string
	funded      map[string]bool
	positionQty map[string]decimal.Decimal
	nextID      int
	nextTimeMS  int64
}

func newOrderFixture() *orderFixture {
	return &orderFixture{
		fills: newFillFixture(),
		instrument: orderInstrument{
			Symbol: "BTC-PERP", ProductType: "perp", TakerFee: "0.0005",
			SizeIncrement: "0.001", PriceIncrement: "0.1",
			MinQuantity: "0.001", MaxQuantity: "1000",
			PositionCap: "100", MaxNotional: "10000000",
		},
		orders:      make(map[string]*orderRecord),
		brackets:    make(map[string]string),
		funded:      map[string]bool{"acct-1": true},
		positionQty: make(map[string]decimal.Decimal),
		nextTimeMS:  1_700_000_000_000,
	}
}

func (fixture *orderFixture) submit(command orderCommand) (*orderRecord, error) {
	if existing := fixture.orders[command.IntentID]; existing != nil {
		if sameOrderPayload(existing, command) {
			return existing, nil
		}
		return nil, &orderFixtureError{Code: "conflict", Message: "intent id reused with a different payload"}
	}
	if err := fixture.validate(command); err != nil {
		return nil, err
	}
	fixture.nextID++
	order := &orderRecord{
		ID: fmt.Sprintf("order-%d", fixture.nextID), AccountID: command.AccountID,
		UserID: command.UserID, IntentID: command.IntentID, Symbol: command.Symbol,
		Side: strings.ToUpper(command.Side), OrderType: command.OrderType,
		Quantity: command.Quantity, LimitPrice: command.LimitPrice,
		TriggerPrice: command.TriggerPrice, TrailingOffset: command.TrailingOffset,
		ReduceOnly: command.ReduceOnly, TimeInForce: command.TimeInForce, Status: "pending",
		Liquidation: strings.HasPrefix(command.IntentID, "stopout:"),
	}
	fixture.orders[command.IntentID] = order
	if !command.ReduceOnly {
		key := command.AccountID + ":" + command.Symbol
		fixture.positionQty[key] = fixture.positionQty[key].Add(decimal.MustParse(command.Quantity))
	}
	return order, nil
}

func (fixture *orderFixture) validate(command orderCommand) error {
	if command.OrderType == "TRAILING_STOP_MARKET" && command.TrailingOffset == "" {
		return &orderFixtureError{Code: "validation", Message: "trailing offset is required"}
	}
	if command.OrderType != "TRAILING_STOP_MARKET" && command.TrailingOffset != "" {
		return &orderFixtureError{Code: "validation", Message: "trailing offset is only valid for trailing stops"}
	}
	quantity := decimal.MustParse(command.Quantity)
	sizeStep := decimal.MustParse(fixture.instrument.SizeIncrement)
	wholeSteps, _ := quantity.Quo(sizeStep, 0, decimal.RoundTowardZero)
	if wholeSteps.IsZero() {
		return &orderFixtureError{Code: "min_size", Message: "quantity rounds to zero at the size step"}
	}
	if !wholeSteps.Mul(sizeStep).Equal(quantity) {
		return &orderFixtureError{Code: "precision_invalid", Message: "quantity violates size step"}
	}
	if command.LimitPrice != "" {
		price := decimal.MustParse(command.LimitPrice)
		priceStep := decimal.MustParse(fixture.instrument.PriceIncrement)
		wholeTicks, _ := price.Quo(priceStep, 0, decimal.RoundTowardZero)
		if !wholeTicks.Mul(priceStep).Equal(price) {
			return &orderFixtureError{Code: "precision_invalid", Message: "price violates price step"}
		}
	}
	if !command.ReduceOnly && !fixture.funded[command.AccountID] {
		return &orderFixtureError{Code: "insufficient_funds", Message: "insufficient free margin"}
	}
	key := command.AccountID + ":" + command.Symbol
	resulting := fixture.positionQty[key].Add(quantity)
	positionCap := decimal.MustParse(fixture.instrument.PositionCap)
	if !positionCap.IsZero() && resulting.Cmp(positionCap) > 0 {
		return &orderFixtureError{Code: "cap_exceeded", Message: "position cap exceeded"}
	}
	if command.LimitPrice != "" {
		notional := quantity.Mul(decimal.MustParse(command.LimitPrice))
		maxNotional := decimal.MustParse(fixture.instrument.MaxNotional)
		if !maxNotional.IsZero() && notional.Cmp(maxNotional) > 0 {
			return &orderFixtureError{Code: "cap_exceeded", Message: "notional cap exceeded"}
		}
	}
	return nil
}

func sameOrderPayload(order *orderRecord, command orderCommand) bool {
	return order.AccountID == command.AccountID && order.UserID == command.UserID &&
		order.Symbol == command.Symbol && order.Side == strings.ToUpper(command.Side) &&
		order.OrderType == command.OrderType && order.Quantity == command.Quantity &&
		order.LimitPrice == command.LimitPrice && order.TriggerPrice == command.TriggerPrice &&
		order.TrailingOffset == command.TrailingOffset && order.ReduceOnly == command.ReduceOnly &&
		order.TimeInForce == command.TimeInForce
}

func (fixture *orderFixture) applyFill(intentID, fillID, positionID, quantity, price, commission string, executedAtMS int64) {
	order := fixture.orders[intentID]
	fixture.fills.seedFill(fillSeed{
		FillID: fillID, AccountID: order.AccountID, UserID: order.UserID, OrderID: order.ID,
		PositionID: positionID, Symbol: order.Symbol, Side: order.Side, Quantity: quantity,
		Price: price, Commission: commission, Liquidity: "taker", ExecutedAtMS: executedAtMS,
	})
	order.Status, order.FilledQuantity, order.AveragePrice = "filled", quantity, price
}

func (fixture *orderFixture) markWorking(intentID string) {
	fixture.orders[intentID].Status = "working"
}

func (fixture *orderFixture) markTriggered(intentID string) bool {
	order := fixture.orders[intentID]
	if order.TriggeredAtMS != nil {
		return false
	}
	at := fixture.nextTimeMS
	fixture.nextTimeMS++
	order.TriggeredAtMS = &at
	return true
}

func (fixture *orderFixture) insertBracket(groupID string, entry bracketLegSeed, protective []bracketLegSeed) (string, bool) {
	if entryIntent := fixture.brackets[groupID]; entryIntent != "" {
		return fixture.orders[entryIntent].ID, false
	}
	fixture.brackets[groupID] = entry.IntentID
	fixture.insertBracketLeg(groupID, entry, false)
	for _, leg := range protective {
		fixture.insertBracketLeg(groupID, leg, true)
	}
	return fixture.orders[entry.IntentID].ID, true
}

func (fixture *orderFixture) insertBracketLeg(groupID string, leg bracketLegSeed, reduceOnly bool) {
	fixture.nextID++
	fixture.orders[leg.IntentID] = &orderRecord{
		ID: leg.ID, AccountID: "acct-1", UserID: "user-1", IntentID: leg.IntentID,
		Symbol: fixture.instrument.Symbol, Side: leg.Side, OrderType: leg.OrderType,
		Quantity: leg.Quantity, LimitPrice: leg.LimitPrice, TriggerPrice: leg.TriggerPrice,
		TimeInForce: "GTC", Status: "pending", BracketGroupID: groupID,
		BracketLeg: leg.BracketLeg, ReduceOnly: reduceOnly,
	}
}

func (fixture *orderFixture) view(intentID string) orderView {
	order := fixture.orders[intentID]
	view := orderView{
		ID: orderURN(order.ID), AccountID: order.AccountID, UserID: order.UserID,
		IntentID: order.IntentID, Symbol: order.Symbol, Side: order.Side,
		OrderType: order.OrderType, Quantity: order.Quantity, LimitPrice: order.LimitPrice,
		TriggerPrice: order.TriggerPrice, TrailingOffset: order.TrailingOffset,
		TimeInForce: order.TimeInForce, Status: order.Status, ProductType: fixture.instrument.ProductType,
		TradingFeeRate: normalizedOptional(fixture.instrument.TakerFee),
		BracketGroupID: order.BracketGroupID, BracketLeg: order.BracketLeg,
		ReduceOnly: order.ReduceOnly, Liquidation: order.Liquidation, TriggeredAtMS: order.TriggeredAtMS,
	}
	var fees, quote decimal.Decimal
	var latest int64
	for _, fill := range fixture.fills.fills {
		if fill.OrderID != order.ID {
			continue
		}
		fees = fees.Add(decimal.MustParse(fill.Commission))
		quote = quote.Add(decimal.MustParse(fill.Quantity).Mul(decimal.MustParse(fill.Price)))
		view.PositionID = positionURN(fill.PositionID)
		if fill.ExecutedAtMS >= latest {
			latest = fill.ExecutedAtMS
		}
	}
	if !fees.IsZero() {
		view.CumulativeTradingFees = fees.Normalize().String()
	}
	if !quote.IsZero() {
		view.CumulativeQuote = quote.Normalize().String()
	}
	if latest != 0 {
		view.FilledAt = fmt.Sprintf("%d", latest)
	}
	return view
}

type createUserCommand struct {
	Login, Email, Password string
}

func validateCreateUser(command createUserCommand) map[string]string {
	errors := make(map[string]string)
	if strings.TrimSpace(command.Login) == "" {
		errors["login"] = "is required"
	}
	if !strings.Contains(command.Email, "@") {
		errors["email"] = "must be a valid email"
	}
	if len(command.Password) < 8 {
		errors["password"] = "must be at least 8 characters"
	}
	return errors
}

func createUser(command createUserCommand) (string, map[string]string) {
	if errors := validateCreateUser(command); len(errors) != 0 {
		return "", errors
	}
	return "urn:uzo:user:" + command.Login, nil
}
