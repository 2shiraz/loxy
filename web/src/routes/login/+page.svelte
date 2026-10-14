<script lang="ts">
  import { goto } from '$app/navigation';
  import { page } from '$app/state';
  import { api, setCsrf } from '$lib/api/client';
  import { session } from '$lib/stores/session.svelte';

  let email = $state('');
  let password = $state('');
  let error = $state('');
  let busy = $state(false);

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    error = '';
    busy = true;
    try {
      const r = await api.POST('/auth/login', { body: { email, password } });
      if (r.data) {
        setCsrf(r.data.csrf_token);
        session.user = r.data.user;
        const next = page.url.searchParams.get('next');
        void goto(next && next.startsWith('/') && !next.startsWith('//') ? next : '/load-balancers', { replaceState: true });
      } else if (r.response.status === 429) {
        error = 'Too many attempts. Wait a minute and try again.';
      } else {
        error = (r.error as { error?: { message?: string } })?.error?.message ?? 'Email or password is incorrect.';
      }
    } catch {
      error = 'The panel is unreachable. Check your connection and try again.';
    } finally {
      busy = false;
    }
  }
</script>

<svelte:head><title>Sign in · Loxy</title></svelte:head>

<div class="wrap">
  <form class="box" onsubmit={submit} novalidate>
    <div class="brand">
      <svg width="28" height="28" viewBox="0 0 32 32" aria-hidden="true"><rect width="32" height="32" rx="7" fill="var(--surface-3)"/><path d="M9 8v16h6M17 12l6 6m0-6-6 6" stroke="var(--accent)" stroke-width="2.6" fill="none" stroke-linecap="round" stroke-linejoin="round"/></svg>
      <h1>Sign in to Loxy</h1>
    </div>
    <div class="field">
      <label for="email">Email</label>
      <input id="email" class="input" type="email" autocomplete="username" required bind:value={email} spellcheck="false" />
    </div>
    <div class="field">
      <label for="password">Password</label>
      <input id="password" class="input" type="password" autocomplete="current-password" required bind:value={password} />
    </div>
    {#if error}<p class="err small" role="alert">{error}</p>{/if}
    <button class="btn primary" type="submit" disabled={busy || !email || !password}>{busy ? 'Signing in…' : 'Sign in'}</button>
  </form>
</div>

<style>
  .wrap { min-height: 100dvh; display: grid; place-items: center; padding: 16px; }
  .box { width: min(360px, 100%); display: flex; flex-direction: column; gap: 14px; padding: 28px; background: var(--surface); border: 1px solid var(--border); border-radius: 14px; }
  .brand { display: flex; align-items: center; gap: 12px; margin-bottom: 6px; }
  h1 { font-size: var(--fs-xl); }
  .err { color: var(--err); }
  .btn { height: 36px; margin-top: 4px; }
</style>
