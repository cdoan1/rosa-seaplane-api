# Phase 4 — UI Authentication Path (Custom-Authorizer Gateway)

**Status**: planned — custom-authorizer architecture is the decided design
**Depends on**: Phase 0 T4 (account-linking design), T1 (identity-header contract)
**Blocks**: Phase 5
**Duration estimate**: large — the largest new operational and security
surface of the PoC

## Goal

Give the UI an RH-Token-based path into the Platform API: a second,
public API Gateway with a **custom (Lambda) authorizer** that verifies the
Red Hat token, confirms the selected ARN is linked to that Red Hat
identity in the DynamoDB account-linking store, and forwards the request
with the **same identity header names** the `AWS_IAM` gateway produces —
so the backend and Cedar treat UI callers exactly like ARN-authenticated
CLI callers.

## Architecture

```
UI ──(RH Token [+ selected ARN])──> CUSTOM API Gateway (public)
                                       │ Lambda authorizer
                                       │  Flow 2a (token only):  validate token, allow
                                       │  Flow 2b (token + ARN): validate token +
                                       │    confirm ARN is linked (DynamoDB)
                                       │  cache 2b on (token, ARN), TTL ≤ 5 min
                                       │  and ≤ token remaining validity
                                       ▼
                            VPC Link v2 → ALB → platform-api pod
                              identity headers from context.authorizer.*,
                              same names as the AWS_IAM gateway
```

- **Flow 2a — list linkable ARNs** (token only): the authorizer validates
  the token and allows; the backend integration queries the linking store
  and returns the ARNs the user may operate with, to populate the UI's ARN
  selector. Never cached.
- **Flow 2b — operate with a selected ARN**: every other UI request carries
  one selected ARN. The authorizer verifies the token **and** confirms the
  selected ARN is linked to that Red Hat identity. Never trust a
  client-supplied ARN without the mapping check; reject any ARN not
  currently linked (a revoked link stops being honored within the cache
  TTL — a documented, bounded staleness window).
- **Linking** (prerequisite, owned by the Platform API authz design): a
  customer links an AWS identity by calling the linking endpoint through
  the `AWS_IAM` gateway — SigV4 proves control of the ARN, the same
  request carries an RH token verified synchronously before the record is
  written. ARN-only never creates a link.

### PoC token issuer: Keycloak on the RC, behind CloudFront

The "RH token" is a Keycloak-issued JWT (Red Hat SSO is Keycloak). For the
PoC we deploy our own **Keycloak instance on the regional cluster (RC),
served behind a CloudFront endpoint**. Rules that keep this
production-swappable:

- issuer, JWKS URL, and expected audience are **configuration, never
  hardcoded** — swapping to real Red Hat SSO later is a config change only;
- only standard OIDC claims (`sub`, `exp`, `iss`, `aud`) are used — no
  Keycloak-specific claims logic, so linking records (keyed on `sub`) and
  all downstream behavior are issuer-agnostic;
- the CloudFront hostname becomes the token issuer URL — changing the
  issuer later means re-minting links, acceptable for a PoC, so record it
  in the decision log.

This is issuer substitution only — not the discarded BFF pattern: there
is no STS federation, no SigV4 custody, no BFF service. The Lambda
  authorizer still resolves token → linked ARN → the same identity headers
  → Cedar.

### Console PoC scope

Phase 4 is demoable with **existing** API endpoints — the authorizer is
  not discovery-specific. A minimal console (or static page) can:
login → Flow 2a (ARN selector) → list/open clusters, node pools,
OidcConfigs, DNS reservations → view conditions. The cluster-creation
form (VPC/subnet pickers) is the Phase 5 follow-on, not a Phase 4
dependency.

### Security posture

- The custom gateway is a **public endpoint whose authentication is
  entirely Lambda code** — unlike `AWS_IAM`, which rejects unsigned
  requests before any custom code runs. The authorizer is security-critical:
  least-privilege Lambda role, hardened, tested, monitored.
