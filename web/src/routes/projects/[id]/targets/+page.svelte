<script lang="ts">
  import { api, unwrap, mutate, type Target, type TargetInput, type TargetUpdate, type ResolvedTarget } from '$lib/api/client';
  import { getProject } from '$lib/stores/project';
  import { resource } from '$lib/stores/resource.svelte';
  import { confirm } from '$lib/stores/confirm.svelte';
  import { protoLabel } from '$lib/util/services';
  import Icon from '$lib/components/Icon.svelte';
  import Menu, { type MenuItem } from '$lib/components/Menu.svelte';
  import Modal from '$lib/components/Modal.svelte';
  import TargetPicker from '$lib/components/TargetPicker.svelte';
  import HealthBadge from '$lib/components/HealthBadge.svelte';
  import Skeleton from '$lib/components/Skeleton.svelte';
  import EmptyState from '$lib/components/EmptyState.svelte';
  import ErrorBox from '$lib/components/ErrorBox.svelte';

  const ctx = getProject();
  const id = $derived(ctx.id);

  const targets = resource(async () => (await unwrap(api.GET('/projects/{id}/targets', { params: { path: { id } } }))).targets, { deps: () => id, keys: () => [`p:${id}`, 'p:*'] });
  const services = resource(async () => (await unwrap(api.GET('/projects/{id}/services', { params: { path: { id } } }))).services, { deps: () => id, keys: () => [`p:${id}`] });
  // Health checks change without events (e.g. a new target's first checks): refresh while visible.
  $effect(() => {
    const t = setInterval(() => { if (!document.hidden) void targets.reload(); }, 5000);
    return () => clearInterval(t);
  });
  const svcLabel = (sid: number) => {
    const s = services.data?.find((x) => x.id === sid);
    return s ? `${protoLabel(s.protocol)} :${s.listen_port}` : `#${sid}`;
  };
  const multiSvc = $derived((services.data?.length ?? 0) > 1);

  let addOpen = $state(false);
  let addMode = $state<'host' | 'label_selector' | 'ip'>('ip');
  let picked = $state<TargetInput[]>([]);
  let pickErr = $state('');
  let serverErr = $state('');
  let busy = $state(false);
  let pickerKey = $state(0);

  function openAdd() { picked = []; pickErr = ''; serverErr = ''; pickerKey++; addOpen = true; }

  async function add() {
    if (!picked.length || pickErr) return;
    busy = true;
    serverErr = '';
    const n = picked.length;
    const r = await mutate(api.POST('/projects/{id}/targets', { params: { path: { id } }, body: { targets: picked } }), {
      success: `${n} target${n === 1 ? '' : 's'} added`,
      onFieldErrors: (f) => (serverErr = Object.entries(f).map(([k, v]) => `${k.replace(/^targets\[(\d+)\]\.?/, (_m, i) => `${describeInput(picked[Number(i)])}: `)}${v}`).join(' · '))
    });
    busy = false;
    if (r) { addOpen = false; void targets.reload(); void ctx.reload(); }
  }

  async function update(t: Target, body: TargetUpdate, msg: string) {
    const r = await mutate(api.PATCH('/projects/{id}/targets/{tid}', { params: { path: { id, tid: t.id } }, body }), { success: msg });
    if (r) void targets.reload();
  }
  async function remove(t: Target) {
    const ok = await confirm({ title: 'Remove target?', message: `${describe(t)} is drained first: open connections finish, then it is removed.`, confirmLabel: 'Remove target', danger: true });
    if (!ok) return;
    const r = await mutate(api.DELETE('/projects/{id}/targets/{tid}', { params: { path: { id, tid: t.id } } }), { success: 'Draining, then removing the target' });
    if (r !== undefined) { void targets.reload(); void ctx.reload(); }
  }
  async function setMaint(t: Target) {
    const ok = await confirm({ title: 'Put target in maintenance?', message: `${describe(t)} stops receiving traffic immediately, including open sessions.`, confirmLabel: 'Maintenance', danger: true });
    if (ok) await update(t, { admin_state: 'maint' }, 'Target in maintenance');
  }

  let weightOpen = $state(false);
  let weightT = $state<Target | null>(null);
  let weight = $state(100);
  const weightOk = $derived(Number.isInteger(weight) && weight >= 0 && weight <= 256);
  function setWeight(t: Target) { weightT = t; weight = t.weight; weightOpen = true; }
  async function saveWeight() {
    if (!weightT || !weightOk) return;
    weightOpen = false;
    await update(weightT, { weight }, 'Weight updated');
  }

  function describeInput(t: TargetInput | undefined) {
    if (!t) return 'target';
    return t.type === 'ip' ? (t.ip ?? 'IP') : t.type === 'host' ? `Host #${t.host_id}` : (t.label_selector ?? 'selector');
  }
  function describe(t: Target) {
    if (t.type === 'ip') return t.ip ?? 'IP target';
    if (t.type === 'host') return t.resolved[0]?.name ?? `Host #${t.host_id}`;
    return `Selector ${t.label_selector}`;
  }
  function items(t: Target): MenuItem[] {
    return [
      { label: 'Enable', onclick: () => update(t, { admin_state: 'ready' }, 'Target enabled'), disabled: t.admin_state === 'ready' || t.removing },
      { label: 'Drain', onclick: () => update(t, { admin_state: 'drain' }, 'Draining target'), disabled: t.admin_state === 'drain' || t.removing },
      { label: 'Maintenance', onclick: () => setMaint(t), disabled: t.admin_state === 'maint' || t.removing },
      { label: 'Set weight…', onclick: () => setWeight(t), separator: true },
      { label: t.backup ? 'Make primary' : 'Make backup', onclick: () => update(t, { backup: !t.backup }, t.backup ? 'Target is primary' : 'Target is a backup') },
      { label: 'Remove', onclick: () => remove(t), danger: true, separator: true, disabled: t.removing }
    ];
  }
  const rowsOf = (t: Target): (ResolvedTarget | null)[] => (t.resolved.length ? t.resolved : [null]);
  const healthy = $derived((targets.data ?? []).reduce((a, t) => a + t.resolved.filter((r) => r.status === 'healthy').length, 0));
  const total = $derived((targets.data ?? []).reduce((a, t) => a + t.resolved.length, 0));
