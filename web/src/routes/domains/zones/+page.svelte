<script lang="ts">
  import { page } from '$app/state';
  import { goto } from '$app/navigation';
  import { api, unwrap, mutate, type DNSRecord } from '$lib/api/client';
  import { resource } from '$lib/stores/resource.svelte';
  import { confirm } from '$lib/stores/confirm.svelte';
  import { RECORD_HINTS } from '$lib/util/dns';
  import Icon from '$lib/components/Icon.svelte';
  import Modal from '$lib/components/Modal.svelte';
  import Skeleton from '$lib/components/Skeleton.svelte';
  import EmptyState from '$lib/components/EmptyState.svelte';
  import ErrorBox from '$lib/components/ErrorBox.svelte';

  const providers = resource(async () => (await unwrap(api.GET('/dns_providers'))).dns_providers);
  const provId = $derived(Number(page.url.searchParams.get('provider')) || null);
  const zone = $derived(page.url.searchParams.get('zone'));

  $effect(() => {
    if (!provId && providers.data?.length) void goto(`/domains/zones?provider=${providers.data[0].id}`, { replaceState: true, keepFocus: true, noScroll: true });
  });

  const zones = resource(async () => (provId ? (await unwrap(api.GET('/dns_providers/{id}/zones', { params: { path: { id: provId } } }))).zones : []), { deps: () => provId });
  const records = resource(async () => (provId && zone ? (await unwrap(api.GET('/dns_providers/{id}/zones/{zone}/records', { params: { path: { id: provId, zone } } }))).records : null), { deps: () => [provId, zone] });

  function nav(p: number | null, z: string | null) {
    const q = new URLSearchParams();
    if (p) q.set('provider', String(p));
    if (z) q.set('zone', z);
    void goto(`/domains/zones?${q}`, { keepFocus: true, noScroll: true });
  }

  const TYPES = ['A', 'AAAA', 'CNAME', 'TXT', 'MX', 'CAA', 'SRV', 'NS'] as const;
  let typeFilter = $state('');
  const shown = $derived((records.data ?? []).filter((r) => !typeFilter || r.type === typeFilter));

  let addOpen = $state(false);
  let rec = $state<DNSRecord>({ type: 'A', name: '@', value: '', ttl: 300 });
  let errs = $state<Record<string, string>>({});
  function openAdd() { rec = { type: 'A', name: '@', value: '', ttl: 300 }; errs = {}; addOpen = true; }
  async function add() {
    errs = {};
    if (!rec.name.trim()) errs.name = 'Use @ for the zone apex';
    if (!rec.value.trim()) errs.value = 'Required';
    if (rec.ttl != null && !(rec.ttl >= 60 && rec.ttl <= 86400)) errs.ttl = 'TTL must be 60–86400';
    if (Object.keys(errs).length || !provId || !zone) return;
    const r = await mutate(api.POST('/dns_providers/{id}/zones/{zone}/records', { params: { path: { id: provId, zone } }, body: { ...rec, name: rec.name.trim(), value: rec.value.trim() } }), { success: 'Record created', onFieldErrors: (f) => (errs = f) });
    if (r) { addOpen = false; void records.reload(); }
  }
  async function del(r: DNSRecord) {
    if (!provId || !zone) return;
    const ok = await confirm({ title: 'Delete record?', message: `${r.type} ${r.name} → ${r.value}${r.managed ? '. This record is managed by Loxy and will be recreated unless auto-records are turned off.' : ''}`, confirmLabel: 'Delete record', danger: true });
    if (!ok) return;
    const res = await mutate(api.DELETE('/dns_providers/{id}/zones/{zone}/records', { params: { path: { id: provId, zone } }, body: r }), { success: 'Record deleted' });
    if (res !== undefined) void records.reload();
  }
  const hint = $derived(RECORD_HINTS[rec.type]);
</script>

