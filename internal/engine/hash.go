package engine

import (
	"crypto/sha256"
	"encoding/binary"
	"hash"
)

func hashInput(input InputEnvelope) Hash {
	result, engineError := hashInputAtVersion(input, CurrentInputHashVersion)
	if engineError != nil {
		return Hash{}
	}
	return result
}

// InputHash fingerprints the complete delivered envelope, including its
// JetStream-assigned stream sequence.
func InputHash(input InputEnvelope) Hash {
	return hashInput(input)
}

// BusinessInputHash fingerprints the stable producer input while excluding
// the JetStream-assigned delivery sequence.
func BusinessInputHash(input InputEnvelope) Hash {
	return hashBusinessInput(input)
}

// BusinessInputHashAtVersion verifies a historical stable-input fingerprint.
func BusinessInputHashAtVersion(
	input InputEnvelope,
	version uint32,
) (Hash, error) {
	result, engineError := businessInputHashAtVersion(input, version)
	if engineError != nil {
		return Hash{}, engineError
	}
	return result, nil
}

func hashBusinessInput(input InputEnvelope) Hash {
	result, engineError := businessInputHashAtVersion(
		input,
		CurrentBusinessHashVersion,
	)
	if engineError != nil {
		return Hash{}
	}
	return result
}

func businessInputHashAtVersion(
	input InputEnvelope,
	version uint32,
) (Hash, *Error) {
	if version != CurrentBusinessHashVersion {
		return Hash{}, &Error{
			Kind:     ErrUnknownHashVersion,
			Sequence: input.StreamSequence,
			Detail:   "business input hash version is not supported",
		}
	}
	return finishHash(func(hasher hash.Hash) {
		writeString(hasher, "platformgo.engine.business-input.v1")
		writeBytes(hasher, input.InputID[:])
		writeUint32(hasher, input.SchemaVersion)
		writeUint32(hasher, uint32(input.ShardID))
		writeUint8(hasher, uint8(input.Kind))
		writeString(hasher, input.SourceID)
		writeUint64(hasher, input.SourceSequence)
		writeUint64(hasher, input.MarketSequence)
		writeInt64(hasher, input.LogicalTime.UnixNano())
		writeUint64(hasher, input.ConfigurationVersion)
		writeUint64(hasher, input.InstrumentVersion)
		writeBytes(hasher, input.Payload.value)
	}), nil
}

func hashInputAtVersion(input InputEnvelope, version uint32) (Hash, *Error) {
	if version != CurrentInputHashVersion {
		return Hash{}, &Error{
			Kind:     ErrUnknownHashVersion,
			Sequence: input.StreamSequence,
			Detail:   "input hash version is not supported",
		}
	}
	return finishHash(func(hasher hash.Hash) {
		writeString(hasher, "platformgo.engine.input.v1")
		writeBytes(hasher, input.InputID[:])
		writeUint32(hasher, input.SchemaVersion)
		writeUint32(hasher, uint32(input.ShardID))
		writeUint8(hasher, uint8(input.Kind))
		writeString(hasher, input.SourceID)
		writeUint64(hasher, input.SourceSequence)
		writeUint64(hasher, input.StreamSequence)
		writeUint64(hasher, input.MarketSequence)
		writeInt64(hasher, input.LogicalTime.UnixNano())
		writeUint64(hasher, input.ConfigurationVersion)
		writeUint64(hasher, input.InstrumentVersion)
		writeBytes(hasher, input.Payload.value)
	}), nil
}

func hashDecision(previousStateHash Hash, inputHash Hash, effectsHash Hash) Hash {
	return finishHash(func(hasher hash.Hash) {
		writeString(hasher, "platformgo.engine.decision.v2")
		writeBytes(hasher, previousStateHash[:])
		writeBytes(hasher, inputHash[:])
		writeBytes(hasher, effectsHash[:])
	})
}