- Authorizer caching is **application-level** (inside the Lambda), not API
  Gateway's authorizer-result cache — no gateway cache-key or
  identity-source coupling.
- `X-Amz-Principal-Org-Id`: no native source for an ARN resolved from the
  linking store; the authorizer populates it explicitly from the linking
  record or leaves it absent (Phase 0 T1.2 decision).

## Code review findings (what exists today)

- **F1 — Account linking is un-designed.** `docs/authz/design.md`
  ("Deferred identity and client decisions") explicitly removed
  AWS-account-to-Red Hat-org and principal-to-user linking from current
  delivery, requiring a future design to decide organization ownership,
  export-control/ban enforcement, and effects on existing resources.
  Flows 2a/2b assume that store exists → **P4.1 is the longest lead**.
- **F2 — Cedar stays ARN-based; no authz schema changes needed.** The
  runtime model (`docs/authz/README.md`, `seaplane-api/pkg/authz/`)
  attaches policies to AWS principal ARNs and matches assumed-role
  sessions via Cedar `principal in`. Because the authorizer emits ARN
  identity headers, `middleware/identity.go` (`RequireIdentity`,
  `authz.Identity.Validate()`) applies to authorizer-minted identities
  as-is.
- **F3 — Header contract is verified and small.** The backend consumes
  five headers (`X-Amz-Caller-Arn`, `X-Amz-Account-Id`, `X-Amz-User-Id`,
  `X-Amz-Source-Ip`, `X-Amz-Request-Id`); the `AWS_IAM` gateway mapping
  lives in `rosa-seaplane/terraform/modules/api-gateway/integration.tf`
  (`context.identity.*` → headers). The custom path reproduces it from
  `context.authorizer.*` — against the T1.3 contract doc.
- **F4 — Net-new terraform.** No Lambda/custom-authorizer precedent exists
  in `rosa-seaplane/terraform` (`rhobs-api-gateway` is observability;
  `sre-ui-alb` is the console ALB). The gateway module, Lambda, and IAM
  wiring are new; the `AWS_IAM` module's VPC Link v2 → ALB integration
  shape is the pattern to copy.
- **F5 — Flow 2a vs `RequireIdentity`.** `RequireIdentity` rejects
  requests without account-ID + caller-ARN headers. Flow 2a requests have
  no ARN yet — decide whether 2a is exempted by path or the authorizer
  emits a Red Hat-identity header the middleware understands.

## Actionable tasks

### P4.0 — Keycloak deployment on the RC, behind CloudFront (new infrastructure)

Deploy and configure the PoC Keycloak. Involves both `rosa-seaplane`
(terraform) and the RC workload (deployment manifests).

- [ ] P4.0.1 Keycloak workload on the RC: deployment + service + ingress
      (namespace, resource limits, persistent volume for the H2/dev DB or
      an external Postgres if the PoC needs durability across restarts —
      prefer Postgres, `seaplane-db`'s instance or a small RDS, since token
      signing keys and realm config should survive pod restarts).
- [ ] P4.0.2 Realm/client configuration for the PoC: one realm, one public
      or confidential client for the console PoC page, a test user (or a
      test IdP federation if you want realistic logins), token lifetimes
      tuned short enough to exercise the authorizer's
      TTL = min(5 min, remaining validity) logic.
- [ ] P4.0.3 **CloudFront distribution** in `rosa-seaplane` terraform:
      HTTPS listener with ACM cert on a service-owned hostname, origin =
      the RC ingress/ALB for the Keycloak service, cache behavior that
      **never caches** token/JWKS/admin paths (`Authorization`, `*.well-known/*`,
      `/realms/*` → no-cache), origin request forwarding of headers needed
      by Keycloak (`Host`/forwarded headers configured so redirects and
      issuer URL render as the CloudFront hostname).
