package decimal

import (
	"math/big"
	"strings"
	"testing"
)

func FuzzParseCanonicalRoundTrip(f *testing.F) {
	for _, seed := range []string{
		"0",
		"-0.000",
		"0001.2300",
		"99999999999999999999.999999999999999999",
		"e3",
		"+e3",
		".e3",
		"-.e3",
		"1e3",
		"0.0000000000000000001",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, input string) {
		value, err := Parse(input)
		if err != nil {
			return
		}
		canonical := value.String()
		if strings.ContainsAny(canonical, "eE+_") || strings.TrimSpace(canonical) != canonical {
			t.Fatalf("non-canonical output %q from %q", canonical, input)
		}
		roundTrip, err := Parse(canonical)
		if err != nil {
			t.Fatalf("Parse(canonical %q) error = %v", canonical, err)
		}
		if !roundTrip.Equal(value) {
			t.Fatalf("round trip %s != %s", roundTrip, value)
		}
	})
}

func FuzzExactArithmeticIdentities(f *testing.F) {
	for _, seed := range []struct {
		leftCoefficient  int64
		rightCoefficient int64
		leftScale        uint8
		rightScale       uint8
	}{
		{0, 0, 0, 0},
		{12345, 6789, 2, 3},
		{-12345, 6789, 4, 1},
		{999999999, -999999999, 6, 6},
	} {
		f.Add(
			seed.leftCoefficient,
			seed.rightCoefficient,
			seed.leftScale,
			seed.rightScale,
		)
	}

	f.Fuzz(func(
		t *testing.T,
		leftCoefficient int64,
		rightCoefficient int64,
		leftScale uint8,
		rightScale uint8,
	) {
		leftScale %= 7
		rightScale %= 7
		left, err := NewScaled(big.NewInt(leftCoefficient), leftScale)
		if err != nil {
			t.Fatal(err)
		}
		right, err := NewScaled(big.NewInt(rightCoefficient), rightScale)
		if err != nil {
			t.Fatal(err)
		}

		sum, err := left.Add(right)
		if err != nil {
			return
		}
		recovered, err := sum.Sub(right)
		if err != nil {
			t.Fatal(err)
		}
		if !recovered.Equal(left) {
			t.Fatalf("(%s + %s) - %s = %s", left, right, right, recovered)
		}

		product, err := left.Mul(right)
		reversed, reverseErr := right.Mul(left)
		if (err == nil) != (reverseErr == nil) {
			t.Fatalf("multiply error asymmetry: %v versus %v", err, reverseErr)
		}
		if err == nil && !product.Equal(reversed) {
			t.Fatalf("%s * %s = %s but reverse = %s", left, right, product, reversed)
		}

		leftBefore := left.String()
		rightBefore := right.String()
		_, _ = MulQuantized(left, right, 6, RoundHalfEven, "fuzz.multiply")
		if left.String() != leftBefore || right.String() != rightBefore {
			t.Fatalf("arithmetic mutated operands: %s/%s became %s/%s", leftBefore, rightBefore, left, right)
		}
	})
}

func FuzzParseRejectsInvalidGrammar(f *testing.F) {
	for _, seed := range []string{
		"0",
		"1.23",
		"-99999999999999999999.999999999999999999",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, seed string) {
		value, err := Parse(seed)
		if err != nil {
			return
		}
		canonical := value.String()
		mutations := []string{
			"+" + canonical,
			" " + canonical,
			canonical + " ",
			canonical + "e0",
			canonical + "E0",
			canonical + "_0",
			canonical + ".",
			"." + canonical,
			"--" + canonical,
			canonical + string([]byte{0xff}),
		}
		for index := 0; index <= len(canonical); index++ {
			for _, forbidden := range []string{"e", "E", "+", "_", " "} {
				mutations = append(
					mutations,
					canonical[:index]+forbidden+canonical[index:],
				)
			}
		}
		if strings.Contains(canonical, ".") {
			mutations = append(mutations, strings.Replace(canonical, ".", "..", 1))
		}
		for _, mutation := range mutations {
			if _, err := Parse(mutation); err == nil {
				t.Fatalf("Parse(%q) succeeded; mutation of canonical %q must fail", mutation, canonical)
			}
		}

		for _, overScale := range []string{
			"1.0000000000000000000",
			"-0.0000000000000000000",
		} {
			if _, err := Parse(overScale); err == nil {
				t.Fatalf("Parse(%q) succeeded above scale limit", overScale)
			}
		}
	})
}
