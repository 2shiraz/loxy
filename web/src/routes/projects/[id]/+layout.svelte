<script lang="ts">
  import { page } from '$app/state';
  import { goto } from '$app/navigation';
  import { api, unwrap, mutate, getText, type Project, type ProjectUpdate } from '$lib/api/client';
  import { resource } from '$lib/stores/resource.svelte';
  import { setProject } from '$lib/stores/project';
  import { confirm } from '$lib/stores/confirm.svelte';
  import { toast } from '$lib/stores/toast.svelte';
  import { bump, withLive, live } from '$lib/stores/live.svelte';
  import { projectStatus, compact, rate, isWebUrl } from '$lib/util/format';
  import LiveNumber from '$lib/components/LiveNumber.svelte';
  import StatusDot from '$lib/components/StatusDot.svelte';
  import Icon from '$lib/components/Icon.svelte';
  import Tabs from '$lib/components/Tabs.svelte';
  import Menu from '$lib/components/Menu.svelte';
  import HealthBadge from '$lib/components/HealthBadge.svelte';
  import Skeleton from '$lib/components/Skeleton.svelte';
  import ErrorBox from '$lib/components/ErrorBox.svelte';
  import Modal from '$lib/components/Modal.svelte';
  import CodeView from '$lib/components/CodeView.svelte';
  import Endpoints from '$lib/components/Endpoints.svelte';

  let { children } = $props();

  const id = $derived(Number(page.params.id));
  let override = $state<Project | undefined>();
  const res = resource(async () => (await unwrap(api.GET('/projects/{id}', { params: { path: { id } } }))).project, {
    deps: () => id,
    keys: () => [`p:${id}`, 'p:*']
  });
  $effect(() => { void res.data; override = undefined; });
  // Status, health and traffic come from the 2 s live tick; a changed health_sig/version
  // bumps `p:<id>` in the live store, which refetches this resource. No polling.
  const raw = $derived(override ?? res.data);
  const p = $derived(raw && live.tick ? withLive(raw) : raw);
  const openUrl = $derived(p ? ([...p.endpoints].sort((a, b) => (a.kind === b.kind ? 0 : a.kind === 'domain' ? -1 : 1)).find((e) => isWebUrl(e.url))?.url) : undefined);

  setProject({
    get project() { return p; },
    get id() { return id; },
    reload: () => res.reload(),
    set: (v) => (override = v)
  });

  const base = $derived(`/projects/${id}`);
  const tab = $derived(page.url.pathname.split('/')[3] ?? 'overview');
  const tabs = $derived([
    { id: 'overview', label: 'Overview' },
    { id: 'graphs', label: 'Graphs' },
    { id: 'targets', label: 'Targets', count: p?.counts.targets },
    { id: 'services', label: 'Services', count: p?.counts.services },
    { id: 'domains', label: 'Domains', count: p?.counts.domains },
    { id: 'logs', label: 'Logs' },
    { id: 'settings', label: 'Settings' }
  ].map((t) => ({ ...t, href: `${base}/${t.id}` })));

  async function patch(body: ProjectUpdate, msg?: string) {
    const r = await mutate(api.PATCH('/projects/{id}', { params: { path: { id } }, body }), { success: msg });
    if (r) { override = r.project; bump('projects'); }
    return r;
  }

  // Inline rename
  let editing = $state(false);
  let nameDraft = $state('');
  let nameInput: HTMLInputElement | undefined = $state();
  function startRename() {
    if (!p) return;
    nameDraft = p.name;
    editing = true;
    queueMicrotask(() => { nameInput?.focus(); nameInput?.select(); });
  }
  async function commitRename() {
    if (!p || !editing) return;
    editing = false;
    const n = nameDraft.trim();
    if (!n || n === p.name) return;
    const prev = p;
    override = { ...p, name: n }; // optimistic
    const r = await patch({ name: n }, 'Renamed');
    if (!r) override = prev;
  }

  async function toggleMaintenance() {
    if (!p) return;
    const on = !p.maintenance;
    const ok = await confirm({
      title: on ? 'Turn maintenance on?' : 'Turn maintenance off?',
      message: on
        ? `${p.name} keeps listening but answers every request with 503 instead of forwarding to targets.`
        : `${p.name} forwards traffic to its targets again.`,
      confirmLabel: on ? 'Turn maintenance on' : 'Turn maintenance off',
      danger: on
    });
    if (ok) await patch({ maintenance: on }, on ? 'Maintenance on' : 'Maintenance off');
  }

  async function remove() {
    if (!p) return;
    const ok = await confirm({
      title: `Delete ${p.name}?`,
      message: `All services stop listening and its ${p.counts.domains ? `${p.counts.domains} domain${p.counts.domains === 1 ? '' : 's'} and ` : ''}targets are removed. Hosts and certificates are kept. This cannot be undone.`,
      confirmLabel: 'Delete project',
      danger: true,
      typed: p.name
    });
    if (!ok) return;
    const r = await mutate(api.DELETE('/projects/{id}', { params: { path: { id } } }), { success: `${p.name} deleted` });
    if (r !== undefined) { bump('projects', 'overview'); void goto('/projects'); }
  }

  let cfgOpen = $state(false);
  let cfgText = $state('');
  async function showConfig() {
    cfgText = '';
    cfgOpen = true;
    try { cfgText = await getText(`/projects/${id}/config`); } catch (e) { cfgOpen = false; toast.error((e as Error).message); }
  }
  function downloadConfig() {
    const a = document.createElement('a');
    a.href = URL.createObjectURL(new Blob([cfgText], { type: 'text/plain' }));
    a.download = `${p?.name ?? 'project'}-haproxy.cfg`;
    a.click();
    URL.revokeObjectURL(a.href);
  }

  let retrying = $state(false);
  async function retry() {
    retrying = true;
    const r = await mutate(api.POST('/apply/retry'), { success: 'Retrying apply…' });
    retrying = false;
    if (r !== undefined) void res.reload();
  }
