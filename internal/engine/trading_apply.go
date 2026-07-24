package engine

import (
	"bytes"
	"fmt"
	"sort"

	"github.com/upcomers-org/platformgo/internal/domain"
)

// ApplyTrading binds a typed action to its canonical envelope payload, then
// applies it through the same sequencing, receipt, decision, and hash boundary
// as every other engine input.
func ApplyTrading(
	state State,
	input InputEnvelope,
	action TradingAction,
) (State, Decision, error) {
	payload, err := EncodeTradingAction(action)
	if err != nil {
		return state, Decision{}, err
	}
	if !bytes.Equal(payload, input.Payload) {
		if !state.ready {
			return Apply(state, input)
		}
		inputHash := hashInput(input)
		if engineError := validateEnvelope(state, input); engineError != nil {
			return halt(state, inputHash, engineError)
		}
		return halt(state, inputHash, &Error{
			Kind:     ErrPayloadMismatch,
			Sequence: input.StreamSequence,
			Detail:   "typed trading action does not match canonical envelope payload",
		})
	}
	return apply(state, input, func(state State) (State, Decision) {
		return applyTradingAction(state, input, action)
	})
}

func applyTradingAction(
	state State,
	input InputEnvelope,
	action TradingAction,
) (State, Decision) {
	if !validTradingActionUnion(action) {
		return rejectedTradingDecision(state, RejectionInvalidAction)
	}

	switch action.Kind {
	case TradingActionConfigureInstrument:
		return configureTradingInstrument(state, *action.ConfigureInstrument)
	case TradingActionUpdateBook:
		return updateTradingBook(state, input, *action.UpdateBook)
	case TradingActionSubmitOrder:
		return submitTradingOrder(state, input, *action.SubmitOrder)
	case TradingActionAmendOrder:
		return amendTradingOrder(state, input, *action.AmendOrder)
	case TradingActionCancelOrder:
		return cancelTradingOrder(state, input, *action.CancelOrder)
	default:
		return rejectedTradingDecision(state, RejectionInvalidAction)
	}
}

func validTradingActionUnion(action TradingAction) bool {
	present := 0
	for _, memberPresent := range []bool{
		action.ConfigureInstrument != nil,
		action.UpdateBook != nil,
		action.SubmitOrder != nil,
		action.AmendOrder != nil,
		action.CancelOrder != nil,
	} {
		if memberPresent {
			present++
		}
	}
	if present != 1 {
		return false
	}
	switch action.Kind {
	case TradingActionConfigureInstrument:
		return action.ConfigureInstrument != nil
	case TradingActionUpdateBook:
		return action.UpdateBook != nil
	case TradingActionSubmitOrder:
		return action.SubmitOrder != nil
	case TradingActionAmendOrder:
		return action.AmendOrder != nil
	case TradingActionCancelOrder:
		return action.CancelOrder != nil
	default:
		return false
	}
}

func acceptedTradingDecision(state State) (State, Decision) {
	return state, Decision{
		CommandResult: CommandResult{Status: CommandStatusAccepted},
	}
}

func rejectedTradingDecision(state State, reason RejectionReason) (State, Decision) {
	return state, Decision{
		CommandResult: CommandResult{
			Status: CommandStatusRejected,
			Reason: reason,
		},
	}
}

func configureTradingInstrument(
	state State,
	configure ConfigureInstrument,
) (State, Decision) {
	revision, err := domain.NewInstrumentRevision(
		configure.InstrumentID,
		configure.Revision,
		configure.PriceScale,
		configure.QuantityScale,
	)
	if err != nil {
		return rejectedTradingDecision(state, RejectionInvalidInstrument)
	}

	state.trading = state.trading.clone()
	state.trading.replaceInstrument(revision)
	state, decision := acceptedTradingDecision(state)
	decision.InstrumentChanges = []InstrumentSnapshot{{
		InstrumentID:  revision.ID(),
		Revision:      revision.Revision(),
		PriceScale:    revision.PriceScale(),
		QuantityScale: revision.QuantityScale(),
	}}
	return state, decision
}

func updateTradingBook(
	state State,
	input InputEnvelope,
	update UpdateBook,
) (State, Decision) {
	instrument, ok := state.trading.instrument(update.InstrumentID)
	if !ok || instrument.Revision() != input.InstrumentVersion {
		return rejectedTradingDecision(state, RejectionInvalidInstrument)
	}
	book, err := newBookRecord(instrument, update)
	if err != nil {
		return rejectedTradingDecision(state, RejectionInvalidAction)
	}

	state.trading = state.trading.clone()
	state.trading.replaceBook(book)
	state, decision := acceptedTradingDecision(state)
	matchWorkingOrders(&state.trading, input, update.InstrumentID, &decision)
	finalBook, _ := state.trading.book(update.InstrumentID)
	decision.BookChanges = []BookSnapshot{finalBook.snapshot()}
	return state, decision
}

