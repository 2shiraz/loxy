<script lang="ts">
  import { api, unwrap, mutate, type Target, type TargetInput, type Server } from '$lib/api/client';
  import { getLb } from '$lib/stores/lbctx';
  import { resource } from '$lib/stores/resource.svelte';
  import { confirm } from '$lib/stores/confirm.svelte';
  import Icon from '$lib/components/Icon.svelte';
  import Menu, { type MenuItem } from '$lib/components/Menu.svelte';
  import Modal from '$lib/components/Modal.svelte';
  import TargetPicker from '$lib/components/TargetPicker.svelte';
  import HealthCount from '$lib/components/HealthCount.svelte';
  import Skeleton from '$lib/components/Skeleton.svelte';
  import EmptyState from '$lib/components/EmptyState.svelte';
  import ErrorBox from '$lib/components/ErrorBox.svelte';

  const ctx = getLb();
  const id = $derived(ctx.id);
  const lb = $derived(ctx.lb!);

  const targets = resource(async () => (await unwrap(api.GET('/load_balancers/{id}/targets', { params: { path: { id } } }))).targets, {
    deps: () => id, keys: () => [`lb:${id}`]
  });
  const servers = resource(async () => (await unwrap(api.GET('/servers'))).servers);
  const serverName = (sid: number | null | undefined) => (servers.data ?? []).find((s: Server) => s.id === sid)?.name;

  let view = $state<'grouped' | 'list'>('grouped');
  let addOpen = $state(false);
  let addMode = $state<'server' | 'label_selector' | 'ip'>('ip');
  let picked = $state<TargetInput[]>([]);
  let pickErr = $state('');
  let busy = $state(false);

  function openAdd(m: typeof addMode) { addMode = m; picked = []; addOpen = true; }

  async function add() {
    if (!picked.length || pickErr) return;
    busy = true;
    const r = await mutate(api.POST('/load_balancers/{id}/targets', { params: { path: { id } }, body: { targets: picked } }), {
      success: `${picked.length} target${picked.length === 1 ? '' : 's'} added`,
      onFieldErrors: (f) => (pickErr = Object.values(f)[0] ?? '')
    });
    busy = false;
    if (r) { addOpen = false; void targets.reload(); void ctx.reload(); }
  }

  async function update(t: Target, body: { weight?: number; admin_state?: 'ready' | 'drain' | 'maint' }, msg: string) {
    const r = await mutate(api.PATCH('/load_balancers/{id}/targets/{tid}', { params: { path: { id, tid: t.id } }, body }), { success: msg });
    if (r) void targets.reload();
  }
  async function remove(t: Target) {
    const ok = await confirm({ title: 'Remove target?', message: `${describe(t)} will be drained, then removed. Open connections finish first.`, confirmLabel: 'Remove target', danger: true });
    if (!ok) return;
    const r = await mutate(api.DELETE('/load_balancers/{id}/targets/{tid}', { params: { path: { id, tid: t.id } } }), { success: 'Target removal started' });
    if (r !== undefined) { void targets.reload(); void ctx.reload(); }
  }

  let weightOpen = $state(false);
  let weightT = $state<Target | null>(null);
  let weight = $state(100);
  function setWeight(t: Target) { weightT = t; weight = t.weight; weightOpen = true; }
  async function saveWeight() {
    if (!weightT || !(weight >= 0 && weight <= 256)) return;
    weightOpen = false;
    await update(weightT, { weight }, 'Weight updated');
  }

  function describe(t: Target) {
    if (t.type === 'ip') return t.ip ?? 'IP target';
    if (t.type === 'server') return serverName(t.server_id) ?? `Server #${t.server_id}`;
    return `Label selector ${t.label_selector}`;
  }
  function items(t: Target): MenuItem[] {
    return [
      { label: 'Drain', onclick: () => update(t, { admin_state: 'drain' }, 'Draining target'), disabled: t.admin_state === 'drain' },
      { label: 'Maintenance', onclick: () => update(t, { admin_state: 'maint' }, 'Target in maintenance'), disabled: t.admin_state === 'maint' },
      { label: 'Enable', onclick: () => update(t, { admin_state: 'ready' }, 'Target enabled'), disabled: t.admin_state === 'ready' },
      { label: 'Set weight…', onclick: () => setWeight(t) },
      { label: 'Remove', onclick: () => remove(t), danger: true, separator: true }
    ];
  }
  function sum(t: Target) {
    return t.resolved.reduce((a, r) => ({ h: a.h + r.healthy, n: a.n + r.total }), { h: 0, n: 0 });
  }

  const groups = $derived.by(() => {
    const all = targets.data ?? [];
    const g: { key: string; title: string; icon: string; items: Target[]; selector?: Target }[] = [];
    for (const t of all.filter((x) => x.type === 'label_selector')) g.push({ key: 'ls' + t.id, title: t.label_selector ?? '', icon: 'tag', items: [t], selector: t });
    g.push({ key: 'srv', title: 'Servers', icon: 'server', items: all.filter((x) => x.type === 'server') });
    g.push({ key: 'ip', title: 'IP targets', icon: 'globe', items: all.filter((x) => x.type === 'ip') });
    return g;
  });
  type Resolved = Target['resolved'][number];
  const flat = $derived((targets.data ?? []).flatMap((t): { t: Target; r: Resolved | null }[] => (t.resolved.length ? t.resolved.map((r) => ({ t, r })) : [{ t, r: null }])));
  const usesPrivate = $derived(lb.private_net.length > 0);
