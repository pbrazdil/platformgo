package position

import (
	"encoding/json"

	"github.com/upcomers-org/platformgo/internal/currency"
	"github.com/upcomers-org/platformgo/internal/ids"
	"github.com/upcomers-org/platformgo/internal/money"
)

type PortfolioSnapshot struct {
	AccountID           ids.AccountID
	AccountType         string
	BaseCurrency        *currency.Currency
	TotalEquity         []money.Money
	BaseCurrencyEquity  *money.Money
	IsStale             bool
	StaleInstruments    []ids.InstrumentID
	StaleCurrencies     []currency.Currency
	UnpricedInstruments []ids.InstrumentID
	EventID             string
	TsEvent             uint64
	TsInit              uint64
}

func NewPortfolioSnapshot(
	accountID ids.AccountID,
	accountType string,
	baseCurrency *currency.Currency,
	totalEquity []money.Money,
	baseCurrencyEquity *money.Money,
	isStale bool,
	staleInstruments []ids.InstrumentID,
	staleCurrencies []currency.Currency,
	unpricedInstruments []ids.InstrumentID,
	eventID string,
	tsEvent uint64,
	tsInit uint64,
) PortfolioSnapshot {
	return PortfolioSnapshot{
		AccountID: accountID, AccountType: accountType, BaseCurrency: cloneCurrency(baseCurrency),
		TotalEquity:        append([]money.Money(nil), totalEquity...),
		BaseCurrencyEquity: cloneMoney(baseCurrencyEquity), IsStale: isStale,
		StaleInstruments:    append([]ids.InstrumentID(nil), staleInstruments...),
		StaleCurrencies:     append([]currency.Currency(nil), staleCurrencies...),
		UnpricedInstruments: append([]ids.InstrumentID(nil), unpricedInstruments...),
		EventID:             eventID, TsEvent: tsEvent, TsInit: tsInit,
	}
}

type portfolioSnapshotWire struct {
	Type                string   `json:"type"`
	AccountID           string   `json:"account_id"`
	AccountType         string   `json:"account_type"`
	BaseCurrency        *string  `json:"base_currency"`
	TotalEquity         []string `json:"total_equity"`
	BaseCurrencyEquity  *string  `json:"base_currency_equity,omitempty"`
	IsStale             bool     `json:"is_stale,omitempty"`
	StaleInstruments    []string `json:"stale_instruments,omitempty"`
	StaleCurrencies     []string `json:"stale_currencies,omitempty"`
	UnpricedInstruments []string `json:"unpriced_instruments,omitempty"`
	EventID             string   `json:"event_id"`
	TsEvent             uint64   `json:"ts_event"`
	TsInit              uint64   `json:"ts_init"`
}

func (snapshot PortfolioSnapshot) MarshalJSON() ([]byte, error) {
	wire := portfolioSnapshotWire{
		Type: "PortfolioSnapshot", AccountID: string(snapshot.AccountID),
		AccountType: snapshot.AccountType, IsStale: snapshot.IsStale,
		EventID: snapshot.EventID, TsEvent: snapshot.TsEvent, TsInit: snapshot.TsInit,
	}
	if snapshot.BaseCurrency != nil {
		value := snapshot.BaseCurrency.Code
		wire.BaseCurrency = &value
	}
	for _, value := range snapshot.TotalEquity {
		wire.TotalEquity = append(wire.TotalEquity, value.String())
	}
	if snapshot.BaseCurrencyEquity != nil {
		value := snapshot.BaseCurrencyEquity.String()
		wire.BaseCurrencyEquity = &value
	}
	for _, value := range snapshot.StaleInstruments {
		wire.StaleInstruments = append(wire.StaleInstruments, value.String())
	}
	for _, value := range snapshot.StaleCurrencies {
		wire.StaleCurrencies = append(wire.StaleCurrencies, value.Code)
	}
	for _, value := range snapshot.UnpricedInstruments {
		wire.UnpricedInstruments = append(wire.UnpricedInstruments, value.String())
	}
	return json.Marshal(wire)
}

func (snapshot *PortfolioSnapshot) UnmarshalJSON(data []byte) error {
	var wire portfolioSnapshotWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	registry := currency.NewDefaultRegistry()
	var baseCurrency *currency.Currency
	if wire.BaseCurrency != nil {
		value := registry.GetOrCreateCrypto(*wire.BaseCurrency)
		baseCurrency = &value
	}
	totalEquity := make([]money.Money, 0, len(wire.TotalEquity))
	for _, text := range wire.TotalEquity {
		value, err := money.Parse(text, registry)
		if err != nil {
			return err
		}
		totalEquity = append(totalEquity, value)
	}
	var baseCurrencyEquity *money.Money
	if wire.BaseCurrencyEquity != nil {
		value, err := money.Parse(*wire.BaseCurrencyEquity, registry)
		if err != nil {
			return err
		}
		baseCurrencyEquity = &value
	}
	staleInstruments := make([]ids.InstrumentID, 0, len(wire.StaleInstruments))
	for _, value := range wire.StaleInstruments {
		staleInstruments = append(staleInstruments, ids.MustInstrumentID(value))
	}
	staleCurrencies := make([]currency.Currency, 0, len(wire.StaleCurrencies))
	for _, value := range wire.StaleCurrencies {
		staleCurrencies = append(staleCurrencies, registry.GetOrCreateCrypto(value))
	}
	unpricedInstruments := make([]ids.InstrumentID, 0, len(wire.UnpricedInstruments))
	for _, value := range wire.UnpricedInstruments {
		unpricedInstruments = append(unpricedInstruments, ids.MustInstrumentID(value))
	}
	*snapshot = NewPortfolioSnapshot(
		ids.MustAccountID(wire.AccountID), wire.AccountType, baseCurrency, totalEquity,
		baseCurrencyEquity, wire.IsStale, staleInstruments, staleCurrencies,
		unpricedInstruments, wire.EventID, wire.TsEvent, wire.TsInit,
	)
	return nil
}
