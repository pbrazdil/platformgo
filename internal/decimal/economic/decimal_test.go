package decimal

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestParseCanonicalEconomicDecimal(t *testing.T) {
	tests := []struct {
		input string
		want  string
		scale uint8
	}{
		{"0", "0", 0},
		{"0001.2300", "1.23", 2},
		{"-0.000", "0", 0},
		{"0.500", "0.5", 1},
		{"-12.3400", "-12.34", 2},
		{
			"12345678901234567890.123456789012345678",
			"12345678901234567890.123456789012345678",
			18,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.input, func(t *testing.T) {
			value, err := Parse(testCase.input)
			if err != nil {
				t.Fatalf("Parse(%q) error = %v", testCase.input, err)
			}
			if got := value.String(); got != testCase.want {
				t.Fatalf("Parse(%q).String() = %q, want %q", testCase.input, got, testCase.want)
			}
			if got := value.Scale(); got != testCase.scale {
				t.Fatalf("Parse(%q).Scale() = %d, want %d", testCase.input, got, testCase.scale)
			}
		})
	}
}

func TestParseRejectsMalformedOrOutOfBudgetEconomicDecimal(t *testing.T) {
	tests := []struct {
		input string
		is    error
	}{
		{"", ErrInvalidSyntax},
		{" ", ErrInvalidSyntax},
		{" 1", ErrInvalidSyntax},
		{"1 ", ErrInvalidSyntax},
		{"+1", ErrInvalidSyntax},
		{"e3", ErrInvalidSyntax},
		{"+e3", ErrInvalidSyntax},
		{".e3", ErrInvalidSyntax},
		{"-.e3", ErrInvalidSyntax},
		{".5", ErrInvalidSyntax},
		{"1.", ErrInvalidSyntax},
		{"1e3", ErrInvalidSyntax},
		{"1_000", ErrInvalidSyntax},
		{"NaN", ErrInvalidSyntax},
		{"Inf", ErrInvalidSyntax},
		{"--1", ErrInvalidSyntax},
		{"1.2.3", ErrInvalidSyntax},
		{"100000000000000000000", ErrPrecision},
		{"0.0000000000000000001", ErrScale},
		{"1.0000000000000000000", ErrScale},
		{"0.1000000000000000000", ErrScale},
		{"-0.0000000000000000000", ErrScale},
		{"12345678901234567890.1234567890123456789", ErrScale},
	}

	for _, testCase := range tests {
		t.Run(testCase.input, func(t *testing.T) {
			_, err := Parse(testCase.input)
			if !errors.Is(err, testCase.is) {
				t.Fatalf("Parse(%q) error = %v, want errors.Is(%v)", testCase.input, err, testCase.is)
			}
		})
	}
}

func TestParseWithMaxScaleRejectsBeforeCanonicalization(t *testing.T) {
	value, err := ParseWithMaxScale("1.23", 2)
	if err != nil {
		t.Fatal(err)
	}
	if value.String() != "1.23" {
		t.Fatalf("value = %s, want 1.23", value)
	}
	for _, input := range []string{"1.230", "0.000", "-1.000"} {
		if _, err := ParseWithMaxScale(input, 2); !errors.Is(err, ErrScale) {
			t.Fatalf("ParseWithMaxScale(%q) error = %v, want ErrScale", input, err)
		}
	}
	if _, err := ParseWithMaxScale("1", MaxScale+1); !errors.Is(err, ErrScale) {
		t.Fatalf("invalid max scale error = %v, want ErrScale", err)
	}
}

