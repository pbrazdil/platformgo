package market

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/upcomers-org/platformgo/internal/decimal"
)

func stringPointer(value string) *string { return &value }
func boolPointer(value bool) *bool       { return &value }

func testInstrumentStatus() InstrumentStatus {
	return NewInstrumentStatus(
		"EURUSD.SIM",
		MarketStatusTrading,
		1_000_000_000,
		2_000_000_000,
		stringPointer("Normal trading"),
		stringPointer("MARKET_OPEN"),
		boolPointer(true),
		boolPointer(true),
		boolPointer(false),
	)
}

func testInstrumentStatusMinimal() InstrumentStatus {
	return NewInstrumentStatus(
		"GBPUSD.SIM",
		MarketStatusPreOpen,
		500_000_000,
		1_000_000_000,
		nil, nil, nil, nil, nil,
	)
}

func stubInstrumentStatus() InstrumentStatus {
	return NewInstrumentStatus(
		"MSFT.XNAS",
		MarketStatusTrading,
		1,
		2,
		nil, nil, nil, nil, nil,
	)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/status.rs:159
//	test: test_instrument_status_new
func TestInstrumentStatusNew(t *testing.T) {
	status := testInstrumentStatus()
	if status.InstrumentID != "EURUSD.SIM" ||
		status.Action != MarketStatusTrading ||
		status.TsEvent != 1_000_000_000 ||
		status.TsInit != 2_000_000_000 {
		t.Fatalf("status core fields = %#v", status)
	}
	if status.Reason == nil || *status.Reason != "Normal trading" {
		t.Fatalf("Reason = %v", status.Reason)
	}
	if status.TradingEvent == nil || *status.TradingEvent != "MARKET_OPEN" {
		t.Fatalf("TradingEvent = %v", status.TradingEvent)
	}
	if status.IsTrading == nil || !*status.IsTrading {
		t.Fatalf("IsTrading = %v", status.IsTrading)
	}
	if status.IsQuoting == nil || !*status.IsQuoting {
		t.Fatalf("IsQuoting = %v", status.IsQuoting)
	}
	if status.IsShortSellRestricted == nil || *status.IsShortSellRestricted {
		t.Fatalf("IsShortSellRestricted = %v", status.IsShortSellRestricted)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/status.rs:174
//	test: test_instrument_status_new_minimal
func TestInstrumentStatusNewMinimal(t *testing.T) {
	status := testInstrumentStatusMinimal()
	if status.InstrumentID != "GBPUSD.SIM" ||
		status.Action != MarketStatusPreOpen ||
		status.TsEvent != 500_000_000 ||
		status.TsInit != 1_000_000_000 {
		t.Fatalf("status core fields = %#v", status)
	}
	if status.Reason != nil ||
		status.TradingEvent != nil ||
		status.IsTrading != nil ||
		status.IsQuoting != nil ||
		status.IsShortSellRestricted != nil {
		t.Fatalf("optional fields = %#v", status)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/status.rs:189
//	test: test_instrument_status_builder
func TestInstrumentStatusBuilder(t *testing.T) {
	status := NewInstrumentStatus(
		"BTCUSD.CRYPTO",
		MarketStatusHalt,
		3_000_000_000,
		4_000_000_000,
		stringPointer("Technical issue"),
		stringPointer("HALT_REQUESTED"),
		boolPointer(false),
		boolPointer(false),
		boolPointer(true),
	)
	if status.InstrumentID != "BTCUSD.CRYPTO" ||
		status.Action != MarketStatusHalt ||
		status.TsEvent != 3_000_000_000 ||
		status.TsInit != 4_000_000_000 {
		t.Fatalf("status core fields = %#v", status)
	}
	if status.Reason == nil || *status.Reason != "Technical issue" ||
		status.TradingEvent == nil || *status.TradingEvent != "HALT_REQUESTED" ||
		status.IsTrading == nil || *status.IsTrading ||
		status.IsQuoting == nil || *status.IsQuoting ||
		status.IsShortSellRestricted == nil || !*status.IsShortSellRestricted {
		t.Fatalf("status optional fields = %#v", status)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/status.rs:215
//	test: test_instrument_status_builder_minimal
func TestInstrumentStatusBuilderMinimal(t *testing.T) {
	status := NewInstrumentStatus(
		"AAPL.XNAS",
		MarketStatusClose,
		1_500_000_000,
		2_500_000_000,
		nil, nil, nil, nil, nil,
	)
	if status.InstrumentID != "AAPL.XNAS" ||
		status.Action != MarketStatusClose ||
		status.TsEvent != 1_500_000_000 ||
		status.TsInit != 2_500_000_000 {
		t.Fatalf("status core fields = %#v", status)
	}
	if status.Reason != nil || status.TradingEvent != nil ||
		status.IsTrading != nil || status.IsQuoting != nil ||
		status.IsShortSellRestricted != nil {
		t.Fatalf("status optional fields = %#v", status)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/status.rs:257
//	test: test_instrument_status_with_all_actions
func TestInstrumentStatusWithAllActions(t *testing.T) {
	actions := []MarketStatusAction{
		MarketStatusNone,
		MarketStatusPreOpen,
		MarketStatusPreCross,
		MarketStatusQuoting,
		MarketStatusCross,
		MarketStatusRotation,
		MarketStatusNewPriceIndication,
		MarketStatusTrading,
		MarketStatusHalt,
		MarketStatusPause,
		MarketStatusSuspend,
		MarketStatusPreClose,
		MarketStatusClose,
		MarketStatusPostClose,
		MarketStatusShortSellRestrictionChange,
		MarketStatusNotAvailableForTrading,
	}
	for _, action := range actions {
		t.Run(action.String(), func(t *testing.T) {
			status := NewInstrumentStatus("TEST.SIM", action, 1_000_000_000, 2_000_000_000, nil, nil, nil, nil, nil)
			if status.Action != action {
				t.Fatalf("Action = %v, want %v", status.Action, action)
			}
		})
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/status.rs:274
//	test: test_get_metadata
func TestInstrumentStatusMetadata(t *testing.T) {
	metadata := InstrumentStatusMetadata("EURUSD.SIM")
	if len(metadata) != 1 {
		t.Fatalf("metadata length = %d, want 1", len(metadata))
	}
	if got := metadata["instrument_id"]; got != "EURUSD.SIM" {
		t.Fatalf("instrument_id = %q", got)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/status.rs:286
//	test: test_get_metadata_different_instruments
func TestInstrumentStatusMetadataDifferentInstruments(t *testing.T) {
	eur := InstrumentStatusMetadata("EURUSD.SIM")
	gbp := InstrumentStatusMetadata("GBPUSD.SIM")
	if eur["instrument_id"] != "EURUSD.SIM" {
		t.Fatalf("EUR metadata = %#v", eur)
	}
	if gbp["instrument_id"] != "GBPUSD.SIM" {
		t.Fatalf("GBP metadata = %#v", gbp)
	}
	if eur["instrument_id"] == gbp["instrument_id"] {
		t.Fatalf("metadata unexpectedly equal: %#v %#v", eur, gbp)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/status.rs:302
//	test: test_instrument_status_partial_eq
func TestInstrumentStatusPartialEquality(t *testing.T) {
	first := testInstrumentStatus()
	second := testInstrumentStatus()
	third := testInstrumentStatusMinimal()
	if !first.Equal(second) {
		t.Fatal("equal statuses differ")
	}
	if first.Equal(third) {
		t.Fatal("different statuses compare equal")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/status.rs:312
//	test: test_instrument_status_partial_eq_different_fields
func TestInstrumentStatusPartialEqualityDifferentFields(t *testing.T) {
	first := testInstrumentStatus()
	action := testInstrumentStatus()
	action.Action = MarketStatusHalt
	trading := testInstrumentStatus()
	trading.IsTrading = boolPointer(false)
	reason := testInstrumentStatus()
	reason.Reason = stringPointer("Different reason")
	if first.Equal(action) || first.Equal(trading) || first.Equal(reason) {
		t.Fatal("status differing by an asserted field compares equal")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/status.rs:329
//	test: test_instrument_status_eq_consistency
func TestInstrumentStatusEqualityConsistency(t *testing.T) {
	first := testInstrumentStatus()
	second := testInstrumentStatus()
	if !first.Equal(second) || !second.Equal(first) || !first.Equal(first) {
		t.Fatal("equality is not symmetric and reflexive")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/status.rs:339
//	test: test_instrument_status_hash
func TestInstrumentStatusHash(t *testing.T) {
	if testInstrumentStatus().Hash() != testInstrumentStatus().Hash() {
		t.Fatal("equal statuses have different hashes")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/status.rs:353
//	test: test_instrument_status_hash_different_objects
func TestInstrumentStatusHashDifferentObjects(t *testing.T) {
	if testInstrumentStatus().Hash() == testInstrumentStatusMinimal().Hash() {
		t.Fatal("different statuses have equal hashes")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/status.rs:367
//	test: test_instrument_status_clone
func TestInstrumentStatusClone(t *testing.T) {
	first := testInstrumentStatus()
	second := first
	if !first.Equal(second) ||
		first.InstrumentID != second.InstrumentID ||
		first.Action != second.Action ||
		first.TsEvent != second.TsEvent ||
		first.TsInit != second.TsInit ||
		!equalOptionalString(first.Reason, second.Reason) ||
		!equalOptionalString(first.TradingEvent, second.TradingEvent) ||
		!equalOptionalBool(first.IsTrading, second.IsTrading) ||
		!equalOptionalBool(first.IsQuoting, second.IsQuoting) ||
		!equalOptionalBool(first.IsShortSellRestricted, second.IsShortSellRestricted) {
		t.Fatal("cloned status fields differ")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/status.rs:387
//	test: test_instrument_status_debug
func TestInstrumentStatusDebug(t *testing.T) {
	debug := testInstrumentStatus().DebugString()
	for _, want := range []string{"InstrumentStatus", "EURUSD.SIM", "Trading", "Normal trading", "MARKET_OPEN"} {
		if !strings.Contains(debug, want) {
			t.Fatalf("DebugString() = %q, missing %q", debug, want)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/status.rs:399
//	test: test_instrument_status_copy
func TestInstrumentStatusCopy(t *testing.T) {
	first := testInstrumentStatus()
	second := first
	if !first.Equal(second) ||
		first.InstrumentID != second.InstrumentID ||
		first.Action != second.Action {
		t.Fatal("copied status differs")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/status.rs:409
//	test: test_instrument_status_has_ts_init
func TestInstrumentStatusHasTimestampInit(t *testing.T) {
	if got := testInstrumentStatus().TimestampInit(); got != 2_000_000_000 {
		t.Fatalf("TimestampInit() = %d", got)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/status.rs:415
//	test: test_instrument_status_has_ts_init_different_values
func TestInstrumentStatusHasTimestampInitDifferentValues(t *testing.T) {
	first := testInstrumentStatus().TimestampInit()
	second := testInstrumentStatusMinimal().TimestampInit()
	if first != 2_000_000_000 || second != 1_000_000_000 || first == second {
		t.Fatalf("timestamps = %d, %d", first, second)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/status.rs:425
//	test: test_instrument_status_display
func TestInstrumentStatusDisplay(t *testing.T) {
	display := testInstrumentStatus().String()
	for _, want := range []string{"EURUSD.SIM", "TRADING", "1000000000", "2000000000"} {
		if !strings.Contains(display, want) {
			t.Fatalf("String() = %q, missing %q", display, want)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/status.rs:436
//	test: test_instrument_status_display_format
func TestInstrumentStatusDisplayFormat(t *testing.T) {
	const want = "EURUSD.SIM,TRADING,1000000000,2000000000"
	if got := testInstrumentStatus().String(); got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/status.rs:444
//	test: test_instrument_status_display_different_actions
func TestInstrumentStatusDisplayDifferentActions(t *testing.T) {
	status := NewInstrumentStatus("TEST.SIM", MarketStatusHalt, 1_000_000_000, 2_000_000_000, nil, nil, nil, nil, nil)
	if got := status.String(); !strings.Contains(got, "HALT") {
		t.Fatalf("String() = %q", got)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/status.rs:462
//	test: test_instrument_status_serialization
func TestInstrumentStatusSerialization(t *testing.T) {
	status := testInstrumentStatus()
	payload, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	var decoded InstrumentStatus
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatal(err)
	}
	if !status.Equal(decoded) {
		t.Fatalf("decoded = %#v, want %#v", decoded, status)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/status.rs:473
//	test: test_instrument_status_serialization_with_optional_fields
func TestInstrumentStatusSerializationWithOptionalFields(t *testing.T) {
	status := testInstrumentStatusMinimal()
	payload, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	var decoded InstrumentStatus
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatal(err)
	}
	if !status.Equal(decoded) {
		t.Fatalf("decoded = %#v, want %#v", decoded, status)
	}
	if decoded.Reason != nil || decoded.TradingEvent != nil ||
		decoded.IsTrading != nil || decoded.IsQuoting != nil ||
		decoded.IsShortSellRestricted != nil {
		t.Fatalf("optional fields = %#v", decoded)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/status.rs:489
//	test: test_instrument_status_with_trading_flags
func TestInstrumentStatusWithTradingFlags(t *testing.T) {
	status := NewInstrumentStatus(
		"TEST.SIM", MarketStatusTrading, 1_000_000_000, 2_000_000_000,
		nil, nil, boolPointer(true), boolPointer(true), boolPointer(false),
	)
	if status.IsTrading == nil || !*status.IsTrading ||
		status.IsQuoting == nil || !*status.IsQuoting ||
		status.IsShortSellRestricted == nil || *status.IsShortSellRestricted {
		t.Fatalf("flags = %#v", status)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/status.rs:508
//	test: test_instrument_status_with_halt_flags
func TestInstrumentStatusWithHaltFlags(t *testing.T) {
	status := NewInstrumentStatus(
		"TEST.SIM", MarketStatusHalt, 1_000_000_000, 2_000_000_000,
		stringPointer("System maintenance"), stringPointer("HALT_SYSTEM"),
		boolPointer(false), boolPointer(false), boolPointer(true),
	)
	if status.Action != MarketStatusHalt ||
		status.IsTrading == nil || *status.IsTrading ||
		status.IsQuoting == nil || *status.IsQuoting ||
		status.IsShortSellRestricted == nil || !*status.IsShortSellRestricted ||
		status.Reason == nil || *status.Reason != "System maintenance" ||
		status.TradingEvent == nil || *status.TradingEvent != "HALT_SYSTEM" {
		t.Fatalf("status = %#v", status)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/status.rs:530
//	test: test_instrument_status_with_short_sell_restriction
func TestInstrumentStatusWithShortSellRestriction(t *testing.T) {
	status := NewInstrumentStatus(
		"TEST.SIM", MarketStatusShortSellRestrictionChange, 1_000_000_000, 2_000_000_000,
		stringPointer("Circuit breaker triggered"), stringPointer("SSR_ACTIVATED"),
		boolPointer(true), boolPointer(true), boolPointer(true),
	)
	if status.Action != MarketStatusShortSellRestrictionChange ||
		status.IsShortSellRestricted == nil || !*status.IsShortSellRestricted ||
		status.Reason == nil || *status.Reason != "Circuit breaker triggered" ||
		status.TradingEvent == nil || *status.TradingEvent != "SSR_ACTIVATED" {
		t.Fatalf("status = %#v", status)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/status.rs:553
//	test: test_instrument_status_with_mixed_optional_fields
func TestInstrumentStatusWithMixedOptionalFields(t *testing.T) {
	status := NewInstrumentStatus(
		"TEST.SIM", MarketStatusQuoting, 1_000_000_000, 2_000_000_000,
		stringPointer("Pre-market"), nil, boolPointer(false), boolPointer(true), nil,
	)
	if status.Reason == nil || *status.Reason != "Pre-market" ||
		status.TradingEvent != nil ||
		status.IsTrading == nil || *status.IsTrading ||
		status.IsQuoting == nil || !*status.IsQuoting ||
		status.IsShortSellRestricted != nil {
		t.Fatalf("status = %#v", status)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/status.rs:574
//	test: test_instrument_status_with_empty_reason
func TestInstrumentStatusWithEmptyReason(t *testing.T) {
	status := NewInstrumentStatus(
		"TEST.SIM", MarketStatusTrading, 1_000_000_000, 2_000_000_000,
		stringPointer(""), nil, nil, nil, nil,
	)
	if status.Reason == nil || *status.Reason != "" {
		t.Fatalf("Reason = %v", status.Reason)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/status.rs:591
//	test: test_instrument_status_with_long_reason
func TestInstrumentStatusWithLongReason(t *testing.T) {
	const reason = "This is a very long reason that explains in detail why the market status has changed and includes multiple sentences to test the handling of longer text strings."
	status := NewInstrumentStatus(
		"TEST.SIM", MarketStatusSuspend, 1_000_000_000, 2_000_000_000,
		stringPointer(reason), nil, nil, nil, nil,
	)
	if status.Reason == nil || *status.Reason != reason {
		t.Fatalf("Reason = %v", status.Reason)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/status.rs:609
//	test: test_instrument_status_with_zero_timestamps
func TestInstrumentStatusWithZeroTimestamps(t *testing.T) {
	status := NewInstrumentStatus("TEST.SIM", MarketStatusNone, 0, 0, nil, nil, nil, nil, nil)
	if status.TsEvent != 0 || status.TsInit != 0 {
		t.Fatalf("timestamps = %d, %d", status.TsEvent, status.TsInit)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/status.rs:627
//	test: test_instrument_status_with_max_timestamps
func TestInstrumentStatusWithMaxTimestamps(t *testing.T) {
	status := NewInstrumentStatus(
		"TEST.SIM", MarketStatusTrading, UnixNanos(math.MaxUint64), UnixNanos(math.MaxUint64),
		nil, nil, nil, nil, nil,
	)
	if status.TsEvent != math.MaxUint64 || status.TsInit != math.MaxUint64 {
		t.Fatalf("timestamps = %d, %d", status.TsEvent, status.TsInit)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/status.rs:645
//	test: test_to_string
func TestInstrumentStatusToString(t *testing.T) {
	if got := stubInstrumentStatus().String(); got != "MSFT.XNAS,TRADING,1,2" {
		t.Fatalf("String() = %q", got)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/status.rs:650
//	test: test_data_from_instrument_status
func TestDataFromInstrumentStatus(t *testing.T) {
	status := stubInstrumentStatus()
	data := DataFromInstrumentStatus(status)
	if data.Status == nil {
		t.Fatal("Data is not InstrumentStatus")
	}
	if got := data.InstrumentID(); got != status.InstrumentID {
		t.Fatalf("InstrumentID() = %q, want %q", got, status.InstrumentID)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/status.rs:657
//	test: test_data_has_ts_init_for_instrument_status
func TestDataHasTimestampInitForInstrumentStatus(t *testing.T) {
	status := stubInstrumentStatus()
	if got := DataFromInstrumentStatus(status).TimestampInit(); got != status.TsInit {
		t.Fatalf("TimestampInit() = %d, want %d", got, status.TsInit)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/status.rs:663
//	test: test_try_from_data_instrument_status
func TestInstrumentStatusFromData(t *testing.T) {
	status := stubInstrumentStatus()
	extracted, err := DataFromInstrumentStatus(status).InstrumentStatus()
	if err != nil {
		t.Fatal(err)
	}
	if !extracted.Equal(status) {
		t.Fatalf("extracted = %#v, want %#v", extracted, status)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/status.rs:670
//	test: test_try_from_data_instrument_status_wrong_variant
func TestInstrumentStatusFromDataWrongVariant(t *testing.T) {
	status := stubInstrumentStatus()
	close := NewInstrumentClose(
		status.InstrumentID,
		decimal.MustPrice("100.00"),
		EndOfSession,
		status.TsEvent,
		status.TsInit,
	)
	if _, err := (Data{Close: &close}).InstrumentStatus(); err == nil {
		t.Fatal("wrong variant accepted")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/status.rs:684
//	test: test_data_serde_roundtrip_instrument_status
func TestDataJSONRoundTripInstrumentStatus(t *testing.T) {
	status := stubInstrumentStatus()
	data := DataFromInstrumentStatus(status)
	payload, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	var roundTrip Data
	if err := json.Unmarshal(payload, &roundTrip); err != nil {
		t.Fatal(err)
	}
	if roundTrip.Status == nil || !roundTrip.Status.Equal(status) {
		t.Fatalf("round trip = %#v, want %#v", roundTrip, status)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/status.rs:695
//	test: test_data_clone_instrument_status
func TestDataCloneInstrumentStatus(t *testing.T) {
	data := DataFromInstrumentStatus(stubInstrumentStatus())
	cloned := data
	if !data.Equal(cloned) {
		t.Fatal("cloned Data differs")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/status.rs:703
//	test: test_data_ffi_try_from_instrument_status_errors
func TestDataFFIFromInstrumentStatusErrors(t *testing.T) {
	if err := DataFromInstrumentStatus(stubInstrumentStatus()).ToFFI(); err == nil {
		t.Fatal("InstrumentStatus converted to FFI")
	}
}
