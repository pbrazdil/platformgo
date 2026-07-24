package market

import (
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/upcomers-org/platformgo/internal/decimal"
)

type BetSide uint8

const (
	BetSideBack BetSide = iota
	BetSideLay
)

func (s BetSide) String() string {
	if s == BetSideBack {
		return "Back"
	}
	if s == BetSideLay {
		return "Lay"
	}
	return fmt.Sprintf("BetSide(%d)", s)
}

func (s BetSide) Opposite() BetSide {
	if s == BetSideBack {
		return BetSideLay
	}
	return BetSideBack
}

type BetOrderSide uint8

const (
	BetOrderSideBuy BetOrderSide = iota
	BetOrderSideSell
)

// Bet stores decimal odds and stake without floating-point conversion.
type Bet struct {
	Price decimal.Decimal
	Stake decimal.Decimal
	Side  BetSide

	// probability retains the exact construction input for probability-derived
	// bets, avoiding repeated reciprocal rounding in payoff calculations.
	probability *decimal.Decimal
}

func NewBet(price, stake decimal.Decimal, side BetSide) Bet {
	return Bet{Price: price, Stake: stake, Side: side}
}

func BetFromStakeOrLiability(price, volume decimal.Decimal, side BetSide) Bet {
	if side == BetSideLay {
		return BetFromLiability(price, volume, side)
	}
	return BetFromStake(price, volume, side)
}

func BetFromStake(price, stake decimal.Decimal, side BetSide) Bet {
	return NewBet(price, stake, side)
}

func BetFromLiability(price, liability decimal.Decimal, side BetSide) Bet {
	if side != BetSideLay {
		panic("Liability-based betting is only applicable for Lay side.")
	}
	one := decimal.MustParse("1")
	if price.Cmp(one) <= 0 {
		panic(fmt.Sprintf(
			"Price must be greater than 1.0 for lay liability calculation, was %s",
			price,
		))
	}
	return NewBet(price, betQuo(liability, price.Sub(one)), side)
}

func (b Bet) Equal(other Bet) bool {
	return b.Price.Equal(other.Price) && b.Stake.Equal(other.Stake) && b.Side == other.Side
}

func (b Bet) Exposure() decimal.Decimal {
	if b.probability != nil {
		exposure := betQuo(b.Stake, *b.probability)
		if b.Side == BetSideLay {
			return exposure.Neg()
		}
		return exposure
	}
	exposure := betMul(b.Price, b.Stake)
	if b.Side == BetSideLay {
		return exposure.Neg()
	}
	return exposure
}

func (b Bet) Liability() decimal.Decimal {
	if b.Side == BetSideBack {
		return b.Stake
	}
	if b.probability != nil {
		numerator := betMul(b.Stake, decimal.MustParse("1").Sub(*b.probability))
		return betQuo(numerator, *b.probability)
	}
	return betMul(b.Stake, b.Price.Sub(decimal.MustParse("1")))
}

func (b Bet) Profit() decimal.Decimal {
	if b.Side == BetSideBack {
		if b.probability != nil {
			numerator := betMul(b.Stake, decimal.MustParse("1").Sub(*b.probability))
			return betQuo(numerator, *b.probability)
		}
		return betMul(b.Stake, b.Price.Sub(decimal.MustParse("1")))
	}
	return b.Stake
}

func (b Bet) OutcomeWinPayoff() decimal.Decimal {
	if b.Side == BetSideBack {
		return b.Profit()
	}
	return b.Liability().Neg()
}

func (b Bet) OutcomeLosePayoff() decimal.Decimal {
	if b.Side == BetSideBack {
		return b.Liability().Neg()
	}
	return b.Profit()
}

func (b Bet) HedgingStake(price decimal.Decimal) decimal.Decimal {
	if b.Side == BetSideBack {
		return betMul(betQuo(b.Price, price), b.Stake)
	}
	return betQuo(b.Stake, betQuo(price, b.Price))
}

func (b Bet) HedgingBet(price decimal.Decimal) Bet {
	return NewBet(price, b.HedgingStake(price), b.Side.Opposite())
}

func (b Bet) String() string {
	return fmt.Sprintf(
		"Bet(%s @ %s x%s)",
		b.Side,
		b.Price.Quantize(2, decimal.RoundHalfEven),
		b.Stake.Quantize(2, decimal.RoundHalfEven),
	)
}

