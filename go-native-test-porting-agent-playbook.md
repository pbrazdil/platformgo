# Native Go Test-Porting Playbook

## Purpose

Port the tests from `upcomers-org/platform` and the relevant NautilusTrader test suites into native Go tests for a clean-room Go rewrite.

The Go tests are the executable specification and the source of truth. The old Rust platform and Nautilus runtime must not be executed, embedded, queried, or used as a differential oracle during development or CI.

Before work starts, pin the exact source revisions:

```text
PLATFORM_SOURCE_COMMIT=<immutable Git commit>
NAUTILUS_SOURCE_REVISION=<immutable Git commit/submodule revision>
```

Never port from a moving branch such as `main`.

---

## Non-negotiable rules

1. **Native Go only.** Every ported test must be an ordinary Go `_test.go` test using `testing`.
2. **Do not run the old system.** Agents may read Rust source, fixtures, comments, and helper implementations, but may not execute Rust binaries, Docker stacks, test suites, or live reference services.
3. **The source test is normative.** Preserve its explicit assertions and observable behavior. Do not infer additional behavior from the production implementation.
4. **Port behavior, not Rust architecture.** Do not reproduce actors, caches, thread models, Rust ownership structures, or Nautilus internals unless the source test explicitly asserts an observable consequence of them.
5. **No floating-point money.** Use the rewrite's exact decimal types. Inputs and expected values enter tests as strings.
6. **No live market data in deterministic tests.** Replace Hyperliquid WebSocket/API setup with explicit test market events or a fake Hyperliquid adapter.
7. **No sleeps in unit/model tests.** Use a manual clock, explicit event delivery, deterministic scheduler, and synchronous state transitions.
8. **Do not weaken assertions.** Do not replace exact equality with ranges, remove assertions, or assert only that no error occurred.
9. **Do not silently skip tests.** Every source test must be accounted for in `test-port-map.csv`.
10. **Preserve provenance.** Every Go test must identify the source repository revision, file, and Rust test function.
11. **One authoritative writer in integration tests.** Tests must not depend on goroutine scheduling for domain event ordering.
12. **No hidden global state.** Environment variables, clocks, random generators, IDs, singleton caches, and message buses must be isolated per test.

---

## What counts as the source of truth

When interpreting a Rust test, use this precedence order:

1. Explicit assertions in the test.
2. Values and state created directly by the test.
3. Observable behavior of test helpers required to make the assertions meaningful.
4. Test comments and test function name.
5. Documentation linked by the test.

Production code outside the test is not normative. It may be read only to understand types, helper behavior, fixture construction, and terminology.

### Conflicting tests

When two source tests require incompatible behavior:

- Port both tests unchanged in meaning.
- Mark both rows as `conflict` in `test-port-map.csv`.
- Create a short decision note under `ports/decisions/`.
- Do not choose one behavior silently.

### Weak source assertions

Preserve the source assertion at minimum. Strengthening is allowed only when the stronger expected result is mathematically or logically determined by static test inputs.

Example: a source test injects a fixed L2 book and only asserts `VWAP > top`. It is acceptable to assert the exact decimal VWAP if the fill policy is explicitly established by that same source test or its fixture. Document the strengthening in the test provenance comment.

---

## Target repository layout

```text
cmd/
  api/
  engine/

internal/
  decimal/
  clock/
  ids/
  order/
  matching/
  position/
  bracket/
  margin/
  funding/
  liquidation/
  ledger/
  engine/
  hyperliquid/
  postgres/
  messaging/

testkit/
  account.go
  market.go
  engine.go
  clock.go
  ids.go
  faults.go
  eventually.go

tests/
  integration/
    postgres/
    messaging/
    recovery/
  contract/
    http/
    grpc/
    realtime/

ports/
  test-port-map.csv
  decisions/
```

Recommended mapping:

