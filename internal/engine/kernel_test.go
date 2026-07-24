package engine

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestDeterministicIDIsStableCanonicalUUID(t *testing.T) {
	namespace := mustID(t, "019f9460-4b36-4e9b-8f44-682611f7ee01")

	first := IDFromSequence(namespace, 1)
	if got, want := first.String(), "c2d8a6a7-bb19-4883-bef5-27988f45835d"; got != want {
		t.Fatalf("IDFromSequence(namespace, 1) = %q, want %q", got, want)
	}
	if first == IDFromSequence(namespace, 2) {
		t.Fatal("different sequences produced the same ID")
	}
	if first.String()[14] != '4' {
		t.Fatalf("version digit = %q, want 4", first.String()[14])
	}
	switch first.String()[19] {
	case '8', '9', 'a', 'b':
	default:
		t.Fatalf("variant digit = %q, want RFC 4122 variant", first.String()[19])
	}

	parsed, err := ParseID(first.String())
	if err != nil {
		t.Fatalf("ParseID(%q): %v", first, err)
	}
	if parsed != first {
		t.Fatalf("ParseID round trip = %v, want %v", parsed, first)
	}
	if _, err := ParseID(strings.ToUpper(first.String())); !errors.Is(err, ErrInvalidID) {
		t.Fatalf("uppercase ParseID error = %v, want ErrInvalidID", err)
	}
}

func TestLogicalTimeIsExplicitAndCanonical(t *testing.T) {
	input := time.Date(2026, time.July, 24, 12, 34, 56, 123456789, time.FixedZone("offset", 2*60*60))
	logicalTime := NewLogicalTime(input)

	if got, want := logicalTime.UnixNano(), int64(1784889296123456789); got != want {
		t.Fatalf("UnixNano() = %d, want %d", got, want)
	}
	if got, want := logicalTime.String(), "2026-07-24T10:34:56.123456789Z"; got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}

func TestApplyRecordsDeterministicDecision(t *testing.T) {
	state := NewState(7)
	input := testInput(t, 1)

	next, decision, err := Apply(state, input)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !next.Ready() {
		t.Fatal("accepted input made state not ready")
	}
	if got, want := next.NextStreamSequence(), uint64(2); got != want {
		t.Fatalf("next stream sequence = %d, want %d", got, want)
	}
	if decision.InputID != input.InputID ||
		decision.StreamSequence != input.StreamSequence ||
		decision.SourceSequence != input.SourceSequence ||
		decision.MarketSequence != input.MarketSequence ||
		decision.LogicalTime != input.LogicalTime ||
		decision.ConfigurationVersion != input.ConfigurationVersion ||
		decision.InstrumentVersion != input.InstrumentVersion {
		t.Fatalf("decision metadata = %+v, want envelope metadata %+v", decision, input)
	}
	if decision.InputHash.IsZero() || decision.DecisionHash.IsZero() || decision.NextStateHash.IsZero() {
		t.Fatalf("decision hashes must be non-zero: %+v", decision)
	}
	if decision.NextStateHash != next.Hash() {
		t.Fatalf("decision next state hash = %s, state hash = %s", decision.NextStateHash, next.Hash())
	}
}

func TestDuplicateInputReturnsRecordedDecisionWithoutMutation(t *testing.T) {
	state, recorded, err := Apply(NewState(7), testInput(t, 1))
	if err != nil {
		t.Fatalf("first Apply: %v", err)
	}
	beforeHash := state.Hash()

	next, duplicate, err := Apply(state, testInput(t, 1))
	if err != nil {
		t.Fatalf("duplicate Apply: %v", err)
	}
	if next.Hash() != beforeHash || next.NextStreamSequence() != state.NextStreamSequence() {
		t.Fatalf("duplicate mutated state: before=%s after=%s", beforeHash, next.Hash())
	}
	if !equalDecision(duplicate, recorded) {
		t.Fatalf("duplicate decision differs:\nrecorded:  %+v\nduplicate: %+v", recorded, duplicate)
	}
}

func TestReplayProducesIdenticalHashes(t *testing.T) {
	inputs := []InputEnvelope{testInput(t, 1), testInput(t, 2), testInput(t, 3)}

	leftState, leftDecisions := replay(t, inputs)
	rightState, rightDecisions := replay(t, inputs)

	if leftState.Hash() != rightState.Hash() {
		t.Fatalf("replayed state hash differs: %s != %s", leftState.Hash(), rightState.Hash())
	}
	for index := range leftDecisions {
		if !equalDecision(leftDecisions[index], rightDecisions[index]) {
			t.Fatalf("decision %d differs:\nleft:  %+v\nright: %+v", index, leftDecisions[index], rightDecisions[index])
		}
	}
}

func TestPayloadMutationCannotChangeRecordedState(t *testing.T) {
	input := testInput(t, 1)
	state, decision, err := Apply(NewState(7), input)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	input.Payload[0] ^= 0xff

	if state.Hash() != decision.NextStateHash {
		t.Fatalf("payload mutation changed state hash: %s != %s", state.Hash(), decision.NextStateHash)
	}
}