func newBookRecord(
	instrument domain.InstrumentRevision,
	update UpdateBook,
) (bookRecord, error) {
	var markPrice domain.Price
	hasMark := update.MarkPrice != ""
	if hasMark {
		var err error
		markPrice, err = domain.NewPrice(update.MarkPrice, instrument)
		if err != nil || markPrice.Decimal().Sign() <= 0 {
			return bookRecord{}, fmt.Errorf("invalid mark price")
		}
	}
	bids, err := newLevelRecords(instrument, update.Bids)
	if err != nil {
		return bookRecord{}, err
	}
	asks, err := newLevelRecords(instrument, update.Asks)
	if err != nil {
		return bookRecord{}, err
	}
	sort.SliceStable(bids, func(left, right int) bool {
		return bids[left].price.Decimal().Cmp(bids[right].price.Decimal()) > 0
	})
	sort.SliceStable(asks, func(left, right int) bool {
		return asks[left].price.Decimal().Cmp(asks[right].price.Decimal()) < 0
	})
	if len(bids) > 0 && len(asks) > 0 &&
		bids[0].price.Decimal().Cmp(asks[0].price.Decimal()) >= 0 {
		return bookRecord{}, fmt.Errorf("crossed book")
	}
	return bookRecord{
		instrumentID: instrument.ID(),
		markPrice:    markPrice,
		hasMark:      hasMark,
		bids:         bids,
		asks:         asks,
	}, nil
}

func newLevelRecords(
	instrument domain.InstrumentRevision,
	levels []BookLevel,
) ([]levelRecord, error) {
	records := make([]levelRecord, len(levels))
	for index, level := range levels {
		price, err := domain.NewPrice(level.Price, instrument)
		if err != nil || price.Decimal().Sign() <= 0 {
			return nil, fmt.Errorf("invalid book price")
		}
		quantity, err := domain.NewQuantity(level.Quantity, instrument)
		if err != nil || quantity.Decimal().Sign() <= 0 {
			return nil, fmt.Errorf("invalid book quantity")
		}
		records[index] = levelRecord{price: price, quantity: quantity}
	}
	return records, nil
}

func submitTradingOrder(
	state State,
	input InputEnvelope,
	submit SubmitOrder,
) (State, Decision) {
	instrument, ok := state.trading.instrument(submit.InstrumentID)
	if !ok || instrument.Revision() != input.InstrumentVersion {
		return rejectedTradingDecision(state, RejectionInvalidInstrument)
	}
	if submit.OrderID.IsZero() || submit.AccountID == "" ||
		!submit.Side.valid() || !submit.Type.valid() ||
		!submit.TimeInForce.valid() {
		return rejectedTradingDecision(state, RejectionInvalidOrder)
	}
	if _, exists := state.trading.orderIndex(submit.OrderID); exists {
		return rejectedTradingDecision(state, RejectionDuplicateOrderID)
	}
	if !marketContextAvailable(state.trading, submit.InstrumentID, submit.Side) {
		return rejectedTradingDecision(state, RejectionInsufficientMarket)
	}

	order, ok := newOrderRecord(instrument, submit)
	if !ok {
		return rejectedTradingDecision(state, RejectionInvalidOrder)
	}
	if order.hasSlippageBand {
		book, exists := state.trading.book(submit.InstrumentID)
		if !exists || !book.hasMark {
			return rejectedTradingDecision(state, RejectionInsufficientMarket)
		}
		order.slippageReference = book.markPrice
	}
	state.trading = state.trading.clone()
	state.trading.orders = append(state.trading.orders, order)
	orderIndex := len(state.trading.orders) - 1

	state, decision := acceptedTradingDecision(state)
	if orderReadyToMatch(state.trading, state.trading.orders[orderIndex]) {
		if reason := executeOrder(&state.trading, input, orderIndex, &decision); reason != "" {
			if reason == RejectionSlippageExceeded {
				rejected := &state.trading.orders[orderIndex]
				rejected.status = OrderStatusRejected
				rejected.rejectReason = reason
				rejected.version++
				decision.CommandResult = CommandResult{
					Status: CommandStatusRejected,
					Reason: reason,
				}
				snapshot := rejected.snapshot()
				decision.OrderChanges = append(decision.OrderChanges, snapshot)
				decision.Events = append(
					decision.Events,
					orderEvent(input, snapshot, nextEventSequence(decision.Events)),
				)
				return state, decision
			}
			return rejectedTradingDecision(removeLastTradingOrder(state), reason)
		}
	}
	snapshot := state.trading.orders[orderIndex].snapshot()
	decision.OrderChanges = append(decision.OrderChanges, snapshot)
	decision.Events = append(decision.Events, orderEvent(input, snapshot, nextEventSequence(decision.Events)))
	return state, decision
}

