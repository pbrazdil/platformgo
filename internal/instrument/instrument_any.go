package instrument

type InstrumentAnyKind uint8

const (
	InstrumentAnyFuturesSpread InstrumentAnyKind = iota + 1
	InstrumentAnyOptionSpread
	InstrumentAnyCryptoFuturesSpread
	InstrumentAnyCryptoOptionSpread
	InstrumentAnyCryptoFuture
	InstrumentAnyCryptoOption
)

func (kind InstrumentAnyKind) IsSpread() bool {
	switch kind {
	case InstrumentAnyFuturesSpread, InstrumentAnyOptionSpread,
		InstrumentAnyCryptoFuturesSpread, InstrumentAnyCryptoOptionSpread:
		return true
	default:
		return false
	}
}
