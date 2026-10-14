import type { ServiceInput, Service } from '$lib/api/client';

export const HC_DEFAULTS = { interval_s: 15, timeout_s: 10, retries: 3 };
export const HTTP_DEFAULTS = { timeout_s: 60, cookie_name: 'LBSTICKY', cookie_lifetime_s: 300 };

export function newService(protocol: 'tcp' | 'http' | 'https', listen: number, dest: number, redirect = false): ServiceInput {
  const isHttp = protocol !== 'tcp';
  return {
    protocol,
    listen_port: listen,
    destination_port: dest,
    proxy_protocol: 'none',
    http: isHttp
      ? { redirect_http: redirect, timeout_s: HTTP_DEFAULTS.timeout_s, certificates: [], sticky_sessions: false, cookie_name: HTTP_DEFAULTS.cookie_name, cookie_lifetime_s: HTTP_DEFAULTS.cookie_lifetime_s }
      : undefined,
    health_check: {
      protocol: isHttp ? 'http' : 'tcp',
      port: dest,
      ...HC_DEFAULTS,
      http: isHttp ? { domain: null, path: '/', response: null, status_codes: ['2??', '3??'], tls_verify: false } : null
    }
  };
}

export const TEMPLATES: { id: string; label: string; desc: string; make: () => ServiceInput }[] = [
  { id: 'http', label: 'HTTP', desc: 'http :80 → :80', make: () => newService('http', 80, 80) },
  { id: 'tls-term', label: 'TLS Termination', desc: 'https :443 → :80, redirect HTTP, certificate required', make: () => newService('https', 443, 80, true) },
  { id: 'tls-pass', label: 'TLS Passthrough', desc: 'tcp :443 → :443', make: () => newService('tcp', 443, 443) }
];

export function toInput(s: Service): ServiceInput {
  return structuredClone({
    protocol: s.protocol,
    listen_port: s.listen_port,
    destination_port: s.destination_port,
    proxy_protocol: s.proxy_protocol,
    http: s.http,
    health_check: s.health_check
  }) as ServiceInput;
}

/** Normalise a service input so http/hc sub-objects match the protocol. */
export function normalise(s: ServiceInput): ServiceInput {
  const out = structuredClone($stateSnapshotSafe(s));
  const isHttp = out.protocol !== 'tcp';
  if (isHttp) {
    out.http = { ...{ redirect_http: false, timeout_s: 60, certificates: [], sticky_sessions: false, cookie_name: 'LBSTICKY', cookie_lifetime_s: 300 }, ...(out.http ?? {}) };
    if (out.protocol !== 'https') { out.http.redirect_http = false; out.http.certificates = []; }
  } else {
    out.http = undefined;
  }
  const hc = (out.health_check ??= { protocol: isHttp ? 'http' : 'tcp', port: out.destination_port, ...HC_DEFAULTS });
  if (hc.protocol === 'tcp') hc.http = null;
  else hc.http = { domain: null, path: '/', response: null, status_codes: ['2??', '3??'], tls_verify: false, ...(hc.http ?? {}) };
  return out;
}

function $stateSnapshotSafe<T>(v: T): T {
  return JSON.parse(JSON.stringify(v)) as T;
}

/** Client-side mirror of the §8.7 validation. Returns field → message. */
export function validateService(s: ServiceInput, others: { listen_port: number }[] = []): Record<string, string> {
  const e: Record<string, string> = {};
  const port = (n: unknown) => Number.isInteger(Number(n)) && Number(n) >= 1 && Number(n) <= 65535;
  if (!port(s.listen_port)) e.listen_port = 'Port must be 1–65535';
  else if (others.some((o) => o.listen_port === Number(s.listen_port))) e.listen_port = 'Port already used by another service';
  else if ([8405, 5514].includes(Number(s.listen_port))) e.listen_port = 'Reserved system port';
  if (!port(s.destination_port)) e.destination_port = 'Port must be 1–65535';
  if (s.protocol === 'https' && !(s.http?.certificates?.length)) e.certificates = 'HTTPS needs at least one certificate';
  if (s.http?.redirect_http) {
    if (s.protocol !== 'https') e.redirect_http = 'Redirect is only available for HTTPS';
    else if (others.some((o) => o.listen_port === 80) || Number(s.listen_port) === 80) e.redirect_http = 'Port 80 is already in use on this load balancer';
  }
  if (s.http?.sticky_sessions && s.protocol === 'tcp') e.sticky = 'Sticky sessions need HTTP or HTTPS';
  const hc = s.health_check;
  if (hc) {
    const iv = Number(hc.interval_s), to = Number(hc.timeout_s), rt = Number(hc.retries);
    if (!(iv >= 3 && iv <= 3600)) e.hc_interval = 'Interval must be 3–3600 s';
    if (!(to >= 1 && to <= 3600)) e.hc_timeout = 'Timeout must be 1–3600 s';
    else if (to >= iv) e.hc_timeout = 'Timeout must be shorter than the interval';
    if (!(rt >= 1 && rt <= 10)) e.hc_retries = 'Retries must be 1–10';
    if (hc.port != null && !port(hc.port)) e.hc_port = 'Port must be 1–65535';
    if (hc.protocol !== 'tcp' && !hc.http?.path) e.hc_path = 'HTTP checks need a path';
  }
  return e;
}

export function serviceTitle(s: { protocol: string; listen_port: number; destination_port: number }) {
  return `${s.protocol.toUpperCase()} :${s.listen_port} → :${s.destination_port}`;
}

export function hcSummary(s: ServiceInput | Service): string {
  const hc = s.health_check;
  if (!hc) return '';
  const parts = [`${(hc.protocol ?? 'tcp').toUpperCase()} :${hc.port ?? s.destination_port}`, `Interval ${hc.interval_s ?? 15} s`, `Timeout ${hc.timeout_s ?? 10} s`, `Retries ${hc.retries ?? 3}`];
  if (hc.protocol !== 'tcp' && hc.http) {
    parts.push(`Domain ${hc.http.domain || '-'}`, `Path ${hc.http.path || '/'}`, `Response ${hc.http.response || '-'}`, `Status codes ${(hc.http.status_codes ?? []).join(', ') || '-'}`);
  }
  return parts.join(' | ');
}
