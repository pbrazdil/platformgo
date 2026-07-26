package postgres

import (
	"errors"
	"testing"
)

func TestValidatePostgresVersion(t *testing.T) {
	for _, test := range []struct {
		name          string
		versionNumber int
		version       string
		wantError     bool
	}{
		{
			name:          "PostgreSQL 18 rejected",
			versionNumber: 180999,
			version:       "18.99",
			wantError:     true,
		},
		{
			name:          "PostgreSQL 19 devel rejected",
			versionNumber: 190000,
			version:       "19devel",
			wantError:     true,
		},
		{
			name:          "PostgreSQL 19 Beta 1 rejected",
			versionNumber: 190000,
			version:       "19beta1",
			wantError:     true,
		},
		{
			name:          "PostgreSQL 19 Beta 2 accepted",
			versionNumber: 190000,
			version:       "19beta2",
		},
		{
			name:          "vendor-labelled PostgreSQL 19 Beta 2 accepted",
			versionNumber: 190000,
			version:       "19beta2 (Debian 19~beta2-1.pgdg120+1)",
		},
		{
			name:          "later PostgreSQL 19 beta accepted",
			versionNumber: 190000,
			version:       "19beta3",
		},
		{
			name:          "PostgreSQL 19 release candidate accepted",
			versionNumber: 190000,
			version:       "19rc1",
		},
		{
			name:          "PostgreSQL 19 stable accepted",
			versionNumber: 190000,
			version:       "19.0",
		},
		{
			name:          "vendor-labelled PostgreSQL 19 stable accepted",
			versionNumber: 190000,
			version:       "19.0 (Ubuntu 19.0-1.pgdg24.04+1)",
		},
		{
			name:          "later PostgreSQL 19 stable accepted",
			versionNumber: 190001,
			version:       "19.1",
		},
		{
			name:          "unqualified future beta rejected",
			versionNumber: 200000,
			version:       "20beta1",
			wantError:     true,
		},
		{
			name:          "future stable accepted",
			versionNumber: 200000,
			version:       "20.0",
		},
		{
			name:          "missing PostgreSQL release suffix rejected",
			versionNumber: 190000,
			version:       "19",
			wantError:     true,
		},
		{
			name:          "noncanonical display major rejected",
			versionNumber: 190000,
			version:       "019.0",
			wantError:     true,
		},
		{
			name:          "stable token with beta suffix rejected",
			versionNumber: 190000,
			version:       "19.0beta1",
			wantError:     true,
		},
		{
			name:          "stable token with development suffix rejected",
			versionNumber: 190000,
			version:       "19.0devel",
			wantError:     true,
		},
		{
			name:          "beta token with development suffix rejected",
			versionNumber: 190000,
			version:       "19beta2devel",
			wantError:     true,
		},
		{
			name:          "release candidate snapshot suffix rejected",
			versionNumber: 190000,
			version:       "19rc1snapshot",
			wantError:     true,
		},
		{
			name:          "stable minor and numeric version must agree",
			versionNumber: 190001,
			version:       "19.0",
			wantError:     true,
		},
		{
			name:          "prerelease numeric version must be base major",
			versionNumber: 190001,
			version:       "19beta2",
			wantError:     true,
		},
		{
			name:          "numeric and display versions must agree",
			versionNumber: 190000,
			version:       "20.0",
			wantError:     true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := validatePostgresVersion(test.versionNumber, test.version)
			if test.wantError && !errors.Is(err, ErrUnsupportedPostgresVersion) {
				t.Fatalf(
					"validatePostgresVersion(%d, %q) error = %v, want ErrUnsupportedPostgresVersion",
					test.versionNumber,
					test.version,
					err,
				)
			}
			if !test.wantError && err != nil {
				t.Fatalf(
					"validatePostgresVersion(%d, %q) error = %v, want nil",
					test.versionNumber,
					test.version,
					err,
				)
			}
		})
	}
}
