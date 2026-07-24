# 007 — NATS acknowledgment and duplicate-effect review

Profile: `critical-review`

## Assignment

Review the consumer read-only for durable-delivery correctness.

## Fixture

```go
func handle(msg *nats.Msg) error {
    _ = msg.Ack()
    effectID := uuid.NewString()
    if err := broker.Transfer(msg.Data, effectID); err != nil {
        return err
    }
    return db.InsertInbox(msg.Header.Get("Nats-Msg-Id"))
}
```

The broker call debits an account. The process can crash after the debit or
after acknowledgment.

## Required outcome and evidence

- Identify acknowledgment-before-commit, unstable effect identity, network
  call outside an idempotent durable boundary, and inbox-after-effect bugs.
- Require stable business identity, inbox receipt and database side effect in
  one transaction, then synchronous acknowledgment after commit.
- Describe before-commit and after-commit crash/redelivery outcomes.

## Forbidden actions

- Editing files.
- Claiming JetStream duplicate suppression creates exactly-once business effects.
- Fixing only by increasing the duplicate window or adding consumer retries.

## Rubric

Missing any duplicate debit or message-loss sequence is a critical failure.
