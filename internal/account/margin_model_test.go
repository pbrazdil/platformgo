package account

import (
	"testing"

	"github.com/upcomers-org/platformgo/internal/currency"
	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
)

func ethUSDTMarginModelInstrument() MarginInstrument {
	base := currency.MustNew("ETH", 8, 0, "Ether", currency.Crypto)
	return MarginInstrument{
		ID:                ids.MustInstrumentID("ETHUSDT-PERP.BINANCE"),
		BaseCurrency:      &base,
		QuoteCurrency:     currency.USDT(),
		Multiplier:        decimal.MustParse("1"),
		InitialMarginRate: decimal.MustParse("1.0"),
		MaintenanceRate:   decimal.MustParse("0.35"),
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/margin_model.rs:273
//	test: test_leveraged_initial_margin
func TestLeveragedMarginModelInitialMargin(t *testing.T) {
	model := LeveragedMarginModel{}
	instrument := ethUSDTMarginModelInstrument()
	quantity := decimal.MustParse("10.000")
	price := decimal.MustParse("5000.00")
	leverage := decimal.MustParse("10")

	margin, err := model.CalculateInitialMargin(instrument, quantity, price, leverage, false)
	if err != nil {
		t.Fatalf("CalculateInitialMargin() error = %v", err)
	}
	expected, err := decimal.MustParse("50000").Quo(
		leverage,
		decimal.MaxPrecision,
		decimal.RoundHalfEven,
	)
	if err != nil {
		t.Fatalf("expected division error = %v", err)
	}
	expected = expected.Mul(instrument.InitialMarginRate)
	if !margin.Decimal().Equal(expected) {
		t.Fatalf("margin decimal = %s, want %s", margin.Decimal(), expected)
	}
	if !margin.Currency().Equal(currency.USDT()) {
		t.Fatalf("margin currency = %s", margin.Currency())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/margin_model.rs:292
//	test: test_standard_ignores_leverage
func TestStandardMarginModelIgnoresLeverage(t *testing.T) {
	model := StandardMarginModel{}
	instrument := ethUSDTMarginModelInstrument()
	quantity := decimal.MustParse("10.000")
	price := decimal.MustParse("5000.00")

	marginLow, err := model.CalculateInitialMargin(
		instrument,
		quantity,
		price,
		decimal.MustParse("2"),
		false,
	)
	if err != nil {
		t.Fatalf("low-leverage CalculateInitialMargin() error = %v", err)
	}
	marginHigh, err := model.CalculateInitialMargin(
		instrument,
		quantity,
		price,
		decimal.MustParse("100"),
		false,
	)
	if err != nil {
		t.Fatalf("high-leverage CalculateInitialMargin() error = %v", err)
	}
	if !marginLow.Equal(marginHigh) {
		t.Fatalf("margins differ: %s != %s", marginLow, marginHigh)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/margin_model.rs:310
//	test: test_leveraged_zero_leverage_errors
func TestLeveragedMarginModelZeroLeverageErrors(t *testing.T) {
	model := LeveragedMarginModel{}
	instrument := ethUSDTMarginModelInstrument()

	_, err := model.CalculateInitialMargin(
		instrument,
		decimal.MustParse("1.000"),
		decimal.MustParse("5000.00"),
		decimal.Decimal{},
		false,
	)
	if err == nil {
		t.Fatal("CalculateInitialMargin() error = nil")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/margin_model.rs:326
//	test: test_leveraged_margin_decimal_overflow_returns_error
func TestLeveragedMarginModelDecimalOverflowReturnsError(t *testing.T) {
	model := LeveragedMarginModel{}
	instrument := ethUSDTMarginModelInstrument()

	_, err := model.CalculateInitialMargin(
		instrument,
		decimal.MustParse("1.000"),
		decimal.MustParse("5000.00"),
		decimal.MustParse("0.0000000000000000000000000001"),
		false,
	)
	if err == nil || err.Error() != "initial margin calculation overflow" {
		t.Fatalf("CalculateInitialMargin() error = %v", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/margin_model.rs:345
//	test: test_margin_model_any_default_is_leveraged
func TestMarginModelAnyDefaultIsLeveraged(t *testing.T) {
	model := DefaultMarginModelAny()

	if !model.IsLeveraged() {
		t.Fatalf("default model = %T", model.model)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/accounts/margin_model.rs:351
//	test: test_maintenance_margin
func TestLeveragedMarginModelMaintenanceMargin(t *testing.T) {
	model := LeveragedMarginModel{}
	instrument := ethUSDTMarginModelInstrument()
	quantity := decimal.MustParse("10.000")
	price := decimal.MustParse("5000.00")
	leverage := decimal.MustParse("10")

	margin, err := model.CalculateMaintenanceMargin(instrument, quantity, price, leverage, false)
	if err != nil {
		t.Fatalf("CalculateMaintenanceMargin() error = %v", err)
	}
	expected, err := decimal.MustParse("50000").Quo(
		leverage,
		decimal.MaxPrecision,
		decimal.RoundHalfEven,
	)
	if err != nil {
		t.Fatalf("expected division error = %v", err)
	}
	expected = expected.Mul(instrument.MaintenanceRate)
	if !margin.Decimal().Equal(expected) {
		t.Fatalf("margin decimal = %s, want %s", margin.Decimal(), expected)
	}
}
