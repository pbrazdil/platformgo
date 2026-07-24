// Package currency models currency metadata and registry-based lookup without
// process-global mutable state.
package currency

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
)

const maxCurrencyPrecision uint8 = 18

// Type identifies the broad category of a currency.
type Type uint8

const (
	Crypto Type = iota + 1
	Fiat
	CommodityBacked
)

func (t Type) String() string {
	switch t {
	case Crypto:
		return "CRYPTO"
	case Fiat:
		return "FIAT"
	case CommodityBacked:
		return "COMMODITY_BACKED"
	default:
		return fmt.Sprintf("Type(%d)", t)
	}
}

// Currency is currency metadata. Its identity is Code; the remaining fields
// describe the code and do not participate in Equal.
type Currency struct {
	Code      string
	Precision uint8
	ISO4217   uint16
	Name      string
	Type      Type
}

// New validates and constructs a currency.
func New(code string, precision uint8, iso4217 uint16, name string, currencyType Type) (Currency, error) {
	if code == "" {
		return Currency{}, errors.New("code must not be empty")
	}
	if strings.TrimSpace(code) == "" {
		return Currency{}, errors.New("code must not contain only whitespace")
	}
	if name == "" {
		return Currency{}, errors.New("name must not be empty")
	}
	if precision > maxCurrencyPrecision {
		return Currency{}, fmt.Errorf(
			"precision exceeded maximum FIXED_PRECISION (%d), was %d",
			maxCurrencyPrecision,
			precision,
		)
	}
	return Currency{
		Code:      code,
		Precision: precision,
		ISO4217:   iso4217,
		Name:      name,
		Type:      currencyType,
	}, nil
}

// MustNew is New for source-derived constants and test fixtures.
func MustNew(code string, precision uint8, iso4217 uint16, name string, currencyType Type) Currency {
	value, err := New(code, precision, iso4217, name, currencyType)
	if err != nil {
		panic(err)
	}
	return value
}

// Equal compares currency identity by code only.
func (c Currency) Equal(other Currency) bool {
	return c.Code == other.Code
}

// String returns the currency code.
func (c Currency) String() string {
	return c.Code
}

// DebugString returns the source model's diagnostic representation.
func (c Currency) DebugString() string {
	return fmt.Sprintf(
		"Currency(code='%s', precision=%d, iso4217=%d, name='%s', currency_type=%s)",
		c.Code,
		c.Precision,
		c.ISO4217,
		c.Name,
		c.Type,
	)
}

// MarshalJSON encodes a currency by code, matching its external wire form.
func (c Currency) MarshalJSON() ([]byte, error) {
	return json.Marshal(c.Code)
}

// UnknownCodeError reports a currency code absent from a registry.
type UnknownCodeError struct {
	Code string
}

func (e UnknownCodeError) Error() string {
	return "Unknown currency: " + e.Code
}

// LockFailureError preserves the source model's typed lock-failure diagnostic.
// Go mutexes do not poison, so normal Registry operations never produce it.
type LockFailureError struct {
	Reason string
}

func (e LockFailureError) Error() string {
	return "Failed to acquire lock on `CURRENCY_MAP`: " + e.Reason
}

// Registry owns a set of currencies. Each fixture or application composition
// creates its own registry so tests cannot leak state through a singleton.
type Registry struct {
	mu         sync.RWMutex
	currencies map[string]Currency
}

// NewRegistry constructs an empty registry and registers initial values.
func NewRegistry(initial ...Currency) *Registry {
	registry := &Registry{currencies: make(map[string]Currency, len(initial))}
	for _, value := range initial {
		registry.currencies[value.Code] = value
	}
	return registry
}

// NewDefaultRegistry constructs a registry containing the built-ins required
// by the native model tests.
func NewDefaultRegistry() *Registry {
	return NewRegistry(AUD(), USD(), BTC(), USDT())
}

// Register adds a currency. Existing metadata is retained unless overwrite is
// true.
func (r *Registry) Register(value Currency, overwrite bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.currencies[value.Code]; exists && !overwrite {
		return
	}
	r.currencies[value.Code] = value
}

// TryLookup returns the registered currency and whether it exists.
func (r *Registry) TryLookup(code string) (Currency, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	value, ok := r.currencies[code]
	return value, ok
}

// Lookup returns the registered currency or an UnknownCodeError.
func (r *Registry) Lookup(code string) (Currency, error) {
	value, ok := r.TryLookup(code)
	if !ok {
		return Currency{}, UnknownCodeError{Code: code}
	}
	return value, nil
}

// MustLookup is Lookup for callers whose contract requires a known code.
func (r *Registry) MustLookup(code string) Currency {
	value, err := r.Lookup(code)
	if err != nil {
		panic(err)
	}
	return value
}

// IsFiat reports whether code identifies a fiat currency.
func (r *Registry) IsFiat(code string) (bool, error) {
	value, err := r.Lookup(code)
	return value.Type == Fiat, err
}

// IsCrypto reports whether code identifies a cryptocurrency.
func (r *Registry) IsCrypto(code string) (bool, error) {
	value, err := r.Lookup(code)
	return value.Type == Crypto, err
}

// IsCommodityBacked reports whether code identifies a commodity-backed
// currency.
func (r *Registry) IsCommodityBacked(code string) (bool, error) {
	value, err := r.Lookup(code)
	return value.Type == CommodityBacked, err
}

// GetOrCreateCrypto resolves code or registers adapter-friendly crypto
// metadata for a newly listed asset.
func (r *Registry) GetOrCreateCrypto(code string) Currency {
	if value, ok := r.TryLookup(code); ok {
		return value
	}

	value := MustNew(code, 8, 0, code, Crypto)
	r.Register(value, false)

	// Another caller may have won the registration race.
	registered, _ := r.TryLookup(code)
	return registered
}

// GetOrCreateCryptoWithContext trims adapter input and falls back to USDT for
// an empty code. Context is accepted for callers' logging context.
func (r *Registry) GetOrCreateCryptoWithContext(code string, _ string) Currency {
	trimmed := strings.TrimSpace(code)
	if trimmed == "" {
		if value, ok := r.TryLookup("USDT"); ok {
			return value
		}
		value := USDT()
		r.Register(value, false)
		return value
	}
	return r.GetOrCreateCrypto(trimmed)
}

// CurrencyFromJSON resolves a code-only JSON currency through this registry.
func (r *Registry) CurrencyFromJSON(data []byte) (Currency, error) {
	var code string
	if err := json.Unmarshal(data, &code); err != nil {
		return Currency{}, err
	}
	return r.Lookup(code)
}

// AUD returns the built-in Australian dollar metadata.
func AUD() Currency {
	return Currency{Code: "AUD", Precision: 2, ISO4217: 36, Name: "Australian dollar", Type: Fiat}
}

// USD returns the built-in United States dollar metadata.
func USD() Currency {
	return Currency{Code: "USD", Precision: 2, ISO4217: 840, Name: "United States dollar", Type: Fiat}
}

// BTC returns the built-in Bitcoin metadata.
func BTC() Currency {
	return Currency{Code: "BTC", Precision: 8, Name: "Bitcoin", Type: Crypto}
}

// USDT returns the built-in Tether metadata.
func USDT() Currency {
	return Currency{Code: "USDT", Precision: 8, Name: "Tether", Type: Crypto}
}
