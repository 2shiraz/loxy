<script lang="ts">
  import { getBe } from '$lib/stores/ctx';
  import { compact } from '$lib/util/format';
  import MetricsGraphs, { type ChartDef } from '$lib/components/MetricsGraphs.svelte';

  const ctx = getBe();
  const rate = (v: number) => compact(v) + '/s';
  const ms = (v: number) => `${compact(v)} ms`;
  const charts = $derived<ChartDef[]>([
    { title: 'Sessions', fmt: compact, optional: true, series: [{ key: 'open_connections', label: 'Current', color: '--chart-1' }] },
    { title: 'Response time', fmt: ms, series: [{ key: 'response_time_ms', label: 'Average', color: '--chart-1' }] },
    ...(ctx.item?.mode === 'http'
      ? [{ title: 'HTTP responses per second', fmt: rate, series: [
          { key: 'http_2xx', label: '2xx', color: '--ok' },
          { key: 'http_3xx', label: '3xx', color: '--chart-1' },
          { key: 'http_4xx', label: '4xx', color: '--warn' },
          { key: 'http_5xx', label: '5xx', color: '--err' }
        ] }]
      : []),
    { title: 'Errors per second', fmt: rate, series: [{ key: 'errors_per_second', label: 'Errors', color: '--err' }] },
    { title: 'Queue', fmt: compact, optional: true, series: [{ key: 'queue', label: 'Queued', color: '--warn' }] }
  ]);
</script>

<svelte:head><title>Graphs · {ctx.item?.name ?? 'Backend'} · Loxy</title></svelte:head>

{#key ctx.id}
  <MetricsGraphs kind="backends" id={ctx.id} types="open_connections,response_time_ms,http_2xx,http_3xx,http_4xx,http_5xx,errors_per_second,queue" {charts} />
{/key}
