package engine

import (
	"fmt"

	"github.com/upcomers-org/platformgo/internal/domain"
)

func clampReduceOnlyOrder(
	state tradingState,
	order *orderRecord,
) RejectionReason {
	if !order.reduceOnly {
		if !order.positionID.IsZero() {
			return RejectionInvalidOrder
		}
		return ""
	}

	positionIndex, ok := reduceOnlyPositionIndex(state, *order)
	if !ok {
		return RejectionReduceOnly
	}
	position := state.positions[positionIndex]
	if (position.side == PositionSideLong && order.side != SideSell) ||
		(position.side == PositionSideShort && order.side != SideBuy) {
		return RejectionReduceOnly
	}
	remaining, err := order.quantity.Sub(order.filledQuantity)
	if err != nil {
		return RejectionInvalidOrder
	}
	if remaining.Decimal().Cmp(position.quantity.Decimal()) > 0 {
		order.quantity, err = order.filledQuantity.Add(position.quantity)
		if err != nil {
			return RejectionInvalidOrder
		}
	}
	order.positionID = position.positionID
	return ""
}

func reduceOnlyPositionIndex(
	state tradingState,
	order orderRecord,
) (int, bool) {
	if !order.positionID.IsZero() {
		for index, position := range state.positions {
			if position.positionID == order.positionID &&
				position.accountID == order.accountID &&
				position.instrument.Equal(order.instrument) &&
				position.status == PositionStatusOpen {
				return index, true
			}
		}
		return 0, false
	}
	if state.accountMode(order.accountID) == OmsModeHedging {
		return 0, false
	}
	return netPositionIndex(state, order.accountID, order.instrument.ID())
}

func netPositionIndex(
	state tradingState,
	accountID string,
	instrumentID string,
) (int, bool) {
	for index, position := range state.positions {
		if position.accountID == accountID &&
			position.instrument.ID() == instrumentID &&
			position.status == PositionStatusOpen {
			return index, true
		}
	}
	return 0, false
}

func applyFillsToPositions(
	state *tradingState,
	order orderRecord,
	firstFillIndex int,
	input InputEnvelope,
	decision *Decision,
) error {
	for fillIndex := firstFillIndex; fillIndex < len(state.fills); fillIndex++ {
		positionIndex, created, err := positionForFill(state, order, state.fills[fillIndex])
		if err != nil {
			return err
		}
		position := &state.positions[positionIndex]
		fill := &state.fills[fillIndex]
		if created {
			fill.positionEffect = PositionEffectOpen
		} else if err := applyFillToPosition(position, fill); err != nil {
			return err
		}
		fill.positionID = position.positionID
		if err := refreshPositionMargin(state, position); err != nil {
			return err
		}
		position.version++
		balanceChanged := false
		if fill.hasRealizedPnL {
			if err := state.applyBalanceDelta(position.accountID, fill.realizedPnL); err != nil {
				return err
			}
			balanceChanged = fill.realizedPnL.Decimal().Sign() != 0
		}
		if fill.hasFee && fill.fee.Decimal().Sign() != 0 {
			if err := state.applyBalanceDebit(position.accountID, fill.fee); err != nil {
				return err
			}
			balanceChanged = true
		}
		if balanceChanged {
			balance, ok := state.balanceSnapshot(
				position.accountID,
				position.settlementCurrency,
			)
			if !ok {
				return fmt.Errorf("project position settlement balance")
			}
			decision.BalanceChanges = append(decision.BalanceChanges, balance)
		}
		snapshot := position.snapshot()
		decision.PositionChanges = append(decision.PositionChanges, snapshot)
		decision.Events = append(
			decision.Events,
			positionEvent(input, snapshot, fill.positionEffect, nextEventSequence(decision.Events)),
		)
	}
	return nil
}

