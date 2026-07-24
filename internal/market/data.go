package market

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
)

// Metadata holds JSON-compatible subscription parameters. Numbers decoded
// from persistence remain json.Number so topic identity never converts them
// through binary floating point.
type Metadata map[string]any

// DataType identifies a data stream by its type and canonicalized metadata.
// Topic and hash are derived, never accepted from persistence.
type DataType struct {
	typeName   string
	metadata   Metadata
	topic      string
	hash       uint64
	identifier *string
}

func NewDataType(typeName string, metadata Metadata, identifier *string) DataType {
	copiedMetadata := cloneMetadata(metadata)
	topic := typeName
	if len(copiedMetadata) != 0 {
		topic += "." + metadataTopicSuffix(copiedMetadata)
	}
	return DataType{
		typeName:   typeName,
		metadata:   copiedMetadata,
		topic:      topic,
		hash:       hashDataTypeTopic(topic),
		identifier: cloneStringPointer(identifier),
	}
}

func DataTypeFromParts(typeName, topic string, metadata Metadata) DataType {
	return DataType{
		typeName: typeName,
		metadata: cloneMetadata(metadata),
		topic:    topic,
		hash:     hashDataTypeTopic(topic),
	}
}

func (d DataType) TypeName() string { return d.typeName }

func (d DataType) Metadata() Metadata {
	return cloneMetadata(d.metadata)
}

func (d DataType) MetadataString() string {
	if d.metadata == nil {
		return "null"
	}
	data, err := json.Marshal(d.metadata)
	if err != nil {
		return ""
	}
	return string(data)
}

func (d DataType) MetadataStringMap() map[string]string {
	if d.metadata == nil {
		return nil
	}
	result := make(map[string]string, len(d.metadata))
	for key, value := range d.metadata {
		result[key] = metadataValueString(value)
	}
	return result
}

func (d DataType) PrecomputedHash() uint64 { return d.hash }
func (d DataType) Topic() string           { return d.topic }

func (d DataType) Identifier() (string, bool) {
	if d.identifier == nil {
		return "", false
	}
	return *d.identifier, true
}

func (d DataType) Equal(other DataType) bool { return d.topic == other.topic }

func (d DataType) Compare(other DataType) int {
	return strings.Compare(d.topic, other.topic)
}

func (d DataType) Hash() uint64 { return d.hash }
func (d DataType) String() string {
	return d.topic
}

func (d DataType) DebugString() string {
	metadata := "None"
	if d.metadata != nil {
		metadata = fmt.Sprintf("Some(%s)", d.MetadataString())
	}
	identifier := "None"
	if d.identifier != nil {
		identifier = fmt.Sprintf("Some(%q)", *d.identifier)
	}
	return fmt.Sprintf(
		"DataType(type_name=%s, metadata=%s, identifier=%s)",
		d.typeName,
		metadata,
		identifier,
	)
}

func (d DataType) InstrumentID() (InstrumentID, bool) {
	value, ok := metadataString(d.metadata, "instrument_id")
	if !ok {
		return "", false
	}
	return InstrumentID(value), true
}

func (d DataType) Venue() (ids.Venue, bool) {
	value, ok := metadataString(d.metadata, "venue")
	if !ok {
		return "", false
	}
	return ids.MustVenue(value), true
}

func (d DataType) Start() (UnixNanos, bool) {
	return metadataUnixNanos(d.metadata, "start")
}

func (d DataType) End() (UnixNanos, bool) {
	return metadataUnixNanos(d.metadata, "end")
}

func (d DataType) Limit() (uint, bool) {
	value, ok := d.metadata["limit"]
	if !ok {
		return 0, false
	}
	text := metadataValueString(value)
	parsed, err := strconv.ParseUint(text, 10, strconv.IntSize)
	if err != nil {
		panic("Invalid `usize` for 'limit'")
	}
	return uint(parsed), true
}

func (d DataType) PersistenceJSON() (string, error) {
	wire := map[string]any{
		"type_name": d.typeName,
		"metadata":  d.metadata,
	}
	if d.identifier != nil {
		wire["identifier"] = *d.identifier
	}
	data, err := json.Marshal(wire)
	return string(data), err
}

