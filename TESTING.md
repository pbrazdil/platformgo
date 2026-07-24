# Testing and Native Test Porting

## 1. Test philosophy

Accepted tests are the maintained executable product specification, subject to `INVARIANTS.md` and reviewed decisions. Production code exists to satisfy deterministic tests and invariants, not the reverse.

The old Rust platform and Nautilus runtime are not run. Agents read tests at the pinned revisions and port their observable claims into native Go tests. Detailed per-agent instructions are in `docs/TEST_PORTING_PLAYBOOK.md`.

## 2. Source revisions

Use only the immutable revisions in `ports/SOURCE_REVISIONS.md`. Do not port from `main`, a local working tree, or an uncommitted patch.

Every ported test must identify:

```go
// Ported from:
//   repository: <owner/repository>@<commit>
//   source: <path>:<line>
//   test: <function>
// Adaptations:
//   - ...
// Assertions preserved:
//   - ...
```

Every source function has one row in `ports/test-port-map.csv`, including its pinned source line and current owner:

```csv
source_repo,source_revision,source_file,source_test,source_line,go_file,go_test,category,status,owner,notes
```

`source_repo` is the pinned alias `platform` or `nautilus`; its revision and allowed source roots must match `ports/SOURCE_REVISIONS.md`. Allowed category values are:

```text
unit
model
integration-postgres
integration-messaging
contract-http
contract-grpc
contract-realtime
adapter-hyperliquid
live-canary
not-applicable
```

## 3. Source-of-truth rules

Interpret source tests in this order:

1. explicit assertions;
2. values and state created directly by the test;
3. observable helper behavior required for those assertions;
4. comments and function name;
5. linked documentation.

Do not infer behavior from production implementation code when the test does not assert it.

When a source test is weak, preserve it at minimum. Strengthen only when static inputs and a tested rule determine an exact result.

When source tests conflict, inventory both as `conflict`, create `ports/decisions/<topic>.md`, and stop for an owner decision. Port the behavior selected by that decision; do not merge contradictory tests or choose silently.

## 4. Test layers

### 4.1 Unit tests

Pure exact arithmetic, parsing, validation and state transitions. No I/O, goroutines, clocks, environment or sleeps.

### 4.2 Model/engine tests

Ordered multi-step behavior through the deterministic engine using explicit:

- initial state;
- IDs;
- market events;
- input sequence;
- logical clock;
- config and instrument revisions.

These are the primary economic correctness suite.

### 4.3 PostgreSQL integration tests

Use real PostgreSQL when testing:

- transactionality;
- constraints;
- migrations;
- locking;
- crash recovery;
- outbox/inbox;
- idempotency storage;
- ledger folding.

Each test gets isolated state and full cleanup.

### 4.4 NATS integration tests

Use real NATS/JetStream when testing:

- publish acknowledgment;
- durable pull consumption;
- ordering;
- redelivery;
- deduplication;
- restart and replay;
- stream-limit failure behavior.

Domain tests do not require NATS.

### 4.5 Contract tests

Preserve exact REST, gRPC and realtime behavior:

- status and error mapping;
- fields, casing, nullability and decimal strings;
- headers, cookies and idempotency;
- protobuf field numbers/enums;
- channel/token/envelope behavior.

### 4.6 Hyperliquid adapter tests

Use checked-in static frames under `testdata/` for:

- subscription/ack messages;
- book, BBO, mark and context data;
- metadata and precision;
- reconnect/snapshot/gap events;
- malformed and unknown payloads.

### 4.7 Live canaries

A minimal scheduled suite may contact Hyperliquid to detect protocol drift. It does not assert economic prices and does not gate deterministic correctness.

## 5. Porting process

For each assigned Rust test:

1. Read the full test, helpers, constants and fixture builder.
2. Inventory:

```text
Initial state:
Inputs/actions:
Market events:
Clock/timers:
Faults/restarts:
Assertions:
Infrastructure noise to remove:
Ambiguities:
```

