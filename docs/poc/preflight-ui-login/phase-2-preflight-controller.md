# Phase 2 — Asynchronous Preflight Controller

**Status**: **DEFERRED** — preflight design and implementation are now owned
by the authoritative design in the `rosa-seaplane` repo:
**PR #1012, `docs/design/aws-preflight-design.md`** (gdbranco,
ROSAENG-65209). This doc is reduced to cross-references, what remains in
our scope, and divergences to settle against that design.

## What PR #1012 covers (and supersedes here)

- **The V1 check inventory** — resolves Phase 0 task **T5**. Its catalog
  table enumerates the `uhc-clusters-service` callbacks
  (`clusters-service: cmd/clusters-service/service/aws_preflight/preflight_service.go`)
  with operation types and applicability, plus the network-verifier worker,
  the Seaplane Installer Role trust-policy gate, and preflight artifact
  cleanup as migration-inventory items.
- **A richer architecture than our draft**: a dedicated preflight
  reconciler over internal `PreflightResource` objects (per Cluster and
  NodePool), step fingerprints (SHA-256 over canonical target/check/
  configuration inputs), per-step `Observe`/`Block` policies, per-step +
  aggregate `Preflight` conditions with `Unknown/True/False` semantics,
  restart/staggering behavior, orphaned write-probe cleanup, Cluster-vs-
  NodePool check scoping, and ApplyDesire admission gates.
- **Porting strategy options** for the `uhc-clusters-service` code
  (shared module vs. port-in-operator vs. staged hybrid vs. shared
  conformance suite).
- **The installer-role association question** (its Options 1/2 correspond
  to our T2 ADR's Options A/B — see divergence notes below).

## What remains in our scope (not superseded)

- **P2.2 — the ExternalId-aware, two-hop assume-role helper** in
  `seaplane-operator/internal/oidc` (jump-account chain per our D-T7
  decision). PR #1012 requires the operator to assume the installer role
  but does not specify the credential chain.
- **Phase 0 decisions** that feed its "Decisions to settle before
  implementation" list — in particular the installer-role association
  storage (its #1) and the ExternalId uniqueness scope (its Customer AWS
  account access section).

## Divergences to settle with the PR #1012 design

1. **Trusted principal in the ExternalId gate (real conflict).** Our D-T7
   decision (best-practice, V1-aligned) has customer trust policies trust
   the **jump account root** (`arn:aws:iam::<jump>:root`), with each
   region's operator identity assuming a dedicated jump-account role
   before the installer role. PR #1012 instead specifies the gate must
   prove the trust policy permits **only the configured Seaplane operator
   AWS role (the service principal supplied by its Pod Identity
   configuration)** — a per-region principal trusted directly, no jump
   account. These are incompatible predicates; align with gdbranco before
   the gate is implemented. Note V1 evidence (`rosa` CLI `JumpAccounts`)
   supports the jump-account model; per-region principals mean customer
   trust policies change per region and on role recreation.
2. **Gate placement.** Our draft placed preflight as a gate function
   inside `ClusterReconciler`; PR #1012 uses a separate `PreflightResource`
   reconciler with fingerprint-based gating and ApplyDesire admission
   binding. PR #1012's model is more complete (NodePool scoping, revision
   invalidation, restart semantics) — defer to it.
3. **Installer-role association storage.** PR #1012 leaves Option 1
   (per-Cluster field) vs Option 2 (account-scoped resource) open — the
   same choice as our T2 ADR, where we recommended Option 1/Option A.
   Share the ADR into that decision.

## Divergence-free alignment points

- ExternalId is **service-owned**, stable per access association (not per
  request/session), and its uniqueness scope (per AWS account vs. per
  account/org association) is explicitly left open — matches our T3 ADR's
  open production question.
- The gate inspects the **entire trust policy** via IAM `GetRole` using
  the assumed installer-role session (the V1 installer policy grants
  `iam:GetRole` on `*`), rejecting wildcard/alternate-principal/
  unconditional allows — matches our Phase 1 gate design, and adds the
  `GetRole` permission dependency we should verify.
- Async-only validation, conditions on `.status`, service-set fields, and
  paused-not-discarded semantics all agree with our drafts.

## Original code-review findings still relevant

- `internal/oidc/infra.go`'s `assumeRoleCredentials` passes no
  `ExternalID` (P2.2, still ours to build).
- `OidcConfigReconciler` remains the template for reconcile-loop idioms
  wherever the PR's design lands.
- Sharding is namespace-based, so per-account AWS-call bounding matters
  (PR #1012 also calls for per-account limits and jitter — aligned).
