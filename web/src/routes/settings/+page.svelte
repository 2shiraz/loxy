<script lang="ts">
  import { api, unwrap, mutate, type User } from '$lib/api/client';
  import { resource } from '$lib/stores/resource.svelte';
  import { confirm } from '$lib/stores/confirm.svelte';
  import { session } from '$lib/stores/session.svelte';
  import { theme, setTheme, type ThemePref } from '$lib/stores/theme.svelte';
  import { dateTime, relTime } from '$lib/util/format';
  import Icon from '$lib/components/Icon.svelte';
  import Modal from '$lib/components/Modal.svelte';
  import Skeleton from '$lib/components/Skeleton.svelte';
  import ErrorBox from '$lib/components/ErrorBox.svelte';
  import { replaceState } from '$app/navigation';
  import Tabs from '$lib/components/Tabs.svelte';
  import ApiTokens from './ApiTokens.svelte';
  import Notifications from './Notifications.svelte';

  const TABS = [{ id: 'general', label: 'General' }, { id: 'tokens', label: 'API tokens' }, { id: 'notifications', label: 'Notifications' }];
  let tab = $state(TABS.some((t) => '#' + t.id === location.hash) ? location.hash.slice(1) : 'general');
  $effect(() => {
    const h = tab === 'general' ? location.pathname + location.search : '#' + tab;
    if (h !== location.pathname + location.search + location.hash) {
      try { replaceState(h, {}); } catch { /* router not ready */ }
    }
  });

  const users = resource(async () => (await unwrap(api.GET('/users'))).users);
  const health = resource(async () => (await api.GET('/healthz')).data);
  const canAdmin = $derived(session.user?.role === 'owner' || session.user?.role === 'admin');

  let uOpen = $state(false);
  let nu = $state({ email: '', password: '', role: 'operator' as 'admin' | 'operator' | 'viewer' });
  let uErr = $state<Record<string, string>>({});
  async function createUser() {
    uErr = {};
    if (!/^[^@\s]+@[^@\s]+\.[^@\s]+$/.test(nu.email)) uErr.email = 'Enter a valid email address';
    if (nu.password.length < 12) uErr.password = 'At least 12 characters';
    if (Object.keys(uErr).length) return;
    const r = await mutate(api.POST('/users', { body: nu }), { success: 'User created', onFieldErrors: (f) => (uErr = f) });
    if (r) { uOpen = false; void users.reload(); }
  }
  async function delUser(u: User) {
    const ok = await confirm({ title: `Delete ${u.email}?`, message: 'Their sessions and API tokens are revoked immediately.', confirmLabel: 'Delete user', danger: true });
    if (!ok) return;
    const r = await mutate(api.DELETE('/users/{id}', { params: { path: { id: u.id } } }), { success: 'User deleted' });
    if (r !== undefined) void users.reload();
  }

  let pw = $state({ current: '', next: '', confirm: '' });
  let pwErr = $state<Record<string, string>>({});
  async function changePw(e: SubmitEvent) {
    e.preventDefault();
    pwErr = {};
    if (pw.next.length < 12) pwErr.new_password = 'At least 12 characters';
    else if (pw.next !== pw.confirm) pwErr.confirm = 'Passwords do not match';
    if (Object.keys(pwErr).length) return;
    const r = await mutate(api.POST('/auth/password', { body: { current_password: pw.current, new_password: pw.next } }), { success: 'Password changed', onFieldErrors: (f) => (pwErr = f) });
    if (r !== undefined) pw = { current: '', next: '', confirm: '' };
  }
</script>

<svelte:head><title>Settings · Loxy</title></svelte:head>
<div class="page-head"><h1>Settings</h1></div>

<div class="tabs-wrap"><Tabs tabs={TABS} bind:active={tab} label="Settings sections" /></div>

