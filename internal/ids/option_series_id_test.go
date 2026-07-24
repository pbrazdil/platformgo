package ids

import (
	"encoding/json"
	"errors"
	"testing"
)

const optionSeriesExpirationNS uint64 = 1_700_000_000_000_000_000

func optionSeriesTestID() OptionSeriesID {
	return NewOptionSeriesID(
		MustVenue("DERIBIT"),
		MustSymbol("BTC"),
		MustSymbol("BTC"),
		optionSeriesExpirationNS,
	)
}

func requireOptionSeriesError(t *testing.T, err error, kind string) *OptionSeriesIDError {
	t.Helper()
	var seriesErr *OptionSeriesIDError
	if !errors.As(err, &seriesErr) {
		t.Fatalf("error type = %T, want *OptionSeriesIDError", err)
	}
	if seriesErr.Kind != kind {
		t.Fatalf("error kind = %q, want %q", seriesErr.Kind, kind)
	}
	return seriesErr
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/option_series_id.rs:259
//	test: test_option_series_id_new
func TestOptionSeriesIDNew(t *testing.T) {
	venue := MustVenue("DERIBIT")
	underlying := MustSymbol("BTC")
	settlement := MustSymbol("BTC")
	id := NewOptionSeriesID(venue, underlying, settlement, optionSeriesExpirationNS)
	if id.Venue != venue {
		t.Fatalf("Venue = %v, want %v", id.Venue, venue)
	}
	if id.Underlying != underlying {
		t.Fatalf("Underlying = %v, want %v", id.Underlying, underlying)
	}
	if id.SettlementCurrency != settlement {
		t.Fatalf("SettlementCurrency = %v, want %v", id.SettlementCurrency, settlement)
	}
	if id.ExpirationNS != optionSeriesExpirationNS {
		t.Fatalf("ExpirationNS = %d, want %d", id.ExpirationNS, optionSeriesExpirationNS)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/option_series_id.rs:274
//	test: test_option_series_id_display
func TestOptionSeriesIDDisplay(t *testing.T) {
	const want = "DERIBIT:BTC:BTC:2023-11-14T22:13:20Z"
	if got := optionSeriesTestID().String(); got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/option_series_id.rs:280
//	test: test_option_series_id_wire_string
func TestOptionSeriesIDWireString(t *testing.T) {
	const want = "DERIBIT:BTC:BTC:1700000000000000000"
	if got := optionSeriesTestID().WireString(); got != want {
		t.Fatalf("WireString() = %q, want %q", got, want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/option_series_id.rs:286
//	test: test_option_series_id_debug
func TestOptionSeriesIDDebug(t *testing.T) {
	const want = `"DERIBIT:BTC:BTC:2023-11-14T22:13:20Z"`
	if got := optionSeriesTestID().DebugString(); got != want {
		t.Fatalf("DebugString() = %q, want %q", got, want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/option_series_id.rs:295
//	test: test_option_series_id_from_str
func TestOptionSeriesIDFromString(t *testing.T) {
	id, err := ParseOptionSeriesID("DERIBIT:BTC:BTC:1700000000000000000")
	if err != nil {
		t.Fatal(err)
	}
	if id.Venue != MustVenue("DERIBIT") {
		t.Fatalf("Venue = %v", id.Venue)
	}
	if id.Underlying != MustSymbol("BTC") {
		t.Fatalf("Underlying = %v", id.Underlying)
	}
	if id.SettlementCurrency != MustSymbol("BTC") {
		t.Fatalf("SettlementCurrency = %v", id.SettlementCurrency)
	}
	if id.ExpirationNS != optionSeriesExpirationNS {
		t.Fatalf("ExpirationNS = %d", id.ExpirationNS)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/option_series_id.rs:308
//	test: test_option_series_id_from_str_rfc3339
func TestOptionSeriesIDFromStringRFC3339(t *testing.T) {
	id, err := ParseOptionSeriesID("DERIBIT:BTC:BTC:2023-11-14T22:13:20Z")
	if err != nil {
		t.Fatal(err)
	}
	if id.Venue != MustVenue("DERIBIT") {
		t.Fatalf("Venue = %v", id.Venue)
	}
	if id.Underlying != MustSymbol("BTC") {
		t.Fatalf("Underlying = %v", id.Underlying)
	}
	if id.ExpirationNS != optionSeriesExpirationNS {
		t.Fatalf("ExpirationNS = %d", id.ExpirationNS)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/option_series_id.rs:319
//	test: test_option_series_id_from_str_date
func TestOptionSeriesIDFromStringDate(t *testing.T) {
	id, err := ParseOptionSeriesID("DERIBIT:BTC:BTC:2023-11-14")
	if err != nil {
		t.Fatal(err)
	}
	if id.Venue != MustVenue("DERIBIT") {
		t.Fatalf("Venue = %v", id.Venue)
	}
	if id.Underlying != MustSymbol("BTC") {
		t.Fatalf("Underlying = %v", id.Underlying)
	}
	const want uint64 = 1_699_920_000_000_000_000
	if id.ExpirationNS != want {
		t.Fatalf("ExpirationNS = %d, want %d", id.ExpirationNS, want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/option_series_id.rs:331
//	test: test_option_series_id_from_str_invalid_format
func TestOptionSeriesIDFromStringInvalidFormat(t *testing.T) {
	const value = "DERIBIT:BTC:BTC"
	err := func() error {
		_, err := ParseOptionSeriesID(value)
		return err
	}()
	seriesErr := requireOptionSeriesError(t, err, "invalid_format")
	if seriesErr.Value != value {
		t.Fatalf("error value = %q", seriesErr.Value)
	}
	const want = "invalid `OptionSeriesId` value 'DERIBIT:BTC:BTC': expected format 'VENUE:UNDERLYING:SETTLEMENT:EXPIRY'"
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err, want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/option_series_id.rs:347
//	test: test_option_series_id_from_str_invalid_venue
func TestOptionSeriesIDFromStringInvalidVenue(t *testing.T) {
	const value = "DÉRIBIT:BTC:BTC:1700000000000000000"
	_, err := ParseOptionSeriesID(value)
	seriesErr := requireOptionSeriesError(t, err, "invalid_venue")
	if seriesErr.Value != value {
		t.Fatalf("error value = %q", seriesErr.Value)
	}
	if got := validationKind(seriesErr.Source); got != "non_ascii_string" {
		t.Fatalf("venue error kind = %q", got)
	}
	const want = "invalid `OptionSeriesId` value 'DÉRIBIT:BTC:BTC:1700000000000000000': invalid venue: invalid string for 'value' contained a non-ASCII char, was 'DÉRIBIT'"
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err, want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/option_series_id.rs:371
//	test: test_option_series_id_from_str_invalid_expiry
func TestOptionSeriesIDFromStringInvalidExpiry(t *testing.T) {
	const value = "DERIBIT:BTC:BTC:not_a_date"
	_, err := ParseOptionSeriesID(value)
	seriesErr := requireOptionSeriesError(t, err, "invalid_expiration")
	if seriesErr.Value != value || seriesErr.Expiration != "not_a_date" ||
		seriesErr.Reason != "Invalid format: not_a_date" {
		t.Fatalf("error fields = %+v", seriesErr)
	}
	const want = "invalid `OptionSeriesId` value 'DERIBIT:BTC:BTC:not_a_date': invalid expiration 'not_a_date': Invalid format: not_a_date"
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err, want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/option_series_id.rs:392
//	test: test_option_series_id_inequality
func TestOptionSeriesIDInequality(t *testing.T) {
	other := NewOptionSeriesID(
		MustVenue("DERIBIT"),
		MustSymbol("ETH"),
		MustSymbol("ETH"),
		optionSeriesExpirationNS,
	)
	if optionSeriesTestID() == other {
		t.Fatal("different option series compare equal")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/option_series_id.rs:404
//	test: test_option_series_id_hash
func TestOptionSeriesIDHash(t *testing.T) {
	btc := optionSeriesTestID()
	eth := NewOptionSeriesID(
		MustVenue("DERIBIT"),
		MustSymbol("ETH"),
		MustSymbol("ETH"),
		optionSeriesExpirationNS,
	)
	set := map[OptionSeriesID]struct{}{btc: {}, eth: {}}
	set[btc] = struct{}{}
	if len(set) != 2 {
		t.Fatalf("set length = %d, want 2", len(set))
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/option_series_id.rs:424
//	test: test_option_series_id_serde_roundtrip
func TestOptionSeriesIDJSONRoundTrip(t *testing.T) {
	id := optionSeriesTestID()
	data, err := json.Marshal(id)
	if err != nil {
		t.Fatal(err)
	}
	var decoded OptionSeriesID
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded != id {
		t.Fatalf("decoded = %+v, want %+v", decoded, id)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/option_series_id.rs:434
//	test: test_option_series_id_deserialize_from_owned_value
func TestOptionSeriesIDDeserializeFromOwnedValue(t *testing.T) {
	id := optionSeriesTestID()
	value := any(id.WireString())
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var decoded OptionSeriesID
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded != id {
		t.Fatalf("decoded = %+v, want %+v", decoded, id)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/option_series_id.rs:443
//	test: test_from_expiry_happy_path
func TestOptionSeriesIDFromExpiryHappyPath(t *testing.T) {
	id, err := OptionSeriesIDFromExpiry("DERIBIT", "BTC", "BTC", "2025-03-28")
	if err != nil {
		t.Fatal(err)
	}
	if id.Venue != MustVenue("DERIBIT") || id.Underlying != MustSymbol("BTC") ||
		id.SettlementCurrency != MustSymbol("BTC") {
		t.Fatalf("identifier = %+v", id)
	}
	if id.ExpirationNS == 0 {
		t.Fatal("expiration is zero")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/option_series_id.rs:452
//	test: test_from_expiry_invalid_date
func TestOptionSeriesIDFromExpiryInvalidDate(t *testing.T) {
	const value = "DERIBIT:BTC:BTC:not-a-date"
	_, err := OptionSeriesIDFromExpiry("DERIBIT", "BTC", "BTC", "not-a-date")
	seriesErr := requireOptionSeriesError(t, err, "invalid_expiration")
	if seriesErr.Value != value || seriesErr.Expiration != "not-a-date" ||
		seriesErr.Reason != "Invalid format: not-a-date" {
		t.Fatalf("error fields = %+v", seriesErr)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/option_series_id.rs:467
//	test: test_from_expiry_invalid_venue
func TestOptionSeriesIDFromExpiryInvalidVenue(t *testing.T) {
	const value = "DÉRIBIT:BTC:BTC:2025-03-28"
	_, err := OptionSeriesIDFromExpiry("DÉRIBIT", "BTC", "BTC", "2025-03-28")
	seriesErr := requireOptionSeriesError(t, err, "invalid_venue")
	if seriesErr.Value != value {
		t.Fatalf("error value = %q, want %q", seriesErr.Value, value)
	}
	if got := validationKind(seriesErr.Source); got != "non_ascii_string" {
		t.Fatalf("venue error kind = %q", got)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/identifiers/option_series_id.rs:483
//	test: test_from_expiry_roundtrip
func TestOptionSeriesIDFromExpiryRoundTrip(t *testing.T) {
	id, err := OptionSeriesIDFromExpiry("DERIBIT", "ETH", "ETH", "2025-06-27")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseOptionSeriesID(id.String())
	if err != nil {
		t.Fatal(err)
	}
	if parsed != id {
		t.Fatalf("parsed = %+v, want %+v", parsed, id)
	}
}
