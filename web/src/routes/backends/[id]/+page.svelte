<script lang="ts">
  import { api, unwrap, mutate, type BackendServer, type BackendServerInput, type ResolvedServer } from '$lib/api/client';
  import { getBe } from '$lib/stores/ctx';
  import { resource } from '$lib/stores/resource.svelte';
  import { confirm } from '$lib/stores/confirm.svelte';
  import { num } from '$lib/util/format';
  import Icon from '$lib/components/Icon.svelte';
  import Menu, { type MenuItem } from '$lib/components/Menu.svelte';
  import Modal from '$lib/components/Modal.svelte';
  import TargetPicker from '$lib/components/TargetPicker.svelte';
  import HealthBadge from '$lib/components/HealthBadge.svelte';
  import Skeleton from '$lib/components/Skeleton.svelte';
  import EmptyState from '$lib/components/EmptyState.svelte';
  import ErrorBox from '$lib/components/ErrorBox.svelte';

  const ctx = getBe();
  const id = $derived(ctx.id);
  const be = $derived(ctx.item!);
  const keys = () => [`be:${id}`, 'be:*'];

  const servers = resource(async () => (await unwrap(api.GET('/backends/{id}/servers', { params: { path: { id } } }))).servers, { deps: () => id, keys });
  const status = resource(async () => unwrap(api.GET('/backends/{id}/status', { params: { path: { id } } })), { deps: () => id, keys });
  const live = $derived(new Map((status.data?.servers ?? []).map((s) => [s.slot_id, s])));

  let addOpen = $state(false);
  let addMode = $state<'host' | 'label_selector' | 'ip'>('ip');
  let picked = $state<BackendServerInput[]>([]);
  let pickErr = $state('');
  let busy = $state(false);
  let pickerKey = $state(0);

  function openAdd() { picked = []; pickErr = ''; pickerKey++; addOpen = true; }
  function reloadAll() { void servers.reload(); void status.reload(); void ctx.reload(); }

  async function add() {
    if (!picked.length || pickErr) return;
    busy = true;
    const n = picked.length;
    const r = await mutate(api.POST('/backends/{id}/servers', { params: { path: { id } }, body: { servers: picked } }), {
      success: `${n} server entr${n === 1 ? 'y' : 'ies'} added`,
      onFieldErrors: (f) => (pickErr = Object.values(f)[0] ?? '')
    });
    busy = false;
    if (r) { addOpen = false; reloadAll(); }
  }

  async function update(s: BackendServer, body: { weight?: number; admin_state?: 'ready' | 'drain' | 'maint'; backup?: boolean }, msg: string) {
    const r = await mutate(api.PATCH('/backends/{id}/servers/{sid}', { params: { path: { id, sid: s.id } }, body }), { success: msg });
    if (r) reloadAll();
  }
  async function setState(s: BackendServer, st: 'ready' | 'drain' | 'maint') {
    if (st === 'maint') {
      const ok = await confirm({ title: `Put ${describe(s)} in maintenance?`, message: 'HAProxy stops sending it traffic immediately, including open sessions on persistent connections.', confirmLabel: 'Maintenance', danger: true });
      if (!ok) return;
    }
    await update(s, { admin_state: st }, st === 'drain' ? 'Draining — no new sessions' : st === 'maint' ? 'In maintenance' : 'Ready for traffic');
  }
  async function remove(s: BackendServer) {
    const ok = await confirm({ title: `Remove ${describe(s)}?`, message: 'It is drained first: no new sessions, open ones finish, then it is removed from the backend.', confirmLabel: 'Drain and remove', danger: true });
    if (!ok) return;
    const r = await mutate(api.DELETE('/backends/{id}/servers/{sid}', { params: { path: { id, sid: s.id } } }), { success: 'Removal started' });
    if (r !== undefined) reloadAll();
  }

  let weightOpen = $state(false);
  let weightS = $state<BackendServer | null>(null);
  let weight = $state(100);
  const weightOk = $derived(Number.isInteger(weight) && weight >= 0 && weight <= 256);
  function setWeight(s: BackendServer) { weightS = s; weight = s.weight; weightOpen = true; }
  async function saveWeight() {
    if (!weightS || !weightOk) return;
    weightOpen = false;
    await update(weightS, { weight }, 'Weight updated');
  }

  function describe(s: BackendServer) {
    if (s.type === 'ip') return `${s.ip}${s.port ? `:${s.port}` : ''}`;
    if (s.type === 'host') return s.resolved[0]?.name ?? `host #${s.host_id}`;
    return `selector ${s.label_selector}`;
  }
  function items(s: BackendServer): MenuItem[] {
    return [
      { label: 'Drain', onclick: () => setState(s, 'drain'), disabled: s.admin_state === 'drain' || s.removing },
      { label: 'Maintenance', onclick: () => setState(s, 'maint'), disabled: s.admin_state === 'maint' || s.removing },
      { label: 'Ready', onclick: () => setState(s, 'ready'), disabled: s.admin_state === 'ready' || s.removing },
      { label: 'Set weight…', onclick: () => setWeight(s), disabled: s.removing },
      { label: s.backup ? 'Unmark backup' : 'Mark as backup', onclick: () => update(s, { backup: !s.backup }, s.backup ? 'No longer a backup' : 'Marked as backup'), disabled: s.removing },
      { label: 'Remove…', onclick: () => remove(s), danger: true, separator: true, disabled: s.removing }
    ];
  }
  const st = (r: ResolvedServer): ResolvedServer => live.get(r.slot_id) ?? r;

  const totals = $derived.by(() => {
    const all = status.data?.servers ?? [];
    return { healthy: all.filter((s) => s.status === 'healthy').length, total: all.length };
  });
