# Agent evaluation fixtures

Store versioned evaluation tasks and expected outcomes for `AGENT_EVALS.md` here. Every run uses the exact model `gpt-5.6-sol`; comparisons vary prompt/configuration, reasoning effort, reasoning context, standard/pro mode, verbosity, or tool routing—not model family.

Initial fixed corpus:

```text
tasks/
  001-exact-decimal-port.md
  002-idempotent-command.md
  003-migration-review.md
  004-determinism-review.md
  005-http-contract-port.md
  006-normative-conflict.md
  007-nats-ack-review.md
  008-realtime-gap-review.md
```

Each task contains its assignment, owned scope, fixed fixture, human-reviewed
expected outcome, forbidden actions, required evidence, success criteria, and
rubric. Fixtures contain no production secrets or customer data.

Reviewed comparison reports belong under `docs/agent-evals/`, not in this fixture directory.
