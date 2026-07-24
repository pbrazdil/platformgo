package ids

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math/bits"
	"strings"
)

const GenericSpreadIDSeparator = "___"

type InstrumentClass string

const (
	InstrumentClassFuture InstrumentClass = "FUTURE"
	InstrumentClassOption InstrumentClass = "OPTION"
)

// InstrumentID combines a UTF-8 symbol and an ASCII venue.
type InstrumentID struct {
	Symbol string
	Venue  string
}

type InstrumentIDError struct {
	Kind   string
	Value  string
	Source error
}

func (e *InstrumentIDError) Error() string {
	switch e.Kind {
	case "missing_separator":
		return fmt.Sprintf("invalid `InstrumentId` value '%s': missing '.' separator between symbol and venue components", e.Value)
	case "invalid_symbol":
		return fmt.Sprintf("invalid `InstrumentId` value '%s': invalid symbol: %v", e.Value, e.Source)
	case "invalid_venue":
		return fmt.Sprintf("invalid `InstrumentId` value '%s': invalid venue: %v", e.Value, e.Source)
	case "invalid_address":
		return fmt.Sprintf("invalid `InstrumentId` value '%s': invalid blockchain address: %v", e.Value, e.Source)
	default:
		return fmt.Sprintf("invalid `InstrumentId` value '%s'", e.Value)
	}
}

func (e *InstrumentIDError) Unwrap() error { return e.Source }

func NewInstrumentID(symbol, venue string) (InstrumentID, error) {
	return ParseInstrumentID(symbol + "." + venue)
}

func ParseInstrumentID(value string) (InstrumentID, error) {
	index := strings.LastIndex(value, ".")
	if index < 0 {
		return InstrumentID{}, &InstrumentIDError{Kind: "missing_separator", Value: value}
	}
	symbol, venue := value[:index], value[index+1:]
	if err := validASCII(venue); err != nil {
		return InstrumentID{}, &InstrumentIDError{
			Kind: "invalid_venue", Value: value, Source: err,
		}
	}
	if strings.Contains(venue, ":") {
		if err := validateDEXVenue(venue); err != nil {
			return InstrumentID{}, &InstrumentIDError{
				Kind: "invalid_venue", Value: value, Source: err,
			}
		}
		if err := validateBlockchainAddress(symbol); err != nil {
			return InstrumentID{}, &InstrumentIDError{
				Kind: "invalid_address", Value: value, Source: err,
			}
		}
	}
	if err := validUTF8(symbol); err != nil {
		return InstrumentID{}, &InstrumentIDError{
			Kind: "invalid_symbol", Value: value, Source: err,
		}
	}
	return InstrumentID{Symbol: symbol, Venue: venue}, nil
}

func MustInstrumentID(value string) InstrumentID {
	id, err := ParseInstrumentID(value)
	panicOnError(err)
	return id
}

func (id InstrumentID) String() string { return id.Symbol + "." + id.Venue }
func (id InstrumentID) IsSynthetic() bool {
	return id.Venue == "SYNTH"
}

func (id InstrumentID) ParentComponents() (string, InstrumentClass, bool) {
	root, suffix, ok := splitFirst(id.Symbol, ".")
	if !ok || root == "" || strings.Contains(suffix, ".") {
		return "", "", false
	}
	switch suffix {
	case "FUT", "FUTURE":
		return root, InstrumentClassFuture, true
	case "OPT", "OPTION":
		return root, InstrumentClassOption, true
	default:
		return "", "", false
	}
}

func (id InstrumentID) Blockchain() (string, bool) {
	chain, _, ok := splitFirst(id.Venue, ":")
	if !ok {
		return "", false
	}
	return chain, true
}

func (id InstrumentID) MarshalJSON() ([]byte, error) { return marshalJSONString(id.String()) }
func (id *InstrumentID) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	parsed, err := ParseInstrumentID(value)
	if err != nil {
		return err
	}
	*id = parsed
	return nil
}

func validateDEXVenue(venue string) error {
	if err := ValidateBlockchainVenue(venue); err != nil {
		return fmt.Errorf("Error creating `Venue` from '%s': %w", venue, err)
	}
	return nil
}

