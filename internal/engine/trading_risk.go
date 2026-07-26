package engine

import (
	"fmt"
	"sort"

	"github.com/upcomers-org/platformgo/internal/domain"
)

func configureTradingRisk(
	state State,
	configure ConfigureRisk,
) (State, Decision) {
	instrument, ok := state.trading.instrumentRecord(configure.InstrumentID)
	if !ok || configure.AccountID == "" || !configure.MarginMode.valid() {
		return rejectedTradingDecision(state, RejectionInvalidAction)
	}
	leverage, err := domain.NewRatio(configure.Leverage)
	if err != nil || leverage.Decimal().Sign() <= 0 ||
		leverage.Decimal().Cmp(instrument.maxLeverage.Decimal()) > 0 {
		return rejectedTradingDecision(state, RejectionInvalidAction)
	}
	if state.trading.hasOpenPositionForInstrument(
		configure.AccountID,
		configure.InstrumentID,
	) || state.trading.hasActiveOrderForInstrument(
		configure.AccountID,
		configure.InstrumentID,
	) {
		return rejectedTradingDecision(state, RejectionRiskConfigLocked)
	}

	state.trading = state.trading.clone()
	state.trading.replaceRisk(riskRecord{
		accountID:    configure.AccountID,
		instrumentID: configure.InstrumentID,
		marginMode:   configure.MarginMode,
		leverage:     leverage,
	})
	state, decision := acceptedTradingDecision(state)
	decision.RiskChanges = []RiskSnapshot{{
		AccountID:    configure.AccountID,
		InstrumentID: configure.InstrumentID,
		MarginMode:   configure.MarginMode,
		Leverage:     leverage.Decimal().String(),
	}}
	return state, decision
}

func adjustTradingBalance(
	state State,
	adjust AdjustBalance,
) (State, Decision) {
	originalState := state
	if adjust.AccountID == "" || !adjust.Operation.valid() {
		return rejectedTradingDecision(state, RejectionInvalidAction)
	}
	currency, err := domain.NewCurrency(adjust.Currency, adjust.CurrencyScale)
	if err != nil || !state.trading.hasSettlementCurrency(currency) {
		return rejectedTradingDecision(state, RejectionInvalidAction)
	}
	amount, err := domain.NewMoney(adjust.Amount, currency)
	if err != nil || amount.Decimal().Sign() < 0 ||
		(adjust.Operation == BalanceOperationDeposit &&
			amount.Decimal().Sign() == 0) {
		return rejectedTradingDecision(state, RejectionInvalidAction)
	}

	state.trading = state.trading.clone()
	index, exists := state.trading.balanceIndex(adjust.AccountID, currency)
	if !exists {
		zero, zeroErr := domain.NewMoney("0", currency)
		if zeroErr != nil {
			return rejectedTradingDecision(originalState, RejectionInvalidAction)
		}
		state.trading.balances = append(state.trading.balances, balanceRecord{
			accountID: adjust.AccountID,
			total:     zero,
		})
		index = len(state.trading.balances) - 1
	}
	switch adjust.Operation {
	case BalanceOperationDeposit:
		state.trading.balances[index].total, err =
			state.trading.balances[index].total.Add(amount)
	case BalanceOperationSet:
		state.trading.balances[index].total = amount
	}
	if err != nil {
		return rejectedTradingDecision(originalState, RejectionInvalidAction)
	}
	state.trading.sortBalances()
	state, decision := acceptedTradingDecision(state)
	snapshot, ok := state.trading.balanceSnapshot(adjust.AccountID, currency)
	if !ok {
		return rejectedTradingDecision(originalState, RejectionMarketDataStale)
	}
	decision.BalanceChanges = []BalanceSnapshot{snapshot}
	return state, decision
}

