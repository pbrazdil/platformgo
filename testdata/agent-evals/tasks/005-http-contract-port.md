# 005 — HTTP compatibility source-test port

Profile: `implementation`

## Assignment

Port the supplied HTTP behavior into a native Go contract test. Own only the
contract test file and assigned ledger row.

## Fixture

The source test sends `POST /v1/orders` with idempotency key `same-key`,
quantity `"0.0100"`, and omitted `stopLoss`. It asserts status `201`, response
quantity `"0.0100"`, JSON field `stopLoss: null`, and command ID `cmd-7`. The
same request returns byte-equivalent status/body. Reusing the key with quantity
`"0.0200"` returns status `409` and code `IDEMPOTENCY_KEY_REUSED`.

## Required outcome and evidence

- An `httptest` contract test preserving path, status, decimal scale, omitted
  versus null behavior, stable stored response, and conflict error code.
- Exact function-attached source provenance and correct ledger port state.
- Targeted Go command and result.

## Forbidden actions

- JSON numeric economic fields.
- Re-rendering a retry response from mutable current state.
- Asserting internal database schema.
- Running the source service.

## Rubric

Every listed wire assertion is mandatory; semantic approximations fail.
