<script lang="ts">
  import { api, unwrap, mutate, type Domain } from '$lib/api/client';
  import { resource } from '$lib/stores/resource.svelte';
  import { confirm } from '$lib/stores/confirm.svelte';
  import { fqdnError } from '$lib/util/validate';
  import { relTime } from '$lib/util/dns';
  import Icon from '$lib/components/Icon.svelte';
  import Modal from '$lib/components/Modal.svelte';
  import Drawer from '$lib/components/Drawer.svelte';
  import CopyText from '$lib/components/CopyText.svelte';
  import Skeleton from '$lib/components/Skeleton.svelte';
  import EmptyState from '$lib/components/EmptyState.svelte';
  import ErrorBox from '$lib/components/ErrorBox.svelte';
  import DomainStatus from '$lib/components/DomainStatus.svelte';

  const domains = resource(async () => (await unwrap(api.GET('/domains'))).domains, { keys: () => ['domains'] });
  const fes = resource(async () => (await unwrap(api.GET('/frontends'))).frontends, { keys: () => ['frontends'] });
  const providers = resource(async () => (await unwrap(api.GET('/dns_providers'))).dns_providers);
  const certs = resource(async () => (await unwrap(api.GET('/certificates'))).certificates, { keys: () => ['certificates'] });

  const feName = (id: number | null | undefined) => fes.data?.find((l) => l.id === id)?.name ?? (id ? `#${id}` : '—');
  const provName = (id: number | null | undefined) => (id ? (providers.data?.find((p) => p.id === id)?.name ?? `#${id}`) : 'Manual');
  function certState(d: Domain) {
    if (!d.certificate_id) return 'none';
    return certs.data?.find((c) => c.id === d.certificate_id)?.status ?? 'pending';
  }

  // Add domain
  let addOpen = $state(false);
  let fqdn = $state('');
  let feId = $state<number | ''>('');
  let provId = $state<number | ''>('');
  let auto = $state(false);
  let errs = $state<Record<string, string>>({});
  let busy = $state(false);

  $effect(() => { if (provId === '') auto = false; });

  function openAdd() { fqdn = ''; feId = fes.data?.find((f) => f.mode === 'http')?.id ?? fes.data?.[0]?.id ?? ''; provId = ''; auto = false; errs = {}; addOpen = true; }
  async function add() {
    const fe = fqdnError(fqdn.trim().toLowerCase());
    errs = {};
    if (fe) errs.fqdn = fe;
    if (feId === '') errs.frontend_id = 'Choose a frontend';
    if (Object.keys(errs).length) return;
    busy = true;
    const r = await mutate(api.POST('/domains', { body: { fqdn: fqdn.trim().toLowerCase(), frontend_id: feId as number, dns_provider_id: provId === '' ? null : provId, auto_records: auto } }), {
      success: 'Domain added. Checking DNS…', onFieldErrors: (f) => (errs = f)
    });
    busy = false;
    if (r) { addOpen = false; void domains.reload(); selected = r.domain; }
  }

  // Drawer
  let selected = $state<Domain | null>(null);
  let drawerOpen = $state(false);
  $effect(() => { drawerOpen = !!selected; });
  $effect(() => { if (!drawerOpen) selected = null; });
  $effect(() => {
    // keep drawer in sync with refreshed list
    const s = selected;
    if (!s || !domains.data) return;
    const fresh = domains.data.find((d) => d.id === s.id);
    if (fresh && fresh !== s && JSON.stringify(fresh) !== JSON.stringify(s)) selected = fresh;
  });
  let checking = $state(false);
  async function verify(d: Domain) {
    checking = true;
    const r = await mutate(api.POST('/domains/{id}/actions/verify', { params: { path: { id: d.id } } }));
    checking = false;
    if (r) { selected = r.domain; void domains.reload(); }
  }
  async function issue(d: Domain) {
    const r = await mutate(api.POST('/domains/{id}/actions/issue_certificate', { params: { path: { id: d.id } } }), { success: `Issuing certificate for ${d.fqdn}` });
    if (r) { void domains.reload(); void certs.reload(); }
  }
  async function remove(d: Domain) {
    const ok = await confirm({ title: `Remove ${d.fqdn}?`, message: d.auto_records ? 'The A/AAAA records Loxy created for this name are deleted at the provider.' : 'Loxy stops checking this name. DNS records are not changed.', confirmLabel: 'Remove domain', danger: true });
    if (!ok) return;
    const r = await mutate(api.DELETE('/domains/{id}', { params: { path: { id: d.id } } }), { success: 'Domain removed' });
    if (r !== undefined) { selected = null; void domains.reload(); }
  }
  function family(d: Domain, type: 'A' | 'AAAA') {
    const want = d.required_records.filter((r) => r.type === type).map((r) => r.value);
    const got = type === 'A' ? d.observed.a : d.observed.aaaa;
    if (!want.length) return { state: 'na', got };
    if (!got.length) return { state: 'missing', got };
    return { state: want.every((w) => got.includes(w)) && got.every((g) => want.includes(g)) ? 'ok' : 'wrong', got };
  }