func settleTradingFunding(
	state State,
	input InputEnvelope,
	settle SettleFunding,
) (State, Decision) {
	originalState := state
	instrument, ok := state.trading.instrumentRecord(settle.InstrumentID)
	if !ok || settle.SettlementID.IsZero() {
		return rejectedTradingDecision(state, RejectionInvalidAction)
	}
	oracle, err := domain.NewPrice(settle.OraclePrice, instrument.revision)
	if err != nil || oracle.Decimal().Sign() <= 0 {
		return rejectedTradingDecision(state, RejectionInvalidAction)
	}
	rate, err := domain.NewRate(settle.Rate)
	if err != nil {
		return rejectedTradingDecision(state, RejectionInvalidAction)
	}
	if existing, exists := state.trading.settlement(settle.SettlementID); exists {
		if !existing.instrument.Equal(instrument.revision) ||
			!existing.oraclePrice.Decimal().Equal(oracle.Decimal()) ||
			!existing.rate.Decimal().Equal(rate.Decimal()) {
			return rejectedTradingDecision(state, RejectionDuplicateSettlement)
		}
		return acceptedTradingDecision(state)
	}

	state.trading = state.trading.clone()
	state.trading.settlements = append(
		state.trading.settlements,
		fundingSettlementRecord{
			settlementID: settle.SettlementID,
			instrument:   instrument.revision,
			oraclePrice:  oracle,
			rate:         rate,
		},
	)
	state, decision := acceptedTradingDecision(state)
	sequence := uint64(1)
	for _, position := range state.trading.positions {
		if position.instrument.ID() != settle.InstrumentID ||
			position.status != PositionStatusOpen {
			continue
		}
		amount, paymentErr := domain.FundingPayment(
			oracle,
			position.quantity,
			rate,
			position.side == PositionSideLong,
			instrument.settlementCurrency,
		)
		if paymentErr != nil {
			return rejectedTradingDecision(originalState, RejectionInvalidAction)
		}
		if applyErr := state.trading.applyBalanceDelta(position.accountID, amount); applyErr != nil {
			return rejectedTradingDecision(originalState, RejectionInvalidAction)
		}
		funding := fundingRecord{
			fundingID:      IDFromSequence(settle.SettlementID, sequence),
			settlementID:   settle.SettlementID,
			positionID:     position.positionID,
			accountID:      position.accountID,
			instrument:     position.instrument,
			signedQuantity: position.snapshot().SignedQuantity,
			oraclePrice:    oracle,
			rate:           rate,
			amount:         amount,
		}
		state.trading.funding = append(state.trading.funding, funding)
		decision.FundingChanges = append(decision.FundingChanges, funding.snapshot())
		balance, balanceOK := state.trading.balanceSnapshot(
			position.accountID,
			instrument.settlementCurrency,
		)
		if !balanceOK {
			return rejectedTradingDecision(originalState, RejectionMarketDataStale)
		}
		decision.BalanceChanges = append(decision.BalanceChanges, balance)
		decision.Events = append(decision.Events, DomainEvent{
			EventID:          IDFromSequence(input.InputID, nextEventSequence(decision.Events)),
			Kind:             "funding.settled",
			AggregateID:      funding.fundingID,
			AggregateVersion: 1,
			LogicalTime:      input.LogicalTime,
		})
		sequence++
	}
	return state, decision
}

func liquidateTradingAccount(
	state State,
	input InputEnvelope,
	liquidate LiquidateAccount,
) (State, Decision) {
	originalState := state
	if liquidate.AccountID == "" {
		return rejectedTradingDecision(state, RejectionInvalidAction)
	}
	breached, positions, reason := liquidationCandidates(state.trading, liquidate.AccountID)
	if reason != "" {
		return rejectedTradingDecision(state, reason)
	}
	if !breached {
		return acceptedTradingDecision(state)
	}

	state.trading = state.trading.clone()
	state, decision := acceptedTradingDecision(state)
	for sequence, candidate := range positions {
		position := state.trading.positions[candidate.index]
		orderID := IDFromSequence(input.InputID, liquidationSequence(sequence))
		if _, exists := state.trading.orderIndex(orderID); exists {
			return rejectedTradingDecision(originalState, RejectionDuplicateOrderID)
		}
		zero, err := domain.NewQuantity("0", position.instrument)
		if err != nil {
			return rejectedTradingDecision(originalState, RejectionInvalidOrder)
		}
		order := orderRecord{
			orderID:        orderID,
			accountID:      position.accountID,
			instrument:     position.instrument,
			side:           oppositePositionSide(position.side),
			orderType:      OrderTypeMarket,
			timeInForce:    TimeInForceGTC,
			status:         OrderStatusWorking,
			quantity:       position.quantity,
			filledQuantity: zero,
			reduceOnly:     true,
			positionID:     position.positionID,
			version:        1,
		}
		state.trading.orders = append(state.trading.orders, order)
		orderIndex := len(state.trading.orders) - 1
		if rejection := executeOrder(
			&state.trading,
			input,
			orderIndex,
			&decision,
		); rejection != "" {
			return rejectedTradingDecision(originalState, rejection)
		}
		snapshot := state.trading.orders[orderIndex].snapshot()
		decision.OrderChanges = append(decision.OrderChanges, snapshot)
		decision.Events = append(
			decision.Events,
			orderEvent(input, snapshot, nextEventSequence(decision.Events)),
		)
	}
	return state, decision
}