{#if tab === 'tokens'}
  <div role="tabpanel" aria-label="API tokens"><ApiTokens {canAdmin} /></div>
{:else if tab === 'notifications'}
  <div role="tabpanel" aria-label="Notifications"><Notifications {canAdmin} /></div>
{:else}
<div class="stack-lg" role="tabpanel" aria-label="General">
  <section class="card" aria-labelledby="su">
    <div class="card-head"><h2 id="su">Users</h2><span class="spacer"></span>{#if canAdmin}<button class="btn sm" onclick={() => { nu = { email: '', password: '', role: 'operator' }; uErr = {}; uOpen = true; }}><Icon name="plus" size={13} />Add user</button>{/if}</div>
    {#if users.error && !users.data}<ErrorBox message={users.error} onretry={users.reload} />
    {:else if !users.data}<Skeleton rows={3} />
    {:else}
      <div class="table-wrap bare">
        <table class="tbl">
          <thead><tr><th>Email</th><th>Role</th><th>Last sign-in</th><th>Created</th><th><span class="sr-only">Actions</span></th></tr></thead>
          <tbody>
            {#each users.data as u (u.id)}
              <tr><td>{u.email}{#if u.id === session.user?.id} <span class="badge">You</span>{/if}</td><td class="small">{u.role}</td><td class="small muted">{relTime(u.last_login_at)}</td><td class="small muted">{dateTime(u.created_at)}</td>
                <td class="actions">{#if canAdmin && u.id !== session.user?.id && u.role !== 'owner'}<button class="btn sm danger" onclick={() => delUser(u)}>Delete</button>{/if}</td></tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
  </section>

  <div class="grid-2">
    <section class="card" aria-labelledby="spw">
      <div class="card-head"><h2 id="spw">Change password</h2></div>
      <form class="stack" onsubmit={changePw} novalidate>
        <input type="text" autocomplete="username" value={session.user?.email ?? ''} hidden readonly />
        <div class="field"><label for="pc">Current password</label><input id="pc" class="input" type="password" autocomplete="current-password" bind:value={pw.current} aria-invalid={pwErr.current_password ? 'true' : undefined} />{#if pwErr.current_password}<span class="ferr">{pwErr.current_password}</span>{/if}</div>
        <div class="field"><label for="pn">New password</label><input id="pn" class="input" type="password" autocomplete="new-password" bind:value={pw.next} aria-invalid={pwErr.new_password ? 'true' : undefined} />{#if pwErr.new_password}<span class="ferr">{pwErr.new_password}</span>{:else}<span class="hint">At least 12 characters.</span>{/if}</div>
        <div class="field"><label for="pcf">Confirm new password</label><input id="pcf" class="input" type="password" autocomplete="new-password" bind:value={pw.confirm} aria-invalid={pwErr.confirm ? 'true' : undefined} />{#if pwErr.confirm}<span class="ferr">{pwErr.confirm}</span>{/if}</div>
        <div><button class="btn primary" type="submit" disabled={!pw.current || !pw.next}>Change password</button></div>
      </form>
    </section>

    <div class="stack-lg">
      <section class="card" aria-labelledby="sth">
        <div class="card-head"><h2 id="sth">Appearance</h2></div>
        <div class="seg" role="radiogroup" aria-label="Theme">
          {#each [['system', 'System'], ['dark', 'Dark'], ['light', 'Light']] as const as [v, l] (v)}
            <button role="radio" aria-checked={theme.pref === v} class:on={theme.pref === v} onclick={() => setTheme(v as ThemePref)}>{l}</button>
          {/each}
        </div>
      </section>
      <section class="card" aria-labelledby="sab">
        <div class="card-head"><h2 id="sab">About</h2></div>
        <dl class="kv">
          <dt>Loxy</dt><dd class="mono small">{health.data?.version ?? '—'}</dd>
          <dt>Status</dt><dd>{health.data?.status ?? '—'}</dd>
          <dt>Signed in as</dt><dd>{session.user?.email} ({session.user?.role})</dd>
          <dt>API</dt><dd class="mono small">/api/v1</dd>
        </dl>
      </section>
    </div>
  </div>
</div>

{/if}

<Modal bind:open={uOpen} title="Add user" width={440}>
  <form id="uf" class="stack" onsubmit={(e) => { e.preventDefault(); void createUser(); }} novalidate autocomplete="off">
    <div class="field"><label for="ue">Email</label><input id="ue" class="input" type="email" bind:value={nu.email} aria-invalid={uErr.email ? 'true' : undefined} />{#if uErr.email}<span class="ferr">{uErr.email}</span>{/if}</div>
    <div class="field"><label for="up">Initial password</label><input id="up" class="input" type="password" autocomplete="new-password" bind:value={nu.password} aria-invalid={uErr.password ? 'true' : undefined} />{#if uErr.password}<span class="ferr">{uErr.password}</span>{/if}</div>
    <div class="field"><label for="ur">Role</label>
      <select id="ur" class="select" bind:value={nu.role}><option value="admin">Admin — everything except owner settings</option><option value="operator">Operator — manage load balancers</option><option value="viewer">Viewer — read only</option></select></div>
  </form>
  {#snippet footer()}
    <button class="btn" onclick={() => (uOpen = false)}>Cancel</button>
    <button class="btn primary" type="submit" form="uf">Add user</button>
  {/snippet}
</Modal>

<style>
  .tabs-wrap { margin: calc(-1 * var(--sp-3)) 0 var(--sp-5); }
  .seg { display: inline-flex; padding: 2px; background: var(--surface-2); border-radius: 8px; }
  .seg button { border: 0; background: none; padding: 5px 14px; border-radius: 6px; color: var(--muted); cursor: pointer; font-weight: 500; font-size: var(--fs-sm); }
  .seg button.on { background: var(--surface); color: var(--text); box-shadow: 0 0 0 1px var(--border-strong); }
</style>
