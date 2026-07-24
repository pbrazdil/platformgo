package market

import (
	"bytes"
	"encoding/gob"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/upcomers-org/platformgo/internal/decimal"
)

// InstrumentCloseType identifies why an instrument was closed.
type InstrumentCloseType uint8

const (
	EndOfSession    InstrumentCloseType = 1
	ContractExpired InstrumentCloseType = 2
)

func (c InstrumentCloseType) String() string {
	switch c {
	case EndOfSession:
		return "END_OF_SESSION"
	case ContractExpired:
		return "CONTRACT_EXPIRED"
	default:
		return fmt.Sprintf("InstrumentCloseType(%d)", c)
	}
}

func (c InstrumentCloseType) MarshalJSON() ([]byte, error) {
	return json.Marshal(c.String())
}

func (c *InstrumentCloseType) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return err
	}
	switch text {
	case "END_OF_SESSION":
		*c = EndOfSession
	case "CONTRACT_EXPIRED":
		*c = ContractExpired
	default:
		return fmt.Errorf("invalid instrument close type %q", text)
	}
	return nil
}

// InstrumentClose represents an instrument close at a venue.
type InstrumentClose struct {
	InstrumentID InstrumentID        `json:"instrument_id"`
	ClosePrice   decimal.Price       `json:"close_price"`
	CloseType    InstrumentCloseType `json:"close_type"`
	TsEvent      UnixNanos           `json:"ts_event"`
	TsInit       UnixNanos           `json:"ts_init"`
}

func NewInstrumentClose(
	instrumentID InstrumentID,
	closePrice decimal.Price,
	closeType InstrumentCloseType,
	tsEvent, tsInit UnixNanos,
) InstrumentClose {
	return InstrumentClose{
		InstrumentID: instrumentID,
		ClosePrice:   closePrice,
		CloseType:    closeType,
		TsEvent:      tsEvent,
		TsInit:       tsInit,
	}
}

func InstrumentCloseMetadata(instrumentID InstrumentID, pricePrecision uint8) map[string]string {
	return map[string]string{
		"instrument_id":   string(instrumentID),
		"price_precision": strconv.Itoa(int(pricePrecision)),
	}
}

func InstrumentCloseFields() []Field {
	return []Field{
		{Name: "close_price", Type: fixedSizeBinary},
		{Name: "close_type", Type: "UInt8"},
		{Name: "ts_event", Type: "UInt64"},
		{Name: "ts_init", Type: "UInt64"},
	}
}

func (c InstrumentClose) Equal(other InstrumentClose) bool {
	return c.InstrumentID == other.InstrumentID &&
		c.ClosePrice.Equal(other.ClosePrice) &&
		c.CloseType == other.CloseType &&
		c.TsEvent == other.TsEvent &&
		c.TsInit == other.TsInit
}

func (c InstrumentClose) String() string {
	return fmt.Sprintf("%s,%s,%s,%d", c.InstrumentID, c.ClosePrice, c.CloseType, c.TsEvent)
}

func (c InstrumentClose) MarshalJSON() ([]byte, error) {
	type wire InstrumentClose
	return json.Marshal(struct {
		Type string `json:"type"`
		wire
	}{Type: "InstrumentClose", wire: wire(c)})
}

func (c *InstrumentClose) UnmarshalJSON(data []byte) error {
	type wire InstrumentClose
	var decoded struct {
		Type string `json:"type"`
		wire
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	if decoded.Type != "" && decoded.Type != "InstrumentClose" {
		return fmt.Errorf("invalid market data type %q", decoded.Type)
	}
	*c = InstrumentClose(decoded.wire)
	return nil
}

// MarshalBinary encodes an instrument close using a native Go binary wire
// representation. Economic values remain decimal strings on the wire.
func (c InstrumentClose) MarshalBinary() ([]byte, error) {
	wire := instrumentCloseBinary{
		InstrumentID: string(c.InstrumentID),
		ClosePrice:   c.ClosePrice.String(),
		CloseType:    uint8(c.CloseType),
		TsEvent:      uint64(c.TsEvent),
		TsInit:       uint64(c.TsInit),
	}
	var output bytes.Buffer
	if err := gob.NewEncoder(&output).Encode(wire); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

// UnmarshalBinary decodes the representation emitted by MarshalBinary.
func (c *InstrumentClose) UnmarshalBinary(data []byte) error {
	var wire instrumentCloseBinary
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&wire); err != nil {
		return err
	}
	price, err := decimal.ParsePrice(wire.ClosePrice)
	if err != nil {
		return err
	}
	*c = NewInstrumentClose(
		InstrumentID(wire.InstrumentID),
		price,
		InstrumentCloseType(wire.CloseType),
		UnixNanos(wire.TsEvent),
		UnixNanos(wire.TsInit),
	)
	return nil
}

type instrumentCloseBinary struct {
	InstrumentID string
	ClosePrice   string
	CloseType    uint8
	TsEvent      uint64
	TsInit       uint64
}
