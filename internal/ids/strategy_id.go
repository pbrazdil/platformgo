package ids

import (
	"encoding/json"
	"fmt"
	"strings"
)

const externalStrategyID = "EXTERNAL"

// StrategyID identifies a strategy by name and order ID tag.
type StrategyID string

func ParseStrategyID(value string) (StrategyID, error) {
	if err := validASCII(value); err != nil {
		return "", err
	}
	if value == externalStrategyID {
		return StrategyID(value), nil
	}

	index := strings.LastIndex(value, "-")
	if index < 0 {
		return "", &ValidationError{
			Kind:    "missing_substring",
			Message: fmt.Sprintf("invalid string for 'value' did not contain '-', was '%s'", value),
		}
	}
	name, tag := value[:index], value[index+1:]
	if name == "" {
		return "", &ValidationError{
			Kind:    "predicate_violation",
			Message: "`value` name part (before '-') cannot be empty",
		}
	}
	if tag == "" {
		return "", &ValidationError{
			Kind:    "predicate_violation",
			Message: "`value` tag part (after '-') cannot be empty",
		}
	}
	return StrategyID(value), nil
}

func MustStrategyID(value string) StrategyID {
	id, err := ParseStrategyID(value)
	panicOnError(err)
	return id
}

func ExternalStrategyID() StrategyID { return MustStrategyID(externalStrategyID) }

func (id StrategyID) String() string   { return string(id) }
func (id StrategyID) IsExternal() bool { return id == externalStrategyID }
func (id StrategyID) Tag() string {
	if index := strings.LastIndex(string(id), "-"); index >= 0 {
		return string(id)[index+1:]
	}
	return string(id)
}

func (id *StrategyID) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	parsed, err := ParseStrategyID(value)
	if err != nil {
		return err
	}
	*id = parsed
	return nil
}

// NormalizeOrderIDTag filters unset order ID tag sentinel values.
func NormalizeOrderIDTag(orderIDTag *string) *string {
	if orderIDTag == nil || *orderIDTag == "" || *orderIDTag == "None" {
		return nil
	}
	return orderIDTag
}
