# Nautilus C FFI Tests Are Not Applicable

Title: Exclude Nautilus C FFI boundary tests from the native Go rewrite
Source revision/files/tests: `nautechsystems/nautilus_trader@116c9b5159ebeb6b578b737d72298cac8d723723`; tests under `crates/model/src/ffi/**`
Conflict or ambiguity: The inventory includes tests for C ABI ownership, C strings, borrowed buffers, and FFI-specific drop behavior. The Go replacement does not expose the Nautilus C ABI.
Economic/API impact: None. The underlying economic and model behavior remains covered by native Go tests; only the unexposed C binding boundary is excluded.
Options considered: Reproduce the C ABI; port the observable model behavior only; classify the binding-only tests as not applicable.
Decision: Classify tests whose only observable behavior is the Nautilus C FFI boundary as `not-applicable`.
Tests added/changed: No Go C ABI tests. Corresponding model behavior remains mapped separately in `ports/test-port-map.csv`.
Approver: Petr Brazdil, recorded on `main` in commit `2ad2ed6a`
Date: 2026-07-24
