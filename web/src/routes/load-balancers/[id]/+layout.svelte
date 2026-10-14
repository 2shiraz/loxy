<script lang="ts">
  import { page } from '$app/state';
  import { goto } from '$app/navigation';
  import { api, unwrap, mutate, getText, type LoadBalancer, type Labels } from '$lib/api/client';
  import { resource } from '$lib/stores/resource.svelte';
  import { setLb } from '$lib/stores/lbctx';
  import { confirm } from '$lib/stores/confirm.svelte';
  import { toast } from '$lib/stores/toast.svelte';
  import { flag } from '$lib/util/format';
  import Icon from '$lib/components/Icon.svelte';
  import Tabs from '$lib/components/Tabs.svelte';
  import Menu from '$lib/components/Menu.svelte';
  import CopyText from '$lib/components/CopyText.svelte';
  import StatusDot from '$lib/components/StatusDot.svelte';
  import Skeleton from '$lib/components/Skeleton.svelte';
  import ErrorBox from '$lib/components/ErrorBox.svelte';
  import Modal from '$lib/components/Modal.svelte';
  import LabelEditor from '$lib/components/LabelEditor.svelte';
  import CodeView from '$lib/components/CodeView.svelte';

  let { children } = $props();

  const id = $derived(Number(page.params.id));
  let override = $state<LoadBalancer | undefined>();
  const res = resource(async () => (await unwrap(api.GET('/load_balancers/{id}', { params: { path: { id } } }))).load_balancer, {
    deps: () => id,
    keys: () => [`lb:${id}`]
  });
  $effect(() => { void res.data; override = undefined; });
  const lb = $derived(override ?? res.data);

  setLb({
    get lb() { return lb; },
    get id() { return id; },
    reload: () => res.reload(),
    set: (v) => (override = v)
  });

  const tab = $derived(page.url.pathname.split('/')[3] ?? 'overview');
  const tabs = $derived([
    { id: 'overview', label: 'Overview' },
    { id: 'graphs', label: 'Graphs' },
    { id: 'targets', label: 'Targets' },
    { id: 'services', label: 'Services' },
    { id: 'networking', label: 'Networking' },
    { id: 'rescale', label: 'Rescale' },
    { id: 'delete', label: 'Delete' }
  ].map((t) => ({ ...t, href: `/load-balancers/${id}/${t.id}` })));

  async function patch(body: Record<string, unknown>, msg?: string) {
    const r = await mutate(api.PATCH('/load_balancers/{id}', { params: { path: { id } }, body }), { success: msg });
    if (r) override = r.load_balancer;
    return r;
  }

  // Inline rename
  let editing = $state(false);
  let nameDraft = $state('');
  let nameInput: HTMLInputElement | undefined = $state();
  function startRename() {
    if (!lb) return;
    nameDraft = lb.name;
    editing = true;
    queueMicrotask(() => { nameInput?.focus(); nameInput?.select(); });
  }
  async function commitRename() {
    if (!lb || !editing) return;
    editing = false;
    const n = nameDraft.trim();
    if (!n || n === lb.name) return;
    const prev = lb;
    override = { ...lb, name: n }; // optimistic
    const r = await patch({ name: n }, 'Renamed');
    if (!r) override = prev;
  }

  async function toggleProtection() {
    if (!lb) return;
    if (lb.delete_protected) {
      const ok = await confirm({ title: 'Disable delete protection?', message: `${lb.name} can then be deleted.`, confirmLabel: 'Disable protection', danger: true });
      if (!ok) return;
    }
    await patch({ delete_protected: !lb.delete_protected }, lb.delete_protected ? 'Delete protection disabled' : 'Delete protection enabled');
  }

  async function toggleMaintenance() {
    if (!lb) return;
    const on = !lb.maintenance;
    const ok = await confirm({
      title: on ? 'Enable maintenance mode?' : 'Disable maintenance mode?',
      message: on ? 'All services will answer with a maintenance response and stop forwarding to targets.' : 'Traffic will be forwarded to targets again.',
      confirmLabel: on ? 'Enable maintenance' : 'Disable maintenance',
      danger: on
    });
    if (ok) await patch({ maintenance: on }, on ? 'Maintenance mode on' : 'Maintenance mode off');
  }

  let labelsOpen = $state(false);
  let labelsDraft = $state<Labels>({});
  let labelsErr = $state('');
  async function saveLabels() {
    if (labelsErr) return;
    const r = await patch({ labels: labelsDraft }, 'Labels saved');
    if (r) labelsOpen = false;
  }

  let cfgOpen = $state(false);
  let cfgText = $state('');
  async function showConfig() {
    cfgText = '';
    cfgOpen = true;
    try { cfgText = await getText(`/load_balancers/${id}/config`); } catch (e) { cfgOpen = false; toast.error((e as Error).message); }
  }
  function downloadConfig() {
    const a = document.createElement('a');
    a.href = URL.createObjectURL(new Blob([cfgText], { type: 'text/plain' }));
    a.download = `${lb?.name ?? 'lb'}-haproxy.cfg`;
    a.click();
    URL.revokeObjectURL(a.href);
  }

  async function retry() {
    const r = await mutate(api.POST('/load_balancers/{id}/actions/retry_apply', { params: { path: { id } } }), { success: 'Retrying apply…' });
    if (r !== undefined) void res.reload();
  }

  const statusLabel = $derived(lb ? (lb.status === 'active' ? lb.health.status : lb.status) : 'unknown');
