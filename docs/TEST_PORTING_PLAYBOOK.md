# Native Go Test-Porting Playbook

## Purpose

Port every test in the pinned source scope from `upcomers-org/platform` and NautilusTrader into native Go tests for a clean-room Go rewrite. The exact repositories, revisions, source roots, and expected inventory counts are fixed in `ports/SOURCE_REVISIONS.md`.

Accepted Go tests become the maintained executable specification, subject to `INVARIANTS.md` and reviewed decisions. The old Rust platform and Nautilus runtime must not be executed, embedded, queried, or used as a differential oracle during development or CI.

Before work starts, pin the exact source revisions:

```text
PLATFORM_SOURCE_COMMIT=<immutable Git commit>
NAUTILUS_SOURCE_REVISION=<immutable Git commit/submodule revision>
```

Never port from a moving branch such as `main`.

### Final goal

The port is complete when the ledger matches the expected inventory counts, every in-scope source test has exactly one row, every applicable behavior is represented by an accepted deterministic native Go test, and every exclusion or source conflict has a reviewed decision record. No row remains `discovered`, `reserved`, `in-progress`, `ported-failing`, or `deferred-live`; the required Go verification passes without executing or depending on the old runtime.

---

## Non-negotiable rules

1. **Native Go only.** Every ported test must be an ordinary Go `_test.go` test using `testing`.
2. **Do not run the old system.** Agents may read Rust source, fixtures, comments, and helper implementations, but may not execute Rust binaries, Docker stacks, test suites, or live reference services.
3. **The source test is normative within the authority order.** Subject to `INVARIANTS.md`, accepted Go tests, and reviewed decisions, preserve its explicit assertions and observable behavior. Do not infer additional behavior from the production implementation.
4. **Port behavior, not Rust architecture.** Do not reproduce actors, caches, thread models, Rust ownership structures, or Nautilus internals unless the source test explicitly asserts an observable consequence of them.
5. **No floating-point money.** Use the rewrite's exact decimal types. Inputs and expected values enter tests as strings.
6. **No live market data in deterministic tests.** Replace Hyperliquid WebSocket/API setup with explicit test market events or a fake Hyperliquid adapter.
7. **No sleeps in unit/model tests.** Use a manual clock, explicit event delivery, deterministic scheduler, and synchronous state transitions.
8. **Do not weaken assertions.** Do not replace exact equality with ranges, remove assertions, or assert only that no error occurred.
9. **Do not silently skip tests.** Every source test must be accounted for in `ports/test-port-map.csv`.
10. **Preserve provenance.** Every Go test must identify the source repository revision, file, line, and Rust test function.
11. **One authoritative writer in integration tests.** Tests must not depend on goroutine scheduling for domain event ordering.
12. **No hidden global state.** Environment variables, clocks, random generators, IDs, singleton caches, and message buses must be isolated per test.

---

## How to interpret a source test

After applying the repository authority order in `AGENTS.md`, interpret a Rust test in this order:

1. Explicit assertions in the test.
2. Values and state created directly by the test.
3. Observable behavior of test helpers required to make the assertions meaningful.
4. Test comments and test function name.
5. Documentation linked by the test.

Production code outside the test is not normative. It may be read only to understand types, helper behavior, fixture construction, and terminology.

### Conflicting tests

When two source tests require incompatible behavior:

- Inventory both tests without weakening either claim.
- Mark both rows as `conflict` in `ports/test-port-map.csv`.
- Create a short decision note under `ports/decisions/`.
- Stop for an owner decision before accepting either behavior.
- After the decision, port every approved behavior and keep the rejected source claim accounted for by its `conflict` row and decision reference.

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
//   repository: upcomers-org/platform@<PLATFORM_SOURCE_COMMIT>
//   source: apps/nautilus/tests/live/trading/e2e_modify_order.rs:<SOURCE_LINE>
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
source_repo,source_revision,source_file,source_test,source_line,go_file,go_test,category,status,owner,notes
platform,<commit>,apps/nautilus/tests/live/trading/e2e_modify_order.rs,resting_limit_can_be_modified,48,internal/order/modify_order_test.go,TestModifyOrder_RestingLimit,model,ported-green,test_porter,"live feed replaced with fixed book"
```

Allowed statuses:

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

The `not-applicable` category and status must be used together. `deferred-live` is valid only for the `live-canary` category.

`discovered`, `reserved`, `in-progress`, `ported-failing`, and `deferred-live` are non-terminal. The only terminal statuses are `ported-green`, `conflict` with a reviewed `ports/decisions/` record, and `not-applicable` with a reviewed `ports/decisions/` record. Run `make port-map-complete` only when claiming the full pinned inventory is complete.

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
- a reviewed decision note under `ports/decisions/` documents the reason.

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

## Agent execution profile

All porting agents and subagents use the exact model `gpt-5.6-sol`. The default porting profile is `.codex/agents/test-porter.toml` with `high` reasoning effort. Do not override it with the `gpt-5.6` alias or a cheaper variant.

Repository rules live in `AGENTS.md`, `TESTING.md`, and this playbook. Task prompts must not paste those rules again. Give the agent only the assigned source, target package, local adaptations, required evidence, and success criteria.

## Multi-agent coordination

Delegate only independent modules with non-overlapping file ownership. A useful work split is:

```text
decimal + money + precision
order state machine + TIF + modify/cancel
matching + VWAP + slippage
positions + netting + hedging + reduce-only
brackets + stops + trigger semantics
margin + liquidation + funding
PostgreSQL recovery + exactly-once business effects
HTTP/gRPC/realtime contract tests
Hyperliquid protocol fixtures/tests
```

The primary agent reserves source tests in `ports/test-port-map.csv`, assigns one source file or tightly related module per agent, prevents overlapping edits, waits for all results, integrates the work, and runs final validation. Parallel read-heavy work is preferred.

Only the designated harness owner edits these shared areas without prior coordination:

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

## Agent acceptance criteria

A porting task is complete only when:

1. Every assigned source test has exactly one `ports/test-port-map.csv` row with its pinned source line.
2. The row is reserved to an owner before implementation starts.
3. Every `ported-failing` or `ported-green` row names one unique native Go test.
4. Every Go test contains the pinned revision, exact source path and line, and source test name.
5. Every explicit source assertion is preserved or strengthened.
6. Live venue dependencies are removed from deterministic economic tests.
7. Economic assertions use exact values, never floating point.
8. Clock, IDs, market inputs, sequence, and faults are deterministic.
9. Sleeps, unbounded polling, permanent skips, and hidden old-runtime calls are absent.
10. Ignored or broken source tests are classified explicitly rather than dropped.
11. Tests compile and fail for the intended missing behavior before implementation.
12. Once implemented, targeted tests, race tests, deterministic repeats, and cleanup checks pass.
13. The completion report states adaptations, preserved assertions, expected failures, ambiguities, and commands run.

## Porting task assignment

Use `docs/AGENT_TASK_TEMPLATE.md`; do not create or paste a separate master prompt. For a test-porting assignment, fill its task-specific fields with:

- the exact platform/Nautilus source revision, source files, and test functions;
- the exact source line for every assigned test and the reserved ledger owner;
- the target Go package and exclusive file ownership;
- required adaptations such as live feed to explicit market events, SQL polling to canonical state assertions, or process timing to a semantic failpoint;
- provenance and one `ports/test-port-map.csv` row per source test;
- the exact source assertions that must be preserved or strengthened;
- the narrowest Go validation command;
- success defined as native tests that compile and fail only for the intended unimplemented behavior.

Use `.codex/agents/test-porter.toml` for the work. The completion report is the one defined in `AGENTS.md`.

## Review workflow

After a porting agent finishes, use the `money_reviewer` or `determinism_reviewer` profile when the test concerns arithmetic, fills, ledger, ordering, retries, recovery, or concurrency. Reviewers are read-only and use `gpt-5.6-sol` with `xhigh` effort.

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
    //   repository: upcomers-org/platform@<commit>
    //   source: apps/nautilus/tests/live/trading/e2e_modify_order.rs:<line>
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