</script>

<svelte:head><title>{p?.name ?? 'Project'} · Loxy</title></svelte:head>

<nav class="crumbs small" aria-label="Breadcrumb"><a href="/projects">Projects</a><Icon name="chevronRight" size={12} /><span aria-current="page">{p?.name ?? '…'}</span></nav>

{#if res.error && !p}
  <ErrorBox message={res.error} onretry={res.reload} />
{:else if !p}
  <Skeleton rows={3} height={20} />
{:else}
  <header class="ph">
    <div class="l1">
      <StatusDot status={projectStatus(p)} />
      {#if editing}
        <input class="input name-in" aria-label="Project name" bind:this={nameInput} bind:value={nameDraft}
          onblur={commitRename} onkeydown={(e) => { if (e.key === 'Enter') commitRename(); if (e.key === 'Escape') editing = false; }} />
      {:else}
        <h1 class="name"><button onclick={startRename} title="Rename">{p.name}</button></h1>
      {/if}
      <HealthBadge status={projectStatus(p)} />
      {#if p.maintenance}<span class="badge warn"><Icon name="pause" size={11} />Maintenance</span>{/if}
      <span class="spacer"></span>
      <div class="acts">
        {#if openUrl}<a class="btn" href={openUrl} target="_blank" rel="noopener noreferrer"><Icon name="external" size={14} />Open</a>{/if}
        <Menu
          label="Actions"
          btnClass="btn"
          items={[
            { label: 'Rename', onclick: startRename },
            { label: p.maintenance ? 'Turn maintenance off' : 'Turn maintenance on', onclick: toggleMaintenance },
            { label: 'View HAProxy config', onclick: showConfig },
            { label: 'Delete…', onclick: remove, danger: true, separator: true }
          ]}
        >
          {#snippet trigger()}Actions<Icon name="chevron" size={14} />{/snippet}
        </Menu>
      </div>
    </div>
    <div class="l2 small">
      <span class="lv" title="Updated every 2 seconds"><LiveNumber value={p.stats.requests_per_second} format={rate} class="lvn" /> req/s</span>
      <span class="lv"><LiveNumber value={p.stats.open_connections} format={compact} class="lvn" /> open connections</span>
      {#if tab !== 'overview'}
        {#if p.endpoints.length}
          <span class="sep" aria-hidden="true"></span>
          <span class="eps"><Endpoints endpoints={p.endpoints} full /></span>
        {:else}
          <span class="muted">Not reachable yet. <a href="{base}/services">Add a service</a>.</span>
        {/if}
      {/if}
      {#if p.description}<span class="muted desc">{p.description}</span>{/if}
    </div>
  </header>

  {#if p.status === 'error'}
    <div class="banner err applyerr" role="alert">
      <Icon name="alert" />
      <div class="spacer stack" style:gap="6px">
        <b>The last change could not be applied. HAProxy is still running the previous configuration.</b>
        {#if p.last_error}<CodeView text={p.last_error} maxHeight={180} label="HAProxy output" />{/if}
      </div>
      <div class="row"><button class="btn sm" onclick={retry} disabled={retrying}><Icon name="refresh" size={13} />{retrying ? 'Retrying…' : 'Retry'}</button><a class="btn sm ghost" href="/system">Revisions</a></div>
    </div>
  {/if}

  <Tabs {tabs} active={tab} label="Project sections" />
  <div class="tabbody">{@render children()}</div>
{/if}

<Modal bind:open={cfgOpen} title="HAProxy config · {p?.name ?? ''}" width={820}>
  {#if cfgText}<CodeView text={cfgText} label="HAProxy configuration" />{:else}<Skeleton rows={8} />{/if}
  <p class="muted xs" style:margin-top="8px">Read-only. These are the sections rendered for this project.</p>
  {#snippet footer()}
    <button class="btn" onclick={() => (cfgOpen = false)}>Close</button>
    <button class="btn primary" disabled={!cfgText} onclick={downloadConfig}><Icon name="download" size={14} />Download</button>
  {/snippet}
</Modal>

<style>
  .ph { display: flex; flex-direction: column; gap: 10px; margin-bottom: 18px; }
  .l1 { display: flex; align-items: center; gap: 10px 12px; flex-wrap: wrap; min-width: 0; }
  .l1 > :global(.sd svg) { width: 14px; height: 14px; }
  .name { margin: 0; font-size: var(--fs-3xl); font-weight: 650; letter-spacing: -0.02em; min-width: 0; max-width: 100%; }
  .name button {
    font: inherit; color: inherit; background: none; border: 0; padding: 0; cursor: text; max-width: 100%;
    overflow: hidden; text-overflow: ellipsis; white-space: nowrap; letter-spacing: inherit;
    text-decoration: underline; text-decoration-color: transparent; text-underline-offset: 6px; text-decoration-thickness: 1px;
    transition: text-decoration-color 120ms ease;
  }
  .name button:hover { text-decoration-color: var(--muted); }
  .name-in { font-size: var(--fs-xl); height: 40px; width: min(360px, 100%); font-weight: 600; }
  .acts { display: flex; gap: 8px; }
  .l2 { display: flex; align-items: center; gap: 8px 16px; flex-wrap: wrap; color: var(--text-2); min-width: 0; }
  .lv { display: inline-flex; align-items: baseline; gap: 5px; color: var(--muted); white-space: nowrap; }
  .lv :global(.lvn) { color: var(--text); font-weight: 600; }
  .sep { width: 1px; height: 16px; background: var(--border-strong); }
  .desc { border-left: 1px solid var(--border); padding-left: 12px; }
  .applyerr { margin-bottom: 16px; }
  .applyerr :global(.icon) { color: var(--err); margin-top: 2px; }
  .tabbody { padding-top: 24px; }
  @media (max-width: 699px) {
    .name { font-size: var(--fs-2xl); }
    .acts { width: 100%; }
    .acts > :global(*) { flex: 1; }
    .acts :global(.menu-root > .btn) { width: 100%; }
    .sep, .eps { display: none; }
    .tabbody { padding-top: 18px; }
  }
</style>