type BetPosition struct {
	Price       decimal.Decimal
	Exposure    decimal.Decimal
	RealizedPnL decimal.Decimal
	Bets        []Bet
}

func NewBetPosition() BetPosition {
	return BetPosition{}
}

func (p BetPosition) Side() (BetSide, bool) {
	switch p.Exposure.Sign() {
	case -1:
		return BetSideLay, true
	case 1:
		return BetSideBack, true
	default:
		return 0, false
	}
}

func (p BetPosition) AsBet() (Bet, bool) {
	side, ok := p.Side()
	if !ok {
		return Bet{}, false
	}
	stake := betQuo(p.Exposure, p.Price)
	if side == BetSideLay {
		stake = betQuo(p.Exposure.Neg(), p.Price)
	}
	return NewBet(p.Price, stake, side), true
}

func (p *BetPosition) AddBet(bet Bet) {
	currentSide, hasSide := p.Side()
	if !hasSide || currentSide == bet.Side {
		p.PositionIncrease(bet)
	} else {
		p.PositionDecrease(bet)
	}
	p.Bets = append(p.Bets, bet)
}

func (p *BetPosition) PositionIncrease(bet Bet) {
	if _, ok := p.Side(); !ok {
		p.Price = bet.Price
	}
	p.Exposure = p.Exposure.Add(bet.Exposure())
}

func (p *BetPosition) PositionDecrease(bet Bet) {
	absoluteBetExposure := bet.Exposure()
	if absoluteBetExposure.Sign() < 0 {
		absoluteBetExposure = absoluteBetExposure.Neg()
	}
	absolutePositionExposure := p.Exposure
	if absolutePositionExposure.Sign() < 0 {
		absolutePositionExposure = absolutePositionExposure.Neg()
	}

	switch absoluteBetExposure.Cmp(absolutePositionExposure) {
	case -1:
		currentSide, ok := p.Side()
		if !ok {
			panic("cannot decrease an empty bet position")
		}
		p.RealizedPnL = p.RealizedPnL.Add(
			betDecreasePnL(bet, p.Price, currentSide, absoluteBetExposure),
		)
		p.Exposure = p.Exposure.Add(bet.Exposure())
	case 1:
		if currentSide, ok := p.Side(); ok {
			p.RealizedPnL = p.RealizedPnL.Add(
				betDecreasePnL(bet, p.Price, currentSide, absolutePositionExposure),
			)
		}
		p.Price = bet.Price
		p.Exposure = p.Exposure.Add(bet.Exposure())
	case 0:
		if currentSide, ok := p.Side(); ok {
			p.RealizedPnL = p.RealizedPnL.Add(
				betDecreasePnL(bet, p.Price, currentSide, absolutePositionExposure),
			)
		}
		p.Price = decimal.Decimal{}
		p.Exposure = decimal.Decimal{}
	}
}

func (p BetPosition) UnrealizedPnL(price decimal.Decimal) decimal.Decimal {
	if _, ok := p.Side(); !ok {
		return decimal.Decimal{}
	}
	flatteningBet, ok := p.FlatteningBet(price)
	if !ok {
		return decimal.Decimal{}
	}
	currentBet, ok := p.AsBet()
	if !ok {
		return decimal.Decimal{}
	}
	return CalcBetsPnL([]Bet{flatteningBet, currentBet})
}

func (p BetPosition) TotalPnL(price decimal.Decimal) decimal.Decimal {
	return p.RealizedPnL.Add(p.UnrealizedPnL(price))
}

func (p BetPosition) FlatteningBet(price decimal.Decimal) (Bet, bool) {
	side, ok := p.Side()
	if !ok {
		return Bet{}, false
	}
	stake := betQuo(p.Exposure, price)
	if side == BetSideLay {
		stake = betQuo(p.Exposure.Neg(), price)
	}
	return NewBet(price, stake, side.Opposite()), true
}

func (p *BetPosition) Reset() {
	p.Price = decimal.Decimal{}
	p.Exposure = decimal.Decimal{}
	p.RealizedPnL = decimal.Decimal{}
	p.Bets = nil
}

