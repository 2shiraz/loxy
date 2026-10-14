<script lang="ts">
  import { page } from '$app/state';
  import { goto } from '$app/navigation';
  import { api, unwrap, mutate, type Backend } from '$lib/api/client';
  import { resource } from '$lib/stores/resource.svelte';
  import { setBe } from '$lib/stores/ctx';
  import { confirm } from '$lib/stores/confirm.svelte';
  import { toast } from '$lib/stores/toast.svelte';
  import { algorithmLabel } from '$lib/util/format';
  import Icon from '$lib/components/Icon.svelte';
  import Tabs from '$lib/components/Tabs.svelte';
  import Menu from '$lib/components/Menu.svelte';
  import HealthBadge from '$lib/components/HealthBadge.svelte';
  import Skeleton from '$lib/components/Skeleton.svelte';
  import ErrorBox from '$lib/components/ErrorBox.svelte';

  let { children } = $props();

  const id = $derived(Number(page.params.id));
  let override = $state<Backend | undefined>();
  const res = resource(async () => (await unwrap(api.GET('/backends/{id}', { params: { path: { id } } }))).backend, {
    deps: () => id,
    keys: () => [`be:${id}`, 'be:*']
  });
  $effect(() => { void res.data; override = undefined; });
  const be = $derived(override ?? res.data);
  const fes = resource(async () => (await unwrap(api.GET('/frontends'))).frontends, { keys: () => ['frontends'] });
  const users = $derived((fes.data ?? []).filter((f) => be?.used_by.includes(f.id)));

  setBe({
    get item() { return be; },
    get id() { return id; },
    reload: () => res.reload(),
    set: (v) => (override = v)
  });

  const base = $derived(`/backends/${id}`);
  const sub = $derived(page.url.pathname.split('/')[3] ?? 'servers');
  const tabs = $derived([
    { id: 'servers', label: 'Servers', href: base, count: be?.servers_count },
    { id: 'settings', label: 'Settings', href: `${base}/settings` },
    { id: 'graphs', label: 'Graphs', href: `${base}/graphs` }
  ]);

  async function remove() {
    if (!be) return;
    if (be.used_by.length) {
      toast.error(`${be.name} is used by ${be.used_by.length} frontend${be.used_by.length === 1 ? '' : 's'}. Point them at another backend first.`);
      return;
    }
    const ok = await confirm({ title: `Delete ${be.name}?`, message: 'The backend and its server list are removed. Hosts in the inventory stay. This cannot be undone.', confirmLabel: 'Delete backend', danger: true, typed: be.name });
    if (!ok) return;
    const r = await mutate(api.DELETE('/backends/{id}', { params: { path: { id } } }), { success: `${be.name} deleted` });
    if (r !== undefined) void goto('/backends');
  }
</script>

<svelte:head><title>{be?.name ?? 'Backend'} · Loxy</title></svelte:head>

<nav class="crumbs small" aria-label="Breadcrumb"><a href="/backends">Backends</a><Icon name="chevronRight" size={12} /><span aria-current="page">{be?.name ?? '…'}</span></nav>

{#if res.error && !be}
  <ErrorBox message={res.error} onretry={res.reload} />
{:else if !be}
  <Skeleton rows={3} height={20} />
{:else}
  <header class="dh">
    <div class="l1">
      <h1>{be.name}</h1>
      <span class="badge mode">{be.mode.toUpperCase()}</span>
      <HealthBadge status={be.health.status} />
      <span class="muted mono small">#{be.id}</span>
      <span class="spacer"></span>
      <Menu
        label="Actions"
        btnClass="btn"
        items={[
          { label: 'Edit settings', onclick: () => goto(`${base}/settings`) },
          { label: 'Copy ID', onclick: () => { void navigator.clipboard?.writeText(String(be!.id)); toast.info('ID copied'); } },
          { label: be.used_by.length ? 'Delete (in use)…' : 'Delete…', onclick: remove, danger: true, separator: true }
        ]}
      >
        {#snippet trigger()}Actions<Icon name="chevron" size={14} />{/snippet}
      </Menu>
    </div>
    <div class="l2 small">
      <span><span class="muted">Algorithm</span> {algorithmLabel(be.algorithm)}</span>
      <span><span class="muted">Port</span> <span class="mono">{be.port}</span></span>
      <span><span class="muted">Health check</span> <span class="mono">{be.health_check.protocol ?? 'tcp'}{be.health_check.http?.path && be.health_check.protocol !== 'tcp' ? ` ${be.health_check.http.path}` : ''} · {be.health_check.interval_s ?? 15}s</span></span>
      <span><span class="muted">Used by</span>
        {#if users.length}{#each users as f, i (f.id)}{i ? ', ' : ' '}<a href="/frontends/{f.id}">{f.name}</a>{/each}
        {:else if be.used_by.length}{be.used_by.length} frontend{be.used_by.length === 1 ? '' : 's'}
        {:else}<span class="muted">no frontend</span>{/if}
      </span>
    </div>
  </header>
  {#if be.health.messages.length}
    <div class="banner warn hm" role="status"><Icon name="alert" /><ul>{#each be.health.messages as m, i (i)}<li>{m}</li>{/each}</ul></div>
  {/if}

  <Tabs {tabs} active={sub} label="Backend sections" />
  <div class="tabbody">{@render children()}</div>
{/if}

<style>
  .dh { display: flex; flex-direction: column; gap: 8px; margin-bottom: 14px; }
  .l1 { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; }
  .l2 { display: flex; align-items: center; gap: 18px; flex-wrap: wrap; color: var(--text-2); }
  .hm { margin-bottom: 14px; }
  .hm ul { margin: 0; padding-left: 16px; }
  .hm :global(.icon) { color: var(--warn); margin-top: 2px; }
  .tabbody { padding-top: 20px; }
</style>
