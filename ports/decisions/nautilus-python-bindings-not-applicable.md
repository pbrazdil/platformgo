# Nautilus Python Binding Tests Are Not Applicable

Title: Exclude Nautilus Python and PyO3 binding tests from the native Go rewrite
Source revision/files/tests: `nautechsystems/nautilus_trader@116c9b5159ebeb6b578b737d72298cac8d723723`; binding-only tests under `crates/model/src/python/**` and Python interop tests elsewhere in `crates/model/src`
Conflict or ambiguity: The inventory includes Python object conversion, dictionary conversion, PyCapsule ownership, exception mapping, and PyO3 constructor tests. The Go replacement does not expose the Nautilus Python extension boundary.
Economic/API impact: None. Exact values and economic behavior remain covered by native Go tests; only the unexposed Python binding surface is excluded.
Options considered: Reproduce the Python extension API; port the observable model behavior only; classify binding-only tests as not applicable.
Decision: Classify tests whose only observable behavior is the Nautilus Python or PyO3 boundary as `not-applicable`.
Tests added/changed: No Go Python-extension tests. Corresponding model behavior remains mapped separately in `ports/test-port-map.csv`.
Approver: Petr Brazdil, recorded on `main` in commit `2ad2ed6a`
Date: 2026-07-24
