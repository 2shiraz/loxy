<script lang="ts">
  import { goto } from '$app/navigation';
  import { api, unwrap, type Frontend } from '$lib/api/client';
  import { resource } from '$lib/stores/resource.svelte';
  import { matchSelector } from '$lib/util/validate';
  import { num, bindsText, modeLabel } from '$lib/util/format';
  import Icon from '$lib/components/Icon.svelte';
  import LabelChips from '$lib/components/LabelChips.svelte';
  import Skeleton from '$lib/components/Skeleton.svelte';
  import EmptyState from '$lib/components/EmptyState.svelte';
  import ErrorBox from '$lib/components/ErrorBox.svelte';
  import StatusDot from '$lib/components/StatusDot.svelte';

  const fes = resource(async () => (await unwrap(api.GET('/frontends'))).frontends, { keys: () => ['frontends'] });
  const bes = resource(async () => (await unwrap(api.GET('/backends'))).backends, { keys: () => ['backends'] });
  const beName = (id: number | null) => (id == null ? null : (bes.data?.find((b) => b.id === id)?.name ?? `#${id}`));

  let q = $state('');
  const rows = $derived.by(() => {
    const t = q.trim();
    const list = (fes.data ?? []).filter((f) => {
      if (!t) return true;
      if (t.includes('=')) return matchSelector(f.labels, t);
      const l = t.toLowerCase();
      return f.name.toLowerCase().includes(l) || bindsText(f.binds).includes(l);
    });
    return [...list].sort((a, b) => a.name.localeCompare(b.name, undefined, { numeric: true }));
  });

  const status = (f: Frontend) => (f.status !== 'active' ? f.status : !f.enabled ? 'unknown' : f.health.status);
  function open(f: Frontend) { void goto(`/frontends/${f.id}`); }
</script>

<svelte:head><title>Frontends · Loxy</title></svelte:head>

<div class="page-head">
  <h1>Frontends</h1>
  <a class="btn primary" href="/frontends/new"><Icon name="plus" size={14} />New frontend</a>
</div>

{#if fes.data?.length}
  <label class="search">
    <Icon name="search" size={14} /><span class="sr-only">Filter frontends</span>
    <input class="input" type="search" placeholder="Filter by name, bind, or env=prod" bind:value={q} spellcheck="false" autocomplete="off" />
  </label>
{/if}

{#if fes.error && !fes.data}
  <ErrorBox message={fes.error} onretry={fes.reload} />
{:else if !fes.data}
  <Skeleton card rows={6} height={18} />
{:else if fes.data.length === 0}
  <div class="card">
    <EmptyState icon="frontend" title="No frontends yet" text="A frontend is where HAProxy accepts traffic: the addresses and ports it binds, TLS, and the rules that pick a backend.">
      <a class="btn primary" href="/frontends/new"><Icon name="plus" size={14} />New frontend</a>
      {#if bes.data && !bes.data.length}<a class="btn" href="/backends/new">Create a backend first</a>{/if}
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
          <th>Binds</th>
          <th>Default backend</th>
          <th class="right">Routes</th>
          <th class="right">Connections</th>
          <th>Labels</th>
        </tr>
      </thead>
      <tbody>
        {#each rows as f (f.id)}
          <tr class="clickable" onclick={() => open(f)}>
            <td><StatusDot status={status(f)} /></td>
            <td>
              <a class="name" href="/frontends/{f.id}" onclick={(e) => e.stopPropagation()}>{f.name}</a>
              {#if f.maintenance}<span class="badge warn"><Icon name="pause" size={10} />Maintenance</span>{/if}
              {#if !f.enabled}<span class="badge">Disabled</span>{/if}
              {#if f.status === 'apply_error'}<span class="badge err">Apply error</span>{/if}
            </td>
            <td><span class="badge mode">{modeLabel(f.mode, f.tls)}</span></td>
            <td class="mono small binds" title={bindsText(f.binds)}>{bindsText(f.binds)}</td>
            <td>
              {#if f.default_backend_id != null}
                <a href="/backends/{f.default_backend_id}" onclick={(e) => e.stopPropagation()}>{beName(f.default_backend_id)}</a>
              {:else}<span class="muted">—</span>{/if}
            </td>
            <td class="right">{f.routes.length || '—'}</td>
            <td class="right">{num(f.stats.open_connections)}</td>
            <td><LabelChips labels={f.labels} max={3} /></td>
          </tr>
        {:else}
          <tr><td colspan="8" class="muted nores">No frontends match “{q}”.</td></tr>
        {/each}
      </tbody>
    </table>
  </div>
{/if}

<style>
  .search { position: relative; display: flex; align-items: center; max-width: 380px; margin-bottom: 14px; }
  .search :global(.icon) { position: absolute; left: 10px; color: var(--muted); pointer-events: none; }
  .search .input { padding-left: 30px; }
  .name { font-weight: 500; text-decoration: none; margin-right: 6px; }
  .name:hover { text-decoration: underline; }
  .binds { max-width: 260px; overflow: hidden; text-overflow: ellipsis; }
  .nores { text-align: center; padding: 28px; }
</style>
