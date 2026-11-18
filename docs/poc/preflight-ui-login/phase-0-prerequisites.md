# Phase 0 — Prerequisites, Code Review Findings, and Action Plan

**Status**: in progress — code review complete, tasks below
**Depends on**: nothing
**Blocks**: Phase 1, Phase 2, Phase 3 (informs Phase 4)
**Duration estimate**: short; mostly verification, decisions, and one external dependency (V1 check inventory)

## Goal

De-risk the design's assumptions before build work starts. This document
records what an initial code review across the involved repos found, the
gaps it exposed, and the actionable tasks that close them.

Repos reviewed:

- `rosa-seaplane-api` (this repo) — API types, identity middleware, authz design
- `rosa-seaplane` — terraform (API Gateway), operator reconcilers
- `rosa` — CLI trust-policy / ExternalId handling, account-role creation

## Findings

### F1 — Identity-header contract (verified, one gap)

The `AWS_IAM` gateway contract lives in **`rosa-seaplane`**
(`terraform/modules/api-gateway/integration.tf`) — note the design doc
references it as if in this repo; path correction needed. It maps six headers
from `context.identity.*` on both the proxy and root integrations:

| Header | Gateway source |
|--------|----------------|
| `X-Amz-Caller-Arn` | `context.identity.userArn` |
| `X-Amz-Account-Id` | `context.identity.accountId` |
| `X-Amz-User-Id` | `context.identity.user` |
| `X-Amz-Source-Ip` | `context.identity.sourceIp` |
| `X-Amz-Principal-Org-Id` | `context.identity.principalOrgId` |
| `X-Amz-Request-Id` | *(not in integration.tf — confirm source, likely stage/request mapping)* |

The backend consumer is `seaplane-api/pkg/middleware/identity.go`, which
parses five headers (`Caller-Arn`, `Account-Id`, `User-Id`, `Source-Ip`,
`Request-Id`) into context and rejects requests without Account-Id +
Caller-Arn (`RequireIdentity`).

**Gaps**:
1. `X-Amz-Principal-Org-Id` is emitted by the gateway but **ignored by the
   backend** — no code in `seaplane-api` reads it. Decide whether it is dead
   weight (drop it from the contract) or future-needed (the design doc says
   the UI authorizer must populate it "explicitly or leave it absent").
2. The contract exists only in terraform + code; there is no single
   specification document the Phase 4 custom gateway can be tested against.

### F2 — Cluster state has no installer-role reference (design gap for Phases 2–3)

- `ClusterSpec` has `AccountID` and `CreatorARN` (both `+seaplane:write-mode=service-set`),
  and `RolesRef` (HyperShift `AWSRolesRef` — control-plane/support/worker
  roles, **not** the cross-account installer role).
- Only `OidcConfig` carries an `InstallerRoleArn` (`spec.installerRoleArn`,
  CEL-validated, immutable, required for `type: unmanaged`).

The preflight controller and the ExternalId gate need to know **which
installer-role ARN to assume for a given Cluster**. Today nothing on the
Cluster records it. This is a real API design decision (new service-set
field? derived from the account's registered/linkage record? part of the
authz attachment model?) and it blocks Phase 2.

### F3 — ExternalId support already partially exists in the CLI (reusable, but user-owned today)

- `rosa` CLI: `pkg/aws/sts_external_id.go` injects `sts:ExternalId` into
  installer/support trust policies when the **user** supplies
  `--external-id`, and contains substantial reusable logic: trust-policy
  JSON parsing, `PreserveSTSExternalIDsInTrustPolicy`, discovery of existing
  ExternalIds, ambiguity/mismatch detection across roles.
- `ocm-common` `accountroles.RequiresSTSExternalIDInTrustPolicy` gates which
  role keys get the condition (installer + support).

**Gap**: the design requires a **service-owned, uniqueness-guaranteed**
ExternalId. The current CLI flow is opt-in and user-supplied. Open decisions:
who generates the value (per org? per account-link?), where it is stored,
how the operator and CLI both obtain it, and how the existing
user-supplied flag migrates (Phase 1 will need to reconcile with it).

### F4 — Account-linking design does not exist yet (blocks Phase 4)

