package market

import (
	"encoding/json"
	"fmt"

	"github.com/upcomers-org/platformgo/internal/currency"
	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
)

type FundingSettlement struct {
	TraderID        ids.TraderID
	InstrumentID    ids.InstrumentID
	AccountID       ids.AccountID
	Rate            decimal.Decimal
	SettlementPrice decimal.Decimal
	Currency        currency.Currency
	EventID         string
	TsEvent         uint64
	TsInit          uint64
}

func NewFundingSettlement(
	traderID ids.TraderID,
	instrumentID ids.InstrumentID,
	accountID ids.AccountID,
	rate decimal.Decimal,
	settlementPrice decimal.Decimal,
	settlementCurrency currency.Currency,
	eventID string,
	tsEvent uint64,
	tsInit uint64,
) FundingSettlement {
	return FundingSettlement{
		TraderID: traderID, InstrumentID: instrumentID, AccountID: accountID,
		Rate: rate, SettlementPrice: settlementPrice, Currency: settlementCurrency,
		EventID: eventID, TsEvent: tsEvent, TsInit: tsInit,
	}
}

func (settlement FundingSettlement) Equal(other FundingSettlement) bool {
	return settlement.TraderID == other.TraderID &&
		settlement.InstrumentID == other.InstrumentID &&
		settlement.AccountID == other.AccountID &&
		settlement.Rate.Equal(other.Rate) &&
		settlement.SettlementPrice.Equal(other.SettlementPrice) &&
		settlement.Currency.Equal(other.Currency) &&
		settlement.EventID == other.EventID &&
		settlement.TsEvent == other.TsEvent &&
		settlement.TsInit == other.TsInit
}

type fundingSettlementWire struct {
	Type            string `json:"type"`
	TraderID        string `json:"trader_id"`
	InstrumentID    string `json:"instrument_id"`
	AccountID       string `json:"account_id"`
	Rate            string `json:"rate"`
	SettlementPrice string `json:"settlement_price"`
	Currency        string `json:"currency"`
	EventID         string `json:"event_id"`
	TsEvent         uint64 `json:"ts_event"`
	TsInit          uint64 `json:"ts_init"`
}

func (settlement FundingSettlement) MarshalJSON() ([]byte, error) {
	return json.Marshal(fundingSettlementWire{
		Type: "FundingSettlement", TraderID: string(settlement.TraderID),
		InstrumentID: settlement.InstrumentID.String(), AccountID: string(settlement.AccountID),
		Rate: settlement.Rate.String(), SettlementPrice: settlement.SettlementPrice.String(),
		Currency: settlement.Currency.Code, EventID: settlement.EventID,
		TsEvent: settlement.TsEvent, TsInit: settlement.TsInit,
	})
}

func (settlement *FundingSettlement) UnmarshalJSON(data []byte) error {
	var wire fundingSettlementWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	if wire.Type != "" && wire.Type != "FundingSettlement" {
		return fmt.Errorf("invalid event type %q", wire.Type)
	}
	rate, err := decimal.Parse(wire.Rate)
	if err != nil {
		return err
	}
	price, err := decimal.Parse(wire.SettlementPrice)
	if err != nil {
		return err
	}
	settlementCurrency := currency.NewDefaultRegistry().GetOrCreateCrypto(wire.Currency)
	*settlement = NewFundingSettlement(
		ids.MustTraderID(wire.TraderID),
		ids.MustInstrumentID(wire.InstrumentID),
		ids.MustAccountID(wire.AccountID),
		rate,
		price,
		settlementCurrency,
		wire.EventID,
		wire.TsEvent,
		wire.TsInit,
	)
	return nil
}
