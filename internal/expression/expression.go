// Package expression compiles deterministic numeric formulas used by synthetic
// instruments. Formula values are float64 because this is a general expression
// language, not an economic value representation.
package expression

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode"
)

const (
	maxStack  = 32
	maxLocals = 16
)

type ValueType uint8

const (
	Empty ValueType = iota
	Number
	Bool
)

func (v ValueType) String() string {
	switch v {
	case Number:
		return "number"
	case Bool:
		return "bool"
	default:
		return "empty"
	}
}

type ErrorKind string

const (
	ErrEmptyExpression     ErrorKind = "empty_expression"
	ErrUnexpected          ErrorKind = "unexpected"
	ErrUnterminated        ErrorKind = "unterminated_block_comment"
	ErrDuplicateBinding    ErrorKind = "duplicate_binding"
	ErrUnknownSymbol       ErrorKind = "unknown_symbol"
	ErrTypeMismatch        ErrorKind = "type_mismatch"
	ErrNonNumeric          ErrorKind = "non_numeric_result"
	ErrInputCount          ErrorKind = "input_count_mismatch"
	ErrArgumentCount       ErrorKind = "invalid_argument_count"
	ErrStackOverflow       ErrorKind = "stack_overflow"
	ErrTooManyLocals       ErrorKind = "too_many_locals"
	ErrMissingClosingParen ErrorKind = "missing_closing_paren"
)

type ExpressionError struct {
	Kind               ErrorKind
	Position           int
	Name               string
	Expected, Actual   int
	ExistingSlot, Slot int
	Left, Right        ValueType
	Message            string
}

func (e *ExpressionError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return string(e.Kind)
}

type Bindings struct {
	byName   map[string]int
	special  map[byte][]string
	inputLen int
}

func NewBindings() *Bindings {
	return &Bindings{byName: make(map[string]int), special: make(map[byte][]string)}
}

func (b *Bindings) Add(slot int, name string) error {
	if name == "" {
		return &ExpressionError{Kind: ErrUnexpected, Message: "empty binding name"}
	}
	if previous, ok := b.byName[name]; ok {
		if previous != slot {
			return &ExpressionError{
				Kind: ErrDuplicateBinding, Name: name, ExistingSlot: previous, Slot: slot,
			}
		}
		return nil
	}
	b.byName[name] = slot
	b.inputLen = max(b.inputLen, slot+1)
	if !plainIdentifier(name) {
		first := name[0]
		b.special[first] = append(b.special[first], name)
		names := b.special[first]
		for i := 1; i < len(names); i++ {
			for j := i; j > 0 && (len(names[j]) > len(names[j-1]) ||
				len(names[j]) == len(names[j-1]) && names[j] < names[j-1]); j-- {
				names[j], names[j-1] = names[j-1], names[j]
			}
		}
	}
	return nil
}

func (b *Bindings) AddAlias(slot int, name string) error { return b.Add(slot, name) }
func (b *Bindings) Resolve(name string) (int, bool) {
	slot, ok := b.byName[name]
	return slot, ok
}
func (b *Bindings) SpecialCandidates(first byte) []string {
	return append([]string(nil), b.special[first]...)
}
func (b *Bindings) InputLen() int { return b.inputLen }

type tokenKind uint8

const (
	tEOF tokenKind = iota
	tNumber
	tBool
	tIdent
	tBinding
	tLParen
	tRParen
	tComma
	tSemicolon
	tAssign
	tPlus
	tMinus
	tStar
	tSlash
	tPercent
	tCaret
	tBang
	tEq
	tNeq
	tLess
	tLE
	tGreater
	tGE
	tAnd
	tOr
)

type token struct {
	kind tokenKind
	text string
	num  float64
	slot int
	pos  int
}

func (t token) description() string {
	if t.kind == tEOF {
		return "end of input"
	}
	if t.text != "" {
		return t.text
	}
	return strconv.FormatFloat(t.num, 'g', -1, 64)
}