- [ ] P4.0.4 Make the CloudFront hostname the Keycloak `hostname`/issuer
      (front-end URL) so tokens carry the public issuer URL, and the
      authorizer's JWKS fetch points at the same.
- [ ] P4.0.5 Secrets: Keycloak admin credentials, client secrets, and
      signing keys managed via the existing secrets pipeline; never in
      terraform state in plaintext.
- [ ] P4.0.6 Smoke test: OIDC discovery (`/.well-known/openid-configuration`)
      and token issuance reachable through CloudFront only; direct RC
      ingress access blocked or at least not the documented path.
- [ ] Record the issuer URL in `poc-decisions.md` (D-P4-token) — changing
      it later re-mints links.

### P4.C — Console UI (TypeScript)

Build the console PoC UI in TypeScript, using
`gitlab.cee.redhat.com/cdoan/day-o-learning-111326` ("day-o" repo,
`seaplane-console-poc`) as the reference base. That repo already contains
a working TypeScript console PoC; the split for Phase 4 is:

**Reuse as-is (translates directly):**

- `apps/console-web` — the React UI (cluster/NodePool views) and styling;
- `packages/platform-api-contracts` — the hand-transcribed TypeScript
  types derived from the public OpenAPI spec (its own phase-3 plan to
  regenerate via `openapi-typescript` still applies);
- `deploy/keycloak` — the Keycloak-on-Kubernetes manifests with Postgres,
  realm setup (`setup-keycloak-realm.sh`), and user onboarding
  (`onboard-console-user.sh`) — the concrete implementation of P4.0.1/
  P4.0.2 on the RC;
- `deploy/console` — web/BFF deployment manifests, nginx config, security
  headers — minus the BFF workload;
- **The Authz / Cedar model translates over**: the day-o repo's plan-2
  flow (multiple users, each with an IAM role, Cedar grants attached to
  role ARNs) maps 1:1 onto Phase 4's linking model — where day-o federates
  a Keycloak user to *their own* IAM role via STS, Phase 4 links the
  identity to an ARN in DynamoDB and the authorizer emits that ARN. The
  same Cedar policies/grants attached to those ARN principals work
  unchanged, because Cedar still sees an ARN principal either way.

**Replace:**

- `apps/console-bff` (Fastify, OIDC + STS + SigV4 server-side) is
  **dropped** — that is the discarded BFF pattern. The web app calls the
  custom-authorizer gateway directly: bearer token (from the P4.0
  Keycloak/CloudFront issuer) + selected-ARN header, no SigV4, no STS,
  no AWS SDK in the browser.

Tasks:

- [ ] P4.C.1 Fork/import the day-o repo structure into the console PoC
      repo: `apps/console-web` + `packages/platform-api-contracts`; delete
      `apps/console-bff` and its SigV4/STS machinery (`@smithy/signature-v4`,
      `fromWebToken`).
- [ ] P4.C.2 New thin API client module replacing the BFF's server-side
      calls: base URL = the P4.3 custom gateway, auth = `Authorization:
      Bearer <token>` from the P4.0 Keycloak, plus the selected-ARN header;
      token refresh handled in the browser session. Flow 2a (list linked
      ARNs) populates the account selector; all resource calls carry the
      selected ARN.
- [ ] P4.C.3 Adapt the day-o `deploy/console` manifests: web workload
      only, pointed at the custom gateway URL (config, not hardcoded);
      keep the nginx/security-headers config.
- [ ] P4.C.4 Port the day-o Keycloak deploy + realm/onboarding scripts to
      the RC (fulfills P4.0.1/P4.0.2); add the CloudFront issuer wiring per
      P4.0.3/P4.0.4.
- [ ] P4.C.5 Translate the day-o plan-2 Cedar setup: the per-user IAM-role
      grants become grants attached to the linked ARNs (same policies,
      attached to the ARNs the authorizer resolves).
