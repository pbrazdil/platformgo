package position

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/upcomers-org/platformgo/internal/currency"
	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
	"github.com/upcomers-org/platformgo/internal/money"
)

func testSnapshotIdentity() PositionIdentity {
	return PositionIdentity{
		TraderID:   ids.MustTraderID("TRADER-001"),
		StrategyID: ids.MustStrategyID("EMA-CROSS"),
		AccountID:  ids.MustAccountID("SIM-001"),
	}
}

func testSnapshotPosition(t *testing.T) *Position {
	t.Helper()
	instrument := audusd()
	aud := currency.AUD()
	instrument.CurrencyPair = true
	instrument.BaseCurrency = &aud
	position, err := New(instrument, "P-001", Fill{
		ClientOrderID: "O-19700101-000000-001-001-1",
		TradeID:       "T-001",
		Side:          Buy,
		Quantity:      decimal.MustParse("100"),
		Price:         decimal.MustParse("0.8000"),
		Commission:    cashPtr("2.0", usd),
		TsEvent:       1_000_000_000,
		TsInit:        2_000_000_000,
	})
	if err != nil {
		t.Fatal(err)
	}
	return position
}

func testPositionSnapshot() PositionSnapshot {
	eur := currency.MustNew("EUR", 2, 978, "Euro", currency.Fiat)
	closingOrderID := ids.MustClientOrderID("O-19700101-000000-001-001-2")
	averageClose := decimal.MustParse("1.0600")
	realizedReturn := decimal.MustParse("0.0095")
	realizedPnL := cash("100.0", usd)
	unrealizedPnL := cash("50.0", usd)
	duration := uint64(3_600_000_000_000)
	tsClosed := uint64(4_600_000_000)
	return PositionSnapshot{
		TraderID:           ids.MustTraderID("TRADER-001"),
		StrategyID:         ids.MustStrategyID("EMA-CROSS"),
		InstrumentID:       ids.MustInstrumentID("EURUSD.SIM"),
		PositionID:         ids.MustPositionID("P-001"),
		AccountID:          ids.MustAccountID("SIM-001"),
		OpeningOrderID:     ids.MustClientOrderID("O-19700101-000000-001-001-1"),
		ClosingOrderID:     &closingOrderID,
		Entry:              Buy,
		Side:               Long,
		SignedQuantity:     decimal.MustParse("100.0"),
		Quantity:           decimal.MustQuantity("100"),
		PeakQuantity:       decimal.MustQuantity("100"),
		QuoteCurrency:      usd,
		BaseCurrency:       &eur,
		SettlementCurrency: usd,
		AverageOpen:        decimal.MustParse("1.0500"),
		AverageClose:       &averageClose,
		RealizedReturn:     &realizedReturn,
		RealizedPnL:        &realizedPnL,
		UnrealizedPnL:      &unrealizedPnL,
		Commissions:        []money.Money{cash("2.0", usd)},
		Duration:           &duration,
		TsOpened:           1_000_000_000,
		TsClosed:           &tsClosed,
		TsInit:             2_000_000_000,
		TsLast:             4_600_000_000,
		ReplayState:        nil,
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/position/snapshot.rs:204
//	test: test_position_snapshot_from
func TestPositionSnapshotFrom(t *testing.T) {
	position := testSnapshotPosition(t)
	unrealizedPnL := cash("75.0", usd)
	snapshot := SnapshotFromPosition(position, testSnapshotIdentity(), &unrealizedPnL)

	if snapshot.TraderID != ids.MustTraderID("TRADER-001") ||
		snapshot.StrategyID != ids.MustStrategyID("EMA-CROSS") ||
		snapshot.InstrumentID != ids.MustInstrumentID(position.Instrument.ID) ||
		snapshot.PositionID != ids.MustPositionID(position.ID) ||
		snapshot.AccountID != ids.MustAccountID("SIM-001") ||
		snapshot.OpeningOrderID != ids.MustClientOrderID(position.OpeningOrderID) {
		t.Fatal("snapshot identity differs from position")
	}
	if snapshot.ClosingOrderID != nil || snapshot.Entry != position.Entry || snapshot.Side != position.Side ||
		!snapshot.SignedQuantity.Equal(position.SignedQuantity) ||
		snapshot.Quantity.String() != position.Quantity.String() ||
		snapshot.PeakQuantity.String() != position.PeakQuantity.String() ||
		!snapshot.QuoteCurrency.Equal(position.Instrument.QuoteCurrency) ||
		snapshot.BaseCurrency == nil || position.Instrument.BaseCurrency == nil ||
		!snapshot.BaseCurrency.Equal(*position.Instrument.BaseCurrency) ||
		!snapshot.SettlementCurrency.Equal(position.Instrument.SettlementCurrency) ||
		!snapshot.AverageOpen.Equal(position.AverageOpen) ||
		snapshot.AverageClose != nil {
		t.Fatal("snapshot accounting state differs from position")
	}
	if snapshot.RealizedReturn == nil || !snapshot.RealizedReturn.Equal(position.RealizedReturn) ||
		snapshot.RealizedPnL == nil || position.RealizedPnL == nil ||
		!snapshot.RealizedPnL.Equal(*position.RealizedPnL) ||
		snapshot.UnrealizedPnL == nil || !snapshot.UnrealizedPnL.Equal(unrealizedPnL) ||
		snapshot.Duration == nil || *snapshot.Duration != position.Duration ||
		snapshot.TsOpened != position.TsOpened || snapshot.TsClosed != nil ||
		snapshot.TsInit != position.TsInit || snapshot.TsLast != position.TsLast ||
		snapshot.ReplayState != nil {
		t.Fatal("snapshot PnL or timestamp state differs from position")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/position/snapshot.rs:241
//	test: test_position_snapshot_from_with_no_unrealized_pnl
func TestPositionSnapshotFromWithNoUnrealizedPnL(t *testing.T) {
	position := testSnapshotPosition(t)
	snapshot := SnapshotFromPosition(position, testSnapshotIdentity(), nil)
	if snapshot.UnrealizedPnL != nil {
		t.Fatalf("got %s, want nil", snapshot.UnrealizedPnL)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/position/snapshot.rs:252
//	test: test_position_snapshot_from_replay_state
func TestPositionSnapshotFromReplayState(t *testing.T) {
	position := testSnapshotPosition(t)
	snapshot, err := SnapshotFromReplayState(position, testSnapshotIdentity(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var restored Position
	if err := json.Unmarshal(snapshot.ReplayState, &restored); err != nil {
		t.Fatal(err)
	}
	originalJSON, err := json.Marshal(position)
	if err != nil {
		t.Fatal(err)
	}
	restoredJSON, err := json.Marshal(restored)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(restoredJSON, originalJSON) {
		t.Fatalf("restored position differs:\n%s\n%s", restoredJSON, originalJSON)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/position/snapshot.rs:264
//	test: test_position_snapshot_serialization
func TestPositionSnapshotSerialization(t *testing.T) {
	original := testPositionSnapshot()
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var restored PositionSnapshot
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	if !original.Equal(restored) {
		t.Fatalf("round-trip changed snapshot:\n%s\n%s", mustSnapshotJSON(original), mustSnapshotJSON(restored))
	}
}

func mustSnapshotJSON(snapshot PositionSnapshot) string {
	data, err := json.Marshal(snapshot)
	if err != nil {
		panic(err)
	}
	return string(data)
}
