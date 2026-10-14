import createClient, { type Middleware } from 'openapi-fetch';
import { goto } from '$app/navigation';
import type { paths, components } from './schema';
import { toast } from '$lib/stores/toast.svelte';

export type Schemas = components['schemas'];
export type LoadBalancer = Schemas['LoadBalancer'];
export type Plan = Schemas['Plan'];
export type Service = Schemas['Service'];
export type ServiceInput = Schemas['ServiceInput'];
export type Target = Schemas['Target'];
export type TargetInput = Schemas['TargetInput'];
export type Certificate = Schemas['Certificate'];
export type Domain = Schemas['Domain'];
export type DNSProvider = Schemas['DNSProvider'];
export type DNSProviderKind = Schemas['DNSProviderKind'];
export type DNSRecord = Schemas['DNSRecord'];
export type Server = Schemas['Server'];
export type Network = Schemas['Network'];
export type Node = Schemas['Node'];
export type Revision = Schemas['Revision'];
export type HealthTree = Schemas['HealthTree'];
export type HealthSummary = Schemas['HealthSummary'];
export type CheckStatus = Schemas['CheckStatus'];
export type Metrics = Schemas['Metrics'];
export type MetricsPoint = Schemas['MetricsPoint'];
export type LoxyEvent = Schemas['Event'];
export type AuditEntry = Schemas['AuditEntry'];
export type User = Schemas['User'];
export type Labels = Schemas['Labels'];
export type ApiErrorBody = Schemas['ErrorResponse'];

let csrf = '';
try { csrf = sessionStorage.getItem('loxy.csrf') ?? ''; } catch { /* storage unavailable */ }

export function setCsrf(token: string) {
  csrf = token;
  try { sessionStorage.setItem('loxy.csrf', token); } catch { /* ignore */ }
}
export function getCsrf() { return csrf; }

const authMiddleware: Middleware = {
  onRequest({ request }) {
    if (request.method !== 'GET' && request.method !== 'HEAD' && csrf) {
      request.headers.set('X-LOXY-CSRF', csrf);
    }
    return request;
  },
  onResponse({ response, request }) {
    if (response.status === 401 && !request.url.includes('/auth/login')) {
      const here = location.pathname + location.search;
      if (!location.pathname.startsWith('/login')) {
        void goto(`/login?next=${encodeURIComponent(here)}`, { replaceState: true });
      }
    }
    return response;
  }
};

export const api = createClient<paths>({ baseUrl: '/api/v1', credentials: 'same-origin' });
api.use(authMiddleware);

/** Normalised API error. */
export class ApiError extends Error {
  code: string;
  status: number;
  fields: Record<string, string>;
  constructor(status: number, body: unknown) {
    const e = (body as ApiErrorBody | undefined)?.error;
    super(e?.message ?? `Request failed (${status})`);
    this.status = status;
    this.code = e?.code ?? 'internal';
    this.fields = {};
    for (const f of e?.details?.fields ?? []) this.fields[f.name] = f.messages.join(', ');
  }
  get hasFields() { return Object.keys(this.fields).length > 0; }
}

type Result<T> = { data?: T; error?: unknown; response: Response };

/**
 * Unwrap an openapi-fetch result: returns data or throws ApiError.
 * Network failures are converted into ApiError(0).
 */
export async function unwrap<T>(p: Promise<Result<T>>): Promise<T> {
  let r: Result<T>;
  try {
    r = await p;
  } catch (e) {
    throw new ApiError(0, { error: { code: 'internal', message: 'Network error: the panel is unreachable.' } });
  }
  if (r.error !== undefined || !r.response.ok) throw new ApiError(r.response.status, r.error);
  return r.data as T;
}

/** Run a mutation; show non-field errors as a toast. Returns undefined on failure. */
export async function mutate<T>(p: Promise<Result<T>>, opts: { success?: string; onFieldErrors?: (f: Record<string, string>) => void } = {}): Promise<T | undefined> {
  try {
    const d = await unwrap(p);
    if (opts.success) toast.success(opts.success);
    return d ?? ({} as T);
  } catch (e) {
    const err = e as ApiError;
    if (err.status === 401) return undefined;
    if (err.hasFields && opts.onFieldErrors) {
      opts.onFieldErrors(err.fields);
      if (err.message) toast.error(err.message);
    } else {
      toast.error(err.message);
    }
    return undefined;
  }
}

/** Fetch plain text (config, diff). */
export async function getText(path: string): Promise<string> {
  const r = await fetch('/api/v1' + path, { credentials: 'same-origin' });
  if (r.status === 401) { void goto('/login'); throw new ApiError(401, undefined); }
  if (!r.ok) {
    let body: unknown;
    try { body = await r.json(); } catch { body = undefined; }
    throw new ApiError(r.status, body);
  }
  return r.text();
}