</script>

<div class="row" style:margin-bottom="14px">
  <p class="muted" style:max-width="70ch">Point names at your frontends. Loxy checks that DNS resolves to the right addresses and can issue certificates once it does.</p>
  <span class="spacer"></span>
  <button class="btn primary" onclick={openAdd}><Icon name="plus" size={14} />Add domain</button>
</div>

{#if domains.error && !domains.data}
  <ErrorBox message={domains.error} onretry={domains.reload} />
{:else if !domains.data}
  <Skeleton card rows={5} />
{:else if !domains.data.length}
  <div class="card"><EmptyState icon="dns" title="No domains yet" text="Add a domain to see which DNS records it needs and whether they already resolve to this HAProxy node."><button class="btn primary" onclick={openAdd}>Add domain</button></EmptyState></div>
{:else}
  <div class="table-wrap">
    <table class="tbl">
      <thead><tr><th>Domain</th><th>Frontend</th><th>DNS</th><th>Status</th><th>Last checked</th><th>Certificate</th><th>Auto records</th></tr></thead>
      <tbody>
        {#each domains.data as d (d.id)}
          <tr class="clickable" onclick={() => (selected = d)}>
            <td><button class="linklike name mono" onclick={(e) => { e.stopPropagation(); selected = d; }}>{d.fqdn}</button></td>
            <td>{#if d.frontend_id}<a href="/frontends/{d.frontend_id}" onclick={(e) => e.stopPropagation()}>{feName(d.frontend_id)}</a>{:else}—{/if}</td>
            <td>{provName(d.dns_provider_id)}</td>
            <td><DomainStatus status={d.status} /></td>
            <td class="muted small">{relTime(d.checked_at)}</td>
            <td class="small">{certState(d)}</td>
            <td class="small">{d.auto_records ? 'On' : 'Off'}</td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
{/if}

<Modal bind:open={addOpen} title="Add domain" width={500}>
  <form id="adddom" class="stack" onsubmit={(e) => { e.preventDefault(); void add(); }} novalidate>
    <div class="field">
      <label for="fqdn">Domain name</label>
      <input id="fqdn" class="input mono" bind:value={fqdn} placeholder="app.example.com" autocomplete="off" spellcheck="false" aria-invalid={errs.fqdn ? 'true' : undefined} />
      {#if errs.fqdn}<span class="ferr">{errs.fqdn}</span>{/if}
    </div>
    <div class="field">
      <label for="dfe">Frontend</label>
      <select id="dfe" class="select" bind:value={feId} aria-invalid={errs.frontend_id ? 'true' : undefined} aria-describedby="dfe-h">
        {#if !fes.data?.length}<option value="">No frontends yet</option>{/if}
        {#each fes.data ?? [] as l (l.id)}<option value={l.id}>{l.name} ({l.mode.toUpperCase()}{l.tls ? ' + TLS' : ''})</option>{/each}
      </select>
      {#if errs.frontend_id}<span class="ferr" id="dfe-h">{errs.frontend_id}</span>{:else}<span class="hint" id="dfe-h">A TLS frontend gets the issued certificate attached automatically.</span>{/if}
    </div>
    <div class="field">
      <label for="dprov">DNS provider <span class="muted">(optional)</span></label>
      <select id="dprov" class="select" bind:value={provId}>
        <option value="">Manual — I'll create the records myself</option>
        {#each providers.data ?? [] as p (p.id)}<option value={p.id}>{p.name}</option>{/each}
      </select>
    </div>
    <label class="check" class:muted={provId === ''}><input type="checkbox" bind:checked={auto} disabled={provId === ''} />Manage A/AAAA records automatically</label>
  </form>
  {#snippet footer()}
    <button class="btn" onclick={() => (addOpen = false)}>Cancel</button>
    <button class="btn primary" type="submit" form="adddom" disabled={busy}>{busy ? 'Adding…' : 'Add domain'}</button>
  {/snippet}
</Modal>

<Drawer bind:open={drawerOpen} title={selected?.fqdn ?? ''}>
  {#if selected}
    {@const d = selected}
    <div class="stack-lg">
      <div class="row-wrap">
        <DomainStatus status={d.status} />
        <span class="muted small">Checked {relTime(d.checked_at)}</span>
        <span class="spacer"></span>
        <button class="btn sm" onclick={() => verify(d)} disabled={checking}><Icon name="refresh" size={13} />{checking ? 'Checking…' : 'Check now'}</button>
        <button class="btn sm primary" onclick={() => issue(d)} disabled={d.status !== 'ok' || !!d.certificate_id} title={d.status !== 'ok' ? 'Available once the domain resolves correctly' : d.certificate_id ? 'A certificate already exists' : undefined}>
          <Icon name="shield" size={13} />Issue certificate
        </button>
      </div>
      {#if d.status_detail}<p class="small muted">{d.status_detail}</p>{/if}

      <section class="stack">
        <h3>Required records</h3>
        {#if d.required_records.length}
          <div class="table-wrap">
            <table class="tbl">
              <thead><tr><th>Type</th><th>Name</th><th>Value</th><th class="right">TTL</th></tr></thead>
              <tbody>
                {#each d.required_records as r (r.type + r.value)}
                  <tr><td class="mono small">{r.type}</td><td><CopyText text={r.name} /></td><td><CopyText text={r.value} /></td><td class="right mono small">{r.ttl}</td></tr>
                {/each}
              </tbody>
            </table>
          </div>
        {:else}
          <p class="muted small">The frontend listens on no public addresses, so no records are required.</p>
        {/if}
      </section>

      <section class="stack">
        <h3>What DNS returns</h3>
        {#each ['A', 'AAAA'] as const as t (t)}
          {@const f = family(d, t)}
          {#if f.state !== 'na'}
            <div class="obs">
              <span class="mono small fam">{t}</span>
              {#if f.state === 'ok'}<span class="ok-text small"><Icon name="check" size={13} /> Resolves correctly</span>
              {:else if f.state === 'missing'}<span class="err-text small"><Icon name="x" size={13} /> Missing</span>
              {:else}<span class="err-text small"><Icon name="x" size={13} /> Wrong IP</span>{/if}
              <span class="mono xs muted">{f.got.join(', ') || 'no answer'}</span>
            </div>
          {/if}
        {/each}
      </section>

      <dl class="kv">
        <dt>Frontend</dt><dd>{#if d.frontend_id}<a href="/frontends/{d.frontend_id}">{feName(d.frontend_id)}</a>{:else}—{/if}</dd>
        <dt>DNS provider</dt><dd>{provName(d.dns_provider_id)}{d.zone ? ` · zone ${d.zone}` : ''}</dd>
        <dt>Auto records</dt><dd>{d.auto_records ? 'On' : 'Off'}</dd>
        <dt>Certificate</dt><dd>{#if d.certificate_id}<a href="/certificates">{certState(d)}</a>{:else}None{/if}</dd>
      </dl>
      <div><button class="btn danger sm" onclick={() => remove(d)}>Remove domain</button></div>
    </div>
  {/if}
</Drawer>

<style>
  .name { text-decoration: none; font-weight: 500; }
  .name:hover { text-decoration: underline; }
  .obs { display: flex; align-items: center; gap: 12px; flex-wrap: wrap; padding: 8px 12px; background: var(--surface-2); border-radius: var(--radius-sm); }
  .fam { width: 40px; color: var(--muted); }
  .obs :global(.icon) { vertical-align: -2px; }
</style>
