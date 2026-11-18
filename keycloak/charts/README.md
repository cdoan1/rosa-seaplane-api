# keycloak

PoC Keycloak — the RH-token issuer for the Phase 4 custom-authorizer flow
(`docs/poc/preflight-ui-login/phase-4-ui-auth-path.md` in this repo).
Adapted from the day-o reference repo's `deploy/keycloak` manifests for the
RC's EKS + TargetGroupBinding pattern.

## What it deploys

- Keycloak (single replica, `start --optimized`, management-port health
  probes) + Service on 8080
- PostgreSQL (single replica, 1Gi PVC so realm config survives pod
  restarts) + Service
- `TargetGroupBinding` onto the Keycloak ALB target group created by the
  `custom-authorizer-gateway` terraform module (rosa-seaplane)

## Topology

```
browser --HTTPS--> CloudFront (default cert) --HTTP--> internet-facing ALB
  (ingress restricted to the CloudFront prefix list) --> Keycloak pods
```

`KC_HOSTNAME` is the CloudFront domain (`keycloak_url` terraform output) —
the token issuer URL. Changing it re-mints account links
(poc-decisions.md D-P4-token).

## Values that matter

| Value | Source |
|-------|--------|
| `keycloak.app.hostname` | terraform output `keycloak_url` (via ApplicationSet annotation) |
| `keycloak.targetGroup.arn` | terraform output `keycloak_target_group_arn` (via ApplicationSet annotation) |
| `keycloak.createSecret` | false by default — the environment supplies the `keycloak-secrets` Secret (KC_BOOTSTRAP_ADMIN_USERNAME/PASSWORD, POSTGRESQL_USER/PASSWORD) via its secret workflow; true renders PoC-only inline credentials |

## PoC scope

Single replica, HTTP CloudFront→ALB origin, default CloudFront certificate,
inline-secret option — all documented as PoC simplifications in
`docs/poc/preflight-ui-login/poc-decisions.md` (D-P4-token). The authorizer
and platform API consume only the issuer/JWKS URLs, so swapping to Red Hat
SSO later is a configuration change.

## Data persistence note (security)

The PostgreSQL PVC persists realm config, **user password hashes** (Keycloak
PBKDF2-SHA512), **client secrets**, session records, and user-profile data
(PII). This is deliberate — day-o's `emptyDir` wipes the realm on every pod
restart, which is worse for a working PoC. Rules that keep it safe:

- **Test users only.** The production issuer is Red Hat SSO; this Keycloak
  deployment (PVC included) is **deleted at PoC teardown, never promoted**
  with real users in it.
- **No AWS linkage in Keycloak.** The token→ARN mapping lives in the
  DynamoDB linking table, not in Keycloak attributes — compromising this DB
  grants no account access.
- **At-rest encryption**: the PVC relies on the RC account's EBS
  encryption-by-default (verify with `aws ec2 get-ebs-encryption-by-default`);
  if not enabled, set `keycloak.postgres.storage.className` to an encrypted
  storage class.
- Bootstrap admin credentials come from the `keycloak-secrets` k8s Secret,
  never the DB.
- Keep realm token lifetimes short (P4.0.2) — caps the blast radius of any
  persisted session data.
