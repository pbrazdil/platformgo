package engine

import (
	"errors"
	"testing"
)

func TestHistoricalDuplicateResolvesBeforeCurrentSchemaValidation(t *testing.T) {
	index := NewMemoryReceiptIndex()
	state := NewState(7)

	firstInput := testInput(t, 1)
	var firstDecision Decision
	var err error
	state, firstDecision, err = ApplyWithReceipts(state, firstInput, index)
	if err != nil {
		t.Fatalf("first ApplyWithReceipts: %v", err)
	}
	if recordErr := index.Record(NewReceipt(firstInput, firstDecision)); recordErr != nil {
		t.Fatalf("record first receipt: %v", recordErr)
	}

	secondInput := testInput(t, 2)
	var secondDecision Decision
	state, secondDecision, err = ApplyWithReceipts(state, secondInput, index)
	if err != nil {
		t.Fatalf("second ApplyWithReceipts: %v", err)
	}
	if recordErr := index.Record(NewReceipt(secondInput, secondDecision)); recordErr != nil {
		t.Fatalf("record second receipt: %v", recordErr)
	}

	beforeHash := state.Hash()
	next, duplicate, err := applyWithSchemaVersion(
		state,
		firstInput,
		index,
		CurrentSchemaVersion+1,
		func(state State) (State, Decision) {
			t.Fatal("historical duplicate executed its transition")
			return state, Decision{}
		},
	)
	if err != nil {
		t.Fatalf("historical duplicate: %v", err)
	}
	if next.Hash() != beforeHash || next.NextStreamSequence() != state.NextStreamSequence() {
		t.Fatal("historical duplicate mutated state")
	}
	if !equalDecision(duplicate, firstDecision) {
		t.Fatalf("historical duplicate decision differs:\nrecorded:  %+v\nduplicate: %+v", firstDecision, duplicate)
	}
}

func TestReceiptIndexRejectsConflictingIdentity(t *testing.T) {
	index := NewMemoryReceiptIndex()
	input := testInput(t, 1)
	state, decision, err := ApplyWithReceipts(NewState(7), input, index)
	if err != nil {
		t.Fatalf("ApplyWithReceipts: %v", err)
	}
	if recordErr := index.Record(NewReceipt(input, decision)); recordErr != nil {
		t.Fatalf("Record: %v", recordErr)
	}

	conflict := testInput(t, 2)
	conflict.InputID = input.InputID
	next, _, err := ApplyWithReceipts(state, conflict, index)
	if !errors.Is(err, ErrInputConflict) {
		t.Fatalf("conflicting input error = %v, want ErrInputConflict", err)
	}
	if next.Ready() {
		t.Fatal("conflicting committed identity left shard ready")
	}
}

func TestRepublishedBusinessInputAdvancesSequenceWithoutEconomicEffects(t *testing.T) {
	first := testInput(t, 1)
	state, originalDecision, err := Apply(NewState(7), first)
	if err != nil {
		t.Fatalf("first Apply: %v", err)
	}
	original := NewReceipt(first, originalDecision)
	republished := first
	republished.StreamSequence = 2

	next, duplicateDecision, err := ApplyDuplicateDelivery(
		state,
		republished,
		original,
	)
	if err != nil {
		t.Fatalf("ApplyDuplicateDelivery: %v", err)
	}
	if next.NextStreamSequence() != 3 ||
		next.Hash() == state.Hash() ||
		duplicateDecision.DuplicateOfDecisionHash != originalDecision.DecisionHash {
		t.Fatalf(
			"duplicate delivery = next %d state %s decision %+v",
			next.NextStreamSequence(),
			next.Hash(),
			duplicateDecision,
		)
	}
	if len(duplicateDecision.LedgerChanges) != 0 ||
		len(duplicateDecision.Fills) != 0 ||
		len(duplicateDecision.Events) != 0 ||
		len(duplicateDecision.BalanceChanges) != 0 {
		t.Fatalf("duplicate delivery emitted effects: %+v", duplicateDecision)
	}
}

