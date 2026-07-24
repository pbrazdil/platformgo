package engine

import "fmt"

// ErrorKind classifies deterministic fail-closed engine errors.
type ErrorKind string

const (
	ErrInvalidEnvelope    ErrorKind = "invalid_envelope"
	ErrUnknownSchema      ErrorKind = "unknown_schema"
	ErrUnknownInputKind   ErrorKind = "unknown_input_kind"
	ErrShardMismatch      ErrorKind = "shard_mismatch"
	ErrPayloadMismatch    ErrorKind = "payload_mismatch"
	ErrSequenceGap        ErrorKind = "sequence_gap"
	ErrSequenceRegression ErrorKind = "sequence_regression"
	ErrSequenceExhausted  ErrorKind = "sequence_exhausted"
	ErrInputConflict      ErrorKind = "input_conflict"
	ErrUnknownHashVersion ErrorKind = "unknown_hash_version"
	ErrShardNotReady      ErrorKind = "shard_not_ready"
)

func (kind ErrorKind) Error() string {
	return string(kind)
}

// Error describes a typed deterministic rejection that callers can match by kind.
type Error struct {
	Kind     ErrorKind
	Sequence uint64
	Detail   string
}

func (engineError *Error) Error() string {
	if engineError.Detail == "" {
		return string(engineError.Kind)
	}
	if engineError.Sequence == 0 {
		return fmt.Sprintf("%s: %s", engineError.Kind, engineError.Detail)
	}
	return fmt.Sprintf("%s at stream sequence %d: %s", engineError.Kind, engineError.Sequence, engineError.Detail)
}

func (engineError *Error) Is(target error) bool {
	switch target := target.(type) {
	case ErrorKind:
		return engineError.Kind == target
	case *Error:
		return target != nil && engineError.Kind == target.Kind
	default:
		return false
	}
}