func tokenize(source string, bindings *Bindings) ([]token, error) {
	var result []token
	for position := 0; position < len(source); {
		if unicode.IsSpace(rune(source[position])) {
			position++
			continue
		}
		if strings.HasPrefix(source[position:], "//") {
			if end := strings.IndexByte(source[position:], '\n'); end >= 0 {
				position += end + 1
			} else {
				position = len(source)
			}
			continue
		}
		if strings.HasPrefix(source[position:], "/*") {
			end := strings.Index(source[position+2:], "*/")
			if end < 0 {
				return nil, &ExpressionError{Kind: ErrUnterminated, Position: position}
			}
			position += end + 4
			continue
		}

		if names := bindings.special[source[position]]; len(names) > 0 {
			matched := false
			for _, name := range names {
				if strings.HasPrefix(source[position:], name) {
					end := position + len(name)
					if end == len(source) || bindingBoundary(source[end]) {
						result = append(result, token{kind: tBinding, text: name, slot: bindings.byName[name], pos: position})
						position = end
						matched = true
						break
					}
				}
			}
			if matched {
				continue
			}
		}

		start := position
		ch := source[position]
		if isIdentStart(ch) {
			position++
			for position < len(source) && isIdentContinue(source[position]) {
				position++
			}
			text := source[start:position]
			if text == "true" || text == "false" {
				result = append(result, token{kind: tBool, text: text, num: boolNumber(text == "true"), pos: start})
			} else if slot, ok := bindings.Resolve(text); ok {
				result = append(result, token{kind: tBinding, text: text, slot: slot, pos: start})
			} else {
				result = append(result, token{kind: tIdent, text: text, pos: start})
			}
			continue
		}
		if ch >= '0' && ch <= '9' || ch == '.' && position+1 < len(source) && source[position+1] >= '0' && source[position+1] <= '9' {
			position++
			for position < len(source) && (source[position] >= '0' && source[position] <= '9' || source[position] == '.') {
				position++
			}
			if position < len(source) && (source[position] == 'e' || source[position] == 'E') {
				position++
				if position < len(source) && (source[position] == '+' || source[position] == '-') {
					position++
				}
				for position < len(source) && source[position] >= '0' && source[position] <= '9' {
					position++
				}
			}
			value, err := strconv.ParseFloat(source[start:position], 64)
			if err != nil {
				return nil, &ExpressionError{Kind: ErrUnexpected, Position: start, Message: err.Error()}
			}
			result = append(result, token{kind: tNumber, text: source[start:position], num: value, pos: start})
			continue
		}

		two := ""
		if position+1 < len(source) {
			two = source[position : position+2]
		}
		if kind, ok := map[string]tokenKind{"==": tEq, "!=": tNeq, "<=": tLE, ">=": tGE, "&&": tAnd, "||": tOr}[two]; ok {
			result = append(result, token{kind: kind, text: two, pos: position})
			position += 2
			continue
		}
		if ch == '&' || ch == '|' {
			return nil, &ExpressionError{Kind: ErrUnexpected, Position: position, Message: fmt.Sprintf("expected `%c%c`, found %q", ch, ch, string(ch))}
		}
		kind, ok := map[byte]tokenKind{
			'(': tLParen, ')': tRParen, ',': tComma, ';': tSemicolon, '=': tAssign,
			'+': tPlus, '-': tMinus, '*': tStar, '/': tSlash, '%': tPercent,
			'^': tCaret, '!': tBang, '<': tLess, '>': tGreater,
		}[ch]
		if !ok {
			return nil, &ExpressionError{Kind: ErrUnexpected, Position: position, Message: fmt.Sprintf("unexpected character %q", ch)}
		}
		result = append(result, token{kind: kind, text: string(ch), pos: position})
		position++
	}
	result = append(result, token{kind: tEOF, pos: len(source)})
	return result, nil
}

type expr interface{}
type literal struct {
	value float64
	typ   ValueType
}
type input struct{ slot int }
type name struct{ value string }
type unary struct {
	op    tokenKind
	value expr
}
type binary struct {
	left, right expr
	op          tokenKind
}
type call struct {
	name string
	args []expr
}
type statement struct {
	assign string
	value  expr
}
type program struct {
	statements []statement
	trailing   bool
}

type parser struct {
	tokens []token
	index  int
}

func parse(tokens []token) (program, error) {
	p := parser{tokens: tokens}
	if p.current().kind == tEOF {
		return program{}, &ExpressionError{Kind: ErrEmptyExpression}
	}
	var result program
	for {
		stmt, err := p.parseStatement()
		if err != nil {
			return program{}, err
		}
		result.statements = append(result.statements, stmt)
		if p.current().kind != tSemicolon {
			break
		}
		result.trailing = true
		p.index++
		if p.current().kind == tEOF {
			break
		}
		result.trailing = false
	}
	if p.current().kind != tEOF {
		return program{}, &ExpressionError{Kind: ErrUnexpected, Position: p.current().pos}
	}
	return result, nil
}

func (p *parser) parseStatement() (statement, error) {
	if p.current().kind == tIdent && p.peek().kind == tAssign {
		name := p.current().text
		p.index += 2
		value, err := p.parseExpression(0)
		return statement{assign: name, value: value}, err
	}
	value, err := p.parseExpression(0)
	return statement{value: value}, err
}

