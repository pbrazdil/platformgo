# Pinned Source Revisions

The source tests are read from immutable revisions. Moving branches are forbidden.

```text
PLATFORM_SOURCE_REPOSITORY=upcomers-org/platform
PLATFORM_SOURCE_COMMIT=50141367492be46ebf5623f6191a14b94af2f2bd
NAUTILUS_SOURCE_REPOSITORY=nautechsystems/nautilus_trader
NAUTILUS_SOURCE_REVISION=116c9b5159ebeb6b578b737d72298cac8d723723
```

The complete static inventory scope at those revisions is:

```text
PLATFORM_SOURCE_ROOTS=apps/nautilus/tests,apps/app/tests
PLATFORM_SOURCE_TEST_COUNT=271
NAUTILUS_SOURCE_ROOTS=crates/model/src
NAUTILUS_SOURCE_TEST_COUNT=2477
```

Inventory every Rust test function under those roots, including ignored, broken, live, FFI, and language-binding tests. Those tests may ultimately be `not-applicable`, but they may not be omitted. The expected total is 2,748 source-test rows.

Changing either revision requires:

1. an ADR or explicit source-revision decision;
2. a diff/inventory of added, removed and changed source tests;
3. updates to the source roots and expected counts above;
4. updates to `ports/test-port-map.csv`;
5. review of compatibility impact;
6. owner approval.

The old binaries and test suites are not executed. These revisions provide source code and provenance only.