```text
apps/nautilus/tests/*/trading/*  -> internal/{order,matching,position,bracket}/..._test.go
apps/nautilus/tests/*/margin/*   -> internal/{margin,liquidation,funding}/..._test.go
apps/nautilus/tests/it/recovery  -> tests/integration/recovery/..._test.go
apps/app/tests/it/*              -> tests/integration or tests/contract
apps/app/tests/live/*            -> deterministic adapter/contract tests plus separate live canaries
upstream Nautilus model tests    -> internal/order, matching, position, margin, emulator-equivalent packages
```

---

## Required provenance header

Every ported test file or test function must include:

```go
// Ported from:
//   platform: upcomers-org/platform@<PLATFORM_SOURCE_COMMIT>
//   source: apps/nautilus/tests/live/trading/e2e_modify_order.rs
//   test: resting_limit_can_be_modified
// Adaptations:
//   - LiveStack replaced by deterministic Go fixture.
//   - SQL polling replaced by direct repository/snapshot assertion.
//   - Hyperliquid feed replaced by explicit market book.
// Assertions preserved:
//   - Resting limit remains working after modification.
//   - Price changes from 1.00 to 2.00.
//   - Quantity changes from 0.001 to 0.002.
```

Do not write vague comments such as `ported from Rust`.

---

## Porting workflow for each source test

### Step 1: Read the complete source context

Read:

- The Rust test function.
- Every helper called by that test.
- The relevant fixture builder.
- Constants used by the test.
- Any source test referenced in comments.

Do not start coding after reading only the test body.

### Step 2: Write a behavior inventory

Before creating the Go test, record in the PR description:

```text
Initial state:
Inputs/actions:
Market data:
Clock/timers:
Faults/restarts:
Observable assertions:
Implementation-specific plumbing to remove:
Ambiguities:
```

### Step 3: Classify the test

Use one category:

- `unit`: pure arithmetic or state transition.
- `model`: deterministic multi-step engine behavior.
- `integration-postgres`: persistence, transactions, migrations, recovery.
- `integration-messaging`: outbox, deduplication, redelivery, ordering.
- `contract-http`: exact HTTP behavior.
- `contract-grpc`: exact protobuf/gRPC behavior.
- `contract-realtime`: external realtime token/channel/envelope behavior.
- `adapter-hyperliquid`: protocol parsing, subscription, reconnect, precision.
- `live-canary`: minimal non-deterministic protocol smoke test, never part of economic CI.
- `not-applicable`: only for implementation-specific tests with no required observable behavior.

### Step 4: Remove infrastructure noise

Translate source setup to the smallest deterministic Go fixture that preserves the asserted behavior.

Examples:

```text
LiveStack::builder().boot()        -> testkit.NewEngineFixture(t, ...)
PlatformTestEnv::boot()            -> testkit.NewPlatformFixture(t, ...)
Composition::execute(command)      -> fixture.Execute(command)
QuoteInjector::inject_book...      -> fixture.Market.ApplyBook(...)
inject_mark                        -> fixture.Market.ApplyMark(...)
tokio::time::sleep                 -> fixture.Clock.Advance(...), or no operation
tokio::time::timeout polling       -> synchronous assertion or bounded Eventually
sqlx query against old schema      -> Go domain snapshot, repository query, or public API assertion
runtime.stop / restart             -> fixture.Crash(component), fixture.Restart(component)
UZO_TEST_* crash env               -> explicit failpoint/fault injector
Centrifugo offset                  -> captured external realtime publication
```

### Step 5: Port the test natively

Use idiomatic Go:

- `t.Run` for cases.
- Table-driven tests for vectors and transition matrices.
- `t.Helper()` in helpers.
- `t.Cleanup()` for resources.
- `context.WithTimeout` for integration boundaries.
- No assertion framework is required; standard library helpers are preferred.
- Do not use `t.Parallel()` until all shared state has been eliminated.

### Step 6: Make determinism explicit

Every test must explicitly control, as applicable:

