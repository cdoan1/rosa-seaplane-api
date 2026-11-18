# Phase 3 — Unmanaged OIDC Signing-Key Retrieval

**Status**: design drafted (below); partially implemented already
**Depends on**:
- Phase 2 **P2.2** (ExternalId-aware AssumeRole — `ReadCrossAccountSecret`
  today assumes the role **without** an ExternalId)
- Phase 0 **T3** (the ExternalId value itself)
**Can run in parallel with**: Phase 2 (shares P2.2)
**Duration estimate**: small–medium — most of the pipeline already exists

## Goal

For self-managed (unmanaged) OIDC: read the customer's RSA private signing
key from their Secrets Manager via the installer role, validate it, and
persist it in the regional control plane — asynchronously, with failures
surfaced through the OidcConfig/Cluster status rather than a synchronous
cross-account call at registration.

## Code review findings — most of this phase already exists

### F1 — The end-to-end unmanaged flow is implemented in `oidcconfig_controller.go`

`reconcileUnmanaged` already does, asynchronously, on the reconcile loop:

1. issuer-URL Index reservation (`reserveIssuerURLIndex`, uniqueness via
   `OidcIssuerReservationsNamespace` + owner UID);
2. idempotency check — `PrivateKeyExists` in the regional secret store;
3. **cross-account read** — `OIDC.ReadCrossAccountSecret(secretArn,
   installerRoleArn)` builds a Secrets Manager client on assumed
   credentials and `GetSecretValue`s the customer's secret;
4. **RSA validation** — `oidc.ValidateRSAPrivateKey(keyData)`;
5. **persistence** — `OIDC.StorePrivateKey(accountID, configID, keyData)`;
6. **issuer reachability** — `checkReadiness` → `ComputeThumbprint`
   (TLS dial with SSRF validation in `resolveIssuerHost`), sets
   `Ready`/`OIDCConfigured` condition, `Phase=Ready`, `Status.Thumbprint`,
   requeues on a 24h thumbprint refresh;
7. **cleanup** — `reconcileDelete` removes the stored key and the Index
   behind the `seaplane.io/oidcconfig` finalizer.

Conditions and phases already exist: `CrossAccountReadFailed`,
`InvalidPrivateKey`, `SecretStoreFailed`, `IssuerNotReady`,
`IssuerURLConflict`, `AwaitingCluster`, plus `OidcConfigPhase`
`Pending/Ready/Error`.

### F2 — Divergence from the design doc: storage target

The design doc says the key is materialized as a **"Kubernetes Secret on
the MC"**. The implementation stores it in the **regional Secrets Manager**
owned by Red Hat (`SecretName(accountID, configID)`, create/overwrite via
`StorePrivateKey`, force-delete in `reconcileDelete`). This is a decision
point, not a bug: either

- (a) amend the design doc to match the implementation (regional SM
  secret; arguably cleaner — no K8s Secret lifecycle, matches existing
  `hypershift-oidc` infra), or
- (b) keep the design's MC-Kubernetes-Secret target and change the
  implementation.

Default recommendation: **(a)** — the implemented path is complete, tested,
and the MC-secret variant adds a second storage lifecycle for the same
material. The "no copy outside the control plane's signing-key Secret"
requirement is satisfied by the regional SM secret being control-plane
infrastructure.

### F3 — Registration-time account-ID check is missing

The design requires a **synchronous local check at registration**: the
account ID embedded in `spec.secretArn` must match the caller's account.
Today `seaplane-api/pkg/handlers/oidcconfig.go` only checks field
*presence* for unmanaged type (error code
`OIDCCONFIGS-MGMT-CREATE-005`); no ARN parsing or account comparison
exists. This is the one genuinely missing synchronous piece.

### F4 — No ExternalId on the cross-account assume

`ReadCrossAccountSecret` → `assumeRoleCredentials(roleARN)` passes no
`ExternalID` to `stscreds.NewAssumeRoleProvider`. Under the new hard
requirement, customers' trust policies will *require* the service-owned
ExternalId, so this call will start failing once Phase 1 lands — P2.2 is a
shared prerequisite, not optional.

### F5 — Tag scoping is enforced customer-side, not service-side

The design notes V1 scopes `secretsmanager:GetSecretValue` to secrets
tagged `red-hat-managed=true`. The operator code does not filter by tag;
the constraint lives in the installer-role's permission policy (customer
account). Fine as-is; document it so the Phase 0 T5 policy-audit table
records where this control actually lives.

