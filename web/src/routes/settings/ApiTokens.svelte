<script lang="ts">
  import { api, unwrap, mutate, type APIToken } from '$lib/api/client';
  import { resource } from '$lib/stores/resource.svelte';
  import { confirm } from '$lib/stores/confirm.svelte';
  import { dateTime, relTime, daysUntil } from '$lib/util/format';
  import Icon from '$lib/components/Icon.svelte';
  import Modal from '$lib/components/Modal.svelte';
  import Skeleton from '$lib/components/Skeleton.svelte';
  import ErrorBox from '$lib/components/ErrorBox.svelte';
  import EmptyState from '$lib/components/EmptyState.svelte';

  let { canAdmin }: { canAdmin: boolean } = $props();

  const tokens = resource(async () => (await unwrap(api.GET('/api_tokens'))).api_tokens);

  type Role = APIToken['role'];
  const EXPIRY = [['30', '30 days'], ['90', '90 days'], ['365', '1 year'], ['', 'Never']] as const;

  let open = $state(false);
  let busy = $state(false);
  let nt = $state({ name: '', role: 'viewer' as Role, expiry: '90' });
  let err = $state<Record<string, string>>({});

  let secret = $state('');
  let secretOpen = $state(false);
  let copied = $state(false);

  function startCreate() {
    nt = { name: '', role: 'viewer', expiry: '90' };
    err = {};
    open = true;
  }

  async function create() {
    err = {};
    const name = nt.name.trim();
    if (!name) err.name = 'Give the token a name';
    else if (name.length > 64) err.name = 'At most 64 characters';
    if (Object.keys(err).length) return;
    busy = true;
    const body: { name: string; role: Role; expires_in_days?: number } = { name, role: nt.role };
    if (nt.expiry) body.expires_in_days = Number(nt.expiry);
    const r = await mutate(api.POST('/api_tokens', { body }), { onFieldErrors: (f) => (err = f) });
    busy = false;
    if (!r) return;
    open = false;
    secret = r.token;
    copied = false;
    secretOpen = true;
    void tokens.reload();
  }

  async function copySecret() {
    try {
      await navigator.clipboard.writeText(secret);
      copied = true;
    } catch { /* clipboard unavailable; the text is selectable */ }
  }

  function closeSecret() {
    secretOpen = false;
    secret = '';
  }

  async function revoke(t: APIToken) {
    const ok = await confirm({
      title: `Revoke “${t.name}”?`,
      message: 'Any script or integration using this token stops working immediately. This cannot be undone.',
      confirmLabel: 'Revoke token',
      danger: true
    });
    if (!ok) return;
    const r = await mutate(api.DELETE('/api_tokens/{id}', { params: { path: { id: t.id } } }), { success: 'Token revoked' });
    if (r !== undefined) void tokens.reload();
  }

  function expiry(t: APIToken): { text: string; cls: string } {
    if (!t.expires_at) return { text: 'Never', cls: 'muted' };
    const d = daysUntil(t.expires_at);
    if (d !== null && d < 0) return { text: 'Expired', cls: 'err-text' };
    if (d !== null && d <= 7) return { text: dateTime(t.expires_at), cls: 'warn-text' };
    return { text: dateTime(t.expires_at), cls: 'muted' };
  }
</script>