- Clock.
- IDs.
- Account sequence.
- Command order.
- Market event order.
- Configuration revision.
- Instrument metadata.
- Message redelivery.
- Fault position.

A test must not call `time.Now`, random UUID generation, or a live venue from domain logic.

### Step 7: Preserve assertions

Create a checklist from the source assertions and verify each appears in Go.

Do not merge a port when even one source assertion is missing.

### Step 8: Update the port map

Add one row per Rust test function:

```csv
source_repo,source_revision,source_file,source_test,source_line,go_file,go_test,category,status,notes
platform,<commit>,apps/nautilus/tests/live/trading/e2e_modify_order.rs,resting_limit_can_be_modified,48,internal/order/modify_order_test.go,TestModifyOrder_RestingLimit,model,ported-green,"live feed replaced with fixed book"
```

Allowed statuses:

```text
discovered
in-progress
ported-failing
ported-green
conflict
deferred-live
not-applicable
```

---

## Test translation rules

| Rust/source construct | Native Go translation |
|---|---|
| `#[tokio::test]` | `func TestXxx(t *testing.T)` |
| `assert_eq!` | exact Go comparison/helper |
| approximate `f64` money assertion | exact decimal or preserved tolerance expressed with decimal |
| `Result<()>` | fail test immediately with `t.Fatalf` |
| builder fixture | typed Go test fixture options |
| `tokio::sleep` | manual clock or explicit event application |
| timeout/poll loop | synchronous state assertion; `Eventually` only for real integration boundaries |
| direct SQL assertion | Go repository assertion when persistence is contractual; otherwise domain snapshot/public API |
| env-based fault injection | explicit failpoint object |
| live WS market data | fixed fake adapter events |
| Nautilus event object | equivalent Go domain event if behaviorally relevant |
| Nautilus actor/strategy/cache internals | omit unless externally asserted |
| Rust enum | typed Go enum/string with invalid-value test coverage |
| `HashSet` comparison | sort canonical keys before comparing |
| ignored test due live dependency | make deterministic and activate |
| ignored test due known bug | port as active failing test unless source expectation is incomplete |

---

## Test categories and exact porting guidance

### 1. Decimal, money, quantity, price, fees

Port these first.

Rules:

- Inputs are string literals.
- Expected outputs are string literals.
- Never parse through `float64`.
- Every rounding operation must state a rounding rule.
- Include invalid precision, overflow, zero, negative, and boundary cases present in source tests.

Preferred shape:

```go
func TestBookVWAP(t *testing.T) {
    tests := []struct {
        name string
        asks []Level
        qty  string
        want string
    }{
        // source-derived cases
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            got := MatchMarketBuy(mustQty(tt.qty), tt.asks)
            requireDecimalEqual(t, tt.want, got.AveragePrice)
        })
    }
}
```

### 2. Order state machine

Port state and transition behavior directly:

- submit
- accept/reject
- working
- trigger
- partial fill
- fill
- modify
- cancel
- reduce-only
- IOC/FOK/GTC
- terminal-state idempotency

Each test should state the initial order, input event, expected new state, and emitted events.

Do not depend on goroutine timing.

### 3. Matching and market data

The source test's feed mechanism is not normative. Its asserted matching behavior is.

Use explicit events:

```go
fixture.Market.ApplyBook(Book{
    Instrument: "BTC-PERP",
    Bids: []Level{{Price: D("59900"), Qty: D("1")}},
    Asks: []Level{
        {Price: D("60000"), Qty: D("0.002")},
        {Price: D("60100"), Qty: D("0.002")},
        {Price: D("60200"), Qty: D("0.002")},
    },
})
```

Then execute the command synchronously.

For tests involving commands between market updates, apply events in exact order in the test. Sequence is defined by the order of method calls.

### 4. OMS, positions, netting, hedging, reversal

Assert:

- resulting position side
- quantity
- average entry
- realized PnL
- remaining working orders
- account isolation
- strategy/leg isolation only where externally meaningful