</script>

<div class="toolbar">
  <Menu
    label="Add targets"
    align="left"
    btnClass="btn primary"
    items={[
      { label: 'Server', onclick: () => openAdd('server') },
      { label: 'Label selector', onclick: () => openAdd('label_selector') },
      { label: 'IP', onclick: () => openAdd('ip') }
    ]}
  >
    {#snippet trigger()}<Icon name="plus" size={14} />Add targets<Icon name="chevron" size={13} />{/snippet}
  </Menu>
  <span class="spacer"></span>
  <span class="muted small">{lb.usage.targets} / {lb.plan.max_targets} targets</span>
  <div class="seg" role="radiogroup" aria-label="View">
    <button role="radio" aria-checked={view === 'grouped'} class:on={view === 'grouped'} onclick={() => (view = 'grouped')}>Grouped</button>
    <button role="radio" aria-checked={view === 'list'} class:on={view === 'list'} onclick={() => (view = 'list')}>List</button>
  </div>
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
{:else if view === 'grouped'}
  <div class="stack">
    {#each groups as g (g.key)}
      <section class="card flush" aria-label={g.title}>
        <div class="ghead">
          <Icon name={g.icon} />
          <b class:mono={!!g.selector}>{g.title}</b>
          {#if g.selector}
            {@const s = sum(g.selector)}
            <span class="muted small">{g.selector.resolved.length} server{g.selector.resolved.length === 1 ? '' : 's'}</span>
            <HealthCount healthy={s.h} total={s.n} />
            {@render adminBadge(g.selector)}
            <span class="spacer"></span>
            <Menu items={items(g.selector)} label="Actions for {g.title}" />
          {:else}
            <span class="muted small">{g.items.length}</span>
          {/if}
        </div>
        {#if (g.selector ? g.selector.resolved.length : g.items.length) === 0}
          <p class="muted small empty">{g.selector ? 'No servers match this selector yet.' : 'No targets added yet.'}</p>
        {:else}
          <div class="table-wrap bare">
            <table class="tbl">
              <thead><tr><th>Name</th><th>Public IP</th>{#if usesPrivate}<th>Private IP</th>{/if}<th>Status</th><th class="right">Weight</th><th><span class="sr-only">Actions</span></th></tr></thead>
              <tbody>
                {#if g.selector}
                  {#each g.selector.resolved as r (r.slot_id)}
                    <tr><td>{r.name ?? '—'}</td><td class="mono small">{r.public_ip ?? r.address}</td>{#if usesPrivate}<td class="mono small">{r.private_ip ?? '—'}</td>{/if}<td><HealthCount healthy={r.healthy} total={r.total} /></td><td class="right">{g.selector.weight}</td><td></td></tr>
                  {/each}
                {:else}
                  {#each g.items as t (t.id)}
                    {@const s = sum(t)}
                    {@const r = t.resolved[0]}
                    <tr>
                      <td>{t.type === 'ip' ? '—' : (r?.name ?? serverName(t.server_id) ?? `#${t.server_id}`)}</td>
                      <td class="mono small">{t.type === 'ip' ? t.ip : (r?.public_ip ?? r?.address ?? '—')}</td>
                      {#if usesPrivate}<td class="mono small">{r?.private_ip ?? '—'}</td>{/if}
                      <td><span class="row"><HealthCount healthy={s.h} total={s.n} />{@render adminBadge(t)}</span></td>
                      <td class="right">{t.weight}</td>
                      <td class="actions"><Menu items={items(t)} label="Actions for {describe(t)}" /></td>
                    </tr>
                  {/each}
                {/if}
              </tbody>
            </table>
          </div>
        {/if}
      </section>
    {/each}
  </div>
{:else if flat.length === 0}
  <div class="card"><EmptyState icon="server" title="No targets added yet." text="Targets receive traffic from this load balancer's services. Add servers, a label selector or IP addresses." /></div>
{:else}
  <div class="table-wrap">
    <table class="tbl">
      <thead><tr><th>Name</th><th>Type</th><th>Public IP</th>{#if usesPrivate}<th>Private IP</th>{/if}<th>Status</th><th class="right">Weight</th><th><span class="sr-only">Actions</span></th></tr></thead>
      <tbody>
        {#each flat as { t, r }, i (i)}
          <tr>
            <td>{r?.name ?? (t.type === 'ip' ? '—' : serverName(t.server_id) ?? '—')}</td>
            <td class="small muted">{t.type === 'label_selector' ? t.label_selector : t.type}</td>
            <td class="mono small">{r?.public_ip ?? r?.address ?? t.ip ?? '—'}</td>
            {#if usesPrivate}<td class="mono small">{r?.private_ip ?? '—'}</td>{/if}
            <td><span class="row">{#if r}<HealthCount healthy={r.healthy} total={r.total} />{:else}<HealthCount healthy={0} total={0} />{/if}{@render adminBadge(t)}</span></td>
            <td class="right">{t.weight}</td>
            <td class="actions"><Menu items={items(t)} label="Actions for {describe(t)}" /></td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
{/if}

<Modal bind:open={addOpen} title="Add targets" width={580}>
  <TargetPicker bind:mode={addMode} bind:targets={picked} bind:error={pickErr} allowPrivate={usesPrivate}
    exclude={[lb.public_net.ipv4 ?? '', lb.public_net.ipv6 ?? '', ...lb.private_net.map((p) => p.ip)].filter(Boolean)} />
  {#snippet footer()}
    <button class="btn" onclick={() => (addOpen = false)}>Cancel</button>
    <button class="btn primary" disabled={!picked.length || !!pickErr || busy} onclick={add}>{busy ? 'Adding…' : `Add ${picked.length || ''} target${picked.length === 1 ? '' : 's'}`}</button>
  {/snippet}
</Modal>

<Modal bind:open={weightOpen} title="Set weight" width={380}>
  <form id="wform" onsubmit={(e) => { e.preventDefault(); void saveWeight(); }}>
    <div class="field">
      <label for="weight">Weight (0–256)</label>
      <input id="weight" class="input mono" type="number" min="0" max="256" bind:value={weight} aria-invalid={!(weight >= 0 && weight <= 256) ? 'true' : undefined} />
      <span class="hint">Relative share of new connections. 0 stops new traffic without draining.</span>
    </div>
  </form>
  {#snippet footer()}
    <button class="btn" onclick={() => (weightOpen = false)}>Cancel</button>
    <button class="btn primary" type="submit" form="wform" disabled={!(weight >= 0 && weight <= 256)}>Save weight</button>
  {/snippet}
</Modal>

<style>
  .toolbar { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; margin-bottom: 14px; }
  .seg { display: inline-flex; padding: 2px; background: var(--surface-2); border-radius: 8px; }
  .seg button { border: 0; background: none; padding: 5px 11px; border-radius: 6px; color: var(--muted); cursor: pointer; font-weight: 500; font-size: var(--fs-sm); }
  .seg button.on { background: var(--surface); color: var(--text); box-shadow: 0 0 0 1px var(--border-strong); }
  .ghead { display: flex; align-items: center; gap: 10px; padding: 10px 14px; border-bottom: 1px solid var(--border); background: var(--surface); }
  .ghead :global(.icon) { color: var(--muted); }
  .empty { padding: 14px; }
</style>
