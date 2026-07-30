package postgres

import (
	"errors"
	"testing"
)

type brokerBalanceTestRows struct {
	rows      []func([]any)
	next      int
	terminal  error
	scanError error
}

func (rows *brokerBalanceTestRows) Next() bool {
	if rows.next >= len(rows.rows) {
		rows.next++
		return false
	}
	rows.next++
	return true
}

func (rows *brokerBalanceTestRows) Scan(destinations ...any) error {
	if rows.scanError != nil {
		return rows.scanError
	}
	rows.rows[rows.next-1](destinations)
	return nil
}

func (rows *brokerBalanceTestRows) Err() error {
	if rows.next <= len(rows.rows) {
		return nil
	}
	return rows.terminal
}

func validBrokerBalanceRow(destinations []any) {
	authorized := true
	currency := "USDC"
	scale := int16(2)
	total := "1000.00"
	used := "25.00"
	free := "975.00"
	equity := "1000.00"

	*destinations[0].(*bool) = authorized
	*destinations[1].(**string) = &currency
	*destinations[2].(**int16) = &scale
	*destinations[3].(**string) = &total
	*destinations[4].(**string) = &used
	*destinations[5].(**string) = &free
	*destinations[6].(**string) = &equity
}

func TestCollectBrokerBalanceRowsRejectsEveryMaterialCorruptionWithoutValues(
	t *testing.T,
) {
	stringPointer := func(value string) *string { return &value }
	scalePointer := func(value int16) *int16 { return &value }
	for _, test := range []struct {
		name   string
		mutate func([]any)
	}{
		{
			name: "unauthorized sentinel with payload",
			mutate: func(destinations []any) {
				*destinations[0].(*bool) = false
			},
		},
		{
			name: "authorized incomplete sentinel",
			mutate: func(destinations []any) {
				*destinations[2].(**int16) = nil
				*destinations[3].(**string) = nil
			},
		},
		{
			name: "missing scale",
			mutate: func(destinations []any) {
				*destinations[2].(**int16) = nil
			},
		},
		{
			name: "invalid currency",
			mutate: func(destinations []any) {
				*destinations[1].(**string) = stringPointer("bad!")
			},
		},
		{
			name: "malformed total",
			mutate: func(destinations []any) {
				*destinations[3].(**string) = stringPointer("not-a-decimal")
			},
		},
		{
			name: "non-finite locked",
			mutate: func(destinations []any) {
				*destinations[4].(**string) = stringPointer("NaN")
			},
		},
		{
			name: "over-scale free",
			mutate: func(destinations []any) {
				*destinations[5].(**string) = stringPointer("975.001")
			},
		},
		{
			name: "over-scale equity",
			mutate: func(destinations []any) {
				*destinations[6].(**string) = stringPointer("1000.001")
			},
		},
		{
			name: "invalid registered scale",
			mutate: func(destinations []any) {
				*destinations[2].(**int16) = scalePointer(19)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			rows := &brokerBalanceTestRows{rows: []func([]any){
				func(destinations []any) {
					validBrokerBalanceRow(destinations)
					test.mutate(destinations)
				},
			}}
			values, err := collectBrokerBalanceRows(rows)
			if err == nil {
				t.Fatal("corrupt broker balance row was accepted")
			}
			if values != nil {
				t.Fatalf("corruption returned partial balances %#v", values)
			}
		})
	}
}

func TestCollectBrokerBalanceRowsCanonicalizesEveryEconomicField(t *testing.T) {
	rows := &brokerBalanceTestRows{rows: []func([]any){
		func(destinations []any) {
			validBrokerBalanceRow(destinations)
			total := "-0.00"
			used := "25.00"
			free := "975.00"
			equity := "1000.00"
			*destinations[3].(**string) = &total
			*destinations[4].(**string) = &used
			*destinations[5].(**string) = &free
			*destinations[6].(**string) = &equity
		},
	}}
	values, err := collectBrokerBalanceRows(rows)
	if err != nil {
		t.Fatalf("collect canonical broker balance row: %v", err)
	}
	if len(values) != 1 ||
		values[0].Total != "0" ||
		values[0].Locked != "25" ||
		values[0].Free != "975" ||
		values[0].Equity != "1000" {
		t.Fatalf("canonical broker balances = %#v", values)
	}
}

func TestCollectBrokerBalanceRowsRejectsTerminalErrorWithoutPartialValues(
	t *testing.T,
) {
	terminalErr := errors.New("terminal broker balance row stream failed")
	rows := &brokerBalanceTestRows{
		rows:     []func([]any){validBrokerBalanceRow},
		terminal: terminalErr,
	}
	values, err := collectBrokerBalanceRows(rows)
	if !errors.Is(err, terminalErr) {
		t.Fatalf("error = %v, want terminal row error", err)
	}
	if values != nil {
		t.Fatalf("terminal row error returned partial balances %#v", values)
	}
}
