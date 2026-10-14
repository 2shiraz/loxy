<script lang="ts">
  /** Create / edit form for a backend (POST/PUT BackendInput). */
  import { goto } from '$app/navigation';
  import { api, mutate, type Backend, type BackendInput, type Labels } from '$lib/api/client';
  import { portError } from '$lib/util/validate';
  import Icon from './Icon.svelte';
  import LabelEditor from './LabelEditor.svelte';

  let { initial, onsaved }: { initial?: Backend; onsaved?: (b: Backend) => void } = $props();

  // svelte-ignore state_referenced_locally
  const src = initial;
  const hc0 = src?.health_check;
  let name = $state(src?.name ?? '');
  let description = $state(src?.description ?? '');
  let mode = $state<'http' | 'tcp'>(src?.mode ?? 'http');
  let algorithm = $state<NonNullable<BackendInput['algorithm']>>(src?.algorithm ?? 'roundrobin');
  let port = $state<number | ''>(src?.port ?? 80);
  let hcProtocol = $state<'tcp' | 'http' | 'https'>(hc0?.protocol ?? (src?.mode === 'tcp' ? 'tcp' : 'http'));
  let hcPort = $state<number | ''>(hc0?.port ?? '');
  let hcInterval = $state(hc0?.interval_s ?? 15);
  let hcTimeout = $state(hc0?.timeout_s ?? 10);
  let hcRetries = $state(hc0?.retries ?? 3);
  let hcDomain = $state(hc0?.http?.domain ?? '');
  let hcPath = $state(hc0?.http?.path ?? '/');
  let hcResponse = $state(hc0?.http?.response ?? '');
  let hcCodes = $state((hc0?.http?.status_codes ?? ['2??', '3??']).join(', '));
  let hcTlsVerify = $state(hc0?.http?.tls_verify ?? false);
  let sticky = $state(src?.sticky_sessions ?? false);
  let cookieName = $state(src?.cookie_name || 'LBSTICKY');
  let cookieLifetime = $state(src?.cookie_lifetime_s ?? 300);
  let proxyProtocol = $state<'none' | 'v1' | 'v2'>(src?.proxy_protocol ?? 'none');
  let timeoutServer = $state(src?.timeout_server_s ?? 60);
  let labels = $state<Labels>({ ...(src?.labels ?? {}) });
  let labelErr = $state('');

  let touched = $state(false);
  let serverErrs = $state<Record<string, string>>({});
  let busy = $state(false);

  function setMode(m: 'http' | 'tcp') {
    mode = m;
    if (m === 'tcp') { sticky = false; if (hcProtocol !== 'tcp') hcProtocol = 'tcp'; }
    else if (hcProtocol === 'tcp') hcProtocol = 'http';
  }
  const isHttpCheck = $derived(hcProtocol !== 'tcp');
  const codes = $derived(hcCodes.split(',').map((c) => c.trim()).filter(Boolean));

  const local = $derived.by(() => {
    const e: Record<string, string> = {};
    if (!name.trim()) e.name = 'Required';
    else if ([...name.trim()].length > 64) e.name = 'Up to 64 characters';
    const pe = portError(port);
    if (pe) e.port = pe;
    if (hcPort !== '' && portError(hcPort)) e['health_check.port'] = portError(hcPort);
    if (!(hcInterval >= 1 && hcInterval <= 3600)) e['health_check.interval_s'] = '1–3600 s';
    if (!(hcTimeout >= 1 && hcTimeout <= 3600)) e['health_check.timeout_s'] = '1–3600 s';
    else if (hcTimeout > hcInterval) e['health_check.timeout_s'] = 'Must not exceed the interval';
    if (!(hcRetries >= 1 && hcRetries <= 10)) e['health_check.retries'] = '1–10';
    if (isHttpCheck) {
      if (!hcPath.startsWith('/')) e['health_check.http.path'] = 'Must start with /';
      if (!codes.length) e['health_check.http.status_codes'] = 'At least one code, e.g. 2??';
      else if (codes.some((c) => !/^[1-5][0-9?]{2}$/.test(c))) e['health_check.http.status_codes'] = 'Use codes like 200 or patterns like 2??';
    }
    if (sticky) {
      if (!/^[A-Za-z0-9_-]{1,64}$/.test(cookieName)) e.cookie_name = 'Letters, digits, dash and underscore';
      if (!(cookieLifetime >= 1)) e.cookie_lifetime_s = 'At least 1 s';
    }
    if (!(timeoutServer >= 1 && timeoutServer <= 3600)) e.timeout_server_s = '1–3600 seconds';
    return e;
  });
  const errs = $derived({ ...(touched ? local : {}), ...serverErrs } as Record<string, string>);
  const fe = (k: string) => errs[k];

  function body(): BackendInput {
    return {
      name: name.trim(),
      description: description.trim(),
      mode,
      algorithm,
      port: Number(port),
      health_check: {
        protocol: hcProtocol,
        ...(hcPort !== '' ? { port: Number(hcPort) } : {}),
        interval_s: Number(hcInterval),
        timeout_s: Number(hcTimeout),
        retries: Number(hcRetries),
        http: isHttpCheck
          ? { domain: hcDomain.trim() || null, path: hcPath.trim(), response: hcResponse.trim() || null, status_codes: codes, tls_verify: hcProtocol === 'https' && hcTlsVerify }
          : null
      },
      sticky_sessions: mode === 'http' && sticky,
      cookie_name: cookieName,
      cookie_lifetime_s: Number(cookieLifetime),
      proxy_protocol: proxyProtocol,
      timeout_server_s: Number(timeoutServer),
      labels
    };
  }

  let formEl: HTMLFormElement | undefined = $state();
  async function submit(e: SubmitEvent) {
    e.preventDefault();
    touched = true;
    serverErrs = {};
    const focus = () => queueMicrotask(() => formEl?.querySelector<HTMLElement>('[aria-invalid="true"]')?.focus());
    if (Object.keys(local).length || labelErr) { focus(); return; }
    busy = true;
    const onFieldErrors = (f: Record<string, string>) => { serverErrs = f; focus(); };
    const r = initial
      ? await mutate(api.PUT('/backends/{id}', { params: { path: { id: initial.id } }, body: body() }), { success: 'Backend saved. Applying…', onFieldErrors })
      : await mutate(api.POST('/backends', { body: body() }), { success: 'Backend created', onFieldErrors });
    busy = false;
    if (!r?.backend) return;
    if (onsaved) onsaved(r.backend);
    else void goto(`/backends/${r.backend.id}`);
  }
  const errCount = $derived(Object.keys(errs).length + (labelErr ? 1 : 0));
