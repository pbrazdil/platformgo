package instrument

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
)

const maxInlineComponents = 8

type SyntheticErrorKind string

const (
	SyntheticValidation         SyntheticErrorKind = "validation"
	SyntheticExpression         SyntheticErrorKind = "expression"
	SyntheticMissingInput       SyntheticErrorKind = "missing_input"
	SyntheticInputCountMismatch SyntheticErrorKind = "input_count_mismatch"
	SyntheticInvalidPrice       SyntheticErrorKind = "invalid_price_result"
)

type SyntheticError struct {
	Kind             SyntheticErrorKind
	Component        string
	Expected, Actual int
	Message          string
}

func (e *SyntheticError) Error() string { return e.Message }

type Synthetic struct {
	ID             ids.InstrumentID   `json:"id"`
	PricePrecision uint8              `json:"price_precision"`
	PriceIncrement decimal.Price      `json:"price_increment"`
	Components     []ids.InstrumentID `json:"components"`
	Formula        string             `json:"formula"`
	TsEvent        uint64             `json:"ts_event"`
	TsInit         uint64             `json:"ts_init"`

	compiled exactExpression
}

func NewSynthetic(
	symbol string,
	pricePrecision uint8,
	components []ids.InstrumentID,
	formula string,
	tsEvent, tsInit uint64,
) (*Synthetic, error) {
	if pricePrecision > decimal.MaxPrecision {
		return nil, &SyntheticError{
			Kind:    SyntheticValidation,
			Message: fmt.Sprintf("precision %d exceeds maximum %d", pricePrecision, decimal.MaxPrecision),
		}
	}
	increment := "1"
	if pricePrecision > 0 {
		increment = "0." + strings.Repeat("0", int(pricePrecision)-1) + "1"
	}
	priceIncrement, err := decimal.NewPrice(increment, pricePrecision)
	if err != nil {
		return nil, &SyntheticError{Kind: SyntheticValidation, Message: err.Error()}
	}
	compiled, err := compileExact(formula, components)
	if err != nil {
		return nil, err
	}
	id, err := ids.NewInstrumentID(symbol, "SYNTH")
	if err != nil {
		return nil, &SyntheticError{Kind: SyntheticValidation, Message: err.Error()}
	}
	return &Synthetic{
		ID: id, PricePrecision: pricePrecision, PriceIncrement: priceIncrement,
		Components: append([]ids.InstrumentID(nil), components...), Formula: formula,
		TsEvent: tsEvent, TsInit: tsInit, compiled: compiled,
	}, nil
}

func DefaultSynthetic() *Synthetic {
	components := []ids.InstrumentID{
		ids.MustInstrumentID("BTC.BINANCE"),
		ids.MustInstrumentID("LTC.BINANCE"),
	}
	synthetic, err := NewSynthetic(
		"BTC-LTC", 1, components, "(BTC.BINANCE + LTC.BINANCE) / 2.0", 0, 0,
	)
	if err != nil {
		panic(err)
	}
	return synthetic
}

func IsValidSyntheticFormula(formula string, components []ids.InstrumentID) bool {
	_, err := compileExact(formula, components)
	return err == nil
}

func (s *Synthetic) IsValidFormula(formula string) bool {
	return IsValidSyntheticFormula(formula, s.Components)
}

func (s *Synthetic) ChangeFormula(formula string) error {
	compiled, err := compileExact(formula, s.Components)
	if err != nil {
		return err
	}
	s.Formula = formula
	s.compiled = compiled
	return nil
}

func (s *Synthetic) Calculate(inputs []decimal.Decimal) (decimal.Price, error) {
	if len(inputs) != len(s.Components) {
		return decimal.Price{}, &SyntheticError{
			Kind: SyntheticInputCountMismatch, Expected: len(s.Components), Actual: len(inputs),
			Message: fmt.Sprintf("Expected %d input values, received %d", len(s.Components), len(inputs)),
		}
	}
	result, err := s.compiled.eval(inputs)
	if err != nil {
		return decimal.Price{}, &SyntheticError{
			Kind:    SyntheticInvalidPrice,
			Message: "Formula result produced invalid price: " + err.Error(),
		}
	}
	price, err := decimal.NewPrice(result.String(), s.PricePrecision)
	if err != nil {
		return decimal.Price{}, &SyntheticError{
			Kind:    SyntheticInvalidPrice,
			Message: "Formula result produced invalid price: " + err.Error(),
		}
	}
	return price, nil
}

func (s *Synthetic) CalculateFromMap(inputs map[string]decimal.Decimal) (decimal.Price, error) {
	values := make([]decimal.Decimal, len(s.Components))
	for index, component := range s.Components {
		name := component.String()
		value, ok := inputs[name]
		if !ok {
			return decimal.Price{}, &SyntheticError{
				Kind: SyntheticMissingInput, Component: name,
				Message: "Missing price for component: " + name,
			}
		}
		values[index] = value
	}
	return s.Calculate(values)
}

func (s Synthetic) MarshalJSON() ([]byte, error) {
	type wire Synthetic
	return json.Marshal(wire(s))
}

func (s *Synthetic) UnmarshalJSON(data []byte) error {
	type wire Synthetic
	var decoded wire
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	compiled, err := compileExact(decoded.Formula, decoded.Components)
	if err != nil {
		return err
	}
	*s = Synthetic(decoded)
	s.compiled = compiled
	return nil
}

type exactExpression interface {
	eval([]decimal.Decimal) (decimal.Decimal, error)
}

