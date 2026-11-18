# Preflight & UI Login Flows — PoC Implementation Plan

**Source design**: [`docs/preflight-and-ui-login-flows.md`](../preflight-and-ui-login-flows.md)

**Goal**: Prove the installer-role-based V2 design end to end: synchronous UI
discovery, asynchronous preflight gated by cluster Conditions, unmanaged OIDC
signing-key retrieval, and the RH-Token custom-authorizer path for the UI.

## Phase overview, status, and dependencies

| Phase | Status | Notes |
|-------|--------|-------|
| 0 | in progress | Code review done; tasks T1–T9 defined. ADRs drafted for **T2** and **T3**; open decisions: **T7** (jump-account/assuming identity), **T9** (home for policy templates — V1 serves them from OCM); **scope note: installer-role only** (support role explicitly out of scope); T4, T5 still open |
| 1 | not started | Blocked by Phase 0 T3 |
| 2 | **deferred to gdbranco's design** | Authoritative preflight design + V1 check catalog now live in `rosa-seaplane` PR #1012 (`docs/design/aws-preflight-design.md`); our phase-2 doc is reduced to cross-references and the divergence notes below. The ExternalId/jump-account credential work (P2.2) remains ours |
| 3 | design drafted; **mostly implemented already** | `reconcileUnmanaged` in `oidcconfig_controller.go` covers the async read/validate/persist flow; remaining work is P3.1–P3.6 (decisions, ExternalId, API-side ARN check, hardening) |
| 4 | planned | Custom-authorizer architecture decided; longest lead is the account-linking design (P4.1 / Phase 0 T4). Net-new terraform (no Lambda-authorizer precedent exists) |
| 5 | planned | Grounded design done; new decision D1 (pre-cluster installer-role resolution — recommend the registration/linking record). No AWS account-facing clients exist in `seaplane-api` today (P5.2 net-new) |
| 6 | not started | Blocked by 1–5 |

Key cross-references:

- **PoC decisions record**: [poc-decisions.md](poc-decisions.md) — pragmatic
  answers to all open questions (T2, T3, T7, T9, P3.1, P3.5, D1), each with
  what to revisit before production. With these calls made, the build order
  in its final section has no external dependencies.

- **ADR — Cluster installer-role reference** (Phase 0 T2):
  [adr-cluster-installer-role-reference.md](adr-cluster-installer-role-reference.md) —
  recommends a service-set immutable `spec.installerRoleArn` on Cluster.
- **ADR — ExternalId value model** (Phase 0 T3):
  [adr-externalid-value-model.md](adr-externalid-value-model.md) —
  recommends a fully service-generated per-account value (Option A),
  org-scoped-suffix scheme deferred as a follow-up.
- Phase 2 and Phase 3 **share P2.2** (ExternalId-aware AssumeRole in
  `internal/oidc/infra.go` — today `assumeRoleCredentials` passes no
  `ExternalID`).
- Phase 3's design diverges from the source design on key storage
  (regional Secrets Manager, not an MC Kubernetes Secret) — decision P3.1.
- **Preflight (Phase 2) is deferred** to the authoritative design in
  `rosa-seaplane` PR #1012 (`docs/design/aws-preflight-design.md`, gdbranco).
  It also resolves Phase 0 **T5** — the V1 check inventory is its catalog
  table. See `phase-2-preflight-controller.md` for what remains ours and the
  divergences to settle with that design (notably: the trusted principal in
  the ExternalId gate — jump-account root vs. the operator's Pod Identity role).
- Phase 4 uses the **custom-authorizer gateway** (decided; see its design
  doc for rationale grounded in the code review).

```
Phase 0 (spikes / prerequisites / decisions)
   |
   +---> Phase 1 (ExternalId gate)          -- no UI infra needed
   |         |
   |         +---> Phase 2 (preflight controller) ---+
   |                    |  (shares P2.2 with v3)     |
   |                    +---> Phase 3 (OIDC key) ----+---> Phase 6 (E2E demo)
   |
   +---> Phase 4 (UI auth path: custom gateway) ---+
                    |
                    +---> Phase 5 (discovery API) --+
```

- Phase 0 is a short de-risking pass; its outputs block Phases 1–3.
- Phases 1–3 (CLI/operator half) are independent of Phases 4–5 (UI half).
- Phase 2 and Phase 3 can run in parallel (P3 is mostly done; P2.2 shared).
- Phase 4 has the largest new operational surface; align with the
  Platform API authz design (`docs/authz.md`) early even if built later.

## Documents

| Phase | Focus | Doc |
|-------|-------|-----|
| 0 | Prerequisites and spikes | [phase-0-prerequisites.md](phase-0-prerequisites.md) |
| 1 | `ExternalId` trust-policy gate | [phase-1-externalid-gate.md](phase-1-externalid-gate.md) |
| 2 | Asynchronous preflight controller | [phase-2-preflight-controller.md](phase-2-preflight-controller.md) |
| 3 | Unmanaged OIDC signing-key retrieval | [phase-3-oidc-key-retrieval.md](phase-3-oidc-key-retrieval.md) |
| 4 | Custom-authorizer gateway for the UI | [phase-4-ui-auth-path.md](phase-4-ui-auth-path.md) |
| 5 | Synchronous discovery API | [phase-5-discovery-api.md](phase-5-discovery-api.md) |
| 6 | End-to-end hardening and demo | [phase-6-e2e-demo.md](phase-6-e2e-demo.md) |

## Recommended starting order

1. **Phase 0 external asks out first** (T4 linking scope, T5 V1 check
   inventory — both depend on other teams and gate Phases 4 and 2).
2. **Phase 0 T3** (ExternalId value model) — gates Phase 1, the first
   build phase.
3. **Start P2.2 immediately** (ExternalId-aware AWS client) — no blockers,
   shared by Phases 2 and 3.
4. Decide **P3.1** (OIDC key storage target) and **P3.5** (re-read/rotation
   semantics) — cheap decisions that close out Phase 3's design deltas.
5. Phases 2–3 remainder, then 4–5, then 6.
