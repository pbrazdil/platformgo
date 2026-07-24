package engine

import "fmt"

type receipt struct {
	inputID   ID
	inputHash Hash
	decision  Decision
}

type State struct {
	shardID            ShardID
	nextStreamSequence uint64
	ready              bool
	hash               Hash
	receipts           []receipt
}

// NewState creates a ready shard state expecting stream sequence one.
func NewState(shardID ShardID) State {
	return State{
		shardID:            shardID,
		nextStreamSequence: 1,
		ready:              true,
		hash:               hashInitialState(shardID),
	}
}

// ShardID returns the shard owned by this state.
func (state State) ShardID() ShardID {
	return state.shardID
}

// NextStreamSequence returns the only sequence that can be newly applied.
func (state State) NextStreamSequence() uint64 {
	return state.nextStreamSequence
}

// Ready reports whether the shard may accept another input.
func (state State) Ready() bool {
	return state.ready
}

// Hash returns the canonical state-chain hash.
func (state State) Hash() Hash {
	return state.hash
}

// Apply deterministically returns a new state and recorded decision.
// The input state is never mutated. Fatal envelope or ordering errors return a
// halted state which the caller must durably record before acknowledging input.
func Apply(state State, input InputEnvelope) (State, Decision, error) {
	if !state.ready {
		return state, Decision{}, &Error{
			Kind:     ErrShardNotReady,
			Sequence: input.StreamSequence,
			Detail:   "the shard has recorded a fatal input error",
		}
	}

	inputHash := hashInput(input)
	if engineError := validateEnvelope(state, input); engineError != nil {
		return halt(state, inputHash, engineError)
	}

	for _, recorded := range state.receipts {
		if recorded.decision.StreamSequence == input.StreamSequence {
			if recorded.inputHash == inputHash {
				return state, recorded.decision, nil
			}
			return halt(state, inputHash, &Error{
				Kind:     ErrInputConflict,
				Sequence: input.StreamSequence,
				Detail:   "stream sequence was already committed with different input",
			})
		}
		if recorded.inputID == input.InputID {
			return halt(state, inputHash, &Error{
				Kind:     ErrInputConflict,
				Sequence: input.StreamSequence,
				Detail:   "input ID was already committed with different input",
			})
		}
	}

	switch {
	case input.StreamSequence < state.nextStreamSequence:
		return halt(state, inputHash, &Error{
			Kind:     ErrSequenceRegression,
			Sequence: input.StreamSequence,
			Detail:   fmt.Sprintf("next expected sequence is %d", state.nextStreamSequence),
		})
	case input.StreamSequence > state.nextStreamSequence:
		return halt(state, inputHash, &Error{
			Kind:     ErrSequenceGap,
			Sequence: input.StreamSequence,
			Detail:   fmt.Sprintf("next expected sequence is %d", state.nextStreamSequence),
		})
	case state.nextStreamSequence == ^uint64(0):
		return halt(state, inputHash, &Error{
			Kind:     ErrSequenceExhausted,
			Sequence: input.StreamSequence,
			Detail:   "stream sequence cannot advance without overflow",
		})
	}

	decisionHash := hashDecision(input, inputHash)
	nextSequence := state.nextStreamSequence + 1
	nextStateHash := hashAcceptedState(state.hash, inputHash, decisionHash, nextSequence)
	decision := Decision{
		InputID:              input.InputID,
		SourceSequence:       input.SourceSequence,
		StreamSequence:       input.StreamSequence,
		MarketSequence:       input.MarketSequence,
		LogicalTime:          input.LogicalTime,
		ConfigurationVersion: input.ConfigurationVersion,
		InstrumentVersion:    input.InstrumentVersion,
		InputHash:            inputHash,
		DecisionHash:         decisionHash,
		NextStateHash:        nextStateHash,
	}

	receipts := make([]receipt, len(state.receipts), len(state.receipts)+1)
	copy(receipts, state.receipts)
	receipts = append(receipts, receipt{
		inputID:   input.InputID,
		inputHash: inputHash,
		decision:  decision,
	})
	state.nextStreamSequence = nextSequence
	state.hash = nextStateHash
	state.receipts = receipts
	return state, decision, nil
}

func validateEnvelope(state State, input InputEnvelope) *Error {
	switch {
	case input.SchemaVersion != CurrentSchemaVersion:
		return &Error{
			Kind:     ErrUnknownSchema,
			Sequence: input.StreamSequence,
			Detail:   fmt.Sprintf("schema version %d is not supported", input.SchemaVersion),
		}
	case !input.Kind.valid():
		return &Error{
			Kind:     ErrUnknownInputKind,
			Sequence: input.StreamSequence,
			Detail:   fmt.Sprintf("input kind %d is not supported", input.Kind),
		}
	case input.ShardID != state.shardID:
		return &Error{
			Kind:     ErrShardMismatch,
			Sequence: input.StreamSequence,
			Detail:   fmt.Sprintf("input shard %d does not match state shard %d", input.ShardID, state.shardID),
		}
	case input.InputID.IsZero():
		return invalidEnvelope(input, "input ID is required")
	case input.StreamSequence == 0:
		return invalidEnvelope(input, "stream sequence is required")
	case input.SourceID == "":
		return invalidEnvelope(input, "source ID is required")
	case input.SourceSequence == 0:
		return invalidEnvelope(input, "source sequence is required")
	case input.ConfigurationVersion == 0:
		return invalidEnvelope(input, "configuration version is required")
	case input.InstrumentVersion == 0:
		return invalidEnvelope(input, "instrument version is required")
	default:
		return nil
	}
}

func invalidEnvelope(input InputEnvelope, detail string) *Error {
	return &Error{
		Kind:     ErrInvalidEnvelope,
		Sequence: input.StreamSequence,
		Detail:   detail,
	}
}

func halt(state State, inputHash Hash, engineError *Error) (State, Decision, error) {
	state.ready = false
	state.hash = hashHaltedState(state.hash, inputHash, engineError)
	return state, Decision{}, engineError
}
