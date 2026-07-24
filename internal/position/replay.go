package position

import (
	"errors"

	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/money"
)

func (position *Position) PurgeEventsForOrder(clientOrderID string) {
	filteredReplay := position.replay[:0]
	for _, event := range position.replay {
		if event.Fill != nil && event.Fill.ClientOrderID == clientOrderID {
			continue
		}
		filteredReplay = append(filteredReplay, event)
	}
	position.replay = filteredReplay
	filteredVoids := position.FillVoids[:0]
	for _, fillVoid := range position.FillVoids {
		if fillVoid.ClientOrderID != clientOrderID {
			filteredVoids = append(filteredVoids, fillVoid)
		}
	}
	position.FillVoids = filteredVoids

	hasFill := false
	for _, event := range position.replay {
		if event.Fill != nil {
			hasFill = true
			break
		}
	}
	if !hasFill {
		position.emptyShell()
		return
	}
	position.rebuild()
}

func (position *Position) ApplyFillVoid(fillVoid FillVoid) error {
	total := decimal.Decimal{}
	for _, event := range position.replay {
		if event.Fill != nil &&
			event.Fill.ClientOrderID == fillVoid.ClientOrderID &&
			event.Fill.TradeID == fillVoid.TradeID {
			total = total.Add(event.Fill.Quantity)
		}
	}
	if fillVoid.VoidedQuantity.IsZero() || fillVoid.VoidedQuantity.Cmp(total) > 0 {
		return errors.New("position fill void exceeds known fragments")
	}
	for index := len(position.FillVoids) - 1; index >= 0; index-- {
		previous := position.FillVoids[index]
		if previous.ClientOrderID != fillVoid.ClientOrderID || previous.TradeID != fillVoid.TradeID {
			continue
		}
		if fillVoid.VoidedQuantity.Cmp(previous.VoidedQuantity) < 0 {
			return errors.New("stale position fill void")
		}
		sameCommission := previous.CommissionVoid == nil && fillVoid.CommissionVoid == nil ||
			previous.CommissionVoid != nil && fillVoid.CommissionVoid != nil &&
				previous.CommissionVoid.Equal(*fillVoid.CommissionVoid)
		if fillVoid.VoidedQuantity.Equal(previous.VoidedQuantity) && sameCommission {
			return errors.New("duplicate position fill void")
		}
		break
	}
	position.FillVoids = append(position.FillVoids, fillVoid)
	position.rebuild()
	return nil
}

func (position *Position) rebuild() {
	replay := append([]replayEvent(nil), position.replay...)
	quantityRemoved := make(map[int]decimal.Decimal)
	commissionRemoved := make(map[int]money.Money)

	latest := make(map[string]FillVoid)
	var order []string
	for _, fillVoid := range position.FillVoids {
		key := fillVoid.ClientOrderID + "\x00" + fillVoid.TradeID
		if _, exists := latest[key]; !exists {
			order = append(order, key)
		}
		latest[key] = fillVoid
	}
	for _, key := range order {
		fillVoid := latest[key]
		remainingQuantity := fillVoid.VoidedQuantity
		var remainingCommission *money.Money
		if fillVoid.CommissionVoid != nil {
			copy := *fillVoid.CommissionVoid
			remainingCommission = &copy
		}
		for index := len(replay) - 1; index >= 0; index-- {
			fill := replay[index].Fill
			if fill == nil ||
				fill.ClientOrderID != fillVoid.ClientOrderID ||
				fill.TradeID != fillVoid.TradeID {
				continue
			}
			if !remainingQuantity.IsZero() {
				removed := minDecimal(remainingQuantity, fill.Quantity)
				quantityRemoved[index] = removed
				remainingQuantity = remainingQuantity.Sub(removed)
			}
			if remainingCommission != nil && fill.Commission != nil {
				removedAmount := minDecimal(
					absDecimal(remainingCommission.Decimal()),
					absDecimal(fill.Commission.Decimal()),
				)
				if remainingCommission.Decimal().Sign() < 0 {
					removedAmount = removedAmount.Neg()
				}
				removed, err := money.FromDecimal(removedAmount, remainingCommission.Currency())
				if err != nil {
					panic(err)
				}
				commissionRemoved[index] = removed
				next := remainingCommission.Sub(removed)
				if next.IsZero() {
					remainingCommission = nil
				} else {
					remainingCommission = &next
				}
			}
		}
	}

	position.resetDerived()
	for index, event := range replay {
		if event.Fill != nil {
			fill := *event.Fill
			if removed, exists := quantityRemoved[index]; exists {
				fill.Quantity = fill.Quantity.Sub(removed)
			}
			if removed, exists := commissionRemoved[index]; exists && fill.Commission != nil {
				commission := fill.Commission.Sub(removed)
				fill.Commission = &commission
			}
			if fill.Quantity.IsZero() {
				if fill.Commission != nil && !fill.Commission.IsZero() {
					position.applySurvivingCommission(fill, *fill.Commission)
				}
				continue
			}
			if err := position.apply(fill, false); err != nil {
				panic(err)
			}
		} else if event.Adjustment != nil {
			position.applyAdjustment(*event.Adjustment, false)
		}
	}
}

func (position *Position) applySurvivingCommission(fill Fill, commission money.Money) {
	position.addCommission(&commission)
	if commission.Currency().Equal(position.Instrument.SettlementCurrency) {
		position.addRealized(commission.Decimal().Neg())
	}
	if position.Instrument.CurrencyPair &&
		position.Instrument.BaseCurrency != nil &&
		commission.Currency().Equal(*position.Instrument.BaseCurrency) {
		change := commission.Decimal().Neg()
		reason := fill.ClientOrderID
		position.applyAdjustment(Adjustment{
			Type: Commission, QuantityChange: &change, Reason: &reason,
			TsEvent: fill.TsEvent, TsInit: fill.TsInit,
		}, false)
	} else {
		position.TsLast = fill.TsEvent
	}
}

func (position *Position) resetDerived() {
	position.Events = nil
	position.Adjustments = nil
	position.tradeIDs = make(map[string]struct{})
	position.BuyQuantity = decimal.Decimal{}
	position.SellQuantity = decimal.Decimal{}
	position.commissions = make(map[string]money.Money)
	position.commissionOrder = nil
	position.SignedQuantity = decimal.Decimal{}
	position.Quantity = decimal.Decimal{}.Quantize(position.Instrument.SizePrecision, decimal.RoundHalfEven)
	position.PeakQuantity = position.Quantity
	position.Side = Flat
	position.ClosingOrderID = nil
	position.TsOpened = 0
	position.TsLast = 0
	closed := uint64(0)
	position.TsClosed = &closed
	position.Duration = 0
	position.AverageOpen = decimal.Decimal{}
	position.AverageClose = nil
	position.RealizedPnL = nil
	position.RealizedReturn = decimal.Decimal{}
}

func (position *Position) emptyShell() {
	position.resetDerived()
	position.replay = nil
	position.FillVoids = nil
}
