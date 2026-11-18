# Phase 5 — Synchronous Discovery API

**Status**: planned
**Depends on**: Phase 4 (UI auth path — for the UI callers), Phase 0 T1
(header contract), Phase 0 T5 (policy audit — the discovery read set)
**Blocks**: Phase 6
**Duration estimate**: medium

## Goal

Serve synchronous customer-account discovery reads (VPCs, subnets, and the
rest of the UI form-population set) from the Platform API, executed via
AssumeRole into the caller's account using the installer role — stateless
per request, available to both UI (RH Token + selected ARN) and CLI
(SigV4) callers.

### The API assumes the role directly (component boundary)

The **Platform API performs the cross-account AssumeRole itself**; it does
not proxy discovery through `seaplane-operator`. The design's split is by
timing: synchronous discovery belongs to the request/response component
(the API), while preflight and the OIDC key fetch — both asynchronous —
belong to the operator's reconcilers. Routing discovery through the
operator would require inventing an RPC/queue layer over seaplane-db just
to proxy `Describe*` calls, adding latency and a failure surface to the
UI's most latency-sensitive path — while both components need the same
ExternalId-aware AssumeRole machinery regardless, so nothing is
centralized by the indirection. The cost accepted here is a small
duplication of the assume-role helper across the two Go modules (P5.2);
if that becomes a maintenance burden, dedup via a shared library is a
refactor, not an architecture change.

## Code review findings (what exists today)

- **F1 — No discovery endpoints and no AWS account-facing clients in
  `seaplane-api`.** The handlers are per-resource CRUD over seaplane-db
  (`pkg/handlers/`), and `go.mod` carries only the AWS SDK core +
  DynamoDB. There is **no STS or EC2 service client** — the cross-account
  AssumeRole machinery (including the ExternalId-aware variant from Phase 2
  P2.2) lives in `seaplane-operator`'s `internal/oidc` and is a different
  Go module. Phase 5 needs an equivalent (shared package or ported helper)
  in the API module: net-new `sts` + `ec2` (+ `iam`/`route53` as the read
  set demands) deps.