</script>

<svelte:head><title>Servers · {be?.name ?? 'Backend'} · Loxy</title></svelte:head>

<div class="toolbar">
  <button class="btn primary" onclick={openAdd}><Icon name="plus" size={14} />Add servers</button>
  <span class="spacer"></span>
  {#if status.data}<span class="muted small">{totals.healthy} of {totals.total} healthy · default port <span class="mono">{be.port}</span></span>{/if}
</div>

{#snippet admin(s: BackendServer)}
  {#if s.removing}<span class="badge warn">Removing</span>
  {:else if s.admin_state === 'drain'}<span class="badge warn"><Icon name="minus" size={11} />Draining</span>
  {:else if s.admin_state === 'maint'}<span class="badge"><Icon name="pause" size={11} />Maintenance</span>{/if}
  {#if s.backup}<span class="badge">Backup</span>{/if}
{/snippet}

{#if servers.error && !servers.data}
  <ErrorBox message={servers.error} onretry={servers.reload} />
{:else if !servers.data}
  <Skeleton card rows={5} />
{:else if !servers.data.length}
  <div class="card">
    <EmptyState icon="server" title="No servers yet" text="Add IP addresses, hosts from the inventory, or a label selector that keeps the server list in sync with host labels.">
      <button class="btn primary" onclick={openAdd}><Icon name="plus" size={14} />Add servers</button>
    </EmptyState>
  </div>
{:else}
  <div class="table-wrap">
    <table class="tbl">
      <thead><tr><th>Server</th><th>Address</th><th>Source</th><th>Status</th><th class="right">Sessions</th><th class="right">Weight</th><th><span class="sr-only">Actions</span></th></tr></thead>
      <tbody>
        {#each servers.data as s (s.id)}
          {#if s.type === 'label_selector'}
            <tr class="grp">
              <td colspan="2"><Icon name="tag" size={13} /> <span class="mono small">{s.label_selector}</span> <span class="muted xs">{s.resolved.length} match{s.resolved.length === 1 ? '' : 'es'}</span></td>
              <td class="small muted">Label selector{s.use_private_ip ? ' · private IP' : ''}</td>
              <td><span class="row">{@render admin(s)}</span></td>
              <td></td>
              <td class="right">{s.weight}</td>
              <td class="actions"><Menu items={items(s)} label="Actions for {describe(s)}" /></td>
            </tr>
            {#each s.resolved as r0 (r0.slot_id)}
              {@const r = st(r0)}
              <tr class="child">
                <td class="ind">{r.name ?? '—'}</td>
                <td class="mono small">{r.address}:{r.port}</td>
                <td></td>
                <td><span title={r.check || undefined}><HealthBadge status={r.status} /></span></td>
                <td class="right">{num(r.sessions)}</td>
                <td class="right muted">{s.weight}</td>
                <td></td>
              </tr>
            {:else}
              <tr class="child"><td colspan="7" class="ind muted small">No hosts match this selector yet.</td></tr>
            {/each}
          {:else}
            {@const r = s.resolved[0] ? st(s.resolved[0]) : null}
            <tr>
              <td>{r?.name ?? (s.type === 'ip' ? '—' : `host #${s.host_id}`)}</td>
              <td class="mono small">{r ? `${r.address}:${r.port}` : `${s.ip ?? '—'}:${s.port ?? be.port}`}</td>
              <td class="small muted">{s.type === 'ip' ? 'IP' : `Host${s.use_private_ip ? ' · private IP' : ''}`}</td>
              <td>
                <span class="row">
                  {#if r}<span title={r.check || undefined}><HealthBadge status={r.status} /></span>{:else}<HealthBadge status="unknown" />{/if}
                  {@render admin(s)}
                </span>
              </td>
              <td class="right">{num(r?.sessions)}</td>
              <td class="right">{s.weight}</td>
              <td class="actions"><Menu items={items(s)} label="Actions for {describe(s)}" /></td>
            </tr>
          {/if}
        {/each}
      </tbody>
    </table>
  </div>
  <p class="muted xs foot">Hover a status for the last health-check result. Removing a server drains it first.</p>
{/if}

<Modal bind:open={addOpen} title="Add servers to {be?.name ?? 'backend'}" width={600}>
  {#key pickerKey}
    <TargetPicker bind:mode={addMode} bind:targets={picked} bind:error={pickErr} />
  {/key}
  {#snippet footer()}
    <button class="btn" onclick={() => (addOpen = false)}>Cancel</button>
    <button class="btn primary" disabled={!picked.length || !!pickErr || busy} onclick={add}>{busy ? 'Adding…' : picked.length > 1 ? `Add ${picked.length} servers` : 'Add server'}</button>
  {/snippet}
</Modal>

<Modal bind:open={weightOpen} title="Set weight" width={380}>
  <form id="wform" onsubmit={(e) => { e.preventDefault(); void saveWeight(); }}>
    <div class="field">
      <label for="weight">Weight (0–256)</label>
      <input id="weight" class="input mono" type="number" min="0" max="256" bind:value={weight} aria-invalid={!weightOk ? 'true' : undefined} />
      <span class="hint">Relative share of new sessions. 0 stops new traffic without draining.</span>
    </div>
  </form>
  {#snippet footer()}
    <button class="btn" onclick={() => (weightOpen = false)}>Cancel</button>
    <button class="btn primary" type="submit" form="wform" disabled={!weightOk}>Save weight</button>
  {/snippet}
</Modal>

<style>
  .toolbar { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; margin-bottom: 14px; }
  .grp td { background: var(--surface-2); }
  .grp :global(.icon) { color: var(--muted); }
  .ind { padding-left: 32px !important; }
  .foot { margin-top: 8px; }
</style>
