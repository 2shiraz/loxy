import type { ServiceInput, Service, HealthCheck } from '$lib/api/client';

export type Protocol = ServiceInput['protocol'];

export const HC_DEFAULTS = { interval_s: 15, timeout_s: 10, retries: 3 };
export const SVC_DEFAULTS = { timeout_s: 60, cookie_name: 'LOXYSTICKY', cookie_lifetime_s: 300 };
/** Ports the panel itself or the shared domain listeners use. */
export const RESERVED_PORTS: Record<number, string> = {
  80: 'Port 80 is reserved for domains. Attach a domain instead.',
  443: 'Port 443 is reserved for domains. Attach a domain instead.',
  8405: 'Reserved system port',
  5514: 'Reserved system port'
};

function httpCheck(): NonNullable<HealthCheck['http']> {
  return { domain: null, path: '/', response: null, status_codes: ['2??', '3??'], tls_verify: false };
}

/** A fresh service. `listen` undefined = pick a free port automatically. */
export function newService(protocol: Protocol = 'http', dest = 80, listen?: number): ServiceInput {
  const isHttp = protocol !== 'tcp';
  return {
    protocol,
    ...(listen != null ? { listen_port: listen } : {}),
    destination_port: dest,
    proxy_protocol: 'none',
    timeout_s: SVC_DEFAULTS.timeout_s,
    certificates: [],
    sticky_sessions: false,
    cookie_name: SVC_DEFAULTS.cookie_name,
    cookie_lifetime_s: SVC_DEFAULTS.cookie_lifetime_s,
    health_check: { protocol: isHttp ? 'http' : 'tcp', port: null, ...HC_DEFAULTS, http: isHttp ? httpCheck() : null }
  };
}

export function toInput(s: Service): ServiceInput {
  return JSON.parse(JSON.stringify({
    protocol: s.protocol,
    listen_port: s.listen_port,
    destination_port: s.destination_port,
    proxy_protocol: s.proxy_protocol,
    timeout_s: s.timeout_s,
    certificates: s.certificates,
    sticky_sessions: s.sticky_sessions,
    cookie_name: s.cookie_name,
    cookie_lifetime_s: s.cookie_lifetime_s,
    health_check: s.health_check
  })) as ServiceInput;
}

/** Normalise a service input so sub-objects match the protocol. Strips an empty listen port (= auto). */
export function normalise(s: ServiceInput): ServiceInput {
  const out = JSON.parse(JSON.stringify(s)) as ServiceInput;
  const isHttp = out.protocol !== 'tcp';
  if ((out.listen_port as unknown) === '' || out.listen_port == null) delete out.listen_port;
  else out.listen_port = Number(out.listen_port);
  out.destination_port = Number(out.destination_port);
  if (out.protocol !== 'https') out.certificates = [];
  if (!isHttp) out.sticky_sessions = false;
  out.cookie_name ||= SVC_DEFAULTS.cookie_name;
  out.cookie_lifetime_s ??= SVC_DEFAULTS.cookie_lifetime_s;
  out.timeout_s ??= SVC_DEFAULTS.timeout_s;
  const hc = (out.health_check ??= { protocol: isHttp ? 'http' : 'tcp', port: null, ...HC_DEFAULTS });
  if ((hc.port as unknown) === '' || hc.port == null) hc.port = null;
  if (hc.protocol === 'tcp') hc.http = null;
  else hc.http = { ...httpCheck(), ...(hc.http ?? {}) };
  return out;
}

const okPort = (n: unknown) => Number.isInteger(Number(n)) && Number(n) >= 1 && Number(n) <= 65535;