func (p *parser) parseExpression(minimum int) (expr, error) {
	left, err := p.prefix()
	if err != nil {
		return nil, err
	}
	for {
		precedence, rightAssociative := binaryPrecedence(p.current().kind)
		if precedence < minimum {
			break
		}
		op := p.current().kind
		p.index++
		next := precedence + 1
		if rightAssociative {
			next = precedence
		}
		right, err := p.parseExpression(next)
		if err != nil {
			return nil, err
		}
		left = binary{left: left, op: op, right: right}
	}
	return left, nil
}

func (p *parser) prefix() (expr, error) {
	current := p.current()
	p.index++
	switch current.kind {
	case tNumber:
		return literal{value: current.num, typ: Number}, nil
	case tBool:
		return literal{value: current.num, typ: Bool}, nil
	case tBinding:
		return input{slot: current.slot}, nil
	case tIdent:
		if p.current().kind != tLParen {
			return name{value: current.text}, nil
		}
		p.index++
		var args []expr
		if p.current().kind == tRParen {
			p.index++
			return call{name: current.text}, nil
		}
		for {
			arg, err := p.parseExpression(0)
			if err != nil {
				return nil, err
			}
			args = append(args, arg)
			if p.current().kind == tRParen {
				p.index++
				break
			}
			if p.current().kind != tComma {
				return nil, &ExpressionError{Kind: ErrUnexpected, Position: p.current().pos}
			}
			p.index++
		}
		return call{name: current.text, args: args}, nil
	case tLParen:
		value, err := p.parseExpression(0)
		if err != nil {
			return nil, err
		}
		if p.current().kind != tRParen {
			return nil, &ExpressionError{Kind: ErrMissingClosingParen, Position: current.pos}
		}
		p.index++
		return value, nil
	case tMinus, tBang:
		value, err := p.parseExpression(7)
		return unary{op: current.kind, value: value}, err
	default:
		return nil, &ExpressionError{Kind: ErrUnexpected, Position: current.pos}
	}
}

func (p *parser) current() token { return p.tokens[min(p.index, len(p.tokens)-1)] }
func (p *parser) peek() token    { return p.tokens[min(p.index+1, len(p.tokens)-1)] }

func binaryPrecedence(kind tokenKind) (int, bool) {
	switch kind {
	case tOr:
		return 1, false
	case tAnd:
		return 2, false
	case tEq, tNeq:
		return 3, false
	case tLess, tLE, tGreater, tGE:
		return 4, false
	case tPlus, tMinus:
		return 5, false
	case tStar, tSlash, tPercent:
		return 6, false
	case tCaret:
		return 7, true
	default:
		return -1, false
	}
}

type Compiled struct {
	program  program
	bindings *Bindings
	typ      ValueType
}

func Compile(source string, bindings *Bindings) (*Compiled, error) {
	tokens, err := tokenize(source, bindings)
	if err != nil {
		return nil, err
	}
	program, err := parse(tokens)
	if err != nil {
		return nil, err
	}
	types := make(map[string]ValueType)
	maxDepth := 0
	for _, stmt := range program.statements {
		typ, depth, err := infer(stmt.value, types)
		if err != nil {
			return nil, err
		}
		maxDepth = max(maxDepth, depth)
		if stmt.assign != "" {
			if old, ok := types[stmt.assign]; ok && old != typ {
				return nil, &ExpressionError{Kind: ErrTypeMismatch, Left: old, Right: typ}
			}
			types[stmt.assign] = typ
		}
	}
	if maxDepth > maxStack {
		return nil, &ExpressionError{Kind: ErrStackOverflow, Actual: maxDepth, Expected: maxStack}
	}
	if len(types) > maxLocals {
		return nil, &ExpressionError{Kind: ErrTooManyLocals, Actual: len(types), Expected: maxLocals}
	}
	resultType := Empty
	last := program.statements[len(program.statements)-1]
	if !program.trailing && last.assign == "" {
		resultType, _, _ = infer(last.value, types)
	}
	return &Compiled{program: program, bindings: bindings, typ: resultType}, nil
}

func CompileNumeric(source string, bindings *Bindings) (*Compiled, error) {
	compiled, err := Compile(source, bindings)
	if err != nil {
		return nil, err
	}
	if compiled.typ != Number {
		return nil, &ExpressionError{Kind: ErrNonNumeric, Left: compiled.typ}
	}
	return compiled, nil
}

func (c *Compiled) ResultType() ValueType { return c.typ }

