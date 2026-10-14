<script lang="ts">
  /**
   * Create / edit form for a frontend (POST/PUT FrontendInput).
   * Server field errors use keys like `binds[0].port` or `routes[1].hosts`.
   */
  import { goto } from '$app/navigation';
  import { api, unwrap, mutate, type Frontend, type FrontendInput, type Labels } from '$lib/api/client';
  import { resource } from '$lib/stores/resource.svelte';
  import { portError, fqdnError } from '$lib/util/validate';
  import Icon from './Icon.svelte';
  import LabelEditor from './LabelEditor.svelte';
  import CertificatePicker from './CertificatePicker.svelte';

  let { initial, oncancel, onsaved }: { initial?: Frontend; oncancel?: () => void; onsaved?: (f: Frontend) => void } = $props();

  interface RouteRow { id?: number; hosts: string; path_prefix: string; backend_id: number | '' }
  interface BindRow { address: string; port: number | '' }

  // svelte-ignore state_referenced_locally
  const src = initial;
  let name = $state(src?.name ?? '');
  let description = $state(src?.description ?? '');
  let mode = $state<'http' | 'tcp'>(src?.mode ?? 'http');
  let tls = $state(src?.tls ?? false);
  let certificates = $state<number[]>(src?.certificates ?? []);
  let httpRedirect = $state(src?.http_redirect ?? true);
  let binds = $state<BindRow[]>(src?.binds.map((b) => ({ ...b })) ?? [{ address: '*', port: 80 }]);
  let defaultBackend = $state<number | ''>(src?.default_backend_id ?? '');
  let routes = $state<RouteRow[]>(src?.routes.map((r) => ({ id: r.id, hosts: (r.hosts ?? []).join(', '), path_prefix: r.path_prefix ?? '', backend_id: r.backend_id })) ?? []);
  let timeoutClient = $state(src?.timeout_client_s ?? 60);
  let maxconn = $state(src?.maxconn ?? 0);
  let forwarded = $state(src?.forwarded_headers ?? true);
  let enabled = $state(src?.enabled ?? true);
  let labels = $state<Labels>({ ...(src?.labels ?? {}) });
  let labelErr = $state('');

  let touched = $state(false);
  let serverErrs = $state<Record<string, string>>({});
  let busy = $state(false);

  const backends = resource(async () => (await unwrap(api.GET('/backends'))).backends, { keys: () => ['backends'] });
  const certs = resource(async () => (await unwrap(api.GET('/certificates'))).certificates, { keys: () => ['certificates'] });
  const nodes = resource(async () => (await unwrap(api.GET('/nodes'))).nodes);
  const sameMode = $derived((backends.data ?? []).filter((b) => b.mode === mode));
  const addrs = $derived((nodes.data ?? []).flatMap((n) => n.ips));

  function setMode(m: 'http' | 'tcp') {
    if (m === mode) return;
    mode = m;
    if (m === 'tcp') tls = false;
    if (defaultBackend !== '' && !sameMode.some((b) => b.id === defaultBackend)) defaultBackend = '';
    serverErrs = {};
  }
  function toggleTls() {
    if (!tls) return;
    // Suggest 443 when switching an untouched :80 bind to TLS.
    if (binds.length === 1 && binds[0].port === 80) binds[0].port = 443;
  }

  const splitHosts = (t: string) => t.split(',').map((h) => h.trim().toLowerCase()).filter(Boolean);

  const local = $derived.by(() => {
    const e: Record<string, string> = {};
    if (!name.trim()) e.name = 'Required';
    else if ([...name.trim()].length > 64) e.name = 'Up to 64 characters';
    if (!binds.length) e.binds = 'Add at least one bind address';
    const seen = new Set<string>();
    binds.forEach((b, i) => {
      if (!b.address.trim()) e[`binds[${i}].address`] = 'Required';
      const pe = portError(b.port);
      if (pe) e[`binds[${i}].port`] = pe;
      const k = `${b.address.trim()}:${b.port}`;
      if (!pe && seen.has(k)) e[`binds[${i}].port`] = 'Duplicate bind';
      seen.add(k);
    });
    if (tls && !certificates.length) e.certificates = 'TLS needs at least one certificate';
    if (mode === 'tcp' && defaultBackend === '') e.default_backend_id = 'A TCP frontend needs a default backend';
    if (mode === 'http') {
      routes.forEach((r, i) => {
        if (r.backend_id === '') e[`routes[${i}].backend_id`] = 'Choose a backend';
        const hs = splitHosts(r.hosts);
        const bad = hs.find((h) => fqdnError(h));
        if (bad) e[`routes[${i}].hosts`] = `${bad} is not a valid host name`;
        if (r.path_prefix && !r.path_prefix.startsWith('/')) e[`routes[${i}].path_prefix`] = 'Must start with /';
        if (!hs.length && !r.path_prefix) e[`routes[${i}].hosts`] = 'Give hosts, a path prefix, or both';
      });
    }
    if (!(timeoutClient >= 1 && timeoutClient <= 3600)) e.timeout_client_s = '1–3600 seconds';
    if (!(maxconn >= 0)) e.maxconn = 'Must be 0 or more';
    return e;
  });
  const errs = $derived({ ...(touched ? local : {}), ...serverErrs } as Record<string, string>);
  const errCount = $derived(Object.keys(errs).length + (labelErr ? 1 : 0));

  function body(): FrontendInput {
    return {
      name: name.trim(),
      description: description.trim(),
      mode,
      tls: mode === 'http' && tls,
      binds: binds.map((b) => ({ address: b.address.trim(), port: Number(b.port) })),
      certificates: mode === 'http' && tls ? certificates : [],
      http_redirect: mode === 'http' && tls && httpRedirect,
      default_backend_id: defaultBackend === '' ? null : defaultBackend,
      routes: mode === 'http'
        ? routes.map((r) => ({ ...(r.id ? { id: r.id } : {}), hosts: splitHosts(r.hosts), path_prefix: r.path_prefix.trim(), backend_id: r.backend_id as number }))
        : [],
      timeout_client_s: Number(timeoutClient),
      maxconn: Number(maxconn),
      forwarded_headers: mode === 'http' && forwarded,
      enabled,
      labels
    };
  }

  let formEl: HTMLFormElement | undefined = $state();
  function focusFirstError() {
    queueMicrotask(() => formEl?.querySelector<HTMLElement>('[aria-invalid="true"]')?.focus());
  }

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    touched = true;
    serverErrs = {};
    if (Object.keys(local).length || labelErr) { focusFirstError(); return; }
    busy = true;
    const onFieldErrors = (f: Record<string, string>) => { serverErrs = f; focusFirstError(); };
    const r = initial
      ? await mutate(api.PUT('/frontends/{id}', { params: { path: { id: initial.id } }, body: body() }), { success: 'Frontend saved. Applying…', onFieldErrors })
      : await mutate(api.POST('/frontends', { body: body() }), { success: 'Frontend created. Applying…', onFieldErrors });
    busy = false;
    if (!r?.frontend) return;
    onsaved?.(r.frontend);
    void goto(`/frontends/${r.frontend.id}`);
  }

  function moveRoute(i: number, d: -1 | 1) {
    const j = i + d;
    if (j < 0 || j >= routes.length) return;
    const next = [...routes];
    [next[i], next[j]] = [next[j], next[i]];
    routes = next;
    serverErrs = {};
  }
  const fe = (k: string) => errs[k];