Do not reproduce Nautilus `StrategyId` or actor structures unless the source test explicitly exposes their effect.

### 5. Brackets, stop loss, take profit

Split large Rust tests into focused Go tests when necessary, but retain one-to-one provenance.

Minimum cases:

- resting entry retains held protective legs
- filled entry arms protective legs
- correct opposite side
- reduce-only protection
- TP execution cancels SL
- SL execution cancels TP
- normal close cancels protection
- reversal cancels old protection
- restart restores valid protection exactly once

### 6. Margin, liquidation, funding

Make all inputs explicit:

- balance
- position
- entry price
- mark price
- leverage
- fee schedule
- margin mode
- funding rate
- clock boundary

Assert exact values and emitted actions.

Timer-driven behavior uses a manual clock:

```go
clock.Set(T("2026-01-01T00:59:59Z"))
engine.Apply(...)
clock.Advance(time.Second)
engine.RunDue()
```

### 7. Persistence and recovery

Use a real PostgreSQL integration test only when transactionality/recovery is part of the source assertion.

Requirements:

- isolated database/schema per test
- forward migrations applied
- deterministic IDs
- explicit crash failpoint
- restart from persisted state
- no sleeps to guess crash position
- exact row/event counts

Example failpoints:

```text
order.after-command-commit
order.after-fill-before-outbox
balance.after-ledger-before-balance-projection
outbox.after-publish-before-mark-published
consumer.after-effect-before-ack
```

The fault name must describe a semantic transaction boundary, not a line number.

### 8. Messaging

Domain tests must not require NATS.

Integration tests with NATS must assert:

- durable publication
- at-least-once redelivery
- inbox deduplication
- ordering within the defined key/shard
- acknowledgement after durable commit
- replay after restart

Never claim exactly-once transport. Assert exactly-once business effect.

### 9. HTTP and gRPC contracts

Port exact external behavior:

- method/path/service
- status code/gRPC code
- request validation
- decimal serialization
- field names
- omitted vs `null`
- headers and cookies
- idempotency behavior
- error code and body

Use `httptest` for HTTP handlers and `bufconn` or an ephemeral loopback listener for gRPC.

Do not assert internal database shape in contract tests.

### 10. Realtime

Port the externally observed contract:

- token claims
- channel name
- envelope type
- payload fields
- event order where contractual
- deduplication semantics

Do not require the same internal broker. The Go implementation may use NATS while preserving the external event contract.

### 11. Hyperliquid adapter

Separate adapter protocol tests from economic engine tests.

Use recorded/static JSON frames checked into `testdata/` for:

- subscription responses
- book snapshots
- BBO
- marks
- asset contexts
- reconnect snapshots
- malformed frames
- precision metadata

A minimal live canary may verify current protocol compatibility, but it must not be a required economic correctness test.

---

## Ignored, flaky, and live tests

### Source test is ignored because it needs live Hyperliquid

Port it as an active deterministic test using explicit market events.

### Source test is ignored because the current implementation is broken

Port it as an active failing test. The rewrite should implement the stated expected behavior.

### Source test is flaky due polling or global state

Remove the flakiness. Preserve the assertions.

### Source test has no stable expected value

Preserve only what it explicitly asserts. Supply deterministic inputs that make those assertions meaningful. Do not invent additional exact outputs.

### Test is purely implementation-specific

Mark `not-applicable` only when all are true:

- it asserts a Nautilus/Rust internal mechanism;
- no external or economic consequence is asserted;
- no equivalent invariant is required in the Go design;
- the PR documents the reason.

---

## TDD commit and PR policy

Preferred vertical-slice history:

```text
Commit 1: port source tests; tests compile and fail for missing behavior
Commit 2+: implement the minimum behavior to make them pass
Commit final: refactor without changing test semantics
```

Do not squash away the test-first commit during review.

A test-only bulk-port branch may be red. Protected `main` should remain green. The preferred merge unit is a coherent vertical slice containing the test-first commit and implementation commit.

