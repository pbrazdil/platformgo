Title: Preserve current Go broker account and user URNs

Source revision/files/tests:
- `upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd`
- `apps/app/tests/it/identity/e2e_broker.rs::scope_gate_403s_write_route_but_admits_probe`
- `shared/api/src/accounts.rs::AccountView`
- `crates/domain/src/ids.rs`
- `tests/integration/compatibility/broker_scope_gate_test.go::TestScopeGate403sWriteRouteButAdmitsProbe`

Conflict or ambiguity:
The pinned source deserializes the successful response as `AccountView`.
Its domain `AccountId` and `UserId` deserializers require base62 URN suffixes.
The current Go platform instead returns account IDs as
`urn:xb:account:<hyphenated UUID>` and accepts existing user URNs such as
`urn:xb:user:scope-gate-source`. The source client would reject those
identifier strings while decoding an otherwise valid 201 response.

Economic/API impact:
The decision preserves the identifiers already used by current Go commands,
PostgreSQL account authority, ownership, profiles, idempotency responses, and
HTTP responses. It changes no monetary value, ordering, transaction,
idempotency, or authorization behavior. A client compiled against the pinned
Rust domain identifier parser cannot decode the current Go identifier form
without adaptation, so the source test's typed `AccountView` compatibility is
an intentional external-contract deviation.

Options considered:
1. Rewrite current Go account and user identifiers to the pinned base62 form.
2. Mark the source row unresolved until all current Go identifiers can change.
3. Preserve current Go identifier authority, record the deviation, and accept
   the source test's scope behavior and current-Go response shape separately.

Decision:
Choose option 3 under the owner's standing instruction to preserve current Go
behavior as the source. The accepted behavior is limited to the observable
authorization sequence: an `accounts:read` credential may call ping but is
denied account creation, while a distinct wildcard credential creates one
committed account. The successful response is validated against the current Go
UUID account URN and current user URN, then matched field-for-field to the
PostgreSQL provisioning intent, ownership, and account profile. This decision
does not claim compatibility with the pinned Rust ID deserializers and does not
authorize any other identifier or wire deviation.

Tests added/changed:
- This decision-only layer changes no executable test. Before the source row
  can be accepted, `TestScopeGate403sWriteRouteButAdmitsProbe` must reject a
  successful account ID unless it has the current Go
  `urn:xb:account:<hyphenated UUID>` form.
- That follow-on test must retain the current Go user URN and prove that every
  returned profile field and timestamp matches the committed PostgreSQL intent
  and profile under the exact tenant and ownership.

Approver: Petr Brazdil, through the active owner instruction:
"Zachovej soucasne chovani jako zdroj."

Date: 2026-07-27
