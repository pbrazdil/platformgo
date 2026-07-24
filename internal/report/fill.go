package report

import (
	"encoding/json"
	"fmt"
	"math/big"
	"reflect"

	"github.com/upcomers-org/platformgo/internal/currency"
	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
	"github.com/upcomers-org/platformgo/internal/money"
	"github.com/upcomers-org/platformgo/internal/order"
)

type FillReportConfig struct {
	AccountID       ids.AccountID
	InstrumentID    ids.InstrumentID
	VenueOrderID    ids.VenueOrderID
	TradeID         ids.TradeID
	OrderSide       order.OrderSide
	LastQuantity    decimal.Quantity
	LastPrice       decimal.Price
	Commission      money.Money
	LiquiditySide   order.LiquiditySide
	ClientOrderID   *ids.ClientOrderID
	VenuePositionID *ids.PositionID
	TsEvent         uint64
	TsInit          uint64
	ReportID        string
}

// FillReport represents a single venue execution.
type FillReport struct {
	AccountID       ids.AccountID
	InstrumentID    ids.InstrumentID
	VenueOrderID    ids.VenueOrderID
	TradeID         ids.TradeID
	OrderSide       order.OrderSide
	LastQuantity    decimal.Quantity
	LastPrice       decimal.Price
	Commission      money.Money
	LiquiditySide   order.LiquiditySide
	AveragePrice    *decimal.Decimal
	ReportID        string
	TsEvent         uint64
	TsInit          uint64
	ClientOrderID   *ids.ClientOrderID
	VenuePositionID *ids.PositionID
}

func NewFillReport(config FillReportConfig) FillReport {
	reportID := config.ReportID
	if reportID == "" {
		reportID = "00000000-0000-4000-8000-000000000001"
	}
	return FillReport{
		AccountID:       config.AccountID,
		InstrumentID:    config.InstrumentID,
		VenueOrderID:    config.VenueOrderID,
		TradeID:         config.TradeID,
		OrderSide:       config.OrderSide,
		LastQuantity:    config.LastQuantity,
		LastPrice:       config.LastPrice,
		Commission:      config.Commission,
		LiquiditySide:   config.LiquiditySide,
		ReportID:        reportID,
		TsEvent:         config.TsEvent,
		TsInit:          config.TsInit,
		ClientOrderID:   copyPointer(config.ClientOrderID),
		VenuePositionID: copyPointer(config.VenuePositionID),
	}
}

func (r FillReport) HasClientOrderID() bool {
	return r.ClientOrderID != nil
}

func (r FillReport) HasVenuePositionID() bool {
	return r.VenuePositionID != nil
}

func (r FillReport) Clone() FillReport {
	result := r
	result.AveragePrice = copyPointer(r.AveragePrice)
	result.ClientOrderID = copyPointer(r.ClientOrderID)
	result.VenuePositionID = copyPointer(r.VenuePositionID)
	return result
}

func (r FillReport) Equal(other FillReport) bool {
	return reflect.DeepEqual(r, other)
}

func (r FillReport) String() string {
	return fmt.Sprintf(
		"FillReport(instrument=%s, side=%s, qty=%s, last_px=%s, trade_id=%s, venue_order_id=%s, commission=%s, liquidity=%s)",
		r.InstrumentID,
		r.OrderSide,
		r.LastQuantity,
		r.LastPrice,
		r.TradeID,
		r.VenueOrderID,
		r.Commission,
		r.LiquiditySide,
	)
}

type fillReportWire struct {
	AccountID                   ids.AccountID
	InstrumentID                ids.InstrumentID
	VenueOrderID                ids.VenueOrderID
	TradeID                     ids.TradeID
	OrderSide                   order.OrderSide
	LastQuantity                decimal.Quantity
	LastPrice                   decimal.Price
	CommissionRaw               string
	CommissionCurrencyCode      string
	CommissionCurrencyPrecision uint8
	CommissionCurrencyISO4217   uint16
	CommissionCurrencyName      string
	CommissionCurrencyType      currency.Type
	LiquiditySide               order.LiquiditySide
	AveragePrice                *decimal.Decimal
	ReportID                    string
	TsEvent                     uint64
	TsInit                      uint64
	ClientOrderID               *ids.ClientOrderID
	VenuePositionID             *ids.PositionID
}

func (r FillReport) MarshalJSON() ([]byte, error) {
	return json.Marshal(fillReportWire{
		AccountID:                   r.AccountID,
		InstrumentID:                r.InstrumentID,
		VenueOrderID:                r.VenueOrderID,
		TradeID:                     r.TradeID,
		OrderSide:                   r.OrderSide,
		LastQuantity:                r.LastQuantity,
		LastPrice:                   r.LastPrice,
		CommissionRaw:               r.Commission.Raw().String(),
		CommissionCurrencyCode:      r.Commission.Currency().Code,
		CommissionCurrencyPrecision: r.Commission.Currency().Precision,
		CommissionCurrencyISO4217:   r.Commission.Currency().ISO4217,
		CommissionCurrencyName:      r.Commission.Currency().Name,
		CommissionCurrencyType:      r.Commission.Currency().Type,
		LiquiditySide:               r.LiquiditySide,
		AveragePrice:                r.AveragePrice,
		ReportID:                    r.ReportID,
		TsEvent:                     r.TsEvent,
		TsInit:                      r.TsInit,
		ClientOrderID:               r.ClientOrderID,
		VenuePositionID:             r.VenuePositionID,
	})
}

func (r *FillReport) UnmarshalJSON(data []byte) error {
	var wire fillReportWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	raw, ok := new(big.Int).SetString(wire.CommissionRaw, 10)
	if !ok {
		return fmt.Errorf("invalid commission raw value %q", wire.CommissionRaw)
	}
	denomination, err := currency.New(
		wire.CommissionCurrencyCode,
		wire.CommissionCurrencyPrecision,
		wire.CommissionCurrencyISO4217,
		wire.CommissionCurrencyName,
		wire.CommissionCurrencyType,
	)
	if err != nil {
		return err
	}
	commission, err := money.FromRawChecked(raw, denomination)
	if err != nil {
		return err
	}
	*r = FillReport{
		AccountID:       wire.AccountID,
		InstrumentID:    wire.InstrumentID,
		VenueOrderID:    wire.VenueOrderID,
		TradeID:         wire.TradeID,
		OrderSide:       wire.OrderSide,
		LastQuantity:    wire.LastQuantity,
		LastPrice:       wire.LastPrice,
		Commission:      commission,
		LiquiditySide:   wire.LiquiditySide,
		AveragePrice:    copyPointer(wire.AveragePrice),
		ReportID:        wire.ReportID,
		TsEvent:         wire.TsEvent,
		TsInit:          wire.TsInit,
		ClientOrderID:   copyPointer(wire.ClientOrderID),
		VenuePositionID: copyPointer(wire.VenuePositionID),
	}
	return nil
}
