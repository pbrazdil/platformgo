package testkit

import "github.com/upcomers-org/platformgo/internal/engine"

var fixtureNamespaceRoot = engine.ID{
	0x01, 0x9f, 0x94, 0x60, 0x4b, 0x36, 0x4e, 0x9b,
	0x8f, 0x44, 0x68, 0x26, 0x11, 0xf7, 0xee, 0x01,
}

// IDSequence owns a deterministic namespace and monotonically increasing
// sequence.
type IDSequence struct {
	namespace engine.ID
	next      uint64
}

// NewShardIDSequence derives a namespace unique to the shard.
func NewShardIDSequence(shardID engine.ShardID) *IDSequence {
	return &IDSequence{
		namespace: engine.IDFromSequence(fixtureNamespaceRoot, uint64(shardID)),
		next:      1,
	}
}

// Next returns the next deterministic ID.
func (sequence *IDSequence) Next() engine.ID {
	id := engine.IDFromSequence(sequence.namespace, sequence.next)
	sequence.next++
	return id
}