func DataTypeFromPersistenceJSON(text string) (DataType, error) {
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.UseNumber()
	var wire map[string]any
	if err := decoder.Decode(&wire); err != nil {
		return DataType{}, fmt.Errorf("Invalid data_type JSON: %w", err)
	}
	typeName, ok := wire["type_name"].(string)
	if !ok {
		return DataType{}, errors.New("data_type must have type_name")
	}
	var metadata Metadata
	if value, exists := wire["metadata"]; exists && value != nil {
		raw, ok := value.(map[string]any)
		if !ok {
			return DataType{}, errors.New("data_type metadata must be a JSON object")
		}
		if len(raw) != 0 {
			metadata = Metadata(raw)
		}
	}
	var identifier *string
	if value, ok := wire["identifier"].(string); ok {
		identifier = &value
	}
	return NewDataType(typeName, metadata, identifier), nil
}

// FundingRateUpdate is included here because the source Data FFI conversion
// explicitly rejects this variant.
type FundingRateUpdate struct {
	InstrumentID  InstrumentID
	Rate          decimal.Decimal
	Interval      *uint16
	NextFundingNS *UnixNanos
	TsEvent       UnixNanos
	TsInit        UnixNanos
}

func NewFundingRateUpdate(
	instrumentID InstrumentID,
	rate decimal.Decimal,
	interval *uint16,
	nextFundingNS *UnixNanos,
	tsEvent, tsInit UnixNanos,
) FundingRateUpdate {
	return FundingRateUpdate{
		InstrumentID:  instrumentID,
		Rate:          rate,
		Interval:      interval,
		NextFundingNS: nextFundingNS,
		TsEvent:       tsEvent,
		TsInit:        tsInit,
	}
}

// DataFFI is the marker for the source's restricted FFI-safe data union.
type DataFFI struct{}

func DataFFIFromFundingRateUpdate(FundingRateUpdate) (DataFFI, error) {
	return DataFFI{}, errors.New("Cannot convert Data::FundingRateUpdate to DataFFI")
}

func metadataTopicSuffix(metadata Metadata) string {
	keys := make([]string, 0, len(metadata))
	for key := range metadata {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+metadataValueString(metadata[key]))
	}
	return strings.Join(parts, ".")
}

func metadataValueString(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case json.Number:
		return typed.String()
	case nil:
		return "null"
	case bool:
		return strconv.FormatBool(typed)
	case float64:
		return strconv.FormatFloat(typed, 'g', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(typed), 'g', -1, 32)
	case int:
		return strconv.Itoa(typed)
	case int64:
		return strconv.FormatInt(typed, 10)
	case uint:
		return strconv.FormatUint(uint64(typed), 10)
	case uint64:
		return strconv.FormatUint(typed, 10)
	default:
		data, err := json.Marshal(value)
		if err != nil {
			return ""
		}
		return string(data)
	}
}

func metadataString(metadata Metadata, key string) (string, bool) {
	if metadata == nil {
		return "", false
	}
	value, ok := metadata[key]
	if !ok {
		return "", false
	}
	text, ok := value.(string)
	return text, ok
}

func metadataUnixNanos(metadata Metadata, key string) (UnixNanos, bool) {
	value, ok := metadataString(metadata, key)
	if !ok {
		return 0, false
	}
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		panic(fmt.Sprintf("Invalid `UnixNanos` for '%s'", key))
	}
	return UnixNanos(parsed), true
}

func cloneMetadata(metadata Metadata) Metadata {
	if metadata == nil {
		return nil
	}
	data, err := json.Marshal(metadata)
	if err != nil {
		panic(fmt.Sprintf("clone metadata: %v", err))
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var cloned Metadata
	if err := decoder.Decode(&cloned); err != nil {
		panic(fmt.Sprintf("clone metadata: %v", err))
	}
	return cloned
}

func cloneStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func hashDataTypeTopic(topic string) uint64 {
	hasher := fnv.New64a()
	_, _ = hasher.Write([]byte(topic))
	return hasher.Sum64()
}

func MetadataEqual(left, right Metadata) bool {
	return reflect.DeepEqual(left, right)
}