func validateBlockchainAddress(address string) error {
	if !strings.HasPrefix(address, "0x") {
		return fmt.Errorf("Ethereum address must start with '0x': %s", address)
	}
	if len(address) != 42 {
		return fmt.Errorf("Blockchain address '%s' is incorrect", address)
	}
	for index, r := range address[2:] {
		if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
			return fmt.Errorf("invalid character '%c' at position %d", r, index)
		}
	}
	if !validEthereumChecksum(address[2:]) {
		return fmt.Errorf("address '%s' has incorrect checksum", address)
	}
	return nil
}

// ValidateBlockchainAddress validates an EIP-55 checksummed Ethereum address.
func ValidateBlockchainAddress(address string) error {
	return validateBlockchainAddress(address)
}

func validEthereumChecksum(address string) bool {
	lower := strings.ToLower(address)
	hash := legacyKeccak256([]byte(lower))
	for index, char := range address {
		if char < 'a' || char > 'f' {
			if char < 'A' || char > 'F' {
				continue
			}
			if hash[index/2]>>(4*(1-index%2))&0xf < 8 {
				return false
			}
			continue
		}
		if hash[index/2]>>(4*(1-index%2))&0xf >= 8 {
			return false
		}
	}
	return true
}

// legacyKeccak256 implements the pre-FIPS Keccak padding used by EIP-55.
func legacyKeccak256(data []byte) [32]byte {
	const rate = 136
	var state [25]uint64
	for len(data) >= rate {
		for i := 0; i < rate/8; i++ {
			state[i] ^= binary.LittleEndian.Uint64(data[i*8:])
		}
		keccakF1600(&state)
		data = data[rate:]
	}
	var block [rate]byte
	copy(block[:], data)
	block[len(data)] = 0x01
	block[rate-1] |= 0x80
	for i := 0; i < rate/8; i++ {
		state[i] ^= binary.LittleEndian.Uint64(block[i*8:])
	}
	keccakF1600(&state)
	var result [32]byte
	for i := 0; i < len(result)/8; i++ {
		binary.LittleEndian.PutUint64(result[i*8:], state[i])
	}
	return result
}

func keccakF1600(state *[25]uint64) {
	roundConstants := [...]uint64{
		0x0000000000000001, 0x0000000000008082, 0x800000000000808a,
		0x8000000080008000, 0x000000000000808b, 0x0000000080000001,
		0x8000000080008081, 0x8000000000008009, 0x000000000000008a,
		0x0000000000000088, 0x0000000080008009, 0x000000008000000a,
		0x000000008000808b, 0x800000000000008b, 0x8000000000008089,
		0x8000000000008003, 0x8000000000008002, 0x8000000000000080,
		0x000000000000800a, 0x800000008000000a, 0x8000000080008081,
		0x8000000000008080, 0x0000000080000001, 0x8000000080008008,
	}
	rotation := [...]int{
		0, 1, 62, 28, 27,
		36, 44, 6, 55, 20,
		3, 10, 43, 25, 39,
		41, 45, 15, 21, 8,
		18, 2, 61, 56, 14,
	}
	for _, roundConstant := range roundConstants {
		var column [5]uint64
		for x := range column {
			column[x] = state[x] ^ state[x+5] ^ state[x+10] ^ state[x+15] ^ state[x+20]
		}
		var theta [5]uint64
		for x := range theta {
			theta[x] = column[(x+4)%5] ^ bits.RotateLeft64(column[(x+1)%5], 1)
		}
		for i := range state {
			state[i] ^= theta[i%5]
		}
		var rotated [25]uint64
		for x := 0; x < 5; x++ {
			for y := 0; y < 5; y++ {
				rotated[y%5+5*((2*x+3*y)%5)] = bits.RotateLeft64(state[x+5*y], rotation[x+5*y])
			}
		}
		for x := 0; x < 5; x++ {
			for y := 0; y < 5; y++ {
				state[x+5*y] = rotated[x+5*y] ^ (^rotated[(x+1)%5+5*y] & rotated[(x+2)%5+5*y])
			}
		}
		state[0] ^= roundConstant
	}
}
