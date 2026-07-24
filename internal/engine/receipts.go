package engine

import "fmt"

// Receipt is the immutable processing proof retained by the durable
// coordinator. It includes the fingerprint version needed to verify a
// historical envelope after the current schema or hash implementation changes.
type Receipt struct {
	InputID           ID
	StreamSequence    uint64
	SchemaVersion     uint32
	InputHashVersion  uint32
	InputHash         Hash
	BusinessInputHash Hash
	Decision          Decision
}

// NewReceipt constructs the receipt for a successfully applied input.
func NewReceipt(input InputEnvelope, decision Decision) Receipt {
	return Receipt{
		InputID:           input.InputID,
		StreamSequence:    input.StreamSequence,
		SchemaVersion:     input.SchemaVersion,
		InputHashVersion:  decision.InputHashVersion,
		InputHash:         decision.InputHash,
		BusinessInputHash: hashBusinessInput(input),
		Decision:          cloneDecision(decision),
	}
}

func cloneReceipt(receipt Receipt) Receipt {
	receipt.Decision = cloneDecision(receipt.Decision)
	return receipt
}

// ReceiptLookup provides the committed identity index consulted before a new
// input is validated. PostgreSQL implements this boundary in durable execution.
type ReceiptLookup interface {
	LookupByInputID(ID) (Receipt, bool)
	LookupByStreamSequence(uint64) (Receipt, bool)
}

// ReceiptIndex is the writable in-memory implementation used by deterministic
// tests. Production records receipts in the same PostgreSQL transaction as the
// decision and checkpoint.
type ReceiptIndex interface {
	ReceiptLookup
	Record(Receipt) error
}

// MemoryReceiptIndex provides O(1) identity lookup without embedding processing
// history in economic State. It is owned by one serialized test processor.
type MemoryReceiptIndex struct {
	byInputID        map[ID]Receipt
	byStreamSequence map[uint64]Receipt
}

// NewMemoryReceiptIndex returns an empty single-owner receipt index.
func NewMemoryReceiptIndex() *MemoryReceiptIndex {
	return &MemoryReceiptIndex{
		byInputID:        make(map[ID]Receipt),
		byStreamSequence: make(map[uint64]Receipt),
	}
}

// LookupByInputID returns a defensive copy of a recorded receipt.
func (index *MemoryReceiptIndex) LookupByInputID(inputID ID) (Receipt, bool) {
	if index == nil {
		return Receipt{}, false
	}
	receipt, ok := index.byInputID[inputID]
	return cloneReceipt(receipt), ok
}

// LookupByStreamSequence returns a defensive copy of a recorded receipt.
func (index *MemoryReceiptIndex) LookupByStreamSequence(sequence uint64) (Receipt, bool) {
	if index == nil {
		return Receipt{}, false
	}
	receipt, ok := index.byStreamSequence[sequence]
	return cloneReceipt(receipt), ok
}

// Record inserts one receipt or accepts an exact idempotent re-record.
func (index *MemoryReceiptIndex) Record(receipt Receipt) error {
	if index == nil {
		return fmt.Errorf("record receipt: nil index")
	}
	if recorded, ok := index.byInputID[receipt.InputID]; ok {
		if sameReceiptIdentity(recorded, receipt) {
			return nil
		}
		return fmt.Errorf("record receipt: input ID already has a different receipt")
	}
	if recorded, ok := index.byStreamSequence[receipt.StreamSequence]; ok {
		if sameReceiptIdentity(recorded, receipt) {
			return nil
		}
		return fmt.Errorf("record receipt: stream sequence already has a different receipt")
	}
	cloned := cloneReceipt(receipt)
	index.byInputID[receipt.InputID] = cloned
	index.byStreamSequence[receipt.StreamSequence] = cloned
	return nil
}

func sameReceiptIdentity(left, right Receipt) bool {
	return left.InputID == right.InputID &&
		left.StreamSequence == right.StreamSequence &&
		left.SchemaVersion == right.SchemaVersion &&
		left.InputHashVersion == right.InputHashVersion &&
		left.InputHash == right.InputHash &&
		left.BusinessInputHash == right.BusinessInputHash &&
		left.Decision.DecisionHash == right.Decision.DecisionHash &&
		left.Decision.NextStateHash == right.Decision.NextStateHash
}
