<script lang="ts">
  import { page } from '$app/state';
  import { goto } from '$app/navigation';
  import { api, unwrap, mutate, getText, type Frontend, type Schemas } from '$lib/api/client';
  import { resource } from '$lib/stores/resource.svelte';
  import { setFe } from '$lib/stores/ctx';
  import { confirm } from '$lib/stores/confirm.svelte';
  import { toast } from '$lib/stores/toast.svelte';
  import { modeLabel, bindsText } from '$lib/util/format';
  import Icon from '$lib/components/Icon.svelte';
  import Tabs from '$lib/components/Tabs.svelte';
  import Menu from '$lib/components/Menu.svelte';
  import StatusDot from '$lib/components/StatusDot.svelte';
  import Skeleton from '$lib/components/Skeleton.svelte';
  import ErrorBox from '$lib/components/ErrorBox.svelte';
  import Modal from '$lib/components/Modal.svelte';
  import CodeView from '$lib/components/CodeView.svelte';

  let { children } = $props();

  const id = $derived(Number(page.params.id));
  let override = $state<Frontend | undefined>();
  const res = resource(async () => (await unwrap(api.GET('/frontends/{id}', { params: { path: { id } } }))).frontend, {
    deps: () => id,
    keys: () => [`fe:${id}`, 'fe:*']
  });
  $effect(() => { void res.data; override = undefined; });
  const fe = $derived(override ?? res.data);

  setFe({
    get item() { return fe; },
    get id() { return id; },
    reload: () => res.reload(),
    set: (v) => (override = v)
  });

  const base = $derived(`/frontends/${id}`);
  const sub = $derived(page.url.pathname.split('/')[3] ?? 'overview');
  const tabs = $derived([
    { id: 'overview', label: 'Overview', href: base },
    { id: 'graphs', label: 'Graphs', href: `${base}/graphs` },
    { id: 'logs', label: 'Logs', href: `${base}/logs` },
    { id: 'config', label: 'Config', href: `${base}/config` }
  ]);

  async function patch(body: Schemas['FrontendPatch'], msg?: string) {
    const r = await mutate(api.PATCH('/frontends/{id}', { params: { path: { id } }, body }), { success: msg });
    if (r) override = r.frontend;
    return r;
  }

  // Inline rename
  let editing = $state(false);
  let nameDraft = $state('');
  let nameInput: HTMLInputElement | undefined = $state();
  function startRename() {
    if (!fe) return;
    nameDraft = fe.name;
    editing = true;
    queueMicrotask(() => { nameInput?.focus(); nameInput?.select(); });
  }
  async function commitRename() {
    if (!fe || !editing) return;
    editing = false;
    const n = nameDraft.trim();
    if (!n || n === fe.name) return;
    const prev = fe;
    override = { ...fe, name: n };
    const r = await patch({ name: n }, 'Renamed');
    if (!r) override = prev;
  }

  async function toggleMaintenance() {
    if (!fe) return;
    const on = !fe.maintenance;
    const ok = await confirm({
      title: on ? 'Turn maintenance on?' : 'Turn maintenance off?',
      message: on
        ? `${fe.name} keeps listening but answers every request with a maintenance response (503) instead of forwarding to backends.`
        : `${fe.name} forwards traffic to its backends again.`,
      confirmLabel: on ? 'Turn maintenance on' : 'Turn maintenance off',
      danger: on
    });
    if (ok) await patch({ maintenance: on }, on ? 'Maintenance on' : 'Maintenance off');
  }
  async function toggleEnabled() {
    if (!fe) return;
    const on = !fe.enabled;
    if (!on) {
      const ok = await confirm({ title: `Disable ${fe.name}?`, message: `HAProxy stops listening on ${bindsText(fe.binds)}. Open connections are closed.`, confirmLabel: 'Disable frontend', danger: true });
      if (!ok) return;
    }
    await patch({ enabled: on }, on ? 'Frontend enabled' : 'Frontend disabled');
  }
  async function remove() {
    if (!fe) return;
    const ok = await confirm({
      title: `Delete ${fe.name}?`,
      message: `HAProxy stops listening on ${bindsText(fe.binds)}. Backends stay in place. This cannot be undone.`,
      confirmLabel: 'Delete frontend',
      danger: true,
      typed: fe.name
    });
    if (!ok) return;
    const r = await mutate(api.DELETE('/frontends/{id}', { params: { path: { id } } }), { success: `${fe.name} deleted` });
    if (r !== undefined) void goto('/frontends');
  }

  let cfgOpen = $state(false);
  let cfgText = $state('');
  async function showConfig() {
    cfgText = '';
    cfgOpen = true;
    try { cfgText = await getText(`/frontends/${id}/config`); } catch (e) { cfgOpen = false; toast.error((e as Error).message); }
  }

  async function retry() {
    const r = await mutate(api.POST('/apply/retry'), { success: 'Retrying apply…' });
    if (r !== undefined) void res.reload();
  }

  const statusLabel = $derived(fe ? (fe.status !== 'active' ? fe.status : fe.health.status) : 'unknown');
</script>

