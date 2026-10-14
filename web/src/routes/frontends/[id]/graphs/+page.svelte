<script lang="ts">
  import { getFe } from '$lib/stores/ctx';
  import { compact, bytes } from '$lib/util/format';
  import MetricsGraphs, { type ChartDef } from '$lib/components/MetricsGraphs.svelte';

  const ctx = getFe();
  const rate = (v: number) => compact(v) + '/s';
  const charts = $derived<ChartDef[]>([
    { title: 'Open connections', fmt: compact, series: [{ key: 'open_connections', label: 'Open', color: '--chart-1' }] },
    { title: 'Connections per second', fmt: rate, series: [{ key: 'connections_per_second', label: 'New', color: '--chart-1' }] },
    ...(ctx.item?.mode === 'http' ? [{ title: 'HTTP requests per second', fmt: rate, series: [{ key: 'requests_per_second', label: 'Requests', color: '--chart-1' }] }] : []),
    { title: 'Bandwidth', fmt: (v: number) => bytes(v, '/s'), series: [{ key: 'bandwidth.in', label: 'In', color: '--chart-in' }, { key: 'bandwidth.out', label: 'Out', color: '--chart-out' }] }
  ]);
</script>

<svelte:head><title>Graphs · {ctx.item?.name ?? 'Frontend'} · Loxy</title></svelte:head>

{#key ctx.id}
  <MetricsGraphs kind="frontends" id={ctx.id} types="open_connections,connections_per_second,requests_per_second,bandwidth" {charts} />
{/key}
