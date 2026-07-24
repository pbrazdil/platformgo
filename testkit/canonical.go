package testkit

import "github.com/upcomers-org/platformgo/internal/engine"

// DecisionHashes is the minimal canonical replay comparison for a decision.
type DecisionHashes struct {
	Previous engine.Hash
	Input    engine.Hash
	Effects  engine.Hash
	Decision engine.Hash
	Next     engine.Hash
}

// CanonicalDecisionHashes extracts the versioned audit hash chain.
func CanonicalDecisionHashes(decision engine.Decision) DecisionHashes {
	return DecisionHashes{
		Previous: decision.PreviousStateHash,
		Input:    decision.InputHash,
		Effects:  decision.EffectsHash,
		Decision: decision.DecisionHash,
		Next:     decision.NextStateHash,
	}
}
