<script lang="ts">
  import { api, unwrap, mutate, type DNSProvider, type DNSProviderKind } from '$lib/api/client';
  import { resource } from '$lib/stores/resource.svelte';
  import { confirm } from '$lib/stores/confirm.svelte';
  import { dateTime } from '$lib/util/format';
  import Icon from '$lib/components/Icon.svelte';
  import Modal from '$lib/components/Modal.svelte';
  import HealthBadge from '$lib/components/HealthBadge.svelte';
  import Skeleton from '$lib/components/Skeleton.svelte';
  import EmptyState from '$lib/components/EmptyState.svelte';
  import ErrorBox from '$lib/components/ErrorBox.svelte';

  const res = resource(async () => await unwrap(api.GET('/dns_providers')));
  const kinds = $derived(res.data?.kinds ?? []);
  const kindLabel = (k: string) => kinds.find((x) => x.kind === k)?.label ?? k;

  let open = $state(false);
  let kind = $state('');
  let name = $state('');
  let creds = $state<Record<string, string>>({});
  let errs = $state<Record<string, string>>({});
  let busy = $state(false);
  const k = $derived<DNSProviderKind | undefined>(kinds.find((x) => x.kind === kind));

  function openAdd() { kind = kinds[0]?.kind ?? ''; name = ''; creds = {}; errs = {}; open = true; }
  async function save() {
    errs = {};
    if (!name.trim()) errs.name = 'Required';
    for (const f of k?.fields ?? []) if (!creds[f.name]?.trim()) errs[`credentials.${f.name}`] = 'Required';
    if (Object.keys(errs).length) return;
    busy = true;
    const r = await mutate(api.POST('/dns_providers', { body: { name: name.trim(), kind, credentials: creds } }), { onFieldErrors: (f) => (errs = f) });
    busy = false;
    if (r) {
      open = false;
      creds = {};
      void res.reload();
      if (r.dns_provider.status === 'error') { /* surfaced in list */ }
    }
  }
  async function remove(p: DNSProvider) {
    const ok = await confirm({ title: `Remove ${p.name}?`, message: 'Stored credentials are deleted. Domains and certificates using this provider must be switched first.', confirmLabel: 'Remove provider', danger: true });
    if (!ok) return;
    const r = await mutate(api.DELETE('/dns_providers/{id}', { params: { path: { id: p.id } } }), { success: 'Provider removed' });
    if (r !== undefined) void res.reload();
  }
</script>

<div class="row" style:margin-bottom="14px">
  <p class="muted" style:max-width="70ch">Connected provider accounts let Loxy manage records and complete DNS-01 challenges. Credentials are encrypted and never shown again.</p>
  <span class="spacer"></span>
  <button class="btn primary" onclick={openAdd} disabled={!kinds.length}><Icon name="plus" size={14} />Connect provider</button>
</div>

{#if res.error && !res.data}<ErrorBox message={res.error} onretry={res.reload} />
{:else if !res.data}<Skeleton card rows={3} />
{:else if !res.data.dns_providers.length}
  <div class="card"><EmptyState icon="dns" title="No DNS providers connected" text="Connect Hetzner DNS, Hetzner Console or Cloudflare to manage records and issue wildcard certificates."><button class="btn primary" onclick={openAdd}>Connect provider</button></EmptyState></div>
{:else}
  <div class="table-wrap">
    <table class="tbl">
      <thead><tr><th>Name</th><th>Provider</th><th>Status</th><th>Connected</th><th><span class="sr-only">Actions</span></th></tr></thead>
      <tbody>
        {#each res.data.dns_providers as p (p.id)}
          <tr>
            <td><b>{p.name}</b></td>
            <td>{kindLabel(p.kind)}</td>
            <td><span title={p.last_error ?? ''}><HealthBadge status={p.status} /></span>{#if p.last_error}<div class="xs err-text err">{p.last_error}</div>{/if}</td>
            <td class="muted small">{dateTime(p.created_at)}</td>
            <td class="actions"><a class="btn sm ghost" href="/domains/zones?provider={p.id}">Zones</a> <button class="btn sm danger" onclick={() => remove(p)}>Remove</button></td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
{/if}

<Modal bind:open title="Connect DNS provider" width={480}>
  <form id="prov" class="stack" onsubmit={(e) => { e.preventDefault(); void save(); }} novalidate autocomplete="off">
    <div class="field">
      <label for="pk">Provider</label>
      <select id="pk" class="select" bind:value={kind} onchange={() => (creds = {})}>
        {#each kinds as x (x.kind)}<option value={x.kind}>{x.label}</option>{/each}
      </select>
    </div>
    <div class="field">
      <label for="pn">Name</label>
      <input id="pn" class="input" bind:value={name} placeholder="e.g. Production DNS" aria-invalid={errs.name ? 'true' : undefined} />
      {#if errs.name}<span class="ferr">{errs.name}</span>{/if}
    </div>
    {#each k?.fields ?? [] as f (f.name)}
      {@const ek = `credentials.${f.name}`}
      <div class="field">
        <label for="f-{f.name}">{f.label}</label>
        <input id="f-{f.name}" class="input mono" type={f.secret ? 'password' : 'text'} autocomplete={f.secret ? 'new-password' : 'off'} spellcheck="false"
          value={creds[f.name] ?? ''} oninput={(e) => (creds[f.name] = (e.currentTarget as HTMLInputElement).value)} aria-invalid={errs[ek] || errs[f.name] ? 'true' : undefined} />
        {#if errs[ek] || errs[f.name]}<span class="ferr">{errs[ek] || errs[f.name]}</span>{:else if f.help}<span class="hint">{f.help}</span>{/if}
      </div>
    {/each}
    {#if errs.credentials}<p class="ferr small err-text">{errs.credentials}</p>{/if}
    <p class="xs muted">Loxy tests the credentials by listing zones before saving.</p>
  </form>
  {#snippet footer()}
    <button class="btn" onclick={() => (open = false)}>Cancel</button>
    <button class="btn primary" type="submit" form="prov" disabled={busy}>{busy ? 'Testing…' : 'Connect'}</button>
  {/snippet}
</Modal>

<style>
  .err { margin-top: 2px; max-width: 320px; white-space: normal; }
</style>
