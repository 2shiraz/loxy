/**
 * One SSE connection to /api/v1/events. Each event bumps invalidation
 * counters; pages read `tick(key)` inside resource() keys and refetch.
 * Keys: 'frontends', 'fe:<id>', 'backends', 'be:<id>', 'hosts',
 * 'certificates', 'domains', 'nodes', 'events', 'overview'.
 * Refetching is deliberately broad: any resource event invalidates the
 * frontend/backend lists and the overview.
 */
import type { LoxyEvent } from '$lib/api/client';
import { toast } from './toast.svelte';

type ConnState = 'connecting' | 'open' | 'closed';

const ticks = $state<Record<string, number>>({});
export const live = $state<{ state: ConnState; last: LoxyEvent | null }>({ state: 'closed', last: null });

let es: EventSource | null = null;

const KINDS = [
  'config.updated', 'config.applied', 'config.rollback',
  'apply.started', 'apply.succeeded', 'apply.failed',
  'server.health.changed', 'certificate.updated',
  'domain.status.changed', 'domain.sync.failed', 'notification.test',
  // tolerated aliases from earlier builds
  'frontend.updated', 'backend.health.changed', 'target.health.changed', 'node.status.changed'
]

export function bump(...keys: string[]) {
  for (const k of keys) ticks[k] = (ticks[k] ?? 0) + 1;
}

/** Reactive read of an invalidation counter. */
export function tick(key: string): number {
  return ticks[key] ?? 0;
}

function handle(kind: string, raw: string) {
  let ev: LoxyEvent | null = null;
  try { ev = JSON.parse(raw) as LoxyEvent; } catch { /* non-JSON payload */ }
  if (ev) live.last = ev;
  const d = (ev?.data ?? {}) as { frontend_id?: number; backend_id?: number };
  const fe = ev?.frontend_id ?? d.frontend_id;
  const be = ev?.backend_id ?? d.backend_id;
  const keys = ['events', 'overview', 'frontends', 'backends'];
  if (fe != null) keys.push(`fe:${fe}`);
  if (be != null) keys.push(`be:${be}`);
  if (kind.startsWith('apply.') || kind.startsWith('config.') || kind.endsWith('.health.changed')) {
    // Apply/health results can touch any frontend or backend page.
    keys.push('fe:*', 'be:*', 'nodes');
  }
  if (kind.startsWith('certificate.')) keys.push('certificates');
  if (kind.startsWith('domain.')) keys.push('domains');
  if (kind.startsWith('node.')) keys.push('nodes');
  bump(...keys);
  if (kind === 'apply.failed') toast.error(ev?.message ?? 'Applying the configuration failed.');
  if (kind === 'domain.sync.failed') toast.error(ev?.message ?? 'DNS sync failed.');
}

export function connectLive() {
  if (es || typeof EventSource === 'undefined') return;
  live.state = 'connecting';
  es = new EventSource('/api/v1/events', { withCredentials: true });
  es.onopen = () => { live.state = 'open'; };
  es.onerror = () => { live.state = es?.readyState === EventSource.CLOSED ? 'closed' : 'connecting'; };
  for (const k of KINDS) es.addEventListener(k, (e) => handle(k, (e as MessageEvent).data));
  es.onmessage = (e) => handle('message', e.data);
}

export function disconnectLive() {
  es?.close();
  es = null;
  live.state = 'closed';
}
