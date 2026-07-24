package market

import (
	"bytes"
	"encoding/json"
	"fmt"
)

type CustomRegistryValue struct {
	TypeName string
	TsInit   uint64
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
		TypeName string `json:"type_name"`
	} `json:"data_type"`
	Payload json.RawMessage `json:"payload"`
}

func EncodeCustomEnvelope(typeName string, payload any) ([]byte, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	envelope := customJSONEnvelope{Type: "CustomData", Payload: raw}
	envelope.DataType.TypeName = typeName
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
	return value, nil
}

func DecodeCustomJSON(payload json.RawMessage, target any, denyUnknownFields bool) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	if denyUnknownFields {
		decoder.DisallowUnknownFields()
	}
	return decoder.Decode(target)
}