</script>

{#snippet num(id: string, label: string, key: string, hint: string, get: () => number | '', set: (v: number | '') => void, min = 1, max = 3600, placeholder = '')}
  <div class="field">
    <label for={id}>{label}</label>
    <input {id} class="input mono" type="number" {min} {max} {placeholder} value={get()} oninput={(e) => { const v = (e.currentTarget as HTMLInputElement).value; set(v === '' ? '' : Number(v)); }}
      aria-invalid={fe(key) ? 'true' : undefined} aria-describedby="{id}-h" />
    {#if fe(key)}<span class="ferr" id="{id}-h">{fe(key)}</span>{:else if hint}<span class="hint" id="{id}-h">{hint}</span>{/if}
  </div>
{/snippet}

<form class="bf" bind:this={formEl} onsubmit={submit} novalidate>
  <section class="card">
    <h2 class="sec">General</h2>
    <div class="form-grid">
      <div class="field">
        <label for="b-name">Name</label>
        <input id="b-name" class="input" bind:value={name} placeholder="app-servers" autocomplete="off" spellcheck="false" aria-invalid={fe('name') ? 'true' : undefined} aria-describedby="b-name-e" />
        {#if fe('name')}<span class="ferr" id="b-name-e">{fe('name')}</span>{/if}
      </div>
      <div class="field">
        <span class="label" id="b-mode-l">Mode</span>
        <div class="seg" role="radiogroup" aria-labelledby="b-mode-l">
          <button type="button" role="radio" aria-checked={mode === 'http'} class:on={mode === 'http'} onclick={() => setMode('http')}>HTTP</button>
          <button type="button" role="radio" aria-checked={mode === 'tcp'} class:on={mode === 'tcp'} onclick={() => setMode('tcp')}>TCP</button>
        </div>
        {#if initial?.used_by.length}<span class="hint">Used by {initial.used_by.length} frontend{initial.used_by.length === 1 ? '' : 's'}; they must use the same mode.</span>{/if}
        {#if fe('mode')}<span class="ferr">{fe('mode')}</span>{/if}
      </div>
    </div>
    <div class="field">
      <label for="b-desc">Description <span class="muted">(optional)</span></label>
      <input id="b-desc" class="input" bind:value={description} autocomplete="off" />
    </div>
    <div class="form-grid">
      <div class="field">
        <label for="b-alg">Algorithm</label>
        <select id="b-alg" class="select" bind:value={algorithm}>
          <option value="roundrobin">Round robin</option>
          <option value="leastconn">Least connections</option>
          <option value="source">Source IP hash</option>
          <option value="random">Random</option>
        </select>
      </div>
      {@render num('b-port', 'Default server port', 'port', 'Used for servers added without a port.', () => port, (v) => (port = v), 1, 65535)}
      {@render num('b-ts', 'Server timeout (s)', 'timeout_server_s', 'Max wait for a server response.', () => timeoutServer, (v) => (timeoutServer = Number(v)))}
    </div>
  </section>

  <section class="card">
    <h2 class="sec">Health check</h2>
    <div class="form-grid">
      <div class="field">
        <label for="b-hcp">Protocol</label>
        <select id="b-hcp" class="select" bind:value={hcProtocol}>
          <option value="tcp">TCP connect</option>
          <option value="http">HTTP</option>
          <option value="https">HTTPS</option>
        </select>
      </div>
      {@render num('b-hcport', 'Port', 'health_check.port', 'Defaults to the server port.', () => hcPort, (v) => (hcPort = v), 1, 65535, 'server port')}
      {@render num('b-hci', 'Interval (s)', 'health_check.interval_s', '', () => hcInterval, (v) => (hcInterval = Number(v)))}
      {@render num('b-hct', 'Timeout (s)', 'health_check.timeout_s', '', () => hcTimeout, (v) => (hcTimeout = Number(v)))}
      {@render num('b-hcr', 'Retries', 'health_check.retries', 'Failures before marking down.', () => hcRetries, (v) => (hcRetries = Number(v)), 1, 10)}
    </div>
    {#if isHttpCheck}
      <div class="form-grid">
        <div class="field">
          <label for="b-hcd">Host header <span class="muted">(optional)</span></label>
          <input id="b-hcd" class="input mono" bind:value={hcDomain} placeholder="app.example.com" spellcheck="false" autocomplete="off" />
        </div>
        <div class="field">
          <label for="b-hcpath">Path</label>
          <input id="b-hcpath" class="input mono" bind:value={hcPath} spellcheck="false" aria-invalid={fe('health_check.http.path') ? 'true' : undefined} />
          {#if fe('health_check.http.path')}<span class="ferr">{fe('health_check.http.path')}</span>{/if}
        </div>
        <div class="field">
          <label for="b-hcc">Status codes</label>
          <input id="b-hcc" class="input mono" bind:value={hcCodes} spellcheck="false" aria-invalid={fe('health_check.http.status_codes') ? 'true' : undefined} aria-describedby="b-hcc-h" />
          {#if fe('health_check.http.status_codes')}<span class="ferr" id="b-hcc-h">{fe('health_check.http.status_codes')}</span>{:else}<span class="hint" id="b-hcc-h">Comma separated; ? matches any digit.</span>{/if}
        </div>
        <div class="field">
          <label for="b-hcresp">Response contains <span class="muted">(optional)</span></label>
          <input id="b-hcresp" class="input mono" bind:value={hcResponse} spellcheck="false" autocomplete="off" />
        </div>
      </div>
      {#if hcProtocol === 'https'}
        <label class="check"><input type="checkbox" bind:checked={hcTlsVerify} />Verify the server's TLS certificate</label>
      {/if}
    {/if}
  </section>

  <section class="card">
    <h2 class="sec">Connection</h2>
    {#if mode === 'http'}
      <label class="check"><input type="checkbox" bind:checked={sticky} />Sticky sessions <span class="muted small">— pin a client to one server with a cookie</span></label>
      {#if sticky}
        <div class="form-grid ind">
          <div class="field">
            <label for="b-ck">Cookie name</label>
            <input id="b-ck" class="input mono" bind:value={cookieName} spellcheck="false" aria-invalid={fe('cookie_name') ? 'true' : undefined} />
            {#if fe('cookie_name')}<span class="ferr">{fe('cookie_name')}</span>{/if}
          </div>
          {@render num('b-ckl', 'Cookie lifetime (s)', 'cookie_lifetime_s', '', () => cookieLifetime, (v) => (cookieLifetime = Number(v)), 1, 31536000)}
        </div>
      {/if}
    {/if}
    <div class="field pp">
      <label for="b-pp">PROXY protocol</label>
      <select id="b-pp" class="select" bind:value={proxyProtocol}>
        <option value="none">Off</option>
        <option value="v1">v1 (text)</option>
        <option value="v2">v2 (binary)</option>
      </select>
    </div>
    {#if proxyProtocol !== 'none'}
      <div class="banner err small" role="alert">
        <Icon name="alert" />
        <span>Your servers must accept the PROXY protocol header. Servers that don't will reject or misread every connection, and health checks may still pass.</span>
      </div>
    {/if}
  </section>

  <section class="card">
    <h2 class="sec">Labels</h2>
    <LabelEditor bind:labels bind:error={labelErr} />
  </section>

  <div class="foot">
    {#if touched && errCount}<span class="err-text small" role="alert">Fix {errCount} field{errCount === 1 ? '' : 's'} above.</span>{/if}
    <span class="spacer"></span>
    <button class="btn primary" type="submit" disabled={busy}>{busy ? 'Saving…' : initial ? 'Save backend' : 'Create backend'}</button>
  </div>
</form>

<style>
  .bf { display: flex; flex-direction: column; gap: 14px; max-width: 900px; }
  .bf > .card { display: flex; flex-direction: column; gap: 12px; }
  .sec { font-size: var(--fs-md); }
  .seg { display: inline-flex; padding: 2px; background: var(--surface-2); border-radius: 8px; align-self: flex-start; }
  .seg button { border: 0; background: none; padding: 5px 14px; border-radius: 6px; color: var(--muted); cursor: pointer; font-weight: 500; font-size: var(--fs-sm); }
  .seg button.on { background: var(--surface); color: var(--text); box-shadow: 0 0 0 1px var(--border-strong); }
  .ind { padding-left: 23px; }
  .pp .select { width: min(240px, 100%); }
  .banner :global(.icon) { color: var(--err); margin-top: 2px; }
  .foot { position: sticky; bottom: 0; display: flex; align-items: center; gap: 8px; padding: 12px 0; background: linear-gradient(transparent, var(--bg) 30%); }
</style>
