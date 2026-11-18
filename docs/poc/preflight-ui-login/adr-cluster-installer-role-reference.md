# ADR: How a Cluster references its installer-role ARN

**Status**: draft — pending API + operator owner sign-off (Phase 0 task T2)
**Blocks**: Phase 1 (ExternalId gate needs a role to inspect), Phase 2
(preflight needs a role to assume)
**Related**: `docs/preflight-and-ui-login-flows.md`,
`docs/poc/preflight-ui-login/phase-0-prerequisites.md` (finding F2)

## Context

The preflight controller and the ExternalId trust-policy gate must, for a
given Cluster, know **which installer-role ARN to assume into the customer
account**. Current state of the API:

- `ClusterSpec` has `AccountID` and `CreatorARN` (both
  `+seaplane:write-mode=service-set`), and `RolesRef`
  (HyperShift `AWSRolesRef`: control-plane/support/worker roles — the
  per-cluster operator roles, **not** the cross-account installer role).
- Only `OidcConfig.spec.installerRoleArn` (account-scoped, CEL-validated,
  immutable, required for `type: unmanaged`) carries an installer-role
  reference — and it exists only to read the OIDC signing-key secret.

Preflight must run for **every** cluster, managed or unmanaged OIDC, so the
OidcConfig field cannot serve as the general source.

## Options

### Option A — service-set immutable field on ClusterSpec *(recommended)*

Add `spec.installerRoleArn` to Cluster:

- `+seaplane:write-mode=service-set`, immutable, pattern
  `^arn:aws:iam::\d{12}:role/...$`;
- set by the Platform API at creation time, resolved from the caller's
  registered identity/account linkage (the same source the authz
  attachment model uses);
- the operator reads it in the preflight gate; nothing derives it at
  reconcile time.

Pros: explicit, testable, one source of truth per cluster; mirrors the
proven `OidcConfig.installerRoleArn` shape; the CEL-validated-immutable
pattern already exists to copy.
Cons: an API/codegen change (`make generate`, clientset, OpenAPI) and one
more service-set field to backfill for existing clusters.

### Option B — resolve at reconcile time from the authz/linkage record

The operator queries the account-linking/attachment store (DynamoDB) for
the installer-role ARN associated with the cluster's account.

Pros: no new API field.
Cons: couples the operator to the (not-yet-designed — Phase 0 T4) linking
schema; reconcile-time resolution can change under the operator (customer
re-links mid-flight) making preflight results ambiguous about *which* role
was checked; cross-Cluster shared state consulted per cluster.

### Option C — naming-convention derivation (V1 style)

Derive the ARN from the account-role prefix recorded on the cluster (V1's
`<prefix>-Installer-Role` convention, as the CLI's
`GetPrefixFromAccountRole` does today).

Pros: no new state anywhere.
Cons: names are conventions, not contracts — renamed or custom-named roles
break the gate silently; couples service behavior to CLI naming choices.

## Recommendation

**Option A.** It is the only option where the checked role is pinned at
creation, immutable for the cluster's life, and identical across the API,
the ExternalId gate, preflight, and any audit tooling. Option B becomes
*how the Platform API resolves the value to write*, not how the Cluster
stores it — so A and the future linking design compose instead of
competing.

## Consequences

- `api/v1alpha1/cluster_types.go`: new field with markers; `make generate`
  (deepcopy, passthrough, registry, CRDs, clientset, OpenAPI) and
  `make verify` must pass.
- The Platform API needs the value at create time — until the Phase 0 T4
  linking design exists, a temporary resolution path is acceptable (e.g.
  CLI-supplied + validated, or the account allowlist/registration record)
  as long as the field remains service-set thereafter.
- Backfill/migration note for existing cluster records.
- `OidcConfig.spec.installerRoleArn` stays as-is (it already exists and is
  immutable); a consistency check that it matches the Cluster's role where
  both are present can be a preflight check.

## Decision

Pending sign-off. Record outcome here and update
`phase-1-externalid-gate.md` and `phase-2-preflight-controller.md` (P2.1).
