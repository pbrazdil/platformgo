# Agent evaluation fixtures

Store versioned evaluation tasks and expected outcomes for `AGENT_EVALS.md` here. Every run uses the exact model `gpt-5.6-sol`; comparisons vary prompt/configuration, reasoning effort, reasoning context, standard/pro mode, verbosity, or tool routing—not model family.

Suggested layout:

```text
tasks/
  001-exact-decimal-port/
  002-idempotent-command/
  003-migration-review/
  004-determinism-review/
  005-api-contract-port/
  006-normative-conflict/
  007-nats-ack-review/
  008-realtime-gap-review/
```

Each task contains its assignment, owned scope, fixture files, human-reviewed expected outcome, forbidden actions, required evidence, success criteria, and rubric. Fixtures contain no production secrets or customer data.

Reviewed comparison reports belong under `docs/agent-evals/`, not in this fixture directory.