type liquidationCandidate struct {
	index    int
	notional domain.Money
}

func liquidationCandidates(
	state tradingState,
	accountID string,
) (bool, []liquidationCandidate, RejectionReason) {
	var candidates []liquidationCandidate
	var crossCandidates []liquidationCandidate
	var currency domain.Currency
	var totalMaintenance domain.Money
	var totalUnrealized domain.Money
	var isolatedCollateral domain.Money
	initialized := false
	unitLeverage, unitErr := domain.NewRatio("1")
	if unitErr != nil {
		return false, nil, RejectionInvalidAction
	}
	for index, position := range state.positions {
		if position.accountID != accountID ||
			position.status != PositionStatusOpen {
			continue
		}
		instrument, ok := state.instrumentRecord(position.instrument.ID())
		book, bookOK := state.book(position.instrument.ID())
		if !ok || !bookOK || !book.hasMark {
			return false, nil, RejectionMarketDataStale
		}
		if !initialized {
			currency = instrument.settlementCurrency
			var err error
			totalMaintenance, err = domain.NewMoney("0", currency)
			if err != nil {
				return false, nil, RejectionInvalidAction
			}
			totalUnrealized, err = domain.NewMoney("0", currency)
			if err != nil {
				return false, nil, RejectionInvalidAction
			}
			isolatedCollateral, err = domain.NewMoney("0", currency)
			if err != nil {
				return false, nil, RejectionInvalidAction
			}
			initialized = true
		} else if !currency.Equal(instrument.settlementCurrency) {
			return false, nil, RejectionInvalidAction
		}
		maintenance, err := domain.PositionMargin(
			book.markPrice,
			position.quantity,
			instrument.maintenanceMarginRate,
			unitLeverage,
			currency,
		)
		if err != nil {
			return false, nil, RejectionInvalidAction
		}
		unrealized, err := domain.RealizedPnL(
			position.averageOpenPrice,
			book.markPrice,
			position.quantity,
			position.side == PositionSideLong,
			currency,
		)
		if err != nil {
			return false, nil, RejectionInvalidAction
		}
		notional, err := domain.PositionNotional(
			book.markPrice,
			position.quantity,
			currency,
		)
		if err != nil {
			return false, nil, RejectionInvalidAction
		}
		candidate := liquidationCandidate{
			index:    index,
			notional: notional,
		}
		if position.marginMode == MarginModeIsolated {
			bucketEquity, bucketErr := position.isolatedCollateral.Add(unrealized)
			if bucketErr != nil {
				return false, nil, RejectionInvalidAction
			}
			isolatedCollateral, err =
				isolatedCollateral.Add(position.isolatedCollateral)
			if err != nil {
				return false, nil, RejectionInvalidAction
			}
			if bucketEquity.Decimal().Cmp(maintenance.Decimal()) < 0 {
				candidates = append(candidates, candidate)
			}
			continue
		}
		totalMaintenance, err = totalMaintenance.Add(maintenance)
		if err != nil {
			return false, nil, RejectionInvalidAction
		}
		totalUnrealized, err = totalUnrealized.Add(unrealized)
		if err != nil {
			return false, nil, RejectionInvalidAction
		}
		crossCandidates = append(crossCandidates, candidate)
	}
	if !initialized {
		return false, nil, ""
	}
	balanceIndex, ok := state.balanceIndex(accountID, currency)
	if !ok {
		return false, nil, RejectionInsufficientFunds
	}
	crossEquity, err := state.balances[balanceIndex].total.Sub(isolatedCollateral)
	if err != nil {
		return false, nil, RejectionInvalidAction
	}
	crossEquity, err = crossEquity.Add(totalUnrealized)
	if err != nil {
		return false, nil, RejectionInvalidAction
	}
	if len(crossCandidates) > 0 &&
		crossEquity.Decimal().Cmp(totalMaintenance.Decimal()) < 0 {
		candidates = append(candidates, crossCandidates...)
	}
	if len(candidates) == 0 {
		return false, nil, ""
	}
	sort.SliceStable(candidates, func(left, right int) bool {
		comparison := candidates[left].notional.Decimal().Cmp(
			candidates[right].notional.Decimal(),
		)
		if comparison != 0 {
			return comparison > 0
		}
		leftID := state.positions[candidates[left].index].positionID.String()
		rightID := state.positions[candidates[right].index].positionID.String()
		return leftID < rightID
	})
	return true, candidates, ""
}

