package application

import (
	"errors"
	"time"

	"github.com/upcomers-org/platformgo/internal/edge"
	"github.com/upcomers-org/platformgo/internal/engine"
)

var (
	// ErrIdempotencyConflict means a key was reused for different request bytes.
	ErrIdempotencyConflict = errors.New("idempotency key request hash conflict")
	// ErrCommandNotFound means completion referenced no durable command.
	ErrCommandNotFound = errors.New("durable command not found")
	// ErrCommandCompletionConflict means a completed command was given a
	// different terminal result.
	ErrCommandCompletionConflict = errors.New("command completion conflict")
	// ErrCommandSequenceGap means a new command did not use the next contiguous
	// sequence for its account.
	ErrCommandSequenceGap = errors.New("command account sequence gap")
	// ErrAccountShardConflict means an account was routed to a shard other than
	// its immutable durable assignment.
	ErrAccountShardConflict = errors.New("account shard assignment conflict")
	// ErrEconomicRevisionChanged means admission observed a configuration or
	// instrument revision that changed before the command could commit.
	ErrEconomicRevisionChanged = errors.New("economic revision changed during admission")
	// ErrRuntimeNotReady means a new command reached the durable admission
	// boundary after command workers began draining.
	ErrRuntimeNotReady = errors.New("command runtime is not ready")
)

// IdempotencyState is the durable lifecycle of a request key.
type IdempotencyState string

const (
	IdempotencyInProgress IdempotencyState = "in_progress"
	IdempotencyCompleted  IdempotencyState = "completed"
)

// CommandStatus is the durable command outcome.
type CommandStatus string

const (
	CommandAccepted  CommandStatus = "accepted"
	CommandRejected  CommandStatus = "rejected"
	CommandCompleted CommandStatus = "completed"
)

// StoredResponse is the exact replayable HTTP response.
type StoredResponse = edge.StoredResponse

// AccountProvisioningIntent is non-economic identity/profile metadata bound to
// one ordered configure-account command.
type AccountProvisioningIntent struct {
	BrokerTenant     string
	UserID           string
	Login            int64
	BaseCurrency     string
	MarketVenue      string
	PermittedClasses []string
	CreatedAt        time.Time
}

// BeginCommandRequest contains one canonical command and its stable identity.
type BeginCommandRequest struct {
	Scope               string
	IdempotencyKey      string
	RequestHash         [32]byte
	CommandID           engine.ID
	OrderID             engine.ID
	IntentID            string
	AccountID           string
	AccountSequence     uint64
	CommandType         string
	SchemaVersion       uint32
	CanonicalPayload    []byte
	OutboxSubject       string
	OutboxPayload       []byte
	LogicalTime         time.Time
	ExpiresAt           time.Time
	Response            StoredResponse
	AccountProvisioning *AccountProvisioningIntent
	RequireRuntimeReady bool
}

// BeginCommandResult reports either a newly-created command or a replay.
type BeginCommandResult struct {
	Created   bool
	CommandID engine.ID
	State     IdempotencyState
	Response  StoredResponse
}

// CompleteCommandRequest atomically finalizes the command and replay record.
type CompleteCommandRequest struct {
	CommandID engine.ID
	Status    CommandStatus
	Result    []byte
	Response  StoredResponse
}
