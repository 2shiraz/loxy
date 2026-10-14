<script lang="ts">
  import { goto } from '$app/navigation';
  import { api, unwrap, type Backend } from '$lib/api/client';
  import { resource } from '$lib/stores/resource.svelte';
  import { matchSelector } from '$lib/util/validate';
  import { algorithmLabel, num } from '$lib/util/format';
  import Icon from '$lib/components/Icon.svelte';
  import LabelChips from '$lib/components/LabelChips.svelte';
  import HealthCount from '$lib/components/HealthCount.svelte';
  import Skeleton from '$lib/components/Skeleton.svelte';
  import EmptyState from '$lib/components/EmptyState.svelte';
  import ErrorBox from '$lib/components/ErrorBox.svelte';
  import StatusDot from '$lib/components/StatusDot.svelte';

  const bes = resource(async () => (await unwrap(api.GET('/backends'))).backends, { keys: () => ['backends'] });

  let q = $state('');
  const rows = $derived.by(() => {
    const t = q.trim();
    const list = (bes.data ?? []).filter((b) => {
      if (!t) return true;
      if (t.includes('=')) return matchSelector(b.labels, t);
      return b.name.toLowerCase().includes(t.toLowerCase());
    });
    return [...list].sort((a, b) => a.name.localeCompare(b.name, undefined, { numeric: true }));
  });
  function open(b: Backend) { void goto(`/backends/${b.id}`); }
</script>

<svelte:head><title>Backends · Loxy</title></svelte:head>

<div class="page-head">
  <h1>Backends</h1>
  <a class="btn primary" href="/backends/new"><Icon name="plus" size={14} />New backend</a>
</div>

{#if bes.data?.length}
  <label class="search">
    <Icon name="search" size={14} /><span class="sr-only">Filter backends</span>
    <input class="input" type="search" placeholder="Filter by name, or env=prod" bind:value={q} spellcheck="false" autocomplete="off" />
  </label>
{/if}

{#if bes.error && !bes.data}
  <ErrorBox message={bes.error} onretry={bes.reload} />
{:else if !bes.data}
  <Skeleton card rows={6} height={18} />
{:else if bes.data.length === 0}
  <div class="card">
    <EmptyState icon="backend" title="No backends yet" text="A backend is a pool of servers HAProxy balances across, with its own algorithm and health check.">
      <a class="btn primary" href="/backends/new"><Icon name="plus" size={14} />New backend</a>
    </EmptyState>
  </div>
{:else}
  <div class="table-wrap">
    <table class="tbl">
      <thead>
        <tr>
          <th style:width="28px"><span class="sr-only">Status</span></th>
          <th>Name</th>
          <th>Mode</th>
          <th>Algorithm</th>
          <th class="right">Port</th>
          <th>Servers</th>
          <th class="right">Sessions</th>
          <th>Used by</th>
          <th>Labels</th>
        </tr>
      </thead>
      <tbody>
        {#each rows as b (b.id)}
          <tr class="clickable" onclick={() => open(b)}>
            <td><StatusDot status={b.health.status} /></td>
            <td><a class="name" href="/backends/{b.id}" onclick={(e) => e.stopPropagation()}>{b.name}</a></td>
            <td><span class="badge mode">{b.mode.toUpperCase()}</span></td>
            <td class="small">{algorithmLabel(b.algorithm)}</td>
            <td class="right mono small">{b.port}</td>
            <td><HealthCount healthy={b.health.healthy} total={b.health.total} title="{b.health.healthy} of {b.health.total} servers healthy" /></td>
            <td class="right">{num(b.stats.sessions)}</td>
            <td class="small">{#if b.used_by.length}{b.used_by.length} frontend{b.used_by.length === 1 ? '' : 's'}{:else}<span class="muted">Unused</span>{/if}</td>
            <td><LabelChips labels={b.labels} max={3} /></td>
          </tr>
        {:else}
          <tr><td colspan="9" class="muted nores">No backends match “{q}”.</td></tr>
        {/each}
      </tbody>
    </table>
  </div>
{/if}

<style>
  .search { position: relative; display: flex; align-items: center; max-width: 380px; margin-bottom: 14px; }
  .search :global(.icon) { position: absolute; left: 10px; color: var(--muted); pointer-events: none; }
  .search .input { padding-left: 30px; }
  .name { font-weight: 500; text-decoration: none; }
  .name:hover { text-decoration: underline; }
  .nores { text-align: center; padding: 28px; }
</style>
