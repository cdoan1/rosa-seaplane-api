# PoC Decisions Record

Pragmatic answers to the open questions, made so build work can start.
Each records the decision, the rationale, and what to revisit before
production. Status: **decided for the PoC** unless noted.

## D-T2 — Cluster installer-role reference

**Decision: Option A** — service-set, immutable `spec.installerRoleArn` on
`ClusterSpec` (`+seaplane:write-mode=service-set`, pattern
`^arn:aws:iam::\d{12}:role/.*`), set by the Platform API at creation.
Full rationale in
[adr-cluster-installer-role-reference.md](adr-cluster-installer-role-reference.md).

*PoC simplification*: until the Phase 4 linking store exists, the API
accepts the installer-role ARN in the create request and stamps it into
the service-set field (validated against the caller's account), mirroring
how `CreatorARN` is set today. The field stays immutable + service-set so
the production resolution path (linking record) drops in without a schema
change.

*Revisit before production*: resolution from the registration/linking
record instead of request input; backfill for existing records.

## D-T3 — ExternalId value model

**Decision: Option A** — fully service-generated, opaque, ≥128-bit-entropy
value (e.g. 32 hex chars), minted per customer account, service-owned.
Full rationale in [adr-externalid-value-model.md](adr-externalid-value-model.md).

*PoC simplification*: no minting service. The value is a **deployment
configuration constant** (one per environment, like V1's jump accounts —
a per-environment value is uniqueness-trivial for a single-region PoC).
Both the operator and the API read it from config. When a real
account-linking store exists, switch to per-account minted values stored
there — consumers only change their lookup, not their behavior.

*Revisit before production*: per-account minting at link time; rotation
runbook; customer-facing docs (design open question 2 stays open for
product, not engineering).

## D-T7 — Assuming identity (jump account)

**Decision: reuse the V1 jump-account model, one jump account per
environment tier, shared by all regional seaplane deployments.** V1's
`JumpAccounts` map is keyed by environment (production
`710019948333`-equivalent, staging, FedRAMP separate) — not by region —
and customer trust policies trust `arn:aws:iam::<jump>:root`, the
**account**, so the trust principal is identical no matter which region
serves the customer.

Layering:

- **Jump account**: one per environment tier, shared across regions.
  Keeps the customer-facing trust-policy contract stable and
  region-independent; one account to audit and guardrail.
- **Jump-account role**: **one per regional deployment** inside the shared
  account (e.g. `rosa-seaplane-<region>`). Each region's service identity
  (operator/API pods) assumes its own role there, then the customer's
  installer role. Gives per-region least privilege, per-region revocation,
  and region-distinguishable CloudTrail sessions — no credentials shared
  across regions.
- **ExternalId**: orthogonal — it conditions the customer-side statement
  (jump account → installer role). One value serves all regions; regional
distinction lives entirely on the Red Hat side, inside the jump account.

Phase 1's gate therefore checks the jump-account-anchored statement
(stable, single principal), and the CLI's trust-policy template needs only
the ExternalId addition, not a new principal.

*PoC simplification*: one PoC-environment jump account and one role in it
(single region) — but the assume-role helpers (P2.2/P5.2) implement the
chain as `regional identity → its own jump-account role → installer role`
from day one, so adding regions is "create another role in the jump
account," not an architecture change. The jump-account role ARN is
deployment configuration alongside the ExternalId constant.

*Revisit before production*: whether per-region direct trust is worth the
principal churn (assumed no); jump-account role least-privilege audit.

## D-T9 — Home for policy/check templates

**Decision for the PoC: ship templates as versioned assets in the `rosa`
CLI repo** (the installer-role trust policy, with the ExternalId condition
and jump-account principal baked in). Rationale: OCM's serving role
disappears in V2; a seaplane-api template-serving endpoint is real
infrastructure to build/secure; CLI-shipped assets match how the V2 CLI
already works (`ocm-common` ships policy constants) and the CLI is the
only consumer during the PoC.

*Revisit before production*: the design doc's "centrally maintained
checks" rationale favors serving from seaplane-api eventually (update
without CLI releases) — revisit when the preflight check set stabilizes.
The **operator-side gate never trusts CLI assets**: it inspects the actual
trust policy in the customer account, so template location only affects
ergonomics, not enforcement.

## D-P3.1 — OIDC key storage target

**Decision: regional Secrets Manager** (as implemented). Amend
`docs/preflight-and-ui-login-flows.md` Flow 3/Security wording from
"Kubernetes Secret on the MC" to the regional SM secret. The implemented
path is complete and tested; an MC K8s Secret adds a second lifecycle for
the same material.

## D-P3.5 — OIDC key re-read/rotation

**Decision: stored copy is authoritative; no re-read at provisioning** for
the PoC. Rotation is manual: delete the OidcConfig's stored secret (or the
config) and recreate. Document the expectation. Revisit: resync annotation
or periodic re-read if customers need self-service rotation.

## D-D1 (Phase 5) — Pre-cluster installer-role resolution

**Decision: registration record (option a)** — the account registration
record (already checked by `RequireIdentity`/`isAccountRegistered`) carries
the installer-role ARN. *PoC simplification*: same as D-T2 — the discovery
endpoint accepts the role ARN as a validated request parameter until the
registration record gains the field.

## D-P4-token — Token issuer for the PoC

**Decision: deploy our own Keycloak on the regional cluster (RC), served
behind a CloudFront endpoint** (see phase-4 P4.0). The CloudFront hostname
is the token issuer URL — changing it later re-mints links, acceptable for
a PoC. Issuer/JWKS/audience remain configuration seams so swapping to real
Red Hat SSO later is a config change. Only standard OIDC claims (`sub`,
`exp`, `iss`, `aud`); linking records keyed on `sub` are issuer-agnostic.
This is issuer substitution only — not the discarded BFF pattern (no STS
federation, no SigV4 custody, no BFF service).

