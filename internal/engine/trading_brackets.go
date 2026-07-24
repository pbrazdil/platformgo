package engine

import "github.com/upcomers-org/platformgo/internal/domain"

func placeTradingBracket(
	state State,
	input InputEnvelope,
	bracket PlaceBracket,
) (State, Decision) {
	instrument, ok := state.trading.instrument(bracket.InstrumentID)
	if !ok || instrument.Revision() != input.InstrumentVersion ||
		!validBracketEnvelope(state.trading, instrument, bracket) {
		return rejectedTradingDecision(state, RejectionInvalidOrder)
	}

	entry := SubmitOrder{
		OrderID:      bracket.EntryOrderID,
		AccountID:    bracket.AccountID,
		InstrumentID: bracket.InstrumentID,
		Side:         bracket.Side,
		Type:         bracket.EntryType,
		TimeInForce:  bracket.TimeInForce,
		Quantity:     bracket.Quantity,
		Price:        bracket.EntryPrice,
	}
	nextState, decision := submitTradingOrder(state, input, entry)
	if decision.CommandResult.Status == CommandStatusRejected {
		return nextState, decision
	}

	nextState.trading = nextState.trading.clone()
	entryIndex, _ := nextState.trading.orderIndex(bracket.EntryOrderID)
	entryOrder := &nextState.trading.orders[entryIndex]
	entryOrder.bracketID = bracket.BracketID
	entryOrder.bracketLeg = BracketLegEntry
	replaceDecisionOrderChange(&decision, entryOrder.snapshot())

	protectiveSide := oppositeSide(bracket.Side)
	for index, leg := range bracket.TakeProfits {
		child, valid := newOrderRecord(instrument, SubmitOrder{
			OrderID:      leg.OrderID,
			AccountID:    bracket.AccountID,
			InstrumentID: bracket.InstrumentID,
			Side:         protectiveSide,
			Type:         OrderTypeTakeProfitLimit,
			TimeInForce:  TimeInForceGTC,
			Quantity:     leg.Quantity,
			Price:        leg.Price,
			TriggerPrice: leg.Price,
			ReduceOnly:   true,
		})
		if !valid {
			return rejectedTradingDecision(state, RejectionInvalidOrder)
		}
		child.status = OrderStatusHeld
		child.bracketID = bracket.BracketID
		child.bracketLeg = BracketLegTakeProfit
		child.bracketLegIndex = uint32(index + 1)
		nextState.trading.orders = append(nextState.trading.orders, child)
	}
	stop, valid := newOrderRecord(instrument, SubmitOrder{
		OrderID:      bracket.StopLossOrderID,
		AccountID:    bracket.AccountID,
		InstrumentID: bracket.InstrumentID,
		Side:         protectiveSide,
		Type:         OrderTypeStopMarket,
		TimeInForce:  TimeInForceGTC,
		Quantity:     bracket.Quantity,
		TriggerPrice: bracket.StopLoss,
		ReduceOnly:   true,
	})
	if !valid {
		return rejectedTradingDecision(state, RejectionInvalidOrder)
	}
	stop.status = OrderStatusHeld
	stop.bracketID = bracket.BracketID
	stop.bracketLeg = BracketLegStopLoss
	nextState.trading.orders = append(nextState.trading.orders, stop)

	if positionID, exists := latestOrderPositionID(
		nextState.trading,
		bracket.EntryOrderID,
	); exists {
		armBracketProtection(
			&nextState.trading,
			bracket.BracketID,
			positionID,
			input,
			&decision,
		)
	}
	appendBracketCreationChanges(
		nextState.trading,
		bracket.BracketID,
		input,
		&decision,
	)
	return nextState, decision
}