func (c *Compiled) Eval(inputs []float64) (float64, error) {
	if len(inputs) < c.bindings.inputLen {
		return 0, &ExpressionError{Kind: ErrInputCount, Expected: c.bindings.inputLen, Actual: len(inputs)}
	}
	locals := make(map[string]runtimeValue)
	result := runtimeValue{}
	for _, stmt := range c.program.statements {
		value, err := evaluate(stmt.value, inputs, locals, nil)
		if err != nil {
			return 0, err
		}
		if stmt.assign != "" {
			locals[stmt.assign] = value
		} else {
			result = value
		}
	}
	if c.typ == Empty {
		return 0, &ExpressionError{Kind: ErrEmptyExpression, Message: "empty result"}
	}
	return result.number, nil
}

// EvalObserved is Eval plus an input-slot callback. It is useful for proving
// that short-circuited branches are not evaluated.
func (c *Compiled) EvalObserved(inputs []float64, observed func(int)) (float64, error) {
	if len(inputs) < c.bindings.inputLen {
		return 0, &ExpressionError{Kind: ErrInputCount, Expected: c.bindings.inputLen, Actual: len(inputs)}
	}
	locals := make(map[string]runtimeValue)
	result := runtimeValue{}
	for _, stmt := range c.program.statements {
		value, err := evaluate(stmt.value, inputs, locals, observed)
		if err != nil {
			return 0, err
		}
		if stmt.assign != "" {
			locals[stmt.assign] = value
		} else {
			result = value
		}
	}
	return result.number, nil
}

type runtimeValue struct {
	number float64
	typ    ValueType
}

func infer(value expr, locals map[string]ValueType) (ValueType, int, error) {
	switch value := value.(type) {
	case literal:
		return value.typ, 1, nil
	case input:
		return Number, 1, nil
	case name:
		typ, ok := locals[value.value]
		if !ok {
			return Empty, 0, &ExpressionError{Kind: ErrUnknownSymbol, Name: value.value}
		}
		return typ, 1, nil
	case unary:
		typ, depth, err := infer(value.value, locals)
		if err != nil {
			return Empty, 0, err
		}
		want := Number
		if value.op == tBang {
			want = Bool
		}
		if typ != want {
			return Empty, 0, &ExpressionError{Kind: ErrTypeMismatch, Left: typ, Right: want}
		}
		return want, depth, nil
	case binary:
		left, leftDepth, err := infer(value.left, locals)
		if err != nil {
			return Empty, 0, err
		}
		right, rightDepth, err := infer(value.right, locals)
		if err != nil {
			return Empty, 0, err
		}
		want, result := Number, Number
		if value.op == tAnd || value.op == tOr {
			want, result = Bool, Bool
		} else if value.op >= tEq && value.op <= tGE {
			result = Bool
		}
		if left != want || right != want {
			return Empty, 0, &ExpressionError{Kind: ErrTypeMismatch, Left: left, Right: right}
		}
		return result, max(leftDepth, 1+rightDepth), nil
	case call:
		if value.name == "if" {
			if len(value.args) != 3 {
				return Empty, 0, argumentError(value.name, 3, len(value.args))
			}
			condition, d0, err := infer(value.args[0], locals)
			if err != nil {
				return Empty, 0, err
			}
			left, d1, err := infer(value.args[1], locals)
			if err != nil {
				return Empty, 0, err
			}
			right, d2, err := infer(value.args[2], locals)
			if err != nil {
				return Empty, 0, err
			}
			if condition != Bool || left != right {
				return Empty, 0, &ExpressionError{Kind: ErrTypeMismatch, Left: left, Right: right}
			}
			return left, max(d0, d1, d2), nil
		}
		if _, ok := map[string]bool{"abs": true, "ceil": true, "floor": true, "round": true, "min": true, "max": true}[value.name]; !ok {
			return Empty, 0, &ExpressionError{Kind: ErrUnknownSymbol, Name: value.name}
		}
		if (value.name == "abs" || value.name == "ceil" || value.name == "floor" || value.name == "round") && len(value.args) != 1 {
			return Empty, 0, argumentError(value.name, 1, len(value.args))
		}
		if (value.name == "min" || value.name == "max") && len(value.args) < 2 {
			return Empty, 0, &ExpressionError{Kind: ErrArgumentCount, Name: value.name, Expected: 2, Actual: len(value.args)}
		}
		depth := 0
		for index, arg := range value.args {
			typ, childDepth, err := infer(arg, locals)
			if err != nil {
				return Empty, 0, err
			}
			if typ != Number {
				return Empty, 0, &ExpressionError{Kind: ErrTypeMismatch, Left: typ, Right: Number}
			}
			depth = max(depth, index+childDepth)
		}
		return Number, depth, nil
	default:
		panic("unknown expression")
	}
}

