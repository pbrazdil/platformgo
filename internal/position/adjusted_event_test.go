package position

import (
	"encoding/json"
	"testing"

	"github.com/upcomers-org/platformgo/internal/currency"
	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
	"github.com/upcomers-org/platformgo/internal/money"
)

func testCommissionAdjustment() PositionAdjusted {
	quantityChange := decimal.MustParse("-0.001")
	reason := "O-123"
	return NewPositionAdjusted(
		ids.MustTraderID("TRADER-001"),
		ids.MustStrategyID("EMA-CROSS"),
		ids.MustInstrumentID("BTCUSDT.BINANCE"),
		ids.MustPositionID("P-001"),
		ids.MustAccountID("BINANCE-001"),
		Commission,
		&quantityChange,
		nil,
		&reason,
		"00000000-0000-4000-8000-000000000000",
		1_000_000_000,
		2_000_000_000,
	)
}

func testFundingAdjustment() PositionAdjusted {
	pnl := money.MustNew("-5.50", currency.USD())
	reason := "funding_2024_01_15_08:00"
	return NewPositionAdjusted(
		ids.MustTraderID("TRADER-001"),
		ids.MustStrategyID("EMA-CROSS"),
		ids.MustInstrumentID("BTCUSD-PERP.BINANCE"),
		ids.MustPositionID("P-002"),
		ids.MustAccountID("BINANCE-001"),
		Funding,
		nil,
		&pnl,
		&reason,
		"00000000-0000-4000-8000-000000000000",
		1_000_000_000,
		2_000_000_000,
	)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/position/adjusted.rs:155
//	test: test_position_adjustment_different_types
func TestPositionAdjustmentDifferentTypes(t *testing.T) {
	commission := testCommissionAdjustment()
	funding := testFundingAdjustment()
	if commission.AdjustmentType != Commission {
		t.Fatalf("commission type = %s", commission.AdjustmentType)
	}
	if funding.AdjustmentType != Funding {
		t.Fatalf("funding type = %s", funding.AdjustmentType)
	}
	if commission.AdjustmentType == funding.AdjustmentType {
		t.Fatal("commission and funding adjustment types are equal")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/position/adjusted.rs:168
//	test: test_position_adjustment_serialization
func TestPositionAdjustmentSerialization(t *testing.T) {
	original := testCommissionAdjustment()
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var restored PositionAdjusted
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !original.Equal(restored) {
		t.Fatalf("round trip = %#v, want %#v", restored, original)
	}
}