- **F2 — Cedar actions are an enumerated, per-resource-kind registry.**
  `pkg/authz/actions.go` maps every action to a resource kind
  (`actionResourceKind`), `model.go` enumerates the `Action` constants,
  and Cedar UIDs are built from collection names. Discovery needs new
  action entries and a decision on the resource kind: discovery targets
  *account-scoped external state*, not a stored Seaplane resource — the
  closest existing pattern is a list-style, account-scoped action.
  The shipped Cedar schema and tests must be updated together
  (per `docs/authz/design.md`'s obligations).
- **F3 — No stored state to authorize against; identity comes from the
  request context.** Discovery runs *before* any Cluster exists, so the
  ADR'd Cluster `spec.installerRoleArn` (T2) does not help here. The
  installer-role ARN must be resolved **at request time** from the
  caller's identity — this is a distinct decision from T2 (see D1 below).
- **F4 — Rate limiting and error plumbing already exist.**
  `pkg/ratelimit/` provides config-driven local rate limiting with
  middleware; `pkg/handlers/errorcodes.go` defines the API error-code
  registry (e.g. `OIDCCONFIGS-MGMT-CREATE-005`) — discovery errors
  (throttled AWS call, unassumable role) need new codes there.
- **F5 — The backend must not special-case UI vs CLI.** Thanks to Phase 4's
  header parity, the discovery handler reads identity from the same
  context (`middleware.GetAccountID` / `GetCallerARN`); only the
  installer-role resolution input differs (selected ARN vs SigV4 identity
  — both arrive as the caller identity).

## Design decisions

### D1 — Resolving the installer role for a pre-cluster request (blocking decision)

Discovery happens before a Cluster exists, so the role ARN can't come from
Cluster state. Options:

- **(a) Account registration/linking record** *(recommended)*: the record
  that already gates admission (`RequireIdentity` → `isAccountRegistered`)
  gains the account's installer-role ARN, written at registration/link
  time. One source of truth per account; consistent with the T2 ADR's
  framing ("the linking design resolves the value the API writes onto the
  Cluster").
- (b) Request parameter: the UI/CLI supplies the role ARN, authorized
  against the linking record. More flexible (multiple installer roles per
  account) but adds a validation surface.
- (c) Naming-convention derivation (`<prefix>-Installer-Role`): rejected in
  the T2 ADR for the same reason here — conventions are not contracts.

Decision recorded with authz owners; (a) is the default.

### D2 — Endpoint shape

- `GET /api/v0/discovery/<resource>` (e.g. `vpcs`, `subnets`,
  `availability-zones`, plus the T5-audited set), account-scoped by the
  caller identity — no account ID in the path (matching the existing
  namespace-from-identity convention).
- Synchronous, stateless: AssumeRole + `Describe*` per request, no
  persistence. Optional per-session response caching only if latency
  numbers demand it (measure first, per the source design's performance
  note).

### D3 — AWS access from the API

- An `assumeRole` helper in the API module mirroring Phase 2 P2.2:
  ExternalId-aware (Phase 0 T3 value), credential cache keyed on
  `(roleARN, externalID)` with a short cap so burst UI traffic doesn't
  mint a credential per request.
- Regional AWS config from the API's existing `config` package; per-request
  timeouts; AWS throttling errors mapped to `429`-class API errors with a
  distinct error code so the UI can retry.

## Actionable tasks

### P5.1 — Installer-role resolution decision (D1) — blocking
- [ ] Confirm option (a) with authz owners; define where the
      installer-role ARN lives on the registration/linking record and how
      it is written (linking flow, Phase 4 P4.1).
- [ ] Record the outcome here and in the T2 ADR (cross-reference).

### P5.2 — AWS client layer in `seaplane-api` (can start now; credential chain per Phase 0 T7)
- [ ] Add `sts` + `ec2` (+ others per the T5 read set) to
      `seaplane-api/go.mod`.
- [ ] Implement the ExternalId-aware assume-role helper with a bounded
      credential cache; unit tests with stubbed STS.
- [ ] Wire the assuming identity per the T7 decision (V1 jump account:
      the API first assumes the jump-account role, then the customer's
      installer role; or direct regional identity if that model wins).
- [ ] Timeouts and retry policy for the Describe\* calls; map AWS errors
      to API errors.

### P5.3 — Cedar + actions for discovery
- [ ] Add discovery actions to `pkg/authz/actions.go` / `model.go`
      (account-scoped, list-style); decide the resource-kind mapping.
- [ ] Update the shipped Cedar schema and tests together; add example
      policies for `Discovery`-style grants.
- [ ] Handler-level authz check wired like existing resource handlers.

### P5.4 — Discovery handlers and routes
- [ ] New `pkg/handlers/discovery.go` implementing the D2 endpoint shape
      for the audited read set; register routes in the server setup.
- [ ] Per-resource response shaping (paginate AWS results; return the
      fields the UI forms need, not raw AWS payloads).
- [ ] Error codes in `errorcodes.go` for: role not assumable
      (distinguish trust-policy/ExternalId failures — surface the Phase 1
      gate's message), AWS throttling, region mismatch.
- [ ] Rate limiting config for discovery routes (they are the highest-QPS
      cross-account path; protect both the API and the customer account).

### P5.5 — Tests and latency
- [ ] Unit tests with stubbed EC2/STS clients per resource.
- [ ] Integration tests on both auth paths: SigV4 (CLI) and RH-Token +
      selected ARN (Phase 4 gateway) — asserting identical behavior
      (header-parity verification).
- [ ] Record `AssumeRole + Describe*` latency benchmarks; decide on
      per-session caching with data.

### P5.6 — Documentation
- [ ] Document the discovery endpoint set, its permission requirements
      (which installer-policy permissions it exercises — feeds the Phase 6
      audit instrumentation), and the error semantics for the UI.

## Exit criteria
- UI form population works end to end over the Phase 4 gateway with live
  customer-account data, and the same endpoints serve CLI callers via
  SigV4 with no behavioral difference.
- Installer-role resolution decided and consistent with the T2 ADR.
- Latency benchmarks recorded; caching decision made with data.

## Sequencing

```
Phase 0 T5 (read set) ──> P5.3/P5.4 scope
Phase 0 T3 (ExternalId) + P2.2 pattern ──> P5.2 (start now)
D1/P5.1 (decision) ──> P5.4 (handlers need role resolution)
Phase 4 (gateway) ──> P5.5 UI-path integration tests
```
