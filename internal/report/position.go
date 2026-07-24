package report

import (
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
)

// PositionSideSpecified is a venue-reported side with no unspecified state.
type PositionSideSpecified uint8

const (
	PositionSideFlat PositionSideSpecified = iota + 1
	PositionSideLong
	PositionSideShort
)

func (side PositionSideSpecified) String() string {
	switch side {
	case PositionSideFlat:
		return "FLAT"
	case PositionSideLong:
		return "LONG"
	case PositionSideShort:
		return "SHORT"
	default:
		return fmt.Sprintf("PositionSideSpecified(%d)", side)
	}
}

// PositionStatusReport represents a venue's position state at a point in time.
type PositionStatusReport struct {
	AccountID        ids.AccountID
	InstrumentID     ids.InstrumentID
	PositionSide     PositionSideSpecified
	Quantity         decimal.Quantity
	SignedDecimalQty decimal.Decimal
	ReportID         string
	TsLast           uint64
	TsInit           uint64
	VenuePositionID  *ids.PositionID
	AveragePriceOpen *decimal.Decimal
}

func NewPositionStatusReport(
	accountID ids.AccountID,
	instrumentID ids.InstrumentID,
	positionSide PositionSideSpecified,
	quantity decimal.Quantity,
	tsLast uint64,
	tsInit uint64,
	reportID string,
	venuePositionID *ids.PositionID,
	averagePriceOpen *decimal.Decimal,
) PositionStatusReport {
	signedQuantity := quantity.Decimal()
	switch positionSide {
	case PositionSideShort:
		signedQuantity = signedQuantity.Neg()
	case PositionSideFlat:
		signedQuantity = decimal.Decimal{}
	}
	if reportID == "" {
		reportID = "00000000-0000-4000-8000-000000000001"
	}
	return PositionStatusReport{
		AccountID:        accountID,
		InstrumentID:     instrumentID,
		PositionSide:     positionSide,
		Quantity:         quantity,
		SignedDecimalQty: signedQuantity,
		ReportID:         reportID,
		TsLast:           tsLast,
		TsInit:           tsInit,
		VenuePositionID:  copyPointer(venuePositionID),
		AveragePriceOpen: copyPointer(averagePriceOpen),
	}
}

func (report PositionStatusReport) HasVenuePositionID() bool {
	return report.VenuePositionID != nil
}

func (report PositionStatusReport) IsFlat() bool {
	return report.PositionSide == PositionSideFlat
}

func (report PositionStatusReport) IsLong() bool {
	return report.PositionSide == PositionSideLong
}

func (report PositionStatusReport) IsShort() bool {
	return report.PositionSide == PositionSideShort
}

func (report PositionStatusReport) Clone() PositionStatusReport {
	result := report
	result.VenuePositionID = copyPointer(report.VenuePositionID)
	result.AveragePriceOpen = copyPointer(report.AveragePriceOpen)
	return result
}

func (report PositionStatusReport) Equal(other PositionStatusReport) bool {
	return reflect.DeepEqual(report, other)
}

func (report PositionStatusReport) String() string {
	venuePositionID := "None"
	if report.VenuePositionID != nil {
		venuePositionID = fmt.Sprintf("Some(%s)", *report.VenuePositionID)
	}
	averagePriceOpen := "None"
	if report.AveragePriceOpen != nil {
		averagePriceOpen = fmt.Sprintf("Some(%s)", *report.AveragePriceOpen)
	}
	return fmt.Sprintf(
		"PositionStatusReport(account=%s, instrument=%s, side=%s, qty=%s, venue_pos_id=%s, avg_px_open=%s, ts_last=%d, ts_init=%d)",
		report.AccountID,
		report.InstrumentID,
		report.PositionSide,
		report.SignedDecimalQty,
		venuePositionID,
		averagePriceOpen,
		report.TsLast,
		report.TsInit,
	)
}

type positionStatusReportWire struct {
	Type             string
	AccountID        ids.AccountID
	InstrumentID     ids.InstrumentID
	PositionSide     PositionSideSpecified
	Quantity         decimal.Quantity
	SignedDecimalQty string
	ReportID         string
	TsLast           uint64
	TsInit           uint64
	VenuePositionID  *ids.PositionID
	AveragePriceOpen *string
}

func (report PositionStatusReport) MarshalJSON() ([]byte, error) {
	wire := positionStatusReportWire{
		Type:             "PositionStatusReport",
		AccountID:        report.AccountID,
		InstrumentID:     report.InstrumentID,
		PositionSide:     report.PositionSide,
		Quantity:         report.Quantity,
		SignedDecimalQty: report.SignedDecimalQty.String(),
		ReportID:         report.ReportID,
		TsLast:           report.TsLast,
		TsInit:           report.TsInit,
		VenuePositionID:  report.VenuePositionID,
	}
	if report.AveragePriceOpen != nil {
		value := report.AveragePriceOpen.String()
		wire.AveragePriceOpen = &value
	}
	return json.Marshal(wire)
}

func (report *PositionStatusReport) UnmarshalJSON(data []byte) error {
	var wire positionStatusReportWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	signedQuantity, err := decimal.Parse(wire.SignedDecimalQty)
	if err != nil {
		return err
	}
	*report = PositionStatusReport{
		AccountID:        wire.AccountID,
		InstrumentID:     wire.InstrumentID,
		PositionSide:     wire.PositionSide,
		Quantity:         wire.Quantity,
		SignedDecimalQty: signedQuantity,
		ReportID:         wire.ReportID,
		TsLast:           wire.TsLast,
		TsInit:           wire.TsInit,
		VenuePositionID:  copyPointer(wire.VenuePositionID),
	}
	if wire.AveragePriceOpen != nil {
		value, err := decimal.Parse(*wire.AveragePriceOpen)
		if err != nil {
			return err
		}
		report.AveragePriceOpen = &value
	}
	return nil
}