func TestExactArithmeticIsImmutableAndBudgeted(t *testing.T) {
	left := mustParse(t, "1.20")
	right := mustParse(t, "3.45")

	sum, err := left.Add(right)
	if err != nil {
		t.Fatal(err)
	}
	if got := sum.String(); got != "4.65" {
		t.Fatalf("sum = %s, want 4.65", got)
	}
	if got := left.String(); got != "1.2" {
		t.Fatalf("left mutated to %s", got)
	}
	if got := right.String(); got != "3.45" {
		t.Fatalf("right mutated to %s", got)
	}

	difference, err := right.Sub(left)
	if err != nil {
		t.Fatal(err)
	}
	if got := difference.String(); got != "2.25" {
		t.Fatalf("difference = %s, want 2.25", got)
	}

	product, err := mustParse(t, "1.25").Mul(mustParse(t, "0.2"))
	if err != nil {
		t.Fatal(err)
	}
	if got := product.String(); got != "0.25" {
		t.Fatalf("product = %s, want 0.25", got)
	}

	maximum := mustParse(t, "99999999999999999999.999999999999999999")
	if _, err := maximum.Add(mustParse(t, "0.000000000000000001")); !errors.Is(err, ErrPrecision) {
		t.Fatalf("overflowing Add() error = %v, want ErrPrecision", err)
	}
	if _, err := mustParse(t, "99999999999999999999").Mul(mustParse(t, "10")); !errors.Is(err, ErrPrecision) {
		t.Fatalf("overflowing Mul() error = %v, want ErrPrecision", err)
	}
}

func TestPolicyRoundingRequiresScaleModeAndOperation(t *testing.T) {
	tests := []struct {
		input string
		scale uint8
		mode  RoundingMode
		want  string
	}{
		{"1.005", 2, RoundHalfEven, "1"},
		{"1.015", 2, RoundHalfEven, "1.02"},
		{"-1.015", 2, RoundHalfEven, "-1.02"},
		{"-1.019", 2, RoundTowardZero, "-1.01"},
	}

	for _, testCase := range tests {
		value := mustParse(t, testCase.input)
		got, err := value.Quantize(testCase.scale, testCase.mode, "test.rounding")
		if err != nil {
			t.Fatalf("Quantize(%s) error = %v", testCase.input, err)
		}
		if got.String() != testCase.want {
			t.Fatalf("Quantize(%s) = %s, want %s", testCase.input, got, testCase.want)
		}
	}

	if _, err := mustParse(t, "1.005").Quantize(2, RoundHalfEven, ""); !errors.Is(err, ErrOperation) {
		t.Fatalf("unnamed Quantize() error = %v, want ErrOperation", err)
	}
	if _, err := mustParse(t, "1.005").Quantize(MaxScale+1, RoundHalfEven, "test"); !errors.Is(err, ErrScale) {
		t.Fatalf("over-scale Quantize() error = %v, want ErrScale", err)
	}
	if _, err := mustParse(t, "1").Quantize(2, RoundingMode(255), "test"); !errors.Is(err, ErrRoundingMode) {
		t.Fatalf("invalid-mode Quantize() error = %v, want ErrRoundingMode", err)
	}
	if _, err := mustParse(t, "1").Quantize(2, RoundingMode(0), "test"); !errors.Is(err, ErrRoundingMode) {
		t.Fatalf("omitted-mode Quantize() error = %v, want ErrRoundingMode", err)
	}
}