func hashEffects(decision Decision) Hash {
	return finishHash(func(hasher hash.Hash) {
		writeString(hasher, "platformgo.engine.effects.v1")
		writeString(hasher, string(decision.CommandResult.Status))
		writeString(hasher, string(decision.CommandResult.Reason))
		writeBytes(hasher, decision.DuplicateOfDecisionHash[:])
		writeUint64(hasher, uint64(len(decision.InstrumentChanges)))
		for _, instrument := range decision.InstrumentChanges {
			writeString(hasher, instrument.InstrumentID)
			writeUint64(hasher, instrument.Revision)
			writeUint8(hasher, instrument.PriceScale)
			writeUint8(hasher, instrument.QuantityScale)
			writeString(hasher, instrument.SettlementCurrency)
			writeUint8(hasher, instrument.SettlementCurrencyScale)
			writeString(hasher, instrument.InitialMarginRate)
			writeString(hasher, instrument.MaintenanceMarginRate)
			writeString(hasher, instrument.MaxLeverage)
			writeString(hasher, instrument.MakerFeeRate)
			writeString(hasher, instrument.TakerFeeRate)
		}
		writeUint64(hasher, uint64(len(decision.AccountChanges)))
		for _, account := range decision.AccountChanges {
			writeString(hasher, account.AccountID)
			writeString(hasher, string(account.OmsMode))
		}
		writeUint64(hasher, uint64(len(decision.RiskChanges)))
		for _, risk := range decision.RiskChanges {
			writeString(hasher, risk.AccountID)
			writeString(hasher, risk.InstrumentID)
			writeString(hasher, string(risk.MarginMode))
			writeString(hasher, risk.Leverage)
		}
		writeUint64(hasher, uint64(len(decision.BalanceChanges)))
		for _, balance := range decision.BalanceChanges {
			writeString(hasher, balance.AccountID)
			writeString(hasher, balance.Currency)
			writeString(hasher, balance.Total)
			writeString(hasher, balance.Used)
			writeString(hasher, balance.Free)
			writeString(hasher, balance.Equity)
		}
		writeUint64(hasher, uint64(len(decision.LedgerChanges)))
		for _, transaction := range decision.LedgerChanges {
			writeBytes(hasher, transaction.TransactionID[:])
			writeString(hasher, transaction.BusinessKey)
			writeBytes(hasher, transaction.InputID[:])
			writeInt64(hasher, transaction.LogicalTime.UnixNano())
			writeUint64(hasher, uint64(len(transaction.Entries)))
			for _, entry := range transaction.Entries {
				writeBytes(hasher, entry.EntryID[:])
				writeString(hasher, entry.AccountID)
				writeString(hasher, entry.Currency)
				writeString(hasher, entry.Amount)
			}
		}
		writeUint64(hasher, uint64(len(decision.FundingChanges)))
		for _, funding := range decision.FundingChanges {
			writeBytes(hasher, funding.FundingID[:])
			writeBytes(hasher, funding.SettlementID[:])
			writeBytes(hasher, funding.PositionID[:])
			writeString(hasher, funding.AccountID)
			writeString(hasher, funding.InstrumentID)
			writeString(hasher, funding.SignedQuantity)
			writeString(hasher, funding.OraclePrice)
			writeString(hasher, funding.Rate)
			writeString(hasher, funding.Amount)
			writeString(hasher, funding.SettlementCurrency)
		}
		writeUint64(hasher, uint64(len(decision.BookChanges)))
		for _, book := range decision.BookChanges {
			writeString(hasher, book.InstrumentID)
			writeString(hasher, book.MarkPrice)
			writeBookLevels(hasher, book.Bids)
			writeBookLevels(hasher, book.Asks)
		}
		writeUint64(hasher, uint64(len(decision.OrderChanges)))
		for _, order := range decision.OrderChanges {
			writeBytes(hasher, order.OrderID[:])
			writeString(hasher, order.AccountID)
			writeString(hasher, order.InstrumentID)
			writeString(hasher, string(order.Side))
			writeString(hasher, string(order.Type))
			writeString(hasher, string(order.TimeInForce))
			writeString(hasher, string(order.Status))
			writeString(hasher, order.Quantity)
			writeString(hasher, order.FilledQuantity)
			writeString(hasher, order.AverageFillPrice)
			writeString(hasher, order.Price)
			writeString(hasher, order.TriggerPrice)
			writeUint8(hasher, boolByte(order.Triggered))
			writeInt64(hasher, order.TriggeredAt.UnixNano())
			writeUint8(hasher, boolByte(order.ReduceOnly))
			writeBytes(hasher, order.PositionID[:])
			writeBytes(hasher, order.BracketID[:])
			writeString(hasher, string(order.BracketLeg))
			writeUint32(hasher, order.BracketLegIndex)
			writeUint8(hasher, boolByte(order.HasRested))
			writeUint8(hasher, boolByte(order.HasSlippageBand))
			writeUint32(hasher, order.MaxSlippageBPS)
			writeString(hasher, order.SlippageReference)
			writeString(hasher, string(order.RejectReason))
			writeUint64(hasher, order.Version)
		}
		writeUint64(hasher, uint64(len(decision.Fills)))
		for _, fill := range decision.Fills {
			writeBytes(hasher, fill.FillID[:])
			writeBytes(hasher, fill.OrderID[:])
			writeString(hasher, fill.AccountID)
			writeString(hasher, fill.InstrumentID)
			writeString(hasher, string(fill.Side))
			writeString(hasher, fill.Price)
			writeString(hasher, fill.Quantity)
			writeBytes(hasher, fill.PositionID[:])
			writeString(hasher, string(fill.PositionEffect))
			writeString(hasher, fill.RealizedPnL)
			writeString(hasher, fill.SettlementCurrency)
			writeString(hasher, string(fill.LiquiditySide))
			writeString(hasher, fill.Fee)
			writeString(hasher, fill.FeeCurrency)
			writeInt64(hasher, fill.LogicalTime.UnixNano())
		}
		writeUint64(hasher, uint64(len(decision.PositionChanges)))
		for _, position := range decision.PositionChanges {
			writeBytes(hasher, position.PositionID[:])
			writeString(hasher, position.AccountID)
			writeString(hasher, position.InstrumentID)
			writeString(hasher, string(position.Side))
			writeString(hasher, string(position.Status))
			writeString(hasher, position.SignedQuantity)
			writeString(hasher, position.AverageOpenPrice)
			writeString(hasher, position.RealizedPnL)
			writeString(hasher, position.SettlementCurrency)
			writeString(hasher, string(position.MarginMode))
			writeString(hasher, position.IsolatedCollateral)
			writeUint64(hasher, position.Version)
		}
		writeUint64(hasher, uint64(len(decision.Events)))
		for _, event := range decision.Events {
			writeBytes(hasher, event.EventID[:])
			writeString(hasher, event.Kind)
			writeBytes(hasher, event.AggregateID[:])
			writeUint64(hasher, event.AggregateVersion)
			writeInt64(hasher, event.LogicalTime.UnixNano())
		}
	})
}

