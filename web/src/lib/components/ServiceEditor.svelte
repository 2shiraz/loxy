<script lang="ts">
  /** Inline service form: protocol, ports, certificates and advanced settings. */
  import { tick } from 'svelte';
  import type { ServiceInput, Certificate } from '$lib/api/client';
  import { validateService, normalise, hcSummary, type Protocol } from '$lib/util/services';
  import Icon from './Icon.svelte';
  import CertificatePicker from './CertificatePicker.svelte';

  let {
    value,
    others,
    certificates,
    serverErrors = {},
    busy = false,
    allowAuto = false,
    submitLabel = 'Save service',
    onsave,
    oncancel,
    oncertcreated
  }: {
    value: ServiceInput;
    others: { listen_port: number }[];
    certificates: Certificate[];
    serverErrors?: Record<string, string>;
    busy?: boolean;
    /** New services may leave the listen port empty to get a free one. */
    allowAuto?: boolean;
    submitLabel?: string;
    onsave: (s: ServiceInput) => void;
    oncancel: () => void;
    oncertcreated?: () => void;
  } = $props();

  // svelte-ignore state_referenced_locally
  let s = $state<ServiceInput>(normalise(value));
  let touched = $state(false);
  let advOpen = $state(false);
  let form: HTMLFormElement | undefined = $state();
  const uid = Math.random().toString(36).slice(2, 7);

  const local = $derived.by(() => {
    const e = validateService(s, others);
    if (!allowAuto && (s.listen_port == null || (s.listen_port as unknown) === '')) e.listen_port = 'Required';
    if (s.protocol === 'https' && !s.certificates?.length) e.certificates = 'An HTTPS port needs a certificate';
    return e;
  });
  const errs = $derived({ ...(touched ? local : {}), ...serverErrors } as Record<string, string>);
  const ADV = ['timeout_s', 'cookie_name', 'cookie_lifetime_s', 'proxy_protocol', 'hc_interval', 'hc_timeout', 'hc_retries', 'hc_port', 'hc_path', 'hc_codes', 'hc_protocol', 'hc_other'];
  const advErr = $derived(ADV.some((k) => errs[k]));
  const otherErrs = $derived(Object.entries(serverErrors).filter(([k]) => !['listen_port', 'destination_port', 'certificates', 'protocol', ...ADV].includes(k)).map(([, v]) => v));

  $effect(() => { if (Object.keys(serverErrors).length) void focusFirstInvalid(); });

  async function focusFirstInvalid() {
    if (advErr) advOpen = true;
    await tick();
    form?.querySelector<HTMLElement>('[aria-invalid="true"]')?.focus();
  }

  function submit(e: SubmitEvent) {
    e.preventDefault();
    touched = true;
    if (Object.keys(local).length) { void focusFirstInvalid(); return; }
    onsave(normalise(s));
  }
  function setProtocol(p: Protocol) {
    s.protocol = p;
    if (s.health_check) s.health_check.protocol = p === 'tcp' ? 'tcp' : 'http';
    s = normalise(s);
  }
  const codesText = {
    get v() { return (s.health_check?.http?.status_codes ?? []).join(', '); },
    set v(t: string) { if (s.health_check?.http) s.health_check.http.status_codes = t.split(',').map((x) => x.trim()).filter(Boolean); }
  };
  const inv = (k: string) => (errs[k] ? 'true' : undefined);
</script>