func evaluate(value expr, inputs []float64, locals map[string]runtimeValue, observed func(int)) (runtimeValue, error) {
	switch value := value.(type) {
	case literal:
		return runtimeValue{number: value.value, typ: value.typ}, nil
	case input:
		if observed != nil {
			observed(value.slot)
		}
		return runtimeValue{number: inputs[value.slot], typ: Number}, nil
	case name:
		return locals[value.value], nil
	case unary:
		operand, err := evaluate(value.value, inputs, locals, observed)
		if err != nil {
			return runtimeValue{}, err
		}
		if value.op == tMinus {
			operand.number = -operand.number
		} else {
			operand.number = boolNumber(operand.number == 0)
		}
		return operand, nil
	case binary:
		left, err := evaluate(value.left, inputs, locals, observed)
		if err != nil {
			return runtimeValue{}, err
		}
		if value.op == tAnd && left.number == 0 || value.op == tOr && left.number != 0 {
			return runtimeValue{number: left.number, typ: Bool}, nil
		}
		right, err := evaluate(value.right, inputs, locals, observed)
		if err != nil {
			return runtimeValue{}, err
		}
		return evalBinary(value.op, left.number, right.number), nil
	case call:
		if value.name == "if" {
			condition, err := evaluate(value.args[0], inputs, locals, observed)
			if err != nil {
				return runtimeValue{}, err
			}
			if condition.number != 0 {
				return evaluate(value.args[1], inputs, locals, observed)
			}
			return evaluate(value.args[2], inputs, locals, observed)
		}
		args := make([]float64, len(value.args))
		for index, arg := range value.args {
			evaluated, err := evaluate(arg, inputs, locals, observed)
			if err != nil {
				return runtimeValue{}, err
			}
			args[index] = evaluated.number
		}
		result := args[0]
		switch value.name {
		case "abs":
			result = math.Abs(result)
		case "ceil":
			result = math.Ceil(result)
		case "floor":
			result = math.Floor(result)
		case "round":
			result = math.Round(result)
		case "min":
			for _, candidate := range args[1:] {
				result = math.Min(result, candidate)
			}
		case "max":
			for _, candidate := range args[1:] {
				result = math.Max(result, candidate)
			}
		}
		return runtimeValue{number: result, typ: Number}, nil
	default:
		panic("unknown expression")
	}
}

func evalBinary(op tokenKind, left, right float64) runtimeValue {
	value := runtimeValue{typ: Number}
	switch op {
	case tPlus:
		value.number = left + right
	case tMinus:
		value.number = left - right
	case tStar:
		value.number = left * right
	case tSlash:
		value.number = left / right
	case tPercent:
		value.number = math.Mod(left, right)
	case tCaret:
		value.number = math.Pow(left, right)
	case tEq:
		value.number, value.typ = boolNumber(left == right), Bool
	case tNeq:
		value.number, value.typ = boolNumber(left != right), Bool
	case tLess:
		value.number, value.typ = boolNumber(left < right), Bool
	case tLE:
		value.number, value.typ = boolNumber(left <= right), Bool
	case tGreater:
		value.number, value.typ = boolNumber(left > right), Bool
	case tGE:
		value.number, value.typ = boolNumber(left >= right), Bool
	case tAnd:
		value.number, value.typ = boolNumber(left != 0 && right != 0), Bool
	case tOr:
		value.number, value.typ = boolNumber(left != 0 || right != 0), Bool
	}
	return value
}

func EnsureArgCount(name string, actual, expected int) error {
	if actual == expected {
		return nil
	}
	return argumentError(name, expected, actual)
}

func argumentError(name string, expected, actual int) error {
	return &ExpressionError{Kind: ErrArgumentCount, Name: name, Expected: expected, Actual: actual}
}

func isIdentStart(ch byte) bool    { return ch == '_' || ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' }
func isIdentContinue(ch byte) bool { return isIdentStart(ch) || ch >= '0' && ch <= '9' }
func plainIdentifier(name string) bool {
	if name == "" || !isIdentStart(name[0]) {
		return false
	}
	for index := 1; index < len(name); index++ {
		if !isIdentContinue(name[index]) {
			return false
		}
	}
	return true
}
func bindingBoundary(ch byte) bool {
	return unicode.IsSpace(rune(ch)) || strings.ContainsRune("(),;+-*/%^!=<>&|", rune(ch))
}
func boolNumber(value bool) float64 {
	if value {
		return 1
	}
	return 0
}
