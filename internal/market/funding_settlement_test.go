package market

import (
	"encoding/json"
	"testing"

	"github.com/upcomers-org/platformgo/internal/currency"
	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
)

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/funding/settlement.rs:108
//	test: test_funding_settlement_serialization
func TestFundingSettlementSerialization(t *testing.T) {
	original := NewFundingSettlement(
		ids.MustTraderID("TRADER-001"),
		ids.MustInstrumentID("BTCUSDT-PERP.BINANCE"),
		ids.MustAccountID("BINANCE-001"),
		decimal.MustParse("0.0001"),
		decimal.MustParse("65000.00"),
		currency.USDT(),
		"00000000-0000-4000-8000-000000000000",
		1_000_000_000,
		2_000_000_000,
	)

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var restored FundingSettlement
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !original.Equal(restored) {
		t.Fatalf("round trip = %#v, want %#v", restored, original)
	}
}
