package expression

import (
	"errors"
	"math"
	"math/rand/v2"
	"strings"
	"testing"
)

const nautilusRevision = "116c9b5159ebeb6b578b737d72298cac8d723723"

func testBindings(t *testing.T) *Bindings {
	t.Helper()
	bindings := NewBindings()
	if err := bindings.Add(0, "x"); err != nil {
		t.Fatal(err)
	}
	if err := bindings.Add(1, "AUD/USD.SIM"); err != nil {
		t.Fatal(err)
	}
	return bindings
}

func mustCompile(t *testing.T, source string, bindings *Bindings) *Compiled {
	t.Helper()
	compiled, err := Compile(source, bindings)
	if err != nil {
		t.Fatal(err)
	}
	return compiled
}

func mustCompileNumeric(t *testing.T, source string, bindings *Bindings) *Compiled {
	t.Helper()
	compiled, err := CompileNumeric(source, bindings)
	if err != nil {
		t.Fatal(err)
	}
	return compiled
}

func requireExpressionError(t *testing.T, err error, kind ErrorKind) *ExpressionError {
	t.Helper()
	var expressionErr *ExpressionError
	if !errors.As(err, &expressionErr) {
		t.Fatalf("error type = %T, want *ExpressionError", err)
	}
	if expressionErr.Kind != kind {
		t.Fatalf("error kind = %q, want %q", expressionErr.Kind, kind)
	}
	return expressionErr
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/expressions/mod.rs:209
//	test: test_bindings_resolve_plain_identifiers
func TestBindingsResolvePlainIdentifiers(t *testing.T) {
	bindings := NewBindings()
	if err := bindings.Add(0, "spread"); err != nil {
		t.Fatal(err)
	}
	if err := bindings.Add(1, "ratio"); err != nil {
		t.Fatal(err)
	}
	if slot, ok := bindings.Resolve("spread"); !ok || slot != 0 {
		t.Fatalf("spread = %d, %v", slot, ok)
	}
	if slot, ok := bindings.Resolve("ratio"); !ok || slot != 1 {
		t.Fatalf("ratio = %d, %v", slot, ok)
	}
	if _, ok := bindings.Resolve("missing"); ok {
		t.Fatal("missing binding resolved")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/expressions/mod.rs:220
//	test: test_bindings_keep_special_candidates_sorted_by_length
func TestBindingsKeepSpecialCandidatesSortedByLength(t *testing.T) {
	bindings := NewBindings()
	_ = bindings.Add(0, "ETH-USDT-SWAP.OKX")
	_ = bindings.Add(1, "ETH-USDT")
	got := bindings.SpecialCandidates('E')
	if len(got) != 2 || got[0] != "ETH-USDT-SWAP.OKX" || got[1] != "ETH-USDT" {
		t.Fatalf("candidates = %#v", got)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/expressions/mod.rs:235
//	test: test_bindings_reject_duplicate_names_for_different_slots
func TestBindingsRejectDuplicateNamesForDifferentSlots(t *testing.T) {
	bindings := NewBindings()
	_ = bindings.Add(0, "spread")
	err := requireExpressionError(t, bindings.Add(1, "spread"), ErrDuplicateBinding)
	if err.Name != "spread" || err.ExistingSlot != 0 || err.Slot != 1 {
		t.Fatalf("error = %#v", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/expressions/lexer.rs:381
//	test: test_tokenize_special_bindings_and_comments
func TestTokenizeSpecialBindingsAndComments(t *testing.T) {
	bindings := NewBindings()
	_ = bindings.Add(0, "AUD/USD.SIM")
	_ = bindings.Add(1, "BTCUSDT.BINANCE")
	tokens, err := tokenize("AUD/USD.SIM + BTCUSDT.BINANCE // trailing\n/* block */ - 1.5", bindings)
	if err != nil {
		t.Fatal(err)
	}
	want := []tokenKind{tBinding, tPlus, tBinding, tMinus, tNumber, tEOF}
	for index := range want {
		if tokens[index].kind != want[index] {
			t.Fatalf("token %d = %v, want %v", index, tokens[index].kind, want[index])
		}
	}
	if tokens[0].slot != 0 || tokens[2].slot != 1 || tokens[4].num != 1.5 {
		t.Fatalf("tokens = %#v", tokens)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/expressions/lexer.rs:405
//	test: test_tokenize_prefers_longest_special_binding_match
func TestTokenizePrefersLongestSpecialBindingMatch(t *testing.T) {
	bindings := NewBindings()
	_ = bindings.Add(0, "ETH")
	_ = bindings.Add(1, "ETH-USDT-SWAP.OKX")
	tokens, err := tokenize("ETH-USDT-SWAP.OKX - 1", bindings)
	if err != nil || tokens[0].kind != tBinding || tokens[0].slot != 1 {
		t.Fatalf("tokens = %#v, %v", tokens, err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/expressions/lexer.rs:424
//	test: test_tokenize_rejects_partial_special_binding_matches
func TestTokenizeRejectsPartialSpecialBindingMatches(t *testing.T) {
	bindings := NewBindings()
	_ = bindings.Add(0, "AUD/USD")
	_, err := tokenize("AUD/USD.SIM + 1", bindings)
	expressionErr := requireExpressionError(t, err, ErrUnexpected)
	if expressionErr.Position != 7 {
		t.Fatalf("position = %d, want 7", expressionErr.Position)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/expressions/lexer.rs:440
//	test: test_tokenize_rejects_single_ampersand_and_pipe
func TestTokenizeRejectsSingleAmpersandAndPipe(t *testing.T) {
	for _, source := range []string{"true & false", "true | false"} {
		_, err := tokenize(source, NewBindings())
		expressionErr := requireExpressionError(t, err, ErrUnexpected)
		if expressionErr.Position != 5 {
			t.Errorf("%q position = %d", source, expressionErr.Position)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/expressions/lexer.rs:465
//	test: test_tokenize_plain_identifiers_bool_and_scientific_notation
func TestTokenizePlainIdentifiersBoolAndScientificNotation(t *testing.T) {
	tokens, err := tokenize("flag && true || foo_1 + 1.2e-3", NewBindings())
	if err != nil {
		t.Fatal(err)
	}
	want := []tokenKind{tIdent, tAnd, tBool, tOr, tIdent, tPlus, tNumber, tEOF}
	for index := range want {
		if tokens[index].kind != want[index] {
			t.Fatalf("token %d = %v, want %v", index, tokens[index].kind, want[index])
		}
	}
	if tokens[6].num != 0.0012 {
		t.Fatalf("scientific value = %g", tokens[6].num)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/expressions/lexer.rs:485
//	test: test_tokenize_rejects_unterminated_block_comment
func TestTokenizeRejectsUnterminatedBlockComment(t *testing.T) {
	_, err := tokenize("1 /* missing", NewBindings())
	expressionErr := requireExpressionError(t, err, ErrUnterminated)
	if expressionErr.Position != 2 {
		t.Fatalf("position = %d, want 2", expressionErr.Position)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/expressions/lexer.rs:496
//	test: test_token_kind_description_for_end_of_input
func TestTokenKindDescriptionForEndOfInput(t *testing.T) {
	if got := (token{kind: tEOF}).description(); got != "end of input" {
		t.Fatalf("description = %q", got)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/expressions/parser.rs:323
//	test: test_parse_operator_precedence_and_right_associative_power
func TestParseOperatorPrecedenceAndRightAssociativePower(t *testing.T) {
	compiled := mustCompileNumeric(t, "1 + 2 * 3 ^ 2", NewBindings())
	got, err := compiled.Eval(nil)
	if err != nil || got != 19 {
		t.Fatalf("value = %g, %v", got, err)
	}
	root := compiled.program.statements[0].value.(binary)
	if root.op != tPlus || root.right.(binary).op != tStar || root.right.(binary).right.(binary).op != tCaret {
		t.Fatalf("AST precedence = %#v", root)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/expressions/parser.rs:349
//	test: test_parse_unary_minus_after_power
func TestParseUnaryMinusAfterPower(t *testing.T) {
	compiled := mustCompileNumeric(t, "-2 ^ 2", NewBindings())
	got, err := compiled.Eval(nil)
	if err != nil || got != -4 {
		t.Fatalf("value = %g, %v", got, err)
	}
	if compiled.program.statements[0].value.(unary).value.(binary).op != tCaret {
		t.Fatal("power was not nested inside unary minus")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/expressions/parser.rs:370
//	test: test_parse_assignments_function_calls_and_sequences
func TestParseAssignmentsFunctionCallsAndSequences(t *testing.T) {
	compiled := mustCompileNumeric(t, "spread = max(1, 2); spread + 3", NewBindings())
	if len(compiled.program.statements) != 2 || compiled.program.statements[0].assign != "spread" {
		t.Fatalf("program = %#v", compiled.program)
	}
	function := compiled.program.statements[0].value.(call)
	if function.name != "max" || len(function.args) != 2 {
		t.Fatalf("call = %#v", function)
	}
	got, _ := compiled.Eval(nil)
	if got != 5 {
		t.Fatalf("value = %g", got)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/expressions/parser.rs:397
//	test: test_parse_supports_special_bindings
func TestParseSupportsSpecialBindings(t *testing.T) {
	bindings := NewBindings()
	_ = bindings.Add(0, "AUD/USD.SIM")
	compiled := mustCompile(t, "AUD/USD.SIM > 1", bindings)
	root := compiled.program.statements[0].value.(binary)
	if root.op != tGreater || root.left.(input).slot != 0 {
		t.Fatalf("AST = %#v", root)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/expressions/parser.rs:417
//	test: test_parse_rejects_missing_closing_paren
func TestParseRejectsMissingClosingParen(t *testing.T) {
	tokens, err := tokenize("(1 + 2", NewBindings())
	if err != nil {
		t.Fatal(err)
	}
	_, err = parse(tokens)
	expressionErr := requireExpressionError(t, err, ErrMissingClosingParen)
	if expressionErr.Position != 0 {
		t.Fatalf("position = %d", expressionErr.Position)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/expressions/eval.rs:809
//	test: test_eval_numeric_expression_with_assignments_and_special_bindings
func TestEvalNumericExpressionWithAssignmentsAndSpecialBindings(t *testing.T) {
	got, err := mustCompileNumeric(t, "spread = AUD/USD.SIM - x; spread / 2", testBindings(t)).Eval([]float64{2, 6})
	if err != nil || got != 2 {
		t.Fatalf("value = %g, %v", got, err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/expressions/eval.rs:818
//	test: test_eval_boolean_expression_with_functions
func TestEvalBooleanExpressionWithFunctions(t *testing.T) {
	compiled := mustCompile(t, "if(x > 2, max(1, x), 0) >= 3", testBindings(t))
	got, err := compiled.Eval([]float64{3, 0})
	if err != nil || compiled.ResultType() != Bool || got != 1 {
		t.Fatalf("type/value = %v/%g, %v", compiled.ResultType(), got, err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/expressions/eval.rs:829
//	test: test_eval_unary_minus_after_power
func TestEvalUnaryMinusAfterPower(t *testing.T) {
	got, err := mustCompileNumeric(t, "-2 ^ 2", NewBindings()).Eval(nil)
	if err != nil || got != -4 {
		t.Fatalf("value = %g, %v", got, err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/expressions/eval.rs:837
//	test: test_compile_short_circuits_if
//
// Adaptations:
//   - The native evaluator exposes observed input slots instead of bytecode internals.
func TestCompileShortCircuitsIf(t *testing.T) {
	compiled := mustCompileNumeric(t, "if(x > 0, x, AUD/USD.SIM)", testBindings(t))
	var slots []int
	got, err := compiled.EvalObserved([]float64{1, 99}, func(slot int) { slots = append(slots, slot) })
	if err != nil || got != 1 || containsInt(slots, 1) {
		t.Fatalf("value/observed = %g/%v, %v", got, slots, err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/expressions/eval.rs:862
//	test: test_compile_short_circuits_logical_operators
//
// Adaptations:
//   - Branch observation replaces assertions about Rust bytecode instructions.
func TestCompileShortCircuitsLogicalOperators(t *testing.T) {
	for _, test := range []struct {
		source string
		inputs []float64
	}{
		{"x > 0 && AUD/USD.SIM > 0", []float64{0, 99}},
		{"x > 0 || AUD/USD.SIM > 0", []float64{1, 99}},
	} {
		var slots []int
		_, err := mustCompile(t, test.source, testBindings(t)).EvalObserved(test.inputs, func(slot int) { slots = append(slots, slot) })
		if err != nil || containsInt(slots, 1) {
			t.Fatalf("%q observed %v, %v", test.source, slots, err)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/expressions/eval.rs:888
//	test: test_compile_rejects_read_before_assignment
func TestCompileRejectsReadBeforeAssignment(t *testing.T) {
	_, err := CompileNumeric("spread + 1", NewBindings())
	expressionErr := requireExpressionError(t, err, ErrUnknownSymbol)
	if expressionErr.Name != "spread" {
		t.Fatalf("name = %q", expressionErr.Name)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/expressions/eval.rs:900
//	test: test_compile_rejects_local_type_change
func TestCompileRejectsLocalTypeChange(t *testing.T) {
	_, err := Compile("x = true; x = 1", NewBindings())
	expressionErr := requireExpressionError(t, err, ErrTypeMismatch)
	if expressionErr.Left != Bool || expressionErr.Right != Number {
		t.Fatalf("types = %v/%v", expressionErr.Left, expressionErr.Right)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/expressions/eval.rs:914
//	test: test_compile_numeric_rejects_boolean_results
func TestCompileNumericRejectsBooleanResults(t *testing.T) {
	_, err := CompileNumeric("x > 1", testBindings(t))
	expressionErr := requireExpressionError(t, err, ErrNonNumeric)
	if expressionErr.Left != Bool {
		t.Fatalf("actual type = %v", expressionErr.Left)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/expressions/eval.rs:926
//	test: test_eval_rejects_missing_inputs
func TestEvalRejectsMissingInputs(t *testing.T) {
	_, err := mustCompileNumeric(t, "x + 1", testBindings(t)).Eval(nil)
	expressionErr := requireExpressionError(t, err, ErrInputCount)
	if expressionErr.Expected != 2 || expressionErr.Actual != 0 {
		t.Fatalf("counts = %d/%d", expressionErr.Expected, expressionErr.Actual)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/expressions/eval.rs:940
//	test: test_ensure_arg_count_accepts_new_exact_arities
func TestEnsureArgCountAcceptsNewExactArities(t *testing.T) {
	if err := EnsureArgCount("test", 2, 2); err != nil {
		t.Fatal(err)
	}
	err := requireExpressionError(t, EnsureArgCount("test", 1, 2), ErrArgumentCount)
	if err.Name != "test" || err.Expected != 2 || err.Actual != 1 {
		t.Fatalf("error = %#v", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/expressions/eval.rs:962
//	test: test_eval_arithmetic_operators
func TestEvalArithmeticOperators(t *testing.T) {
	tests := map[string]struct {
		inputs []float64
		want   float64
	}{
		"x + 1": {inputs: []float64{5, 0}, want: 6},
		"x - 1": {inputs: []float64{5, 0}, want: 4},
		"x * 3": {inputs: []float64{5, 0}, want: 15},
		"x / 2": {inputs: []float64{10, 0}, want: 5},
		"x % 3": {inputs: []float64{7, 0}, want: 1},
		"x ^ 3": {inputs: []float64{2, 0}, want: 8},
	}
	for source, test := range tests {
		got, err := mustCompileNumeric(t, source, testBindings(t)).Eval(test.inputs)
		if err != nil || got != test.want {
			t.Errorf("%q = %g, %v; want %g", source, got, err, test.want)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/expressions/eval.rs:986
//	test: test_eval_comparison_operators
func TestEvalComparisonOperators(t *testing.T) {
	tests := []struct {
		source  string
		x, want float64
	}{
		{"x < 10", 5, 1}, {"x < 10", 15, 0}, {"x <= 5", 5, 1}, {"x <= 5", 6, 0},
		{"x > 5", 5, 0}, {"x > 5", 6, 1}, {"x >= 5", 5, 1}, {"x >= 5", 4, 0},
		{"x == 5", 5, 1}, {"x == 5", 6, 0}, {"x != 5", 5, 0}, {"x != 5", 6, 1},
	}
	for _, test := range tests {
		got, err := mustCompile(t, test.source, testBindings(t)).Eval([]float64{test.x, 0})
		if err != nil || got != test.want {
			t.Errorf("%q(%g) = %g, %v", test.source, test.x, got, err)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/expressions/eval.rs:1004
//	test: test_eval_logical_operators
func TestEvalLogicalOperators(t *testing.T) {
	tests := map[string]float64{
		"true && false": 0, "true && true": 1, "false || true": 1,
		"false || false": 0, "!false": 1, "!true": 0,
	}
	for source, want := range tests {
		got, err := mustCompile(t, source, NewBindings()).Eval(nil)
		if err != nil || got != want {
			t.Errorf("%q = %g, %v", source, got, err)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/expressions/eval.rs:1028
//	test: test_eval_builtin_functions
func TestEvalBuiltinFunctions(t *testing.T) {
	tests := []struct {
		source string
		inputs []float64
		want   float64
	}{
		{"abs(x)", []float64{-3, 0}, 3}, {"abs(x)", []float64{3, 0}, 3},
		{"ceil(x)", []float64{2.3, 0}, 3}, {"ceil(x)", []float64{-2.3, 0}, -2},
		{"floor(x)", []float64{2.7, 0}, 2}, {"floor(x)", []float64{-2.7, 0}, -3},
		{"round(x)", []float64{2.5, 0}, 3}, {"round(x)", []float64{2.4, 0}, 2},
		{"min(x, 10)", []float64{3, 0}, 3}, {"min(x, 10)", []float64{20, 0}, 10},
		{"min(x, AUD/USD.SIM, 100)", []float64{5, 3}, 3},
		{"max(x, 10)", []float64{3, 0}, 10}, {"max(x, 10)", []float64{20, 0}, 20},
		{"max(x, AUD/USD.SIM, 0)", []float64{5, 8}, 8},
		{"if(x > 0, x, 10)", []float64{5, 0}, 5}, {"if(x > 0, x, 10)", []float64{-5, 0}, 10},
	}
	for _, test := range tests {
		got, err := mustCompileNumeric(t, test.source, testBindings(t)).Eval(test.inputs)
		if err != nil || got != test.want {
			t.Errorf("%q = %g, %v; want %g", test.source, got, err, test.want)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/expressions/eval.rs:1040
//	test: test_compile_rejects_stack_overflow
func TestCompileRejectsStackOverflow(t *testing.T) {
	args := strings.TrimSuffix(strings.Repeat("1,", maxStack+1), ",")
	_, err := CompileNumeric("min("+args+")", NewBindings())
	expressionErr := requireExpressionError(t, err, ErrStackOverflow)
	if expressionErr.Actual != maxStack+1 || expressionErr.Expected != maxStack {
		t.Fatalf("depth/max = %d/%d", expressionErr.Actual, expressionErr.Expected)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/expressions/eval.rs:1058
//	test: test_compile_rejects_too_many_locals
func TestCompileRejectsTooManyLocals(t *testing.T) {
	var parts []string
	for index := 0; index < maxLocals+1; index++ {
		parts = append(parts, "a"+strconvItoa(index)+" = 1")
	}
	_, err := CompileNumeric(strings.Join(parts, "; ")+"; a0", NewBindings())
	expressionErr := requireExpressionError(t, err, ErrTooManyLocals)
	if expressionErr.Actual != maxLocals+1 || expressionErr.Expected != maxLocals {
		t.Fatalf("count/max = %d/%d", expressionErr.Actual, expressionErr.Expected)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/expressions/eval.rs:1077
//	test: test_if_short_circuits_untaken_branch_at_runtime
func TestIfShortCircuitsUntakenBranchAtRuntime(t *testing.T) {
	var slots []int
	got, err := mustCompileNumeric(t, "if(x > 0, AUD/USD.SIM / x, 42)", testBindings(t)).
		EvalObserved([]float64{0, 99}, func(slot int) { slots = append(slots, slot) })
	if err != nil || got != 42 || containsInt(slots, 1) {
		t.Fatalf("value/slots = %g/%v, %v", got, slots, err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/expressions/eval.rs:1093
//	test: test_and_short_circuits_when_left_is_false
func TestAndShortCircuitsWhenLeftIsFalse(t *testing.T) {
	var slots []int
	got, err := mustCompile(t, "x > 0 && AUD/USD.SIM > 0", testBindings(t)).
		EvalObserved([]float64{0, 99}, func(slot int) { slots = append(slots, slot) })
	if err != nil || got != 0 || containsInt(slots, 1) {
		t.Fatalf("value/slots = %g/%v, %v", got, slots, err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/expressions/eval.rs:1109
//	test: test_or_short_circuits_when_left_is_true
func TestOrShortCircuitsWhenLeftIsTrue(t *testing.T) {
	var slots []int
	got, err := mustCompile(t, "x > 0 || AUD/USD.SIM > 0", testBindings(t)).
		EvalObserved([]float64{5, 99}, func(slot int) { slots = append(slots, slot) })
	if err != nil || got != 1 || containsInt(slots, 1) {
		t.Fatalf("value/slots = %g/%v, %v", got, slots, err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/expressions/mod.rs:372
//	test: prop_numeric_expressions_match_native_evaluation
func TestPropertyNumericExpressionsMatchNativeEvaluation(t *testing.T) {
	random := rand.New(rand.NewPCG(1, 2))
	formulas := []struct {
		source string
		native func(float64, float64) float64
	}{
		{"x + y", func(x, y float64) float64 { return x + y }},
		{"x - y", func(x, y float64) float64 { return x - y }},
		{"x * y", func(x, y float64) float64 { return x * y }},
		{"x / y", func(x, y float64) float64 { return x / y }},
		{"x % y", math.Mod},
		{"(x + y) * (x - y) / (abs(x) + 1)", func(x, y float64) float64 {
			return (x + y) * (x - y) / (math.Abs(x) + 1)
		}},
	}
	bindings := NewBindings()
	_ = bindings.Add(0, "x")
	_ = bindings.Add(1, "y")
	for iteration := 0; iteration < 500; iteration++ {
		x := random.Float64()*2e6 - 1e6
		y := random.Float64()*2e6 - 1e6
		if y == 0 {
			y = 1
		}
		for _, formula := range formulas {
			got, err := mustCompileNumeric(t, formula.source, bindings).Eval([]float64{x, y})
			want := formula.native(x, y)
			if err != nil || math.Float64bits(got) != math.Float64bits(want) {
				t.Fatalf("%q(%g,%g) = %g, %v; want %g", formula.source, x, y, got, err, want)
			}
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/expressions/mod.rs:387
//	test: prop_comparisons_match_native_evaluation
func TestPropertyComparisonsMatchNativeEvaluation(t *testing.T) {
	random := rand.New(rand.NewPCG(3, 4))
	formulas := []string{"x < y", "x <= y", "x > y", "x >= y", "x == y", "x != y"}
	bindings := NewBindings()
	_ = bindings.Add(0, "x")
	_ = bindings.Add(1, "y")
	for iteration := 0; iteration < 500; iteration++ {
		x, y := random.Float64()*2e6-1e6, random.Float64()*2e6-1e6
		want := []float64{
			boolNumber(x < y), boolNumber(x <= y), boolNumber(x > y),
			boolNumber(x >= y), boolNumber(x == y), boolNumber(x != y),
		}
		for index, formula := range formulas {
			got, err := mustCompile(t, formula, bindings).Eval([]float64{x, y})
			if err != nil || got != want[index] {
				t.Fatalf("%q = %g, %v; want %g", formula, got, err, want[index])
			}
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/expressions/mod.rs:402
//	test: prop_arbitrary_ascii_never_panics_lexer
func TestPropertyArbitraryASCIINeverPanicsLexer(t *testing.T) {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789+-*/ ().;=<>!&|^%,"
	random := rand.New(rand.NewPCG(5, 6))
	for iteration := 0; iteration < 2_000; iteration++ {
		length := random.IntN(65)
		source := make([]byte, length)
		for index := range source {
			source[index] = alphabet[random.IntN(len(alphabet))]
		}
		_, _ = Compile(string(source), NewBindings())
	}
}

func containsInt(values []int, want int) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func strconvItoa(value int) string {
	if value == 0 {
		return "0"
	}
	var digits [20]byte
	index := len(digits)
	for value > 0 {
		index--
		digits[index] = byte('0' + value%10)
		value /= 10
	}
	return string(digits[index:])
}
