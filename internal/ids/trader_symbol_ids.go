package ids

import (
	"encoding/json"
	"fmt"
	"strings"
)

// TraderID identifies a trader node by name and tag.
type TraderID string

func ParseTraderID(value string) (TraderID, error) {
	if err := validASCII(value); err != nil {
		return "", err
	}
	separator := strings.LastIndex(value, "-")
	if separator < 0 {
		return "", &ValidationError{
			Kind:    "missing_substring",
			Message: fmt.Sprintf("invalid string for 'value' did not contain '-', was '%s'", value),
		}
	}
	if value[:separator] == "" {
		return "", &ValidationError{
			Kind:    "predicate_violation",
			Message: "`value` name part (before '-') cannot be empty",
		}
	}
	if value[separator+1:] == "" {
		return "", &ValidationError{
			Kind:    "predicate_violation",
			Message: "`value` tag part (after '-') cannot be empty",
		}
	}
	return TraderID(value), nil
}

func MustTraderID(value string) TraderID {
	id, err := ParseTraderID(value)
	panicOnError(err)
	return id
}

func DefaultTraderID() TraderID  { return MustTraderID("TRADER-001") }
func ExternalTraderID() TraderID { return MustTraderID("EXTERNAL-0") }
func (id TraderID) String() string {
	return string(id)
}
func (id TraderID) Tag() string {
	separator := strings.LastIndex(string(id), "-")
	if separator < 0 {
		panic("TraderID contains '-'")
	}
	return string(id)[separator+1:]
}
func (id TraderID) IsExternal() bool { return id == "EXTERNAL-0" }
func (id TraderID) MarshalJSON() ([]byte, error) {
	return marshalJSONString(string(id))
}
func (id *TraderID) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	parsed, err := ParseTraderID(value)
	if err != nil {
		return err
	}
	*id = parsed
	return nil
}

// Symbol identifies a tradable instrument ticker and permits non-ASCII UTF-8.
type Symbol string

func ParseSymbol(value string) (Symbol, error) {
	if err := validUTF8(value); err != nil {
		return "", err
	}
	return Symbol(value), nil
}

func MustSymbol(value string) Symbol {
	symbol, err := ParseSymbol(value)
	panicOnError(err)
	return symbol
}

func (symbol Symbol) String() string { return string(symbol) }
func (symbol Symbol) IsComposite() bool {
	return strings.Contains(string(symbol), ".")
}
func (symbol Symbol) Root() string {
	value := string(symbol)
	if separator := strings.Index(value, "."); separator >= 0 {
		return value[:separator]
	}
	return value
}
func (symbol Symbol) Topic() string {
	root := symbol.Root()
	if root == string(symbol) {
		return root
	}
	return root + "*"
}
func (symbol Symbol) MarshalJSON() ([]byte, error) {
	return marshalJSONString(string(symbol))
}
func (symbol *Symbol) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	parsed, err := ParseSymbol(value)
	if err != nil {
		return err
	}
	*symbol = parsed
	return nil
}
