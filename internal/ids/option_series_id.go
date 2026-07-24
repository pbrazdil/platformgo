package ids

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// OptionSeriesID identifies an option series by venue, underlying,
// settlement currency, and expiration.
type OptionSeriesID struct {
	Venue              Venue
	Underlying         Symbol
	SettlementCurrency Symbol
	ExpirationNS       uint64
}

// OptionSeriesIDError is returned when a wire or constructor value cannot be
// parsed as an option-series identifier.
type OptionSeriesIDError struct {
	Kind       string
	Value      string
	Expiration string
	Reason     string
	Source     error
}

func (e *OptionSeriesIDError) Error() string {
	switch e.Kind {
	case "invalid_format":
		return fmt.Sprintf(
			"invalid `OptionSeriesId` value '%s': expected format 'VENUE:UNDERLYING:SETTLEMENT:EXPIRY'",
			e.Value,
		)
	case "invalid_venue":
		return fmt.Sprintf("invalid `OptionSeriesId` value '%s': invalid venue: %v", e.Value, e.Source)
	case "invalid_expiration":
		return fmt.Sprintf(
			"invalid `OptionSeriesId` value '%s': invalid expiration '%s': %s",
			e.Value,
			e.Expiration,
			e.Reason,
		)
	default:
		return fmt.Sprintf("invalid `OptionSeriesId` value '%s'", e.Value)
	}
}

func (e *OptionSeriesIDError) Unwrap() error { return e.Source }

// NewOptionSeriesID constructs an identifier from already validated
// components.
func NewOptionSeriesID(
	venue Venue,
	underlying Symbol,
	settlementCurrency Symbol,
	expirationNS uint64,
) OptionSeriesID {
	return OptionSeriesID{
		Venue:              venue,
		Underlying:         underlying,
		SettlementCurrency: settlementCurrency,
		ExpirationNS:       expirationNS,
	}
}

// ParseOptionSeriesID parses VENUE:UNDERLYING:SETTLEMENT:EXPIRY. Expiry may
// be nanoseconds, an RFC 3339 timestamp, or a YYYY-MM-DD UTC date.
func ParseOptionSeriesID(value string) (OptionSeriesID, error) {
	parts := strings.SplitN(value, ":", 4)
	if len(parts) != 4 {
		return OptionSeriesID{}, &OptionSeriesIDError{Kind: "invalid_format", Value: value}
	}
	return parseOptionSeriesIDParts(value, parts[0], parts[1], parts[2], parts[3])
}

// OptionSeriesIDFromExpiry constructs an identifier from text components.
func OptionSeriesIDFromExpiry(
	venue string,
	underlying string,
	settlementCurrency string,
	expiry string,
) (OptionSeriesID, error) {
	value := strings.Join([]string{venue, underlying, settlementCurrency, expiry}, ":")
	return parseOptionSeriesIDParts(value, venue, underlying, settlementCurrency, expiry)
}

func parseOptionSeriesIDParts(
	value string,
	venueText string,
	underlyingText string,
	settlementText string,
	expiryText string,
) (OptionSeriesID, error) {
	venue, err := ParseVenue(venueText)
	if err != nil {
		return OptionSeriesID{}, &OptionSeriesIDError{
			Kind: "invalid_venue", Value: value, Source: err,
		}
	}
	expirationNS, err := parseExpirationNS(expiryText)
	if err != nil {
		return OptionSeriesID{}, &OptionSeriesIDError{
			Kind:       "invalid_expiration",
			Value:      value,
			Expiration: expiryText,
			Reason:     err.Error(),
		}
	}
	return NewOptionSeriesID(
		venue,
		Symbol(underlyingText),
		Symbol(settlementText),
		expirationNS,
	), nil
}

func parseExpirationNS(value string) (uint64, error) {
	if raw, err := strconv.ParseUint(value, 10, 64); err == nil {
		return raw, nil
	}
	for _, layout := range []string{"2006-01-02", time.RFC3339Nano} {
		parsed, err := time.Parse(layout, value)
		if err == nil {
			nanos := parsed.UnixNano()
			if nanos < 0 {
				break
			}
			return uint64(nanos), nil
		}
	}
	return 0, fmt.Errorf("Invalid format: %s", value)
}

// WireString returns the exact persistence representation.
func (id OptionSeriesID) WireString() string {
	return fmt.Sprintf(
		"%s:%s:%s:%d",
		id.Venue,
		id.Underlying,
		id.SettlementCurrency,
		id.ExpirationNS,
	)
}

func (id OptionSeriesID) String() string {
	expiry := time.Unix(0, int64(id.ExpirationNS)).UTC().Format("2006-01-02T15:04:05Z")
	return fmt.Sprintf("%s:%s:%s:%s", id.Venue, id.Underlying, id.SettlementCurrency, expiry)
}

// DebugString returns the source model's quoted debug representation.
func (id OptionSeriesID) DebugString() string { return strconv.Quote(id.String()) }

func (id OptionSeriesID) MarshalJSON() ([]byte, error) {
	return marshalJSONString(id.WireString())
}

func (id *OptionSeriesID) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	parsed, err := ParseOptionSeriesID(value)
	if err != nil {
		return err
	}
	*id = parsed
	return nil
}
