package postgres

import (
	"errors"
	"testing"
)

func TestValidatePostgresVersionNumber(t *testing.T) {
	for _, test := range []struct {
		name          string
		versionNumber int
		wantError     bool
	}{
		{name: "PostgreSQL 16 rejected", versionNumber: 160999, wantError: true},
		{name: "PostgreSQL 17 accepted", versionNumber: 170000},
		{name: "PostgreSQL 18 accepted", versionNumber: 180001},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := validatePostgresVersionNumber(test.versionNumber)
			if test.wantError && !errors.Is(err, ErrUnsupportedPostgresVersion) {
				t.Fatalf(
					"validatePostgresVersionNumber(%d) error = %v, want ErrUnsupportedPostgresVersion",
					test.versionNumber,
					err,
				)
			}
			if !test.wantError && err != nil {
				t.Fatalf(
					"validatePostgresVersionNumber(%d) error = %v, want nil",
					test.versionNumber,
					err,
				)
			}
		})
	}
}
