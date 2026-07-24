# platformgo

Clean-room Go replacement for `upcomers-org/platform`, using pinned source tests as the executable specification. The intended production stack is Go, PostgreSQL, NATS with JetStream, Centrifugo, and Hyperliquid first.

## Current status

Last updated: 2026-07-24

Current delivery stage: **Phase 1 — pure-engine implementation complete locally; publication and evidence promotion pending**.

The pinned source inventory is complete: all 2,748 tests are recorded in
`ports/test-port-map.csv`. Five numeric tests are independently reviewed and
green against the production exact-decimal and typed-value boundary, 2,646
native Go representations remain `unreviewed/placeholder/spec-fixture`, and 97
implementation-only tests are reviewed and excluded with decision records.
Mechanical porting is not conflated with semantic acceptance or production
implementation.

The Phase 0 deterministic kernel supplies canonical IDs and logical time,
versioned input envelopes, strict shard-stream sequencing, recorded duplicate
results, fail-closed readiness, canonical decision/state hashes, and a
synchronous fixture with manual time and IDs. Its replay and duplicate
properties are policy-native evidence derived from the repository invariants;
they are not presented as reviewed ports of unrelated source tests.

The first reviewed Phase 1 cohort now exercises the production engine
for market and limit admission, exact cumulative fills, GTC resting, IOC/FOK
non-resting behavior, stop admission, amend, cancel, immutable economic
decisions, and committed business rejections. Six pinned source rows are
reviewed, green, and wired to this `model-real` boundary.

The next implementation candidate adds exact depth-weighted VWAP, deterministic
deepest-level pricing beyond displayed B-book depth, bid/ask execution,
admission-bound mark-price slippage limits, typed slippage rejection, and
partial IOC execution at the slippage boundary. Its five source rows remain
unreviewed until the separate evidence promotion is accepted.

The stacked positions/OMS candidate adds exact settlement-currency realized
PnL, netting fill taxonomy, account-isolated positions, hedging legs, targeted
leg closure, execution-time reduce-only clamping, and immutable position
decisions. Its source rows also remain unreviewed until matching lands and the
positions evidence is independently promoted.

The stacked risk candidate adds exact used/free margin and equity, reservation
for working orders, typed funding and margin denials, realized-PnL settlement,
business-key-idempotent funding, cross and isolated liquidation,
worst-notional-first stop-out order, and fail-closed stale-mark handling. Its
ten source rows remain unreviewed until the prerequisite slices land.

The final stacked Phase 1 candidate adds durable stop and favorable-touch
trigger latching, atomic bracket creation, held OTO activation, reduce-only
OCO cleanup, ladder resizing, hedging-position isolation, and cleanup after
external closes or reversals. It also closes the fill-fee invariant with
explicit maker/taker schedules, signed maker rebates, one-boundary half-even
rounding, fee-aware margin reservation, exact fill attribution, atomic balance
settlement, and duplicate-delivery protection. Its ten source rows remain
unreviewed until the implementation stack lands and its evidence is promoted
separately.

This repository is not yet a production-capable replacement. It has no executable `cmd` services, production PostgreSQL schema or adapter, NATS/JetStream adapter, Centrifugo adapter, or production Hyperliquid adapter. Current integration tests use deterministic in-memory fixtures and do not prove those runtime boundaries.

## Delivery progress

| Phase | Status | Evidence and remaining work |
|---|---|---|
| 0 — Policy and test harness | Complete | Machine-readable package scope, AST policy checks, split port/review/wiring evidence, exact function provenance, canonical source authorities, pinned Go 1.26.5, CODEOWNERS, immutable CI actions, complete-port and tidy gates, and the initial agent-evaluation corpus exist. `main` is protected and all seven required checks are enforced. The numeric foundation provides the sole `apd/v3`-backed production decimal, strict canonical parsing, explicit one-boundary rounding, immutable unit-bearing values, parser/arithmetic fuzzing, and five reviewed green source rows. The deterministic kernel adds explicit logical time and IDs, strict input sequencing, idempotent duplicate receipts, fail-closed typed errors, canonical decision/state hashes, replay properties, and the minimal synchronous engine fixture. |
| 1 — Pure engine | Implementation complete locally; publication and evidence pending | The production engine has reviewed `model-real` evidence for market/limit/stop admission, GTC/IOC/FOK behavior, exact cumulative fills, amend, cancel, typed business rejection, immutable decisions, and deterministic replay. Six pinned source rows are green at that boundary, reinforced by policy-native invariant/fuzz coverage. Stacked local candidates now cover deterministic depth/VWAP and slippage, netting and hedging positions, exact PnL, margin, funding, cross/isolated liquidation, durable stop/touch triggers, brackets and ladders, protection cleanup, and exact maker/taker fees. All required local validation passes. Phase 1 is not complete on `main` until the implementation stack lands and each source cohort is independently promoted from placeholder evidence. |
| 2 — Durable execution | Not started | PostgreSQL migrations and persistence, idempotency journal, transactional ledger/state, NATS/JetStream transport, outbox/inbox, and durable recovery are not implemented. |
| 3 — Compatibility edges | Not started | Production REST, gRPC, authentication, realtime/Centrifugo, health, CLI, and deployment-compatible services are not implemented. |
| 4 — Hyperliquid production integration | Not started | No production adapter, reconnect/resynchronization path, controlled live canary, soak test, or incident drill exists. |
| 5 — Replacement rehearsal | Not started | Data import, cutover, rollback, reconciliation, and audited go-live rehearsal remain. |

## Validation snapshot

Verified on 2026-07-24:

- `make policy`, `make port-map-complete`, `make fmt-check`, `make lint`,
  `make test`, `make test-race`, `make test-repeat`, and `make vuln` pass in
  the current working tree.
- `go mod tidy -diff` is clean.
- The strict lint profile covers production, infrastructure, non-economic, and
  tooling classifications. Ported compatibility and placeholder packages
  remain quarantined but still compile, test, vet, and pass AST safety checks.
- All 2,748 ledger rows pass structural validation and exact Go `FuncDecl`
  provenance binding.
- The numeric implementation and both boundary-policy prerequisites passed all
  seven required hosted checks before merge. The evidence-only promotion is
  covered by the policy and complete-inventory gates.
- The deterministic kernel has focused duplicate, replay, ordering, immutable
  state, canonical hash, manual-time, deterministic-ID, fuzz/property, repeated,
  and race-enabled coverage. It does not claim durable transaction or transport
  behavior.
- The first six Phase 1 order-lifecycle rows are reviewed and green against the
  production engine. Their focused source, invariant, fuzz seed, 100-repeat,
  and race-enabled tests pass locally.
- The complete stacked Phase 1 implementation passes focused 100-repeat tests,
  repository-wide tests, race tests, deterministic repeats, policy, formatting,
  strict lint, complete source inventory, tidy-diff, and vulnerability checks.
  Matching, positions, risk, bracket/trigger, and fee source rows remain
  deliberately unreviewed until their evidence-only promotions are accepted.

## Next milestone

Publish the stacked Phase 1 slices in order, then promote each source cohort in
separate evidence-only changes after local and hosted validation. Once those
changes are green on `main`, begin Phase 2 with PostgreSQL authority and
immutable migrations before NATS delivery work.

The authoritative scope, phase definitions, and completion criteria are in `PROJECT_CHARTER.md`. Repository-wide execution rules are in `AGENTS.md`.
