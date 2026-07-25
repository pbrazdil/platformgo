# Dependency review: `github.com/golang-jwt/jwt/v5`

Version: `v5.3.1`

Problem not solved adequately by standard library/current dependencies:
The client and Centrifugo compatibility boundaries require JWT signing and
verification. `SECURITY.md` explicitly forbids implementing JWT signatures
ad hoc, and the Go standard library does not provide a JWT implementation.

API surface used:

- `jwt.NewWithClaims` and `Token.SignedString` for HS256 signing;
- `jwt.ParseWithClaims` with explicit algorithm, audience, expiration, and
  issued-at validation options;
- `jwt.RegisteredClaims`, `jwt.ClaimStrings`, and `jwt.NumericDate`.

Maintenance/security posture:
The module is the actively maintained continuation of the established
`dgrijalva/jwt-go` project. Version 5 exposes parser options that pin accepted
algorithms and required registered claims. Callers retain responsibility for
claim-schema validation, secret management, token-type separation, and
rejecting ambiguous JSON. CI runs `govulncheck`, and the dependency is pinned
to an exact tagged release.

License:
MIT.

Transitive dependency impact:
No runtime transitive modules are added by `v5.3.1`; it uses the Go standard
library.

Determinism impact:
None in the deterministic engine. JWT issuance and verification remain in
edge adapters and use explicit claim values and injected clocks.

Money-path impact:
No arithmetic or monetary state is handled by the module. It protects the
authentication boundary that authorizes commands before they reach the
economic engine.

Removal strategy:
Replace the module only through a versioned authentication-contract change
with golden-token interoperability tests for both the HTTP/gRPC edge and the
configured Centrifugo server.