- [ ] P4.C.6 End-to-end console demo: login (Keycloak via CloudFront) →
      Flow 2a selector → list/open clusters and NodePools through the
      custom gateway, with Cedar authorizing the linked-ARN identity —
      verified to behave identically to a CLI caller on the same ARN.

### P4.1 — Account-linking design and store (longest lead — start first)
- [ ] **PoC schema decided (D-T4)**: single DynamoDB table, PK = token
      `sub`, one item per linked ARN (sort key = ARN, attributes `arn`,
      `accountId`, `linkedAt`, `revokedAt`); conditional writes on link;
      tombstone-or-delete revocation honored within the authorizer cache
      TTL. Org ownership / export-control / link limits are explicitly out
      of PoC scope — reconcile with the real authz design (T4) before
      production.
- [ ] Linking endpoint via the `AWS_IAM` gateway: SigV4-proven ARN +
      synchronously verified token writes the record. **PoC: the token may
      come from a standalone Keycloak behind a configurable issuer/JWKS/
      audience (see the PoC simplification above).**
- [ ] Define revocation propagation: the authorizer cache TTL bounds how
      fast a revoked link stops being honored; document the window.
- [ ] CORS configuration for browser origins (preflight `OPTIONS`, allowed
      headers incl. the token/ARN headers) — without it the console PoC
      fails on the first preflight request.
- [x] Token transport — **decided (D-T1)**: `Authorization: Bearer` header,
      stateless authorizer.

### P4.2 — Lambda authorizer
- [ ] Token-only mode (Flow 2a): validate RH token, allow, no mapping,
      never cached.
- [ ] Mapping mode (Flow 2b): validate token + confirm the selected ARN is
      linked; reject unlinked ARNs.
- [ ] Application-level cache keyed on `(RH token, ARN)` with
      TTL = min(~5 min, token remaining validity) — a cached decision never
      outlives its token.
- [ ] Emit the resolved identity via `context.authorizer.*` per the T1.3
      contract.

### P4.3 — Custom API Gateway (net-new terraform, F4)
- [ ] New REST API module in `rosa-seaplane/terraform`: `CUSTOM`
      authorization, public endpoint, Lambda authorizer attachment,
      VPC Link v2 → the platform ALB (copy the `api-gateway` module's
      integration shape).
- [ ] Identity headers sourced from `context.authorizer.*`, same names as
      the `AWS_IAM` gateway; `X-Amz-Principal-Org-Id` and
      `X-Amz-Request-Id` per the T1 decisions.
- [ ] CORS enablement (see P4.1) — native API Gateway config.
- [ ] Least-privilege IAM: Lambda role limited to linking-table read and
      log delivery.

### P4.4 — Flow 2a endpoint (list linked ARNs)
- [ ] Platform API endpoint returning the ARNs linked to the caller's Red
      Hat identity (backend integration, not the authorizer).
- [ ] Resolve the F5 `RequireIdentity` question for token-only requests —
      **decided (D-P4.4a): path exemption** in `RequireIdentity`'s switch
      (patterned on the existing `/live|ready|info` exemption); the 2a
      handler derives identity from the authorizer's context output.
- [ ] Cedar policy for the new action.

### P4.5 — Security hardening
- [ ] Authorizer code review with security focus; alerting on deny spikes
      and anomalies; least-privilege verification.
- [ ] Threat-model doc: public endpoint, bounded-staleness window, token
      verification failure modes, cache-key integrity.

### P4.6 — Tests
- [ ] Authorizer unit tests: valid/invalid token, linked/unlinked ARN,
      revoked link within/after the cache window, token expiring inside
      the TTL.
- [ ] Header-parity integration test against the T1.3 contract: both
      gateways produce the same header names with resolvable values.
- [ ] Cedar evaluation on the UI path: account registration +
      `RequireIdentity` + a resource action, all with authorizer-minted
      identities.

