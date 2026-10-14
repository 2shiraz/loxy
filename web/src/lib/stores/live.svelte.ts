/**
 * One SSE connection to /api/v1/events. Each event bumps invalidation
 * counters; pages read `live.tick(key)` inside an $effect and refetch.
 * Keys: 'lbs', 'lb:<id>', 'certificates', 'domains', 'nodes', 'events'.
 */
import type { LoxyEvent } from '$lib/api/client';
import { toast } from './toast.svelte';

type ConnState = 'connecting' | 'open' | 'closed';

const ticks = $state<Record<string, number>>({});
export const live = $state<{ state: ConnState; last: LoxyEvent | null }>({ state: 'closed', last: null });

let es: EventSource | null = null;

const KINDS = [
  'lb.updated', 'lb.apply.started', 'lb.apply.succeeded', 'lb.apply.failed',
  'target.health.changed', 'service.health.changed', 'certificate.updated',
  'domain.status.changed', 'domain.sync.failed', 'action.updated', 'node.status.changed'
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
  const lb = ev?.lb_id ?? (ev?.data as { lb_id?: number } | undefined)?.lb_id;
  const keys = ['events'];
  if (kind.startsWith('lb.') || kind.startsWith('target.') || kind.startsWith('service.')) {
    keys.push('lbs');
    if (lb != null) keys.push(`lb:${lb}`);
  }
  if (kind.startsWith('certificate.')) keys.push('certificates', 'lbs');
  if (kind.startsWith('domain.')) keys.push('domains');
  if (kind.startsWith('node.') || kind === 'lb.apply.succeeded' || kind === 'lb.apply.failed') keys.push('nodes');
  bump(...keys);
  if (kind === 'lb.apply.failed') toast.error(ev?.message ?? 'Applying configuration failed.');
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
