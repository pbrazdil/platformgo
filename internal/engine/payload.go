package engine

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// CanonicalPayload is the immutable byte representation committed by an input
// hash. Its bytes can only be created through a canonical encoder inside this
// package.
type CanonicalPayload struct {
	value []byte
}

// NewCanonicalJSONPayload encodes a typed value with Go's deterministic JSON
// field ordering and returns an immutable payload.
func NewCanonicalJSONPayload(value any) (CanonicalPayload, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return CanonicalPayload{}, fmt.Errorf("encode canonical JSON payload: %w", err)
	}
	return canonicalPayloadFromTrustedBytes(encoded), nil
}

// Bytes returns a defensive copy for persistence or transport.
func (payload CanonicalPayload) Bytes() []byte {
	return append([]byte(nil), payload.value...)
}

func canonicalPayloadFromTrustedBytes(value []byte) CanonicalPayload {
	return CanonicalPayload{value: append([]byte(nil), value...)}
}

func (payload CanonicalPayload) equal(other CanonicalPayload) bool {
	return bytes.Equal(payload.value, other.value)
}
