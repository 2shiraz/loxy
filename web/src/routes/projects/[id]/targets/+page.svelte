<script lang="ts">
  import { api, unwrap, mutate, type Target, type TargetInput, type TargetUpdate, type ResolvedTarget } from '$lib/api/client';
  import { getProject } from '$lib/stores/project';
  import { resource } from '$lib/stores/resource.svelte';
  import { confirm } from '$lib/stores/confirm.svelte';
  import { toast } from '$lib/stores/toast.svelte';
  import { protoLabel } from '$lib/util/services';
  import { checkText } from '$lib/util/format';
  import Icon from '$lib/components/Icon.svelte';
  import Menu, { type MenuItem } from '$lib/components/Menu.svelte';
  import ContextMenu from '$lib/components/ContextMenu.svelte';
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
  // No polling: the live tick carries a per-project health_sig; when any check of this
  // project flips, the live store bumps `p:<id>` and this resource refetches.
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
  let selected = $state<number[]>([]);
  let bulkBusy = $state(false);
  let completed = $state(0);
  let selectionAnchor = 0;
  let selectionProject: number | undefined;
  let contextPoint = $state<{ x: number; y: number } | null>(null);
  const selectable = $derived((targets.data ?? []).filter((t) => !t.removing));
  const selectedTargets = $derived(selectable.filter((t) => selected.includes(t.id)));
  const allSelected = $derived(selectable.length > 0 && selectedTargets.length === selectable.length);
  $effect(() => {
    const current = id;
    if (selectionProject !== current) { selectionProject = current; selected = []; contextPoint = null; }
  });
  $effect(() => {
    if (!targets.data) return;
    const available = new Set(targets.data.filter((t) => !t.removing).map((t) => t.id));
    const kept = selected.filter((tid) => available.has(tid));
    if (kept.length !== selected.length) selected = kept;
  });
  function selectTarget(t: Target, e: MouseEvent) {
    if (bulkBusy || t.removing) return;
    const index = selectable.findIndex((x) => x.id === t.id);
    if (e.shiftKey) {
      const range = selectable.slice(Math.min(selectionAnchor, index), Math.max(selectionAnchor, index) + 1).map((x) => x.id);
      selected = [...new Set([...selected, ...range])];
    } else {
      selected = selected.includes(t.id) ? selected.filter((tid) => tid !== t.id) : [...selected, t.id];
    }
    selectionAnchor = index;
  }
  function context(t: Target, e: MouseEvent | KeyboardEvent) {
    if (t.removing || bulkBusy) return;
    e.preventDefault();
    if (!selected.includes(t.id)) selected = [t.id];
    const rect = (e.target as HTMLElement).getBoundingClientRect();
    contextPoint = e instanceof MouseEvent ? { x: e.clientX, y: e.clientY } : { x: rect.left, y: rect.bottom };
  }
  function contextKey(t: Target, e: KeyboardEvent) {
    if (e.key === 'ContextMenu' || (e.shiftKey && e.key === 'F10')) context(t, e);
    if (e.key === 'Escape' && !bulkBusy) { selected = []; contextPoint = null; }
  }
  async function applyMany(list: Target[], body: TargetUpdate | null, message: string) {
    if (!list.length || bulkBusy) return;
    const projectId = id;
    bulkBusy = true;
    completed = 0;
    const failures: string[] = [];
    const succeeded: number[] = [];
    try {
      for (let i = 0; i < list.length; i += 5) {
        const group = list.slice(i, i + 5);
        const results = await Promise.allSettled(group.map((t) => body === null
          ? unwrap(api.DELETE('/projects/{id}/targets/{tid}', { params: { path: { id: projectId, tid: t.id } } }))
          : unwrap(api.PATCH('/projects/{id}/targets/{tid}', { params: { path: { id: projectId, tid: t.id } }, body }))));
        results.forEach((result, index) => {
          if (result.status === 'fulfilled') succeeded.push(group[index].id);
          else failures.push(`${describe(group[index])}: ${result.reason instanceof Error ? result.reason.message : 'Request failed'}`);
        });
        completed += group.length;
      }
      if (succeeded.length) toast.success(`${message}: ${succeeded.length} target${succeeded.length === 1 ? '' : 's'}`);
      if (failures.length) toast.error(`${failures.length} target${failures.length === 1 ? '' : 's'} could not be updated. ${failures.slice(0, 2).join('; ')}${failures.length > 2 ? '; retry the remaining selection.' : ''}`);
      if (id === projectId) selected = failures.length ? list.filter((t) => !succeeded.includes(t.id)).map((t) => t.id) : selected;
      await Promise.all([targets.reload(), ctx.reload()]);
    } finally { bulkBusy = false; }
  }
  async function bulkConfirm(list: Target[], removeTargets = false) {
    const n = list.length;
    const ok = await confirm({ title: removeTargets ? `Remove ${n} targets?` : `Put ${n} targets in maintenance?`,
      message: removeTargets ? 'Selected targets are drained first, then removed when their connections finish.' : 'Selected targets stop receiving traffic immediately, including open sessions.',
      confirmLabel: removeTargets ? 'Remove targets' : 'Maintenance', danger: true });
    if (ok) await applyMany(list, removeTargets ? null : { admin_state: 'maint' }, removeTargets ? 'Draining, then removing' : 'In maintenance');
  }
  function sharedItems(list: Target[]): MenuItem[] {
    const disabled = bulkBusy || !list.length;
    return [
      { label: 'Enable', onclick: () => applyMany(list, { admin_state: 'ready' }, 'Enabled'), disabled },
      { label: 'Drain', onclick: () => applyMany(list, { admin_state: 'drain' }, 'Draining'), disabled },
      { label: 'Maintenance', onclick: () => bulkConfirm(list), disabled },
      { label: 'Set weight…', onclick: () => setWeight(list), disabled, separator: true },
      { label: 'Make primary', onclick: () => applyMany(list, { backup: false }, 'Made primary'), disabled },
      { label: 'Make backup', onclick: () => applyMany(list, { backup: true }, 'Made backup'), disabled },
      { label: 'Remove', onclick: () => bulkConfirm(list, true), disabled, danger: true, separator: true }
    ];
  }

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
  let weightTargets = $state<Target[]>([]);
  let weight = $state(100);
  const weightOk = $derived(Number.isInteger(weight) && weight >= 0 && weight <= 256);
  function setWeight(t: Target | Target[]) { weightTargets = Array.isArray(t) ? [...t] : [t]; weight = weightTargets[0]?.weight ?? 100; weightOpen = true; }
  async function saveWeight() {
    if (!weightTargets.length || !weightOk || bulkBusy) return;
    weightOpen = false;
    await applyMany(weightTargets, { weight }, 'Weight updated');
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
    if (bulkBusy) return sharedItems([]);
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
  <p class="section-intro intro">Targets are the servers that receive this project's traffic. Loxy checks each one and only sends traffic to those that answer.</p>
  <div class="toolbar-actions">
    {#if total}<span class="sum small" class:ok={healthy === total}><b>{healthy}</b> of {total} healthy</span>{/if}
    {#if targets.data?.length}<button class="btn primary" disabled={bulkBusy} onclick={openAdd}><Icon name="plus" size={14} />Add targets</button>{/if}
  </div>
</div>

{#snippet adminBadge(t: Target)}
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
  <div class="selection-bar" aria-live="polite" aria-busy={bulkBusy}>
    <label class="select-all"><input type="checkbox" aria-label="Select all targets" checked={allSelected} indeterminate={selectedTargets.length > 0 && !allSelected} disabled={bulkBusy || !selectable.length} onchange={() => selected = allSelected ? [] : selectable.map((t) => t.id)} />
      {selectedTargets.length ? `${selectedTargets.length} selected` : 'Select targets'}
    </label>
    {#if bulkBusy}<span class="small muted">Updating… {completed} completed</span>
    {:else if selectedTargets.length}
      <Menu items={sharedItems([...selectedTargets])} btnClass="btn sm" label="Selected target actions">
        {#snippet trigger()}Actions for {selectedTargets.length}<Icon name="chevron" size={12} />{/snippet}
      </Menu>
      <button class="btn ghost sm" onclick={() => selected = []}>Clear selection</button>
    {:else}<span class="small muted selection-hint">Select several targets, then right-click for shared actions.</span>{/if}
  </div>
  <div class="table-wrap">
    <table class="tbl tt">
      <thead>
        <tr><th class="select-cell"><span class="sr-only">Select</span></th><th>Target</th><th>Address</th><th>Status</th><th>{multiSvc ? 'Checks per service' : 'Last check'}</th><th class="right">Weight</th><th><span class="sr-only">Actions</span></th></tr>
      </thead>
      {#each targets.data as t (t.id)}
        {@const rows = rowsOf(t)}
        <!-- Right-click supplements the focusable checkbox and visible Actions button. -->
        <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
        <tbody class:removing={t.removing} class:selected={selected.includes(t.id)} oncontextmenu={(e) => context(t, e)}>
          {#each rows as r, i (r?.slot_id ?? -1)}
            <tr>
              {#if i === 0}
                <td rowspan={rows.length} class="select-cell"><input type="checkbox" aria-label="Select {describe(t)}" checked={selected.includes(t.id)} disabled={bulkBusy || t.removing} onclick={(e) => selectTarget(t, e)} onkeydown={(e) => contextKey(t, e)} /></td>
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
              <td class="mono small addr" data-label="Address">
                {#if r}{#if t.type === 'label_selector' && r.name}<span class="rn">{r.name}</span>{/if}<span class="mono">{r.address}</span>{:else}<span class="muted">{t.type === 'label_selector' ? 'No hosts match yet' : '—'}</span>{/if}
              </td>
              <td data-label="Status">{#if t.removing}<HealthBadge status="draining" label="Removing" />{:else if t.admin_state === 'maint'}<HealthBadge status="maintenance" />{:else if t.admin_state === 'drain'}<HealthBadge status="draining" />{:else if r}<HealthBadge status={r.status} />{:else}<HealthBadge status="unknown" />{/if}</td>
              <td data-label={multiSvc ? 'Checks' : 'Last check'} class="ckcell"><div class="checks">
                {#if r?.checks.length}
                  {#if multiSvc}
                    {#each r.checks as c (c.service_id)}
                      <span class="ck {c.status}" title={c.check ? `${checkText(c.check)} (${c.check})` : c.status}>
                        <Icon name={c.status === 'healthy' ? 'check' : c.status === 'unhealthy' ? 'x' : 'minus'} size={11} />{svcLabel(c.service_id)}
                      </span>
                    {/each}
                  {:else}
                    <span class="xs muted" title={r.checks[0].check ?? ''}>{r.checks[0].check ? checkText(r.checks[0].check) : '—'}</span>
                  {/if}
                {:else}<span class="muted xs">—</span>{/if}
              </div></td>
              {#if i === 0}
                <td rowspan={rows.length} class="right" data-label="Weight">{t.weight}</td>
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

<ContextMenu bind:point={contextPoint} items={sharedItems([...selectedTargets])} label="Actions for selected targets" />

<Modal bind:open={weightOpen} title={weightTargets.length > 1 ? `Set weight for ${weightTargets.length} targets` : 'Set weight'} width={380}>
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
    <button class="btn primary" type="submit" form="wform" disabled={!weightOk || bulkBusy}>Save weight</button>
  {/snippet}
</Modal>

<style>
  .intro { margin: 0; flex: 1 1 360px; }
  .toolbar { align-items: center; justify-content: space-between; gap: var(--sp-4); }
  .toolbar-actions { display: flex; align-items: center; gap: var(--sp-3); flex-wrap: wrap; }
  .selection-bar { display: flex; align-items: center; flex-wrap: wrap; gap: var(--sp-3); min-height: 48px; margin-bottom: var(--sp-2); }
  .select-all { display: inline-flex; align-items: center; gap: var(--sp-2); font-size: var(--fs-sm); font-weight: 500; cursor: pointer; min-height: 40px; }
  input[type=checkbox] { width: 16px; height: 16px; accent-color: var(--accent); cursor: pointer; }
  .tt .select-cell { width: 44px; padding-right: 0; text-align: center; vertical-align: top; }
  .tt tbody.selected { background: var(--accent-soft); }
  .tt .tcell { width: 24%; }
  .tt .ckcell { white-space: normal; min-width: 150px; }
  .sum { color: var(--muted); white-space: nowrap; }
  .sum b { color: var(--warn); font-weight: 650; }
  .sum.ok b { color: var(--ok); }
  .tt tbody + tbody { border-top: 1px solid var(--border-strong); }
  .tt tbody.removing { opacity: 0.6; }
  .tcell { vertical-align: top; }
  .tname { display: inline-flex; align-items: center; gap: 6px; font-weight: 500; }
  .tname :global(.icon) { color: var(--muted); }
  .meta { gap: 6px; }
  .meta:not(:empty) { margin-top: 4px; }
  .rn { color: var(--text-2); margin-right: 8px; }
  .checks { display: flex; gap: 6px; flex-wrap: wrap; align-items: center; }
  .ck { display: inline-flex; align-items: center; gap: 4px; font-size: var(--fs-xs); padding: 1px 6px; border-radius: 999px; background: var(--surface-2); color: var(--muted); font-variant-numeric: tabular-nums; }
  .ck.healthy { color: var(--ok); }
  .ck.unhealthy { color: var(--err); }
  .actions { vertical-align: top; text-align: right; }
  .tt tbody { transition: opacity 200ms ease; }

  /* Phones: each target becomes a card; extra addresses stack inside it. */
  @media (max-width: 699px) {
    .toolbar-actions { width: 100%; justify-content: space-between; }
    .selection-hint { display: none; }
    .tt td.select-cell { position: absolute; top: 10px; left: 8px; width: 40px; height: 40px; padding: 0; justify-content: center; }
    .tt .tcell { width: auto; }
    .table-wrap { border: 0; background: transparent; overflow: visible; border-radius: 0; }
    .tt, .tt tbody, .tt tr, .tt td { display: block; }
    .tt thead { position: absolute; width: 1px; height: 1px; overflow: hidden; clip: rect(0 0 0 0); }
    .tt { display: flex; flex-direction: column; gap: 10px; }
    .tt tbody { position: relative; background: var(--surface); border: 1px solid var(--border); border-radius: var(--radius); padding: 6px 0; }
    .tt tbody + tbody { border-top: 1px solid var(--border); }
    .tt tr + tr { border-top: 1px dashed var(--border); margin-top: 4px; padding-top: 4px; }
    .tt td { border: 0; padding: 6px 16px; white-space: normal; display: flex; align-items: center; justify-content: space-between; gap: 12px; text-align: right; }
    .tt td[data-label]::before { content: attr(data-label); color: var(--muted); font-size: var(--fs-sm); font-family: var(--font); flex: none; }
    .tt td.tcell { display: block; text-align: left; padding: 10px 56px 6px 48px; }
    .tt td.actions { position: absolute; top: 6px; right: 6px; padding: 0; width: auto; display: block; }
    .tt td.ckcell .checks { justify-content: flex-end; }
    .tt td.addr { overflow-wrap: anywhere; }
  }
</style>