### F6 — Timing already matches "async only"

The design drops V1's synchronous registration-time cross-account
validation. The current flow already validates only at reconcile time
(when a referencing Cluster exists, `isReferencedByCluster`), which is
provisioning-adjacent. No change needed beyond the F3 local check.

## Actionable tasks

### P3.1 — Resolve the storage-target divergence (F2) — decision, do first
- [ ] ADR (or design-doc amendment): regional Secrets Manager secret vs MC
      Kubernetes Secret. Record rationale, secret naming
      (`SecretName(accountID, configID)`), lifecycle, and who may read it.
- [ ] Update `docs/preflight-and-ui-login-flows.md` Flow 3 / Security
      wording to match the decision.

### P3.2 — Registration-time ARN account-ID validation (F3)
- [ ] `seaplane-api/pkg/handlers/oidcconfig.go`: on create (and patch, since
      the fields are CEL-immutable patch should not reach this path, but
      verify), parse `spec.secretArn` and reject when the embedded account
      ID ≠ caller account ID (`GetAccountID(ctx)` from the identity
      middleware). Local check only — no AWS calls.
- [ ] New error code alongside `OIDCCONFIGS-MGMT-CREATE-005`
      (e.g. `...-CREATE-006`, secretArn account mismatch).
- [ ] Same validation for `spec.installerRoleArn`'s account if T2/Phase 2
      conventions make that meaningful.
- [ ] Handler unit tests: matching, mismatched, malformed ARN.

### P3.3 — ExternalId on the cross-account read (F4, shares P2.2)
- [ ] Thread the service-owned ExternalId through
      `ReadCrossAccountSecret` → `assumeRoleCredentials(roleARN, externalID)`
      (P2.2's cache keyed on `(roleARN, externalID)`).
- [ ] Map AssumeRole failure to a distinct condition reason (e.g.
      `CrossAccountAssumeDenied`) so a trust-policy problem (Phase 1 gate)
      is distinguishable from a secret-read problem in the existing
      `CrossAccountReadFailed` path.
- [ ] Integration test with a role requiring ExternalId.

### P3.4 — Sensitive-material hardening
- [ ] Audit the existing path: `keyData` must never appear in logs,
      conditions, or API responses. Check `CrossAccountReadFailed` /
      `InvalidPrivateKey` messages don't embed secret contents (they
      currently carry `err.Error()` — verify AWS errors can't leak the
      secret value; they can include the secret *ID*, which is acceptable).
- [ ] Add a test asserting conditions/errors never contain the key material.
- [ ] Confirm logcheck/golangci-lint rules pass (`make lint`).

### P3.5 — Re-read semantics at provisioning (design conformance)
- [ ] V1 re-reads the key asynchronously at provisioning. Decide whether
      V2 keeps the stored copy authoritative (current behavior — read once
      at config reconcile, 24h thumbprint refresh only) or re-reads the
      customer secret when the referencing Cluster provisions. Record the
      decision; implement if re-read is chosen (small: call
      `ReadCrossAccountSecret` + `ValidateRSAPrivateKey` in the
      cluster-provisioning path and surface failures as a Cluster
      condition).
- [ ] Define rotation handling: if the customer rotates their secret, when
      does V2 notice? (Options: config resync annotation, periodic re-read,
      never — document customer expectations.)

### P3.6 — Tests
- [ ] Verify existing unit/integration coverage for
      `reconcileUnmanaged` failure paths (`InvalidPrivateKey`,
      `CrossAccountReadFailed`, `IssuerNotReady`) — extend for the new
      `CrossAccountAssumeDenied` reason and the P3.5 behavior.
- [ ] `make verify`, `make lint`, `make test-operator` green.

## Exit criteria
- Storage-target decision recorded and design doc aligned.
- ARN account-ID validation enforced at registration (API-side, tested).
- Cross-account read carries the service-owned ExternalId; assume failures
  distinguishable from read failures.
- Sensitive-material guarantees tested.
- Re-read/rotation semantics decided and documented.

## Sequencing

```
P3.1 (decision, immediate)
P3.2 (independent, start now)
P2.2 (shared with Phase 2) ──> P3.3 ──> P3.6
P3.4 (parallel, any time)
P3.5 (decision; implementation optional per outcome)
```