func TestQuantizedMultiplicationAndDivision(t *testing.T) {
	fee, err := MulQuantized(
		mustParse(t, "12.345"),
		mustParse(t, "0.01"),
		2,
		RoundHalfEven,
		"fee.usd",
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := fee.String(); got != "0.12" {
		t.Fatalf("fee = %s, want 0.12", got)
	}

	maximumFee, err := MulQuantized(
		mustParse(t, "99999999999999999999.999999999999999999"),
		mustParse(t, "0.01"),
		18,
		RoundHalfEven,
		"fee.maximum",
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := maximumFee.String(); got != "1000000000000000000" {
		t.Fatalf("maximum fee = %s, want 1000000000000000000", got)
	}

	tinyFee, err := MulQuantized(
		mustParse(t, "0.000000000000000001"),
		mustParse(t, "0.1"),
		18,
		RoundHalfEven,
		"fee.tiny",
	)
	if err != nil {
		t.Fatal(err)
	}
	if !tinyFee.IsZero() {
		t.Fatalf("tiny fee = %s, want 0", tinyFee)
	}

	for _, testCase := range []struct {
		left  string
		right string
		want  string
	}{
		{"1.005", "1", "1"},
		{"1.015", "1", "1.02"},
		{"-1.005", "1", "-1"},
		{"-1.015", "1", "-1.02"},
	} {
		rounded, roundErr := MulQuantized(
			mustParse(t, testCase.left),
			mustParse(t, testCase.right),
			2,
			RoundHalfEven,
			"fee.half",
		)
		if roundErr != nil {
			t.Fatal(roundErr)
		}
		if got := rounded.String(); got != testCase.want {
			t.Fatalf("%s * %s = %s, want %s", testCase.left, testCase.right, got, testCase.want)
		}
	}

	third, err := QuoQuantized(
		mustParse(t, "1"),
		mustParse(t, "3"),
		2,
		RoundHalfEven,
		"ratio.third",
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := third.String(); got != "0.33" {
		t.Fatalf("third = %s, want 0.33", got)
	}

	if _, err := QuoQuantized(
		mustParse(t, "1"),
		Decimal{},
		2,
		RoundHalfEven,
		"ratio.zero",
	); !errors.Is(err, ErrDivisionByZero) {
		t.Fatalf("division by zero error = %v, want ErrDivisionByZero", err)
	}

	for _, testCase := range []struct {
		name string
		call func() error
	}{
		{
			"multiply omitted mode",
			func() error {
				_, err := MulQuantized(mustParse(t, "1"), mustParse(t, "2"), 2, 0, "fee")
				return err
			},
		},
		{
			"divide omitted mode",
			func() error {
				_, err := QuoQuantized(mustParse(t, "1"), mustParse(t, "2"), 2, 0, "ratio")
				return err
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if err := testCase.call(); !errors.Is(err, ErrRoundingMode) {
				t.Fatalf("error = %v, want ErrRoundingMode", err)
			}
		})
	}

	overflowing := mustParse(t, "99999999999999999999.999999999999999999")
	if _, err := MulQuantized(overflowing, overflowing, 18, RoundHalfEven, ""); !errors.Is(err, ErrOperation) {
		t.Fatalf("unnamed overflowing MulQuantized error = %v, want ErrOperation", err)
	}
	if _, err := MulQuantized(overflowing, overflowing, 18, 0, "fee"); !errors.Is(err, ErrRoundingMode) {
		t.Fatalf("invalid-mode overflowing MulQuantized error = %v, want ErrRoundingMode", err)
	}
	if _, err := MulQuantized(overflowing, overflowing, 18, RoundHalfEven, "fee"); !errors.Is(err, ErrPrecision) {
		t.Fatalf("overflowing MulQuantized error = %v, want ErrPrecision", err)
	}
}

func TestEconomicDecimalJSONUsesStringsOnly(t *testing.T) {
	value := mustParse(t, "1.2300")
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); got != `"1.23"` {
		t.Fatalf("Marshal() = %s, want string decimal", got)
	}

	var decoded Decimal
	if err := json.Unmarshal([]byte(`"1.2300"`), &decoded); err != nil {
		t.Fatal(err)
	}
	if !decoded.Equal(value) {
		t.Fatalf("decoded = %s, want %s", decoded, value)
	}
	if err := json.Unmarshal([]byte(`1.23`), &decoded); !errors.Is(err, ErrInvalidSyntax) {
		t.Fatalf("numeric JSON error = %v, want ErrInvalidSyntax", err)
	}

	unchanged := mustParse(t, "9.87")
	for _, invalid := range []string{
		`"1.23" "4.56"`,
		`"1.23"x`,
		`[]`,
		`{}`,
		`true`,
		`null`,
	} {
		decoded = unchanged
		if err := decoded.UnmarshalJSON([]byte(invalid)); !errors.Is(err, ErrInvalidSyntax) {
			t.Fatalf("UnmarshalJSON(%q) error = %v, want ErrInvalidSyntax", invalid, err)
		}
		if !decoded.Equal(unchanged) {
			t.Fatalf("UnmarshalJSON(%q) mutated receiver to %s", invalid, decoded)
		}
	}
}

func TestEconomicDecimalZeroValue(t *testing.T) {
	var zero Decimal
	if !zero.IsZero() || zero.Sign() != 0 || zero.String() != "0" || zero.Scale() != 0 {
		t.Fatalf("zero value = %q sign=%d scale=%d", zero.String(), zero.Sign(), zero.Scale())
	}
}

func mustParse(t *testing.T, input string) Decimal {
	t.Helper()
	value, err := Parse(input)
	if err != nil {
		t.Fatalf("Parse(%q) error = %v", input, err)
	}
	return value
}
