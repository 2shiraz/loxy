<script lang="ts">
  import { page } from '$app/state';
  import { goto } from '$app/navigation';
  import { api, unwrap } from '$lib/api/client';
  import { resource } from '$lib/stores/resource.svelte';
  import { EVENT_KINDS } from '$lib/stores/live.svelte';
  import { dateTime, relTime } from '$lib/util/format';
  import Tabs from '$lib/components/Tabs.svelte';
  import Icon from '$lib/components/Icon.svelte';
  import Skeleton from '$lib/components/Skeleton.svelte';
  import EmptyState from '$lib/components/EmptyState.svelte';
  import ErrorBox from '$lib/components/ErrorBox.svelte';

  const tab = $derived(page.url.searchParams.get('tab') ?? 'events');
  const projectId = $derived(Number(page.url.searchParams.get('project_id')) || undefined);
  const projects = resource(async () => (await unwrap(api.GET('/projects'))).projects, { keys: () => ['projects'] });
  const projectName = (pid: number) => projects.data?.find((p) => p.id === pid)?.name ?? `Project #${pid}`;
  const tabs = [
    { id: 'events', label: 'Events', href: '/activity' },
    { id: 'audit', label: 'Audit log', href: '/activity?tab=audit' }
  ];

  let kindFilter = $state('');
  let sevFilter = $state('');
  const events = resource(async () => (tab === 'events' ? (await unwrap(api.GET('/events/history', { params: { query: { limit: 200, kind: kindFilter || undefined, project_id: projectId } } }))).events : []), { deps: () => [tab, kindFilter, projectId], keys: () => ['events'] });
  const shownEvents = $derived((events.data ?? []).filter((e) => !sevFilter || e.severity === sevFilter));
  const audit = resource(async () => (tab === 'audit' ? (await unwrap(api.GET('/audit', { params: { query: { limit: 200 } } }))).audit : []), { deps: () => tab });
  function exportCsv() {
    const rows = [['time', 'actor', 'ip', 'action', 'resource_type', 'resource_id']];
    for (const a of audit.data ?? []) rows.push([new Date(a.ts).toISOString(), a.actor ?? '', a.ip ?? '', a.action, a.resource_type, String(a.resource_id ?? '')]);
    const csv = rows.map((r) => r.map((c) => `"${c.replaceAll('"', '""')}"`).join(',')).join('\n');
    const el = document.createElement('a');
    el.href = URL.createObjectURL(new Blob([csv], { type: 'text/csv' }));
    el.download = 'loxy-audit.csv';
    el.click();
    URL.revokeObjectURL(el.href);
  }
  const sevIcon = (s: string) => (s === 'error' ? 'x' : s === 'warning' ? 'alert' : 'info');
</script>

<svelte:head><title>Activity · Loxy</title></svelte:head>
<div class="page-head"><h1>Activity</h1></div>
<Tabs {tabs} active={tab} label="Activity sections" />
<div class="body">
  {#if tab === 'events'}
    <div class="row-wrap filt">
      <select class="select" style:width="220px" bind:value={kindFilter} aria-label="Filter by kind">
        <option value="">All kinds</option>
        {#each EVENT_KINDS as k (k)}<option>{k}</option>{/each}
      </select>
      {#if projectId}
        <span class="chip">{projectName(projectId)} <a href="/activity" aria-label="Show events of all projects" class="clr">×</a></span>
      {/if}
      <select class="select" style:width="150px" bind:value={sevFilter} aria-label="Filter by severity">
        <option value="">All severities</option><option value="info">Info</option><option value="warning">Warning</option><option value="error">Error</option>
      </select>
    </div>
    {#if events.error && !events.data}<ErrorBox message={events.error} onretry={events.reload} />
    {:else if !events.data}<Skeleton card rows={8} />
    {:else if !shownEvents.length}<div class="card"><EmptyState icon="activity" title="No events" text="Health changes, reloads and certificate events appear here as they happen." /></div>
    {:else}
      <ol class="feed card flush">
        {#each shownEvents as e (e.id)}
          <li>
            <span class="sev {e.severity}"><Icon name={sevIcon(e.severity)} size={13} label={e.severity} /></span>
            <span class="msg">{e.message}</span>
            <span class="mono xs muted kind">{e.kind}</span>
            {#if e.project_id && !projectId}<a class="xs" href="/projects/{e.project_id}/overview">{projectName(e.project_id)}</a>{/if}
            <time class="xs muted" datetime={new Date(e.ts).toISOString()} title={dateTime(e.ts)}>{relTime(e.ts)}</time>
          </li>
        {/each}
      </ol>
    {/if}
  {:else}
    <div class="row filt"><span class="spacer"></span><button class="btn sm" onclick={exportCsv} disabled={!audit.data?.length}><Icon name="download" size={13} />Export CSV</button></div>
    {#if audit.error && !audit.data}<ErrorBox message={audit.error} onretry={audit.reload} />
    {:else if !audit.data}<Skeleton card rows={8} />
    {:else if !audit.data.length}<div class="card"><EmptyState icon="activity" title="No audit entries yet" /></div>
    {:else}
      <div class="table-wrap">
        <table class="tbl">
          <thead><tr><th>Time</th><th>Actor</th><th>Action</th><th>Resource</th><th>IP</th></tr></thead>
          <tbody>
            {#each audit.data as a (a.id)}
              <tr><td class="small" title={dateTime(a.ts)}>{dateTime(a.ts)}</td><td>{a.actor ?? 'system'}</td><td class="mono small">{a.action}</td><td class="small">{a.resource_type}{a.resource_id ? ` #${a.resource_id}` : ''}</td><td class="mono small muted">{a.ip ?? '—'}</td></tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
  {/if}
</div>

<style>
  .body { padding-top: 16px; }
  .filt { margin-bottom: 12px; }
  .feed { list-style: none; margin: 0; padding: 0; }
  .feed li { display: flex; align-items: center; gap: 12px; padding: 9px 14px; border-bottom: 1px solid var(--border); flex-wrap: wrap; }
  .feed li:last-child { border-bottom: 0; }
  .msg { flex: 1; min-width: 200px; }
  .sev { color: var(--info); display: inline-flex; }
  .sev.warning { color: var(--warn); }
  .sev.error { color: var(--err); }
  .kind { min-width: 160px; }
  time { min-width: 90px; text-align: right; }
  .clr { text-decoration: none; margin-left: 4px; color: var(--muted); }
  .clr:hover { color: var(--text); }
</style>
