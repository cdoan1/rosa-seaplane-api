# seaplane-authorizer

Lambda authorizer for the Phase 4 PoC custom-authorizer API Gateway.
Implements the design in
`docs/poc/preflight-ui-login/phase-4-ui-auth-path.md` and the decisions in
`docs/poc/preflight-ui-login/poc-decisions.md` (D-T1, D-T4, D-P4.4a,
D-P4-token).

## What it does

A REST-API REQUEST-type Lambda authorizer with two modes:

- **Flow 2a (token-only)** — `Authorization: Bearer <token>` with no
  `X-Seaplane-Arn` header: verify the token, allow. Never cached. The
  backend (`GET /api/v0/account_links`) re-verifies the token for the
  subject.
- **Flow 2b (token + selected ARN)** — verify the token, then confirm the
  selected ARN is linked to the token subject in the DynamoDB linking
  table. Decisions are cached in-process, keyed on the SHA-256 of
  `(token, arn)`, with TTL = min(`AUTHZ_CACHE_TTL`, token remaining
  validity) — a cached decision never outlives its token. Denies are
  cached too (bounded staleness for revoked links is documented in the
  phase 4 doc).

On success the authorizer context carries `sub`, `callerArn`, and
`accountId`; the gateway maps them to the same identity headers the
`AWS_IAM` gateway produces (see
`docs/poc/preflight-ui-login/identity-header-contract.md`).

## Shared code

Token verification and the link store are **not** reimplemented here: the
module depends on `seaplane-api/pkg/linking` (sibling module via a
`replace` directive), so the linking endpoint and the authorizer verify
tokens and read the same DynamoDB schema with one implementation.

## Configuration (environment variables, wired by terraform)

| Variable | Purpose |
|----------|---------|
| `LINKING_TABLE` | DynamoDB link table (required) |
| `RH_TOKEN_ISSUER` | expected token issuer (required) — the PoC Keycloak behind CloudFront |
| `RH_TOKEN_JWKS_URL` | issuer JWKS endpoint (required) |
| `RH_TOKEN_AUDIENCE` | expected audience (optional) |
| `AUTHZ_CACHE_TTL` | Flow 2b decision cache TTL in seconds (default 300) |

## Build and test

```sh
cd seaplane-authorizer
go test ./...
docker build -f Dockerfile -t seaplane-authorizer ..   # from repo root; context includes seaplane-api
```

The terraform that deploys this image lives in `rosa-seaplane`
(`terraform/modules/custom-authorizer-gateway`).

## PoC scope notes

- Fail-closed on link-store errors.
- In-process cache only (per warm Lambda instance) — no cross-instance
  coordination, which the phase 4 design accepts for the PoC.
- `X-Amz-Principal-Org-Id` is intentionally absent from the emitted
  context (D-T1).
