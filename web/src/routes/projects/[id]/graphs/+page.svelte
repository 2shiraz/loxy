<script lang="ts">
  import { api, unwrap } from '$lib/api/client';
  import { getProject } from '$lib/stores/project';
  import { resource } from '$lib/stores/resource.svelte';
  import { compact, bytes } from '$lib/util/format';
  import { serviceTitle } from '$lib/util/services';
  import MetricsGraphs, { type ChartDef } from '$lib/components/MetricsGraphs.svelte';

  const ctx = getProject();
  const id = $derived(ctx.id);
  const services = resource(async () => (await unwrap(api.GET('/projects/{id}/services', { params: { path: { id } } }))).services, { deps: () => id });

  let sid = $state<number | ''>('');
  const svc = $derived(sid === '' ? undefined : services.data?.find((s) => s.id === sid));
  const http = $derived(svc ? svc.protocol !== 'tcp' : (services.data ?? []).some((s) => s.protocol !== 'tcp'));

  const rate = (v: number) => compact(v) + '/s';
  const ms = (v: number) => `${compact(v)} ms`;
  const charts = $derived<ChartDef[]>([
    { title: 'Open connections', fmt: compact, series: [{ key: 'open_connections', label: 'Open', color: '--chart-1' }] },
    { title: 'Connections per second', fmt: rate, series: [{ key: 'connections_per_second', label: 'New', color: '--chart-1' }] },
    ...(http ? [{ title: 'HTTP requests per second', fmt: rate, series: [{ key: 'requests_per_second', label: 'Requests', color: '--chart-1' }] }] : []),
    ...(http
      ? [{ title: 'HTTP responses per second', fmt: rate, optional: true, series: [
          { key: 'http_2xx', label: '2xx', color: '--ok' },
          { key: 'http_3xx', label: '3xx', color: '--chart-1' },
          { key: 'http_4xx', label: '4xx', color: '--warn' },
          { key: 'http_5xx', label: '5xx', color: '--err' }
        ] }]
      : []),
    { title: 'Response time', fmt: ms, optional: true, series: [{ key: 'response_time_ms', label: 'Average', color: '--chart-1' }] },
    { title: 'Bandwidth', fmt: (v: number) => bytes(v, '/s'), series: [{ key: 'bandwidth.in', label: 'In', color: '--chart-in' }, { key: 'bandwidth.out', label: 'Out', color: '--chart-out' }] },
    { title: 'Errors per second', fmt: rate, optional: true, series: [{ key: 'errors_per_second', label: 'Errors', color: '--err' }] }
  ]);
</script>

<svelte:head><title>Graphs · {ctx.project?.name ?? 'Project'} · Loxy</title></svelte:head>

{#if (services.data?.length ?? 0) > 1}
  <div class="svcsel">
    <label for="gsvc" class="small muted">Service</label>
    <select id="gsvc" class="select" bind:value={sid}>
      <option value="">Whole project</option>
      {#each services.data ?? [] as s (s.id)}<option value={s.id}>{serviceTitle(s)}</option>{/each}
    </select>
  </div>
{/if}

{#key `${id}:${sid}`}
  <MetricsGraphs {id} serviceId={sid === '' ? undefined : sid} types="open_connections,connections_per_second,requests_per_second,bandwidth,http_2xx,http_3xx,http_4xx,http_5xx,response_time_ms,errors_per_second" {charts} />
{/key}

<style>
  .svcsel { display: flex; align-items: center; gap: 8px; margin-bottom: 12px; }
  .svcsel .select { width: auto; min-width: 220px; }
</style>