func TestDecisionHashBindsPreviousStateAndCanonicalEffects(t *testing.T) {
	input := testInput(t, 1)
	left := NewState(7)
	right := left
	right.hash[0] ^= 0xff

	leftNext, leftDecision, err := apply(
		left,
		input,
		func(state State) (State, Decision) {
			return state, Decision{CommandResult: CommandResult{Status: CommandStatusAccepted}}
		},
	)
	if err != nil {
		t.Fatalf("left apply: %v", err)
	}
	rightNext, rightDecision, err := apply(
		right,
		input,
		func(state State) (State, Decision) {
			return state, Decision{CommandResult: CommandResult{Status: CommandStatusAccepted}}
		},
	)
	if err != nil {
		t.Fatalf("right apply: %v", err)
	}

	if leftDecision.PreviousStateHash != left.Hash() ||
		rightDecision.PreviousStateHash != right.Hash() {
		t.Fatal("decision did not retain its previous state hash")
	}
	if leftDecision.EffectsHash.IsZero() || rightDecision.EffectsHash.IsZero() {
		t.Fatal("decision effects hash is zero")
	}
	if leftDecision.DecisionHash == rightDecision.DecisionHash {
		t.Fatal("different previous states produced the same decision hash")
	}
	if leftNext.Hash() == rightNext.Hash() {
		t.Fatal("different previous states produced the same next state hash")
	}
}

func TestErrorIsHandlesTypedNilTarget(t *testing.T) {
	engineError := &Error{Kind: ErrSequenceGap}
	var target *Error

	if errors.Is(engineError, target) {
		t.Fatal("errors.Is matched a typed-nil target")
	}
}

func TestCanonicalJSONPayloadHasStableOrderingAndDefensiveBytes(t *testing.T) {
	left, err := NewCanonicalJSONPayload(map[string]any{
		"side":     "buy",
		"quantity": "1",
	})
	if err != nil {
		t.Fatalf("left payload: %v", err)
	}
	right, err := NewCanonicalJSONPayload(map[string]any{
		"quantity": "1",
		"side":     "buy",
	})
	if err != nil {
		t.Fatalf("right payload: %v", err)
	}
	if !left.equal(right) {
		t.Fatalf("semantically equal payloads differ: %s != %s", left.Bytes(), right.Bytes())
	}

	returned := left.Bytes()
	returned[0] ^= 0xff
	if !left.equal(right) {
		t.Fatal("mutating returned bytes changed canonical payload")
	}
}

