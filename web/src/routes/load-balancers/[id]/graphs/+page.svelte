<script lang="ts">
  import { onDestroy, untrack } from 'svelte';
  import { api, unwrap, type MetricsPoint } from '$lib/api/client';
  import { getLb } from '$lib/stores/lbctx';
  import { compact, bytes } from '$lib/util/format';
  import Chart from '$lib/components/Chart.svelte';
  import Skeleton from '$lib/components/Skeleton.svelte';
  import ErrorBox from '$lib/components/ErrorBox.svelte';

  const ctx = getLb();
  const id = $derived(ctx.id);

  const RANGES = [
    { id: 'live', label: 'Live', sec: 300, step: 1 },
    { id: '30m', label: '30 min', sec: 1800, step: 5 },
    { id: '1h', label: '1 h', sec: 3600, step: 10 },
    { id: '6h', label: '6 h', sec: 21600, step: 60 },
    { id: '24h', label: '24 h', sec: 86400, step: 300 },
    { id: '7d', label: '7 d', sec: 604800, step: 1800 },
    { id: '30d', label: '30 d', sec: 2592000, step: 7200 }
  ];
  const KEYS = ['open_connections', 'connections_per_second', 'requests_per_second', 'bandwidth.in', 'bandwidth.out'] as const;
  type Key = (typeof KEYS)[number];

  let range = $state('live');
  let ts = $state<number[]>([]);
  let data = $state<Record<Key, (number | null)[]>>({ open_connections: [], connections_per_second: [], requests_per_second: [], 'bandwidth.in': [], 'bandwidth.out': [] });
  let loading = $state(true);
  let error = $state('');
  let hovering = false;
  let pending: MetricsPoint[] = [];
  let es: EventSource | null = null;

  let hasHttp = $state(true);
  $effect(() => {
    void unwrap(api.GET('/load_balancers/{id}/services', { params: { path: { id } } }))
      .then((r) => (hasHttp = r.services.some((s) => s.protocol !== 'tcp')))
      .catch(() => {});
  });

  async function load(rid: string) {
    const r = RANGES.find((x) => x.id === rid)!;
    const end = Math.floor(Date.now() / 1000);
    loading = ts.length === 0;
    error = '';
    try {
      const m = await unwrap(api.GET('/load_balancers/{id}/metrics', {
        params: { path: { id }, query: { type: 'open_connections,connections_per_second,requests_per_second,bandwidth', start: end - r.sec, end, step: r.step } }
      }));
      ts = m.timestamps;
      const next = {} as Record<Key, (number | null)[]>;
      for (const k of KEYS) next[k] = m.series[k] ?? m.timestamps.map(() => null);
      data = next;
    } catch (e) {
      error = (e as Error).message;
    } finally {
      loading = false;
    }
  }

  function apply(points: MetricsPoint[]) {
    if (!points.length) return;
    const nts = [...ts];
    const nd = { ...data } as Record<Key, (number | null)[]>;
    for (const k of KEYS) nd[k] = [...nd[k]];
    for (const p of points) {
      nts.push(p.ts);
      for (const k of KEYS) nd[k].push((p as Record<string, number | undefined>)[k] ?? null);
    }
    const cut = Math.max(0, nts.length - 300);
    ts = nts.slice(cut);
    for (const k of KEYS) nd[k] = nd[k].slice(cut);
    data = nd;
  }

  function startLive() {
    stopLive();
    es = new EventSource(`/api/v1/load_balancers/${id}/metrics/stream`, { withCredentials: true });
    es.addEventListener('metrics.tick', (e) => {
      try {
        pending.push(JSON.parse((e as MessageEvent).data) as MetricsPoint);
        if (!hovering) { apply(pending); pending = []; }
      } catch { /* ignore malformed */ }
    });
  }
  function stopLive() { es?.close(); es = null; pending = []; }

  $effect(() => {
    const r = range;
    void id;
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
  const rate = (v: number) => compact(v) + '/s';
</script>

<div class="bar">
  <div class="seg" role="radiogroup" aria-label="Time range">
    {#each RANGES as r (r.id)}
      <button role="radio" aria-checked={range === r.id} class:on={range === r.id} onclick={() => (range = r.id)}>
        {#if r.id === 'live'}<span class="ld" class:on={range === 'live'} aria-hidden="true"></span>{/if}{r.label}
      </button>
    {/each}
  </div>
  {#if range === 'live'}<span class="muted xs">Rolling 5 minutes, 1 s resolution. Pauses while you hover a chart.</span>{/if}
</div>

{#if error}
  <ErrorBox message={error} onretry={() => load(range)} />
{:else if loading}
  <div class="stack"><Skeleton card rows={5} /><Skeleton card rows={5} /></div>
{:else}
  <div class="charts">
    <Chart title="Open connections" {ts} series={[{ label: 'Open', color: '--chart-1', values: data.open_connections }]} fmt={compact} {onhover} />
    <Chart title="Connections per second" {ts} series={[{ label: 'New', color: '--chart-1', values: data.connections_per_second }]} fmt={rate} {onhover} />
    {#if hasHttp}
      <Chart title="HTTP requests per second" {ts} series={[{ label: 'Requests', color: '--chart-1', values: data.requests_per_second }]} fmt={rate} {onhover} />
    {/if}
    <Chart title="Bandwidth" {ts} series={[{ label: 'In', color: '--chart-in', values: data['bandwidth.in'] }, { label: 'Out', color: '--chart-out', values: data['bandwidth.out'] }]} fmt={(v) => bytes(v, '/s')} {onhover} />
  </div>
{/if}

<style>
  .bar { display: flex; align-items: center; gap: 14px; flex-wrap: wrap; margin-bottom: 14px; }
  .seg { display: inline-flex; padding: 2px; background: var(--surface-2); border-radius: 8px; overflow-x: auto; max-width: 100%; }
  .seg button { display: inline-flex; align-items: center; gap: 6px; border: 0; background: none; padding: 5px 11px; border-radius: 6px; color: var(--muted); cursor: pointer; font-weight: 500; font-size: var(--fs-sm); white-space: nowrap; }
  .seg button.on { background: var(--surface); color: var(--text); box-shadow: 0 0 0 1px var(--border-strong); }
  .ld { width: 6px; height: 6px; border-radius: 50%; background: var(--muted); }
  .ld.on { background: var(--ok); }
  .charts { display: flex; flex-direction: column; gap: 12px; }
</style>
