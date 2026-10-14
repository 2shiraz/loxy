<script lang="ts">
  /** Inline service form (§8.7) with advanced settings modal. */
  import type { ServiceInput, Certificate } from '$lib/api/client';
  import { validateService, normalise, hcSummary } from '$lib/util/services';
  import Icon from './Icon.svelte';
  import Modal from './Modal.svelte';
  import PortField from './PortField.svelte';

  let {
    value,
    others,
    certificates,
    serverErrors = {},
    busy = false,
    submitLabel = 'Save service',
    onsave,
    oncancel
  }: {
    value: ServiceInput;
    others: { listen_port: number }[];
    certificates: Certificate[];
    serverErrors?: Record<string, string>;
    busy?: boolean;
    submitLabel?: string;
    onsave: (s: ServiceInput) => void;
    oncancel: () => void;
  } = $props();

  // svelte-ignore state_referenced_locally
  let s = $state<ServiceInput>(normalise(value));
  let touched = $state(false);
  let advOpen = $state(false);
  let certPick = $state<number | ''>('');
  const uid = Math.random().toString(36).slice(2, 7);

  const local = $derived(validateService(s, others));
  const errs = $derived({ ...(touched ? local : {}), ...serverErrors } as Record<string, string>);
  const certs = $derived(s.http?.certificates ?? []);
  const certName = (id: number) => certificates.find((c) => c.id === id)?.name ?? `#${id}`;

  function submit(e: SubmitEvent) {
    e.preventDefault();
    touched = true;
    if (Object.keys(local).length) return;
    onsave(normalise(s));
  }
  function addCert() {
    if (certPick === '' || !s.http) return;
    if (!s.http.certificates) s.http.certificates = [];
    if (!s.http.certificates.includes(certPick)) s.http.certificates.push(certPick);
    certPick = '';
  }
  function setProtocol(p: 'tcp' | 'http' | 'https') {
    s.protocol = p;
    s = normalise(s);
    if (s.health_check) s.health_check.protocol = p === 'tcp' ? 'tcp' : 'http';
    s = normalise(s);
  }
  const codesText = {
    get v() { return (s.health_check?.http?.status_codes ?? []).join(', '); },
    set v(t: string) { if (s.health_check?.http) s.health_check.http.status_codes = t.split(',').map((x) => x.trim()).filter(Boolean); }
  };
</script>

