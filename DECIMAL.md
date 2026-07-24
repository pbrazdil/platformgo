# Exact Decimal and Numeric Policy

## 1. Single implementation

Only `internal/decimal` may import `github.com/cockroachdb/apd/v3`.

All other packages use immutable domain wrappers such as:

```text
Decimal
Money
Price
Quantity
Rate
Ratio
BasisPoints
```

Do not expose mutable `apd.Decimal` pointers or contexts across package boundaries.

## 2. Construction

Economic values are constructed from:

- canonical decimal strings;
- integers with an explicit scale;
- PostgreSQL numeric text/binary decoded exactly.

Forbidden construction paths:

- `float32` or `float64`;
- JSON numbers for fields contractually represented as strings;
- `fmt.Sprint` of an arbitrary numeric type;
- silently defaulting an invalid value to zero.

Parsing returns an error for:

- exponent notation where the external contract forbids it;
- NaN or infinity;
- out-of-range precision/scale;
- empty values;
- malformed sign/decimal separator;
- negative values for unsigned domain types.

## 3. Precision

The initial economic precision budget is 38 significant decimal digits. PostgreSQL economic columns use `NUMERIC(38,18)` unless an accepted ADR defines a different representation for a specific field.

Intermediate arithmetic must not silently exceed the precision budget. A test must cover every boundary introduced by a formula.

## 4. Contexts and rounding

There is no package-global “default arithmetic context” used indiscriminately.

Operations are divided into:

### Exact operations

Addition, subtraction, multiplication and exact quantization paths trap or return an error on unexpected rounded/inexact results.

### Policy operations

Division, fee calculation, funding, margin, VWAP, PnL, liquidation and wire quantization use named functions that require:

```text
result scale or instrument/currency precision
rounding mode
operation name
```

Example conceptual API:

```go
fee, err := decimal.MulQuantized(
    notional,
    feeRate,
    currency.Scale,
    decimal.RoundHalfEven,
)
```

A caller may not omit the rounding rule.

## 5. Rounding ownership

Rounding occurs only at a defined domain boundary:

- venue price/quantity normalization;
- fee or funding policy boundary;
- currency posting boundary;
- externally specified wire scale;
- tested margin/liquidation policy boundary.

Do not round intermediate values merely to make arithmetic convenient. Keep full allowed precision until the owning boundary.

Every rounding rule has table tests including:

- exact value;
- half-way value;
- positive/negative value where allowed;
- maximum precision;
- one unit below/above boundary.

## 6. Canonical formatting

Canonical internal/hash form:

- base-10 plain notation;
- no exponent;
- no leading `+`;
- one leading zero for values between `-1` and `1`;
- `-0` becomes `0`;
- no unnecessary leading zeros;
- trailing fractional zeros removed;
- empty fractional part removes the decimal point.

Examples:

```text
"0001.2300" -> "1.23"
"-0.000"    -> "0"
"0.500"     -> "0.5"
```

API formatting is field-specific and may retain required scale. Do not use canonical hash formatting when the compatibility contract requires `"1.2300"`.

## 7. Typed units

- `Money` always includes a currency.
- `Price` and `Quantity` are associated with an instrument revision.
- `Rate` and `Ratio` are not interchangeable with money.
- Basis points are represented as an integer type where possible.
- Cross-unit arithmetic requires an explicitly named function.

## 8. Database and wire rules

- PostgreSQL encode/decode is exact and never uses floating point.
- JSON economic values use strings when required by the contract.
- Protobuf uses the frozen representation; do not introduce `double` for economics.
- Logs should use canonical strings and include units.

## 9. Required tests

- parser and canonical formatter;
- every rounding mode actually used;
- PostgreSQL round trip;
- JSON/protobuf round trip;
- arithmetic overflow and inexact traps;
- `-0` normalization;
- fee, funding, margin, VWAP and PnL vectors ported from source tests;
- fuzzing of valid and invalid decimal strings;
- proof that no public economic API accepts a Go float.
