package testkit

import "github.com/upcomers-org/platformgo/internal/engine"

// TradingInput describes every explicit envelope value for one typed action.
type TradingInput struct {
	InputID              engine.ID
	ShardID              engine.ShardID
	SourceID             string
	SourceSequence       uint64
	StreamSequence       uint64
	MarketSequence       uint64
	LogicalTime          engine.LogicalTime
	ConfigurationVersion uint64
	InstrumentVersion    uint64
	Action               engine.TradingAction
}

// CanonicalEnvelope constructs a versioned envelope from a typed action.
func (input TradingInput) CanonicalEnvelope() (engine.InputEnvelope, error) {
	payload, err := engine.EncodeTradingAction(input.Action)
	if err != nil {
		return engine.InputEnvelope{}, err
	}
	return engine.InputEnvelope{
		InputID:              input.InputID,
		SchemaVersion:        engine.CurrentSchemaVersion,
		ShardID:              input.ShardID,
		Kind:                 engine.InputKindCommand,
		SourceID:             input.SourceID,
		SourceSequence:       input.SourceSequence,
		StreamSequence:       input.StreamSequence,
		MarketSequence:       input.MarketSequence,
		LogicalTime:          input.LogicalTime,
		ConfigurationVersion: input.ConfigurationVersion,
		InstrumentVersion:    input.InstrumentVersion,
		Payload:              payload,
	}, nil
}
