# Review Checklist

## Scope and preflight

- [ ] High-risk boundaries completed adversarial preflight; low-risk exemption is justified.
- [ ] Scope/design/failure matrix and representative failing tests were reviewed before implementation.
- [ ] Advisory blockers are explicitly closed by the originating specialist.

## Behavior and specification

- [ ] Tests were added/ported before production behavior.
- [ ] Every source assertion is represented.
- [ ] Every source test has one owned ledger row with exact revision, path, line, and function provenance.
- [ ] Every ported row names one unique native Go test.
- [ ] Conflicts and exclusions reference reviewed records under `ports/decisions/`.
- [ ] No unrelated behavior was introduced.

## Determinism

- [ ] Time, IDs, market data, sequence, config and instrument revisions are explicit.
- [ ] No wall clock, randomness, environment or map-order dependency in deterministic core.
- [ ] No goroutine mutates economic state outside the shard owner.
- [ ] Repeated tests produce identical canonical results.

## Money

- [ ] No floating-point economic values.
- [ ] Every rounding rule is explicit and tested.
- [ ] Ledger entries balance exactly.
- [ ] Duplicate execution creates no duplicate effect.
- [ ] Order/fill/position/bracket/margin invariants hold.

## Transactions and recovery

- [ ] Transaction boundary is stated.
- [ ] No network I/O occurs inside a DB transaction.
- [ ] Locks are acquired in deterministic order.
- [ ] Crash before/after commit and ack is tested where relevant.
- [ ] Acknowledgment occurs after commit.

## Messaging

- [ ] Stable message IDs and schema versions exist.
- [ ] Outbox/inbox behavior is used for durable effects.
- [ ] Stream limits and retries fail visibly.
- [ ] Engine ordering is preserved.

## Compatibility

- [ ] HTTP/gRPC/realtime contract changes are tested.
- [ ] Null/omitted fields and decimal formatting are exact.
- [ ] Idempotent retry returns the stored response.
- [ ] Intentional deviations are approved and documented.

## Database

- [ ] Frozen protected-branch or shared/persistent migration history is unchanged.
- [ ] Any unfrozen candidate was used only in an explicitly disposable database.
- [ ] Frozen schema corrections use a new forward migration.
- [ ] Constraints/indexes and upgrade path are tested.
- [ ] Economic SQL uses no floating point or `SELECT *`.

## Security and operations

- [ ] No secrets/PII in logs or events.
- [ ] Authorization and role boundaries remain intact.
- [ ] Metrics/alerts cover new failure modes.
- [ ] No leaked goroutines, streams, consumers, listeners or DB state.

## Commands

- [ ] `make policy`
- [ ] `make fmt-check`
- [ ] `make lint`
- [ ] `make test`
- [ ] `make test-race`
- [ ] `make test-repeat`
- [ ] Relevant integration/contract tests
- [ ] Exact candidate SHA/tree/base recorded; evidence belongs to that tree only
