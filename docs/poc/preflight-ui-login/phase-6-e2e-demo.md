# Phase 6 — End-to-End Hardening and Demo

**Status**: not started
**Depends on**: Phases 1–5
**Duration estimate**: small–medium

## Goal

Prove the full design end to end, harden the new surface, and set up the
audit instrumentation that feeds the future reduced-policy decision.

## Tasks

### 6.1 End-to-end demos
- [ ] CLI: create cluster → accepted → preflight fails (e.g. quota) →
      Condition surfaces reason → remediate → resync → pass → provision.
- [ ] UI: login → ARN selector (Flow 2a) → discovery (Flow 2b) → create →
      preflight Conditions visible.
- [ ] Unmanaged OIDC: register config → provisioning fetches and validates
      the key → cluster provisions with the MC Secret materialized.

### 6.2 Observability verification
- [ ] Confirm every AssumeRole and customer-account call appears in
      CloudTrail in **both** the Red Hat and customer accounts.
- [ ] Authorizer and custom-gateway request/authorization logs flowing to
      the standard observability stack.

### 6.3 Security review
- [ ] Formal review of the Lambda authorizer (public endpoint, trust
      boundary, cache staleness).
- [ ] Review the OIDC key-handling guarantees (no leak in logs/errors).
- [ ] Confirm the ExternalId gate behavior against adversarial trust
      policies.

### 6.4 Audit instrumentation (feeds open question 1)
- [ ] Instrument which installer-role permissions are actually exercised in
      V2 (from CloudTrail / call logs), so a later audit can decide whether
      a reduced-permission V2 policy is worth publishing.

### 6.5 Operability
- [ ] Runbooks for the new components (custom gateway, authorizer, linking
      store, preflight controller).
- [ ] Load/concurrency check on the network verifier bounds and discovery
      path.

## Exit criteria
- Both demo flows recorded.
- Security review findings addressed or tracked.
- Audit instrumentation live.