func marketContextAvailable(
	state tradingState,
	instrumentID string,
	side Side,
) bool {
	book, ok := state.book(instrumentID)
	if !ok {
		return false
	}
	if side == SideBuy {
		return len(book.asks) > 0
	}
	return len(book.bids) > 0
}

func removeLastTradingOrder(state State) State {
	state.trading.orders = state.trading.orders[:len(state.trading.orders)-1]
	return state
}

func newOrderRecord(
	instrument domain.InstrumentRevision,
	submit SubmitOrder,
) (orderRecord, bool) {
	quantity, err := domain.NewQuantity(submit.Quantity, instrument)
	if err != nil || quantity.Decimal().Sign() <= 0 {
		return orderRecord{}, false
	}
	filled, err := domain.NewQuantity("0", instrument)
	if err != nil {
		return orderRecord{}, false
	}
	order := orderRecord{
		orderID:        submit.OrderID,
		accountID:      submit.AccountID,
		instrument:     instrument,
		side:           submit.Side,
		orderType:      submit.Type,
		timeInForce:    submit.TimeInForce,
		status:         OrderStatusWorking,
		quantity:       quantity,
		filledQuantity: filled,
		reduceOnly:     submit.ReduceOnly,
		version:        1,
	}
	if submit.MaxSlippageBPS != nil {
		order.hasSlippageBand = true
		order.maxSlippageBPS = *submit.MaxSlippageBPS
	}

	switch submit.Type {
	case OrderTypeMarket:
		if submit.Price != "" || submit.TriggerPrice != "" {
			return orderRecord{}, false
		}
	case OrderTypeLimit:
		if submit.TriggerPrice != "" || !setOrderPrice(&order, submit.Price) {
			return orderRecord{}, false
		}
	case OrderTypeStopMarket:
		if submit.Price != "" || !setOrderTrigger(&order, submit.TriggerPrice) {
			return orderRecord{}, false
		}
	case OrderTypeStopLimit:
		if !setOrderPrice(&order, submit.Price) ||
			!setOrderTrigger(&order, submit.TriggerPrice) {
			return orderRecord{}, false
		}
	default:
		return orderRecord{}, false
	}
	return order, true
}

func setOrderPrice(order *orderRecord, text string) bool {
	price, err := domain.NewPrice(text, order.instrument)
	if err != nil || price.Decimal().Sign() <= 0 {
		return false
	}
	order.price = price
	order.hasPrice = true
	return true
}

func setOrderTrigger(order *orderRecord, text string) bool {
	price, err := domain.NewPrice(text, order.instrument)
	if err != nil || price.Decimal().Sign() <= 0 {
		return false
	}
	order.triggerPrice = price
	order.hasTrigger = true
	return true
}

func orderReadyToMatch(state tradingState, order orderRecord) bool {
	book, ok := state.book(order.instrument.ID())
	if !ok {
		return false
	}
	switch order.orderType {
	case OrderTypeMarket, OrderTypeLimit:
		return true
	case OrderTypeStopMarket, OrderTypeStopLimit:
		return stopTriggered(*book, order)
	default:
		return false
	}
}

func stopTriggered(book bookRecord, order orderRecord) bool {
	switch order.side {
	case SideBuy:
		return len(book.asks) > 0 &&
			book.asks[0].price.Decimal().Cmp(order.triggerPrice.Decimal()) >= 0
	case SideSell:
		return len(book.bids) > 0 &&
			book.bids[0].price.Decimal().Cmp(order.triggerPrice.Decimal()) <= 0
	default:
		return false
	}
}