<form class="editor" onsubmit={submit} novalidate>
  <div class="form-grid">
    <div class="field">
      <label for="p-{uid}">Protocol</label>
      <select id="p-{uid}" class="select" value={s.protocol} onchange={(e) => setProtocol((e.currentTarget as HTMLSelectElement).value as 'tcp')}>
        <option value="tcp">TCP</option><option value="http">HTTP</option><option value="https">HTTPS</option>
      </select>
    </div>
    <PortField id="l-{uid}" label="Source port" bind:value={s.listen_port} error={errs.listen_port} />
    <PortField id="d-{uid}" label="Destination port" bind:value={s.destination_port} error={errs.destination_port} />
  </div>

  {#if s.protocol === 'https' && s.http}
    <label class="check"><input type="checkbox" bind:checked={s.http.redirect_http} />HTTP redirect (301 from :80 to HTTPS)</label>
    {#if errs.redirect_http}<span class="ferr small err-text">{errs.redirect_http}</span>{/if}

    <div class="field">
      <span class="label">Certificates</span>
      <div class="row-wrap">
        {#each certs as cid (cid)}
          <span class="chip"><Icon name="shield" size={11} />{certName(cid)}
            <button type="button" class="x" aria-label="Remove certificate {certName(cid)}" onclick={() => s.http!.certificates = certs.filter((c) => c !== cid)}><Icon name="x" size={11} /></button>
          </span>
        {/each}
        <select class="select sm" aria-label="Add certificate" bind:value={certPick} onchange={addCert}>
          <option value="">Add certificate…</option>
          {#each certificates.filter((c) => !certs.includes(c.id)) as c (c.id)}
            <option value={c.id}>{c.name} ({c.domains.slice(0, 2).join(', ')}{c.domains.length > 2 ? '…' : ''}) · {c.status}</option>
          {/each}
        </select>
        <a class="small" href="/certificates">Manage certificates</a>
      </div>
      {#if errs.certificates}<span class="ferr">{errs.certificates}</span>{/if}
    </div>
  {/if}

  <div class="summary">
    <div class="sumgrid small">
      <span class="muted">Health check</span><span class="mono xs">{hcSummary(s)}</span>
      {#if s.protocol !== 'tcp'}
        <span class="muted">HTTP timeout</span><span>{s.http?.timeout_s ?? 60} s</span>
        <span class="muted">Sticky sessions</span><span>{s.http?.sticky_sessions ? `${s.http.cookie_name} · ${s.http.cookie_lifetime_s} s` : 'Off'}</span>
      {/if}
      <span class="muted">Proxy protocol</span><span>{s.proxy_protocol === 'none' || !s.proxy_protocol ? 'Off' : s.proxy_protocol}</span>
    </div>
    <button type="button" class="btn sm" onclick={() => (advOpen = true)}><Icon name="edit" size={13} />Advanced settings</button>
  </div>
  {#if touched && (errs.hc_interval || errs.hc_timeout || errs.hc_retries || errs.hc_path || errs.hc_port)}
    <p class="ferr small err-text">Health check settings need attention — open Advanced settings.</p>
  {/if}

  <div class="row">
    <span class="spacer"></span>
    <button type="button" class="btn" onclick={oncancel}>Cancel</button>
    <button type="submit" class="btn primary" disabled={busy}>{busy ? 'Saving…' : submitLabel}</button>
  </div>
</form>

<Modal bind:open={advOpen} title="Advanced settings" width={620}>
  {#if s.health_check}
    {@const hc = s.health_check}
    <div class="stack-lg">
      <fieldset class="stack">
        <legend>Health check</legend>
        <div class="form-grid">
          <div class="field">
            <label for="hcp-{uid}">Protocol</label>
            <select id="hcp-{uid}" class="select" bind:value={hc.protocol} onchange={() => (s = normalise(s))}>
              <option value="tcp">TCP</option><option value="http">HTTP</option><option value="https">HTTPS</option>
            </select>
          </div>
          <div class="field">
            <label for="hcport-{uid}">Port</label>
            <input id="hcport-{uid}" class="input mono" type="number" bind:value={hc.port} aria-invalid={local.hc_port ? 'true' : undefined} />
            {#if local.hc_port}<span class="ferr">{local.hc_port}</span>{/if}
          </div>
          <div class="field">
            <label for="hci-{uid}">Interval (s)</label>
            <input id="hci-{uid}" class="input mono" type="number" bind:value={hc.interval_s} aria-invalid={local.hc_interval ? 'true' : undefined} />
            {#if local.hc_interval}<span class="ferr">{local.hc_interval}</span>{/if}
          </div>
          <div class="field">
            <label for="hct-{uid}">Timeout (s)</label>
            <input id="hct-{uid}" class="input mono" type="number" bind:value={hc.timeout_s} aria-invalid={local.hc_timeout ? 'true' : undefined} />
            {#if local.hc_timeout}<span class="ferr">{local.hc_timeout}</span>{/if}
          </div>
          <div class="field">
            <label for="hcr-{uid}">Retries</label>
            <input id="hcr-{uid}" class="input mono" type="number" bind:value={hc.retries} aria-invalid={local.hc_retries ? 'true' : undefined} />
            {#if local.hc_retries}<span class="ferr">{local.hc_retries}</span>{/if}
          </div>
        </div>
        {#if hc.protocol !== 'tcp' && hc.http}
          <div class="form-grid">
            <div class="field">
              <label for="hcdom-{uid}">Domain</label>
              <input id="hcdom-{uid}" class="input mono" placeholder="optional Host header" value={hc.http.domain ?? ''} oninput={(e) => (hc.http!.domain = (e.currentTarget as HTMLInputElement).value || null)} />
            </div>
            <div class="field">
              <label for="hcpath-{uid}">Path</label>
              <input id="hcpath-{uid}" class="input mono" bind:value={hc.http.path} aria-invalid={local.hc_path ? 'true' : undefined} />
              {#if local.hc_path}<span class="ferr">{local.hc_path}</span>{/if}
            </div>
            <div class="field">
              <label for="hcresp-{uid}">Response contains</label>
              <input id="hcresp-{uid}" class="input mono" placeholder="optional" value={hc.http.response ?? ''} oninput={(e) => (hc.http!.response = (e.currentTarget as HTMLInputElement).value || null)} />
            </div>
            <div class="field">
              <label for="hcsc-{uid}">Status codes</label>
              <input id="hcsc-{uid}" class="input mono" bind:value={codesText.v} placeholder="2??, 3??" />
            </div>
          </div>
          {#if hc.protocol === 'https'}<label class="check"><input type="checkbox" bind:checked={hc.http.tls_verify} />Verify TLS certificate of targets</label>{/if}
        {/if}
      </fieldset>

      {#if s.protocol !== 'tcp' && s.http}
        <fieldset class="stack">
          <legend>HTTP</legend>
          <div class="form-grid">
            <div class="field">
              <label for="ht-{uid}">HTTP timeout (s)</label>
              <input id="ht-{uid}" class="input mono" type="number" bind:value={s.http.timeout_s} />
            </div>
          </div>
          <label class="check"><input type="checkbox" bind:checked={s.http.sticky_sessions} />Sticky sessions</label>
          {#if s.http.sticky_sessions}
            <div class="form-grid">
              <div class="field"><label for="ck-{uid}">Cookie name</label><input id="ck-{uid}" class="input mono" bind:value={s.http.cookie_name} /></div>
              <div class="field"><label for="cl-{uid}">Cookie lifetime (s)</label><input id="cl-{uid}" class="input mono" type="number" bind:value={s.http.cookie_lifetime_s} /></div>
            </div>
          {/if}
        </fieldset>
      {/if}

      <fieldset class="stack">
        <legend>Proxy protocol</legend>
        <div class="field" style:max-width="220px">
          <label for="pp-{uid}">Version</label>
          <select id="pp-{uid}" class="select" bind:value={s.proxy_protocol}>
            <option value="none">Off</option><option value="v1">v1 (text)</option><option value="v2">v2 (binary)</option>
          </select>
        </div>
        {#if s.proxy_protocol && s.proxy_protocol !== 'none'}
          <div class="banner err small" role="alert"><Icon name="alert" size={14} /><span>Every target must accept the PROXY header. Targets that don't will reject all connections and the whole service becomes unreachable.</span></div>
        {/if}
      </fieldset>
    </div>
  {/if}
  {#snippet footer()}<button class="btn primary" onclick={() => (advOpen = false)}>Done</button>{/snippet}
</Modal>

<style>
  .editor { display: flex; flex-direction: column; gap: 14px; }
  .summary { display: flex; align-items: flex-start; gap: 12px; padding: 10px 12px; background: var(--surface-2); border-radius: var(--radius-sm); flex-wrap: wrap; }
  .sumgrid { flex: 1; display: grid; grid-template-columns: max-content 1fr; gap: 4px 14px; min-width: 240px; }
  .sumgrid .mono { overflow-wrap: anywhere; }
  .select.sm { height: 26px; width: auto; font-size: var(--fs-sm); }
  .x { border: 0; background: none; padding: 0; cursor: pointer; color: var(--muted); display: inline-flex; }
  .x:hover { color: var(--text); }
  legend { font-weight: 600; margin-bottom: 8px; padding: 0; }
</style>