func TestKernelHashGoldenVectors(t *testing.T) {
	input := testInput(t, 1)
	state := NewState(7)
	accepted := func(state State) (State, Decision) {
		return state, Decision{CommandResult: CommandResult{Status: CommandStatusAccepted}}
	}
	v2Next, v2Decision, err := applyWithSchemaAndDecisionHashVersion(
		state,
		input,
		nil,
		CurrentSchemaVersion,
		accepted,
		2,
	)
	if err != nil {
		t.Fatalf("accepted apply v2: %v", err)
	}
	v3Next, v3Decision, err := applyWithSchemaAndDecisionHashVersion(
		state,
		input,
		nil,
		CurrentSchemaVersion,
		accepted,
		3,
	)
	if err != nil {
		t.Fatalf("accepted apply v3: %v", err)
	}
	v4Next, v4Decision, err := apply(state, input, accepted)
	if err != nil {
		t.Fatalf("accepted current apply: %v", err)
	}
	if v2Decision.DecisionHashVersion != 2 ||
		v3Decision.DecisionHashVersion != 3 ||
		v4Decision.DecisionHashVersion != CurrentDecisionHashVersion ||
		CurrentDecisionHashVersion != 4 {
		t.Fatalf(
			"decision hash versions = v2:%d v3:%d current:%d/%d",
			v2Decision.DecisionHashVersion,
			v3Decision.DecisionHashVersion,
			v4Decision.DecisionHashVersion,
			CurrentDecisionHashVersion,
		)
	}

	invalid := testInput(t, 1)
	invalid.SchemaVersion = CurrentSchemaVersion + 1
	halted, _, err := Apply(NewState(7), invalid)
	if !errors.Is(err, ErrUnknownSchema) {
		t.Fatalf("halted apply error = %v, want ErrUnknownSchema", err)
	}

	negativeTime := testInput(t, 1)
	negativeTime.LogicalTime = LogicalTime(-1)
	emptyPayload := testInput(t, 1)
	emptyPayload.Payload = CanonicalPayload{}
	binaryPayload := testInput(t, 1)
	binaryPayload.Payload = canonicalPayloadFromTrustedBytes([]byte{0x00, 0xff, 0x80, 0x01})

	vectors := []struct {
		name string
		got  Hash
		want string
	}{
		{name: "initial state", got: state.Hash(), want: "6685cbadc498da804da2b0f316b0b598ff43f501672c619c248330380e1496ab"},
		{name: "input", got: v4Decision.InputHash, want: "03c13213415db9db34fd61e86021074bf078552c47218ffa054313c6a86c1e2b"},
		{name: "effects v2", got: v2Decision.EffectsHash, want: "1a6d7a5957c2bc6f2f4fe77a1d184a6b5d8bf4420aa296ebdabf6756bfe48f75"},
		{name: "decision v2", got: v2Decision.DecisionHash, want: "cf0e84ad5edfcca4a891b0831b202c5ec93a68cfda62960fffd280cd89ed99b0"},
		{name: "accepted state v2", got: v2Next.Hash(), want: "456d87bb496db7c6e94a446e0dfacd617bc45281044a5d95f1ef6a9daf5c9b2d"},
		{name: "effects v3", got: v3Decision.EffectsHash, want: "1a6d7a5957c2bc6f2f4fe77a1d184a6b5d8bf4420aa296ebdabf6756bfe48f75"},
		{name: "decision v3", got: v3Decision.DecisionHash, want: "8f084ec5428516d7a54f4074eb8957dd0d688d5b83b5a9ea49b09040d885db48"},
		{name: "accepted state v3", got: v3Next.Hash(), want: "fb8a2722d8b2fd39ebac53c055c7aaa86d67513d469c1d83bda9bcb9ce425294"},
		{name: "effects v4", got: v4Decision.EffectsHash, want: "3eebc4d2e3b833babcde727c0c7627a4bec572f35a27d7558bf8863700231996"},
		{name: "decision v4", got: v4Decision.DecisionHash, want: "b0b2bf5a34e50943cc3a3dfbf100698a977ff91782b25ad896d5d529bb7ff7f1"},
		{name: "accepted state v4", got: v4Next.Hash(), want: "5aef9288325b3743164e7ddfdc6afffebc5ee104fc44639724c123f1238efb9a"},
		{name: "halted state", got: halted.Hash(), want: "cd748e998b0582ad13540b96e0bb5682faa9f10b636ef7e2c3c15ea384771565"},
		{name: "negative logical time", got: hashInput(negativeTime), want: "c803b3138c8db07c468b249391405f394d1942207eecaebbfa1e621a52b2fa26"},
		{name: "empty payload", got: hashInput(emptyPayload), want: "59837e1266e78dd508ae139143de7e4f23bb8d848698982be96131910a40b54b"},
		{name: "binary payload", got: hashInput(binaryPayload), want: "9f6a04c2cf393ef911e59522cb37070f6891c3dcda06ea040a07647f74408bd2"},
	}
	for _, vector := range vectors {
		t.Run(vector.name, func(t *testing.T) {
			if got := vector.got.String(); got != vector.want {
				t.Fatalf("hash = %s, want %s", got, vector.want)
			}
		})
	}
}
