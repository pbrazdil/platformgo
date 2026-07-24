package engine

import "github.com/upcomers-org/platformgo/internal/domain"

func executeOrder(
	state *tradingState,
	input InputEnvelope,
	orderIndex int,
	decision *Decision,
) RejectionReason {
	candidate := state.clone()
	order := &candidate.orders[orderIndex]
	book, ok := candidate.book(order.instrument.ID())
	if !ok {
		if order.orderType == OrderTypeMarket {
			return RejectionInsufficientMarket
		}
		return ""
	}

	requiresFullFill := (marketUsesDeepestFallback(*order) &&
		order.timeInForce != TimeInForceIOC) ||
		order.timeInForce == TimeInForceFOK
	if requiresFullFill {
		fillability := bookFillability(*book, *order)
		if fillability != "" {
			if order.timeInForce == TimeInForceFOK {
				order.status = OrderStatusCancelled
				order.version++
				*state = candidate
				return ""
			}
			return fillability
		}
	}

	startFillCount := len(candidate.fills)
	pricingBook := bookRecord{
		instrumentID: book.instrumentID,
		bids:         append([]levelRecord(nil), book.bids...),
		asks:         append([]levelRecord(nil), book.asks...),
	}
	if !matchOrder(&pricingBook, order, input, &candidate.fills) {
		return RejectionInvalidOrder
	}
	newFills := candidate.fills[startFillCount:]
	if len(newFills) > 0 {
		average, err := averageOrderFillPrice(candidate.fills, order.orderID)
		if err != nil {
			return RejectionInvalidOrder
		}
		order.averagePrice = average
		order.hasAverage = true
	}

	switch {
	case order.filledQuantity.Decimal().Equal(order.quantity.Decimal()):
		order.status = OrderStatusFilled
	case order.timeInForce == TimeInForceIOC:
		order.status = OrderStatusCancelled
	case order.filledQuantity.Decimal().Sign() > 0:
		order.status = OrderStatusPartiallyFilled
	default:
		order.status = OrderStatusWorking
	}
	order.version++
	*state = candidate
	for _, fill := range newFills {
		decision.Fills = append(decision.Fills, fill.snapshot())
	}
	return ""
}

func bookFillability(book bookRecord, order orderRecord) RejectionReason {
	remaining := order.quantity.Decimal()
	levels := executableLevels(book, order.side)
	for index, level := range levels {
		if !levelEligible(level, order) {
			if order.hasSlippageBand &&
				priceViolatesSlippage(level.price, order) {
				return RejectionSlippageExceeded
			}
			return RejectionInsufficientMarket
		}
		var err error
		remaining, err = remaining.Sub(level.quantity.Decimal())
		if err != nil {
			return RejectionInvalidOrder
		}
		if remaining.Sign() <= 0 {
			return ""
		}
		// The B-book source contract models liquidity past visible depth at the
		// deepest admissible level. Limit and FOK orders never use this fallback.
		if index == len(levels)-1 && marketUsesDeepestFallback(order) {
			return ""
		}
	}
	return RejectionInsufficientMarket
}

func executableLevels(book bookRecord, side Side) []levelRecord {
	if side == SideBuy {
		return book.asks
	}
	return book.bids
}

func levelEligible(level levelRecord, order orderRecord) bool {
	if order.orderType != OrderTypeMarket && order.orderType != OrderTypeStopMarket {
		if order.side == SideBuy &&
			level.price.Decimal().Cmp(order.price.Decimal()) > 0 {
			return false
		}
		if order.side == SideSell &&
			level.price.Decimal().Cmp(order.price.Decimal()) < 0 {
			return false
		}
	}
	return !priceViolatesSlippage(level.price, order)
}

func priceViolatesSlippage(price domain.Price, order orderRecord) bool {
	if !order.hasSlippageBand {
		return false
	}
	withinBand, err := domain.PriceWithinAdverseBasisPoints(
		order.slippageReference,
		price,
		order.side == SideBuy,
		order.maxSlippageBPS,
	)
	return err != nil || !withinBand
}

func marketUsesDeepestFallback(order orderRecord) bool {
	return order.orderType == OrderTypeMarket ||
		order.orderType == OrderTypeStopMarket
}

func matchOrder(
	book *bookRecord,
	order *orderRecord,
	input InputEnvelope,
	fills *[]fillRecord,
) bool {
	levels := &book.bids
	if order.side == SideBuy {
		levels = &book.asks
	}
	var deepestEligible *domain.Price
	for levelIndex := 0; levelIndex < len(*levels); levelIndex++ {
		level := &(*levels)[levelIndex]
		if !levelEligible(*level, *order) {
			break
		}
		deepest := level.price
		deepestEligible = &deepest
		remaining, err := order.quantity.Sub(order.filledQuantity)
		if err != nil {
			return false
		}
		if remaining.Decimal().IsZero() {
			break
		}
		fillQuantity := remaining
		if level.quantity.Decimal().Cmp(remaining.Decimal()) < 0 {
			fillQuantity = level.quantity
		}
		order.filledQuantity, err = order.filledQuantity.Add(fillQuantity)
		if err != nil {
			return false
		}
		level.quantity, err = level.quantity.Sub(fillQuantity)
		if err != nil {
			return false
		}
		*fills = append(*fills, fillRecord{
			fillID:      IDFromSequence(order.orderID, nextOrderFillSequence(*fills, order.orderID)),
			orderID:     order.orderID,
			accountID:   order.accountID,
			instrument:  order.instrument,
			side:        order.side,
			price:       level.price,
			quantity:    fillQuantity,
			logicalTime: input.LogicalTime,
		})
	}
	remaining, err := order.quantity.Sub(order.filledQuantity)
	if err != nil {
		return false
	}
	if remaining.Decimal().Sign() > 0 &&
		deepestEligible != nil &&
		marketUsesDeepestFallback(*order) &&
		allLevelsEligible(*levels, *order) {
		order.filledQuantity, err = order.filledQuantity.Add(remaining)
		if err != nil {
			return false
		}
		*fills = append(*fills, fillRecord{
			fillID:      IDFromSequence(order.orderID, nextOrderFillSequence(*fills, order.orderID)),
			orderID:     order.orderID,
			accountID:   order.accountID,
			instrument:  order.instrument,
			side:        order.side,
			price:       *deepestEligible,
			quantity:    remaining,
			logicalTime: input.LogicalTime,
		})
	}
	compacted := (*levels)[:0]
	for _, level := range *levels {
		if !level.quantity.Decimal().IsZero() {
			compacted = append(compacted, level)
		}
	}
	*levels = compacted
	return true
}

func allLevelsEligible(levels []levelRecord, order orderRecord) bool {
	for _, level := range levels {
		if !levelEligible(level, order) {
			return false
		}
	}
	return true
}

func nextOrderFillSequence(fills []fillRecord, orderID ID) uint64 {
	sequence := uint64(1)
	for _, fill := range fills {
		if fill.orderID == orderID {
			sequence++
		}
	}
	return sequence
}

func averageOrderFillPrice(fills []fillRecord, orderID ID) (domain.Price, error) {
	var values []domain.PriceQuantity
	for _, fill := range fills {
		if fill.orderID == orderID {
			values = append(values, domain.PriceQuantity{
				Price:    fill.price,
				Quantity: fill.quantity,
			})
		}
	}
	return domain.WeightedAveragePrice(values)
}
