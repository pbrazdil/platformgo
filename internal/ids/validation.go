package ids

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ValidationError identifies the stable kind of identifier validation failure.
type ValidationError struct {
	Kind    string
	Message string
}

func (e *ValidationError) Error() string { return e.Message }

func validASCII(value string) error {
	if value == "" {
		return &ValidationError{Kind: "empty_string", Message: "invalid string for 'value', was empty"}
	}
	hasNonWhitespace := false
	for _, r := range value {
		if !unicode.IsSpace(r) {
			hasNonWhitespace = true
		}
		if r > unicode.MaxASCII {
			return &ValidationError{
				Kind:    "non_ascii_string",
				Message: fmt.Sprintf("invalid string for 'value' contained a non-ASCII char, was '%s'", value),
			}
		}
	}
	if !hasNonWhitespace {
		return &ValidationError{Kind: "whitespace_string", Message: "invalid string for 'value', was all whitespace"}
	}
	return nil
}

func validUTF8(value string) error {
	if value == "" {
		return &ValidationError{Kind: "empty_string", Message: "invalid string for 'value', was empty"}
	}
	if !utf8.ValidString(value) {
		return &ValidationError{Kind: "invalid_utf8", Message: "invalid UTF-8 string for 'value'"}
	}
	for _, r := range value {
		if !unicode.IsSpace(r) {
			return nil
		}
	}
	return &ValidationError{Kind: "whitespace_string", Message: "invalid string for 'value', was all whitespace"}
}

func panicOnError(err error) {
	if err != nil {
		panic("Condition failed: " + err.Error())
	}
}

func validationKind(err error) string {
	var validationErr *ValidationError
	if errors.As(err, &validationErr) {
		return validationErr.Kind
	}
	return ""
}

func splitFirst(value, separator string) (string, string, bool) {
	index := strings.Index(value, separator)
	if index < 0 {
		return "", "", false
	}
	return value[:index], value[index+len(separator):], true
}

func marshalJSONString(value string) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buffer.Bytes(), []byte{'\n'}), nil
}