<section class="card" aria-labelledby="sat">
  <div class="card-head">
    <h2 id="sat">API tokens</h2><span class="spacer"></span>
    {#if canAdmin && tokens.data?.length}<button class="btn sm" onclick={startCreate}><Icon name="plus" size={13} />Create token</button>{/if}
  </div>
  <p class="intro muted small">Tokens authenticate scripts and CI against <code>/api/v1</code> with <code>Authorization: Bearer &lt;token&gt;</code>. A token acts with the role you give it.</p>
  {#if tokens.error && !tokens.data}<ErrorBox message={tokens.error} onretry={tokens.reload} />
  {:else if !tokens.data}<Skeleton rows={3} />
  {:else if tokens.data.length === 0}
    <EmptyState icon="lock" title="No API tokens" text={canAdmin ? 'Create a token to automate Loxy from scripts or CI.' : 'An owner or admin can create tokens.'}>
      {#if canAdmin}<button class="btn primary sm" onclick={startCreate}><Icon name="plus" size={13} />Create token</button>{/if}
    </EmptyState>
  {:else}
    <div class="table-wrap bare">
      <table class="tbl">
        <thead><tr><th>Name</th><th>Prefix</th><th>Role</th><th>Last used</th><th>Expires</th><th>Created</th><th><span class="sr-only">Actions</span></th></tr></thead>
        <tbody>
          {#each tokens.data as t (t.id)}
            {@const ex = expiry(t)}
            <tr>
              <td>{t.name}</td>
              <td class="mono small">loxy_{t.prefix}_…</td>
              <td class="small">{t.role}</td>
              <td class="small muted">{t.last_used_at ? relTime(t.last_used_at) : 'Never'}</td>
              <td class="small {ex.cls}">{ex.text}</td>
              <td class="small muted">{dateTime(t.created_at)}</td>
              <td class="actions">{#if canAdmin}<button class="btn sm danger" onclick={() => revoke(t)} aria-label="Revoke token {t.name}">Revoke</button>{/if}</td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {/if}
</section>

<Modal bind:open title="Create API token" width={440}>
  <form id="atf" class="stack" onsubmit={(e) => { e.preventDefault(); void create(); }} novalidate autocomplete="off">
    <div class="field">
      <label for="atn">Name</label>
      <input id="atn" class="input" bind:value={nt.name} placeholder="e.g. ci-deploy…" spellcheck="false" aria-invalid={err.name ? 'true' : undefined} aria-describedby="atn-h" />
      {#if err.name}<span id="atn-h" class="ferr">{err.name}</span>{:else}<span id="atn-h" class="hint">Shown in the audit log next to every action the token takes.</span>{/if}
    </div>
    <div class="field">
      <label for="atr">Role</label>
      <select id="atr" class="select" bind:value={nt.role} aria-invalid={err.role ? 'true' : undefined}>
        <option value="admin">Admin — everything except owner settings</option>
        <option value="operator">Operator — manage projects</option>
        <option value="viewer">Viewer — read only</option>
      </select>
      {#if err.role}<span class="ferr">{err.role}</span>{/if}
    </div>
    <div class="field">
      <label for="ate">Expires after</label>
      <select id="ate" class="select" bind:value={nt.expiry} aria-invalid={err.expires_in_days ? 'true' : undefined}>
        {#each EXPIRY as [v, l] (v)}<option value={v}>{l}</option>{/each}
      </select>
      {#if err.expires_in_days}<span class="ferr">{err.expires_in_days}</span>{/if}
    </div>
  </form>
  {#snippet footer()}
    <button class="btn" onclick={() => (open = false)}>Cancel</button>
    <button class="btn primary" type="submit" form="atf" disabled={busy}>{busy ? 'Creating…' : 'Create token'}</button>
  {/snippet}
</Modal>

<Modal bind:open={secretOpen} title="Token created" width={520} onclose={closeSecret}>
  <div class="stack">
    <div class="banner warn" role="status"><Icon name="alert" /><div>Copy this token now. <strong>You won’t be able to see it again</strong> — if you lose it, revoke it and create a new one.</div></div>
    <div class="field">
      <label for="ats">Token</label>
      <div class="secret">
        <input id="ats" class="input mono" readonly value={secret} spellcheck="false" onfocus={(e) => e.currentTarget.select()} />
        <button class="btn" type="button" onclick={copySecret}><Icon name={copied ? 'check' : 'copy'} size={13} />{copied ? 'Copied' : 'Copy'}</button>
      </div>
      <span class="sr-only" aria-live="polite">{copied ? 'Token copied to clipboard' : ''}</span>
    </div>
  </div>
  {#snippet footer()}
    <button class="btn primary" onclick={closeSecret}>Done</button>
  {/snippet}
</Modal>

<style>
  .intro { margin: calc(-1 * var(--sp-2)) 0 var(--sp-3); max-width: 72ch; }
  .secret { display: flex; gap: var(--sp-2); }
  .secret .input { flex: 1; min-width: 0; }
  .tbl td:first-child { white-space: normal; overflow-wrap: anywhere; }
</style>
