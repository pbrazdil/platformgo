package engine

import (
	"fmt"
	"sort"

	decimal "github.com/upcomers-org/platformgo/internal/decimal/economic"
)

type State struct {
	shardID            ShardID
	nextStreamSequence uint64
	ready              bool
	hash               Hash
	lastReceipt        Receipt
	hasLastReceipt     bool
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

// ApplyWithReceipts consults committed receipt identity before treating the
// envelope as new. Durable orchestration supplies a PostgreSQL-backed lookup;
// model tests use MemoryReceiptIndex.
func ApplyWithReceipts(
	state State,
	input InputEnvelope,
	receipts ReceiptLookup,
) (State, Decision, error) {
	return applyWithReceipts(state, input, receipts, func(state State) (State, Decision) {
		return state, Decision{}
	})
}

// ApplyDuplicateDelivery records a later JetStream sequence for an already
// committed identical business input. It advances only the audit/state chain;
// every economic effect collection remains empty.
func ApplyDuplicateDelivery(
	state State,
	input InputEnvelope,
	original Receipt,
) (State, Decision, error) {
	inputHash := hashInput(input)
	if !state.ready {
		return state, Decision{}, &Error{
			Kind:     ErrShardNotReady,
			Sequence: input.StreamSequence,
			Detail:   "the shard has recorded a fatal input error",
		}
	}
	businessHash, businessHashErr := businessInputHashAtVersion(
		input,
		original.BusinessHashVersion,
	)
	if businessHashErr != nil {
		return halt(state, inputHash, businessHashErr)
	}
	if input.InputID != original.InputID ||
		businessHash != original.BusinessInputHash {
		return halt(state, inputHash, &Error{
			Kind:     ErrInputConflict,
			Sequence: input.StreamSequence,
			Detail:   "re-published input differs from its committed business identity",
		})
	}
	if input.StreamSequence != state.nextStreamSequence {
		kind := ErrSequenceGap
		if input.StreamSequence < state.nextStreamSequence {
			kind = ErrSequenceRegression
		}
		return halt(state, inputHash, &Error{
			Kind:     kind,
			Sequence: input.StreamSequence,
			Detail:   fmt.Sprintf("next expected sequence is %d", state.nextStreamSequence),
		})
	}
	if state.nextStreamSequence == ^uint64(0) {
		return halt(state, inputHash, &Error{
			Kind:     ErrSequenceExhausted,
			Sequence: input.StreamSequence,
			Detail:   "stream sequence cannot advance without overflow",
		})
	}

	previousStateHash := state.hash
	decision := Decision{
		InputID:                 input.InputID,
		SourceSequence:          input.SourceSequence,
		StreamSequence:          input.StreamSequence,
		MarketSequence:          input.MarketSequence,
		LogicalTime:             input.LogicalTime,
		ConfigurationVersion:    input.ConfigurationVersion,
		InstrumentVersion:       input.InstrumentVersion,
		InputHashVersion:        CurrentInputHashVersion,
		DecisionHashVersion:     CurrentDecisionHashVersion,
		PreviousStateHash:       previousStateHash,
		InputHash:               inputHash,
		DuplicateOfDecisionHash: original.Decision.DecisionHash,
		CommandResult:           original.Decision.CommandResult,
	}
	decision.EffectsHash = hashEffects(decision)
	decision.DecisionHash = hashDecision(
		previousStateHash,
		inputHash,
		decision.EffectsHash,
	)
	nextSequence := state.nextStreamSequence + 1
	decision.NextStateHash = hashAcceptedState(
		previousStateHash,
		inputHash,
		decision.DecisionHash,
		nextSequence,
	)
	state.nextStreamSequence = nextSequence
	state.hash = decision.NextStateHash
	state.hasLastReceipt = false
	return state, cloneDecision(decision), nil
}

type transition func(State) (State, Decision)

func apply(state State, input InputEnvelope, transition transition) (State, Decision, error) {
	return applyWithReceipts(state, input, nil, transition)
}

func applyWithReceipts(
	state State,
	input InputEnvelope,
	receipts ReceiptLookup,
	transition transition,
) (State, Decision, error) {
	return applyWithSchemaVersion(
		state,
		input,
		receipts,
		CurrentSchemaVersion,
		transition,
	)
}

func applyWithSchemaVersion(
	state State,
	input InputEnvelope,
	receipts ReceiptLookup,
	currentSchemaVersion uint32,
	transition transition,
) (State, Decision, error) {
	recorded, found, conflict := lookupReceipt(state, receipts, input)
	if conflict != nil {
		if !state.ready {
			return state, Decision{}, &Error{
				Kind:     ErrShardNotReady,
				Sequence: input.StreamSequence,
				Detail:   "the shard has recorded a fatal input error",
			}
		}
		inputHash := hashInput(input)
		return halt(state, inputHash, conflict)
	}
	if found {
		inputHash, engineError := hashInputAtVersion(input, recorded.InputHashVersion)
		if engineError != nil {
			if !state.ready {
				return state, Decision{}, engineError
			}
			return halt(state, hashInput(input), engineError)
		}
		if inputHash != recorded.InputHash ||
			recorded.InputID != input.InputID ||
			recorded.StreamSequence != input.StreamSequence {
			if !state.ready {
				return state, Decision{}, &Error{
					Kind:     ErrShardNotReady,
					Sequence: input.StreamSequence,
					Detail:   "the shard has recorded a fatal input error",
				}
			}
			return halt(state, inputHash, &Error{
				Kind:     ErrInputConflict,
				Sequence: input.StreamSequence,
				Detail:   "committed input identity was reused with different content",
			})
		}
		return state, cloneDecision(recorded.Decision), nil
	}

	if !state.ready {
		return state, Decision{}, &Error{
			Kind:     ErrShardNotReady,
			Sequence: input.StreamSequence,
			Detail:   "the shard has recorded a fatal input error",
		}
	}

	inputHash := hashInput(input)
	if engineError := validateEnvelope(state, input, currentSchemaVersion); engineError != nil {
		return halt(state, inputHash, engineError)
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

	previousState := state
	previousStateHash := state.hash
	state, decision := transition(state)
	ledgerChanges, ledgerError := deriveLedgerChanges(
		previousState,
		input,
		decision.BalanceChanges,
	)
	if ledgerError != nil {
		return halt(previousState, inputHash, ledgerError)
	}
	decision.LedgerChanges = ledgerChanges
	decision.InputID = input.InputID
	decision.SourceSequence = input.SourceSequence
	decision.StreamSequence = input.StreamSequence
	decision.MarketSequence = input.MarketSequence
	decision.LogicalTime = input.LogicalTime
	decision.ConfigurationVersion = input.ConfigurationVersion
	decision.InstrumentVersion = input.InstrumentVersion
	decision.InputHashVersion = CurrentInputHashVersion
	decision.DecisionHashVersion = CurrentDecisionHashVersion
	decision.PreviousStateHash = previousStateHash
	decision.InputHash = inputHash
	decision.EffectsHash = hashEffects(decision)
	decisionHash := hashDecision(previousStateHash, inputHash, decision.EffectsHash)
	nextSequence := state.nextStreamSequence + 1
	nextStateHash := hashAcceptedState(previousStateHash, inputHash, decisionHash, nextSequence)
	decision.DecisionHash = decisionHash
	decision.NextStateHash = nextStateHash

	state.nextStreamSequence = nextSequence
	state.hash = nextStateHash
	state.lastReceipt = NewReceipt(input, decision)
	state.hasLastReceipt = true
	return state, cloneDecision(decision), nil
}

func lookupReceipt(
	state State,
	receipts ReceiptLookup,
	input InputEnvelope,
) (Receipt, bool, *Error) {
	var byInputID Receipt
	var hasInputID bool
	var bySequence Receipt
	var hasSequence bool

	if state.hasLastReceipt {
		if state.lastReceipt.InputID == input.InputID {
			byInputID, hasInputID = cloneReceipt(state.lastReceipt), true
		}
		if state.lastReceipt.StreamSequence == input.StreamSequence {
			bySequence, hasSequence = cloneReceipt(state.lastReceipt), true
		}
	}
	if receipts != nil {
		if recorded, ok := receipts.LookupByInputID(input.InputID); ok {
			byInputID, hasInputID = recorded, true
		}
		if recorded, ok := receipts.LookupByStreamSequence(input.StreamSequence); ok {
			bySequence, hasSequence = recorded, true
		}
	}

	switch {
	case hasInputID && byInputID.StreamSequence != input.StreamSequence:
		return Receipt{}, false, &Error{
			Kind:     ErrInputConflict,
			Sequence: input.StreamSequence,
			Detail:   "input ID was already committed at a different stream sequence",
		}
	case hasSequence && bySequence.InputID != input.InputID:
		return Receipt{}, false, &Error{
			Kind:     ErrInputConflict,
			Sequence: input.StreamSequence,
			Detail:   "stream sequence was already committed with a different input ID",
		}
	case hasInputID && hasSequence && !sameReceiptIdentity(byInputID, bySequence):
		return Receipt{}, false, &Error{
			Kind:     ErrInputConflict,
			Sequence: input.StreamSequence,
			Detail:   "committed receipt indexes disagree",
		}
	case hasInputID:
		return cloneReceipt(byInputID), true, nil
	case hasSequence:
		return cloneReceipt(bySequence), true, nil
	default:
		return Receipt{}, false, nil
	}
}

func validateEnvelope(
	state State,
	input InputEnvelope,
	currentSchemaVersion uint32,
) *Error {
	switch {
	case input.SchemaVersion != currentSchemaVersion:
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
	decision.AccountChanges = append([]AccountSnapshot(nil), decision.AccountChanges...)
	decision.RiskChanges = append([]RiskSnapshot(nil), decision.RiskChanges...)
	decision.BalanceChanges = append([]BalanceSnapshot(nil), decision.BalanceChanges...)
	decision.LedgerChanges = cloneLedgerTransactions(decision.LedgerChanges)
	decision.FundingChanges = append([]FundingSnapshot(nil), decision.FundingChanges...)
	decision.BookChanges = cloneBookSnapshots(decision.BookChanges)
	decision.OrderChanges = append([]OrderSnapshot(nil), decision.OrderChanges...)
	decision.Fills = append([]FillSnapshot(nil), decision.Fills...)
	decision.PositionChanges = append([]PositionSnapshot(nil), decision.PositionChanges...)
	decision.Events = append([]DomainEvent(nil), decision.Events...)
	return decision
}

func deriveLedgerChanges(
	previous State,
	input InputEnvelope,
	changes []BalanceSnapshot,
) ([]LedgerTransactionSnapshot, *Error) {
	type balanceKey struct {
		accountID string
		currency  string
	}
	final := make(map[balanceKey]BalanceSnapshot, len(changes))
	for _, change := range changes {
		final[balanceKey{
			accountID: change.AccountID,
			currency:  change.Currency,
		}] = change
	}
	keys := make([]balanceKey, 0, len(final))
	for key := range final {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(left, right int) bool {
		if keys[left].accountID != keys[right].accountID {
			return keys[left].accountID < keys[right].accountID
		}
		return keys[left].currency < keys[right].currency
	})

	transactions := make([]LedgerTransactionSnapshot, 0, len(keys))
	for _, key := range keys {
		nextTotal, err := decimal.Parse(final[key].Total)
		if err != nil {
			return nil, invalidLedgerEffect(input, "next balance total", err)
		}
		previousTotal := decimal.Decimal{}
		if snapshot, ok := previous.Balance(key.accountID, key.currency); ok {
			previousTotal, err = decimal.Parse(snapshot.Total)
			if err != nil {
				return nil, invalidLedgerEffect(input, "previous balance total", err)
			}
		}
		delta, err := nextTotal.Sub(previousTotal)
		if err != nil {
			return nil, invalidLedgerEffect(input, "balance delta", err)
		}
		if delta.IsZero() {
			continue
		}
		counterAmount, err := (decimal.Decimal{}).Sub(delta)
		if err != nil {
			return nil, invalidLedgerEffect(input, "clearing delta", err)
		}
		transactionID := IDFromSequence(input.InputID, uint64(len(transactions)+1))
		transactions = append(transactions, LedgerTransactionSnapshot{
			TransactionID: transactionID,
			BusinessKey: fmt.Sprintf(
				"balance:%s:%s:%s",
				input.InputID,
				key.accountID,
				key.currency,
			),
			InputID:     input.InputID,
			LogicalTime: input.LogicalTime,
			Entries: []LedgerEntrySnapshot{
				{
					EntryID:   IDFromSequence(transactionID, 1),
					AccountID: key.accountID,
					Currency:  key.currency,
					Amount:    delta.String(),
				},
				{
					EntryID:   IDFromSequence(transactionID, 2),
					AccountID: SystemClearingAccount,
					Currency:  key.currency,
					Amount:    counterAmount.String(),
				},
			},
		})
	}
	return transactions, nil
}

func invalidLedgerEffect(
	input InputEnvelope,
	field string,
	err error,
) *Error {
	return &Error{
		Kind:     ErrInvalidEffect,
		Sequence: input.StreamSequence,
		Detail:   fmt.Sprintf("%s is not exact canonical decimal: %v", field, err),
	}
}

func cloneLedgerTransactions(
	transactions []LedgerTransactionSnapshot,
) []LedgerTransactionSnapshot {
	cloned := make([]LedgerTransactionSnapshot, len(transactions))
	for index, transaction := range transactions {
		cloned[index] = transaction
		cloned[index].Entries = append(
			[]LedgerEntrySnapshot(nil),
			transaction.Entries...,
		)
	}
	return cloned
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