func TestApplyDoesNotMutateInputState(t *testing.T) {
	firstState, firstDecision, err := Apply(NewState(7), testInput(t, 1))
	if err != nil {
		t.Fatalf("first Apply: %v", err)
	}
	firstHash := firstState.Hash()

	leftInput := testInput(t, 2)
	leftInput.Payload = []byte("left")
	leftState, _, err := Apply(firstState, leftInput)
	if err != nil {
		t.Fatalf("left Apply: %v", err)
	}
	rightInput := testInput(t, 2)
	rightInput.Payload = []byte("right")
	rightState, _, err := Apply(firstState, rightInput)
	if err != nil {
		t.Fatalf("right Apply: %v", err)
	}

	if firstState.Hash() != firstHash || firstState.NextStreamSequence() != 2 {
		t.Fatal("Apply mutated its input state")
	}
	duplicateState, duplicateDecision, err := Apply(firstState, testInput(t, 1))
	if err != nil {
		t.Fatalf("duplicate on input state: %v", err)
	}
	if duplicateState.Hash() != firstHash || !equalDecision(duplicateDecision, firstDecision) {
		t.Fatal("later branches mutated the input state's receipt")
	}
	if leftState.Hash() == rightState.Hash() {
		t.Fatal("distinct branch inputs produced the same state hash")
	}
}

func TestOrderingAndEnvelopeFailuresHaltShard(t *testing.T) {
	tests := []struct {
		name  string
		state State
		input InputEnvelope
		want  error
	}{
		{
			name:  "gap",
			state: NewState(7),
			input: testInput(t, 2),
			want:  ErrSequenceGap,
		},
		{
			name:  "unknown schema",
			state: NewState(7),
			input: func() InputEnvelope {
				input := testInput(t, 1)
				input.SchemaVersion = CurrentSchemaVersion + 1
				return input
			}(),
			want: ErrUnknownSchema,
		},
		{
			name:  "unknown kind",
			state: NewState(7),
			input: func() InputEnvelope {
				input := testInput(t, 1)
				input.Kind = InputKind(255)
				return input
			}(),
			want: ErrUnknownInputKind,
		},
		{
			name:  "wrong shard",
			state: NewState(8),
			input: testInput(t, 1),
			want:  ErrShardMismatch,
		},
		{
			name: "sequence exhausted",
			state: func() State {
				state := NewState(7)
				state.nextStreamSequence = ^uint64(0)
				return state
			}(),
			input: testInput(t, ^uint64(0)),
			want:  ErrSequenceExhausted,
		},
		{
			name: "regression",
			state: func() State {
				state, _, err := Apply(NewState(7), testInput(t, 1))
				if err != nil {
					t.Fatalf("prepare regression state: %v", err)
				}
				input := testInput(t, 2)
				state, _, err = Apply(state, input)
				if err != nil {
					t.Fatalf("prepare regression state: %v", err)
				}
				state.receipts = nil
				return state
			}(),
			input: func() InputEnvelope {
				input := testInput(t, 1)
				input.Payload = []byte("different")
				return input
			}(),
			want: ErrSequenceRegression,
		},
		{
			name: "sequence conflict",
			state: func() State {
				state, _, err := Apply(NewState(7), testInput(t, 1))
				if err != nil {
					t.Fatalf("prepare conflict state: %v", err)
				}
				return state
			}(),
			input: func() InputEnvelope {
				input := testInput(t, 1)
				input.Payload = []byte("different")
				return input
			}(),
			want: ErrInputConflict,
		},
		{
			name: "input id reuse",
			state: func() State {
				state, _, err := Apply(NewState(7), testInput(t, 1))
				if err != nil {
					t.Fatalf("prepare ID reuse state: %v", err)
				}
				return state
			}(),
			input: func() InputEnvelope {
				input := testInput(t, 2)
				input.InputID = testInput(t, 1).InputID
				return input
			}(),
			want: ErrInputConflict,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			next, _, err := Apply(testCase.state, testCase.input)
			if !errors.Is(err, testCase.want) {
				t.Fatalf("Apply error = %v, want %v", err, testCase.want)
			}
			if next.Ready() {
				t.Fatal("fatal input failure left shard ready")
			}
			if next.Hash() == testCase.state.Hash() {
				t.Fatal("fatal input failure did not record a distinct halted state hash")
			}
			again, _, err := Apply(next, testInput(t, next.NextStreamSequence()))
			if !errors.Is(err, ErrShardNotReady) {
				t.Fatalf("Apply after halt error = %v, want ErrShardNotReady", err)
			}
			if again.Hash() != next.Hash() {
				t.Fatal("Apply after halt mutated state")
			}
		})
	}
}

func replay(t *testing.T, inputs []InputEnvelope) (State, []Decision) {
	t.Helper()

	state := NewState(7)
	decisions := make([]Decision, 0, len(inputs))
	for _, input := range inputs {
		var decision Decision
		var err error
		state, decision, err = Apply(state, input)
		if err != nil {
			t.Fatalf("Apply sequence %d: %v", input.StreamSequence, err)
		}
		decisions = append(decisions, decision)
	}
	return state, decisions
}

func testInput(t *testing.T, streamSequence uint64) InputEnvelope {
	t.Helper()

	namespace := mustID(t, "019f9460-4b36-4e9b-8f44-682611f7ee01")
	return InputEnvelope{
		InputID:              IDFromSequence(namespace, streamSequence),
		SchemaVersion:        CurrentSchemaVersion,
		ShardID:              7,
		Kind:                 InputKindCommand,
		SourceID:             "fixture",
		SourceSequence:       streamSequence,
		StreamSequence:       streamSequence,
		MarketSequence:       31,
		LogicalTime:          NewLogicalTime(time.Date(2026, time.July, 24, 10, 0, 1, 0, time.UTC)),
		ConfigurationVersion: 11,
		InstrumentVersion:    23,
		Payload:              []byte(`{"command":"noop"}`),
	}
}

func mustID(t *testing.T, input string) ID {
	t.Helper()

	id, err := ParseID(input)
	if err != nil {
		t.Fatalf("ParseID(%q): %v", input, err)
	}
	return id
}

func equalDecision(left, right Decision) bool {
	return reflect.DeepEqual(left, right)
}
