/**
 * One SSE connection to /api/v1/events. Each event bumps invalidation
 * counters; pages read `tick(key)` inside resource() keys and refetch.
 * Keys: 'projects', 'p:<id>', 'p:*', 'hosts', 'certificates', 'domains',
 * 'nodes', 'events', 'overview'.
 * Refetching is deliberately broad: any resource event invalidates the
 * project list and the overview.
 */
import type { LoxyEvent } from '$lib/api/client';
import { toast } from './toast.svelte';

type ConnState = 'connecting' | 'open' | 'closed';

const ticks = $state<Record<string, number>>({});
export const live = $state<{ state: ConnState; last: LoxyEvent | null }>({ state: 'closed', last: null });

let es: EventSource | null = null;

export const EVENT_KINDS = [
  'config.updated', 'config.applied', 'config.rollback',
  'apply.started', 'apply.succeeded', 'apply.failed',
  'target.health.changed', 'certificate.updated',
  'domain.status.changed', 'domain.sync.failed', 'notification.test', 'backup.failed'
];

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
  const pid = ev?.project_id ?? (ev?.data as { project_id?: number } | undefined)?.project_id;
  const keys = ['events', 'overview', 'projects'];
  if (pid != null) keys.push(`p:${pid}`);
  if (kind.startsWith('apply.') || kind.startsWith('config.') || kind.endsWith('.health.changed')) {
    // Apply/health results can touch any project page.
    keys.push('p:*', 'nodes', 'hosts');
  }
  if (kind.startsWith('certificate.')) keys.push('certificates', 'domains', 'p:*');
  if (kind.startsWith('domain.')) keys.push('domains');
  bump(...keys);
  if (kind === 'apply.failed') toast.error(ev?.message ?? 'Applying the configuration failed.');
  if (kind === 'domain.sync.failed') toast.error(ev?.message ?? 'DNS sync failed.');
  if (kind === 'backup.failed') toast.error(ev?.message ?? 'The configuration backup failed.');
}

export function connectLive() {
  if (es || typeof EventSource === 'undefined') return;
  live.state = 'connecting';
  es = new EventSource('/api/v1/events', { withCredentials: true });
  es.onopen = () => { live.state = 'open'; };
  es.onerror = () => { live.state = es?.readyState === EventSource.CLOSED ? 'closed' : 'connecting'; };
  for (const k of EVENT_KINDS) es.addEventListener(k, (e) => handle(k, (e as MessageEvent).data));
  es.onmessage = (e) => handle('message', e.data);
}

export function disconnectLive() {
  es?.close();
  es = null;
  live.state = 'closed';
}
