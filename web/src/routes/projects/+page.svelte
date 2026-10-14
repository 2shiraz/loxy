<script lang="ts">
  import { goto } from '$app/navigation';
  import { api, unwrap, type Project } from '$lib/api/client';
  import { resource } from '$lib/stores/resource.svelte';
  import { matchSelector } from '$lib/util/validate';
  import { compact, num, projectStatus, algorithmLabel } from '$lib/util/format';
  import Icon from '$lib/components/Icon.svelte';
  import LabelChips from '$lib/components/LabelChips.svelte';
  import HealthCount from '$lib/components/HealthCount.svelte';
  import Skeleton from '$lib/components/Skeleton.svelte';
  import EmptyState from '$lib/components/EmptyState.svelte';
  import ErrorBox from '$lib/components/ErrorBox.svelte';
  import StatusDot from '$lib/components/StatusDot.svelte';
  import Endpoints from '$lib/components/Endpoints.svelte';

  const projects = resource(async () => (await unwrap(api.GET('/projects'))).projects, { keys: () => ['projects', 'p:*'] });

  let q = $state('');
  let selector = $state('');
  let sort = $state<'name' | 'created' | 'rps'>('name');

  const rows = $derived.by(() => {
    const t = q.trim().toLowerCase();
    const list = (projects.data ?? []).filter((p) => {
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
  <a class="btn primary" href="/projects/new"><Icon name="plus" size={14} />Create project</a>
</div>

{#if projects.error && !projects.data}
  <ErrorBox message={projects.error} onretry={projects.reload} />
{:else if !projects.data}
  <Skeleton card rows={6} height={18} />
{:else if projects.data.length === 0}
  <div class="card">
    <EmptyState icon="lb" title="No projects yet" text="A project is one load balancer: it spreads traffic for a port or a domain across your servers, with health checks.">
      <a class="btn primary" href="/projects/new"><Icon name="plus" size={14} />Create project</a>
    </EmptyState>
  </div>
{:else}
  <div class="filters">
    <label class="search">
      <Icon name="search" size={14} />
      <span class="sr-only">Search projects</span>
      <input class="input" type="search" placeholder="Search name or domain" bind:value={q} spellcheck="false" autocomplete="off" />
    </label>
    <label class="search sel">
      <Icon name="tag" size={14} />
      <span class="sr-only">Filter by label selector</span>
      <input class="input mono" placeholder="env=prod" bind:value={selector} spellcheck="false" autocomplete="off" />
    </label>
    <label class="sortl"><span class="muted small">Sort</span>
      <select class="select" bind:value={sort}>
        <option value="name">Name</option>
        <option value="created">Newest</option>
        <option value="rps">Requests / s</option>
      </select>
    </label>
  </div>

  <div class="table-wrap">
    <table class="tbl">
      <thead>
        <tr>
          <th style:width="28px"><span class="sr-only">Status</span></th>
          <th>Name</th>
          <th>Endpoints</th>
          <th>Targets</th>
          <th class="right">Services</th>
          <th class="right">Req/s</th>
          <th class="right">Conns</th>
          <th>Labels</th>
        </tr>
      </thead>
      <tbody>
        {#each rows as p (p.id)}
          <tr class="clickable" onclick={() => open(p)}>
            <td><StatusDot status={projectStatus(p)} /></td>
            <td>
              <a class="name" href="/projects/{p.id}/overview" onclick={(e) => e.stopPropagation()}>{p.name}</a>
              {#if p.maintenance}<span class="badge warn xs-badge"><Icon name="pause" size={10} />Maintenance</span>{/if}
              <div class="muted xs">{algorithmLabel(p.algorithm)}{p.description ? ` · ${p.description}` : ''}</div>
            </td>
            <!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_noninteractive_element_interactions -->
            <td class="eps" onclick={(e) => e.stopPropagation()}><Endpoints endpoints={p.endpoints} /></td>
            <td><HealthCount healthy={p.health.healthy} total={p.health.total} title="{p.health.healthy} of {p.health.total} target checks healthy" /> <span class="muted xs">{p.counts.targets} target{p.counts.targets === 1 ? '' : 's'}</span></td>
            <td class="right">{num(p.counts.services)}</td>
            <td class="right">{compact(p.stats.requests_per_second)}</td>
            <td class="right">{compact(p.stats.open_connections)}</td>
            <td><LabelChips labels={p.labels} max={3} /></td>
          </tr>
        {:else}
          <tr><td colspan="8" class="muted nores">No projects match these filters.</td></tr>
        {/each}
      </tbody>
    </table>
  </div>
{/if}

<style>
  .filters { display: flex; gap: 8px; flex-wrap: wrap; margin-bottom: 14px; }
  .search { position: relative; flex: 1 1 220px; max-width: 340px; display: flex; align-items: center; }
  .search :global(.icon) { position: absolute; left: 10px; color: var(--muted); pointer-events: none; }
  .search .input { padding-left: 30px; }
  .sortl { display: flex; align-items: center; gap: 8px; margin-left: auto; }
  .sortl .select { width: 150px; }
  .name { font-weight: 500; text-decoration: none; margin-right: 6px; }
  .name:hover { text-decoration: underline; }
  .xs-badge { height: 18px; font-size: var(--fs-xs); vertical-align: 1px; }
  .eps { max-width: 280px; }
  .nores { text-align: center; padding: 28px; }
</style>
