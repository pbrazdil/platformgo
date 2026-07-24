# Source-Test Porting Instructions

These rules apply under `ports/` and to tasks that port pinned Rust or Nautilus tests.

- Read `TESTING.md`, `docs/TEST_PORTING_PLAYBOOK.md`, and `ports/SOURCE_REVISIONS.md`.
- Do not execute the Rust platform, Nautilus, their tests, containers, or services.
- Explicit source-test assertions are normative subject to `INVARIANTS.md` and recorded owner decisions.
- Port tests into native deterministic Go; do not reproduce Rust/Nautilus internals without an observable requirement.
- Reserve and update one owned `ports/test-port-map.csv` row per source test.
- Preserve exact source revision, path, line, function, assertions, and documented adaptations.
- Replace live economic feeds, sleeps, random IDs, wall time, and global state with deterministic fixtures.
- Do not resolve conflicting source tests silently. Mark `conflict`, record a decision note, and stop for an owner decision.
- Do not weaken assertions or hide missing behavior with skips, build tags, tolerances, or TODOs.
