# Phase 1 — `ExternalId` Trust-Policy Gate

**Status**: design drafted; **the gate's authoritative specification is now
`rosa-seaplane` PR #1012** (`docs/design/aws-preflight-design.md`, "Seaplane
Installer Role trust-policy gate" catalog entry + the Customer AWS account
access section) — mandatory `Block` check, entire-policy inspection, reject
wildcard/alternate-principal/unconditional allows, `GetRole` via the assumed
installer-role session. This doc keeps the CLI-side task and the open
**principal divergence** (below).

**Depends on**: Phase 0 (task 0.3 — the ExternalId value model)
**Blocks**: Phase 2 (deferred to PR #1012)
**Duration estimate**: small–medium

## Divergence to settle (D-T7 vs PR #1012)

Our D-T7 decision has trust policies trust the **jump-account root**;
PR #1012 specifies the gate must prove the trust policy permits **only the
operator's Pod Identity role** (per-region principal, no jump
account). These are incompatible predicates — align with gdbranco before
implementing the gate. See `phase-2-preflight-controller.md` divergence 1.

## Goal

Implement the design's one new hard customer-facing requirement: Seaplane
refuses to provision clusters unless the installer-role trust policy requires
the service-owned `sts:ExternalId` on **every** allow that permits the
installer identity to assume the role.

## Tasks

### 1.1 CLI: emit the `ExternalId` condition
- [ ] `rosa` repo: add the `sts:ExternalId` condition, set to the
      service-owned value, to the **installer-role** trust-policy template
      on the statement that lets the installer identity assume the role.
      Scope is installer-role only (V1 also injects into the support role —
      explicitly out of scope for this PoC).
- [ ] Template source per Phase 0 T9: V1 trust-policy templates are served
      by OCM (`GetPolicyDetails`/`cmv1.AWSSTSPolicy`); V2 must decide the
      serving home (seaplane-api endpoint vs CLI-shipped assets) before
      the template can carry the condition.
- [ ] Scope per Phase 0 T8: V1 injects ExternalId into installer **and
      support** trust policies (`RequiresSTSExternalIDInTrustPolicy`) —
      include the support role if the T8 decision keeps it.
- [ ] Template source per Phase 0 T9: V1 trust-policy templates are served
      by OCM (`GetPolicyDetails`/`cmv1.AWSSTSPolicy`); V2 must decide the
      serving home (seaplane-api endpoint vs CLI-shipped assets) before
      the template can carry the condition.

### 1.2 Operator: implement the trust-policy gate
- [ ] New check in `seaplane-operator` (runs as part of preflight/provisioning
      gating): fetch the trust policy and inspect it **in its entirety**, not
      just the CLI-managed statement.
- [ ] **Define the installer identity** (Phase 0 T7 outcome): the principal
      whose assumption the gate protects — the V1 jump account
      (`arn:aws:iam::<jump>:root`, per `rosa` CLI `JumpAccounts`) if reused,
      or the per-region service identities if that model is chosen.
- [ ] Pass only if every allow statement permitting assumption of the role by
      the installer identity requires the service-owned `ExternalId` value.
- [ ] Fail on: wildcard condition values, the correct value appearing on an
      unrelated statement, any unconditional allow, separate allows without
      the condition.
- [ ] Surface the result (pass / fail + reason) as a cluster **Condition**.

### 1.3 Tests
- [ ] Fixture-based unit tests covering at minimum:
  - [ ] correct value on the right statement → pass
  - [ ] wildcard `sts:ExternalId` condition → fail
  - [ ] correct value on a non-assumption statement → fail
  - [ ] unconditional separate allow statement → fail
  - [ ] no condition at all → fail
- [ ] Integration test against a real IAM role with a misconfigured policy.

### 1.4 Documentation
- [ ] Public-facing doc: the `ExternalId` requirement, why the service owns
      the value (confused-deputy mitigation, uniqueness), and how customers
      update existing roles.
- [ ] Note the residual risk (customer edits after the check) explicitly.

## Exit criteria
- Gate enforced end to end for CLI-created clusters.
- Fixture tests and one live integration test green.
- Customer-facing documentation drafted.
