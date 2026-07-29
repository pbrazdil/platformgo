package application

import (
	"context"
	"errors"
	"testing"

	"github.com/upcomers-org/platformgo/internal/edge"
)

var errAdminRiskMonitorDatabaseUnavailable = errors.New(
	"admin risk monitor database unavailable",
)

type recordingAdminRiskStateReader struct {
	exists bool
	err    error
	calls  int
}

func (reader *recordingAdminRiskStateReader) AdminRiskStateExists(
	context.Context,
) (bool, error) {
	reader.calls++
	return reader.exists, reader.err
}

// TestAdminRiskMonitorAuthorizationBoundary is the focused companion
// regression for the production-bound source port in tests/integration/postgres.
func TestAdminRiskMonitorAuthorizationBoundary(t *testing.T) {
	t.Run("fresh admin result is present and exactly empty", func(t *testing.T) {
		reader := &recordingAdminRiskStateReader{}
		handler := NewAdminRiskMonitorHandler(reader)
		accounts, err := handler.Handle(context.Background(), edge.Principal{
			Subject:  "admin-1",
			Audience: edge.AudienceAdmin,
		})
		if err != nil {
			t.Fatalf("read fresh risk monitor: %v", err)
		}
		if accounts == nil || len(accounts) != 0 {
			t.Fatalf("fresh at-risk accounts = %#v, want non-nil empty", accounts)
		}
		if reader.calls != 1 {
			t.Fatalf("fresh result reader calls = %d, want 1", reader.calls)
		}
	})

	t.Run("client wildcard is forbidden before store", func(t *testing.T) {
		reader := &recordingAdminRiskStateReader{}
		handler := NewAdminRiskMonitorHandler(reader)
		accounts, err := handler.Handle(context.Background(), edge.Principal{
			Subject:  "client-1",
			Audience: edge.AudienceClient,
			Scopes:   []string{"*"},
			Accounts: []string{"*"},
		})
		if !errors.Is(err, edge.ErrForbidden) {
			t.Fatalf("error = %v, want forbidden", err)
		}
		if reader.calls != 0 {
			t.Fatalf("forbidden request reader calls = %d, want 0", reader.calls)
		}
		if accounts != nil {
			t.Fatalf("forbidden request returned partial accounts %#v", accounts)
		}
	})

	t.Run("database error propagates without partial result", func(t *testing.T) {
		reader := &recordingAdminRiskStateReader{
			err: errAdminRiskMonitorDatabaseUnavailable,
		}
		handler := NewAdminRiskMonitorHandler(reader)
		accounts, err := handler.Handle(context.Background(), edge.Principal{
			Subject:  "admin-1",
			Audience: edge.AudienceAdmin,
		})
		if !errors.Is(err, errAdminRiskMonitorDatabaseUnavailable) {
			t.Fatalf("error = %v, want database sentinel", err)
		}
		if reader.calls != 1 {
			t.Fatalf("database failure reader calls = %d, want 1", reader.calls)
		}
		if accounts != nil {
			t.Fatalf("database failure returned partial accounts %#v", accounts)
		}
	})

	t.Run("durable risk state fails closed without partial result", func(t *testing.T) {
		reader := &recordingAdminRiskStateReader{exists: true}
		handler := NewAdminRiskMonitorHandler(reader)
		accounts, err := handler.Handle(context.Background(), edge.Principal{
			Subject:  "admin-1",
			Audience: edge.AudienceAdmin,
		})
		var stateError *AdminRiskMonitorNonEmptyStateError
		if !errors.As(err, &stateError) {
			t.Fatalf("error = %v, want typed non-empty state error", err)
		}
		if reader.calls != 1 {
			t.Fatalf("non-empty state reader calls = %d, want 1", reader.calls)
		}
		if accounts != nil {
			t.Fatalf("non-empty state returned partial accounts %#v", accounts)
		}
	})

	t.Run("missing reader fails without partial result", func(t *testing.T) {
		handler := NewAdminRiskMonitorHandler(nil)
		accounts, err := handler.Handle(context.Background(), edge.Principal{
			Subject:  "admin-1",
			Audience: edge.AudienceAdmin,
		})
		if !errors.Is(err, ErrAdminRiskMonitorReaderRequired) {
			t.Fatalf("error = %v, want reader-required sentinel", err)
		}
		if accounts != nil {
			t.Fatalf("missing reader returned partial accounts %#v", accounts)
		}
	})
}
