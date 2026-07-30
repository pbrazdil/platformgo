package postgres

import (
	"errors"
	"testing"
	"time"
)

type brokerAccountTestRows struct {
	rows      []func([]any)
	next      int
	terminal  error
	scanError error
	scanAt    int
}

func (rows *brokerAccountTestRows) Next() bool {
	if rows.next >= len(rows.rows) {
		rows.next++
		return false
	}
	rows.next++
	return true
}

func (rows *brokerAccountTestRows) Scan(destinations ...any) error {
	if rows.scanError != nil && rows.next == rows.scanAt {
		return rows.scanError
	}
	rows.rows[rows.next-1](destinations)
	return nil
}

func (rows *brokerAccountTestRows) Err() error {
	if rows.next <= len(rows.rows) {
		return nil
	}
	return rows.terminal
}

func validBrokerAccountRow(destinations []any) {
	accountID := "urn:xb:account:00000000-0000-4000-8000-000000000001"
	login := int64(73000001)
	userID := "urn:xb:user:current-go-user"
	baseCurrency := "USDC"
	marginMode := "CROSS"
	omsMode := "NETTING"
	marketVenue := "HYPERLIQUID"
	permittedClasses := []string{"CRYPTOCURRENCY"}
	status := "ACTIVE"
	createdAt := time.Date(2026, 7, 30, 8, 9, 10, 0, time.UTC)
	*destinations[0].(*string) = accountID
	*destinations[1].(**int64) = &login
	*destinations[2].(*string) = userID
	*destinations[3].(**string) = &baseCurrency
	*destinations[4].(**string) = &marginMode
	*destinations[5].(**string) = &omsMode
	*destinations[6].(**string) = &marketVenue
	*destinations[7].(*[]string) = permittedClasses
	*destinations[8].(**string) = &status
	*destinations[9].(**time.Time) = &createdAt
}

func TestCollectBrokerAccountRowsRejectsScanErrorWithoutPartialList(
	t *testing.T,
) {
	scanErr := errors.New("broker account scan failed")
	rows := &brokerAccountTestRows{
		rows:      []func([]any){validBrokerAccountRow, validBrokerAccountRow},
		scanError: scanErr,
		scanAt:    2,
	}
	accounts, err := collectBrokerAccountRows(rows)
	if !errors.Is(err, scanErr) {
		t.Fatalf("error=%v, want scan error", err)
	}
	if accounts != nil {
		t.Fatalf("scan error returned partial accounts %#v", accounts)
	}
}

func TestCollectBrokerAccountRowsRejectsTerminalErrorWithoutPartialList(
	t *testing.T,
) {
	terminalErr := errors.New("broker account row stream failed")
	rows := &brokerAccountTestRows{
		rows:     []func([]any){validBrokerAccountRow},
		terminal: terminalErr,
	}
	accounts, err := collectBrokerAccountRows(rows)
	if !errors.Is(err, terminalErr) {
		t.Fatalf("error=%v, want terminal error", err)
	}
	if accounts != nil {
		t.Fatalf("terminal error returned partial accounts %#v", accounts)
	}
}

func TestCollectBrokerAccountRowsRejectsIncompleteLateRowWithoutPartialList(
	t *testing.T,
) {
	rows := &brokerAccountTestRows{rows: []func([]any){
		validBrokerAccountRow,
		func(destinations []any) {
			validBrokerAccountRow(destinations)
			*destinations[3].(**string) = nil
		},
	}}
	accounts, err := collectBrokerAccountRows(rows)
	if err == nil {
		t.Fatal("incomplete late account row was accepted")
	}
	if accounts != nil {
		t.Fatalf("incomplete row returned partial accounts %#v", accounts)
	}
}