Do not place tests behind permanent skips or build tags merely to keep CI green.

---

## Multi-agent coordination

### Ownership model

Assign one agent to each cohesive source area:

```text
Agent A: decimal + money + precision
Agent B: order state machine + TIF + modify/cancel
Agent C: matching + VWAP + slippage
Agent D: positions + netting + hedging + reduce-only
Agent E: brackets + stops + trigger semantics
Agent F: margin + liquidation + funding
Agent G: PostgreSQL recovery + exactly-once business effects
Agent H: HTTP/gRPC/realtime contract tests
Agent I: Hyperliquid adapter protocol fixtures/tests
```

### Shared files

Only a designated harness owner may edit these without coordination:

```text
testkit/engine.go
testkit/account.go
testkit/market.go
testkit/clock.go
testkit/ids.go
testkit/faults.go
internal/decimal/*
ports/test-port-map.csv
```

Other agents propose required harness changes in their PR description or a small isolated commit.

### Work unit size

Assign one source file or one tightly related test module per task. Avoid tasks such as `port all trading tests`.

### No duplicated source ownership

The orchestrator must reserve each source test row in `test-port-map.csv` before assigning it.

---

## Agent acceptance criteria

A porting task is complete only when:

1. Every assigned Rust test function has a corresponding row in `test-port-map.csv`.
2. Every test with status `ported-failing` or `ported-green` has a native Go test function.
3. Every Go test contains exact source provenance.
4. Every explicit source assertion is represented.
5. Live venue dependencies are removed from deterministic tests.
6. No money assertion uses `float32` or `float64`.
7. No model/unit test sleeps or polls.
8. IDs, clock, market data, and faults are deterministic.
9. Ignored tests are handled according to the rules above.
10. Tests compile.
11. The PR states which tests are expected to fail before implementation.
12. Once implemented, tests pass under:

```bash
go test ./... -count=1
go test -race ./... -count=1
go test ./... -count=20
```

13. No resource remains after integration tests: database/schema, NATS stream/consumer, process, goroutine, file, or listener.

---

## Master prompt for a porting agent

Copy this prompt and fill in the placeholders.

```text
You are porting tests from a pinned Rust brokerage implementation into native Go tests for a clean-room rewrite.

Source revisions:
- platform: upcomers-org/platform@<PLATFORM_SOURCE_COMMIT>
- NautilusTrader: <NAUTILUS_SOURCE_REVISION>

Assigned source files/tests:
<SOURCE_PATHS_AND_TEST_NAMES>

Hard constraints:
1. Do not run the Rust platform, Nautilus, Docker stack, or any old service.
2. Do not use the old runtime as an oracle. The source test code and its explicit assertions are the specification.
3. Write ordinary native Go `_test.go` tests using `testing`.
4. Port observable behavior, not Rust/Nautilus architecture.
5. Preserve every explicit source assertion. Do not weaken assertions.
6. Use exact decimal types and string literals for money, price, quantity, margin, fees, and PnL. Never use float64 for economic assertions.
7. Replace live Hyperliquid inputs with deterministic test market events or static protocol fixtures.
8. Replace sleeps and polling with a manual clock, explicit event application, deterministic scheduler, or a bounded integration barrier.
9. Use deterministic IDs and isolated state.
10. Do not silently skip tests. Account for every assigned source test in ports/test-port-map.csv.
11. Add source provenance comments to every Go test.
12. Do not implement unrelated production functionality.

Process:
A. Read each source test, all helpers it calls, and the relevant fixture builder.
B. Write a behavior inventory: initial state, actions, market inputs, clock, faults, assertions, and infrastructure noise.
C. Classify each test as unit, model, integration-postgres, integration-messaging, contract-http, contract-grpc, contract-realtime, adapter-hyperliquid, live-canary, or not-applicable.
D. Port it to the smallest deterministic native Go test preserving all assertions.
E. Update ports/test-port-map.csv.
F. Run gofmt and the narrowest relevant go test command.

Source-of-truth precedence:
1. explicit assertions;
2. test-local setup values;
3. required observable helper behavior;
4. comments and name;
5. documentation.
Production implementation details are not normative.

When source behavior is ambiguous or tests conflict, do not decide silently. Add a conflict note and report it.

Deliverables:
- native Go test files;
- any narrowly required testkit additions;
- updated test-port-map.csv rows;
- PR summary listing assertions preserved, adaptations made, expected failures, and ambiguities.

Do not return a prose-only analysis. Make the code changes.
```