</script>

<svelte:head><title>Targets · {ctx.project?.name ?? 'Project'} · Loxy</title></svelte:head>

<div class="toolbar">
  <p class="section-intro" style:margin="0">The servers this project sends traffic to. Health is checked per service.</p>
  <span class="spacer"></span>
  {#if total}<span class="muted small">{healthy} of {total} healthy</span>{/if}
  <button class="btn primary" onclick={openAdd}><Icon name="plus" size={14} />Add targets</button>
</div>

{#snippet adminBadge(t: Target)}
  {#if t.removing}<span class="badge warn">Removing</span>
  {:else if t.admin_state === 'drain'}<span class="badge warn"><Icon name="minus" size={11} />Draining</span>
  {:else if t.admin_state === 'maint'}<span class="badge"><Icon name="pause" size={11} />Maintenance</span>{/if}
  {#if t.backup}<span class="badge">Backup</span>{/if}
{/snippet}

{#if targets.error && !targets.data}
  <ErrorBox message={targets.error} onretry={targets.reload} />
{:else if !targets.data}
  <Skeleton card rows={5} />
{:else if !targets.data.length}
  <div class="card">
    <EmptyState icon="server" title="No targets yet" text="Add IP addresses, hosts from your inventory, or a label selector that picks hosts up automatically.">
      <button class="btn primary" onclick={openAdd}><Icon name="plus" size={14} />Add targets</button>
    </EmptyState>
  </div>
{:else}
  <div class="table-wrap">
    <table class="tbl tt">
      <thead>
        <tr><th>Target</th><th>Address</th><th>Status</th><th>{multiSvc ? 'Checks per service' : 'Last check'}</th><th class="right">Weight</th><th><span class="sr-only">Actions</span></th></tr>
      </thead>
      {#each targets.data as t (t.id)}
        {@const rows = rowsOf(t)}
        <tbody class:removing={t.removing}>
          {#each rows as r, i (r?.slot_id ?? -1)}
            <tr>
              {#if i === 0}
                <td rowspan={rows.length} class="tcell">
                  <span class="tname" class:mono={t.type !== 'host'}>
                    <Icon name={t.type === 'label_selector' ? 'tag' : t.type === 'host' ? 'host' : 'globe'} size={14} />
                    {t.type === 'ip' ? t.ip : t.type === 'host' ? (r?.name ?? `Host #${t.host_id}`) : t.label_selector}
                  </span>
                  <span class="row-wrap meta">
                    {#if t.type === 'label_selector'}<span class="muted xs">{t.resolved.length} matching host{t.resolved.length === 1 ? '' : 's'}</span>{/if}
                    {#if t.port}<span class="muted xs mono">port {t.port}</span>{/if}
                    {#if t.use_private_ip}<span class="muted xs">private IP</span>{/if}
                    {@render adminBadge(t)}
                  </span>
                </td>
              {/if}
              <td class="mono small">
                {#if r}{#if t.type === 'label_selector' && r.name}<span class="rn">{r.name}</span>{/if}<span class="mono">{r.address}</span>{:else}<span class="muted">{t.type === 'label_selector' ? 'No hosts match yet' : '—'}</span>{/if}
              </td>
              <td>{#if r}<HealthBadge status={r.status} />{:else}<HealthBadge status="unknown" />{/if}</td>
              <td><div class="checks">
                {#if r?.checks.length}
                  {#if multiSvc}
                    {#each r.checks as c (c.service_id)}
                      <span class="ck {c.status}" title={c.check ?? c.status}>
                        <Icon name={c.status === 'healthy' ? 'check' : c.status === 'unhealthy' ? 'x' : 'minus'} size={11} />{svcLabel(c.service_id)}
                      </span>
                    {/each}
                  {:else}
                    <span class="mono xs muted">{r.checks[0].check ?? '—'}</span>
                  {/if}
                {:else}<span class="muted xs">—</span>{/if}
              </div></td>
              {#if i === 0}
                <td rowspan={rows.length} class="right">{t.weight}</td>
                <td rowspan={rows.length} class="actions"><Menu items={items(t)} label="Actions for {describe(t)}" /></td>
              {/if}
            </tr>
          {/each}
        </tbody>
      {/each}
    </table>
  </div>
{/if}

<Modal bind:open={addOpen} title="Add targets" width={580}>
  {#key pickerKey}
    <TargetPicker bind:mode={addMode} bind:targets={picked} bind:error={pickErr} />
  {/key}
  {#if serverErr}<p class="ferr small" role="alert" style:margin-top="10px">{serverErr}</p>{/if}
  {#snippet footer()}
    <button class="btn" onclick={() => (addOpen = false)}>Cancel</button>
    <button class="btn primary" disabled={!picked.length || !!pickErr || busy} onclick={add}>{busy ? 'Adding…' : picked.length > 1 ? `Add ${picked.length} targets` : 'Add target'}</button>
  {/snippet}
</Modal>

<Modal bind:open={weightOpen} title="Set weight" width={380}>
  <form id="wform" onsubmit={(e) => { e.preventDefault(); void saveWeight(); }}>
    <div class="field">
      <label for="weight">Weight (0–256)</label>
      <input id="weight" class="input mono" type="number" min="0" max="256" bind:value={weight} aria-invalid={!weightOk ? 'true' : undefined} aria-describedby="weight-h" />
      {#if !weightOk}<span class="ferr" id="weight-h">Enter a whole number from 0 to 256</span>
      {:else}<span class="hint" id="weight-h">Relative share of new connections. 0 stops new traffic without draining.</span>{/if}
    </div>
  </form>
  {#snippet footer()}
    <button class="btn" onclick={() => (weightOpen = false)}>Cancel</button>
    <button class="btn primary" type="submit" form="wform" disabled={!weightOk}>Save weight</button>
  {/snippet}
</Modal>

<style>
  .toolbar { display: flex; align-items: center; gap: 12px; flex-wrap: wrap; margin-bottom: 14px; }
  .tt tbody + tbody { border-top: 1px solid var(--border-strong); }
  .tt tbody.removing { opacity: 0.6; }
  .tcell { vertical-align: top; }
  .tname { display: inline-flex; align-items: center; gap: 6px; font-weight: 500; }
  .tname :global(.icon) { color: var(--muted); }
  .meta { gap: 6px; margin-top: 3px; }
  .rn { color: var(--text-2); margin-right: 8px; }
  .checks { display: flex; gap: 6px; flex-wrap: wrap; align-items: center; }
  .ck { display: inline-flex; align-items: center; gap: 4px; font-size: var(--fs-xs); padding: 1px 6px; border-radius: 999px; background: var(--surface-2); color: var(--muted); font-variant-numeric: tabular-nums; }
  .ck.healthy { color: var(--ok); }
  .ck.unhealthy { color: var(--err); }
  .actions { vertical-align: top; text-align: right; }
</style>