</script>

<svelte:head><title>{lb?.name ?? 'Load Balancer'} · Loxy</title></svelte:head>

<nav class="crumbs small" aria-label="Breadcrumb"><a href="/load-balancers">Load Balancers</a><Icon name="chevronRight" size={12} /><span aria-current="page">{lb?.name ?? '…'}</span></nav>

{#if res.error && !lb}
  <ErrorBox message={res.error} onretry={res.reload} />
{:else if !lb}
  <Skeleton rows={3} height={20} />
{:else}
  <header class="lbh">
    <div class="l1">
      <span class="badge plan lg">{lb.plan.name}</span>
      {#if editing}
        <input class="input name-in" aria-label="Load balancer name" bind:this={nameInput} bind:value={nameDraft}
          onblur={commitRename} onkeydown={(e) => { if (e.key === 'Enter') commitRename(); if (e.key === 'Escape') editing = false; }} />
      {:else}
        <button class="name" onclick={startRename} title="Rename">{lb.name}</button>
      {/if}
      <StatusDot status={statusLabel} />
      <span class="muted mono small">#{lb.id}</span>
      <button class="btn ghost icon sm lock" class:on={lb.delete_protected} onclick={toggleProtection}
        aria-pressed={lb.delete_protected} aria-label={lb.delete_protected ? 'Delete protection on — disable' : 'Delete protection off — enable'}
        title={lb.delete_protected ? 'Delete protection is on' : 'Delete protection is off'}>
        <Icon name={lb.delete_protected ? 'lock' : 'unlock'} size={15} />
      </button>
      <span class="spacer"></span>
      <Menu
        label="Actions"
        btnClass="btn"
        items={[
          { label: 'Rename', onclick: startRename },
          { label: 'Edit labels', onclick: () => { labelsDraft = { ...lb!.labels }; labelsOpen = true; } },
          { label: lb.delete_protected ? 'Disable delete protection' : 'Enable delete protection', onclick: toggleProtection },
          { label: lb.maintenance ? 'Maintenance mode off' : 'Maintenance mode on', onclick: toggleMaintenance },
          { label: 'View HAProxy config', onclick: showConfig, separator: true },
          { label: 'Copy ID', onclick: () => { void navigator.clipboard?.writeText(String(lb!.id)); toast.info('ID copied'); } }
        ]}
      >
        {#snippet trigger()}Actions<Icon name="chevron" size={14} />{/snippet}
      </Menu>
    </div>
    <div class="l2 small">
      {#if lb.public_net.enabled}
        <span><span class="muted">IPv4</span> <CopyText text={lb.public_net.ipv4} label="IPv4 address" /></span>
        <span><span class="muted">IPv6</span> <CopyText text={lb.public_net.ipv6} label="IPv6 address" /></span>
      {:else}
        <span class="muted">Public interface disabled</span>
      {/if}
      <span>{flag(lb.location.country)} {lb.location.city ?? ''}{lb.location.network_zone ? ` · ${lb.location.network_zone}` : ''}</span>
      {#if lb.maintenance}<span class="badge warn"><Icon name="pause" size={11} />Maintenance</span>{/if}
      {#if lb.status === 'applying'}<span class="badge warn"><Icon name="refresh" size={11} />Applying</span>{/if}
    </div>
  </header>

  {#if lb.status === 'apply_error'}
    <div class="banner err applyerr" role="alert">
      <Icon name="alert" />
      <div class="spacer stack" style:gap="6px">
        <b>The last change could not be applied. HAProxy is still running the previous configuration.</b>
        {#if lb.last_error}<CodeView text={lb.last_error} maxHeight={180} label="HAProxy output" />{/if}
      </div>
      <div class="row"><button class="btn sm" onclick={retry}><Icon name="refresh" size={13} />Retry</button><a class="btn sm ghost" href="/activity?tab=revisions">Revisions</a></div>
    </div>
  {/if}

  <Tabs {tabs} active={tab} label="Load balancer sections" />
  <div class="tabbody">{@render children()}</div>
{/if}

<Modal bind:open={labelsOpen} title="Edit labels">
  <LabelEditor bind:labels={labelsDraft} bind:error={labelsErr} />
  {#snippet footer()}
    <button class="btn" onclick={() => (labelsOpen = false)}>Cancel</button>
    <button class="btn primary" disabled={!!labelsErr} onclick={saveLabels}>Save labels</button>
  {/snippet}
</Modal>

<Modal bind:open={cfgOpen} title="HAProxy config · {lb?.name ?? ''}" width={820}>
  {#if cfgText}<CodeView text={cfgText} label="HAProxy configuration" />{:else}<Skeleton rows={8} />{/if}
  <p class="muted xs" style:margin-top="8px">Read-only. These are the sections rendered for this load balancer only.</p>
  {#snippet footer()}
    <button class="btn" onclick={() => (cfgOpen = false)}>Close</button>
    <button class="btn primary" disabled={!cfgText} onclick={downloadConfig}><Icon name="download" size={14} />Download</button>
  {/snippet}
</Modal>

<style>
  .crumbs { display: flex; align-items: center; gap: 6px; color: var(--muted); margin-bottom: 10px; }
  .crumbs a { color: var(--muted); text-decoration: none; }
  .crumbs a:hover { color: var(--text); }
  .lbh { display: flex; flex-direction: column; gap: 8px; margin-bottom: 14px; }
  .l1 { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; }
  .badge.lg { height: 24px; font-size: var(--fs-sm); padding: 0 8px; }
  .name {
    font-size: var(--fs-2xl); font-weight: 600; letter-spacing: -0.01em; background: none; border: 0; padding: 0;
    cursor: text; text-decoration: underline; text-decoration-color: transparent; text-underline-offset: 5px; text-decoration-thickness: 1px;
  }
  .name:hover { text-decoration-color: var(--muted); }
  .name-in { font-size: var(--fs-xl); height: 36px; width: min(360px, 100%); font-weight: 600; }
  .lock { color: var(--muted); }
  .lock.on { color: var(--text); }
  .l2 { display: flex; align-items: center; gap: 18px; flex-wrap: wrap; color: var(--text-2); }
  .applyerr { margin-bottom: 14px; }
  .applyerr :global(.icon) { color: var(--err); margin-top: 2px; }
  .tabbody { padding-top: 20px; }
</style>
