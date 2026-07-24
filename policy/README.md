# Machine-readable repository policy

`go-package-policy.csv` is the single package-classification authority used by
`tools/policycheck`. Every Go package below `cmd/`, `internal/`, `scripts/`,
`testkit/`, `tests/`, and `tools/` must match one row.

Classifications have distinct meanings:

- `production-economic`: intended production money/domain code.
- `ported-compatibility`: native representations of pinned source tests and
  their temporary model implementations; not semantically accepted or wired
  production evidence.
- `test-placeholder`: deterministic fixtures that currently prove only the
  ported specification.
- `non-economic`: analytics or protocol behavior that may legitimately use
  floating point and cannot feed economic decisions.
- `infrastructure`: edge and adapter packages outside the pure core.
- `tooling`: repository maintenance and validation commands.

The `economic` and `deterministic` columns select AST-enforced restrictions.
New files inherit their package rule. Existing legacy float and panic surfaces
are enumerated file-by-file in the compatibility lists; entries are rejected
when they become stale. Compatibility entries are debt records, not permission
for the engine, ledger, margin, fee, funding, API-decoding, or persistence paths
to use floats or panic.

`make lint` runs the default full-tree `go vet` gate and the stricter
golangci-lint profile over production, infrastructure, non-economic, and
tooling classifications. Ported compatibility packages and placeholder test
packages remain outside that strict profile until their implementation cohort
is reviewed and reclassified; they are still compiled, tested, vetted, and
subject to the AST safety policy. Production packages may not import a
quarantined compatibility package.

`github.com/cockroachdb/apd/v3` is restricted to
`internal/decimal/economic`. That package is the sole production exact-decimal
implementation; the parent `internal/decimal` compatibility tree remains
quarantined. Classification rows may predeclare a package boundary for the
next implementation PR; strict lint selects only classified directories that
exist in the current tree.
