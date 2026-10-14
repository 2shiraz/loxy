<script lang="ts">
  import { goto } from '$app/navigation';
  import { api, unwrap, type Project } from '$lib/api/client';
  import { resource } from '$lib/stores/resource.svelte';
  import { live, withLive } from '$lib/stores/live.svelte';
  import { matchSelector } from '$lib/util/validate';
  import { compact, rate, projectStatus, algorithmLabel } from '$lib/util/format';
  import Icon from '$lib/components/Icon.svelte';
  import LabelChips from '$lib/components/LabelChips.svelte';
  import HealthCount from '$lib/components/HealthCount.svelte';
  import HealthBadge from '$lib/components/HealthBadge.svelte';
  import Skeleton from '$lib/components/Skeleton.svelte';
  import EmptyState from '$lib/components/EmptyState.svelte';
  import ErrorBox from '$lib/components/ErrorBox.svelte';
  import StatusDot from '$lib/components/StatusDot.svelte';
  import Endpoints from '$lib/components/Endpoints.svelte';
  import LiveNumber from '$lib/components/LiveNumber.svelte';
  import Sparkline from '$lib/components/Sparkline.svelte';

  const projects = resource(async () => (await unwrap(api.GET('/projects'))).projects, { keys: () => ['projects', 'p:*'] });

  let q = $state('');
  let selector = $state('');
  let sort = $state<'name' | 'created' | 'rps'>('name');

  const hasLabels = $derived((projects.data ?? []).some((p) => Object.keys(p.labels ?? {}).length));
  const rows = $derived.by(() => {
    const t = q.trim().toLowerCase();
    const list = (projects.data ?? []).map(withLive).filter((p) => {
      if (t && !(p.name.toLowerCase().includes(t) || (p.description ?? '').toLowerCase().includes(t) || p.endpoints.some((e) => e.url.toLowerCase().includes(t)))) return false;
      return matchSelector(p.labels, selector);
    });
    const s = [...list];
    if (sort === 'name') s.sort((a, b) => a.name.localeCompare(b.name, undefined, { numeric: true }));
    if (sort === 'created') s.sort((a, b) => b.created_at - a.created_at);
    if (sort === 'rps') s.sort((a, b) => b.stats.requests_per_second - a.stats.requests_per_second);
    return s;
  });

  function open(p: Project) { void goto(`/projects/${p.id}/overview`); }
</script>

<svelte:head><title>Projects · Loxy</title></svelte:head>

