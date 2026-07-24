package market

import (
	"encoding/json"
	"testing"

	"github.com/upcomers-org/platformgo/internal/decimal"
)

func fundingUint16(value uint16) *uint16 {
	return &value
}

func fundingUnixNanos(value UnixNanos) *UnixNanos {
	return &value
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/funding.rs:162
//	test: test_funding_rate_update_new
func TestFundingRateUpdateNew(t *testing.T) {
	instrumentID := InstrumentID("BTCUSDT-PERP.BINANCE")
	rate := decimal.MustParse("0.0001")
	tsEvent := UnixNanos(1)
	tsInit := UnixNanos(2)
	fundingRate := NewFundingRateUpdate(instrumentID, rate, nil, nil, tsEvent, tsInit)
	if fundingRate.InstrumentID != instrumentID ||
		!fundingRate.Rate.Equal(rate) ||
		fundingRate.Interval != nil ||
		fundingRate.NextFundingNS != nil ||
		fundingRate.TsEvent != tsEvent ||
		fundingRate.TsInit != tsInit {
		t.Fatalf("unexpected funding rate: %+v", fundingRate)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/funding.rs:179
//	test: test_funding_rate_update_new_with_optional_fields
func TestFundingRateUpdateNewWithOptionalFields(t *testing.T) {
	instrumentID := InstrumentID("BTCUSDT-PERP.BINANCE")
	rate := decimal.MustParse("0.0001")
	interval := fundingUint16(60)
	nextFundingNS := fundingUnixNanos(1000)
	tsEvent := UnixNanos(1)
	tsInit := UnixNanos(2)
	fundingRate := NewFundingRateUpdate(
		instrumentID,
		rate,
		interval,
		nextFundingNS,
		tsEvent,
		tsInit,
	)
	if fundingRate.InstrumentID != instrumentID ||
		!fundingRate.Rate.Equal(rate) ||
		fundingRate.Interval == nil || *fundingRate.Interval != *interval ||
		fundingRate.NextFundingNS == nil || *fundingRate.NextFundingNS != *nextFundingNS ||
		fundingRate.TsEvent != tsEvent ||
		fundingRate.TsInit != tsInit {
		t.Fatalf("unexpected funding rate: %+v", fundingRate)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/funding.rs:204
//	test: test_funding_rate_update_display
func TestFundingRateUpdateDisplay(t *testing.T) {
	fundingRate := NewFundingRateUpdate(
		"BTCUSDT-PERP.BINANCE",
		decimal.MustParse("0.0001"),
		fundingUint16(60),
		fundingUnixNanos(1000),
		1,
		2,
	)
	const want = "BTCUSDT-PERP.BINANCE,0.0001,Some(60),Some(1000),1,2"
	if got := fundingRate.String(); got != want {
		t.Fatalf("display = %q, want %q", got, want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/funding.rs:227
//	test: test_funding_rate_update_get_ts_init
func TestFundingRateUpdateGetTsInit(t *testing.T) {
	tsInit := UnixNanos(2)
	fundingRate := NewFundingRateUpdate(
		"BTCUSDT-PERP.BINANCE",
		decimal.MustParse("0.0001"),
		nil,
		nil,
		1,
		tsInit,
	)
	if fundingRate.TsInit != tsInit {
		t.Fatalf("ts_init = %d, want %d", fundingRate.TsInit, tsInit)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/funding.rs:239
//	test: test_funding_rate_update_eq_hash
func TestFundingRateUpdateEqHash(t *testing.T) {
	instrumentID := InstrumentID("BTCUSDT-PERP.BINANCE")
	rate := decimal.MustParse("0.0001")
	fundingRate1 := NewFundingRateUpdate(instrumentID, rate, nil, nil, 1, 2)
	fundingRate2 := NewFundingRateUpdate(instrumentID, rate, nil, nil, 1, 2)
	fundingRate3 := NewFundingRateUpdate(
		instrumentID,
		decimal.MustParse("0.0002"),
		nil,
		nil,
		1,
		2,
	)
	if !fundingRate1.Equal(fundingRate2) {
		t.Fatal("equal funding rates compare unequal")
	}
	if fundingRate1.Equal(fundingRate3) {
		t.Fatal("different funding rates compare equal")
	}
	if fundingRate1.Hash() != fundingRate2.Hash() {
		t.Fatalf("equal funding rates have unequal hashes: %d != %d", fundingRate1.Hash(), fundingRate2.Hash())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/funding.rs:269
//	test: test_funding_rate_update_json_serialization
func TestFundingRateUpdateJSONSerialization(t *testing.T) {
	fundingRate := NewFundingRateUpdate(
		"BTCUSDT-PERP.BINANCE",
		decimal.MustParse("0.0001"),
		fundingUint16(60),
		fundingUnixNanos(1000),
		1,
		2,
	)
	serialized, err := json.Marshal(fundingRate)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var deserialized FundingRateUpdate
	if err := json.Unmarshal(serialized, &deserialized); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !fundingRate.Equal(deserialized) {
		t.Fatalf("round-trip differs: %+v != %+v", fundingRate, deserialized)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/funding.rs:292
//	test: test_funding_rate_update_msgpack_serialization
//
// Adaptation: Rust MessagePack is replaced by a deterministic native Go binary codec.
func TestFundingRateUpdateMsgpackSerialization(t *testing.T) {
	fundingRate := NewFundingRateUpdate(
		"BTCUSDT-PERP.BINANCE",
		decimal.MustParse("0.0001"),
		fundingUint16(60),
		fundingUnixNanos(1000),
		1,
		2,
	)
	serialized, err := fundingRate.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal binary: %v", err)
	}
	var deserialized FundingRateUpdate
	if err := deserialized.UnmarshalBinary(serialized); err != nil {
		t.Fatalf("unmarshal binary: %v", err)
	}
	if !fundingRate.Equal(deserialized) {
		t.Fatalf("round-trip differs: %+v != %+v", fundingRate, deserialized)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/funding.rs:315
//	test: test_funding_rate_update_serde_json
func TestFundingRateUpdateSerdeJSON(t *testing.T) {
	fundingRate := NewFundingRateUpdate(
		"BTCUSDT-PERP.BINANCE",
		decimal.MustParse("0.0001"),
		fundingUint16(60),
		fundingUnixNanos(1000),
		1,
		2,
	)
	jsonText, err := json.Marshal(fundingRate)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var deserialized FundingRateUpdate
	if err := json.Unmarshal(jsonText, &deserialized); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !fundingRate.Equal(deserialized) {
		t.Fatalf("round-trip differs: %+v != %+v", fundingRate, deserialized)
	}
}