`docs/authz/design.md` explicitly defers it: "Any future linking design must
decide organization ownership, export-control and …" — the account-linking
store, linking endpoint, and token→ARN resolution are un-designed. The
preflight/UI-login design assumes they exist. Phase 4 cannot start until
that authz workstream is scoped; this is the longest-lead item.

### F5 — V1 preflight check set is not in any reviewed repo (external dependency)

The V1 checks run server-side in OCM (not in the `rosa` CLI, which only
reports conditions). Porting coverage (Phase 2) needs the authoritative V1
check inventory — including the network verifier's EC2 probe details and the
block-vs-warn HCP toggle semantics — from the OCM/V1 backend owners. Start
this conversation now; it is not unblockable from within these repos.

### F6 — Reusable scaffolding that already exists

- `oidcconfig_controller.go` in `seaplane-operator` — reconciler pattern and
  OidcConfig handling Phase 3 builds on (secretArn/installerRoleArn fields
  already CEL-validated).
- `hypershift-oidc` terraform module in `rosa-seaplane` — existing OIDC infra
  context.
- DynamoDB usage already present in the operator (`fake_dynamo_test.go`,
  authz attachments) — relevant to the linking store and Cedar policy store.
- `docs/authz/` (design, management, storage-contract) — the Cedar model the
  discovery endpoints (Phase 5) must plug into.

### F8 — Support account role: out of scope for this PoC

V1 account roles are installer, **support**, worker, and control-plane
(`rosa` CLI `pkg/aws/policies.go` `AccountRoles`), and V1 injects
`sts:ExternalId` into installer **and** support trust policies
(`RequiresSTSExternalIDInTrustPolicy`). This PoC deliberately scopes to
the **installer role only**. If the support role is ever brought into
scope, it needs the same ExternalId treatment and CLI injection — a
follow-up question, not a task here.

### F9 — V1 policy and check templates are served by OCM; V2 has no home for them

The CLI does not bake in trust policies or managed policies — it fetches
them from OCM (`GetPolicyDetails(map[string]*cmv1.AWSSTSPolicy, ...)`;
`r.OCMClient.GetPolicies("OSDSCPPolicy")` for SCP checks). With no OCM in
V2, something must serve the installer-role trust-policy and managed-
policy templates (and the check definitions) — a seaplane-api endpoint or
versioned assets shipped in the CLI repo. This also sharpens T5: the V1
policy to audit **is** the OCM-served policy document.

### F10 — Network-verifier client is reusable from `ocm-common`

`ocm-common/pkg/aws/aws_client/instance.go` implements the EC2 probe
(`RunInstances` + client interface + mocks) — Phase 2's network verifier
should port this rather than reimplement.

## Actionable tasks

### T1 — Close the header-contract gaps (F1) — **decided (D-T1)**; contract doc still to write
- [x] T1.1 ~~Find where `X-Amz-Request-Id` originates~~ — **decided**: map
      from `$context.requestId` in the custom gateway; verify during P4.3
      that the `AWS_IAM` gateway matches.
- [x] T1.2 ~~Decide `X-Amz-Principal-Org-Id`'s fate~~ — **decided**: leave
      absent on the custom path (no backend consumer; a derived value adds
      nothing). The `AWS_IAM` gateway keeps its native mapping.
- [ ] T1.3 Write `docs/poc/preflight-ui-login/identity-header-contract.md`:
      the authoritative table (header, source expression, backend consumer,
      required/optional) that Phase 4's gateway and its integration tests are
      built against. **Also record D-P4.4a** (Flow 2a path exemption in
      `RequireIdentity`) and the Bearer-header transport decision (D-T1).
- [ ] T1.4 Fix the path reference in `docs/preflight-and-ui-login-flows.md`
      (terraform lives in `rosa-seaplane`, not this repo).

### T2 — Decide how a Cluster references its installer role (F2) — design decision
- [ ] T2.1 Write a short ADR covering the options: (a) new service-set
      `spec.installerRoleArn` on Cluster, (b) resolution via the authz
      attachment/linkage record at reconcile time, (c) reuse of
      `OidcConfig.InstallerRoleArn` generalized. Include immutability,
      CEL validation, and codegen impact (`make generate`, clientset, OpenAPI).
- [ ] T2.2 Get sign-off from API + operator owners; record the outcome here
      and update Phase 2's doc.
- [ ] T2.3 Verify `RolesRef` (HyperShift `AWSRolesRef`) really never carries
      the cross-account installer role in the V2 provisioning path, so we
      don't design a second source of truth.

