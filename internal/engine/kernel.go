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
	trading            tradingState
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
	return apply(state, input, func(state State) (State, Decision) {
		return state, Decision{}
	})
}

type transition func(State) (State, Decision)

func apply(state State, input InputEnvelope, transition transition) (State, Decision, error) {
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
				return state, cloneDecision(recorded.decision), nil
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

	state, decision := transition(state)
	decision.InputID = input.InputID
	decision.SourceSequence = input.SourceSequence
	decision.StreamSequence = input.StreamSequence
	decision.MarketSequence = input.MarketSequence
	decision.LogicalTime = input.LogicalTime
	decision.ConfigurationVersion = input.ConfigurationVersion
	decision.InstrumentVersion = input.InstrumentVersion
	decision.InputHash = inputHash
	decisionHash := hashDecision(input, inputHash, decision)
	nextSequence := state.nextStreamSequence + 1
	nextStateHash := hashAcceptedState(state.hash, inputHash, decisionHash, nextSequence)
	decision.DecisionHash = decisionHash
	decision.NextStateHash = nextStateHash

	receipts := make([]receipt, len(state.receipts), len(state.receipts)+1)
	copy(receipts, state.receipts)
	receipts = append(receipts, receipt{
		inputID:   input.InputID,
		inputHash: inputHash,
		decision:  cloneDecision(decision),
	})
	state.nextStreamSequence = nextSequence
	state.hash = nextStateHash
	state.receipts = receipts
	return state, cloneDecision(decision), nil
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

func cloneDecision(decision Decision) Decision {
	decision.InstrumentChanges = append([]InstrumentSnapshot(nil), decision.InstrumentChanges...)
	decision.BookChanges = cloneBookSnapshots(decision.BookChanges)
	decision.OrderChanges = append([]OrderSnapshot(nil), decision.OrderChanges...)
	decision.Fills = append([]FillSnapshot(nil), decision.Fills...)
	decision.Events = append([]DomainEvent(nil), decision.Events...)
	return decision
}

func cloneBookSnapshots(books []BookSnapshot) []BookSnapshot {
	cloned := make([]BookSnapshot, len(books))
	for index, book := range books {
		cloned[index] = book
		cloned[index].Bids = append([]BookLevel(nil), book.Bids...)
		cloned[index].Asks = append([]BookLevel(nil), book.Asks...)
	}
	return cloned
}
