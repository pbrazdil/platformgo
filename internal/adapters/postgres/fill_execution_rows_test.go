package postgres

import (
	"errors"
	"testing"
)

type terminalFillExecutionRows struct {
	nextCalls int
	err       error
}

func (rows *terminalFillExecutionRows) Next() bool {
	rows.nextCalls++
	return rows.nextCalls == 1
}

func (rows *terminalFillExecutionRows) Scan(destinations ...any) error {
	scanValidFillExecutionRow(destinations)
	return nil
}

func scanValidFillExecutionRow(destinations []any) {
	fillID := "00000000-0000-4000-8000-000000000701"
	orderID := "00000000-0000-4000-8000-000000000702"
	positionID := "00000000-0000-4000-8000-000000000703"
	side := "BUY"
	tradeType := "open"
	leverage := "5"
	logicalTime := int64(100)
	orderAccount := "urn:xb:account:00000000-0000-4000-8000-000000000009"

	*destinations[0].(**string) = &fillID
	*destinations[1].(**string) = &orderID
	*destinations[2].(**string) = &positionID
	*destinations[3].(**string) = &side
	*destinations[4].(**string) = &tradeType
	*destinations[5].(**string) = nil
	*destinations[6].(**string) = nil
	*destinations[7].(**int16) = nil
	*destinations[8].(**string) = &leverage
	*destinations[9].(**int64) = &logicalTime
	*destinations[10].(**string) = &orderAccount
	*destinations[11].(**string) = nil
	*destinations[12].(**string) = nil
	*destinations[13].(**string) = nil
	*destinations[14].(**string) = nil
	*destinations[15].(*int64) = 1
}

func (rows *terminalFillExecutionRows) Err() error {
	if rows.nextCalls < 2 {
		return nil
	}
	return rows.err
}

type corruptFillExecutionRows struct {
	next   bool
	mutate func([]any)
}

func (rows *corruptFillExecutionRows) Next() bool {
	if rows.next {
		return false
	}
	rows.next = true
	return true
}

func (rows *corruptFillExecutionRows) Scan(destinations ...any) error {
	scanValidFillExecutionRow(destinations)
	rows.mutate(destinations)
	return nil
}

func (*corruptFillExecutionRows) Err() error { return nil }

func TestCollectFillExecutionRowsRejectsEveryMaterialCorruptionWithoutPage(
	t *testing.T,
) {
	stringPointer := func(value string) *string { return &value }
	scale := int16(2)
	for _, test := range []struct {
		name   string
		mutate func([]any)
	}{
		{
			name: "over-scale realized PnL",
			mutate: func(destinations []any) {
				*destinations[5].(**string) = stringPointer("1.234")
				*destinations[6].(**string) = stringPointer("USDC")
				*destinations[7].(**int16) = &scale
			},
		},
		{
			name: "realized PnL without currency",
			mutate: func(destinations []any) {
				*destinations[5].(**string) = stringPointer("1")
			},
		},
		{
			name: "non-positive leverage",
			mutate: func(destinations []any) {
				*destinations[8].(**string) = stringPointer("0")
			},
		},
		{
			name: "non-finite leverage",
			mutate: func(destinations []any) {
				*destinations[8].(**string) = stringPointer("NaN")
			},
		},
		{
			name: "invalid trade type",
			mutate: func(destinations []any) {
				*destinations[4].(**string) = stringPointer("invented")
			},
		},
		{
			name: "order account mismatch",
			mutate: func(destinations []any) {
				*destinations[10].(**string) = stringPointer(
					"urn:xb:account:00000000-0000-4000-8000-000000000099",
				)
			},
		},
		{
			name: "incomplete intent authority",
			mutate: func(destinations []any) {
				*destinations[12].(**string) = stringPointer(
					"00000000-0000-4000-8000-000000000799",
				)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			page, err := collectFillExecutionRows(
				&corruptFillExecutionRows{mutate: test.mutate},
				"urn:xb:account:00000000-0000-4000-8000-000000000009",
				true,
				50,
				nil,
				true,
			)
			if err == nil {
				t.Fatal("corrupt row was accepted")
			}
			if page.Items != nil ||
				page.Total != 0 ||
				page.NextCursor != nil ||
				page.PrevCursor != nil {
				t.Fatalf("corruption returned partial page %#v", page)
			}
		})
	}
}

func TestCollectFillExecutionRowsPreservesEveryTradeTypeAndReason(t *testing.T) {
	stringPointer := func(value string) *string { return &value }
	accountID := "urn:xb:account:00000000-0000-4000-8000-000000000009"
	for _, test := range []struct {
		name      string
		tradeType string
		reason    string
		mutate    func([]any)
	}{
		{name: "open manual", tradeType: "open", reason: "manual", mutate: func([]any) {}},
		{
			name: "increase stop loss", tradeType: "increase", reason: "stop_loss",
			mutate: func(destinations []any) {
				*destinations[11].(**string) = stringPointer("stop_loss")
			},
		},
		{
			name: "reduce take profit", tradeType: "reduce", reason: "take_profit",
			mutate: func(destinations []any) {
				*destinations[11].(**string) = stringPointer("take_profit")
			},
		},
		{
			name: "flip liquidation", tradeType: "flip", reason: "liquidation",
			mutate: func(destinations []any) {
				*destinations[12].(**string) = stringPointer("stopout:test")
				*destinations[13].(**string) = &accountID
				*destinations[14].(**string) = &accountID
			},
		},
		{
			name: "close flatten", tradeType: "close", reason: "flatten",
			mutate: func(destinations []any) {
				*destinations[12].(**string) = stringPointer("flatten:test")
				*destinations[13].(**string) = &accountID
				*destinations[14].(**string) = &accountID
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			page, err := collectFillExecutionRows(
				&corruptFillExecutionRows{mutate: func(destinations []any) {
					*destinations[4].(**string) = stringPointer(test.tradeType)
					test.mutate(destinations)
				}},
				accountID,
				true,
				50,
				nil,
				true,
			)
			if err != nil {
				t.Fatalf("collect valid row: %v", err)
			}
			if len(page.Items) != 1 ||
				page.Items[0].TradeType != test.tradeType ||
				page.Items[0].Reason != test.reason {
				t.Fatalf("page = %#v, want tradeType=%s reason=%s", page, test.tradeType, test.reason)
			}
		})
	}
}

func TestCollectFillExecutionRowsRejectsTerminalErrorWithoutPartialPage(
	t *testing.T,
) {
	terminalErr := errors.New("terminal row stream failed")
	rows := &terminalFillExecutionRows{err: terminalErr}
	page, err := collectFillExecutionRows(
		rows,
		"urn:xb:account:00000000-0000-4000-8000-000000000009",
		true,
		50,
		nil,
		true,
	)
	if !errors.Is(err, terminalErr) {
		t.Fatalf("error = %v, want terminal row error", err)
	}
	if page.Items != nil ||
		page.Total != 0 ||
		page.NextCursor != nil ||
		page.PrevCursor != nil {
		t.Fatalf("terminal row error returned partial page %#v", page)
	}
}
