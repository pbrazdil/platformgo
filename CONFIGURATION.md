# Configuration and Runtime Settings

## 1. Configuration boundary

Environment variables and files are read only during process bootstrap in the configuration adapter. Domain and engine packages receive typed immutable configuration values.

`os.Getenv` and `os.LookupEnv` are forbidden outside the configuration/bootstrap package.

## 2. Typed configuration

- Every key has one typed definition, documentation, validation and sensitivity classification.
- Unknown/deprecated keys are reported according to compatibility policy.
- Durations are parsed once into `time.Duration`.
- Decimal/risk values use exact decimal parsing.
- Secrets are wrapped/redacted and cannot be formatted accidentally.
- Production has no silently permissive default for security, connectivity, stream retention or money policy.

## 3. Environment modes

At minimum:

```text
local
test
staging
production
```

Production and staging fail closed when:

- required secrets or endpoints are absent;
- development/default credentials are used;
- engine shard identity is ambiguous;
- JetStream stream policy is unsafe;
- database schema is incompatible;
- CORS/realtime origins are not explicitly configured;
- engine input retention/capacity is not configured;
- market freshness/risk parameters are invalid.

## 4. Runtime economic settings

Money/risk settings changed at runtime are not process globals.

Flow:

1. authorized operator writes a new versioned setting in PostgreSQL;
2. the transaction records audit and an outbox event;
3. a versioned config input enters the engine shard stream;
4. the engine applies it in stream order;
5. decisions record the applied configuration version.

A restart loads the durable effective version before accepting inputs.

## 5. Compatibility keys

Existing environment names required for drop-in deployment are retained or explicitly aliased at the bootstrap edge. Internal package names do not leak into the external configuration contract.

Freeze the environment key list in the compatibility manifest.

### Phase 3 user API-key authority

The effective API-key policy is a versioned singleton in PostgreSQL, not a
process-local environment value. Migration `20260726000700` installs the pinned
defaults: `25` active keys per owner, `600` authenticated client requests per
`60` seconds, and a `24` hour idempotency replay window. The source deployment
names `UZO_AUTH_MAX_API_KEYS_PER_OWNER`,
`UZO_API_RATE_LIMIT_MAX_REQUESTS`, `UZO_API_RATE_LIMIT_WINDOW_SECS`, and
`UZO_API_IDEMPOTENCY_TTL_SECS` remain cutover inputs. When any is present, the
API compares it with the durable singleton before serving and on every
readiness probe. A mismatch fails closed until a separately reviewed policy
operation reconciles PostgreSQL; an API replica never applies a local override.
The durable schema preserves the source domains: signed 64-bit owner cap,
unsigned 32-bit rate maximum, and unsigned 64-bit rate-window/replay-TTL
seconds. Zero and negative source values retain their fail-closed effects
instead of being rejected during cutover. Replay TTLs beyond PostgreSQL's
finite timestamp horizon are stored as `infinity`, which is observationally
equivalent for the database lifetime. Every successful audit fact records the
durable policy version and effective owner cap. Policy changes
require a separately reviewed, audited PostgreSQL operation; mixed API replicas
cannot submit different limits.

`POST /v1/me/api-keys` requires a nonempty client-stable
`Idempotency-Key`. This is an owner-approved safety deviation from the pinned
source's optional middleware contract: a shown-once credential is never made
active unless its exact encrypted response can be recovered after an unknown
HTTP outcome.
The owner-approved ordering deviation resolves existing same-request replay or
changed-request conflict before new-work rate rejection. Invalid new requests
consume the shared rate bucket exactly once. The raw external idempotency key
is reduced to a fixed SHA-256 digest before durable lookup, indexing, or replay
authentication and is never stored.

`UZO_AUTH_API_KEY_REPLAY_KEYS` is secret JSON containing an AES-256-GCM
keyring. `UZO_AUTH_API_KEY_REPLAY_ACTIVE_KEY_ID` explicitly selects the key that
encrypts new idempotency responses:

```json
[{"id":"2026-07-primary","key":"<base64-encoded 32 bytes>"}]
```

Rotation is two-stage. First distribute the future key to every serving replica
while the old active ID remains unchanged, and verify that every replica can
decrypt both keys. Only then change the active ID across the fleet. Retain the
old key for at least the durable replay TTL and until bounded cleanup reports no
live response encrypted under it. Startup and every readiness check compare the
configured keyring with `identity.api_key_replay_coverage()` and fail closed
when any live replay names an unavailable key. Startup and every cleanup cycle
emit `key_id`, `live_count`, and `oldest_expires_at`; a configured old key is
safe to remove only after its live count is repeatedly zero across the fleet.
Plaintext API-key tokens and raw idempotency keys are never stored in the key
table, audit trail, or replay table.

`platformgo_api` is the trusted authenticated-principal authority for this
slice. PostgreSQL independently enforces the durable policy, transaction, and
audit shape, but the shared API role is intentionally authorized to attest the
user owner passed by the authenticated application. Compromise of that database
credential is therefore a credential-minting incident and requires immediate
role rotation and reconciliation; it is not claimed as contained by table
revocations alone.

## 6. Reloading

- Secret rotation and economic configuration use separate mechanisms.
- Partial reload is rejected; a configuration revision is applied atomically.
- Invalid runtime settings have no effect and produce an audited rejection.
- File watchers and signal handlers may request reload but never mutate engine state directly.

## 7. Required tests

- all required production keys;
- rejection of development secrets in production;
- exact decimal/duration parsing;
- compatibility aliases;
- redaction;
- runtime config version order and replay;
- duplicate setting event idempotency;
- restart with effective settings;
- invalid revision/gap fail-closed behavior.
