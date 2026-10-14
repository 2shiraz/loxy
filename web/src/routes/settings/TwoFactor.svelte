<script lang="ts">
  import { api, unwrap, ApiError } from '$lib/api/client';
  import { session, loadSession } from '$lib/stores/session.svelte';
  import { toast } from '$lib/stores/toast.svelte';
  import CopyText from '$lib/components/CopyText.svelte';

  const enabled = $derived(!!session.user?.totp_enabled);

  let setup = $state<{ secret: string; otpauth_url: string } | null>(null);
  let disabling = $state(false);
  let code = $state('');
  let err = $state('');
  let busy = $state(false);
  const codeOk = $derived(/^\d{6}$/.test(code));
  const grouped = $derived(setup ? (setup.secret.match(/.{1,4}/g) ?? []).join(' ') : '');

  function reset() {
    setup = null;
    disabling = false;
    code = '';
    err = '';
  }

  async function run<T>(p: Promise<{ data?: T; error?: unknown; response: Response }>): Promise<T | undefined | false> {
    busy = true;
    err = '';
    try {
      return await unwrap(p);
    } catch (e) {
      const ae = e as ApiError;
      err = ae.fields.code ?? ae.message;
      return false;
    } finally {
      busy = false;
    }
  }

  async function start() {
    const r = await run(api.POST('/auth/totp/setup'));
    if (r === false) { toast.error(err); err = ''; return; }
    if (r) { setup = r; code = ''; }
  }

  async function enable(e: SubmitEvent) {
    e.preventDefault();
    if (!codeOk) return;
    const r = await run(api.POST('/auth/totp/enable', { body: { code } }));
    if (r === false) { code = ''; return; }
    reset();
    toast.success('Two-factor authentication enabled');
    await loadSession();
  }

  async function disable(e: SubmitEvent) {
    e.preventDefault();
    if (!codeOk) return;
    const r = await run(api.POST('/auth/totp/disable', { body: { code } }));
    if (r === false) { code = ''; return; }
    reset();
    toast.success('Two-factor authentication disabled');
    await loadSession();
  }

  const digitsOnly = () => (code = code.replace(/\D/g, '').slice(0, 6));
</script>

<section class="card" aria-labelledby="s2fa">
  <div class="card-head">
    <h2 id="s2fa">Two-factor authentication</h2>
    <span class="spacer"></span>
    {#if enabled}<span class="badge ok">On</span>{:else}<span class="badge">Off</span>{/if}
  </div>

  {#if setup}
    <form class="stack" onsubmit={enable} novalidate>
      <p class="small text-2">Add this key to your authenticator app, then enter the 6-digit code it shows.</p>
      <div class="field">
        <span class="label">Setup key</span>
        <span class="secret"><CopyText text={grouped} label="setup key" /></span>
      </div>
      <div class="field">
        <span class="label">Or open this link on a device with an authenticator app</span>
        <span class="url small"><CopyText text={setup.otpauth_url} label="otpauth link" /></span>
      </div>
      <div class="field">
        <label for="tfa-en">Code from app</label>
        <!-- svelte-ignore a11y_autofocus -->
        <input id="tfa-en" class="input mono code" type="text" inputmode="numeric" autocomplete="one-time-code" pattern="[0-9]*" maxlength="6"
          spellcheck="false" autofocus bind:value={code} oninput={digitsOnly} aria-invalid={err ? 'true' : undefined} aria-describedby={err ? 'tfa-en-err' : undefined} />
        {#if err}<span id="tfa-en-err" class="ferr" role="alert">{err}</span>{/if}
      </div>
      <div class="row-wrap">
        <button class="btn primary" type="submit" disabled={busy || !codeOk}>{busy ? 'Verifying…' : 'Turn on'}</button>
        <button class="btn ghost" type="button" onclick={reset} disabled={busy}>Cancel</button>
      </div>
    </form>
  {:else if disabling}
    <form class="stack" onsubmit={disable} novalidate>
      <p class="small text-2">Enter a current code from your authenticator app to turn two-factor authentication off.</p>
      <div class="field">
        <label for="tfa-dis">Code from app</label>
        <!-- svelte-ignore a11y_autofocus -->
        <input id="tfa-dis" class="input mono code" type="text" inputmode="numeric" autocomplete="one-time-code" pattern="[0-9]*" maxlength="6"
          spellcheck="false" autofocus bind:value={code} oninput={digitsOnly} aria-invalid={err ? 'true' : undefined} aria-describedby={err ? 'tfa-dis-err' : undefined} />
        {#if err}<span id="tfa-dis-err" class="ferr" role="alert">{err}</span>{/if}
      </div>
      <div class="row-wrap">
        <button class="btn danger" type="submit" disabled={busy || !codeOk}>{busy ? 'Verifying…' : 'Turn off'}</button>
        <button class="btn ghost" type="button" onclick={reset} disabled={busy}>Cancel</button>
      </div>
    </form>
  {:else if enabled}
    <div class="row-wrap">
      <p class="small text-2 spacer">Signing in asks for a code from your authenticator app.</p>
      <button class="btn sm" onclick={() => { reset(); disabling = true; }}>Disable</button>
    </div>
  {:else}
    <div class="row-wrap">
      <p class="small text-2 spacer">Require a code from an authenticator app when you sign in.</p>
      <button class="btn sm" onclick={start} disabled={busy}>{busy ? 'Starting…' : 'Enable'}</button>
    </div>
  {/if}
</section>

<style>
  .text-2 { color: var(--text-2); }
  .secret { font-size: var(--fs-md); letter-spacing: 0.04em; }
  .url { min-width: 0; color: var(--text-2); }
  .url :global(.copy) { max-width: 100%; }
  .code { width: 12ch; letter-spacing: 0.2em; font-variant-numeric: tabular-nums; }
</style>