func validBracketEnvelope(
	state tradingState,
	instrument domain.InstrumentRevision,
	bracket PlaceBracket,
) bool {
	if bracket.BracketID.IsZero() || bracket.EntryOrderID.IsZero() ||
		bracket.StopLossOrderID.IsZero() || bracket.AccountID == "" ||
		!bracket.Side.valid() || !bracket.TimeInForce.valid() ||
		(bracket.EntryType != OrderTypeMarket &&
			bracket.EntryType != OrderTypeLimit) ||
		len(bracket.TakeProfits) == 0 {
		return false
	}
	if bracket.EntryType == OrderTypeMarket && bracket.EntryPrice != "" {
		return false
	}
	if bracket.EntryType == OrderTypeLimit && bracket.EntryPrice == "" {
		return false
	}
	entryQuantity, err := domain.NewQuantity(bracket.Quantity, instrument)
	if err != nil || entryQuantity.Decimal().Sign() <= 0 {
		return false
	}
	if bracket.EntryOrderID == bracket.StopLossOrderID {
		return false
	}
	seen := []ID{bracket.EntryOrderID, bracket.StopLossOrderID}
	total, err := domain.NewQuantity("0", instrument)
	if err != nil {
		return false
	}
	for _, leg := range bracket.TakeProfits {
		if leg.OrderID.IsZero() || containsID(seen, leg.OrderID) {
			return false
		}
		seen = append(seen, leg.OrderID)
		quantity, quantityErr := domain.NewQuantity(leg.Quantity, instrument)
		if quantityErr != nil || quantity.Decimal().Sign() <= 0 {
			return false
		}
		total, err = total.Add(quantity)
		if err != nil {
			return false
		}
	}
	if total.Decimal().Cmp(entryQuantity.Decimal()) > 0 {
		return false
	}
	for _, id := range seen {
		if _, exists := state.orderIndex(id); exists {
			return false
		}
	}
	for _, order := range state.orders {
		if order.bracketID == bracket.BracketID {
			return false
		}
	}
	return true
}

func containsID(ids []ID, candidate ID) bool {
	for _, id := range ids {
		if id == candidate {
			return true
		}
	}
	return false
}

func cancelBracketProtection(
	state *tradingState,
	bracketID ID,
	input InputEnvelope,
	decision *Decision,
) {
	for index := range state.orders {
		order := &state.orders[index]
		if order.bracketID != bracketID ||
			order.bracketLeg == BracketLegEntry ||
			(order.status != OrderStatusHeld &&
				order.status != OrderStatusWorking &&
				order.status != OrderStatusPartiallyFilled) {
			continue
		}
		order.status = OrderStatusCancelled
		order.version++
		appendOrderTransition(decision, input, order.snapshot())
	}
}

func oppositeSide(side Side) Side {
	if side == SideBuy {
		return SideSell
	}
	return SideBuy
}

func replaceDecisionOrderChange(decision *Decision, snapshot OrderSnapshot) {
	for index := range decision.OrderChanges {
		if decision.OrderChanges[index].OrderID == snapshot.OrderID {
			decision.OrderChanges[index] = snapshot
			return
		}
	}
}

func latestOrderPositionID(
	state tradingState,
	orderID ID,
) (ID, bool) {
	for index := len(state.fills) - 1; index >= 0; index-- {
		if state.fills[index].orderID == orderID &&
			!state.fills[index].positionID.IsZero() {
			return state.fills[index].positionID, true
		}
	}
	return ID{}, false
}

func armBracketProtection(
	state *tradingState,
	bracketID ID,
	positionID ID,
	input InputEnvelope,
	decision *Decision,
) {
	for index := range state.orders {
		order := &state.orders[index]
		if order.bracketID != bracketID ||
			order.bracketLeg == BracketLegEntry ||
			(order.status != OrderStatusHeld &&
				order.status != OrderStatusWorking &&
				order.status != OrderStatusPartiallyFilled) {
			continue
		}
		beforeStatus := order.status
		beforeQuantity := order.quantity.Decimal().String()
		order.positionID = positionID
		if !resizeProtectionToPosition(*state, order) {
			continue
		}
		if order.status == OrderStatusHeld {
			order.status = OrderStatusWorking
		}
		if order.status != beforeStatus ||
			order.quantity.Decimal().String() != beforeQuantity {
			order.version++
			appendOrderTransition(decision, input, order.snapshot())
		}
	}
}

func resizeProtectionToPosition(
	state tradingState,
	order *orderRecord,
) bool {
	positionIndex, ok := reduceOnlyPositionIndex(state, *order)
	if !ok {
		return false
	}
	position := state.positions[positionIndex]
	unfilledCap, err := order.originalQuantity.Sub(order.filledQuantity)
	if err != nil {
		return false
	}
	desiredRemaining := unfilledCap
	if desiredRemaining.Decimal().Cmp(position.quantity.Decimal()) > 0 {
		desiredRemaining = position.quantity
	}
	order.quantity, err = order.filledQuantity.Add(desiredRemaining)
	return err == nil
}

