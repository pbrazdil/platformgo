package engine

import (
	"crypto/sha256"
	"encoding/binary"
	"hash"
)

func hashInput(input InputEnvelope) Hash {
	return finishHash(func(hasher hash.Hash) {
		writeString(hasher, "platformgo.engine.input.v1")
		writeBytes(hasher, input.InputID[:])
		writeUint32(hasher, input.SchemaVersion)
		writeUint32(hasher, uint32(input.ShardID))
		writeUint8(hasher, uint8(input.Kind))
		writeString(hasher, input.SourceID)
		writeUint64(hasher, input.SourceSequence)
		writeUint64(hasher, input.StreamSequence)
		writeUint64(hasher, input.MarketSequence)
		writeInt64(hasher, input.LogicalTime.UnixNano())
		writeUint64(hasher, input.ConfigurationVersion)
		writeUint64(hasher, input.InstrumentVersion)
		writeBytes(hasher, input.Payload)
	})
}

func hashDecision(input InputEnvelope, inputHash Hash) Hash {
	return finishHash(func(hasher hash.Hash) {
		writeString(hasher, "platformgo.engine.decision.v1")
		writeBytes(hasher, input.InputID[:])
		writeUint64(hasher, input.SourceSequence)
		writeUint64(hasher, input.StreamSequence)
		writeUint64(hasher, input.MarketSequence)
		writeInt64(hasher, input.LogicalTime.UnixNano())
		writeUint64(hasher, input.ConfigurationVersion)
		writeUint64(hasher, input.InstrumentVersion)
		writeBytes(hasher, inputHash[:])
	})
}

func hashInitialState(shardID ShardID) Hash {
	return finishHash(func(hasher hash.Hash) {
		writeString(hasher, "platformgo.engine.state.v1")
		writeUint32(hasher, uint32(shardID))
		writeUint64(hasher, 1)
		writeUint8(hasher, 1)
	})
}

func hashAcceptedState(previous Hash, inputHash Hash, decisionHash Hash, nextSequence uint64) Hash {
	return finishHash(func(hasher hash.Hash) {
		writeString(hasher, "platformgo.engine.state.accepted.v1")
		writeBytes(hasher, previous[:])
		writeBytes(hasher, inputHash[:])
		writeBytes(hasher, decisionHash[:])
		writeUint64(hasher, nextSequence)
		writeUint8(hasher, 1)
	})
}

func hashHaltedState(previous Hash, inputHash Hash, engineError *Error) Hash {
	return finishHash(func(hasher hash.Hash) {
		writeString(hasher, "platformgo.engine.state.halted.v1")
		writeBytes(hasher, previous[:])
		writeBytes(hasher, inputHash[:])
		writeString(hasher, string(engineError.Kind))
		writeUint64(hasher, engineError.Sequence)
		writeString(hasher, engineError.Detail)
		writeUint8(hasher, 0)
	})
}

func finishHash(write func(hash.Hash)) Hash {
	hasher := sha256.New()
	write(hasher)
	var result Hash
	copy(result[:], hasher.Sum(nil))
	return result
}

func writeBytes(hasher hash.Hash, value []byte) {
	writeUint64(hasher, uint64(len(value)))
	_, _ = hasher.Write(value)
}

func writeString(hasher hash.Hash, value string) {
	writeBytes(hasher, []byte(value))
}

func writeUint8(hasher hash.Hash, value uint8) {
	_, _ = hasher.Write([]byte{value})
}

func writeUint32(hasher hash.Hash, value uint32) {
	var encoded [4]byte
	binary.BigEndian.PutUint32(encoded[:], value)
	_, _ = hasher.Write(encoded[:])
}

func writeUint64(hasher hash.Hash, value uint64) {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], value)
	_, _ = hasher.Write(encoded[:])
}

func writeInt64(hasher hash.Hash, value int64) {
	var encoded [binary.MaxVarintLen64]byte
	length := binary.PutVarint(encoded[:], value)
	writeBytes(hasher, encoded[:length])
}
