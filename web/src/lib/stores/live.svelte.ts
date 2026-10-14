/**
 * One SSE connection to /api/v1/events.
 *
 * 1. Resource events (apply.*, target.health.changed, domain.*, …) bump
 *    invalidation counters; pages read `tick(key)` inside resource() keys and
 *    refetch. Keys: 'projects', 'p:<id>', 'p:*', 'hosts', 'certificates',
 *    'domains', 'nodes', 'events', 'overview'.
 * 2. Every 2 s the server pushes `live.tick`: status, health and traffic for
 *    every project plus node apply versions. It is kept in `live.tick` and a
 *    short per-project req/s history so pages update in place without polling.
 *    A changed health_sig / version / status / maintenance bumps only that
 *    project's key, so e.g. the Targets tab refetches exactly when a check flips.
 */
import type { LoxyEvent, Project } from '$lib/api/client';
import { toast } from './toast.svelte';

type ConnState = 'connecting' | 'open' | 'closed';

export interface LiveStats {
  open_connections: number;
  requests_per_second: number;
  bytes_in_per_second: number;
  bytes_out_per_second: number;
  response_time_ms: number;
}
export interface LiveProject {
  id: number;
  status: string;
  maintenance: boolean;
  version: number;
  health: { status: string; healthy: number; total: number };
  health_sig: string;
  stats: LiveStats;
}
export interface LiveTick {
  ts: number;
  projects: LiveProject[];
  traffic: { open_connections: number; requests_per_second: number; bytes_in_per_second: number; bytes_out_per_second: number };
  node?: { status: string; applied_version: number; desired_version: number };
}

/** How many ticks of history to keep (2 s each → 3 minutes). */
export const HISTORY = 90;
/** No tick for this long while "open" means the stream is stalled. */
const STALE_MS = 7000;

const ticks = $state<Record<string, number>>({});
export const live = $state<{
  state: ConnState;
  stale: boolean;
  last: LoxyEvent | null;
  tick: LiveTick | null;
  /** Per-project req/s history, oldest first. */
  rps: Record<number, number[]>;
  /** Whole-node req/s history, oldest first. */
  trafficRps: number[];
}>({ state: 'closed', stale: false, last: null, tick: null, rps: {}, trafficRps: [] });

let es: EventSource | null = null;
let missed = false;
let staleTimer: ReturnType<typeof setTimeout> | undefined;
const sigs = new Map<number, string>();

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

/** Latest live snapshot of one project (reactive). */
export function liveProject(id: number): LiveProject | undefined {
  return live.tick?.projects.find((p) => p.id === id);
}

/** True while HAProxy has not caught up with the latest saved change. */
export function applying(): boolean {
  const n = live.tick?.node;
  return !!n && n.desired_version > n.applied_version;
}

/** Overlay the latest live values onto a fetched project. */
export function withLive<P extends Project>(p: P): P {
  const l = liveProject(p.id);
  if (!l) return p;
  return {
    ...p,
    status: l.status as P['status'],
    maintenance: l.maintenance,
    health: { ...p.health, status: l.health.status as P['health']['status'], healthy: l.health.healthy, total: l.health.total },
    stats: { ...p.stats, open_connections: l.stats.open_connections, requests_per_second: l.stats.requests_per_second, bytes_out_per_second: l.stats.bytes_out_per_second }
  };
}

function push(arr: number[] | undefined, v: number): number[] {
  const next = arr ? arr.slice(-(HISTORY - 1)) : [];
  next.push(v);
  return next;
}

function onTick(raw: string) {
  let t: LiveTick | undefined;
  try { t = (JSON.parse(raw) as LoxyEvent).data as unknown as LiveTick; } catch { return; }
  if (!t || !Array.isArray(t.projects)) return;
  const prev = live.tick;
  // Targeted invalidation: only projects whose checks, config or state moved.
  const changed: string[] = [];
  const seen = new Set<number>();
  for (const p of t.projects) {
    seen.add(p.id);
    const sig = `${p.health_sig}|${p.version}|${p.status}|${p.maintenance}`;
    const before = sigs.get(p.id);
    if (before !== undefined && before !== sig) changed.push(`p:${p.id}`);
    sigs.set(p.id, sig);
  }
  let membership = !!prev && prev.projects.length !== t.projects.length;
  for (const id of sigs.keys()) if (!seen.has(id)) { sigs.delete(id); membership = true; }
  if (changed.length || membership) bump(...changed, 'projects', 'overview');
  if (prev?.node && t.node && prev.node.applied_version !== t.node.applied_version) bump('nodes');

  const rps: Record<number, number[]> = {};
  for (const p of t.projects) rps[p.id] = push(live.rps[p.id], p.stats.requests_per_second);
  live.rps = rps;
  live.trafficRps = push(live.trafficRps, t.traffic?.requests_per_second ?? 0);
  live.tick = t;
  markFresh();
}

function markFresh() {
  live.stale = false;
  clearTimeout(staleTimer);
  staleTimer = setTimeout(() => { live.stale = true; }, STALE_MS);
}

function handle(kind: string, raw: string) {
  let ev: LoxyEvent | null = null;
  try { ev = JSON.parse(raw) as LoxyEvent; } catch { /* non-JSON payload */ }
  if (ev) live.last = ev;
  const pid = ev?.project_id ?? (ev?.data as { project_id?: number } | undefined)?.project_id;
  const keys = ['events', 'overview', 'projects'];
  if (pid != null) keys.push(`p:${pid}`);
  if (kind.startsWith('apply.') || kind.startsWith('config.')) keys.push('p:*', 'nodes', 'hosts');
  else if (kind.endsWith('.health.changed')) keys.push(...(pid == null ? ['p:*'] : []), 'hosts');
  if (kind.startsWith('certificate.')) keys.push('certificates', 'domains', 'p:*');
  if (kind.startsWith('domain.')) keys.push('domains');
  bump(...keys);
  if (kind === 'apply.failed') toast.error(ev?.message || 'Applying the configuration failed. HAProxy keeps running the previous one.');
  if (kind === 'domain.sync.failed') toast.error(ev?.message || 'DNS sync failed.');
  if (kind === 'backup.failed') toast.error(ev?.message || 'The configuration backup failed.');
}

export function connectLive() {
  if (es || typeof EventSource === 'undefined') return;
  live.state = 'connecting';
  es = new EventSource('/api/v1/events', { withCredentials: true });
  es.onopen = () => {
    live.state = 'open';
    markFresh();
    if (missed) { missed = false; bump('projects', 'overview', 'events', 'p:*', 'nodes'); }
  };
  es.onerror = () => {
    live.state = es?.readyState === EventSource.CLOSED ? 'closed' : 'connecting';
    // After a gap, refetch everything once the stream is back: events may have been missed.
    missed = true;
  };
  es.addEventListener('live.tick', (e) => onTick((e as MessageEvent).data));
  for (const k of EVENT_KINDS) es.addEventListener(k, (e) => handle(k, (e as MessageEvent).data));
  es.onmessage = (e) => handle('message', e.data);
}

export function disconnectLive() {
  es?.close();
  es = null;
  clearTimeout(staleTimer);
  live.state = 'closed';
  live.tick = null;
  live.rps = {};
  live.trafficRps = [];
  sigs.clear();
}