func appendBracketCreationChanges(
	state tradingState,
	bracketID ID,
	input InputEnvelope,
	decision *Decision,
) {
	for _, order := range state.orders {
		if order.bracketID != bracketID ||
			order.bracketLeg == BracketLegEntry ||
			decisionHasOrderChange(*decision, order.orderID) {
			continue
		}
		appendOrderTransition(decision, input, order.snapshot())
	}
}

func decisionHasOrderChange(decision Decision, orderID ID) bool {
	for _, change := range decision.OrderChanges {
		if change.OrderID == orderID {
			return true
		}
	}
	return false
}

func reconcileBracketAfterExecution(
	state *tradingState,
	orderIndex int,
	input InputEnvelope,
	decision *Decision,
) {
	order := state.orders[orderIndex]
	if order.bracketID.IsZero() {
		return
	}
	if order.bracketLeg == BracketLegEntry {
		if positionID, ok := latestOrderPositionID(*state, order.orderID); ok {
			armBracketProtection(
				state,
				order.bracketID,
				positionID,
				input,
				decision,
			)
		}
		return
	}
	if order.bracketLeg != BracketLegTakeProfit &&
		order.bracketLeg != BracketLegStopLoss {
		return
	}

	_, positionOpen := reduceOnlyPositionIndex(*state, order)
	for index := range state.orders {
		sibling := &state.orders[index]
		if sibling.orderID == order.orderID ||
			sibling.bracketID != order.bracketID ||
			sibling.bracketLeg == BracketLegEntry ||
			(sibling.status != OrderStatusHeld &&
				sibling.status != OrderStatusWorking &&
				sibling.status != OrderStatusPartiallyFilled) {
			continue
		}
		if !positionOpen {
			sibling.status = OrderStatusCancelled
			sibling.version++
			appendOrderTransition(decision, input, sibling.snapshot())
			continue
		}
		beforeQuantity := sibling.quantity.Decimal().String()
		if reason := clampReduceOnlyOrder(*state, sibling); reason != "" {
			sibling.status = OrderStatusCancelled
			sibling.version++
			appendOrderTransition(decision, input, sibling.snapshot())
			continue
		}
		if sibling.quantity.Decimal().String() != beforeQuantity {
			sibling.version++
			appendOrderTransition(decision, input, sibling.snapshot())
		}
	}
}

func reconcilePositionProtection(
	state *tradingState,
	input InputEnvelope,
	decision *Decision,
) {
	for index := range state.orders {
		order := &state.orders[index]
		if order.bracketLeg == BracketLegEntry ||
			order.bracketID.IsZero() ||
			order.positionID.IsZero() ||
			(order.status != OrderStatusWorking &&
				order.status != OrderStatusPartiallyFilled) {
			continue
		}
		positionIndex, positionOpen := reduceOnlyPositionIndex(*state, *order)
		if !positionOpen ||
			!protectiveSideMatchesPosition(
				order.side,
				state.positions[positionIndex].side,
			) {
			order.status = OrderStatusCancelled
			order.version++
			appendOrderTransition(decision, input, order.snapshot())
			continue
		}
		beforeQuantity := order.quantity.Decimal().String()
		if reason := clampReduceOnlyOrder(*state, order); reason != "" {
			order.status = OrderStatusCancelled
			order.version++
			appendOrderTransition(decision, input, order.snapshot())
			continue
		}
		if order.quantity.Decimal().String() != beforeQuantity {
			order.version++
			appendOrderTransition(decision, input, order.snapshot())
		}
	}
}

func protectiveSideMatchesPosition(
	orderSide Side,
	positionSide PositionSide,
) bool {
	return (positionSide == PositionSideLong && orderSide == SideSell) ||
		(positionSide == PositionSideShort && orderSide == SideBuy)
}

func appendOrderTransition(
	decision *Decision,
	input InputEnvelope,
	snapshot OrderSnapshot,
) {
	decision.OrderChanges = append(decision.OrderChanges, snapshot)
	decision.Events = append(
		decision.Events,
		orderEvent(input, snapshot, nextEventSequence(decision.Events)),
	)
}
