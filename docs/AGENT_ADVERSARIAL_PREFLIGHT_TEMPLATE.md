# Adversarial Preflight Template

Use this short pre-implementation preflight for work affecting money, ledger,
fills, margin, funding, balances, durable PostgreSQL state or migrations,
ordering, single-writer/concurrency, idempotency/duplicate delivery or
acknowledgment, restart/recovery/reconciliation/rollback, HTTP/gRPC/realtime
compatibility, authentication/authorization/ACL/security, or production
lifecycle/readiness/shutdown.

Low-risk docs-only, mechanical, and isolated test-only changes are exempt.
Scope mixed with a high-risk boundary is not exempt. Run applicable migration,
determinism, and money reviewers in parallel before production implementation.

```text
Goal and protected behavior:

Authority and transaction boundary:

Lock order and writer ownership:

Duplicate delivery and lost acknowledgment:

Restart and recovery:

Representative current-main upgrade:

Rollback and unknown commit:

ACL and Hostile default privileges:

Fail-closed conditions:

Tests that must fail first:

Stop conditions and owner decisions:

Reviewers and exact file ownership:
```

Each sequence names representative state/input and the observable failure or
safe outcome. Keep unknowns explicit. Stop rather than guess when money,
ordering, idempotency, compatibility, security, irreversible data, or a
normative conflict is unresolved.
