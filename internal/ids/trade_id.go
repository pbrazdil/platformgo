package ids

import (
	"encoding/json"
	"fmt"
	"strings"
)

const maxTradeIDLength = 36

// TradeID is the trade match identifier assigned by a venue or counterparty.
type TradeID string

func ParseTradeID(value string) (TradeID, error) {
	if value == "" {
		return "", tradeIDValidationError("String is empty")
	}
	if len(value) > maxTradeIDLength {
		return "", tradeIDValidationError(fmt.Sprintf(
			"String exceeds maximum length of %d characters, was %d",
			maxTradeIDLength,
			len(value),
		))
	}
	if err := validASCII(value); err != nil {
		switch validationKind(err) {
		case "non_ascii_string":
			return "", tradeIDValidationError("String contains non-ASCII character")
		case "whitespace_string":
			return "", tradeIDValidationError("String contains only whitespace")
		default:
			return "", err
		}
	}
	if strings.IndexByte(value, 0) >= 0 {
		return "", tradeIDValidationError("String contains interior NUL byte")
	}
	return TradeID(value), nil
}

func MustTradeID(value string) TradeID {
	id, err := ParseTradeID(value)
	panicOnError(err)
	return id
}

// ParseTradeIDBytes accepts an optional trailing C NUL terminator.
func ParseTradeIDBytes(value []byte) (TradeID, error) {
	if len(value) > 0 && value[len(value)-1] == 0 {
		value = value[:len(value)-1]
	}
	return ParseTradeID(string(value))
}

func MustTradeIDBytes(value []byte) TradeID {
	id, err := ParseTradeIDBytes(value)
	panicOnError(err)
	return id
}

func (id TradeID) String() string { return string(id) }

func (id TradeID) DebugString() string {
	return fmt.Sprintf("TradeId('%s')", id)
}

// CBytes returns a fresh NUL-terminated representation for C APIs.
func (id TradeID) CBytes() []byte {
	return append([]byte(id), 0)
}

func (id TradeID) MarshalJSON() ([]byte, error) {
	return marshalJSONString(string(id))
}

func (id *TradeID) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	parsed, err := ParseTradeID(value)
	if err != nil {
		return err
	}
	*id = parsed
	return nil
}

func tradeIDValidationError(message string) error {
	return &ValidationError{Kind: "predicate_violation", Message: message}
}
