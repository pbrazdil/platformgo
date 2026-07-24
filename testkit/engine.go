package testkit

import (
	"fmt"

	"github.com/upcomers-org/platformgo/internal/engine"
)

// EngineFixture owns one synchronous engine state, explicit clock, deterministic
// shard-scoped IDs, and O(1) receipt index.
type EngineFixture struct {
	state      engine.State
	receipts   *engine.MemoryReceiptIndex
	IDs        *IDSequence
	Clock      *ManualClock
	sourceID   string
	marketSeq  uint64
	configRev  uint64
	instrument uint64
}

// NewEngineFixture creates a deterministic fixture for one shard.
func NewEngineFixture(
	shardID engine.ShardID,
	start engine.LogicalTime,
) *EngineFixture {
	return &EngineFixture{
		state:      engine.NewState(shardID),
		receipts:   engine.NewMemoryReceiptIndex(),
		IDs:        NewShardIDSequence(shardID),
		Clock:      NewManualClock(start),
		sourceID:   "testkit",
		marketSeq:  1,
		configRev:  1,
		instrument: 1,
	}
}

// State returns the current immutable engine state.
func (fixture *EngineFixture) State() engine.State {
	return fixture.state
}

// ApplyTrading synchronously applies one typed action and records its receipt
// after successful processing.
func (fixture *EngineFixture) ApplyTrading(action engine.TradingAction) (engine.Decision, error) {
	sequence := fixture.state.NextStreamSequence()
	input, err := (TradingInput{
		InputID:              fixture.IDs.Next(),
		ShardID:              fixture.state.ShardID(),
		SourceID:             fixture.sourceID,
		SourceSequence:       sequence,
		StreamSequence:       sequence,
		MarketSequence:       fixture.marketSeq,
		LogicalTime:          fixture.Clock.Now(),
		ConfigurationVersion: fixture.configRev,
		InstrumentVersion:    fixture.instrument,
		Action:               action,
	}).CanonicalEnvelope()
	if err != nil {
		return engine.Decision{}, err
	}
	next, decision, applyErr := engine.ApplyTradingWithReceipts(
		fixture.state,
		input,
		action,
		fixture.receipts,
	)
	fixture.state = next
	if applyErr != nil {
		return engine.Decision{}, applyErr
	}
	if err := fixture.receipts.Record(engine.NewReceipt(input, decision)); err != nil {
		return engine.Decision{}, fmt.Errorf("record fixture receipt: %w", err)
	}
	return decision, nil
}