## D-1.4 — Trusted principal in the ExternalId gate

**Decision: jump-account root (D-T7) stands for the PoC.** Customer trust
policies keep `arn:aws:iam::<jump>:root`; the gate's predicate is
"every allow that lets any principal assume the role requires the exact
service-owned ExternalId, and the only allowed principals are within the
jump account." This matches the V1 OCM flow exactly (existing customers'
policies already trust the jump account; the CLI's role discovery depends
on it) and makes the V2 change a single added predicate. PR #1012's
operator-Pod-Identity-principal model is recorded as the alternative —
revisit only if gdbranco's design lands on it, in which case the gate
predicate and the assume-chain hop count change together (estimated 2–3 D
rework, not structural).

## D-T1 — Header-contract sub-decisions

**`X-Amz-Principal-Org-Id`: leave absent on the custom-authorizer path.**
The backend consumes it nowhere (verified in `middleware/identity.go`),
and the `AWS_IAM` gateway's native `principalOrgId` has no equivalent
source for an authorizer-resolved ARN. Emitting a derived value would add
a spoofable-looking header with no consumer. The `AWS_IAM` gateway keeps
its native mapping (harmless); the contract doc records the custom path's
value as intentionally absent.

**`X-Amz-Request-Id`: map from `$context.requestId`** in the custom
gateway's integration (as with the other headers), not from the
authorizer. API Gateway provides a unique request ID per request for both
authorization modes, so parity is exact. *Verify during P4.3 that the
`AWS_IAM` gateway's Request-Id source matches (T1.1's open question) — if
it uses stage variables instead, mirror whichever source it uses.*

**Token transport: `Authorization: Bearer <token>` header.** Keeps the
authorizer stateless (no cookie session handling in Lambda), matches the
API Gateway `identitySource` pattern, and lets the browser own refresh.
Cookie-based transport deferred as a possible hardening follow-up (CSRF
considerations) — not a PoC concern.

## D-T4 — Account-linking store, PoC simplification

**Decision (PoC schema, to be reconciled with the real authz design):**

- Single DynamoDB table, PK = the token subject (`rh-sub`, the Keycloak/
  SSO `sub` claim), one item per linked ARN: sort key = ARN string;
  attributes: `arn`, `accountId` (parsed), `linkedAt`, `revokedAt`
  (tombstone, optional).
- **Linking**: one write per ARN through the `AWS_IAM` gateway — SigV4
  proves ARN control, the verified token supplies `rh-sub`; conditional
  writes (attribute_not_exists) prevent duplicates.
- **Revocation**: tombstone write (`revokedAt`) or delete; the authorizer
  treats missing and tombstoned identically, honored within the cache TTL
  (bounded staleness, already documented).
- **Out of PoC scope, recorded for the real design**: organization
  ownership, export-control/ban enforcement, org-level administration,
  link limits per identity. These are the questions `docs/authz/design.md`
  defers; the PoC schema must not be treated as answering them.
- One ARN per item (not a set) keeps revocation and per-link metadata
  simple and avoids DynamoDB set-size limits.

## D-P4.4a — Flow 2a vs `RequireIdentity`

**Decision: path exemption.** `RequireIdentity`'s existing switch already
  exempts `/api/v0/live|ready|info`; add the Flow 2a path (e.g.
  `/api/v0/accounts/linkable`) to that exemption. Token-only requests
  legitimately have no ARN yet; the Lambda authorizer has still validated
  the token before the request reaches the backend. The alternative —
  emitting a Red Hat-identity header the middleware understands — adds a
  second identity shape to the backend for one endpoint; rejected for the
  PoC. The 2a handler derives the identity from the authorizer's context
  output (forwarded header) rather than the AWS identity headers.

## Decision status after this pass

All PoC-blocking questions are now answered. Remaining open items are
external conversations, not decisions we owe: T4's real linking design
(our PoC schema is a stand-in), 1.4 confirmation with gdbranco (our
decision is recorded and V1-aligned), and PR #1012's own open list.

## Explicitly out of scope

- **Support account role** — installer-role only for this PoC (V1 also
  injects ExternalId into the support role; follow-up if ever in scope).
- **BFF/Keycloak UI path** — custom-authorizer gateway is the decided UI
  architecture.

## What this unblocks

With these calls made, the build order is (no external dependencies):

1. **P2.2** — ExternalId-aware, two-hop assume-role helper in
   `seaplane-operator/internal/oidc` (config constant for the value and
   jump-account role).
2. **Phase 1** — operator ExternalId gate + CLI trust-policy template
   change (per D-T9, CLI-shipped). **Note:** the gate's authoritative spec
   is now `rosa-seaplane` PR #1012; settle the trusted-principal divergence
   (jump-account root vs operator Pod Identity role) with gdbranco first — see
   `phase-2-preflight-controller.md`.
3. **P3.2** — API-side secretArn account-ID validation (independent,
   small).
4. **T1.3** — `identity-header-contract.md` (local doc, feeds Phase 4).

**Deferred to PR #1012 / gdbranco** (2026 update): the preflight framework
and check implementations (former P2.1, P2.3–P2.5) — the authoritative
preflight design, including the V1 check catalog (resolving T5), lives in
`rosa-seaplane` PR #1012. Our scope keeps only the credential-chain helper
(P2.2) and the Phase 0 decisions that feed its open questions.

Still blocked on others (not PoC-critical): T4 (linking design → P4.1).
