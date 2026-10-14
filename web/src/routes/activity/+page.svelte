<script lang="ts">
  import { page } from '$app/state';
  import { goto } from '$app/navigation';
  import { api, unwrap, mutate, getText, type Revision } from '$lib/api/client';
  import { resource } from '$lib/stores/resource.svelte';
  import { confirm } from '$lib/stores/confirm.svelte';
  import { dateTime, relTime } from '$lib/util/format';
  import Tabs from '$lib/components/Tabs.svelte';
  import Icon from '$lib/components/Icon.svelte';
  import Modal from '$lib/components/Modal.svelte';
  import CodeView from '$lib/components/CodeView.svelte';
  import HealthBadge from '$lib/components/HealthBadge.svelte';
  import Skeleton from '$lib/components/Skeleton.svelte';
  import EmptyState from '$lib/components/EmptyState.svelte';
  import ErrorBox from '$lib/components/ErrorBox.svelte';

  const tab = $derived(page.url.searchParams.get('tab') ?? 'events');
  const tabs = [
    { id: 'events', label: 'Events', href: '/activity' },
    { id: 'audit', label: 'Audit log', href: '/activity?tab=audit' },
    { id: 'revisions', label: 'Config revisions', href: '/activity?tab=revisions' }
  ];

  let kindFilter = $state('');
  let sevFilter = $state('');
  const events = resource(async () => (tab === 'events' ? (await unwrap(api.GET('/events/history', { params: { query: { limit: 200, kind: kindFilter || undefined } } }))).events : []), { deps: () => [tab, kindFilter], keys: () => ['events'] });
  const shownEvents = $derived((events.data ?? []).filter((e) => !sevFilter || e.severity === sevFilter));
  const audit = resource(async () => (tab === 'audit' ? (await unwrap(api.GET('/audit', { params: { query: { limit: 200 } } }))).audit : []), { deps: () => tab });
  const revs = resource(async () => (tab === 'revisions' ? (await unwrap(api.GET('/nodes/{id}/revisions', { params: { path: { id: 1 } } }))).revisions : []), { deps: () => tab, keys: () => ['nodes'] });

  let diffOpen = $state(false);
  let diffRev = $state<Revision | null>(null);
  let diffText = $state('');
  let diffErr = $state('');
  async function showDiff(r: Revision) {
    diffRev = r; diffText = ''; diffErr = ''; diffOpen = true;
    try { diffText = (await getText(`/nodes/1/revisions/${r.revision}/diff`)) || '(no changes)'; } catch (e) { diffErr = (e as Error).message; }
  }
  async function rollback(r: Revision) {
    const ok = await confirm({ title: `Roll back to revision ${r.revision}?`, message: 'The desired state stored with this revision is re-applied. Changes made since are reverted.', confirmLabel: 'Roll back', danger: true });
    if (!ok) return;
    const res = await mutate(api.POST('/nodes/{id}/revisions/{rev}/actions/rollback', { params: { path: { id: 1, rev: r.revision } } }), { success: `Rolling back to revision ${r.revision}` });
    if (res !== undefined) { diffOpen = false; void revs.reload(); }
  }

  function exportCsv() {
    const rows = [['time', 'actor', 'ip', 'action', 'resource_type', 'resource_id']];
    for (const a of audit.data ?? []) rows.push([new Date(a.ts * 1000).toISOString(), a.actor ?? '', a.ip ?? '', a.action, a.resource_type, String(a.resource_id ?? '')]);
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
        {#each ['lb.updated', 'lb.apply.succeeded', 'lb.apply.failed', 'target.health.changed', 'service.health.changed', 'certificate.updated', 'domain.status.changed', 'domain.sync.failed', 'node.status.changed'] as k (k)}<option>{k}</option>{/each}
      </select>
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
            {#if e.lb_id}<a class="xs" href="/load-balancers/{e.lb_id}/overview">LB #{e.lb_id}</a>{/if}
            <time class="xs muted" datetime={new Date(e.ts * 1000).toISOString()} title={dateTime(e.ts)}>{relTime(e.ts)}</time>
          </li>
        {/each}
      </ol>
    {/if}
  {:else if tab === 'audit'}
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
  {:else}
    {#if revs.error && !revs.data}<ErrorBox message={revs.error} onretry={revs.reload} />
    {:else if !revs.data}<Skeleton card rows={8} />
    {:else if !revs.data.length}<div class="card"><EmptyState icon="activity" title="No revisions yet" text="Every applied configuration is kept as a revision you can inspect and roll back to." /></div>
    {:else}
      <div class="table-wrap">
        <table class="tbl">
          <thead><tr><th class="right">Revision</th><th>Kind</th><th>Result</th><th class="right">Duration</th><th>SHA-256</th><th>Created</th><th><span class="sr-only">Actions</span></th></tr></thead>
          <tbody>
            {#each revs.data as r (r.id)}
              <tr>
                <td class="right mono">{r.revision}{#if r.pinned} <Icon name="tag" size={12} label="Pinned" />{/if}</td>
                <td class="small">{r.kind}</td>
                <td><HealthBadge status={r.result === 'ok' ? 'ok' : 'failed'} /></td>
                <td class="right small">{r.duration_ms} ms</td>
                <td class="mono xs muted">{r.sha256.slice(0, 12)}</td>
                <td class="small" title={dateTime(r.created_at)}>{relTime(r.created_at)}</td>
                <td class="actions"><span class="row"><button class="btn sm" onclick={() => showDiff(r)}>View diff</button><button class="btn sm ghost" onclick={() => rollback(r)}>Roll back</button></span></td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
  {/if}
</div>

<Modal bind:open={diffOpen} title="Revision {diffRev?.revision ?? ''}" width={900}>
  {#if diffErr}<ErrorBox message={diffErr} />
  {:else if !diffText}<Skeleton rows={10} />
  {:else}<CodeView text={diffText} diff label="Revision diff" />{/if}
  {#if diffRev?.haproxy_output}<h3 style:margin="14px 0 6px">HAProxy output</h3><CodeView text={diffRev.haproxy_output} maxHeight={200} label="HAProxy output" />{/if}
  {#snippet footer()}
    <button class="btn" onclick={() => (diffOpen = false)}>Close</button>
    {#if diffRev}<button class="btn danger" onclick={() => rollback(diffRev!)}>Roll back to this revision</button>{/if}
  {/snippet}
</Modal>

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
</style>