---

## Per-task prompt template

```text
Port the following source test module to native Go tests:

Source revision:
  platform: <commit>
  nautilus: <revision>

Source:
  <path>

Tests:
  - <test function>
  - <test function>

Target package:
  <Go package/path>

Required behavior:
  Treat every explicit source assertion as normative.

Expected adaptations:
  - <for example: LiveStack -> deterministic engine fixture>
  - <for example: QuoteInjector -> explicit L2 book>
  - <for example: SQL polling -> repository snapshot>

Out of scope:
  - production implementation beyond signatures required for the tests to compile
  - unrelated refactors
  - executing the Rust source

Acceptance:
  - tests are native Go `_test.go` tests;
  - exact decimals, deterministic clock/IDs/market input;
  - all source assertions preserved;
  - no sleeps in model tests;
  - provenance comments added;
  - test-port-map.csv updated;
  - gofmt clean;
  - report any ambiguity or conflict.
```

---

## Example: native port of a resting-limit modification test

The source behavior is:

- submit a deep out-of-market limit buy for `0.001 @ 1.00`;
- it becomes `working`;
- modify it to `0.002 @ 2.00`;
- it remains `working`;
- stored quantity and price reflect the modification.

A native Go model test should look conceptually like:

```go
func TestModifyOrder_RestingLimitRemainsWorking(t *testing.T) {
    // Ported from:
    //   platform: upcomers-org/platform@<commit>
    //   source: apps/nautilus/tests/live/trading/e2e_modify_order.rs
    //   test: resting_limit_can_be_modified
    // Adaptations:
    //   - Live Hyperliquid stack replaced with an explicit fixed market book.
    //   - SQL polling replaced by synchronous engine snapshot assertions.

    f := testkit.NewEngineFixture(t,
        testkit.WithClock("2026-01-01T00:00:00Z"),
        testkit.WithAccount("account-1", "1000000", "USDC"),
        testkit.WithInstrument("BTC-PERP"),
    )

    f.ApplyBook("BTC-PERP",
        []testkit.Level{{Price: "59999.00", Quantity: "1.000"}},
        []testkit.Level{{Price: "60000.00", Quantity: "1.000"}},
    )

    orderID := f.SubmitOrder(testkit.SubmitOrder{
        AccountID:   "account-1",
        Instrument:  "BTC-PERP",
        Side:        "buy",
        Type:        "limit",
        Quantity:    "0.001",
        LimitPrice:  "1.00",
        TimeInForce: "GTC",
    })

    requireOrder(t, f, orderID, OrderExpectation{
        Status:   "working",
        Quantity: "0.001",
        Price:    "1.00",
    })

    f.ModifyOrder(testkit.ModifyOrder{
        AccountID:  "account-1",
        OrderID:    orderID,
        Quantity:   ptr("0.002"),
        LimitPrice: ptr("2.00"),
    })

    requireOrder(t, f, orderID, OrderExpectation{
        Status:   "working",
        Quantity: "0.002",
        Price:    "2.00",
    })
}
```

This is native Go, deterministic, and directly derived from the source assertions. It neither runs nor imitates the Rust runtime.

---

## Final principle

The porting target is not line-by-line syntactic similarity. The target is one-to-one preservation of the source test's behavioral claims in deterministic, idiomatic Go tests.

The old test tells the agent **what must be true**. The Go test should express that truth with the smallest amount of new-system plumbing possible.
