<script lang="ts" module>
  export interface ChartDef {
    title: string;
    fmt: (v: number) => string;
    series: { key: string; label: string; color: string }[];
    /** Hide the chart when none of its series came back from the API. */
    optional?: boolean;
  }
</script>

<script lang="ts">
  /**
   * Range picker + stacked uPlot charts for one project (optionally one
   * service). The whole-project view gets a 1 s "Live" range fed by
   * /projects/{id}/metrics/stream.
   */
  import { onDestroy, untrack } from 'svelte';
  import { api, unwrap, type Metrics } from '$lib/api/client';
  import Chart from './Chart.svelte';
  import Skeleton from './Skeleton.svelte';
  import ErrorBox from './ErrorBox.svelte';
  import EmptyState from './EmptyState.svelte';

  let { id, serviceId, types, charts }: { id: number; serviceId?: number; types: string; charts: ChartDef[] } = $props();

  const ALL = [
    { id: 'live', label: 'Live', sec: 900, step: 1 },
    { id: '30m', label: '30 min', sec: 1800, step: 5 },
    { id: '1h', label: '1 h', sec: 3600, step: 10 },
    { id: '6h', label: '6 h', sec: 21600, step: 60 },
    { id: '24h', label: '24 h', sec: 86400, step: 300 },
    { id: '7d', label: '7 d', sec: 604800, step: 1800 },
    { id: '30d', label: '30 d', sec: 2592000, step: 7200 }
  ];
  const LIVE_POINTS = 900;
  const streams = $derived(serviceId == null);
  const ranges = $derived(streams ? ALL : ALL.slice(1));
  const keys = $derived([...new Set(charts.flatMap((c) => c.series.map((s) => s.key)))]);

  // svelte-ignore state_referenced_locally
  let range = $state(serviceId == null ? 'live' : '1h');
  let ts = $state<number[]>([]);
  let data = $state<Record<string, (number | null)[]>>({});
  let present = $state<Set<string>>(new Set());
  let loading = $state(true);
  let error = $state('');
  let hovering = false;
  let pending: Record<string, number>[] = [];
  let es: EventSource | null = null;

  async function load(rid: string) {
    const r = ALL.find((x) => x.id === rid)!;
    const end = Math.floor(Date.now() / 1000);
    loading = ts.length === 0;
    error = '';
    try {
      const query = { type: types, start: end - r.sec, end, step: r.step, ...(serviceId != null ? { service_id: serviceId } : {}) };
      const m: Metrics = await unwrap(api.GET('/projects/{id}/metrics', { params: { path: { id }, query } }));
      ts = m.timestamps;
      // Keep every series the API returned, not only the charts shown right now: the chart list
      // depends on the project's services, which may load after this (HTTP charts appear later).
      const next: Record<string, (number | null)[]> = {};
      for (const k of new Set([...keys, ...Object.keys(m.series)])) next[k] = m.series[k] ?? m.timestamps.map(() => null);
      data = next;
      present = new Set(Object.keys(m.series));
    } catch (e) {
      error = (e as Error).message;
    } finally {
      loading = false;
    }
  }

  function apply(points: Record<string, number>[]) {
    if (!points.length) return;
    const nts = [...ts];
    const nd: Record<string, (number | null)[]> = {};
    // Every series must stay aligned with ts (pad a series that appeared late with nulls),
    // otherwise trimming to LIVE_POINTS would cut it to nothing.
    for (const k of new Set([...keys, ...Object.keys(data)])) {
      const a = [...(data[k] ?? [])];
      if (a.length < nts.length) a.unshift(...new Array(nts.length - a.length).fill(null));
      nd[k] = a;
    }
    for (const p of points) {
      nts.push(p.ts);
      for (const k in nd) nd[k].push(p[k] ?? null);
    }
    const cut = Math.max(0, nts.length - LIVE_POINTS);
    ts = nts.slice(cut);
    for (const k in nd) nd[k] = nd[k].slice(cut);
    data = nd;
  }

  function startLive() {
    stopLive();
    es = new EventSource(`/api/v1/projects/${id}/metrics/stream`, { withCredentials: true });
    es.addEventListener('metrics.tick', (e) => {
      try {
        pending.push(JSON.parse((e as MessageEvent).data) as Record<string, number>);
        if (!hovering) { apply(pending); pending = []; }
      } catch { /* ignore malformed */ }
    });
  }
  function stopLive() { es?.close(); es = null; pending = []; }

  $effect(() => {
    const r = range;
    void id;
    void serviceId;
    untrack(() => {
      stopLive();
      void load(r).then(() => { if (r === 'live' && range === 'live') startLive(); });
    });
  });
  onDestroy(stopLive);

  function onhover(h: boolean) {
    hovering = h;
    if (!h && pending.length) { apply(pending); pending = []; }
  }
  const shown = $derived(charts.filter((c) => !c.optional || c.series.some((s) => present.has(s.key))));
</script>

<div class="bar">
  <div class="seg" role="radiogroup" aria-label="Time range">
    {#each ranges as r (r.id)}
      <button role="radio" aria-checked={range === r.id} class:on={range === r.id} onclick={() => (range = r.id)}>
        {#if r.id === 'live'}<span class="ld" class:on={range === 'live'} aria-hidden="true"></span>{/if}{r.label}
      </button>
    {/each}
  </div>
  {#if range === 'live'}<span class="muted xs note">Last 15 minutes, one point per second. Hover a chart to hold it still.</span>{/if}
</div>

{#if error}
  <ErrorBox message={error} onretry={() => load(range)} />
{:else if loading}
  <div class="stack"><Skeleton card rows={5} /><Skeleton card rows={5} /></div>
{:else if !shown.length}
  <div class="card"><EmptyState icon="pulse" title="No metrics yet" text="Metrics appear once HAProxy has served traffic for this project." /></div>
{:else}
  <div class="charts">
    {#each shown as c (c.title)}
      <Chart title={c.title} {ts} series={c.series.map((s) => ({ label: s.label, color: s.color, values: data[s.key] ?? [] }))} fmt={c.fmt} {onhover} xWindow={range === 'live' ? 900 : undefined} />
    {/each}
  </div>
{/if}

<style>
  .bar { display: flex; align-items: center; gap: 14px; flex-wrap: wrap; margin-bottom: 16px; }
  .seg { display: inline-flex; padding: 3px; background: var(--surface-2); border-radius: 10px; overflow-x: auto; max-width: 100%; scrollbar-width: none; }
  .seg button {
    display: inline-flex; align-items: center; gap: 7px; border: 0; background: none; height: 32px; padding: 0 12px; border-radius: 7px;
    color: var(--muted); cursor: pointer; font-weight: 550; font-size: var(--fs-sm); white-space: nowrap; flex: none;
    transition: color 120ms ease, background-color 120ms ease;
  }
  .seg button:hover { color: var(--text); }
  .seg button.on { background: var(--surface); color: var(--text); box-shadow: 0 1px 2px rgb(0 0 0 / 0.08), 0 0 0 1px var(--border); }
  .ld { width: 7px; height: 7px; border-radius: 50%; background: var(--muted); }
  .ld.on { background: var(--ok); box-shadow: 0 0 0 3px var(--ok-soft); animation: ldp 2s ease-in-out infinite; }
  @keyframes ldp { 50% { box-shadow: 0 0 0 5px transparent; } }
  .charts { display: grid; grid-template-columns: repeat(auto-fit, minmax(min(100%, 480px), 1fr)); gap: 16px; }
  @media (max-width: 699px) { .note { flex-basis: 100%; } }
</style>
