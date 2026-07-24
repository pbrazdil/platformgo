package defi

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/upcomers-org/platformgo/internal/ids"
)

type PoolIdentifierKind uint8

const (
	PoolAddress PoolIdentifierKind = iota + 1
	PoolID
)

type PoolIdentifier struct {
	kind  PoolIdentifierKind
	value string
}

func ParsePoolIdentifier(value string) (PoolIdentifier, error) {
	if !strings.HasPrefix(value, "0x") {
		return PoolIdentifier{}, fmt.Errorf("Pool identifier must start with '0x', was: %s", value)
	}
	switch len(value) {
	case 42:
		if err := validateHex(value[2:]); err != nil {
			return PoolIdentifier{}, fmt.Errorf("Invalid hex characters in: %s", value)
		}
		checksummed, err := ids.ChecksumBlockchainAddress(value)
		if err != nil {
			return PoolIdentifier{}, err
		}
		return PoolIdentifier{kind: PoolAddress, value: checksummed}, nil
	case 66:
		if err := validateHex(value[2:]); err != nil {
			return PoolIdentifier{}, fmt.Errorf("Invalid hex characters in: %s", value)
		}
		return PoolIdentifier{kind: PoolID, value: strings.ToLower(value)}, nil
	default:
		return PoolIdentifier{}, fmt.Errorf(
			"Pool identifier must be 42 chars (address) or 66 chars (pool ID), was %d chars: %s",
			len(value), value,
		)
	}
}

func MustPoolIdentifier(value string) PoolIdentifier {
	identifier, err := ParsePoolIdentifier(value)
	if err != nil {
		panic(err)
	}
	return identifier
}

func PoolIdentifierFromAddress(address string) (PoolIdentifier, error) {
	return ParsePoolIdentifier(address)
}

func PoolIdentifierFromBytes(value []byte) (PoolIdentifier, error) {
	if len(value) != 32 {
		return PoolIdentifier{}, fmt.Errorf("Pool ID must be 32 bytes, was %d", len(value))
	}
	return PoolIdentifier{kind: PoolID, value: "0x" + hex.EncodeToString(value)}, nil
}

func (p PoolIdentifier) IsAddress() bool { return p.kind == PoolAddress }
func (p PoolIdentifier) IsPoolID() bool  { return p.kind == PoolID }
func (p PoolIdentifier) String() string  { return p.value }

func (p PoolIdentifier) Address() (string, error) {
	if !p.IsAddress() {
		return "", errors.New("Cannot convert PoolId variant to Address")
	}
	return p.value, nil
}

func (p PoolIdentifier) Bytes() ([32]byte, error) {
	var result [32]byte
	if !p.IsPoolID() {
		return result, errors.New("Cannot convert Address variant to PoolId bytes")
	}
	decoded, err := hex.DecodeString(strings.TrimPrefix(p.value, "0x"))
	if err != nil {
		return result, fmt.Errorf("Failed to decode pool ID hex: %w", err)
	}
	copy(result[:], decoded)
	return result, nil
}

func (p PoolIdentifier) MarshalJSON() ([]byte, error) {
	return json.Marshal(p.value)
}

func (p *PoolIdentifier) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	parsed, err := ParsePoolIdentifier(value)
	if err != nil {
		return err
	}
	*p = parsed
	return nil
}

func validateHex(value string) error {
	for _, character := range value {
		if !strings.ContainsRune("0123456789abcdefABCDEF", character) {
			return errors.New("invalid hex")
		}
	}
	return nil
}