type exactValue struct{ value decimal.Decimal }

func (v exactValue) eval([]decimal.Decimal) (decimal.Decimal, error) { return v.value, nil }

type exactInput struct{ slot int }

func (v exactInput) eval(inputs []decimal.Decimal) (decimal.Decimal, error) {
	return inputs[v.slot], nil
}

type exactBinary struct {
	op          byte
	left, right exactExpression
}

func (v exactBinary) eval(inputs []decimal.Decimal) (decimal.Decimal, error) {
	left, err := v.left.eval(inputs)
	if err != nil {
		return decimal.Decimal{}, err
	}
	right, err := v.right.eval(inputs)
	if err != nil {
		return decimal.Decimal{}, err
	}
	switch v.op {
	case '+':
		return left.Add(right), nil
	case '-':
		return left.Sub(right), nil
	case '*':
		return left.Mul(right), nil
	case '/':
		return left.Quo(right, decimal.MaxPrecision, decimal.RoundHalfEven)
	default:
		panic("invalid exact operator")
	}
}

type exactToken struct {
	kind byte
	text string
	slot int
}

func compileExact(formula string, components []ids.InstrumentID) (exactExpression, error) {
	aliases := make(map[string]int, len(components)*2)
	primary := make(map[string]bool, len(components))
	for slot, component := range components {
		name := component.String()
		if _, exists := aliases[name]; exists {
			return nil, expressionError("Duplicate binding `" + name + "`")
		}
		aliases[name], primary[name] = slot, true
	}
	for slot, component := range components {
		alias := strings.ReplaceAll(component.String(), "-", "_")
		if !primary[alias] {
			if _, exists := aliases[alias]; !exists {
				aliases[alias] = slot
			}
		}
	}
	names := make([]string, 0, len(aliases))
	for name := range aliases {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		if len(names[i]) == len(names[j]) {
			return names[i] < names[j]
		}
		return len(names[i]) > len(names[j])
	})
	tokens, err := tokenizeExact(formula, aliases, names)
	if err != nil {
		return nil, err
	}
	parser := exactParser{tokens: tokens}
	result, err := parser.expression(0)
	if err != nil {
		return nil, err
	}
	if parser.current().kind != 0 {
		return nil, expressionError("Unexpected token `" + parser.current().text + "`")
	}
	return result, nil
}

func tokenizeExact(formula string, aliases map[string]int, names []string) ([]exactToken, error) {
	var tokens []exactToken
	for position := 0; position < len(formula); {
		if formula[position] == ' ' || formula[position] == '\t' || formula[position] == '\n' {
			position++
			continue
		}
		matched := false
		for _, name := range names {
			if strings.HasPrefix(formula[position:], name) {
				end := position + len(name)
				if end == len(formula) || strings.ContainsRune(" +-*/()", rune(formula[end])) {
					tokens = append(tokens, exactToken{kind: 'i', text: name, slot: aliases[name]})
					position = end
					matched = true
					break
				}
			}
		}
		if matched {
			continue
		}
		ch := formula[position]
		if strings.ContainsRune("+-*/()", rune(ch)) {
			tokens = append(tokens, exactToken{kind: ch, text: string(ch)})
			position++
			continue
		}
		if ch >= '0' && ch <= '9' || ch == '.' {
			start := position
			position++
			for position < len(formula) && (formula[position] >= '0' && formula[position] <= '9' || formula[position] == '.') {
				position++
			}
			text := formula[start:position]
			if _, err := decimal.Parse(text); err != nil {
				return nil, expressionError(err.Error())
			}
			tokens = append(tokens, exactToken{kind: 'n', text: text})
			continue
		}
		start := position
		for position < len(formula) && !strings.ContainsRune(" +-*/()", rune(formula[position])) {
			position++
		}
		if start == position {
			position++
		}
		return nil, expressionError("Unknown symbol `" + formula[start:position] + "`")
	}
	tokens = append(tokens, exactToken{})
	return tokens, nil
}

type exactParser struct {
	tokens []exactToken
	index  int
}

func (p *exactParser) expression(minimum int) (exactExpression, error) {
	token := p.current()
	p.index++
	var left exactExpression
	switch token.kind {
	case 'n':
		left = exactValue{value: decimal.MustParse(token.text)}
	case 'i':
		left = exactInput{slot: token.slot}
	case '(':
		value, err := p.expression(0)
		if err != nil {
			return nil, err
		}
		if p.current().kind != ')' {
			return nil, expressionError("Missing closing parenthesis")
		}
		p.index++
		left = value
	case '-':
		value, err := p.expression(3)
		if err != nil {
			return nil, err
		}
		left = exactBinary{op: '-', left: exactValue{value: decimal.MustParse("0")}, right: value}
	default:
		return nil, expressionError("Expected an expression")
	}
	for {
		precedence := 0
		switch p.current().kind {
		case '+', '-':
			precedence = 1
		case '*', '/':
			precedence = 2
		}
		if precedence == 0 || precedence < minimum {
			break
		}
		op := p.current().kind
		p.index++
		right, err := p.expression(precedence + 1)
		if err != nil {
			return nil, err
		}
		left = exactBinary{op: op, left: left, right: right}
	}
	return left, nil
}

func (p *exactParser) current() exactToken {
	if p.index >= len(p.tokens) {
		return exactToken{}
	}
	return p.tokens[p.index]
}

func expressionError(message string) *SyntheticError {
	return &SyntheticError{Kind: SyntheticExpression, Message: message}
}