3. Choose the smallest correct Go layer.
4. Port all explicit assertions.
5. Replace live data with explicit market events.
6. Replace polling/sleep with synchronous state application or a bounded integration barrier.
7. Replace old-schema SQL assertions with domain snapshots, new repository checks, or external contract assertions as appropriate.
8. Add provenance and update the port map.
9. Verify the test fails for missing behavior.
10. Implement the minimum production logic.

## 6. Deterministic testkit

The shared testkit provides:

```text
testkit/clock.go       manual logical clock
testkit/ids.go         deterministic ID sequence
testkit/market.go      explicit books, marks, quotes and feed gaps
testkit/engine.go      synchronous engine fixture
testkit/account.go     typed account setup
testkit/faults.go      semantic crash/failure points
testkit/eventually.go  bounded integration-only barriers
testkit/canonical.go   stable result normalization and hashes
```

Only the designated harness owner edits these shared files without coordination.

## 7. Forbidden test patterns

Forbidden in unit/model tests:

- `time.Sleep`;
- `time.Now`;
- random IDs;
- live network access;
- environment-variable mutation;
- polling database state;
- `float32`/`float64` economic assertions;
- dependence on map order;
- `t.Parallel()`;
- permanent `t.Skip`;
- broad tolerances used to hide nondeterminism.

Integration tests may use `context.WithTimeout` and `testkit.Eventually`, but must wait on explicit state/watermarks rather than arbitrary time passage.

## 8. Fault testing

Failpoints are named by semantic boundary, for example:

```text
command.after-insert-before-outbox
engine.after-ledger-before-state
engine.after-commit-before-ack
outbox.after-publish-before-mark
consumer.after-effect-before-inbox-commit
consumer.after-commit-before-ack
realtime.after-publish-before-mark
```

Every failpoint test must prove convergence after restart/redelivery and exact business-effect count.

Do not use line numbers or sleeps to approximate a crash window.

## 9. Property and fuzz tests

Use Go fuzzing and generated command sequences for:

- decimal parsing/formatting;
- precision boundaries;
- order transitions;
- command duplication/reordering where allowed;
- fill splitting/aggregation;
- netting/hedging/reversal;
- bracket lifecycle;
- ledger balancing;
- recovery at failpoints;
- JSON/protobuf/Hyperliquid decoding.

Persist every discovered regression as a deterministic named test case.

## 10. Repetition and race requirements

Required before merge:

```bash
go test ./... -count=1
go test -race ./... -count=1
go test ./internal/... ./testkit/... -count=20
```

The repeat suite must not rely on test order.

Longer scheduled suites should include:

```bash
go test ./... -count=100
go test -race ./... -count=10
```

## 11. Resource cleanup

After each integration test there must be no leaked:

- database/schema;
- PostgreSQL connection or transaction;
- NATS stream, consumer or subscription;
- goroutine;
- listener;
- process/container;
- temporary file;
- environment mutation;
- global cache entry.

Use `t.Cleanup` and verify cleanup where possible.

## 12. Statuses in the port map

Allowed values:

```text
discovered
reserved
in-progress
ported-failing
ported-green
conflict
deferred-live
not-applicable
```

`not-applicable` requires a reviewed `ports/decisions/` note proving that the source test asserts only a Rust/Nautilus implementation detail with no required observable consequence.

Every status except `discovered` requires a non-empty owner. A row may move to `ported-failing` or `ported-green` only when it names one unique native Go test with exact revision, source path, line, and function provenance.

The `not-applicable` category and status must be used together. `deferred-live` is valid only for the `live-canary` category.

`discovered`, `reserved`, `in-progress`, `ported-failing`, and `deferred-live` are non-terminal. A full port is complete only when every row is `ported-green`, `conflict` with a reviewed decision, or `not-applicable` with a reviewed decision, and `make port-map-complete` passes.

## 13. Required CI gates

- policy checks;
- dependency allowlist;
- migration immutability;
- gofmt and lint;
- unit/model tests;
- race detector;
- deterministic repeat tests;
- PostgreSQL/NATS integration tests;
- contract tests;
- port-map validation;
- vulnerability scan.