func matchWorkingOrders(
	state *tradingState,
	input InputEnvelope,
	instrumentID string,
	decision *Decision,
) {
	for index := range state.orders {
		order := state.orders[index]
		if order.instrument.ID() != instrumentID ||
			(order.status != OrderStatusWorking &&
				order.status != OrderStatusPartiallyFilled) ||
			!orderReadyToMatch(*state, order) {
			continue
		}
		beforeFills := len(decision.Fills)
		reason := executeOrder(state, input, index, decision)
		if reason != "" {
			if reason == RejectionSlippageExceeded {
				order := &state.orders[index]
				order.status = OrderStatusRejected
				order.rejectReason = reason
				order.version++
				snapshot := order.snapshot()
				decision.OrderChanges = append(decision.OrderChanges, snapshot)
				decision.Events = append(
					decision.Events,
					orderEvent(input, snapshot, nextEventSequence(decision.Events)),
				)
			}
			continue
		}
		if len(decision.Fills) > beforeFills ||
			state.orders[index].status == OrderStatusCancelled {
			snapshot := state.orders[index].snapshot()
			decision.OrderChanges = append(decision.OrderChanges, snapshot)
			decision.Events = append(decision.Events, orderEvent(input, snapshot, nextEventSequence(decision.Events)))
		}
	}
}

func amendTradingOrder(
	state State,
	input InputEnvelope,
	amend AmendOrder,
) (State, Decision) {
	originalState := state
	index, ok := state.trading.orderIndex(amend.OrderID)
	if !ok {
		return rejectedTradingDecision(state, RejectionOrderNotFound)
	}
	current := state.trading.orders[index]
	if current.accountID != amend.AccountID {
		return rejectedTradingDecision(state, RejectionOrderOwnership)
	}
	if current.status != OrderStatusWorking &&
		current.status != OrderStatusPartiallyFilled {
		return rejectedTradingDecision(state, RejectionOrderTerminal)
	}
	quantity, err := domain.NewQuantity(amend.Quantity, current.instrument)
	if err != nil || quantity.Decimal().Sign() <= 0 ||
		quantity.Decimal().Cmp(current.filledQuantity.Decimal()) < 0 {
		return rejectedTradingDecision(state, RejectionInvalidOrder)
	}
	price, err := domain.NewPrice(amend.Price, current.instrument)
	if err != nil || price.Decimal().Sign() <= 0 {
		return rejectedTradingDecision(state, RejectionInvalidOrder)
	}

	state.trading = state.trading.clone()
	order := &state.trading.orders[index]
	order.quantity = quantity
	order.price = price
	order.hasPrice = true
	order.version++
	state, decision := acceptedTradingDecision(state)
	if orderReadyToMatch(state.trading, *order) {
		if reason := executeOrder(&state.trading, input, index, &decision); reason != "" {
			return rejectedTradingDecision(originalState, reason)
		}
	}
	snapshot := state.trading.orders[index].snapshot()
	decision.OrderChanges = append(decision.OrderChanges, snapshot)
	decision.Events = append(decision.Events, orderEvent(input, snapshot, nextEventSequence(decision.Events)))
	return state, decision
}

func cancelTradingOrder(
	state State,
	input InputEnvelope,
	cancel CancelOrder,
) (State, Decision) {
	index, ok := state.trading.orderIndex(cancel.OrderID)
	if !ok {
		return rejectedTradingDecision(state, RejectionOrderNotFound)
	}
	current := state.trading.orders[index]
	if current.accountID != cancel.AccountID {
		return rejectedTradingDecision(state, RejectionOrderOwnership)
	}
	if current.status != OrderStatusWorking &&
		current.status != OrderStatusPartiallyFilled {
		return rejectedTradingDecision(state, RejectionOrderTerminal)
	}

	state.trading = state.trading.clone()
	state.trading.orders[index].status = OrderStatusCancelled
	state.trading.orders[index].version++
	state, decision := acceptedTradingDecision(state)
	snapshot := state.trading.orders[index].snapshot()
	decision.OrderChanges = []OrderSnapshot{snapshot}
	decision.Events = []DomainEvent{orderEvent(input, snapshot, 1)}
	return state, decision
}

func orderEvent(
	input InputEnvelope,
	order OrderSnapshot,
	sequence uint64,
) DomainEvent {
	return DomainEvent{
		EventID:          IDFromSequence(input.InputID, sequence),
		Kind:             "order." + string(order.Status),
		AggregateID:      order.OrderID,
		AggregateVersion: order.Version,
		LogicalTime:      input.LogicalTime,
	}
}

func nextEventSequence(events []DomainEvent) uint64 {
	sequence := uint64(1)
	for range events {
		sequence++
	}
	return sequence
}
