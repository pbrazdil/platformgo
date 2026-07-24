package report

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"

	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
	"github.com/upcomers-org/platformgo/internal/order"
)

// MassPositionStatusReport is the position report shape grouped by mass status.
type MassPositionStatusReport struct {
	AccountID        ids.AccountID
	InstrumentID     ids.InstrumentID
	PositionSide     order.PositionSide
	Quantity         decimal.Quantity
	SignedQuantity   decimal.Decimal
	ReportID         string
	TsLast           uint64
	TsInit           uint64
	VenuePositionID  *ids.PositionID
	AverageOpenPrice *decimal.Decimal
}

func NewMassPositionStatusReport(
	accountID ids.AccountID,
	instrumentID ids.InstrumentID,
	positionSide order.PositionSide,
	quantity decimal.Quantity,
	tsLast, tsInit uint64,
	venuePositionID *ids.PositionID,
	averageOpenPrice *decimal.Decimal,
) MassPositionStatusReport {
	signed := quantity.Decimal()
	switch positionSide {
	case order.PositionSideShort:
		signed = signed.Neg()
	case order.PositionSideFlat:
		signed = decimal.Decimal{}
	}
	return MassPositionStatusReport{
		AccountID:        accountID,
		InstrumentID:     instrumentID,
		PositionSide:     positionSide,
		Quantity:         quantity,
		SignedQuantity:   signed,
		ReportID:         "00000000-0000-4000-8000-000000000001",
		TsLast:           tsLast,
		TsInit:           tsInit,
		VenuePositionID:  copyPointer(venuePositionID),
		AverageOpenPrice: copyPointer(averageOpenPrice),
	}
}

func (r MassPositionStatusReport) Clone() MassPositionStatusReport {
	result := r
	result.VenuePositionID = copyPointer(r.VenuePositionID)
	result.AverageOpenPrice = copyPointer(r.AverageOpenPrice)
	return result
}

// ExecutionMassStatus groups all execution reports returned by one client.
type ExecutionMassStatus struct {
	ClientID  ids.ClientID
	AccountID ids.AccountID
	Venue     ids.Venue
	ReportID  string
	TsInit    uint64

	orderReports    map[ids.VenueOrderID]OrderStatusReport
	fillReports     map[ids.VenueOrderID][]FillReport
	positionReports map[ids.InstrumentID][]MassPositionStatusReport
}

func NewExecutionMassStatus(
	clientID ids.ClientID,
	accountID ids.AccountID,
	venue ids.Venue,
	tsInit uint64,
	reportID string,
) ExecutionMassStatus {
	if reportID == "" {
		reportID = "00000000-0000-4000-8000-000000000001"
	}
	return ExecutionMassStatus{
		ClientID:        clientID,
		AccountID:       accountID,
		Venue:           venue,
		ReportID:        reportID,
		TsInit:          tsInit,
		orderReports:    make(map[ids.VenueOrderID]OrderStatusReport),
		fillReports:     make(map[ids.VenueOrderID][]FillReport),
		positionReports: make(map[ids.InstrumentID][]MassPositionStatusReport),
	}
}

func (s ExecutionMassStatus) OrderReports() map[ids.VenueOrderID]OrderStatusReport {
	result := make(map[ids.VenueOrderID]OrderStatusReport, len(s.orderReports))
	for key, value := range s.orderReports {
		result[key] = value.Clone()
	}
	return result
}

func (s ExecutionMassStatus) FillReports() map[ids.VenueOrderID][]FillReport {
	result := make(map[ids.VenueOrderID][]FillReport, len(s.fillReports))
	for key, values := range s.fillReports {
		copied := make([]FillReport, len(values))
		for index, value := range values {
			copied[index] = value.Clone()
		}
		result[key] = copied
	}
	return result
}

func (s ExecutionMassStatus) PositionReports() map[ids.InstrumentID][]MassPositionStatusReport {
	result := make(map[ids.InstrumentID][]MassPositionStatusReport, len(s.positionReports))
	for key, values := range s.positionReports {
		copied := make([]MassPositionStatusReport, len(values))
		for index, value := range values {
			copied[index] = value.Clone()
		}
		result[key] = copied
	}
	return result
}

func (s *ExecutionMassStatus) AddOrderReports(reports []OrderStatusReport) {
	for _, report := range reports {
		s.orderReports[report.VenueOrderID] = report.Clone()
	}
}

