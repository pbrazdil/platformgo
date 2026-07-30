package postgres

import (
	"errors"
	"testing"

	"github.com/upcomers-org/platformgo/internal/edge"
)

type brokerFundingTestRows struct {
	rows      []func([]any)
	next      int
	terminal  error
	scanError error
}

func (rows *brokerFundingTestRows) Next() bool {
	if rows.next >= len(rows.rows) {
		rows.next++
		return false
	}
	rows.next++
	return true
}

func (rows *brokerFundingTestRows) Scan(destinations ...any) error {
	if rows.scanError != nil {
		return rows.scanError
	}
	rows.rows[rows.next-1](destinations)
	return nil
}

func (rows *brokerFundingTestRows) Err() error {
	if rows.next <= len(rows.rows) {
		return nil
	}
	return rows.terminal
}

func validBrokerFundingRow(destinations []any) {
	authorized := true
	accountLogin := int64(901)
	fundingID := "00000000-0000-4000-8000-000000000911"
	symbol := "BTC-PERP"
	instrumentRevision := int64(7)
	priceScale := int16(2)
	quantityScale := int16(3)
	positionID := "00000000-0000-4000-8000-000000000912"
	signedQuantity := "-1.250"
	oraclePrice := "64000.50"
	fundingRate := "-0.000100"
	fundingAmount := "8.000000"
	currency := "USDC"
	scale := int16(6)
	logicalTime := int64(1_750_000_000_123_456_789)
	total := int64(1)

	*destinations[0].(*bool) = authorized
	*destinations[1].(**int64) = &accountLogin
	*destinations[2].(**string) = &fundingID
	*destinations[3].(**string) = &symbol
	*destinations[4].(**int64) = &instrumentRevision
	*destinations[5].(**int16) = &priceScale
	*destinations[6].(**int16) = &quantityScale
	*destinations[7].(**string) = &positionID
	*destinations[8].(**string) = &signedQuantity
	*destinations[9].(**string) = &oraclePrice
	*destinations[10].(**string) = &fundingRate
	*destinations[11].(**string) = &fundingAmount
	*destinations[12].(**string) = &currency
	*destinations[13].(**int16) = &scale
	*destinations[14].(**int64) = &logicalTime
	*destinations[15].(**int64) = &total
}

