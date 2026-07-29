package application

import (
	"context"
	"errors"
	"testing"

	"github.com/upcomers-org/platformgo/internal/edge"
)

var errAdminFleetPositionsDatabaseUnavailable = errors.New(
	"admin fleet positions database unavailable",
)

type recordingAdminFleetPositionsReader struct {
	exists bool
	err    error
	calls  int
}

func (reader *recordingAdminFleetPositionsReader) AdminFleetPositionsExist(
	context.Context,
) (bool, error) {
	reader.calls++
	return reader.exists, reader.err
}

// TestAdminFleetPositionsAuthorizationBoundary is the focused companion
// regression for the production-bound source port in tests/integration/postgres.
func TestAdminFleetPositionsAuthorizationBoundary(t *testing.T) {
	t.Run("fresh admin page is present and exactly empty", func(t *testing.T) {
		reader := &recordingAdminFleetPositionsReader{}
		handler := NewAdminFleetPositionsHandler(reader)
		page, err := handler.Handle(context.Background(), edge.Principal{
			Subject:  "admin-1",
			Audience: edge.AudienceAdmin,
		})
		if err != nil {
			t.Fatalf("read fresh fleet positions: %v", err)
		}
		if page.Items == nil || len(page.Items) != 0 {
			t.Fatalf("fresh page items = %#v, want non-nil empty", page.Items)
		}
		if page.Total == nil || *page.Total != 0 {
			t.Fatalf("fresh page total = %v, want present exact 0", page.Total)
		}
		if reader.calls != 1 {
			t.Fatalf("fresh page reader calls = %d, want 1", reader.calls)
		}
	})

	t.Run("client wildcard is forbidden before store", func(t *testing.T) {
		reader := &recordingAdminFleetPositionsReader{}
		handler := NewAdminFleetPositionsHandler(reader)
		page, err := handler.Handle(context.Background(), edge.Principal{
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
		if page.Items != nil || page.Total != nil {
			t.Fatalf("forbidden request returned partial page %#v", page)
		}
	})

	t.Run("database error propagates without partial page", func(t *testing.T) {
		reader := &recordingAdminFleetPositionsReader{
			err: errAdminFleetPositionsDatabaseUnavailable,
		}
		handler := NewAdminFleetPositionsHandler(reader)
		page, err := handler.Handle(context.Background(), edge.Principal{
			Subject:  "admin-1",
			Audience: edge.AudienceAdmin,
		})
		if !errors.Is(err, errAdminFleetPositionsDatabaseUnavailable) {
			t.Fatalf("error = %v, want database sentinel", err)
		}
		if reader.calls != 1 {
			t.Fatalf("database failure reader calls = %d, want 1", reader.calls)
		}
		if page.Items != nil || page.Total != nil {
			t.Fatalf("database failure returned partial page %#v", page)
		}
	})

	t.Run("non-empty state fails closed without partial page", func(t *testing.T) {
		reader := &recordingAdminFleetPositionsReader{exists: true}
		handler := NewAdminFleetPositionsHandler(reader)
		page, err := handler.Handle(context.Background(), edge.Principal{
			Subject:  "admin-1",
			Audience: edge.AudienceAdmin,
		})
		var stateError *AdminFleetPositionsNonEmptyStateError
		if !errors.As(err, &stateError) {
			t.Fatalf("error = %v, want typed non-empty state error", err)
		}
		if reader.calls != 1 {
			t.Fatalf("non-empty state reader calls = %d, want 1", reader.calls)
		}
		if page.Items != nil || page.Total != nil {
			t.Fatalf("non-empty state returned partial page %#v", page)
		}
	})

	t.Run("missing reader fails without partial page", func(t *testing.T) {
		handler := NewAdminFleetPositionsHandler(nil)
		page, err := handler.Handle(context.Background(), edge.Principal{
			Subject:  "admin-1",
			Audience: edge.AudienceAdmin,
		})
		if !errors.Is(err, ErrAdminFleetPositionsReaderRequired) {
			t.Fatalf("error = %v, want reader-required sentinel", err)
		}
		if page.Items != nil || page.Total != nil {
			t.Fatalf("missing reader returned partial page %#v", page)
		}
	})
}
