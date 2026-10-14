<script lang="ts">
  import { api, unwrap, mutate, type Certificate } from '$lib/api/client';
  import { resource } from '$lib/stores/resource.svelte';
  import { confirm } from '$lib/stores/confirm.svelte';
  import { daysUntil, dateTime } from '$lib/util/format';
  import { fqdnError } from '$lib/util/validate';
  import Icon from '$lib/components/Icon.svelte';
  import Menu from '$lib/components/Menu.svelte';
  import Modal from '$lib/components/Modal.svelte';
  import Drawer from '$lib/components/Drawer.svelte';
  import HealthBadge from '$lib/components/HealthBadge.svelte';
  import LabelChips from '$lib/components/LabelChips.svelte';
  import CopyText from '$lib/components/CopyText.svelte';
  import CodeView from '$lib/components/CodeView.svelte';
  import Skeleton from '$lib/components/Skeleton.svelte';
  import EmptyState from '$lib/components/EmptyState.svelte';
  import ErrorBox from '$lib/components/ErrorBox.svelte';

  const certs = resource(async () => (await unwrap(api.GET('/certificates'))).certificates, { keys: () => ['certificates'] });
  const providers = resource(async () => (await unwrap(api.GET('/dns_providers'))).dns_providers);

  function expiry(c: Certificate) {
    const d = daysUntil(c.not_after);
    if (d == null) return { text: '—', cls: '' };
    if (d < 0) return { text: 'Expired', cls: 'err-text' };
    return { text: d === 0 ? 'Today' : `${d} day${d === 1 ? '' : 's'}`, cls: d < 7 ? 'err-text' : d < 21 ? 'warn-text' : '' };
  }

  // Upload
  let upOpen = $state(false);
  let up = $state({ name: '', certificate: '', chain: '', private_key: '' });
  let upErr = $state<Record<string, string>>({});
  async function readFile(e: Event, key: 'certificate' | 'chain' | 'private_key') {
    const f = (e.currentTarget as HTMLInputElement).files?.[0];
    if (f) up[key] = await f.text();
  }
  async function upload() {
    upErr = {};
    if (!up.name.trim()) upErr.name = 'Required';
    if (!up.certificate.includes('BEGIN CERTIFICATE')) upErr.certificate = 'Paste a PEM certificate';
    if (!/BEGIN (EC |RSA )?PRIVATE KEY/.test(up.private_key)) upErr.private_key = 'Paste a PEM private key';
    if (Object.keys(upErr).length) return;
    const r = await mutate(api.POST('/certificates', { body: { type: 'uploaded', name: up.name.trim(), certificate: up.certificate, chain: up.chain || undefined, private_key: up.private_key } }), { success: 'Certificate uploaded', onFieldErrors: (f) => (upErr = f) });
    if (r) { upOpen = false; up = { name: '', certificate: '', chain: '', private_key: '' }; void certs.reload(); }
  }

  // Managed
  let mOpen = $state(false);
  let m = $state({ name: '', domains: '', challenge: 'http01' as 'http01' | 'dns01', dns_provider_id: '' as number | '', key_type: 'ecdsa256' as 'ecdsa256' | 'rsa2048' | 'rsa4096' });
  let mErr = $state<Record<string, string>>({});
  const mDomains = $derived(m.domains.split(/[\s,]+/).map((d) => d.trim().toLowerCase()).filter(Boolean));
  const hasWildcard = $derived(mDomains.some((d) => d.startsWith('*.')));
  $effect(() => { if (hasWildcard) m.challenge = 'dns01'; });
  async function createManaged() {
    mErr = {};
    if (!m.name.trim()) mErr.name = 'Required';
    if (!mDomains.length) mErr.domains = 'Enter at least one domain';
    else if (mDomains.length > 100) mErr.domains = 'At most 100 domains';
    else { const bad = mDomains.find((d) => fqdnError(d)); if (bad) mErr.domains = `${bad} is not a valid domain`; }
    if (m.challenge === 'dns01' && m.dns_provider_id === '') mErr.dns_provider_id = 'Choose a DNS provider';
    if (Object.keys(mErr).length) return;
    const r = await mutate(api.POST('/certificates', { body: { type: 'managed', name: m.name.trim(), domains: mDomains, challenge: m.challenge, dns_provider_id: m.challenge === 'dns01' ? (m.dns_provider_id as number) : undefined, key_type: m.key_type } }), { success: 'Certificate requested', onFieldErrors: (f) => (mErr = f) });
    if (r) { mOpen = false; void certs.reload(); }
  }

  let sel = $state<Certificate | null>(null);
  let dOpen = $state(false);
  $effect(() => { dOpen = !!sel; });
  $effect(() => { if (!dOpen) sel = null; });

  async function renew(c: Certificate) {
    const r = await mutate(api.POST('/certificates/{id}/actions/renew', { params: { path: { id: c.id } } }), { success: `Renewing ${c.name}` });
    if (r !== undefined) void certs.reload();
  }
  async function remove(c: Certificate) {
    if (c.used_by.length) return;
    const ok = await confirm({ title: `Delete ${c.name}?`, message: 'The certificate and its private key are deleted permanently.', confirmLabel: 'Delete certificate', danger: true });
    if (!ok) return;
    const r = await mutate(api.DELETE('/certificates/{id}', { params: { path: { id: c.id } } }), { success: 'Certificate deleted' });
    if (r !== undefined) { sel = null; void certs.reload(); }
  }
