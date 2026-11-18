# Identity Header Contract (API Gateway → Platform API)

**Status**: decided (D-T1, D-P4.4a in [poc-decisions.md](poc-decisions.md)) — authoritative for the Phase 4 custom-authorizer gateway and its integration tests.

This is the contract both API gateways must satisfy when forwarding
requests to the Platform API. The `AWS_IAM` gateway already implements it
(`rosa-seaplane/terraform/modules/api-gateway/integration.tf`); the
custom-authorizer gateway (Phase 4 P4.3) reproduces it from
`context.authorizer.*` so the backend never special-cases UI vs CLI
callers.

## Headers

| Header | `AWS_IAM` source | Custom-authorizer source | Backend consumer | Required |
|--------|------------------|--------------------------|------------------|----------|
| `X-Amz-Caller-Arn` | `context.identity.userArn` | authorizer-resolved linked ARN | `middleware.Identity` → `ContextKeyCallerARN`, `RequireIdentity`, Cedar principal | yes (resource routes) |
| `X-Amz-Account-Id` | `context.identity.accountId` | account ID parsed from the linked ARN | `middleware.Identity` → `ContextKeyAccountID`, `RequireIdentity`, namespace derivation | yes (resource routes) |
| `X-Amz-User-Id` | `context.identity.user` | authorizer context (e.g. token `sub`) | `middleware.Identity` → `ContextKeyUserID` | no |
| `X-Amz-Source-Ip` | `context.identity.sourceIp` | `context.identity.sourceIp` (native, still present) | `middleware.Identity` → `ContextKeySourceIP` | no |
| `X-Amz-Request-Id` | `$context.requestId` (verify in P4.3) | `$context.requestId` | `middleware.Identity` → `ContextKeyRequestID` | no |
| `X-Amz-Principal-Org-Id` | `context.identity.principalOrgId` | **intentionally absent** | none (no consumer exists) | no |

Decisions recorded in `poc-decisions.md` (D-T1):

- **`X-Amz-Principal-Org-Id` is left absent on the custom path.** No
  backend code consumes it, and a derived value would add nothing. The
  `AWS_IAM` gateway keeps its native mapping.
- **`X-Amz-Request-Id` maps from `$context.requestId`** on both gateways;
  P4.3 verifies the `AWS_IAM` gateway's actual source matches.
- **Token transport is `Authorization: Bearer <token>`** on the custom
  gateway; the Lambda authorizer reads it as its identity source. The
  authorizer is stateless.

## Route classes and identity requirements

| Route class | Gateway | Identity requirement |
|-------------|---------|----------------------|
| Resource routes (`/api/v0/clusters`, …) | either | Full identity headers (Caller-Arn + Account-Id) — enforced by `RequireIdentity`; custom-gateway requests satisfy it via the authorizer's resolved ARN |
| Flow 2a — list linked ARNs (`GET /api/v0/account_links`) | custom (or `AWS_IAM`) | **Token-only**: no ARN yet; the route is registered outside the `RequireIdentity` subrouters (same placement pattern as `/live`, `/ready`, `/info`). The handler verifies the bearer token itself to derive the subject (D-P4.4a) |
| Link management (`POST`/`DELETE /api/v0/account_links`) | `AWS_IAM` | SigV4 identity headers **plus** an `X-Rh-Token` header (the SigV4 signature occupies `Authorization`, so the RH token rides a dedicated header); the handler verifies the token and requires the link ARN's account to match the SigV4 account |

## Authorizer output shape (custom gateway)

The Lambda authorizer returns, in `context.authorizer`:

- mapping mode (Flow 2b): `callerArn` (the linked ARN), `accountId`
  (parsed), `sub` (token subject) — the gateway maps these to the headers
  above;
- token-only mode (Flow 2a): `sub` only — allow decision; the backend
  re-verifies the token for the subject (PoC simplification; production
  may trust a forwarded subject header instead).

## Parity test (P4.6)

An integration test exercises both gateways with the same logical caller
and asserts identical header names and resolvable values for every row in
the table above (absent-by-design rows asserted absent).