<div class="page-head">
  <h1>Projects</h1>
  {#if projects.data?.length}<a class="btn primary" href="/projects/new"><Icon name="plus" size={15} />Create project</a>{/if}
  <p class="lede">Each project is one load balancer: an address people connect to, and the servers it spreads their traffic across.</p>
</div>

{#if projects.error && !projects.data}
  <ErrorBox message={projects.error} onretry={projects.reload} />
{:else if !projects.data}
  <Skeleton card rows={6} height={18} />
{:else if projects.data.length === 0}
  <div class="card first">
    <EmptyState icon="route" title="Put your first service online" text="Name it, list the servers that should receive traffic, and Loxy gives it its own port right away. Attach a domain later for HTTPS on 443.">
      <a class="btn primary" href="/projects/new"><Icon name="plus" size={15} />Create project</a>
    </EmptyState>
  </div>
{:else}
  <div class="filters">
    <label class="search">
      <Icon name="search" size={15} />
      <span class="sr-only">Search projects</span>
      <input class="input" type="search" placeholder="Search by name or address…" bind:value={q} spellcheck="false" autocomplete="off" />
    </label>
    {#if hasLabels || selector}
      <label class="search sel">
        <Icon name="tag" size={15} />
        <span class="sr-only">Filter by label selector</span>
        <input class="input mono" placeholder="env=prod" bind:value={selector} spellcheck="false" autocomplete="off" />
      </label>
    {/if}
    <label class="sortl"><span class="muted small">Sort</span>
      <select class="select" bind:value={sort}>
        <option value="name">Name</option>
        <option value="created">Newest</option>
        <option value="rps">Busiest</option>
      </select>
    </label>
  </div>

  <div class="table-wrap">
    <table class="tbl cards plist">
      <thead>
        <tr>
          <th>Project</th>
          <th>Reach it at</th>
          <th>Targets</th>
          <th class="right">Requests/s</th>
          <th class="right">Connections</th>
          {#if hasLabels}<th>Labels</th>{/if}
        </tr>
      </thead>
      <tbody>
        {#each rows as p (p.id)}
          {@const st = projectStatus(p)}
          <tr class="clickable" onclick={() => open(p)}>
            <td class="lead">
              <span class="who">
                <StatusDot status={st} />
                <span class="nm">
                  <a class="name" href="/projects/{p.id}/overview" onclick={(e) => e.stopPropagation()}>{p.name}</a>
                  <span class="sub muted xs">
                    {#if st === 'applying'}<span class="warn-text">Applying change…</span>
                    {:else if st === 'apply_error'}<span class="err-text">Last change failed</span>
                    {:else}{algorithmLabel(p.algorithm)}{/if}{p.description ? ` · ${p.description}` : ''}
                  </span>
                </span>
                {#if p.maintenance}<span class="badge warn"><Icon name="pause" size={10} />Maintenance</span>{/if}
                <span class="m-badge"><HealthBadge status={st} /></span>
              </span>
            </td>
            <!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_noninteractive_element_interactions -->
            <td class="eps" data-label="Reach it at" onclick={(e) => e.stopPropagation()}><Endpoints endpoints={p.endpoints} /></td>
            <td data-label="Targets">
              <span class="tg"><HealthCount healthy={p.health.healthy} total={p.health.total} title="{p.health.healthy} of {p.health.total} target checks healthy" /> <span class="muted xs">{p.counts.targets} target{p.counts.targets === 1 ? '' : 's'}</span></span>
            </td>
            <td class="right" data-label="Requests/s">
              <span class="traffic">
                <span class="sp"><Sparkline values={live.rps[p.id] ?? []} height={22} /></span>
                <LiveNumber value={p.stats.requests_per_second} format={rate} class="val" />
              </span>
            </td>
            <td class="right" data-label="Connections"><LiveNumber value={p.stats.open_connections} format={compact} class="val" /></td>
            {#if hasLabels}<td data-label="Labels"><LabelChips labels={p.labels} max={3} /></td>{/if}
          </tr>
        {:else}
          <tr><td colspan="6" class="muted nores">No projects match these filters.</td></tr>
        {/each}
      </tbody>
    </table>
  </div>
{/if}

<style>
  .first { padding: 24px; }
  .filters { display: flex; gap: 8px; flex-wrap: wrap; margin-bottom: 14px; }
  .search { position: relative; flex: 1 1 220px; max-width: 360px; display: flex; align-items: center; }
  .search.sel { max-width: 220px; }
  .search :global(.icon) { position: absolute; left: 11px; color: var(--muted); pointer-events: none; }
  .search .input { padding-left: 34px; }
  .sortl { display: flex; align-items: center; gap: 8px; margin-left: auto; }
  .sortl .select { width: 140px; }
  .who { display: flex; align-items: center; gap: 12px; min-width: 0; }
  .nm { display: flex; flex-direction: column; min-width: 0; }
  .name { font-weight: 600; font-size: var(--fs-lg); text-decoration: none; letter-spacing: -0.005em; max-width: 260px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .name:hover { text-decoration: underline; }
  .sub { white-space: nowrap; overflow: hidden; text-overflow: ellipsis; max-width: 260px; }
  .m-badge { display: none; }
  .eps { max-width: 300px; }
  .tg { display: inline-flex; align-items: center; gap: 8px; }
  .traffic { display: inline-flex; align-items: center; justify-content: flex-end; gap: 12px; }
  .sp { width: 72px; }
  .plist :global(.val) { font-weight: 600; min-width: 3.5ch; display: inline-block; text-align: right; }
  .nores { text-align: center; padding: 32px; }

  @media (max-width: 699px) {
    .filters .search { max-width: none; flex-basis: 100%; }
    .sortl { margin-left: 0; }
    .who { width: 100%; }
    .nm { flex: 1; }
    .m-badge { display: inline-flex; }
    .eps { max-width: none; }
    .sp { width: 56px; }
    .sub { max-width: 100%; }
  }
</style>
