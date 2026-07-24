package ids

import (
	"encoding/json"
	"strings"
)

// ClientOrderID is an order identifier assigned by the client.
type ClientOrderID string

func ParseClientOrderID(value string) (ClientOrderID, error) {
	if err := validASCII(value); err != nil {
		return "", err
	}
	return ClientOrderID(value), nil
}

func MustClientOrderID(value string) ClientOrderID {
	id, err := ParseClientOrderID(value)
	panicOnError(err)
	return id
}

func ExternalClientOrderID() ClientOrderID { return MustClientOrderID("EXTERNAL") }
func (id ClientOrderID) IsExternal() bool  { return id == "EXTERNAL" }
func (id ClientOrderID) String() string    { return string(id) }

func (id ClientOrderID) MarshalJSON() ([]byte, error) { return marshalJSONString(string(id)) }
func (id *ClientOrderID) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	parsed, err := ParseClientOrderID(value)
	if err != nil {
		return err
	}
	*id = parsed
	return nil
}

// ParseClientOrderIDs converts a comma-delimited optional representation.
func ParseClientOrderIDs(value *string) []ClientOrderID {
	if value == nil {
		return nil
	}
	parts := strings.Split(*value, ",")
	ids := make([]ClientOrderID, len(parts))
	for i, part := range parts {
		ids[i] = MustClientOrderID(part)
	}
	return ids
}

// FormatClientOrderIDs converts an optional identifier slice to comma-delimited form.
func FormatClientOrderIDs(ids []ClientOrderID) *string {
	if ids == nil {
		return nil
	}
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = id.String()
	}
	value := strings.Join(parts, ",")
	return &value
}

// OrderListID identifies a related list of orders.
type OrderListID string

func ParseOrderListID(value string) (OrderListID, error) {
	if err := validASCII(value); err != nil {
		return "", err
	}
	return OrderListID(value), nil
}

func MustOrderListID(value string) OrderListID {
	id, err := ParseOrderListID(value)
	panicOnError(err)
	return id
}

func (id OrderListID) String() string { return string(id) }
func (id OrderListID) MarshalJSON() ([]byte, error) {
	return marshalJSONString(string(id))
}
func (id *OrderListID) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	parsed, err := ParseOrderListID(value)
	if err != nil {
		return err
	}
	*id = parsed
	return nil
}

// PositionID identifies a position. Position IDs permit non-ASCII UTF-8.
type PositionID string

func ParsePositionID(value string) (PositionID, error) {
	if err := validUTF8(value); err != nil {
		return "", err
	}
	return PositionID(value), nil
}

func MustPositionID(value string) PositionID {
	id, err := ParsePositionID(value)
	panicOnError(err)
	return id
}

func (id PositionID) String() string  { return string(id) }
func (id PositionID) IsVirtual() bool { return strings.HasPrefix(string(id), "P-") }
func (id PositionID) MarshalJSON() ([]byte, error) {
	return marshalJSONString(string(id))
}
func (id *PositionID) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	parsed, err := ParsePositionID(value)
	if err != nil {
		return err
	}
	*id = parsed
	return nil
}