func writeBookLevels(hasher hash.Hash, levels []BookLevel) {
	writeUint64(hasher, uint64(len(levels)))
	for _, level := range levels {
		writeString(hasher, level.Price)
		writeString(hasher, level.Quantity)
	}
}

func boolByte(value bool) uint8 {
	if value {
		return 1
	}
	return 0
}

func hashInitialState(shardID ShardID) Hash {
	return finishHash(func(hasher hash.Hash) {
		writeString(hasher, "platformgo.engine.state.v1")
		writeUint32(hasher, uint32(shardID))
		writeUint64(hasher, 1)
		writeUint8(hasher, 1)
	})
}

func hashAcceptedState(previous Hash, inputHash Hash, decisionHash Hash, nextSequence uint64) Hash {
	return finishHash(func(hasher hash.Hash) {
		writeString(hasher, "platformgo.engine.state.accepted.v2")
		writeBytes(hasher, previous[:])
		writeBytes(hasher, inputHash[:])
		writeBytes(hasher, decisionHash[:])
		writeUint64(hasher, nextSequence)
		writeUint8(hasher, 1)
	})
}

func hashHaltedState(previous Hash, inputHash Hash, engineError *Error) Hash {
	return finishHash(func(hasher hash.Hash) {
		writeString(hasher, "platformgo.engine.state.halted.v1")
		writeBytes(hasher, previous[:])
		writeBytes(hasher, inputHash[:])
		writeString(hasher, string(engineError.Kind))
		writeUint64(hasher, engineError.Sequence)
		writeString(hasher, engineError.Detail)
		writeUint8(hasher, 0)
	})
}

func finishHash(write func(hash.Hash)) Hash {
	hasher := sha256.New()
	write(hasher)
	var result Hash
	copy(result[:], hasher.Sum(nil))
	return result
}

func writeBytes(hasher hash.Hash, value []byte) {
	writeUint64(hasher, uint64(len(value)))
	_, _ = hasher.Write(value)
}

func writeString(hasher hash.Hash, value string) {
	writeBytes(hasher, []byte(value))
}

func writeUint8(hasher hash.Hash, value uint8) {
	_, _ = hasher.Write([]byte{value})
}

func writeUint32(hasher hash.Hash, value uint32) {
	var encoded [4]byte
	binary.BigEndian.PutUint32(encoded[:], value)
	_, _ = hasher.Write(encoded[:])
}

func writeUint64(hasher hash.Hash, value uint64) {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], value)
	_, _ = hasher.Write(encoded[:])
}

func writeInt64(hasher hash.Hash, value int64) {
	var encoded [binary.MaxVarintLen64]byte
	length := binary.PutVarint(encoded[:], value)
	writeBytes(hasher, encoded[:length])
}