</script>

<form class="ff" bind:this={formEl} onsubmit={submit} novalidate>
  <section class="card">
    <h2 class="sec">General</h2>
    <div class="form-grid">
      <div class="field">
        <label for="f-name">Name</label>
        <input id="f-name" class="input" bind:value={name} autocomplete="off" spellcheck="false" aria-invalid={fe('name') ? 'true' : undefined} aria-describedby="f-name-e" placeholder="web-public" />
        {#if fe('name')}<span class="ferr" id="f-name-e">{fe('name')}</span>{/if}
      </div>
      <div class="field">
        <span class="label" id="f-mode-l">Mode</span>
        <div class="seg" role="radiogroup" aria-labelledby="f-mode-l">
          <button type="button" role="radio" aria-checked={mode === 'http'} class:on={mode === 'http'} onclick={() => setMode('http')}>HTTP</button>
          <button type="button" role="radio" aria-checked={mode === 'tcp'} class:on={mode === 'tcp'} onclick={() => setMode('tcp')}>TCP</button>
        </div>
      </div>
    </div>
    <div class="field">
      <label for="f-desc">Description <span class="muted">(optional)</span></label>
      <input id="f-desc" class="input" bind:value={description} autocomplete="off" />
    </div>
  </section>

  <section class="card">
    <div class="card-head"><h2 class="sec">Binds</h2><span class="muted small">Addresses and ports HAProxy listens on</span></div>
    <datalist id="bind-addrs">
      <option value="*">All addresses</option>
      <option value="::">All IPv6 addresses</option>
      {#each addrs as a (a.id)}<option value={a.address}>{a.label || `IPv${a.family}`}</option>{/each}
    </datalist>
    <div class="rows">
      {#each binds as b, i (i)}
        <div class="brow">
          <div class="field">
            <label for="b-a{i}" class:sr-only={i > 0}>Address</label>
            <input id="b-a{i}" class="input mono" list="bind-addrs" bind:value={b.address} placeholder="*" spellcheck="false" autocomplete="off"
              aria-invalid={fe(`binds[${i}].address`) ? 'true' : undefined} aria-describedby="b-a{i}-e" />
            {#if fe(`binds[${i}].address`)}<span class="ferr" id="b-a{i}-e">{fe(`binds[${i}].address`)}</span>{/if}
          </div>
          <div class="field port">
            <label for="b-p{i}" class:sr-only={i > 0}>Port</label>
            <input id="b-p{i}" class="input mono" type="number" min="1" max="65535" inputmode="numeric" bind:value={b.port}
              aria-invalid={fe(`binds[${i}].port`) ? 'true' : undefined} aria-describedby="b-p{i}-e" />
            {#if fe(`binds[${i}].port`)}<span class="ferr" id="b-p{i}-e">{fe(`binds[${i}].port`)}</span>{/if}
          </div>
          <button type="button" class="btn ghost icon rm" class:first={i === 0} aria-label="Remove bind {i + 1}" disabled={binds.length === 1}
            onclick={() => { binds = binds.filter((_, k) => k !== i); serverErrs = {}; }}><Icon name="x" size={14} /></button>
        </div>
      {/each}
    </div>
    <div class="row">
      <button type="button" class="btn sm" onclick={() => binds.push({ address: '*', port: '' })}><Icon name="plus" size={13} />Add bind</button>
      {#if fe('binds')}<span class="ferr small err-text">{fe('binds')}</span>{/if}
      <span class="hint xs muted">“*” listens on every address. Pick a node address to listen on one interface only.</span>
    </div>
  </section>

  {#if mode === 'http'}
    <section class="card">
      <h2 class="sec">TLS</h2>
      <label class="check"><input type="checkbox" bind:checked={tls} onchange={toggleTls} />Terminate TLS (HTTPS)</label>
      {#if tls}
        <div class="stack tlsb">
          <CertificatePicker id="f-cert" bind:selected={certificates} certificates={certs.data ?? []} error={fe('certificates')} />
          <label class="check"><input type="checkbox" bind:checked={httpRedirect} />Redirect HTTP → HTTPS <span class="muted small">(301 from :80)</span></label>
        </div>
      {/if}
    </section>
  {/if}

  <section class="card">
    <div class="card-head"><h2 class="sec">Routing</h2></div>
    <div class="field db">
      <label for="f-db">Default backend</label>
      <div class="row-wrap">
        <select id="f-db" class="select" bind:value={defaultBackend} aria-invalid={fe('default_backend_id') ? 'true' : undefined} aria-describedby="f-db-e">
          <option value="">{mode === 'http' ? 'None — unmatched requests get 503' : 'Choose a backend…'}</option>
          {#each sameMode as b (b.id)}<option value={b.id}>{b.name} · :{b.port}</option>{/each}
        </select>
        <a class="small" href="/backends/new">Create backend</a>
      </div>
      {#if fe('default_backend_id')}<span class="ferr" id="f-db-e">{fe('default_backend_id')}</span>
      {:else if backends.data && !sameMode.length}<span class="hint" id="f-db-e">No {mode.toUpperCase()} backends yet. Create one first.</span>
      {:else}<span class="hint" id="f-db-e">Only {mode.toUpperCase()} backends are listed.</span>{/if}
    </div>

    {#if mode === 'http'}
      <h3 class="sub">Rules <span class="muted small">— evaluated top to bottom; the first match wins</span></h3>
      {#if routes.length}
        <div class="table-wrap bare rt">
          <table class="tbl">
            <thead><tr><th>Hosts</th><th>Path prefix</th><th>Backend</th><th><span class="sr-only">Order and remove</span></th></tr></thead>
            <tbody>
              {#each routes as r, i (i)}
                <tr>
                  <td>
                    <input class="input mono" aria-label="Hosts for rule {i + 1}" placeholder="example.com, *.example.com" bind:value={r.hosts} spellcheck="false" autocomplete="off"
                      aria-invalid={fe(`routes[${i}].hosts`) ? 'true' : undefined} />
                    {#if fe(`routes[${i}].hosts`)}<span class="ferr xs">{fe(`routes[${i}].hosts`)}</span>{/if}
                  </td>
                  <td class="pp">
                    <input class="input mono" aria-label="Path prefix for rule {i + 1}" placeholder="/api" bind:value={r.path_prefix} spellcheck="false" autocomplete="off"
                      aria-invalid={fe(`routes[${i}].path_prefix`) ? 'true' : undefined} />
                    {#if fe(`routes[${i}].path_prefix`)}<span class="ferr xs">{fe(`routes[${i}].path_prefix`)}</span>{/if}
                  </td>
                  <td class="bk">
                    <select class="select" aria-label="Backend for rule {i + 1}" bind:value={r.backend_id} aria-invalid={fe(`routes[${i}].backend_id`) ? 'true' : undefined}>
                      <option value="">Choose…</option>
                      {#each sameMode as b (b.id)}<option value={b.id}>{b.name}</option>{/each}
                    </select>
                    {#if fe(`routes[${i}].backend_id`)}<span class="ferr xs">{fe(`routes[${i}].backend_id`)}</span>{/if}
                  </td>
                  <td class="actions">
                    <span class="row ord">
                      <button type="button" class="btn ghost icon sm" aria-label="Move rule {i + 1} up" disabled={i === 0} onclick={() => moveRoute(i, -1)}><Icon name="arrowUp" size={14} /></button>
                      <button type="button" class="btn ghost icon sm" aria-label="Move rule {i + 1} down" disabled={i === routes.length - 1} onclick={() => moveRoute(i, 1)}><Icon name="arrowDown" size={14} /></button>
                      <button type="button" class="btn ghost icon sm" aria-label="Remove rule {i + 1}" onclick={() => { routes = routes.filter((_, k) => k !== i); serverErrs = {}; }}><Icon name="x" size={14} /></button>
                    </span>
                  </td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      {:else}
        <p class="muted small">No rules. Every request goes to the default backend.</p>
      {/if}
      <div><button type="button" class="btn sm" onclick={() => routes.push({ hosts: '', path_prefix: '', backend_id: '' })}><Icon name="plus" size={13} />Add rule</button></div>
    {/if}
  </section>

  <section class="card">
    <h2 class="sec">Options</h2>
    <div class="form-grid">
      <div class="field">
        <label for="f-tc">Client timeout (s)</label>
        <input id="f-tc" class="input mono" type="number" min="1" max="3600" bind:value={timeoutClient} aria-invalid={fe('timeout_client_s') ? 'true' : undefined} />
        {#if fe('timeout_client_s')}<span class="ferr">{fe('timeout_client_s')}</span>{:else}<span class="hint">Idle time before HAProxy closes a client connection.</span>{/if}
      </div>
      <div class="field">
        <label for="f-mc">Max connections</label>
        <input id="f-mc" class="input mono" type="number" min="0" bind:value={maxconn} aria-invalid={fe('maxconn') ? 'true' : undefined} />
        {#if fe('maxconn')}<span class="ferr">{fe('maxconn')}</span>{:else}<span class="hint">0 = unlimited (process limit applies).</span>{/if}
      </div>
    </div>
    <div class="stack opts">
      {#if mode === 'http'}
        <label class="check"><input type="checkbox" bind:checked={forwarded} />Add X-Forwarded-For / -Proto headers</label>
      {/if}
      <label class="check"><input type="checkbox" bind:checked={enabled} />Enabled <span class="muted small">— a disabled frontend stays in the config but does not listen</span></label>
    </div>
    <div class="field lbl">
      <span class="label">Labels</span>
      <LabelEditor bind:labels bind:error={labelErr} />
    </div>
  </section>

  <div class="foot">
    {#if touched && errCount}<span class="err-text small" role="alert">Fix {errCount} field{errCount === 1 ? '' : 's'} above.</span>{/if}
    <span class="spacer"></span>
    {#if oncancel}<button type="button" class="btn" onclick={oncancel}>Cancel</button>{/if}
    <button class="btn primary" type="submit" disabled={busy}>{busy ? 'Saving…' : initial ? 'Save frontend' : 'Create frontend'}</button>
  </div>
</form>

<style>
  .ff { display: flex; flex-direction: column; gap: 14px; max-width: 900px; }
  .ff > .card { display: flex; flex-direction: column; gap: 12px; }
  .sec { font-size: var(--fs-md); }
  .card-head { margin-bottom: 0; gap: 12px; }
  .sub { font-size: var(--fs-md); font-weight: 600; margin-top: 6px; }
  .seg { display: inline-flex; padding: 2px; background: var(--surface-2); border-radius: 8px; align-self: flex-start; }
  .seg button { border: 0; background: none; padding: 5px 14px; border-radius: 6px; color: var(--muted); cursor: pointer; font-weight: 500; font-size: var(--fs-sm); }
  .seg button.on { background: var(--surface); color: var(--text); box-shadow: 0 0 0 1px var(--border-strong); }
  .rows { display: flex; flex-direction: column; gap: 8px; }
  .brow { display: grid; grid-template-columns: minmax(0, 1fr) 120px 32px; gap: 8px; align-items: start; }
  .brow .rm.first { margin-top: 23px; }
  .tlsb { padding-left: 23px; }
  .db .select { width: min(360px, 100%); }
  .rt .tbl td { vertical-align: top; padding: 6px 8px 6px 0; }
  .rt .tbl th { padding-left: 0; }
  .rt .ferr { display: block; margin-top: 3px; color: var(--err); white-space: normal; }
  .pp { width: 160px; }
  .bk { width: 200px; }
  .ord { gap: 0; }
  .opts { gap: 8px; }
  .lbl { margin-top: 4px; }
  .foot { position: sticky; bottom: 0; display: flex; align-items: center; gap: 8px; padding: 12px 0; background: linear-gradient(transparent, var(--bg) 30%); }
  @media (max-width: 640px) {
    .rt .tbl, .rt tbody, .rt tr, .rt td { display: block; width: 100%; }
    .rt thead { display: none; }
    .rt tr { border-bottom: 1px solid var(--border); padding: 6px 0; }
  }
</style>