{#snippet ferr(k: string)}
  {#if errs[k]}<span class="ferr" id="{k}-{uid}-e">{errs[k]}</span>{/if}
{/snippet}

<form class="editor" bind:this={form} onsubmit={submit} novalidate>
  <div class="form-grid">
    <div class="field">
      <label for="p-{uid}">Protocol</label>
      <select id="p-{uid}" class="select" value={s.protocol} onchange={(e) => setProtocol((e.currentTarget as HTMLSelectElement).value as Protocol)}>
        <option value="http">HTTP</option><option value="https">HTTPS</option><option value="tcp">TCP</option>
      </select>
    </div>
    <div class="field">
      <label for="l-{uid}">Listen port</label>
      <input id="l-{uid}" class="input mono" type="number" min="1" max="65535" inputmode="numeric" placeholder={allowAuto ? 'Auto' : ''}
        bind:value={s.listen_port} aria-invalid={inv('listen_port')} aria-describedby="l-{uid}-h" />
      {#if errs.listen_port}<span class="ferr" id="l-{uid}-h">{errs.listen_port}</span>
      {:else}<span class="hint" id="l-{uid}-h">{allowAuto ? 'Empty = next free port from 20000.' : 'Clients connect here.'}</span>{/if}
    </div>
    <div class="field">
      <label for="d-{uid}">Destination port</label>
      <input id="d-{uid}" class="input mono" type="number" min="1" max="65535" inputmode="numeric" bind:value={s.destination_port} aria-invalid={inv('destination_port')} aria-describedby="d-{uid}-h" />
      {#if errs.destination_port}<span class="ferr" id="d-{uid}-h">{errs.destination_port}</span>
      {:else}<span class="hint" id="d-{uid}-h">Port on your targets.</span>{/if}
    </div>
  </div>
  {#if errs.protocol}<span class="ferr small">{errs.protocol}</span>{/if}

  {#if s.protocol === 'https'}
    <CertificatePicker id="c-{uid}" bind:selected={s.certificates} {certificates} error={errs.certificates} oncreated={() => oncertcreated?.()} />
    <p class="small muted note"><Icon name="info" size={13} />For a domain on 443 you don't need this: attach the domain on the Domains tab and HTTPS is set up automatically.</p>
  {/if}

  <details class="adv" bind:open={advOpen}>
    <summary>
      <span class="sl">Advanced settings</span>
      {#if advErr}<span class="badge err">Needs attention</span>{/if}
      <span class="mono xs muted sum">{hcSummary(s)}</span>
    </summary>
    {#if s.health_check}
      {@const hc = s.health_check}
      <div class="stack-lg advbody">
        <fieldset class="stack">
          <legend>Health check</legend>
          <div class="form-grid">
            <div class="field">
              <label for="hcp-{uid}">Check protocol</label>
              <select id="hcp-{uid}" class="select" bind:value={hc.protocol} onchange={() => (s = normalise(s))}>
                <option value="tcp">TCP connect</option><option value="http">HTTP</option><option value="https">HTTPS</option>
              </select>
            </div>
            <div class="field">
              <label for="hcport-{uid}">Check port</label>
              <input id="hcport-{uid}" class="input mono" type="number" placeholder="Target port" bind:value={hc.port} aria-invalid={inv('hc_port')} />
              {@render ferr('hc_port')}
            </div>
            <div class="field">
              <label for="hci-{uid}">Interval (s)</label>
              <input id="hci-{uid}" class="input mono" type="number" bind:value={hc.interval_s} aria-invalid={inv('hc_interval')} />
              {@render ferr('hc_interval')}
            </div>
            <div class="field">
              <label for="hct-{uid}">Check timeout (s)</label>
              <input id="hct-{uid}" class="input mono" type="number" bind:value={hc.timeout_s} aria-invalid={inv('hc_timeout')} />
              {@render ferr('hc_timeout')}
            </div>
            <div class="field">
              <label for="hcr-{uid}">Retries</label>
              <input id="hcr-{uid}" class="input mono" type="number" bind:value={hc.retries} aria-invalid={inv('hc_retries')} />
              {@render ferr('hc_retries')}
            </div>
          </div>
          {#if hc.protocol !== 'tcp' && hc.http}
            <div class="form-grid">
              <div class="field">
                <label for="hcpath-{uid}">Path</label>
                <input id="hcpath-{uid}" class="input mono" bind:value={hc.http.path} aria-invalid={inv('hc_path')} spellcheck="false" />
                {@render ferr('hc_path')}
              </div>
              <div class="field">
                <label for="hcdom-{uid}">Host header</label>
                <input id="hcdom-{uid}" class="input mono" placeholder="optional" spellcheck="false" value={hc.http.domain ?? ''} oninput={(e) => (hc.http!.domain = (e.currentTarget as HTMLInputElement).value || null)} />
              </div>
              <div class="field">
                <label for="hcsc-{uid}">Status codes</label>
                <input id="hcsc-{uid}" class="input mono" bind:value={codesText.v} placeholder="2??, 3??" aria-invalid={inv('hc_codes')} spellcheck="false" />
                {@render ferr('hc_codes')}
              </div>
              <div class="field">
                <label for="hcresp-{uid}">Body contains</label>
                <input id="hcresp-{uid}" class="input mono" placeholder="optional" spellcheck="false" value={hc.http.response ?? ''} oninput={(e) => (hc.http!.response = (e.currentTarget as HTMLInputElement).value || null)} />
              </div>
            </div>
            {#if hc.protocol === 'https'}<label class="check"><input type="checkbox" bind:checked={hc.http.tls_verify} />Verify the targets' TLS certificates</label>{/if}
          {/if}
          {#if errs.hc_protocol || errs.hc_other}<span class="ferr small">{errs.hc_protocol || errs.hc_other}</span>{/if}
        </fieldset>

        <fieldset class="stack">
          <legend>Connection</legend>
          <div class="form-grid">
            <div class="field">
              <label for="to-{uid}">Timeout (s)</label>
              <input id="to-{uid}" class="input mono" type="number" bind:value={s.timeout_s} aria-invalid={inv('timeout_s')} />
              {#if errs.timeout_s}{@render ferr('timeout_s')}{:else}<span class="hint">Idle client and server timeout.</span>{/if}
            </div>
            <div class="field">
              <label for="pp-{uid}">PROXY protocol</label>
              <select id="pp-{uid}" class="select" bind:value={s.proxy_protocol}>
                <option value="none">Off</option><option value="v1">v1 (text)</option><option value="v2">v2 (binary)</option>
              </select>
            </div>
          </div>
          {#if s.proxy_protocol && s.proxy_protocol !== 'none'}
            <div class="banner err small" role="alert"><Icon name="alert" size={14} /><span>Every target must accept the PROXY header. Targets that don't will reject all connections.</span></div>
          {/if}
          {#if s.protocol !== 'tcp'}
            <label class="check"><input type="checkbox" bind:checked={s.sticky_sessions} />Sticky sessions (cookie)</label>
            {#if s.sticky_sessions}
              <div class="form-grid">
                <div class="field">
                  <label for="ck-{uid}">Cookie name</label>
                  <input id="ck-{uid}" class="input mono" bind:value={s.cookie_name} aria-invalid={inv('cookie_name')} spellcheck="false" />
                  {@render ferr('cookie_name')}
                </div>
                <div class="field">
                  <label for="cl-{uid}">Cookie lifetime (s)</label>
                  <input id="cl-{uid}" class="input mono" type="number" bind:value={s.cookie_lifetime_s} aria-invalid={inv('cookie_lifetime_s')} />
                  {@render ferr('cookie_lifetime_s')}
                </div>
              </div>
            {/if}
          {/if}
        </fieldset>
      </div>
    {/if}
  </details>

  {#each otherErrs as m (m)}<p class="ferr small">{m}</p>{/each}

  <div class="row">
    <span class="spacer"></span>
    <button type="button" class="btn" onclick={oncancel}>Cancel</button>
    <button type="submit" class="btn primary" disabled={busy}>{busy ? 'Saving…' : submitLabel}</button>
  </div>
</form>

<style>
  .editor { display: flex; flex-direction: column; gap: 14px; }
  .note { display: flex; align-items: flex-start; gap: 6px; }
  .note :global(svg) { flex: none; margin-top: 3px; }
  .adv { border: 1px solid var(--border); border-radius: var(--radius-sm); background: var(--surface-2); }
  .adv summary { display: flex; align-items: center; gap: 10px; padding: 9px 12px; cursor: pointer; list-style: none; flex-wrap: wrap; }
  .adv summary::-webkit-details-marker { display: none; }
  .adv summary::before { content: '▸'; color: var(--muted); font-size: 11px; transition: transform 120ms var(--ease-out); }
  .adv[open] summary::before { transform: rotate(90deg); }
  .adv summary:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; border-radius: var(--radius-sm); }
  .sl { font-weight: 500; }
  .sum { flex: 1 1 260px; min-width: 0; overflow-wrap: anywhere; }
  .advbody { padding: 4px 12px 14px; }
  legend { font-weight: 600; margin-bottom: 8px; padding: 0; }
  fieldset { border: 0; padding: 0; margin: 0; min-width: 0; }
</style>
