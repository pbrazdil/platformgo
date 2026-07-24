package engine

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"time"
)

const CurrentSchemaVersion uint32 = 1

// IDError is a stable canonical-ID validation error.
type IDError string

const ErrInvalidID IDError = "invalid canonical ID"

func (idError IDError) Error() string {
	return string(idError)
}

// ID is the canonical 128-bit identity used by deterministic engine inputs.
type ID [16]byte

// ParseID accepts only lowercase, hyphenated canonical UUID text.
func ParseID(input string) (ID, error) {
	var id ID
	if len(input) != 36 ||
		input[8] != '-' ||
		input[13] != '-' ||
		input[18] != '-' ||
		input[23] != '-' {
		return id, ErrInvalidID
	}

	const encodedLength = 32
	var encoded [encodedLength]byte
	copy(encoded[0:8], input[0:8])
	copy(encoded[8:12], input[9:13])
	copy(encoded[12:16], input[14:18])
	copy(encoded[16:20], input[19:23])
	copy(encoded[20:32], input[24:36])
	if _, err := hex.Decode(id[:], encoded[:]); err != nil {
		return ID{}, fmt.Errorf("%w: %q", ErrInvalidID, input)
	}
	if id.String() != input {
		return ID{}, fmt.Errorf("%w: %q", ErrInvalidID, input)
	}
	return id, nil
}

// IDFromSequence derives a stable RFC 4122 variant/version-4-shaped ID.
// Callers must assign one namespace per independently owned ID sequence.
func IDFromSequence(namespace ID, sequence uint64) ID {
	hasher := sha256.New()
	_, _ = hasher.Write([]byte("platformgo.engine.id.v1"))
	_, _ = hasher.Write(namespace[:])
	var encodedSequence [8]byte
	binary.BigEndian.PutUint64(encodedSequence[:], sequence)
	_, _ = hasher.Write(encodedSequence[:])

	sum := hasher.Sum(nil)
	var id ID
	copy(id[:], sum[:len(id)])
	id[6] = (id[6] & 0x0f) | 0x40
	id[8] = (id[8] & 0x3f) | 0x80
	return id
}

// IsZero reports whether the ID is the all-zero value.
func (id ID) IsZero() bool {
	return id == ID{}
}

// String returns lowercase, hyphenated canonical UUID text.
func (id ID) String() string {
	var encoded [36]byte
	hex.Encode(encoded[0:8], id[0:4])
	encoded[8] = '-'
	hex.Encode(encoded[9:13], id[4:6])
	encoded[13] = '-'
	hex.Encode(encoded[14:18], id[6:8])
	encoded[18] = '-'
	hex.Encode(encoded[19:23], id[8:10])
	encoded[23] = '-'
	hex.Encode(encoded[24:36], id[10:16])
	return string(encoded[:])
}

// LogicalTime is explicit engine time represented as Unix nanoseconds.
type LogicalTime int64

// NewLogicalTime converts a supplied time without consulting the wall clock.
func NewLogicalTime(value time.Time) LogicalTime {
	return LogicalTime(value.UnixNano())
}

// UnixNano returns the stored Unix nanosecond value.
func (logicalTime LogicalTime) UnixNano() int64 {
	return int64(logicalTime)
}

// String returns canonical UTC RFC 3339 text with necessary nanosecond precision.
func (logicalTime LogicalTime) String() string {
	return time.Unix(0, int64(logicalTime)).UTC().Format(time.RFC3339Nano)
}

// ShardID identifies the single-writer engine shard.
type ShardID uint32

// InputKind identifies the versioned payload family in an input envelope.
type InputKind uint8

const (
	InputKindCommand InputKind = iota + 1
	InputKindMarket
	InputKindTimer
	InputKindConfiguration
	InputKindControl
)

func (kind InputKind) valid() bool {
	return kind >= InputKindCommand && kind <= InputKindControl
}

type Hash [sha256.Size]byte

// IsZero reports whether the hash is the all-zero value.
func (hash Hash) IsZero() bool {
	return hash == Hash{}
}

// String returns lowercase canonical hexadecimal text.
func (hash Hash) String() string {
	return hex.EncodeToString(hash[:])
}

// InputEnvelope contains every external value that can affect an engine decision.
type InputEnvelope struct {
	InputID              ID
	SchemaVersion        uint32
	ShardID              ShardID
	Kind                 InputKind
	SourceID             string
	SourceSequence       uint64
	StreamSequence       uint64
	MarketSequence       uint64
	LogicalTime          LogicalTime
	ConfigurationVersion uint64
	InstrumentVersion    uint64
	Payload              []byte
}

// Decision records the deterministic result metadata and canonical hashes.
// Economic effect collections are added by Phase 1 vertical slices.
type Decision struct {
	InputID              ID
	SourceSequence       uint64
	StreamSequence       uint64
	MarketSequence       uint64
	LogicalTime          LogicalTime
	ConfigurationVersion uint64
	InstrumentVersion    uint64
	InputHash            Hash
	DecisionHash         Hash
	NextStateHash        Hash
	CommandResult        CommandResult
	InstrumentChanges    []InstrumentSnapshot
	AccountChanges       []AccountSnapshot
	BookChanges          []BookSnapshot
	OrderChanges         []OrderSnapshot
	Fills                []FillSnapshot
	PositionChanges      []PositionSnapshot
	Events               []DomainEvent
}
