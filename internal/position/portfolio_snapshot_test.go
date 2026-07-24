package position

import (
	"encoding/json"
	"testing"

	"github.com/upcomers-org/platformgo/internal/currency"
	"github.com/upcomers-org/platformgo/internal/ids"
	"github.com/upcomers-org/platformgo/internal/money"
)

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/portfolio/snapshot.rs:175
//	test: test_deserialize_legacy_snapshot_defaults_valuation_metadata
func TestDeserializeLegacyPortfolioSnapshotDefaultsValuationMetadata(t *testing.T) {
	baseCurrency := currency.USD()
	baseEquity := money.MustNew("100.00", currency.USD())
	snapshot := NewPortfolioSnapshot(
		ids.MustAccountID("SIM-001"),
		"CASH",
		&baseCurrency,
		[]money.Money{baseEquity},
		&baseEquity,
		true,
		[]ids.InstrumentID{ids.MustInstrumentID("AUDUSD.SIM")},
		[]currency.Currency{currency.AUD()},
		[]ids.InstrumentID{ids.MustInstrumentID("GBPUSD.SIM")},
		"00000000-0000-4000-8000-000000000001",
		1,
		2,
	)
	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var legacy map[string]json.RawMessage
	if err := json.Unmarshal(data, &legacy); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	delete(legacy, "base_currency_equity")
	delete(legacy, "is_stale")
	delete(legacy, "stale_instruments")
	delete(legacy, "stale_currencies")
	delete(legacy, "unpriced_instruments")
	data, err = json.Marshal(legacy)
	if err != nil {
		t.Fatalf("marshal legacy: %v", err)
	}

	var decoded PortfolioSnapshot
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal legacy: %v", err)
	}
	if decoded.BaseCurrencyEquity != nil {
		t.Fatalf("base currency equity = %v, want nil", decoded.BaseCurrencyEquity)
	}
	if decoded.IsStale {
		t.Fatal("legacy snapshot defaulted to stale")
	}
	if len(decoded.StaleInstruments) != 0 || len(decoded.StaleCurrencies) != 0 || len(decoded.UnpricedInstruments) != 0 {
		t.Fatalf("legacy valuation metadata = %#v, %#v, %#v", decoded.StaleInstruments, decoded.StaleCurrencies, decoded.UnpricedInstruments)
	}
}
