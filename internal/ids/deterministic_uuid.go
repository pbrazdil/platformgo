package ids

import "fmt"

// DeterministicUUIDSequence produces reproducible RFC 4122 version-4-shaped IDs
// without process-global random state.
type DeterministicUUIDSequence struct {
	next uint64
}

func NewDeterministicUUIDSequence() *DeterministicUUIDSequence {
	return &DeterministicUUIDSequence{next: 1}
}

func (sequence *DeterministicUUIDSequence) Next() string {
	value := sequence.next
	sequence.next++
	return fmt.Sprintf("00000000-0000-4000-8000-%012x", value)
}
