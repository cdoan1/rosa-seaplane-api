# ADR: Service-owned `sts:ExternalId` value model

**Status**: draft — pending decision (Phase 0 task T3; design doc open question 2)
**Blocks**: Phase 1 (gate + CLI template), Phase 2/3 (P2.2 consumers)
**Related**: `docs/preflight-and-ui-login-flows.md` (ExternalId requirement
section), `phase-0-prerequisites.md` finding F3,
`adr-cluster-installer-role-reference.md`

## Context

V2 makes `sts:ExternalId` a **hard provisioning gate**: the installer-role
trust policy must require a service-owned ExternalId on every allow that
permits assumption (confused-deputy mitigation), and the service must
guarantee the value's uniqueness across tenants.

What exists today (from the `rosa` CLI review):

- `--external-id` is **user-supplied and opt-in**; injected only into
  installer/support trust policies
  (`ocm-common/accountroles.RequiresSTSExternalIDInTrustPolicy`).
- The CLI resolves an existing role's ExternalId by reading its trust
  policy (`ResolveAccountRoleTrustPolicyExternalID`), fails on ambiguity
  (multiple values), and preserves values across role rebuilds
  (`PreserveSTSExternalIDsInTrustPolicy`).
- V1's OCM-role trust already uses the **Red Hat org ID** as its
  ExternalId — an org-scoped value is a proven pattern in production.

Design constraints from the source design doc:

- The service guarantees uniqueness; a fully customer-chosen value that
  could collide across tenants is **not permitted**.
- Open question: fully service-generated value, or an org-scoped prefix
  with an optional customer-supplied suffix?
- Customer-pressure friction is expected (customers asking to supply their
  own value); strong documentation and possibly AWS engagement are needed.

## Options

### Option A — fully service-generated, per account-link *(recommended as the default)*

A random, opaque, high-entropy value (e.g. 32+ hex chars) minted by the
service when an account is registered/linked, stored with the linkage
record, and used for all installer-role operations against that account.

- Uniqueness: guaranteed by construction (collision probability
  negligible), no coordination needed.
- The CLI fetches it from the service (the create-account-roles flow calls
  the API to obtain the value); the operator reads it from the same store.
- Customer input: none. Simplest security story ("the service owns the
  value" is literally true).

Cons: none functionally; only the customer-relations friction the design
already accepts.

### Option B — org-scoped prefix + optional customer suffix

`<service-prefix>-<orgID>[-<customer-suffix>]`, e.g.
`rh-rosa-12345678-acme`.

- Uniqueness: the prefix guarantees no cross-tenant collision; the suffix
  only needs to be unique *within* the org, which the service can enforce
  at link time.
- Lets customers keep a memorable/audit-friendly value.

Cons: customer-supplied suffix needs validation rules (charset, length,
case — AWS ExternalId matching is exact and case-sensitive), enforcement
logic, documentation, and it invites the "can I set my own value?"
negotiation the design doc warns about. More surface for the same security
property.

### Option C — pure org ID (as V1's OCM role does)

ExternalId = Red Hat org ID.

- Proven, zero new minting infrastructure; every component already knows
  the org ID.

Cons: org ID is public-ish (appears in support cases, invoices), reducing
the confused-deputy value of the secret-ish ExternalId; and org-level
granularity means every account in the org shares one value — rotating or
revoking per-account is impossible.

## Recommendation

**Option A** (fully service-generated, per account-link) as the default,
with **Option B kept as a documented follow-up** if customer pressure
materializes: the prefix scheme can be introduced later without migrating
existing values (values never collide across schemes because formats
differ). Reject Option C for the reduced deputy-protection and blast
radius.

### Value definition (to be finalized on sign-off)

- Format: opaque, ≥128 bits of entropy, AWS-compatible charset
  (`[A-Za-z0-9_:=,.@-]`, length 2–1224; recommend staying ≤ 64 chars).
- Minted by: the Platform API at account registration/link time (the same
  moment the ADR'd Cluster `installerRoleArn` resolution will live).
- Stored: the account-link/registration record (Phase 0 T4's store; until
  it exists, the registration record the `RequireIdentity` /
  `isAccountRegistered` path already uses).
- Read by: the `rosa` CLI (account-role creation: inject into the
  installer/support trust-policy templates via the existing
  `BuildAccountRoleAssumeRolePolicy` injection path — swap the
  user-supplied `externalID` argument for the service-fetched value), and
  `seaplane-operator` (P2.2's `assumeRoleCredentials(roleARN, externalID)`
  + the Phase 1 gate).

### Migration / coexistence with today's `--external-id`

- Existing user-supplied values remain valid for roles that already have
  them **only if** they match the service-minted value — in general they
  won't, so the V2 gate will fail for them. Migration path: the CLI
  offers a "update installer-role trust policy" command that rewrites the
  trust policy to the service value (reuse
  `PreserveSTSExternalIDsInTrustPolicy`-style injection, replacing rather
  than preserving).
- The `--external-id` flag's long-term fate: deprecate in favor of the
  service value; decide whether it remains as a V1-only compatibility
  flag. Record in the public-facing requirement doc (Phase 1 deliverable).

## Consequences

- No customer input is accepted for the ExternalId; docs must say so
  plainly.
- The mint-and-store point must exist before Phase 1's CLI work can fetch
  the value — sequencing: land the value in the registration/link record
  (or a minimal interim store) first.
- Rotation: per-account value can be rotated by the service; requires a
  coordinated update of the customer's trust policy — document as an
  operator-runbook item, not a self-service feature, for the PoC.

## Decision

Pending sign-off. Record the outcome here, then update
`phase-1-externalid-gate.md` (value source) and `phase-2/3` P2.2/P3.3
(consumer wiring).
