/*
 * Platform API client for the Phase 4 custom-authorizer flow.
 *
 * Calls the custom-authorizer API Gateway directly (no BFF): the browser
 * sends `Authorization: Bearer <token>` (verified by the Lambda
 * authorizer) plus the selected ARN in `X-Seaplane-Arn` (Flow 2b). Flow 2a
 * (listing linkable ARNs) omits the ARN header. The gateway maps the
 * authorizer's resolution onto the same identity headers the AWS_IAM
 * gateway produces, so resource paths and response shapes are identical to
 * the CLI's.
 */

import { getToken } from './auth';
import { loadConfig } from './config';

export const SELECTED_ARN_HEADER = 'X-Seaplane-Arn';
const SELECTED_ARN_STORAGE_KEY = 'sc_selected_arn';

export interface LinkableArn {
  arn: string;
  accountId: string;
}

let selectedArn: string | undefined;

export function setSelectedArn(arn: string | undefined) {
  selectedArn = arn;
  if (arn) localStorage.setItem(SELECTED_ARN_STORAGE_KEY, arn);
  else localStorage.removeItem(SELECTED_ARN_STORAGE_KEY);
}

export function getSelectedArn(): string | undefined {
  if (selectedArn === undefined) {
    selectedArn = localStorage.getItem(SELECTED_ARN_STORAGE_KEY) ?? undefined;
  }
  return selectedArn;
}

const runtimeConfig = loadConfig();

function apiBase(): string {
  return runtimeConfig.apiBaseUrl.replace(/\/+$/, '');
}

/**
 * Fetch wrapper over the Platform API. Paths keep the legacy `/bff/v1/`
 * prefix used by the discarded BFF's call sites; they are mapped onto the
 * platform's `/api/v0/` routes. The bearer token comes from the OIDC
 * client; the selected ARN rides `X-Seaplane-Arn`.
 */
export async function api<T>(path: string, init?: RequestInit): Promise<T> {
  const resource = path.replace(/^\/bff\/v1\//, '/api/v0/');
  const headers = new Headers(init?.headers);
  headers.set('Authorization', `Bearer ${await getToken()}`);
  const arn = getSelectedArn();
  if (arn) headers.set(SELECTED_ARN_HEADER, arn);

  const response = await fetch(`${apiBase()}${resource}`, { ...init, headers });
  if (response.status === 204) return undefined as T;
  const body: unknown = await response.json().catch(() => undefined);
  if (!response.ok) {
    const error = body as { error?: { message?: string }; message?: string } | undefined;
    throw new Error(error?.error?.message || error?.message || `Request failed (${response.status}).`);
  }
  return body as T;
}

/** Flow 2a: the ARNs this user may operate as (token-only, no ARN header). */
export async function listLinkableArns(): Promise<LinkableArn[]> {
  const arn = getSelectedArn();
  try {
    setSelectedArn(undefined); // token-only request
    const result = await api<{ arns?: LinkableArn[] }>('/bff/v1/account_links');
    return result.arns ?? [];
  } finally {
    setSelectedArn(arn); // restore the selection
  }
}
