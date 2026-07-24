package market

import (
	"bytes"
	"encoding/json"
	"fmt"
)

type CustomRegistryValue struct {
	TypeName     string
	Metadata     map[string]string
	Identifier   *string
	TsInit       uint64
	InstrumentID InstrumentID
	Payload      any
}

type CustomJSONDeserializer func(json.RawMessage) (CustomRegistryValue, error)

type CustomArrowRegistration struct {
	Schema string
}

type CustomDataRegistry struct {
	json  map[string]CustomJSONDeserializer
	arrow map[string]CustomArrowRegistration
}

func NewCustomDataRegistry() *CustomDataRegistry {
	return &CustomDataRegistry{
		json:  make(map[string]CustomJSONDeserializer),
		arrow: make(map[string]CustomArrowRegistration),
	}
}

func (registry *CustomDataRegistry) RegisterJSON(typeName string, decoder CustomJSONDeserializer) error {
	if _, exists := registry.json[typeName]; exists {
		return fmt.Errorf("custom data type %q is already registered for JSON", typeName)
	}
	registry.json[typeName] = decoder
	return nil
}

func (registry *CustomDataRegistry) EnsureJSON(typeName string, decoder CustomJSONDeserializer) error {
	if _, exists := registry.json[typeName]; !exists {
		registry.json[typeName] = decoder
	}
	return nil
}

func (registry *CustomDataRegistry) EnsureArrow(typeName string, registration CustomArrowRegistration) error {
	if _, exists := registry.arrow[typeName]; !exists {
		registry.arrow[typeName] = registration
	}
	return nil
}

type customJSONEnvelope struct {
	Type     string `json:"type"`
	DataType struct {
		TypeName   string            `json:"type_name"`
		Metadata   map[string]string `json:"metadata"`
		Identifier *string           `json:"identifier,omitempty"`
	} `json:"data_type"`
	Payload json.RawMessage `json:"payload"`
}

func EncodeCustomEnvelope(typeName string, payload any) ([]byte, error) {
	return EncodeRegisteredCustom(typeName, nil, nil, payload)
}

func EncodeRegisteredCustom(
	typeName string,
	metadata map[string]string,
	identifier *string,
	payload any,
) ([]byte, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	envelope := customJSONEnvelope{Type: "CustomData", Payload: raw}
	envelope.DataType.TypeName = typeName
	envelope.DataType.Metadata = cloneStringMap(metadata)
	envelope.DataType.Identifier = cloneRegistryStringPointer(identifier)
	return json.Marshal(envelope)
}

func (registry *CustomDataRegistry) DeserializeJSON(data []byte) (CustomRegistryValue, error) {
	var envelope customJSONEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return CustomRegistryValue{}, err
	}
	decoder, exists := registry.json[envelope.DataType.TypeName]
	if !exists {
		return CustomRegistryValue{}, fmt.Errorf("custom data type %q is not registered", envelope.DataType.TypeName)
	}
	value, err := decoder(envelope.Payload)
	if err != nil {
		return CustomRegistryValue{}, err
	}
	value.TypeName = envelope.DataType.TypeName
	value.Metadata = cloneStringMap(envelope.DataType.Metadata)
	value.Identifier = cloneRegistryStringPointer(envelope.DataType.Identifier)
	return value, nil
}

func DecodeCustomJSON(payload json.RawMessage, target any, denyUnknownFields bool) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	if denyUnknownFields {
		decoder.DisallowUnknownFields()
	}
	return decoder.Decode(target)
}

type CustomDataType struct {
	TypeName   string
	Metadata   map[string]string
	Identifier *string
}

type CustomDataRecord struct {
	DataType     CustomDataType
	Payload      any
	tsInit       uint64
	instrumentID InstrumentID
}

func NewCustomDataRecord(
	dataType CustomDataType,
	payload any,
	tsInit uint64,
	instrumentID InstrumentID,
) CustomDataRecord {
	dataType.Metadata = cloneStringMap(dataType.Metadata)
	dataType.Identifier = cloneRegistryStringPointer(dataType.Identifier)
	return CustomDataRecord{
		DataType: dataType, Payload: payload, tsInit: tsInit, instrumentID: instrumentID,
	}
}

func (record CustomDataRecord) TsInit() uint64 {
	return record.tsInit
}

func (record CustomDataRecord) InstrumentID() InstrumentID {
	return record.instrumentID
}

func cloneStringMap(value map[string]string) map[string]string {
	if value == nil {
		return nil
	}
	cloned := make(map[string]string, len(value))
	for key, item := range value {
		cloned[key] = item
	}
	return cloned
}

func cloneRegistryStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
