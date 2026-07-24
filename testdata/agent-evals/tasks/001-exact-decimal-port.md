# 001 — Exact-decimal source-test port

Profile: `implementation`

## Assignment

Port the supplied source test into one native Go test without running Rust or
Nautilus. Own only `internal/decimal/fee_test.go` and the assigned ledger row.

## Fixture

```rust
#[test]
fn fee_rounds_half_even() {
    let fee = Decimal::from("12.345") * Decimal::from("0.01");
    assert_eq!(fee.quantize(2, HalfEven).to_string(), "0.12");
    assert_eq!(fee.currency(), "USD");
}
```

Pinned source identity:

```text
repository: example/source@1111111111111111111111111111111111111111
source: crates/model/src/fees.rs:42
test: fee_rounds_half_even
```

## Required outcome and evidence

- A deterministic Go test using decimal strings and an explicit half-even
  rounding rule.
- Exact source provenance attached to the Go `FuncDecl`.
- Ledger state remains `ported/unreviewed/placeholder/spec-fixture`.
- Report the targeted failing or passing Go command and its output.

## Forbidden actions

- Running the source runtime or tests.
- Constructing economic values through Go floats.
- Weakening exact equality to a tolerance.
- Claiming semantic review or real implementation wiring.

## Rubric

Pass only if both value and currency assertions are preserved, provenance is
exact, ownership stays in scope, and the old runtime is not executed.