func liquidationSequence(index int) uint64 {
	sequence := uint64(1)
	for range index {
		sequence++
	}
	return sequence
}

func oppositePositionSide(side PositionSide) Side {
	if side == PositionSideLong {
		return SideSell
	}
	return SideBuy
}

func (state tradingState) hasSettlementCurrency(currency domain.Currency) bool {
	for _, instrument := range state.instruments {
		if instrument.settlementCurrency.Equal(currency) {
			return true
		}
	}
	return false
}

func (state tradingState) hasOpenPositionForInstrument(
	accountID string,
	instrumentID string,
) bool {
	for _, position := range state.positions {
		if position.accountID == accountID &&
			position.instrument.ID() == instrumentID &&
			position.status == PositionStatusOpen {
			return true
		}
	}
	return false
}

func (state tradingState) hasEconomicStateForInstrument(instrumentID string) bool {
	for _, position := range state.positions {
		if position.instrument.ID() == instrumentID &&
			position.status == PositionStatusOpen {
			return true
		}
	}
	for _, order := range state.orders {
		if order.instrument.ID() == instrumentID &&
			(order.status == OrderStatusHeld ||
				order.status == OrderStatusWorking ||
				order.status == OrderStatusPartiallyFilled) {
			return true
		}
	}
	return false
}

func (state tradingState) hasRiskAboveMaxLeverage(
	instrumentID string,
	maxLeverage domain.Ratio,
) bool {
	for _, risk := range state.risks {
		if risk.instrumentID == instrumentID &&
			risk.leverage.Decimal().Cmp(maxLeverage.Decimal()) > 0 {
			return true
		}
	}
	return false
}

func (state tradingState) hasActiveOrderForInstrument(
	accountID string,
	instrumentID string,
) bool {
	for _, order := range state.orders {
		if order.accountID == accountID &&
			order.instrument.ID() == instrumentID &&
			(order.status == OrderStatusWorking ||
				order.status == OrderStatusPartiallyFilled) {
			return true
		}
	}
	return false
}

func (state tradingState) risk(
	accountID string,
	instrument instrumentRecord,
) riskRecord {
	for _, risk := range state.risks {
		if risk.accountID == accountID &&
			risk.instrumentID == instrument.revision.ID() {
			return risk
		}
	}
	return riskRecord{
		accountID:    accountID,
		instrumentID: instrument.revision.ID(),
		marginMode:   MarginModeCross,
		leverage:     instrument.maxLeverage,
	}
}

func (state *tradingState) replaceRisk(risk riskRecord) {
	for index := range state.risks {
		if state.risks[index].accountID == risk.accountID &&
			state.risks[index].instrumentID == risk.instrumentID {
			state.risks[index] = risk
			return
		}
	}
	state.risks = append(state.risks, risk)
	sort.Slice(state.risks, func(left, right int) bool {
		if state.risks[left].accountID != state.risks[right].accountID {
			return state.risks[left].accountID < state.risks[right].accountID
		}
		return state.risks[left].instrumentID < state.risks[right].instrumentID
	})
}

func (state tradingState) balanceIndex(
	accountID string,
	currency domain.Currency,
) (int, bool) {
	for index, balance := range state.balances {
		if balance.accountID == accountID &&
			balance.total.Currency().Equal(currency) {
			return index, true
		}
	}
	return 0, false
}

func (state *tradingState) sortBalances() {
	sort.Slice(state.balances, func(left, right int) bool {
		if state.balances[left].accountID != state.balances[right].accountID {
			return state.balances[left].accountID < state.balances[right].accountID
		}
		return state.balances[left].total.Currency().Code() <
			state.balances[right].total.Currency().Code()
	})
}

