# Domain package instructions

This directory contains pure economic types, rules, and invariants.

- No PostgreSQL, NATS, Centrifugo, HTTP, environment access, logging control flow, wall time, sleeps, randomness, goroutines, or global mutable state.
- No `float32` or `float64` for economic values.
- Inputs and outputs use typed exact values; every rounding operation names its context and rule.
- Functions must be deterministic and side-effect free unless mutation is confined to an explicitly owned aggregate passed by the engine.
- Invalid states and business rejections are typed errors, never panics or fallback defaults.
- Map-backed data is sorted before any decision, serialization, or hash.
- Add or port the failing native Go test before production logic.
- Any change to an economic rule requires tests for boundary values, duplicate application, and canonical serialization.

Read `INVARIANTS.md` and `DECIMAL.md` for every change here.
