package order

import (
	"testing"

	"github.com/upcomers-org/platformgo/internal/decimal"
)

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/builder.rs:731
//	test: normalizes_an_absent_contingency_type
func TestOrderBuilderNormalizesAbsentContingencyType(t *testing.T) {
	order := NewTestOrderBuilder(OrderTypeLimit).
		Instrument("AUDUSD.SIM").
		Quantity(decimal.MustQuantity("1")).
		Price(decimal.MustPrice("1")).
		Build()
	if order.ContingencyType != ContingencyTypeNoContingency || !order.IsContingency() {
		t.Fatalf("contingency = %v/%v", order.ContingencyType, order.IsContingency())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/builder.rs:746
//	test: preserves_a_configured_contingency_type
func TestOrderBuilderPreservesConfiguredContingencyType(t *testing.T) {
	order := NewTestOrderBuilder(OrderTypeLimit).
		Instrument("AUDUSD.SIM").
		Quantity(decimal.MustQuantity("1")).
		Price(decimal.MustPrice("1")).
		Contingency(ContingencyTypeOTO).
		Build()
	if order.ContingencyType != ContingencyTypeOTO || !order.IsContingency() {
		t.Fatalf("contingency = %v/%v", order.ContingencyType, order.IsContingency())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/builder.rs:759
//	test: submits_to_the_account_issuer
func TestOrderBuilderSubmitsToAccountIssuer(t *testing.T) {
	order := NewTestOrderBuilder(OrderTypeMarket).
		Instrument("AUDUSD.SIM").
		Quantity(decimal.MustQuantity("1")).
		Submit(true).
		Build()
	if order.AccountID == nil || *order.AccountID != "ACCOUNT-001" {
		t.Fatalf("account = %v", order.AccountID)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/stubs.rs:713
//	test: preserves_legacy_fill_defaults
func TestOrderFilledStubPreservesLegacyDefaults(t *testing.T) {
	order := NewTestOrderBuilder(OrderTypeMarket).
		Instrument("AUDUSD.SIM").
		Quantity(decimal.MustQuantity("1")).
		Build()
	fill := NewOrderFilledStub(order).Build()
	if fill.PositionID == nil || *fill.PositionID != "1" || fill.Commission == nil ||
		fill.Commission.String() != "2.00 USD" || fill.LiquiditySide != LiquiditySideMaker {
		t.Fatalf("fill = %#v", fill)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/orders/stubs.rs:730
//	test: can_omit_position_id_and_commission
func TestOrderFilledStubCanOmitPositionAndCommission(t *testing.T) {
	order := NewTestOrderBuilder(OrderTypeMarket).
		Instrument("AUDUSD.SIM").
		Quantity(decimal.MustQuantity("1")).
		Build()
	fill := NewOrderFilledStub(order).WithoutPositionID().WithoutCommission().Build()
	if fill.PositionID != nil || fill.Commission != nil {
		t.Fatalf("fill = %#v", fill)
	}
}
