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
	if err := index.Record(NewReceipt(firstInput, firstDecision)); err != nil {
		t.Fatalf("record first receipt: %v", err)
	}

	secondInput := testInput(t, 2)
	var secondDecision Decision
	state, secondDecision, err = ApplyWithReceipts(state, secondInput, index)
	if err != nil {
		t.Fatalf("second ApplyWithReceipts: %v", err)
	}
	if err := index.Record(NewReceipt(secondInput, secondDecision)); err != nil {
		t.Fatalf("record second receipt: %v", err)
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
	if err := index.Record(NewReceipt(input, decision)); err != nil {
		t.Fatalf("Record: %v", err)
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
