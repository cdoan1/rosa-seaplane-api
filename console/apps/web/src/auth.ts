/*
 * Browser OIDC client for the Phase 4 custom-authorizer flow.
 *
 * Authorization Code + PKCE against the PoC Keycloak (public client — no
 * client secret in the browser). The access token is sent to the
 * custom-authorizer API Gateway as `Authorization: Bearer`, which the
 * seaplane-authorizer Lambda verifies. This replaces the discarded BFF's
 * server-side OIDC session: there is no STS federation and no SigV4
 * custody in the browser.
 *
 * PoC scope: tokens live in sessionStorage (cleared per tab); token
 * lifetimes are expected to be short (the authorizer cache TTL is bounded
 * by them). Not a hardening target.
 */

const AUTH_STORAGE_KEY = 'sc_auth';

export interface AuthConfig {
  issuer: string; // e.g. https://<cloudfront-domain>/realms/poc
  clientId: string;
  redirectUri: string;
}

export interface AuthState {
  accessToken: string;
  refreshToken?: string | undefined;
  idToken?: string | undefined;
  expiresAt: number; // epoch ms
}

export interface ConsoleUser {
  subject: string;
  name?: string;
  email?: string;
}

interface OidcDiscovery {
  authorization_endpoint: string;
  token_endpoint: string;
  end_session_endpoint?: string;
}

function config(): AuthConfig {
  const issuer = import.meta.env.VITE_OIDC_ISSUER as string | undefined;
  const clientId = import.meta.env.VITE_OIDC_CLIENT_ID as string | undefined;
  if (!issuer || !clientId) {
    throw new Error('VITE_OIDC_ISSUER and VITE_OIDC_CLIENT_ID must be set at build time');
  }
  return { issuer: issuer.replace(/\/+$/, ''), clientId, redirectUri: window.location.origin };
}

let discovery: Promise<OidcDiscovery> | undefined;

function discover(): Promise<OidcDiscovery> {
  discovery ??= fetch(`${config().issuer}/.well-known/openid-configuration`)
    .then((r) => {
      if (!r.ok) throw new Error(`OIDC discovery failed (${r.status})`);
      return r.json() as Promise<OidcDiscovery>;
    });
  return discovery;
}

function readState(): AuthState | undefined {
  try {
    const raw = sessionStorage.getItem(AUTH_STORAGE_KEY);
    return raw ? (JSON.parse(raw) as AuthState) : undefined;
  } catch {
    return undefined;
  }
}

function writeState(state: AuthState | undefined) {
  if (state) sessionStorage.setItem(AUTH_STORAGE_KEY, JSON.stringify(state));
  else sessionStorage.removeItem(AUTH_STORAGE_KEY);
}

// --- PKCE helpers -----------------------------------------------------------

function base64UrlEncode(bytes: Uint8Array): string {
  let binary = '';
  for (const b of bytes) binary += String.fromCharCode(b);
  return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}

async function sha256(input: string): Promise<Uint8Array> {
  const digest = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(input));
  return new Uint8Array(digest);
}

function randomString(length = 64): string {
  const bytes = new Uint8Array(length);
  crypto.getRandomValues(bytes);
  return base64UrlEncode(bytes).slice(0, length);
}

// --- public API --------------------------------------------------------------

/** Begin the login redirect (Authorization Code + PKCE). */
export async function login(): Promise<void> {
  const verifier = randomString();
  const state = randomString(16);
  sessionStorage.setItem('sc_pkce', JSON.stringify({ verifier, state }));
  const disco = await discover();
  const challenge = base64UrlEncode(await sha256(verifier));
  const url = new URL(disco.authorization_endpoint);
  url.searchParams.set('client_id', config().clientId);
  url.searchParams.set('response_type', 'code');
  url.searchParams.set('scope', 'openid profile email');
  url.searchParams.set('redirect_uri', config().redirectUri);
  url.searchParams.set('code_challenge', challenge);
  url.searchParams.set('code_challenge_method', 'S256');
  url.searchParams.set('state', state);
  window.location.assign(url.toString());
}

/**
 * Complete the redirect callback if present. Returns true when a code was
 * exchanged (and history cleaned); false when there is nothing to do.
 * Throws on exchange failure.
 */
export async function completeLogin(): Promise<boolean> {
  const params = new URLSearchParams(window.location.search);
  const code = params.get('code');
  const state = params.get('state');
  if (!code) return false;

  const stored = sessionStorage.getItem('sc_pkce');
  sessionStorage.removeItem('sc_pkce');
  window.history.replaceState({}, '', window.location.pathname);

  if (!stored) throw new Error('Sign-in state was lost. Please try again.');
  const { verifier, state: expectedState } = JSON.parse(stored) as { verifier: string; state: string };
  if (state !== expectedState) throw new Error('Sign-in state mismatch. Please try again.');

  const disco = await discover();
  const body = new URLSearchParams({
    grant_type: 'authorization_code',
    client_id: config().clientId,
    code,
    redirect_uri: config().redirectUri,
    code_verifier: verifier,
  });
  const response = await fetch(disco.token_endpoint, {
    method: 'POST',
    headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
    body,
  });
  if (!response.ok) throw new Error(`Token exchange failed (${response.status}).`);
  writeState(await toAuthState(await response.json()));
  return true;
}

interface TokenResponse {
  access_token: string;
  refresh_token?: string;
  id_token?: string;
  expires_in: number;
}

async function toAuthState(token: TokenResponse): Promise<AuthState> {
  return {
    accessToken: token.access_token,
    refreshToken: token.refresh_token,
    idToken: token.id_token,
    expiresAt: Date.now() + token.expires_in * 1000 - 10_000, // 10s clock skew margin
  };
}

/** A valid access token, refreshing when expired. Throws when signed out. */
export async function getToken(): Promise<string> {
  const state = readState();
  if (!state) throw new Error('Not signed in.');
  if (Date.now() >= state.expiresAt) {
    if (!state.refreshToken) {
      writeState(undefined);
      throw new Error('Session expired. Please sign in again.');
    }
    const disco = await discover();
    const body = new URLSearchParams({
      grant_type: 'refresh_token',
      client_id: config().clientId,
      refresh_token: state.refreshToken,
    });
    const response = await fetch(disco.token_endpoint, {
      method: 'POST',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body,
    });
    if (!response.ok) {
      writeState(undefined);
      throw new Error('Session expired. Please sign in again.');
    }
    const next = await toAuthState(await response.json());
    writeState(next);
    return next.accessToken;
  }
  return state.accessToken;
}

/** The signed-in user, parsed from the ID token claims. */
export function currentUser(): ConsoleUser | undefined {
  const state = readState();
  const idToken = state?.idToken;
  if (!idToken) return undefined;
  try {
    const encodedPayload = idToken.split('.')[1];
    if (!encodedPayload) return undefined;
    const payload = JSON.parse(atob(encodedPayload.replace(/-/g, '+').replace(/_/g, '/')));
    return {
      subject: payload.sub,
      name: payload.name,
      email: payload.email,
    };
  } catch {
    return undefined;
  }
}

export function isSignedIn(): boolean {
  return readState() !== undefined;
}

/** RP-initiated logout: end the Keycloak SSO session, then return home. */
export async function logout(): Promise<void> {
  const state = readState();
  writeState(undefined);
  const disco = await discover();
  if (state?.idToken && disco.end_session_endpoint) {
    const url = new URL(disco.end_session_endpoint);
    url.searchParams.set('id_token_hint', state.idToken);
    url.searchParams.set('post_logout_redirect_uri', config().redirectUri);
    window.location.assign(url.toString());
    return;
  }
  window.location.assign('/');
}
