# Seaplane Console (Phase 4 PoC)

TypeScript web console for the Seaplane Platform API, authenticating
through the **custom-authorizer API Gateway** (Phase 4 PoC,
`docs/poc/preflight-ui-login/phase-4-ui-auth-path.md`). Adapted from the
day-o reference repo (`seaplane-console-poc`): the React UI and the
OpenAPI-derived contracts are reused; the **BFF is dropped** — the browser
calls the gateway directly.

## Architecture

```
browser ── Authorization: Bearer <token> (+ X-Seaplane-Arn) ──>
   custom-authorizer API Gateway ──> Lambda authorizer
     (verify token; confirm ARN is linked in DynamoDB) ──>
   platform-api (same identity headers as the AWS_IAM gateway) ──> Cedar
```

- **`apps/web`** — the React console. OIDC Authorization Code + **PKCE**
  public client (`src/auth.ts`) against the PoC Keycloak behind CloudFront;
  the access token goes to the gateway as a bearer header. No SigV4, no
  STS, no AWS SDK in the browser.
- **`packages/platform-api-contracts`** — hand-transcribed TypeScript types
  from the Platform API's public OpenAPI spec (unchanged from day-o;
  follow-up: regenerate via `openapi-typescript` from `make
  generate-openapi` output).

## Flow

1. Sign in (PKCE redirect to Keycloak).
2. **Flow 2a**: `GET /api/v0/account_links` (token-only) populates the ARN
   selector in the topbar; the selection persists in localStorage and rides
   the `X-Seaplane-Arn` header on every resource call (**Flow 2b**).
3. Resource calls use the same paths/shapes as the CLI — the gateway maps
   the authorizer's resolution onto the `AWS_IAM` gateway's identity
   headers.

## Build-time configuration (Vite env)

| Variable | Value |
|----------|-------|
| `VITE_API_BASE_URL` | custom-authorizer gateway invoke URL |
| `VITE_OIDC_ISSUER` | Keycloak realm issuer (`https://<cloudfront-domain>/realms/poc`) |
| `VITE_OIDC_CLIENT_ID` | public (PKCE) client ID — no client secret in the browser |

## Develop

```sh
cd console
npm install
npm run typecheck
VITE_API_BASE_URL=... VITE_OIDC_ISSUER=... VITE_OIDC_CLIENT_ID=... npm run dev
```

The gateway serves CORS for browser origins (OPTIONS mock with the
`Authorization`, `X-Seaplane-Arn` headers allow-listed).

## PoC scope

- Tokens in `sessionStorage` (per-tab); short token lifetimes expected —
  the authorizer's decision cache is bounded by them.
- The Keycloak realm must define the console client as **public with PKCE
  required** (no secret), standard OIDC claims only.
- The day-o repo's Cedar grant model translates 1:1: its per-user IAM-role
  grants become grants attached to the ARNs resolved by the authorizer.
