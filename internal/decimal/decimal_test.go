package decimal

import "testing"

func TestParsePreservesScaleAndNeverUsesFloat(t *testing.T) {
	tests := []struct {
		input string
		want  string
		scale uint8
	}{
		{"0", "0", 0},
		{"-0.00", "0.00", 2},
		{"123.4500", "123.4500", 4},
		{".5", "0.5", 1},
		{"1_000.25", "1000.25", 2},
		{"1.25e2", "125", 0},
		{"1.25e-2", "0.0125", 4},
	}
	for _, test := range tests {
		got, err := Parse(test.input)
		if err != nil {
			t.Fatalf("Parse(%q): %v", test.input, err)
		}
		if got.String() != test.want || got.Scale() != test.scale {
			t.Errorf("Parse(%q) = %s scale %d, want %s scale %d", test.input, got, got.Scale(), test.want, test.scale)
		}
	}
}