{#if providers.error && !providers.data}<ErrorBox message={providers.error} onretry={providers.reload} />
{:else if !providers.data}<Skeleton card rows={3} />
{:else if !providers.data.length}
  <div class="card"><EmptyState icon="dns" title="No DNS providers connected" text="Connect a provider to browse and edit its zones."><a class="btn primary" href="/domains/providers">Connect provider</a></EmptyState></div>
{:else}
  <div class="layout">
    <aside class="card flush side" aria-label="Zones">
      <div class="field pad">
        <label for="zp">Provider</label>
        <select id="zp" class="select" value={provId ?? ''} onchange={(e) => nav(Number((e.currentTarget as HTMLSelectElement).value), null)}>
          {#each providers.data as p (p.id)}<option value={p.id}>{p.name}</option>{/each}
        </select>
      </div>
      {#if zones.error}<p class="err-text small pad">{zones.error}</p>
      {:else if zones.loading && !zones.data}<div class="pad"><Skeleton rows={4} /></div>
      {:else}
        <ul class="zl">
          {#each zones.data ?? [] as z (z.name)}
            <li><button class:on={zone === z.name} aria-current={zone === z.name ? 'true' : undefined} onclick={() => nav(provId, z.name)}><span class="mono">{z.name}</span></button></li>
          {:else}<li class="muted small pad">No zones in this account.</li>{/each}
        </ul>
      {/if}
    </aside>

    <section class="main" aria-label="Records">
      {#if !zone}
        <div class="card"><EmptyState icon="dns" title="Select a zone" text="Pick a zone on the left to view and edit its records." /></div>
      {:else}
        <div class="row" style:margin-bottom="10px">
          <h2 class="mono">{zone}</h2>
          <span class="spacer"></span>
          <select class="select" style:width="130px" bind:value={typeFilter} aria-label="Filter by type"><option value="">All types</option>{#each TYPES as t (t)}<option>{t}</option>{/each}</select>
          <button class="btn primary" onclick={openAdd}><Icon name="plus" size={14} />Add record</button>
        </div>
        {#if records.error}<ErrorBox message={records.error} onretry={records.reload} />
        {:else if !records.data}<Skeleton card rows={6} />
        {:else}
          <div class="table-wrap">
            <table class="tbl">
              <thead><tr><th>Type</th><th>Name</th><th>Value</th><th class="right">TTL</th><th><span class="sr-only">Actions</span></th></tr></thead>
              <tbody>
                {#each shown as r, i (r.id ?? i)}
                  <tr>
                    <td class="mono small">{r.type}</td>
                    <td class="mono small">{r.name}</td>
                    <td class="mono small val" title={r.value}>{r.value}</td>
                    <td class="right mono small">{r.ttl ?? '—'}</td>
                    <td class="actions"><span class="row">{#if r.managed}<span class="badge" title="Created and kept in sync by Loxy">Loxy</span>{/if}<button class="btn ghost icon sm" aria-label="Delete {r.type} record {r.name}" onclick={() => del(r)}><Icon name="trash" size={14} /></button></span></td>
                  </tr>
                {:else}
                  <tr><td colspan="5" class="muted small" style:text-align="center" style:padding="24px">No records{typeFilter ? ` of type ${typeFilter}` : ''}.</td></tr>
                {/each}
              </tbody>
            </table>
          </div>
        {/if}
      {/if}
    </section>
  </div>
{/if}

<Modal bind:open={addOpen} title="Add record to {zone ?? ''}" width={520}>
  <form id="rec" class="stack" onsubmit={(e) => { e.preventDefault(); void add(); }} novalidate>
    <div class="form-grid">
      <div class="field">
        <label for="rt">Type</label>
        <select id="rt" class="select" bind:value={rec.type}>{#each TYPES as t (t)}<option>{t}</option>{/each}</select>
      </div>
      <div class="field">
        <label for="rn">Name</label>
        <input id="rn" class="input mono" bind:value={rec.name} placeholder="@ or www" aria-invalid={errs.name ? 'true' : undefined} />
        {#if errs.name}<span class="ferr">{errs.name}</span>{/if}
      </div>
      <div class="field">
        <label for="rttl">TTL (s)</label>
        <input id="rttl" class="input mono" type="number" bind:value={rec.ttl} aria-invalid={errs.ttl ? 'true' : undefined} />
        {#if errs.ttl}<span class="ferr">{errs.ttl}</span>{/if}
      </div>
    </div>
    <div class="field">
      <label for="rv">Value</label>
      <input id="rv" class="input mono" bind:value={rec.value} placeholder={hint?.placeholder} aria-invalid={errs.value ? 'true' : undefined} />
      {#if errs.value}<span class="ferr">{errs.value}</span>{:else}<span class="hint">{hint?.hint}</span>{/if}
    </div>
  </form>
  {#snippet footer()}
    <button class="btn" onclick={() => (addOpen = false)}>Cancel</button>
    <button class="btn primary" type="submit" form="rec">Add record</button>
  {/snippet}
</Modal>

<style>
  .layout { display: grid; grid-template-columns: 240px minmax(0, 1fr); gap: 16px; align-items: start; }
  .side { position: sticky; top: calc(var(--topbar-h) + 12px); }
  .pad { padding: 12px; }
  .zl { list-style: none; margin: 0; padding: 0 6px 8px; max-height: 60vh; overflow: auto; }
  .zl button { width: 100%; text-align: left; border: 0; background: none; padding: 6px 8px; border-radius: 5px; cursor: pointer; color: var(--text-2); font-size: var(--fs-sm); }
  .zl button:hover { background: var(--surface-2); }
  .zl button.on { background: var(--surface-3); color: var(--text); }
  h2 { font-size: var(--fs-lg); }
  .val { max-width: 420px; overflow: hidden; text-overflow: ellipsis; }
  @media (max-width: 760px) { .layout { grid-template-columns: 1fr; } .side { position: static; } }
</style>
