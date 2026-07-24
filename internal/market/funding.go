package market

import (
	"bytes"
	"encoding/gob"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/upcomers-org/platformgo/internal/decimal"
)

func FundingRateMetadata(instrumentID InstrumentID) map[string]string {
	return map[string]string{"instrument_id": string(instrumentID)}
}

func FundingRateFields() []Field {
	return []Field{
		{Name: "rate", Type: "Decimal128"},
		{Name: "interval", Type: "UInt16"},
		{Name: "next_funding_ns", Type: "UInt64"},
		{Name: "ts_event", Type: "UInt64"},
		{Name: "ts_init", Type: "UInt64"},
	}
}

// Equal follows the source identity contract: event ingestion timestamps are
// deliberately excluded.
func (f FundingRateUpdate) Equal(other FundingRateUpdate) bool {
	return f.InstrumentID == other.InstrumentID &&
		f.Rate.Equal(other.Rate) &&
		optionalUint16Equal(f.Interval, other.Interval) &&
		optionalUnixNanosEqual(f.NextFundingNS, other.NextFundingNS)
}

// Hash covers exactly the fields used by Equal.
func (f FundingRateUpdate) Hash() uint64 {
	return hashStrings(
		string(f.InstrumentID),
		f.Rate.String(),
		fundingOptionalUint16(f.Interval),
		fundingOptionalUnixNanos(f.NextFundingNS),
	)
}

func (f FundingRateUpdate) String() string {
	return fmt.Sprintf(
		"%s,%s,%s,%s,%d,%d",
		f.InstrumentID,
		f.Rate,
		fundingOptionalUint16(f.Interval),
		fundingOptionalUnixNanos(f.NextFundingNS),
		f.TsEvent,
		f.TsInit,
	)
}

func (f FundingRateUpdate) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type          string       `json:"type"`
		InstrumentID  InstrumentID `json:"instrument_id"`
		Rate          string       `json:"rate"`
		Interval      *uint16      `json:"interval"`
		NextFundingNS *UnixNanos   `json:"next_funding_ns"`
		TsEvent       UnixNanos    `json:"ts_event"`
		TsInit        UnixNanos    `json:"ts_init"`
	}{
		Type:          "FundingRateUpdate",
		InstrumentID:  f.InstrumentID,
		Rate:          f.Rate.String(),
		Interval:      f.Interval,
		NextFundingNS: f.NextFundingNS,
		TsEvent:       f.TsEvent,
		TsInit:        f.TsInit,
	})
}

func (f *FundingRateUpdate) UnmarshalJSON(data []byte) error {
	var wire struct {
		Type          string       `json:"type"`
		InstrumentID  InstrumentID `json:"instrument_id"`
		Rate          string       `json:"rate"`
		Interval      *uint16      `json:"interval"`
		NextFundingNS *UnixNanos   `json:"next_funding_ns"`
		TsEvent       UnixNanos    `json:"ts_event"`
		TsInit        UnixNanos    `json:"ts_init"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	if wire.Type != "" && wire.Type != "FundingRateUpdate" {
		return fmt.Errorf("invalid market data type %q", wire.Type)
	}
	rate, err := decimal.Parse(wire.Rate)
	if err != nil {
		return err
	}
	*f = NewFundingRateUpdate(
		wire.InstrumentID,
		rate,
		wire.Interval,
		wire.NextFundingNS,
		wire.TsEvent,
		wire.TsInit,
	)
	return nil
}

// MarshalBinary is the native Go adaptation of the source MessagePack path.
// The exact rate stays a decimal string on the wire.
func (f FundingRateUpdate) MarshalBinary() ([]byte, error) {
	wire := fundingRateBinary{
		InstrumentID: string(f.InstrumentID),
		Rate:         f.Rate.String(),
		Interval:     f.Interval,
		TsEvent:      uint64(f.TsEvent),
		TsInit:       uint64(f.TsInit),
	}
	if f.NextFundingNS != nil {
		value := uint64(*f.NextFundingNS)
		wire.NextFundingNS = &value
	}
	var output bytes.Buffer
	if err := gob.NewEncoder(&output).Encode(wire); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func (f *FundingRateUpdate) UnmarshalBinary(data []byte) error {
	var wire fundingRateBinary
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&wire); err != nil {
		return err
	}
	rate, err := decimal.Parse(wire.Rate)
	if err != nil {
		return err
	}
	var nextFundingNS *UnixNanos
	if wire.NextFundingNS != nil {
		value := UnixNanos(*wire.NextFundingNS)
		nextFundingNS = &value
	}
	*f = NewFundingRateUpdate(
		InstrumentID(wire.InstrumentID),
		rate,
		wire.Interval,
		nextFundingNS,
		UnixNanos(wire.TsEvent),
		UnixNanos(wire.TsInit),
	)
	return nil
}

type fundingRateBinary struct {
	InstrumentID  string
	Rate          string
	Interval      *uint16
	NextFundingNS *uint64
	TsEvent       uint64
	TsInit        uint64
}

func optionalUint16Equal(left, right *uint16) bool {
	return left == nil && right == nil ||
		left != nil && right != nil && *left == *right
}

func optionalUnixNanosEqual(left, right *UnixNanos) bool {
	return left == nil && right == nil ||
		left != nil && right != nil && *left == *right
}

func fundingOptionalUint16(value *uint16) string {
	if value == nil {
		return "None"
	}
	return "Some(" + strconv.FormatUint(uint64(*value), 10) + ")"
}

func fundingOptionalUnixNanos(value *UnixNanos) string {
	if value == nil {
		return "None"
	}
	return "Some(" + strconv.FormatUint(uint64(*value), 10) + ")"
}
