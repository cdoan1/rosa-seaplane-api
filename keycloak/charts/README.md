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
