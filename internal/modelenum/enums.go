package modelenum

type AggressorSide uint8

const (
	NoAggressor AggressorSide = iota
	Buyer
	Seller
)

func AggressorSideFromUint8(value uint8) (AggressorSide, bool) {
	if value <= uint8(Seller) {
		return AggressorSide(value), true
	}
	return 0, false
}

type GreeksConvention string

const (
	BlackScholes  GreeksConvention = "BLACK_SCHOLES"
	PriceAdjusted GreeksConvention = "PRICE_ADJUSTED"
)

func (g GreeksConvention) String() string { return string(g) }
func DefaultGreeksConvention() GreeksConvention {
	return BlackScholes
}

type ContinuousFutureAdjustmentType string

const (
	BackwardSpread ContinuousFutureAdjustmentType = "BACKWARD_SPREAD"
	ForwardSpread  ContinuousFutureAdjustmentType = "FORWARD_SPREAD"
	BackwardRatio  ContinuousFutureAdjustmentType = "BACKWARD_RATIO"
	ForwardRatio   ContinuousFutureAdjustmentType = "FORWARD_RATIO"
)

func (a ContinuousFutureAdjustmentType) String() string { return string(a) }
func (a ContinuousFutureAdjustmentType) IsRatio() bool {
	return a == BackwardRatio || a == ForwardRatio
}
func (a ContinuousFutureAdjustmentType) IsBackward() bool {
	return a == BackwardSpread || a == BackwardRatio
}
func DefaultContinuousFutureAdjustmentType() ContinuousFutureAdjustmentType {
	return BackwardSpread
}

type InstrumentClass string

const (
	Spot          InstrumentClass = "SPOT"
	Swap          InstrumentClass = "SWAP"
	Future        InstrumentClass = "FUTURE"
	Forward       InstrumentClass = "FORWARD"
	CFD           InstrumentClass = "CFD"
	Bond          InstrumentClass = "BOND"
	Option        InstrumentClass = "OPTION"
	Warrant       InstrumentClass = "WARRANT"
	FuturesSpread InstrumentClass = "FUTURES_SPREAD"
	OptionSpread  InstrumentClass = "OPTION_SPREAD"
	SportsBetting InstrumentClass = "SPORTS_BETTING"
	BinaryOption  InstrumentClass = "BINARY_OPTION"
)

func (c InstrumentClass) String() string { return string(c) }
func (c InstrumentClass) AllowsNegativePrice() bool {
	return c == Option || c == FuturesSpread || c == OptionSpread
}
func (c InstrumentClass) ParentSuffix() (string, bool) {
	switch c {
	case Future:
		return "FUT", true
	case Option:
		return "OPT", true
	default:
		return "", false
	}
}
func InstrumentClassFromParentSuffix(suffix string) (InstrumentClass, bool) {
	switch suffix {
	case "FUT", "FUTURE":
		return Future, true
	case "OPT", "OPTION":
		return Option, true
	default:
		return "", false
	}
}