func (state *tradingState) applyBalanceDelta(
	accountID string,
	delta domain.Money,
) error {
	index, ok := state.balanceIndex(accountID, delta.Currency())
	if !ok {
		return fmt.Errorf("account balance is missing")
	}
	total, err := state.balances[index].total.Add(delta)
	if err != nil {
		return err
	}
	state.balances[index].total = total
	return nil
}

func (state *tradingState) applyBalanceDebit(
	accountID string,
	amount domain.Money,
) error {
	index, ok := state.balanceIndex(accountID, amount.Currency())
	if !ok {
		return fmt.Errorf("account balance is missing")
	}
	total, err := state.balances[index].total.Sub(amount)
	if err != nil {
		return err
	}
	state.balances[index].total = total
	return nil
}

func (state tradingState) settlement(
	settlementID ID,
) (fundingSettlementRecord, bool) {
	for _, settlement := range state.settlements {
		if settlement.settlementID == settlementID {
			return settlement, true
		}
	}
	return fundingSettlementRecord{}, false
}

func (funding fundingRecord) snapshot() FundingSnapshot {
	return FundingSnapshot{
		FundingID:          funding.fundingID,
		SettlementID:       funding.settlementID,
		PositionID:         funding.positionID,
		AccountID:          funding.accountID,
		InstrumentID:       funding.instrument.ID(),
		SignedQuantity:     funding.signedQuantity,
		OraclePrice:        funding.oraclePrice.Decimal().String(),
		Rate:               funding.rate.Decimal().String(),
		Amount:             funding.amount.Decimal().String(),
		SettlementCurrency: funding.amount.Currency().Code(),
	}
}

// Balance returns the current exact risk projection for one account currency.
func (state State) Balance(accountID string, currencyCode string) (BalanceSnapshot, bool) {
	for _, balance := range state.trading.balances {
		if balance.accountID == accountID &&
			balance.total.Currency().Code() == currencyCode {
			return state.trading.balanceSnapshot(accountID, balance.total.Currency())
		}
	}
	return BalanceSnapshot{}, false
}

func (state tradingState) balanceSnapshot(
	accountID string,
	currency domain.Currency,
) (BalanceSnapshot, bool) {
	index, ok := state.balanceIndex(accountID, currency)
	if !ok {
		return BalanceSnapshot{}, false
	}
	used, unrealized, err := state.accountRiskTotals(accountID, currency)
	if err != nil {
		return BalanceSnapshot{}, false
	}
	total := state.balances[index].total
	equity, err := total.Add(unrealized)
	if err != nil {
		return BalanceSnapshot{}, false
	}
	free, err := equity.Sub(used)
	if err != nil {
		return BalanceSnapshot{}, false
	}
	return BalanceSnapshot{
		AccountID: accountID,
		Currency:  currency.Code(),
		Total:     total.Decimal().String(),
		Used:      used.Decimal().String(),
		Free:      free.Decimal().String(),
		Equity:    equity.Decimal().String(),
	}, true
}

