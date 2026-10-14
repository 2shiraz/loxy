<script lang="ts">
  import { goto } from '$app/navigation';
  import { api, unwrap, type LoadBalancer } from '$lib/api/client';
  import { resource } from '$lib/stores/resource.svelte';
  import { matchSelector } from '$lib/util/validate';
  import { flag, num } from '$lib/util/format';
  import Icon from '$lib/components/Icon.svelte';
  import LabelChips from '$lib/components/LabelChips.svelte';
  import HealthCount from '$lib/components/HealthCount.svelte';
  import Skeleton from '$lib/components/Skeleton.svelte';
  import EmptyState from '$lib/components/EmptyState.svelte';
  import ErrorBox from '$lib/components/ErrorBox.svelte';
  import StatusDot from '$lib/components/StatusDot.svelte';

  const lbs = resource(async () => (await unwrap(api.GET('/load_balancers'))).load_balancers, { keys: () => ['lbs'] });

  let q = $state('');
  let selector = $state('');
  let sort = $state<'name' | 'created' | 'connections'>('name');

  const rows = $derived.by(() => {
    const list = (lbs.data ?? []).filter((lb) => {
      const t = q.trim().toLowerCase();
      if (t && !(lb.name.toLowerCase().includes(t) || String(lb.id).includes(t) || lb.public_net.ipv4?.includes(t) || lb.public_net.ipv6?.includes(t))) return false;
      return matchSelector(lb.labels, selector);
    });
    const s = [...list];
    if (sort === 'name') s.sort((a, b) => a.name.localeCompare(b.name));
    if (sort === 'created') s.sort((a, b) => b.created_at - a.created_at);
    if (sort === 'connections') s.sort((a, b) => b.usage.open_connections - a.usage.open_connections);
    return s;
  });

  function open(lb: LoadBalancer) { void goto(`/load-balancers/${lb.id}/overview`); }
</script>

<svelte:head><title>Load Balancers · Loxy</title></svelte:head>

<div class="page-head">
  <h1>Load Balancers</h1>
  <a class="btn primary" href="/load-balancers/new"><Icon name="plus" size={14} />Create Load Balancer</a>
</div>

<div class="filters">
  <label class="search">
    <Icon name="search" size={14} />
    <span class="sr-only">Search load balancers</span>
    <input class="input" placeholder="Search name, ID or IP" bind:value={q} spellcheck="false" />
  </label>
  <label class="search sel">
    <Icon name="tag" size={14} />
    <span class="sr-only">Label selector</span>
    <input class="input mono" placeholder="env=prod,team!=ops" bind:value={selector} spellcheck="false" />
  </label>
  <label class="sortl"><span class="muted small">Sort</span>
    <select class="select" bind:value={sort} aria-label="Sort by">
      <option value="name">Name</option>
      <option value="created">Newest</option>
      <option value="connections">Connections</option>
    </select>
  </label>
</div>

{#if lbs.error && !lbs.data}
  <ErrorBox message={lbs.error} onretry={lbs.reload} />
{:else if !lbs.data}
  <Skeleton card rows={6} height={18} />
{:else if lbs.data.length === 0}
  <div class="card">
    <EmptyState icon="lb" title="No load balancers yet" text="A load balancer distributes traffic across your servers. Create one to get a public IP, services and health checks.">
      <a class="btn primary" href="/load-balancers/new"><Icon name="plus" size={14} />Create Load Balancer</a>
    </EmptyState>
  </div>
{:else}
  <div class="table-wrap">
    <table class="tbl">
      <thead>
        <tr>
          <th style:width="28px"><span class="sr-only">Status</span></th>
          <th>Name</th>
          <th>IPv4</th>
          <th>IPv6</th>
          <th>Plan</th>
          <th>Location</th>
          <th class="right">Services</th>
          <th>Targets</th>
          <th class="right">Connections</th>
          <th>Labels</th>
        </tr>
      </thead>
      <tbody>
        {#each rows as lb (lb.id)}
          <tr class="clickable" onclick={() => open(lb)}>
            <td><StatusDot status={lb.status === 'active' ? lb.health.status : lb.status} /></td>
            <td>
              <a class="name" href="/load-balancers/{lb.id}/overview" onclick={(e) => e.stopPropagation()}>{lb.name}</a>
              <span class="muted xs mono">#{lb.id}</span>
              {#if lb.delete_protected}<Icon name="lock" size={12} label="Delete protected" class="lockic" />{/if}
            </td>
            <td class="mono small">{lb.public_net.enabled ? (lb.public_net.ipv4 ?? '—') : 'disabled'}</td>
            <td class="mono small ip6">{lb.public_net.enabled ? (lb.public_net.ipv6 ?? '—') : '—'}</td>
            <td><span class="badge plan">{lb.plan.name}</span></td>
            <td>{flag(lb.location.country)} {lb.location.city ?? '—'}</td>
            <td class="right">{lb.usage.services}</td>
            <td><HealthCount healthy={lb.health.healthy} total={lb.health.total} /></td>
            <td class="right">{num(lb.usage.open_connections)}</td>
            <td><LabelChips labels={lb.labels} max={3} /></td>
          </tr>
        {:else}
          <tr><td colspan="10" class="muted nores">No load balancers match these filters.</td></tr>
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
  .ip6 { max-width: 200px; overflow: hidden; text-overflow: ellipsis; }
  :global(.lockic) { color: var(--muted); margin-left: 4px; }
  .nores { text-align: center; padding: 28px; }
</style>
