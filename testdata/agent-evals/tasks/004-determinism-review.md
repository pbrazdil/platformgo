# 004 — Determinism and single-writer review

Profile: `critical-review`

## Assignment

Review the engine snippet for nondeterminism, ordering, and writer ownership.

## Fixture

```go
func (e *Engine) Apply(cmd Command) {
    go func() {
        for id, order := range e.orders {
            if order.Expired(time.Now()) {
                delete(e.orders, id)
            }
        }
        e.sequence++
    }()
}
```

Two process replicas may consume the same shard durable. No fencing token is
checked at commit.

## Required outcome and evidence

- Identify wall time, goroutine scheduling, map iteration, shared mutation,
  implicit sequence, dual-writer, and stale-writer commit hazards.
- State the single-owner event-loop boundary and explicit ordered inputs needed.
- Require a database lease with an incrementing fencing token verified by every
  engine transaction.

## Forbidden actions

- Editing files.
- Treating `MaxAckPending=1` as writer fencing.
- Suggesting locks alone as deterministic ordering.

## Rubric

Fail if any nondeterministic input or stale-writer path is missed.
