package testkit

import (
	"testing"
	"time"

	"github.com/upcomers-org/platformgo/internal/engine"
)

func TestShardIDSequencesUseDistinctNamespaces(t *testing.T) {
	left := NewShardIDSequence(7)
	right := NewShardIDSequence(8)

	if left.Next() == right.Next() {
		t.Fatal("different shards produced the same first fixture ID")
	}
}

func TestEngineFixtureIsDeterministic(t *testing.T) {
	start := engine.NewLogicalTime(time.Date(2026, time.July, 24, 10, 0, 0, 0, time.UTC))
	left := NewEngineFixture(7, start)
	right := NewEngineFixture(7, start)
	action := engine.TradingAction{
		Kind: engine.TradingActionConfigureAccount,
		ConfigureAccount: &engine.ConfigureAccount{
			AccountID: "account-1",
			OmsMode:   engine.OmsModeNetting,
		},
	}

	leftDecision, err := left.ApplyTrading(action)
	if err != nil {
		t.Fatalf("left ApplyTrading: %v", err)
	}
	rightDecision, err := right.ApplyTrading(action)
	if err != nil {
		t.Fatalf("right ApplyTrading: %v", err)
	}
	if CanonicalDecisionHashes(leftDecision) != CanonicalDecisionHashes(rightDecision) {
		t.Fatal("identical fixture inputs produced different hashes")
	}
	if left.State().Hash() != right.State().Hash() {
		t.Fatal("identical fixtures produced different state hashes")
	}
}
