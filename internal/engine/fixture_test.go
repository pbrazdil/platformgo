package engine

import (
	"testing"
	"time"
)

type engineFixture struct {
	state     State
	namespace ID
	nextID    uint64
	nextTime  LogicalTime
}

func newEngineFixture(t *testing.T, shardID ShardID) *engineFixture {
	t.Helper()

	return &engineFixture{
		state:     NewState(shardID),
		namespace: mustID(t, "019f9460-4b36-4e9b-8f44-682611f7ee01"),
		nextID:    1,
		nextTime:  NewLogicalTime(time.Date(2026, time.July, 24, 10, 0, 0, 0, time.UTC)),
	}
}

func (fixture *engineFixture) apply(t *testing.T, payload string) Decision {
	t.Helper()

	sequence := fixture.state.NextStreamSequence()
	input := InputEnvelope{
		InputID:              IDFromSequence(fixture.namespace, fixture.nextID),
		SchemaVersion:        CurrentSchemaVersion,
		ShardID:              fixture.state.ShardID(),
		Kind:                 InputKindCommand,
		SourceID:             "engine-fixture",
		SourceSequence:       sequence,
		StreamSequence:       sequence,
		MarketSequence:       1,
		LogicalTime:          fixture.nextTime,
		ConfigurationVersion: 1,
		InstrumentVersion:    1,
		Payload:              []byte(payload),
	}
	fixture.nextID++
	fixture.nextTime += LogicalTime(time.Second)

	var decision Decision
	var err error
	fixture.state, decision, err = Apply(fixture.state, input)
	if err != nil {
		t.Fatalf("Apply sequence %d: %v", sequence, err)
	}
	return decision
}

func TestEngineFixtureUsesManualTimeIDsAndSynchronousApply(t *testing.T) {
	left := newEngineFixture(t, 19)
	right := newEngineFixture(t, 19)

	leftFirst := left.apply(t, `{"command":"first"}`)
	leftSecond := left.apply(t, `{"command":"second"}`)
	rightFirst := right.apply(t, `{"command":"first"}`)
	rightSecond := right.apply(t, `{"command":"second"}`)

	if !equalDecision(leftFirst, rightFirst) || !equalDecision(leftSecond, rightSecond) {
		t.Fatalf("identical fixture runs differed:\nleft:  %+v %+v\nright: %+v %+v", leftFirst, leftSecond, rightFirst, rightSecond)
	}
	if left.state.Hash() != right.state.Hash() {
		t.Fatalf("identical fixture state hashes differ: %s != %s", left.state.Hash(), right.state.Hash())
	}
	if leftFirst.LogicalTime.String() != "2026-07-24T10:00:00Z" ||
		leftSecond.LogicalTime.String() != "2026-07-24T10:00:01Z" {
		t.Fatalf("fixture logical times = %s, %s", leftFirst.LogicalTime, leftSecond.LogicalTime)
	}
}