func TestCollectBrokerFundingRowsCanonicalizesCompletePage(t *testing.T) {
	rows := &brokerFundingTestRows{rows: []func([]any){validBrokerFundingRow}}
	page, err := collectBrokerFundingRows(rows, 50, nil, true)
	if err != nil {
		t.Fatalf("collect broker funding: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("items = %#v, want one", page.Items)
	}
	item := page.Items[0]
	if item.PositionSignedQuantity != "-1.25" ||
		item.OraclePrice != "64000.5" ||
		item.FundingRate != "-0.0001" ||
		item.FundingAmount != "8" ||
		item.Currency != "USDC" ||
		item.AccountLogin == nil ||
		*item.AccountLogin != 901 {
		t.Fatalf("canonical item = %#v", item)
	}
	if page.Total == nil || *page.Total != 1 {
		t.Fatalf("total = %#v, want 1", page.Total)
	}
}

func TestCollectBrokerFundingRowsRejectsCorruptionWithoutPartialPage(t *testing.T) {
	stringPointer := func(value string) *string { return &value }
	scalePointer := func(value int16) *int16 { return &value }
	int64Pointer := func(value int64) *int64 { return &value }
	for _, test := range []struct {
		name   string
		mutate func([]any)
	}{
		{
			name: "unauthorized payload",
			mutate: func(destinations []any) {
				*destinations[0].(*bool) = false
			},
		},
		{
			name: "missing login",
			mutate: func(destinations []any) {
				*destinations[1].(**int64) = nil
			},
		},
		{
			name: "invalid login",
			mutate: func(destinations []any) {
				*destinations[1].(**int64) = int64Pointer(0)
			},
		},
		{
			name: "noncanonical funding ID",
			mutate: func(destinations []any) {
				*destinations[2].(**string) = stringPointer(
					"00000000-0000-4000-8000-000000000911 ",
				)
			},
		},
		{
			name: "invalid symbol",
			mutate: func(destinations []any) {
				*destinations[3].(**string) = stringPointer(" BTC-PERP")
			},
		},
		{
			name: "noncanonical position ID",
			mutate: func(destinations []any) {
				*destinations[7].(**string) = stringPointer(
					"00000000-0000-4000-8000-000000000912 ",
				)
			},
		},
		{
			name: "missing instrument revision",
			mutate: func(destinations []any) {
				*destinations[4].(**int64) = nil
			},
		},
		{
			name: "invalid instrument revision",
			mutate: func(destinations []any) {
				*destinations[4].(**int64) = int64Pointer(0)
			},
		},
		{
			name: "missing price scale",
			mutate: func(destinations []any) {
				*destinations[5].(**int16) = nil
			},
		},
		{
			name: "invalid price scale",
			mutate: func(destinations []any) {
				*destinations[5].(**int16) = scalePointer(19)
			},
		},
		{
			name: "missing quantity scale",
			mutate: func(destinations []any) {
				*destinations[6].(**int16) = nil
			},
		},
		{
			name: "invalid quantity scale",
			mutate: func(destinations []any) {
				*destinations[6].(**int16) = scalePointer(-1)
			},
		},
		{
			name: "non-finite signed quantity",
			mutate: func(destinations []any) {
				*destinations[8].(**string) = stringPointer("NaN")
			},
		},
		{
			name: "off-step signed quantity",
			mutate: func(destinations []any) {
				*destinations[8].(**string) = stringPointer("-1.2501")
			},
		},
		{
			name: "non-positive oracle",
			mutate: func(destinations []any) {
				*destinations[9].(**string) = stringPointer("0")
			},
		},
		{
			name: "off-tick oracle",
			mutate: func(destinations []any) {
				*destinations[9].(**string) = stringPointer("64000.501")
			},
		},
		{
			name: "non-finite rate",
			mutate: func(destinations []any) {
				*destinations[10].(**string) = stringPointer("NaN")
			},
		},
		{
			name: "over-scale money",
			mutate: func(destinations []any) {
				*destinations[11].(**string) = stringPointer("8.0000001")
			},
		},
		{
			name: "invalid currency",
			mutate: func(destinations []any) {
				*destinations[12].(**string) = stringPointer("usd")
			},
		},
		{
			name: "invalid currency scale",
			mutate: func(destinations []any) {
				*destinations[13].(**int16) = scalePointer(19)
			},
		},
		{
			name: "negative total",
			mutate: func(destinations []any) {
				*destinations[15].(**int64) = int64Pointer(-1)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			rows := &brokerFundingTestRows{rows: []func([]any){
				func(destinations []any) {
					validBrokerFundingRow(destinations)
					*destinations[15].(**int64) = int64Pointer(2)
				},
				func(destinations []any) {
					validBrokerFundingRow(destinations)
					*destinations[15].(**int64) = int64Pointer(2)
					test.mutate(destinations)
				},
			}}
			page, err := collectBrokerFundingRows(rows, 50, nil, true)
			if err == nil {
				t.Fatal("corrupt broker funding row was accepted")
			}
			if page.Items != nil || page.Total != nil {
				t.Fatalf("corruption returned partial page %#v", page)
			}
		})
	}
}

func TestCollectBrokerFundingRowsRejectsUnauthorizedAndTerminalStreams(t *testing.T) {
	t.Run("foreign account", func(t *testing.T) {
		rows := &brokerFundingTestRows{rows: []func([]any){
			func(destinations []any) {
				*destinations[0].(*bool) = false
			},
		}}
		page, err := collectBrokerFundingRows(rows, 50, nil, true)
		if !errors.Is(err, edge.ErrForbidden) {
			t.Fatalf("error = %v, want forbidden", err)
		}
		if page.Items != nil {
			t.Fatalf("foreign account returned page %#v", page)
		}
	})

	t.Run("terminal error after valid row", func(t *testing.T) {
		terminalErr := errors.New("funding stream failed")
		rows := &brokerFundingTestRows{
			rows:     []func([]any){validBrokerFundingRow},
			terminal: terminalErr,
		}
		page, err := collectBrokerFundingRows(rows, 50, nil, true)
		if !errors.Is(err, terminalErr) {
			t.Fatalf("error = %v, want terminal error", err)
		}
		if page.Items != nil || page.Total != nil {
			t.Fatalf("terminal error returned partial page %#v", page)
		}
	})
}
