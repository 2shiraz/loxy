import type { Labels } from '$lib/api/client';

const IPV4 = /^(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)(\.(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)){3}$/;

export function isIPv4(s: string) { return IPV4.test(s); }
export function isIPv6(s: string) {
  if (!s.includes(':') || /[^0-9a-fA-F:.]/.test(s)) return false;
  const dbl = s.split('::').length - 1;
  if (dbl > 1) return false;
  const parts = s.split(':').filter((p) => p !== '');
  return parts.every((p) => p.length <= 4 || isIPv4(p)) && (dbl === 1 ? parts.length < 8 : parts.length === 8 || (parts.length === 7 && isIPv4(parts[6])));
}

/** Validate a target IP per §8.6: v4/v6, no loopback/link-local/multicast. Returns error or ''. */
export function targetIpError(s: string): string {
  if (isIPv4(s)) {
    const [a, b] = s.split('.').map(Number);
    if (a === 127) return 'loopback addresses are not allowed';
    if (a === 169 && b === 254) return 'link-local addresses are not allowed';
    if (a >= 224 && a <= 239) return 'multicast addresses are not allowed';
    if (a === 0) return 'invalid address';
    return '';
  }
  if (isIPv6(s)) {
    const l = s.toLowerCase();
    if (l === '::1') return 'loopback addresses are not allowed';
    if (l.startsWith('fe8') || l.startsWith('fe9') || l.startsWith('fea') || l.startsWith('feb')) return 'link-local addresses are not allowed';
    if (l.startsWith('ff')) return 'multicast addresses are not allowed';
    return '';
  }
  return 'not a valid IPv4 or IPv6 address';
}

export function portError(v: unknown): string {
  const n = Number(v);
  if (!Number.isInteger(n) || n < 1 || n > 65535) return 'Port must be 1–65535';
  return '';
}

const LABEL_KEY = /^([a-z0-9]([-a-z0-9.]*[a-z0-9])?\/)?[A-Za-z0-9]([-A-Za-z0-9_.]{0,61}[A-Za-z0-9])?$/;
const LABEL_VAL = /^([A-Za-z0-9]([-A-Za-z0-9_.]{0,61}[A-Za-z0-9])?)?$/;
export function labelError(k: string, v: string): string {
  if (!LABEL_KEY.test(k)) return `Invalid label key “${k}”`;
  if (!LABEL_VAL.test(v)) return `Invalid label value “${v}”`;
  return '';
}

const HOST = /^(\*\.)?([a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$/i;
export function fqdnError(s: string): string {
  if (!s) return 'Required';
  if (s.length > 253 || !HOST.test(s)) return 'Enter a valid domain name like app.example.com';
  return '';
}

export function parseLabels(text: string): { labels: Labels; error: string } {
  const labels: Labels = {};
  for (const raw of text.split(/[\n,]/)) {
    const part = raw.trim();
    if (!part) continue;
    const [k, ...rest] = part.split('=');
    const v = rest.join('=').trim();
    const e = labelError(k.trim(), v);
    if (e) return { labels, error: e };
    labels[k.trim()] = v;
  }
  return { labels, error: '' };
}

export function labelsToText(l: Labels | undefined): string {
  return Object.entries(l ?? {}).map(([k, v]) => `${k}=${v}`).join(', ');
}

/** Very small client-side label-selector matcher (k=v, k!=v, k in (a,b), k, !k). */
export function matchSelector(labels: Labels, selector: string): boolean {
  const s = selector.trim();
  if (!s) return true;
  const parts = s.match(/[^,(]+(\([^)]*\))?/g) ?? [];
  return parts.every((p0) => {
    const p = p0.trim();
    let m: RegExpMatchArray | null;
    if ((m = p.match(/^(\S+)\s+(notin|in)\s*\(([^)]*)\)$/))) {
      const set = m[3].split(',').map((x) => x.trim());
      const has = set.includes(labels[m[1]] ?? '\u0000');
      return m[2] === 'in' ? has : !has;
    }
    if ((m = p.match(/^([^!=]+)!=(.*)$/))) return labels[m[1].trim()] !== m[2].trim();
    if ((m = p.match(/^([^=]+)==?(.*)$/))) return labels[m[1].trim()] === m[2].trim();
    if (p.startsWith('!')) return !(p.slice(1) in labels);
    return p in labels;
  });
}