func (s *ExecutionMassStatus) AddFillReports(reports []FillReport) {
	for _, report := range reports {
		key := report.VenueOrderID
		s.fillReports[key] = append(s.fillReports[key], report.Clone())
	}
}

func (s *ExecutionMassStatus) AddPositionReports(reports []MassPositionStatusReport) {
	for _, report := range reports {
		key := report.InstrumentID
		s.positionReports[key] = append(s.positionReports[key], report.Clone())
	}
}

func (s ExecutionMassStatus) Clone() ExecutionMassStatus {
	result := NewExecutionMassStatus(s.ClientID, s.AccountID, s.Venue, s.TsInit, s.ReportID)
	for _, report := range s.orderReports {
		result.AddOrderReports([]OrderStatusReport{report})
	}
	for _, reports := range s.fillReports {
		result.AddFillReports(reports)
	}
	for _, reports := range s.positionReports {
		result.AddPositionReports(reports)
	}
	return result
}

func (s ExecutionMassStatus) Equal(other ExecutionMassStatus) bool {
	return reflect.DeepEqual(s, other)
}

func (s ExecutionMassStatus) String() string {
	return fmt.Sprintf(
		"ExecutionMassStatus(client_id=%s, account_id=%s, venue=%s, order_reports=%v, fill_reports=%v, position_reports=%v, report_id=%s, ts_init=%d)",
		s.ClientID,
		s.AccountID,
		s.Venue,
		s.orderReports,
		s.fillReports,
		s.positionReports,
		s.ReportID,
		s.TsInit,
	)
}

type massOrderEntry struct {
	Key    ids.VenueOrderID
	Report OrderStatusReport
}
type massFillEntry struct {
	Key     ids.VenueOrderID
	Reports []FillReport
}
type massPositionEntry struct {
	Key     ids.InstrumentID
	Reports []MassPositionStatusReport
}
type massStatusWire struct {
	ClientID        ids.ClientID
	AccountID       ids.AccountID
	Venue           ids.Venue
	ReportID        string
	TsInit          uint64
	OrderReports    []massOrderEntry
	FillReports     []massFillEntry
	PositionReports []massPositionEntry
}

func (s ExecutionMassStatus) MarshalJSON() ([]byte, error) {
	wire := massStatusWire{
		ClientID:  s.ClientID,
		AccountID: s.AccountID,
		Venue:     s.Venue,
		ReportID:  s.ReportID,
		TsInit:    s.TsInit,
	}
	orderKeys := make([]string, 0, len(s.orderReports))
	for key := range s.orderReports {
		orderKeys = append(orderKeys, key.String())
	}
	sort.Strings(orderKeys)
	for _, key := range orderKeys {
		id := ids.MustVenueOrderID(key)
		wire.OrderReports = append(wire.OrderReports, massOrderEntry{Key: id, Report: s.orderReports[id]})
	}
	fillKeys := make([]string, 0, len(s.fillReports))
	for key := range s.fillReports {
		fillKeys = append(fillKeys, key.String())
	}
	sort.Strings(fillKeys)
	for _, key := range fillKeys {
		id := ids.MustVenueOrderID(key)
		wire.FillReports = append(wire.FillReports, massFillEntry{Key: id, Reports: s.fillReports[id]})
	}
	positionKeys := make([]string, 0, len(s.positionReports))
	positionIDs := make(map[string]ids.InstrumentID, len(s.positionReports))
	for key := range s.positionReports {
		positionKeys = append(positionKeys, key.String())
		positionIDs[key.String()] = key
	}
	sort.Strings(positionKeys)
	for _, key := range positionKeys {
		id := positionIDs[key]
		wire.PositionReports = append(
			wire.PositionReports,
			massPositionEntry{Key: id, Reports: s.positionReports[id]},
		)
	}
	return json.Marshal(wire)
}

func (s *ExecutionMassStatus) UnmarshalJSON(data []byte) error {
	var wire massStatusWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	result := NewExecutionMassStatus(
		wire.ClientID,
		wire.AccountID,
		wire.Venue,
		wire.TsInit,
		wire.ReportID,
	)
	for _, entry := range wire.OrderReports {
		result.orderReports[entry.Key] = entry.Report
	}
	for _, entry := range wire.FillReports {
		result.fillReports[entry.Key] = append([]FillReport(nil), entry.Reports...)
	}
	for _, entry := range wire.PositionReports {
		result.positionReports[entry.Key] =
			append([]MassPositionStatusReport(nil), entry.Reports...)
	}
	*s = result
	return nil
}
