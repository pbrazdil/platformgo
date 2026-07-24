package ids

import (
	"encoding/json"
	"fmt"
)

// AccountID combines an issuer and issuer-assigned account identifier.
type AccountID string

func ParseAccountID(value string) (AccountID, error) {
	if err := validASCII(value); err != nil {
		return "", err
	}
	issuer, account, ok := splitFirst(value, "-")
	if !ok {
		return "", &ValidationError{
			Kind:    "missing_substring",
			Message: fmt.Sprintf("invalid string for 'value' did not contain '-', was '%s'", value),
		}
	}
	if issuer == "" {
		return "", &ValidationError{
			Kind:    "predicate_violation",
			Message: "`value` issuer part (before '-') cannot be empty",
		}
	}
	if account == "" {
		return "", &ValidationError{
			Kind:    "predicate_violation",
			Message: "`value` account part (after '-') cannot be empty",
		}
	}
	return AccountID(value), nil
}

func MustAccountID(value string) AccountID {
	id, err := ParseAccountID(value)
	panicOnError(err)
	return id
}

func (id AccountID) String() string { return string(id) }
func (id AccountID) Issuer() string {
	issuer, _, ok := splitFirst(string(id), "-")
	if !ok {
		panic("AccountID contains '-'")
	}
	return issuer
}
func (id AccountID) IssuersID() string {
	_, account, ok := splitFirst(string(id), "-")
	if !ok {
		panic("AccountID contains '-'")
	}
	return account
}
func (id AccountID) MarshalJSON() ([]byte, error) { return marshalJSONString(string(id)) }
func (id *AccountID) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	parsed, err := ParseAccountID(value)
	if err != nil {
		return err
	}
	*id = parsed
	return nil
}