/** Client-side mirror of the server validation. Returns editor field → message. */
export function validateService(s: ServiceInput, others: { listen_port: number }[] = []): Record<string, string> {
  const e: Record<string, string> = {};
  const auto = (s.listen_port as unknown) === '' || s.listen_port == null;
  if (!auto) {
    const lp = Number(s.listen_port);
    if (!okPort(lp)) e.listen_port = 'Port must be 1–65535';
    else if (RESERVED_PORTS[lp]) e.listen_port = RESERVED_PORTS[lp];
    else if (others.some((o) => o.listen_port === lp)) e.listen_port = 'Another service of this project uses this port';
  }
  if (!okPort(s.destination_port)) e.destination_port = 'Port must be 1–65535';
  const to = Number(s.timeout_s ?? 60);
  if (!(to >= 1 && to <= 86400)) e.timeout_s = 'Timeout must be 1–86400 s';
  if (s.sticky_sessions) {
    if (!/^[A-Za-z0-9_-]{1,64}$/.test(s.cookie_name ?? '')) e.cookie_name = 'Letters, digits, - and _ only';
    if (!(Number(s.cookie_lifetime_s) >= 1)) e.cookie_lifetime_s = 'Must be at least 1 s';
  }
  const hc = s.health_check;
  if (hc) {
    const iv = Number(hc.interval_s), ht = Number(hc.timeout_s), rt = Number(hc.retries);
    if (!(iv >= 3 && iv <= 3600)) e.hc_interval = 'Interval must be 3–3600 s';
    if (!(ht >= 1 && ht <= 3600)) e.hc_timeout = 'Timeout must be 1–3600 s';
    else if (ht >= iv) e.hc_timeout = 'Timeout must be shorter than the interval';
    if (!(rt >= 1 && rt <= 10)) e.hc_retries = 'Retries must be 1–10';
    if (hc.port != null && (hc.port as unknown) !== '' && !okPort(hc.port)) e.hc_port = 'Port must be 1–65535';
    if (hc.protocol !== 'tcp' && !hc.http?.path?.startsWith('/')) e.hc_path = 'Path must start with /';
  }
  return e;
}

const HC_FIELD: Record<string, string> = {
  'health_check.interval_s': 'hc_interval',
  'health_check.timeout_s': 'hc_timeout',
  'health_check.retries': 'hc_retries',
  'health_check.port': 'hc_port',
  'health_check.http.path': 'hc_path',
  'health_check.http.status_codes': 'hc_codes',
  'health_check.protocol': 'hc_protocol'
};

/**
 * Map server field errors (`listen_port`, `service.listen_port`,
 * `health_check.http.path` …) onto the editor's keys. `prefix` strips a
 * leading path such as `service.`.
 */
export function serviceFieldErrors(fields: Record<string, string>, prefix = ''): Record<string, string> {
  const out: Record<string, string> = {};
  for (const [k0, msg] of Object.entries(fields)) {
    if (prefix && !k0.startsWith(prefix)) continue;
    const k = k0.slice(prefix.length);
    if (HC_FIELD[k]) out[HC_FIELD[k]] = msg;
    else if (k.startsWith('certificates')) out.certificates = msg;
    else if (k.startsWith('health_check')) out.hc_other = msg;
    else out[k] = msg;
  }
  return out;
}

export function protoLabel(p: string) {
  return p.toUpperCase();
}

export function serviceTitle(s: { protocol: string; listen_port?: number; destination_port: number }) {
  return `${protoLabel(s.protocol)} :${s.listen_port ?? 'auto'} → :${s.destination_port}`;
}

export function hcSummary(s: ServiceInput | Service): string {
  const hc = s.health_check;
  if (!hc) return '';
  const port = hc.port ?? 'target port';
  const parts = [`${(hc.protocol ?? 'tcp').toUpperCase()} :${port}`, `every ${hc.interval_s ?? 15} s`, `timeout ${hc.timeout_s ?? 10} s`, `${hc.retries ?? 3} retries`];
  if (hc.protocol !== 'tcp' && hc.http) {
    parts.push(`${hc.http.domain ? hc.http.domain : ''}${hc.http.path || '/'}`);
    if (hc.http.status_codes?.length) parts.push(`expects ${hc.http.status_codes.join(', ')}`);
    if (hc.http.response) parts.push(`body contains “${hc.http.response}”`);
  }
  return parts.join(' · ');
}