### P4.7 — Documentation alignment
- [ ] Ensure `docs/preflight-and-ui-login-flows.md` remains the
      authoritative UI-auth design; fold in any detail shifted during
      implementation.

## Repos, branches, and workflow

All Phase 4 code changes go on the **`poc-custom-authorizer`** branch,
created in both:

- `rosa-seaplane-api` (this repo) — linking endpoint, Flow 2a endpoint,
  Cedar actions/schema, `RequireIdentity` exemption;
- `rosa-seaplane` — custom-gateway terraform, Lambda authorizer, DynamoDB
  linking table, CloudFront/Keycloak infra.

**Do not push to remote yet** — commits stay local until the PoC shape is
  reviewed. The console work (P4.C, from the day-o repo) is separate and
  follows that repo's own workflow.

The day-o reference repo and `rosa` (CLI) are not touched by Phase 4 code
  changes.

## Exit criteria
- UI can authenticate with an RH Token, list linked ARNs (2a), and have an
  ARN-selected request (2b) authorized and forwarded with correct identity
  headers; Cedar authorizes it like any ARN caller.
- Linking lifecycle (create, revoke, staleness window) works end to end.
- Authorizer security review passed.

## Sequencing (risk-ordered: unknowns first, example-backed work last)

The task order below front-loads unresolved design questions and novel,
security-critical builds, and defers work we have working examples for
(the day-o repo covers Keycloak deployment and the console UI; neither can
de-risk the authorizer contract — they consume it).

```
TIER 1 — unknowns, start day 1 (parallel)
  T4/P4.1a  linking design + DynamoDB schema freeze        (external, long pole)
  T1.3      identity-header-contract.md                    (resolves the
            X-Amz-Request-Id / Principal-Org-Id sub-decisions P4.3 needs)
  P4.2a     authorizer spike: token verification + JWKS against a LOCAL
            Keycloak (docker; throwaway — not the RC deployment),
            context.authorizer.* output shape, cache design            
  P4.4a     resolve the RequireIdentity-vs-Flow-2a question (design note,
            can be decided in the T1.3 doc)

TIER 2 — novel builds (after their Tier 1 inputs)
  P4.1b     linking endpoint (known pattern; after schema freeze)
  P4.2b     full Lambda authorizer + unit tests (after spike + schema)
  P4.3      custom gateway terraform + CORS + header parity  (after T1.3;
             integration-tested against P4.2)
  P4.4b     Flow 2a endpoint + Cedar action                  (after P4.1b)

TIER 3 — example-backed work, deferred to last (day-o repo supplies working
         examples; consuming the now-frozen authorizer contract)
  P4.0      Keycloak on the RC + CloudFront                 (day-o
             deploy/keycloak manifests + standard CloudFront terraform)
  P4.C      console UI                                       (day-o apps/
             console-web + contracts, BFF dropped)
  P4.C.6    end-to-end demo                                  (convergence)

TIER 4 — hardening and closeout
  P4.5/P4.6 security review, alerting, threat model, full test pass
  P4.7      docs
```

Rationale:

- **P4.0 and P4.C move to the back.** Both have working references in the
  day-o repo — they are adaptation, not invention, and they cannot start
  de-risking anything until the authorizer contract (token shape, header
  output, 2a response) is frozen. Doing them early produces rework risk,
  not risk reduction. The one early exception is the **local throwaway
  Keycloak** in Tier 1 (P4.2a) — the authorizer spike needs *a* token
  source; that is a `docker run`, not the RC deployment.
- **The authorizer spike (P4.2a) is the highest-value early work we fully
  control** — T4 is external, so while it runs, spike token verification,
  the `context.authorizer.*` payload, and the cache against a stubbed
  linking table. Everything downstream integrates against what it
  produces.
- **P4.3 is gated on T1.3**, not on effort — the two open header
  sub-decisions are cheap to resolve and unblock the terraform.
