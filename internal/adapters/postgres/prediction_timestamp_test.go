package postgres

import (
	"testing"
	"time"
)

func TestFormatPredictionRFC3339ChronoYearBounds(t *testing.T) {
	tests := []struct {
		name    string
		value   time.Time
		want    string
		wantErr bool
	}{
		{
			name:  "lower bound",
			value: time.Date(-262143, time.January, 1, 0, 0, 0, 0, time.UTC),
			want:  "-262143-01-01T00:00:00+00:00",
		},
		{
			name:  "upper bound",
			value: time.Date(262142, time.December, 31, 23, 59, 59, 0, time.UTC),
			want:  "+262142-12-31T23:59:59+00:00",
		},
		{
			name:    "below lower bound",
			value:   time.Date(-262144, time.January, 1, 0, 0, 0, 0, time.UTC),
			wantErr: true,
		},
		{
			name:    "above upper bound",
			value:   time.Date(262143, time.January, 1, 0, 0, 0, 0, time.UTC),
			wantErr: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := formatPredictionRFC3339(test.value)
			if test.wantErr {
				if err == nil {
					t.Fatalf("format year %d succeeded with %q", test.value.Year(), got)
				}
				return
			}
			if err != nil {
				t.Fatalf("format year %d: %v", test.value.Year(), err)
			}
			if got != test.want {
				t.Fatalf("format year %d = %q, want %q", test.value.Year(), got, test.want)
			}
		})
	}
}
