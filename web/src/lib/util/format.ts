const nf = new Intl.NumberFormat(undefined, { maximumFractionDigits: 1 });
const nf0 = new Intl.NumberFormat(undefined, { maximumFractionDigits: 0 });

export function num(n: number | null | undefined, digits = 0): string {
  if (n == null || Number.isNaN(n)) return '—';
  return digits ? n.toFixed(digits) : nf0.format(n);
}

export function compact(n: number | null | undefined): string {
  if (n == null) return '—';
  const a = Math.abs(n);
  if (a >= 1e9) return nf.format(n / 1e9) + 'B';
  if (a >= 1e6) return nf.format(n / 1e6) + 'M';
  if (a >= 1e4) return nf.format(n / 1e3) + 'k';
  return nf.format(n);
}

const UNITS = ['B', 'KB', 'MB', 'GB', 'TB', 'PB'];
export function bytes(n: number | null | undefined, suffix = ''): string {
  if (n == null || Number.isNaN(n)) return '—';
  let i = 0;
  let v = Math.abs(n);
  while (v >= 1000 && i < UNITS.length - 1) { v /= 1000; i++; }
  return (n < 0 ? '-' : '') + (v >= 100 || i === 0 ? nf0.format(v) : nf.format(v)) + ' ' + UNITS[i] + suffix;
}

export function duration(sec: number | null | undefined): string {
  if (sec == null) return '—';
  const s = Math.floor(sec);
  const d = Math.floor(s / 86400), h = Math.floor((s % 86400) / 3600), m = Math.floor((s % 3600) / 60);
  if (d) return `${d}d ${h}h`;
  if (h) return `${h}h ${m}m`;
  if (m) return `${m}m`;
  return `${s}s`;
}

// API timestamps are unix milliseconds (spec §6); metric series use unix seconds.
export function dateTime(ts: number | null | undefined): string {
  if (!ts) return '—';
  return new Date(ts).toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'short' });
}

export function relTime(ts: number | null | undefined): string {
  if (!ts) return '—';
  const diff = (ts - Date.now()) / 1000;
  const a = Math.abs(diff);
  const rtf = new Intl.RelativeTimeFormat(undefined, { numeric: 'auto' });
  if (a < 60) return rtf.format(Math.round(diff), 'second');
  if (a < 3600) return rtf.format(Math.round(diff / 60), 'minute');
  if (a < 86400) return rtf.format(Math.round(diff / 3600), 'hour');
  return rtf.format(Math.round(diff / 86400), 'day');
}

export function daysUntil(ts: number | null | undefined): number | null {
  if (!ts) return null;
  return Math.floor((ts - Date.now()) / 86_400_000);
}

export function plural(n: number, one: string, many = one + 's') {
  return `${n} ${n === 1 ? one : many}`;
}

export function algorithmLabel(a: string): string {
  return ({ roundrobin: 'Round Robin', leastconn: 'Least Connections', source: 'Source IP', random: 'Random' } as Record<string, string>)[a] ?? a;
}

/** Short label for a project endpoint: ":20001" for ports, the hostname for domains. */
export function endpointLabel(e: { kind: string; url: string }): string {
  const m = /^[a-z]+:\/\/(\[[^\]]+\]|[^/:]+)(?::(\d+))?/i.exec(e.url);
  if (!m) return e.url;
  if (e.kind === 'port') return m[2] ? `:${m[2]}` : e.url;
  return m[1];
}

/** Only http(s) endpoints can be opened in a browser. */
export function isWebUrl(u: string): boolean {
  return /^https?:\/\//i.test(u);
}

/** One status word for a project: apply problems first, then health. */
export function projectStatus(p: { status: string; health: { status: string } }): string {
  if (p.status === 'error') return 'apply_error';
  if (p.status === 'applying') return 'applying';
  return p.health.status;
}