func (state tradingState) accountRiskTotals(
	accountID string,
	currency domain.Currency,
) (domain.Money, domain.Money, error) {
	used, err := domain.NewMoney("0", currency)
	if err != nil {
		return domain.Money{}, domain.Money{}, err
	}
	unrealized, err := domain.NewMoney("0", currency)
	if err != nil {
		return domain.Money{}, domain.Money{}, err
	}
	for _, position := range state.positions {
		if position.accountID != accountID ||
			position.status != PositionStatusOpen ||
			!position.settlementCurrency.Equal(currency) {
			continue
		}
		instrument, ok := state.instrumentRecord(position.instrument.ID())
		if !ok {
			return domain.Money{}, domain.Money{}, fmt.Errorf("missing instrument risk")
		}
		risk := state.risk(accountID, instrument)
		margin, marginErr := domain.PositionMargin(
			position.averageOpenPrice,
			position.quantity,
			instrument.initialMarginRate,
			risk.leverage,
			currency,
		)
		if marginErr != nil {
			return domain.Money{}, domain.Money{}, marginErr
		}
		used, err = used.Add(margin)
		if err != nil {
			return domain.Money{}, domain.Money{}, err
		}
		book, bookOK := state.book(position.instrument.ID())
		if !bookOK || !book.hasMark {
			return domain.Money{}, domain.Money{}, fmt.Errorf("missing position mark")
		}
		pnl, pnlErr := domain.RealizedPnL(
			position.averageOpenPrice,
			book.markPrice,
			position.quantity,
			position.side == PositionSideLong,
			currency,
		)
		if pnlErr != nil {
			return domain.Money{}, domain.Money{}, pnlErr
		}
		unrealized, err = unrealized.Add(pnl)
		if err != nil {
			return domain.Money{}, domain.Money{}, err
		}
	}
	for _, order := range state.orders {
		if order.accountID != accountID || order.reduceOnly ||
			(order.status != OrderStatusWorking &&
				order.status != OrderStatusPartiallyFilled) {
			continue
		}
		instrument, ok := state.instrumentRecord(order.instrument.ID())
		if !ok || !instrument.settlementCurrency.Equal(currency) {
			continue
		}
		margin, marginErr := state.orderMargin(order, instrument)
		if marginErr != nil {
			return domain.Money{}, domain.Money{}, marginErr
		}
		used, err = used.Add(margin)
		if err != nil {
			return domain.Money{}, domain.Money{}, err
		}
	}
	return used, unrealized, nil
}

func (state tradingState) orderMargin(
	order orderRecord,
	instrument instrumentRecord,
) (domain.Money, error) {
	remaining, err := order.quantity.Sub(order.filledQuantity)
	if err != nil {
		return domain.Money{}, err
	}
	book, ok := state.book(order.instrument.ID())
	if !ok || !book.hasMark {
		return domain.Money{}, fmt.Errorf("missing order mark")
	}
	price := book.markPrice
	for _, candidate := range []struct {
		price   domain.Price
		present bool
	}{
		{price: order.price, present: order.hasPrice},
		{price: order.triggerPrice, present: order.hasTrigger},
	} {
		if candidate.present &&
			candidate.price.Decimal().Cmp(price.Decimal()) > 0 {
			price = candidate.price
		}
	}
	for _, level := range executableLevels(*book, order.side) {
		if !levelEligible(level, order) {
			break
		}
		if level.price.Decimal().Cmp(price.Decimal()) > 0 {
			price = level.price
		}
	}
	risk := state.risk(order.accountID, instrument)
	margin, err := domain.PositionMargin(
		price,
		remaining,
		instrument.initialMarginRate,
		risk.leverage,
		instrument.settlementCurrency,
	)
	if err != nil {
		return domain.Money{}, err
	}
	feeRate := instrument.takerFeeRate
	if instrument.makerFeeRate.Decimal().Cmp(feeRate.Decimal()) > 0 {
		feeRate = instrument.makerFeeRate
	}
	if feeRate.Decimal().Sign() < 0 {
		var rateErr error
		feeRate, rateErr = domain.NewRate("0")
		if rateErr != nil {
			return domain.Money{}, rateErr
		}
	}
	fee, err := domain.TradingFee(
		price,
		remaining,
		feeRate,
		instrument.settlementCurrency,
	)
	if err != nil {
		return domain.Money{}, err
	}
	return margin.Add(fee)
}

func marginAdmissionReason(
	state tradingState,
	order orderRecord,
) RejectionReason {
	if order.reduceOnly {
		return ""
	}
	instrument, ok := state.instrumentRecord(order.instrument.ID())
	if !ok {
		return RejectionInvalidInstrument
	}
	balanceIndex, ok := state.balanceIndex(order.accountID, instrument.settlementCurrency)
	if !ok || state.balances[balanceIndex].total.Decimal().Sign() <= 0 {
		return RejectionInsufficientFunds
	}
	snapshot, ok := state.balanceSnapshot(order.accountID, instrument.settlementCurrency)
	if !ok {
		return RejectionMarketDataStale
	}
	free, err := domain.NewMoney(snapshot.Free, instrument.settlementCurrency)
	if err != nil {
		return RejectionInvalidOrder
	}
	required, err := state.orderMargin(order, instrument)
	if err != nil {
		return RejectionMarketDataStale
	}
	if required.Decimal().Cmp(free.Decimal()) > 0 {
		return RejectionInsufficientMargin
	}
	return ""
}
