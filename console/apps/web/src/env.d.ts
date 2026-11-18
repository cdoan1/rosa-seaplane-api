/// <reference types="vite/client" />

interface ImportMetaEnv {
  /** Custom-authorizer API Gateway base URL (e.g. https://<api-id>.execute-api.<region>.amazonaws.com/api). */
  readonly VITE_API_BASE_URL: string;
  /** OIDC issuer — the PoC Keycloak behind CloudFront (e.g. https://<cloudfront-domain>/realms/poc). */
  readonly VITE_OIDC_ISSUER: string;
  /** Public OIDC client ID (PKCE; no client secret in the browser). */
  readonly VITE_OIDC_CLIENT_ID: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}