<svelte:head><title>{fe?.name ?? 'Frontend'} · Loxy</title></svelte:head>

<nav class="crumbs small" aria-label="Breadcrumb"><a href="/frontends">Frontends</a><Icon name="chevronRight" size={12} />
  {#if sub === 'edit'}<a href={base}>{fe?.name ?? '…'}</a><Icon name="chevronRight" size={12} /><span aria-current="page">Edit</span>
  {:else}<span aria-current="page">{fe?.name ?? '…'}</span>{/if}
</nav>

{#if res.error && !fe}
  <ErrorBox message={res.error} onretry={res.reload} />
{:else if !fe}
  <Skeleton rows={3} height={20} />
{:else}
  <header class="dh">
    <div class="l1">
      {#if editing}
        <input class="input name-in" aria-label="Frontend name" bind:this={nameInput} bind:value={nameDraft}
          onblur={commitRename} onkeydown={(e) => { if (e.key === 'Enter') commitRename(); if (e.key === 'Escape') editing = false; }} />
      {:else}
        <button class="name" onclick={startRename} title="Rename">{fe.name}</button>
      {/if}
      <span class="badge mode lg">{modeLabel(fe.mode, fe.tls)}</span>
      <StatusDot status={statusLabel} />
      <span class="muted mono small">#{fe.id}</span>
      {#if fe.maintenance}<span class="badge warn"><Icon name="pause" size={11} />Maintenance</span>{/if}
      {#if !fe.enabled}<span class="badge">Disabled</span>{/if}
      {#if fe.status === 'applying'}<span class="badge warn"><Icon name="refresh" size={11} />Applying</span>{/if}
      <span class="spacer"></span>
      {#if sub !== 'edit'}<a class="btn" href="{base}/edit"><Icon name="edit" size={14} />Edit</a>{/if}
      <Menu
        label="Actions"
        btnClass="btn"
        items={[
          { label: 'Edit', onclick: () => goto(`${base}/edit`) },
          { label: 'Rename', onclick: startRename },
          { label: fe.maintenance ? 'Turn maintenance off' : 'Turn maintenance on', onclick: toggleMaintenance, separator: true },
          { label: fe.enabled ? 'Disable' : 'Enable', onclick: toggleEnabled },
          { label: 'View config', onclick: showConfig, separator: true },
          { label: 'Copy ID', onclick: () => { void navigator.clipboard?.writeText(String(fe!.id)); toast.info('ID copied'); } },
          { label: 'Delete…', onclick: remove, danger: true, separator: true }
        ]}
      >
        {#snippet trigger()}Actions<Icon name="chevron" size={14} />{/snippet}
      </Menu>
    </div>
    <div class="l2 small">
      <span><span class="muted">Binds</span> <span class="mono">{bindsText(fe.binds)}</span></span>
      {#if fe.description}<span class="muted">{fe.description}</span>{/if}
    </div>
  </header>

  {#if fe.status === 'apply_error'}
    <div class="banner err applyerr" role="alert">
      <Icon name="alert" />
      <div class="spacer stack" style:gap="6px">
        <b>The last change could not be applied. HAProxy is still running the previous configuration.</b>
        {#if fe.last_error}<CodeView text={fe.last_error} maxHeight={180} label="HAProxy output" />{/if}
      </div>
      <div class="row"><button class="btn sm" onclick={retry}><Icon name="refresh" size={13} />Retry</button><a class="btn sm ghost" href="/system#revisions">Revisions</a></div>
    </div>
  {/if}

  {#if sub !== 'edit'}<Tabs {tabs} active={sub} label="Frontend sections" />{/if}
  <div class="tabbody">{@render children()}</div>
{/if}

<Modal bind:open={cfgOpen} title="Config · {fe?.name ?? ''}" width={820}>
  {#if cfgText}<CodeView text={cfgText} label="HAProxy configuration" />{:else}<Skeleton rows={8} />{/if}
  <p class="muted xs" style:margin-top="8px">Read-only. The sections rendered for this frontend.</p>
  {#snippet footer()}
    <button class="btn" onclick={() => (cfgOpen = false)}>Close</button>
  {/snippet}
</Modal>

<style>
  .dh { display: flex; flex-direction: column; gap: 8px; margin-bottom: 14px; }
  .l1 { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; }
  .badge.lg { height: 22px; font-size: var(--fs-xs); padding: 0 7px; }
  .name {
    font-size: var(--fs-2xl); font-weight: 600; letter-spacing: -0.01em; background: none; border: 0; padding: 0;
    cursor: text; text-decoration: underline; text-decoration-color: transparent; text-underline-offset: 5px; text-decoration-thickness: 1px;
  }
  .name:hover { text-decoration-color: var(--muted); }
  .name-in { font-size: var(--fs-xl); height: 36px; width: min(360px, 100%); font-weight: 600; }
  .l2 { display: flex; align-items: center; gap: 18px; flex-wrap: wrap; color: var(--text-2); }
  .applyerr { margin-bottom: 14px; }
  .applyerr :global(.icon) { color: var(--err); margin-top: 2px; }
  .tabbody { padding-top: 20px; }
</style>