</script>

<svelte:head><title>Certificates · Loxy</title></svelte:head>

<div class="page-head">
  <h1>Certificates</h1>
  <button class="btn" onclick={() => { upErr = {}; upOpen = true; }}><Icon name="plus" size={14} />Upload certificate</button>
  <button class="btn primary" onclick={() => { mErr = {}; mOpen = true; }}><Icon name="shield" size={14} />Create managed certificate</button>
</div>

{#if certs.error && !certs.data}<ErrorBox message={certs.error} onretry={certs.reload} />
{:else if !certs.data}<Skeleton card rows={5} />
{:else if !certs.data.length}
  <div class="card"><EmptyState icon="cert" title="No certificates yet" text="HTTPS ports need a certificate; domains get one automatically. Let Loxy issue and renew one from Let's Encrypt, or upload your own.">
    <button class="btn primary" onclick={() => (mOpen = true)}>Create managed certificate</button></EmptyState></div>
{:else}
  <div class="table-wrap">
    <table class="tbl">
      <thead><tr><th>Name</th><th>Type</th><th>Domains</th><th>Status</th><th>Expires in</th><th class="right">Used by</th><th>Labels</th><th><span class="sr-only">Actions</span></th></tr></thead>
      <tbody>
        {#each certs.data as c (c.id)}
          {@const ex = expiry(c)}
          <tr class="clickable" onclick={() => (sel = c)}>
            <td><button class="linklike nm" onclick={(e) => { e.stopPropagation(); sel = c; }}>{c.name}</button></td>
            <td class="small">{c.type === 'managed' ? 'Managed' : 'Uploaded'}</td>
            <td class="mono small doms" title={c.domains.join(', ')}>{c.domains.slice(0, 2).join(', ')}{c.domains.length > 2 ? ` +${c.domains.length - 2}` : ''}</td>
            <td><span title={c.last_error ?? ''}><HealthBadge status={c.status} /></span></td>
            <td class="small {ex.cls}">{ex.text}</td>
            <td class="right">{c.used_by.length} service{c.used_by.length === 1 ? '' : 's'}</td>
            <td><LabelChips labels={c.labels} max={2} /></td>
            <td class="actions" onclick={(e) => e.stopPropagation()}>
              <Menu label="Actions for {c.name}" items={[
                { label: 'Details', onclick: () => (sel = c) },
                { label: 'Renew now', onclick: () => renew(c), disabled: c.type !== 'managed' },
                { label: c.used_by.length ? 'Delete (in use)' : 'Delete', onclick: () => remove(c), danger: true, disabled: c.used_by.length > 0, separator: true }
              ]} />
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
{/if}

<Modal bind:open={upOpen} title="Upload certificate" width={620}>
  <form id="upf" class="stack" onsubmit={(e) => { e.preventDefault(); void upload(); }} novalidate>
    <div class="field">
      <label for="un">Name</label>
      <input id="un" class="input" bind:value={up.name} aria-invalid={upErr.name ? 'true' : undefined} />
      {#if upErr.name}<span class="ferr">{upErr.name}</span>{/if}
    </div>
    {#each [['certificate', 'Certificate (PEM)', '-----BEGIN CERTIFICATE-----'], ['chain', 'Chain (optional)', 'Intermediate certificates, any order'], ['private_key', 'Private key (PEM)', '-----BEGIN PRIVATE KEY-----']] as const as [k, l, ph] (k)}
      <div class="field">
        <div class="row"><label for="u-{k}">{l}</label><span class="spacer"></span><label class="btn sm ghost file">Choose file<input type="file" accept=".pem,.crt,.key,.cer" class="sr-only" onchange={(e) => readFile(e, k)} /></label></div>
        <textarea id="u-{k}" class="textarea" rows="5" bind:value={up[k]} placeholder={ph} spellcheck="false" aria-invalid={upErr[k] ? 'true' : undefined}></textarea>
        {#if upErr[k]}<span class="ferr">{upErr[k]}</span>{/if}
      </div>
    {/each}
    <p class="xs muted">The key must match the certificate. Chains are reordered if needed. The key is encrypted at rest.</p>
  </form>
  {#snippet footer()}
    <button class="btn" onclick={() => (upOpen = false)}>Cancel</button>
    <button class="btn primary" type="submit" form="upf">Upload</button>
  {/snippet}
</Modal>

<Modal bind:open={mOpen} title="Create managed certificate" width={520}>
  <form id="mf" class="stack" onsubmit={(e) => { e.preventDefault(); void createManaged(); }} novalidate>
    <div class="field">
      <label for="mn">Name</label>
      <input id="mn" class="input" bind:value={m.name} aria-invalid={mErr.name ? 'true' : undefined} />
      {#if mErr.name}<span class="ferr">{mErr.name}</span>{/if}
    </div>
    <div class="field">
      <label for="md">Domains</label>
      <textarea id="md" class="textarea" rows="3" bind:value={m.domains} placeholder={'example.com\nwww.example.com'} spellcheck="false" aria-invalid={mErr.domains ? 'true' : undefined}></textarea>
      {#if mErr.domains}<span class="ferr">{mErr.domains}</span>{:else}<span class="hint">One per line or comma separated, up to 100. Wildcards need DNS-01.</span>{/if}
    </div>
    <fieldset class="field">
      <legend class="label">Challenge</legend>
      <label class="check"><input type="radio" name="ch" value="http01" bind:group={m.challenge} disabled={hasWildcard} />HTTP-01 — domains must resolve to this HAProxy node</label>
      <label class="check"><input type="radio" name="ch" value="dns01" bind:group={m.challenge} />DNS-01 — via a connected DNS provider</label>
    </fieldset>
    {#if m.challenge === 'dns01'}
      <div class="field">
        <label for="mp">DNS provider</label>
        <select id="mp" class="select" bind:value={m.dns_provider_id} aria-invalid={mErr.dns_provider_id ? 'true' : undefined}>
          <option value="">Choose…</option>
          {#each providers.data ?? [] as p (p.id)}<option value={p.id}>{p.name}</option>{/each}
        </select>
        {#if mErr.dns_provider_id}<span class="ferr">{mErr.dns_provider_id}</span>{:else if !(providers.data ?? []).length}<span class="hint">No providers yet. <a href="/domains/providers">Connect one</a>.</span>{/if}
      </div>
    {/if}
    <div class="field">
      <label for="mk">Key type</label>
      <select id="mk" class="select" bind:value={m.key_type}><option value="ecdsa256">ECDSA P-256</option><option value="rsa2048">RSA 2048</option><option value="rsa4096">RSA 4096</option></select>
    </div>
  </form>
  {#snippet footer()}
    <button class="btn" onclick={() => (mOpen = false)}>Cancel</button>
    <button class="btn primary" type="submit" form="mf">Create certificate</button>
  {/snippet}
</Modal>

<Drawer bind:open={dOpen} title={sel?.name ?? ''}>
  {#if sel}
    {@const c = sel}
    <div class="stack-lg">
      <div class="row-wrap"><HealthBadge status={c.status} /><span class="badge">{c.type === 'managed' ? 'Managed' : 'Uploaded'}</span><span class="spacer"></span>
        {#if c.type === 'managed'}<button class="btn sm" onclick={() => renew(c)}><Icon name="refresh" size={13} />Renew now</button>{/if}
        <button class="btn sm danger" disabled={c.used_by.length > 0} title={c.used_by.length ? 'Detach it from all services first' : undefined} onclick={() => remove(c)}>Delete</button>
      </div>
      <dl class="kv">
        <dt>Domains</dt><dd class="mono small">{c.domains.join(', ')}</dd>
        <dt>Issuer</dt><dd>{c.issuer ?? '—'}</dd>
        <dt>Valid from</dt><dd>{dateTime(c.not_before)}</dd>
        <dt>Valid until</dt><dd>{dateTime(c.not_after)} <span class={expiry(c).cls}>({expiry(c).text})</span></dd>
        <dt>Serial</dt><dd class="mono xs">{c.serial ?? '—'}</dd>
        <dt>SHA-256</dt><dd class="xs">{#if c.fingerprint_sha256}<CopyText text={c.fingerprint_sha256} label="fingerprint" />{:else}—{/if}</dd>
        <dt>Key type</dt><dd>{c.key_type ?? '—'}</dd>
        {#if c.type === 'managed'}<dt>Challenge</dt><dd>{c.challenge === 'dns01' ? 'DNS-01' : 'HTTP-01'}</dd><dt>Renewal</dt><dd class="small">Automatic, 30 days before expiry</dd>{/if}
        <dt>Used by</dt><dd>{c.used_by.length} service{c.used_by.length === 1 ? '' : 's'}</dd>
      </dl>
      {#if c.last_error}<section class="stack"><h3>Last issuance error</h3><CodeView text={c.last_error} maxHeight={240} label="Issuance log" /></section>{/if}
    </div>
  {/if}
</Drawer>

<style>
  .nm { font-weight: 500; text-decoration: none; }
  .nm:hover { text-decoration: underline; }
  .doms { max-width: 280px; overflow: hidden; text-overflow: ellipsis; }
  .file { cursor: pointer; }
  legend { margin-bottom: 6px; padding: 0; }
</style>