### T3 — Define the service-owned ExternalId model (F3) — decision + spike
- [ ] T3.1 Decide the uniqueness model: fully service-generated, or
      org-scoped prefix + optional customer suffix (design open question 2).
      Constraint from the design doc: the service must guarantee uniqueness.
- [ ] T3.2 Decide generation & storage: which component mints it, where it is
      stored (per org? per account-link record? in seaplane-db?), and how
      both the `rosa` CLI (role creation) and `seaplane-operator` (gate,
      AssumeRole) obtain the same value.
- [ ] T3.3 Inventory the reuse surface in `rosa` `pkg/aws/sts_external_id.go`
      — which helpers the V2 CLI flow can call directly vs. what needs a
      service-value mode alongside the existing user-supplied flag.
- [ ] T3.4 Draft the public-facing requirement doc skeleton (customer impact,
      migration for accounts with no ExternalId today).

### T4 — Scope the account-linking authz workstream (F4) — alignment
- [ ] T4.1 Open the linking-design discussion with the authz owners
      (`docs/authz/design.md` is the anchor): org ownership, export control,
      revocation semantics, DynamoDB schema, linking endpoint via the
      `AWS_IAM` gateway.
- [ ] T4.2 Record the agreed boundary (what Phase 4 builds vs. consumes) in
      `phase-4-ui-auth-path.md`.

### T5 — Obtain the V1 preflight check inventory — **RESOLVED**
- [x] T5.1 ~~Identify the OCM/V1 owners and request the check list~~ —
      resolved by `rosa-seaplane` PR #1012 (`docs/design/aws-preflight-design.md`),
      whose catalog table enumerates the `uhc-clusters-service` preflight
      callbacks with operation types and applicability, plus the network
      verifier, the trust-policy gate, and artifact cleanup.
- [ ] T5.2 Cross-reference the catalog against the V1 installer managed
      policy (design assumption: it covers everything; verify, don't
      assume). The PR notes `iam:GetRole` on `*` is granted by the checked-in
      V1 installer policy — confirm deployed versions retain it.
- [ ] T5.3 (was folded into Phase 2, now deferred) — preflight
      implementation work is deferred to the PR #1012 design / gdbranco;
      see `phase-2-preflight-controller.md` for scope split and divergences.

### T9 — Decide the home for policy/check templates (F9) — decision
- [ ] T9.1 Decide: seaplane-api serves the installer-role trust-policy and
      managed-policy templates (OCM's role) vs. versioned assets shipped
      in the `rosa` CLI repo. Consider update cadence (the design doc's
      "centrally maintained checks" rationale favors serving, not
      shipping).
- [ ] T9.2 Fold the outcome into T5's inventory request — the audited V1
      policy is the OCM-served document, not CLI code.

### T6 — Lambda authorizer caching spike (Phase 4 input) — small
- [ ] T6.1 Prototype the decision shape: token-only mode (Flow 2a, never
      cached) vs mapping mode (Flow 2b, app-level cache keyed on
      `(RH token, ARN)`, TTL = min(~5 min, token remaining validity)).
- [ ] T6.2 Confirm how `context.authorizer.*` values reach integration
      request headers in the custom gateway (mirroring T1.3's table).

## Exit criteria

- [ ] `identity-header-contract.md` committed; `Principal-Org-Id` decided.
- [ ] Installer-role reference ADR decided and signed off.
- [ ] ExternalId value model decided; storage and CLI/operator access path defined.
- [ ] Linking-design boundary agreed with authz owners.
- [ ] V1 check inventory obtained and policy-coverage table committed.
- [ ] Authorizer caching approach written up.

## Sequencing within Phase 0

```
T5 (external, start day 1)  ─┐
T4 (external, start day 1)  ─┼─> longest lead; everything else is local
T3 (decision) ──> feeds Phase 1
T7 (decision) ──> feeds Phase 1 gate predicate + P2.2/P5.2 credential chain
T9 (decision) ──> feeds Phase 1 + T5
T2 (decision) ──> feeds Phase 2/3
T1, T6 (local) ──> feed Phase 4
```

T4 and T5 depend on other teams and gate Phases 4 and 2 respectively —
kick both off first. T3 is the gating decision for Phase 1 (first build
phase), so prioritize it after the external asks are out.