func (p BetPosition) String() string {
	return fmt.Sprintf(
		"BetPosition(price: %s, exposure: %s, realized_pnl: %s)",
		p.Price.Quantize(2, decimal.RoundHalfEven),
		p.Exposure.Quantize(2, decimal.RoundHalfEven),
		p.RealizedPnL.Quantize(2, decimal.RoundHalfEven),
	)
}

func CalcBetsPnL(bets []Bet) decimal.Decimal {
	var result decimal.Decimal
	for _, bet := range bets {
		result = result.Add(bet.OutcomeWinPayoff())
	}
	return result
}

func ProbabilityToBet(
	probability, volume decimal.Decimal,
	side BetOrderSide,
) (Bet, error) {
	if probability.IsZero() {
		return Bet{}, errors.New("invalid probability: must be non-zero")
	}
	price := betQuo(decimal.MustParse("1"), probability)
	// volume / (1 / probability) is exactly volume * probability. Keeping the
	// algebraic form avoids introducing a second rounded division.
	stake := betMul(volume, probability)
	betSide := BetSideBack
	if side == BetOrderSideSell {
		betSide = BetSideLay
	}
	bet := NewBet(price, stake, betSide)
	probabilityCopy := probability
	bet.probability = &probabilityCopy
	return bet, nil
}

func InverseProbabilityToBet(
	probability, volume decimal.Decimal,
	side BetOrderSide,
) (Bet, error) {
	one := decimal.MustParse("1")
	if probability.Equal(one) {
		return Bet{}, errors.New("invalid probability: must not be 1.0 (inverse would be zero)")
	}
	inverseSide := BetOrderSideBuy
	if side == BetOrderSideBuy {
		inverseSide = BetOrderSideSell
	}
	return ProbabilityToBet(one.Sub(probability), volume, inverseSide)
}

// betQuo mirrors rust_decimal's 28 significant-digit division boundary.
func betQuo(left, right decimal.Decimal) decimal.Decimal {
	if right.IsZero() {
		panic("bet decimal division by zero")
	}
	integerDigits := quotientIntegerDigits(left, right)
	scale := max(0, 28-integerDigits)
	result, err := left.Quo(right, uint8(scale), decimal.RoundHalfEven)
	if err != nil {
		panic(err)
	}
	return result
}

func quotientIntegerDigits(left, right decimal.Decimal) int {
	leftRat, ok := new(big.Rat).SetString(left.String())
	if !ok {
		panic("invalid left bet decimal " + left.String())
	}
	rightRat, ok := new(big.Rat).SetString(right.String())
	if !ok {
		panic("invalid right bet decimal " + right.String())
	}
	quotient := new(big.Rat).Quo(leftRat, rightRat)
	integer := new(big.Int).Quo(quotient.Num(), quotient.Denom())
	text := strings.TrimPrefix(integer.String(), "-")
	if text == "0" {
		return 0
	}
	return len(text)
}

// betDecreasePnL combines both outcome payoffs before division. This matches
// rust_decimal's single 28-significant-digit rounding boundary and avoids
// rounding an intermediate synthetic stake.
func betDecreasePnL(
	opposing Bet,
	positionPrice decimal.Decimal,
	positionSide BetSide,
	decreasingExposure decimal.Decimal,
) decimal.Decimal {
	positionProfitFactor := positionPrice.Sub(decimal.MustParse("1"))
	positionPayoffNumerator := betMul(decreasingExposure, positionProfitFactor)
	if positionSide == BetSideLay {
		positionPayoffNumerator = positionPayoffNumerator.Neg()
	}
	numerator := betMul(opposing.OutcomeWinPayoff(), positionPrice).Add(positionPayoffNumerator)
	return betQuo(numerator, positionPrice)
}

func betMul(left, right decimal.Decimal) decimal.Decimal {
	result := left.Mul(right)
	if result.IsZero() {
		return result.Normalize()
	}
	integerDigits := decimalIntegerDigits(result)
	targetScale := max(0, 29-integerDigits)
	if int(result.Scale()) <= targetScale {
		return result
	}
	return result.Quantize(uint8(targetScale), decimal.RoundHalfEven)
}

func decimalIntegerDigits(value decimal.Decimal) int {
	text := strings.TrimPrefix(value.String(), "-")
	whole, _, _ := strings.Cut(text, ".")
	whole = strings.TrimLeft(whole, "0")
	if whole == "" {
		return 0
	}
	return len(whole)
}