func positionForFill(
	state *tradingState,
	order orderRecord,
	fill fillRecord,
) (int, bool, error) {
	if order.reduceOnly {
		for index, position := range state.positions {
			if position.positionID == order.positionID &&
				position.status == PositionStatusOpen {
				return index, false, nil
			}
		}
		return 0, false, fmt.Errorf("reduce-only target disappeared")
	}

	mode := state.accountMode(order.accountID)
	if mode == OmsModeNetting {
		if index, ok := netPositionIndex(*state, order.accountID, order.instrument.ID()); ok {
			return index, false, nil
		}
	}
	if mode == OmsModeHedging {
		positionID := IDFromSequence(order.orderID, 0)
		for index, position := range state.positions {
			if position.positionID == positionID {
				return index, false, nil
			}
		}
	}

	instrument, ok := state.instrumentRecord(order.instrument.ID())
	if !ok {
		return 0, false, fmt.Errorf("position instrument disappeared")
	}
	zeroRealized, err := domain.NewMoney("0", instrument.settlementCurrency)
	if err != nil {
		return 0, false, err
	}
	zeroCollateral, err := domain.NewMoney("0", instrument.settlementCurrency)
	if err != nil {
		return 0, false, err
	}
	risk := state.risk(order.accountID, instrument)
	marginMode := risk.marginMode
	if state.accountMode(order.accountID) == OmsModeHedging &&
		marginMode == MarginModeIsolated {
		marginMode = MarginModeCross
	}
	positionID := IDFromSequence(order.orderID, 0)
	state.positions = append(state.positions, positionRecord{
		positionID:         positionID,
		accountID:          order.accountID,
		instrument:         order.instrument,
		settlementCurrency: instrument.settlementCurrency,
		side:               positionSide(fill.side),
		status:             PositionStatusOpen,
		quantity:           fill.quantity,
		averageOpenPrice:   fill.price,
		realizedPnL:        zeroRealized,
		marginMode:         marginMode,
		isolatedCollateral: zeroCollateral,
	})
	return len(state.positions) - 1, true, nil
}

func refreshPositionMargin(
	state *tradingState,
	position *positionRecord,
) error {
	zero, err := domain.NewMoney("0", position.settlementCurrency)
	if err != nil {
		return err
	}
	if position.status == PositionStatusClosed ||
		position.marginMode != MarginModeIsolated {
		position.isolatedCollateral = zero
		return nil
	}
	instrument, ok := state.instrumentRecord(position.instrument.ID())
	if !ok {
		return fmt.Errorf("position risk instrument disappeared")
	}
	risk := state.risk(position.accountID, instrument)
	position.isolatedCollateral, err = domain.PositionMargin(
		position.averageOpenPrice,
		position.quantity,
		instrument.initialMarginRate,
		risk.leverage,
		position.settlementCurrency,
	)
	return err
}

func applyFillToPosition(position *positionRecord, fill *fillRecord) error {
	fillSide := positionSide(fill.side)
	if fillSide == position.side {
		average, err := domain.WeightedAveragePrice([]domain.PriceQuantity{
			{Price: position.averageOpenPrice, Quantity: position.quantity},
			{Price: fill.price, Quantity: fill.quantity},
		})
		if err != nil {
			return err
		}
		quantity, err := position.quantity.Add(fill.quantity)
		if err != nil {
			return err
		}
		position.quantity = quantity
		position.averageOpenPrice = average
		position.status = PositionStatusOpen
		fill.positionEffect = PositionEffectIncrease
		return nil
	}

	closingQuantity := fill.quantity
	comparison := fill.quantity.Decimal().Cmp(position.quantity.Decimal())
	if comparison > 0 {
		closingQuantity = position.quantity
	}
	realized, err := domain.RealizedPnL(
		position.averageOpenPrice,
		fill.price,
		closingQuantity,
		position.side == PositionSideLong,
		position.settlementCurrency,
	)
	if err != nil {
		return err
	}
	position.realizedPnL, err = position.realizedPnL.Add(realized)
	if err != nil {
		return err
	}
	fill.realizedPnL = realized
	fill.hasRealizedPnL = true

	switch comparison {
	case -1:
		position.quantity, err = position.quantity.Sub(fill.quantity)
		if err != nil {
			return err
		}
		fill.positionEffect = PositionEffectReduce
	case 0:
		position.quantity, err = position.quantity.Sub(fill.quantity)
		if err != nil {
			return err
		}
		position.status = PositionStatusClosed
		fill.positionEffect = PositionEffectClose
	case 1:
		position.quantity, err = fill.quantity.Sub(position.quantity)
		if err != nil {
			return err
		}
		position.side = fillSide
		position.averageOpenPrice = fill.price
		position.status = PositionStatusOpen
		fill.positionEffect = PositionEffectFlip
	}
	return nil
}

func positionSide(side Side) PositionSide {
	if side == SideBuy {
		return PositionSideLong
	}
	return PositionSideShort
}

func positionEvent(
	input InputEnvelope,
	position PositionSnapshot,
	effect PositionEffect,
	sequence uint64,
) DomainEvent {
	return DomainEvent{
		EventID:          IDFromSequence(input.InputID, sequence),
		Kind:             "position." + string(effect),
		AggregateID:      position.PositionID,
		AggregateVersion: position.Version,
		LogicalTime:      input.LogicalTime,
	}
}
