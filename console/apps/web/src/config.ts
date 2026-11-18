/*
 * Runtime configuration for the console.
 *
 * Priority: window.__CONSOLE_CONFIG__ (served as /config.js from a
 * ConfigMap — deploy-time configuration, one image for all environments)
 * → import.meta.env VITE_* (local development) → error.
 *
 * None of these values are secrets: the gateway URL and issuer are public
 * endpoints, and the OIDC client is public (PKCE — no client secret).
 */

export interface ConsoleConfig {
  /** Custom-authorizer API Gateway base URL. */
  apiBaseUrl: string;
  /** OIDC issuer — the PoC Keycloak behind CloudFront. */
  oidcIssuer: string;
  /** Public OIDC client ID (PKCE). */
  oidcClientId: string;
}

declare global {
  interface Window {
    __CONSOLE_CONFIG__?: Partial<ConsoleConfig>;
  }
}

export function loadConfig(): ConsoleConfig {
  const runtime = window.__CONSOLE_CONFIG__ ?? {};
  const config: ConsoleConfig = {
    apiBaseUrl: runtime.apiBaseUrl ?? (import.meta.env.VITE_API_BASE_URL as string | undefined) ?? '',
    oidcIssuer: runtime.oidcIssuer ?? (import.meta.env.VITE_OIDC_ISSUER as string | undefined) ?? '',
    oidcClientId: runtime.oidcClientId ?? (import.meta.env.VITE_OIDC_CLIENT_ID as string | undefined) ?? '',
  };
  if (!config.apiBaseUrl || !config.oidcIssuer || !config.oidcClientId) {
    throw new Error(
      'Console configuration is missing: serve /config.js setting window.__CONSOLE_CONFIG__ ' +
        '{apiBaseUrl, oidcIssuer, oidcClientId}, or set the VITE_* variables for local development.',
    );
  }
  return config;
}
