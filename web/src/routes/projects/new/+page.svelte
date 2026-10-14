<script lang="ts">
  import { tick } from 'svelte';
  import { goto } from '$app/navigation';
  import { api, unwrap, mutate, type TargetInput, type ProjectCreate } from '$lib/api/client';
  import { resource } from '$lib/stores/resource.svelte';
  import { toast } from '$lib/stores/toast.svelte';
  import { bump } from '$lib/stores/live.svelte';
  import { newService, normalise, validateService, serviceFieldErrors, type Protocol } from '$lib/util/services';
  import { fqdnError } from '$lib/util/validate';
  import { algorithmLabel } from '$lib/util/format';
  import Icon from '$lib/components/Icon.svelte';
  import TargetPicker from '$lib/components/TargetPicker.svelte';
  import CertificatePicker from '$lib/components/CertificatePicker.svelte';

  const certs = resource(async () => (await unwrap(api.GET('/certificates'))).certificates, { keys: () => ['certificates'] });
  // The address people will use: the node's public IP, for the preview only.
  const nodes = resource(async () => (await unwrap(api.GET('/nodes'))).nodes, { keys: () => ['nodes'] });
  const publicIp = $derived.by(() => {
    const ips = nodes.data?.[0]?.ips ?? [];
    return (ips.find((i) => i.family === 4 && i.label === 'public') ?? ips.find((i) => i.family === 4) ?? ips[0])?.address;
  });

  type Alg = NonNullable<ProjectCreate['algorithm']>;
  const ALGS: { id: Alg; text: string }[] = [
    { id: 'roundrobin', text: 'Take turns, respecting weights. Right for most services.' },
    { id: 'leastconn', text: 'Send each new connection to the least busy server. Good for long connections.' },
    { id: 'source', text: 'The same visitor always reaches the same server.' },
    { id: 'random', text: 'Pick a server at random, respecting weights.' }
  ];

  let name = $state('');
  let description = $state('');
  let targetMode = $state<'host' | 'label_selector' | 'ip'>('ip');
  let targets = $state<TargetInput[]>([]);
  let targetErr = $state('');
  let svc = $state(newService('http', 80));
  let algorithm = $state<Alg>('roundrobin');
  let domain = $state('');
  let domainHttps = $state(true);
  let tried = $state(false);
  let serverErrs = $state<Record<string, string>>({});
  let busy = $state(false);
  let advanced = $state(false);
  let form: HTMLFormElement | undefined = $state();

  const nameErr = $derived(!name.trim() ? 'Give the project a name' : !/^[A-Za-z0-9][A-Za-z0-9._-]{0,62}$/.test(name.trim()) ? 'Use letters, digits, dots, dashes and underscores (up to 63)' : '');
  const svcLocal = $derived.by(() => {
    const e = validateService(svc);
    if (svc.protocol === 'https' && !svc.certificates?.length) e.certificates = 'An HTTPS port needs a certificate';
    return e;
  });
  const svcServer = $derived(serviceFieldErrors(serverErrs, 'service.'));
  const fqdn = $derived(domain.trim().toLowerCase());
  const useDomain = $derived(svc.protocol !== 'tcp' && !!fqdn);
  const domainErr = $derived(useDomain ? fqdnError(fqdn) : '');
  const targetServerErr = $derived(Object.entries(serverErrs).find(([k]) => k.startsWith('targets'))?.[1] ?? '');

  const err = $derived({
    name: (tried ? nameErr : '') || serverErrs.name || '',
    description: serverErrs.description || '',
    listen_port: (tried ? svcLocal.listen_port : '') || svcServer.listen_port || '',
    destination_port: (tried ? svcLocal.destination_port : '') || svcServer.destination_port || '',
    certificates: (tried ? svcLocal.certificates : '') || svcServer.certificates || '',
    domain: (tried ? domainErr : '') || '',
    algorithm: serverErrs.algorithm || ''
  });
  const svcOther = $derived(Object.entries(svcServer).filter(([k]) => !['listen_port', 'destination_port', 'certificates'].includes(k)).map(([, v]) => v));
  const generalErr = $derived(Object.entries(serverErrs).filter(([k]) => !k.startsWith('service') && !k.startsWith('targets') && !['name', 'description', 'algorithm'].includes(k)).map(([, v]) => v));

  // Advanced errors must not hide inside a closed disclosure.
  $effect(() => { if (err.description || err.algorithm || err.certificates) advanced = true; });

  function setProtocol(p: Protocol) {
    svc.protocol = p;
    if (svc.health_check) svc.health_check.protocol = p === 'tcp' ? 'tcp' : 'http';
    svc = normalise(svc);
  }
  function clear(k: string) {
    if (!Object.keys(serverErrs).some((x) => x === k || x.endsWith(k))) return;
    serverErrs = Object.fromEntries(Object.entries(serverErrs).filter(([x]) => !(x === k || x.endsWith(k))));
  }

  async function focusInvalid() {
    await tick();
    form?.querySelector<HTMLElement>('[aria-invalid="true"]')?.focus();
  }

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    tried = true;
    if (nameErr || targetErr || domainErr || Object.keys(svcLocal).length) { await focusInvalid(); return; }
    busy = true;
    serverErrs = {};
    const body: ProjectCreate = {
      name: name.trim(),
      ...(description.trim() ? { description: description.trim() } : {}),
      algorithm,
      ...(targets.length ? { targets } : {}),
      service: normalise(svc)
    };
    const res = await mutate(api.POST('/projects', { body }), { onFieldErrors: (f) => { serverErrs = f; void focusInvalid(); } });
    if (!res) { busy = false; return; }
    const p = res.project;
    bump('projects');
    toast.success(`${p.name} is live`);
    if (useDomain) {
      // A failed domain still leaves a working project; the Domains tab shows what to fix.
      await mutate(api.POST('/domains', { body: { fqdn, project_id: p.id, https: domainHttps } }));
      busy = false;
      void goto(`/projects/${p.id}/domains`);
      return;
    }
    busy = false;
    void goto(`/projects/${p.id}/overview`);
  }

  // Live preview of what the project will look like.
  const scheme = $derived(svc.protocol === 'tcp' ? 'tcp' : svc.protocol);
  const portUrl = $derived(`${scheme}://${publicIp ?? 'server-ip'}:${svc.listen_port || 'auto'}`);
  const targetLines = $derived(targets.slice(0, 4).map((t) => t.type === 'ip' ? `${t.ip?.includes(':') ? `[${t.ip}]` : t.ip}:${t.port ?? (svc.destination_port || '?')}` : t.type === 'host' ? `Host #${t.host_id}` : `${t.label_selector}`));
</script>

<svelte:head><title>Create project · Loxy</title></svelte:head>

<nav class="crumbs small" aria-label="Breadcrumb"><a href="/projects">Projects</a><Icon name="chevronRight" size={12} /><span aria-current="page">New</span></nav>
<div class="page-head">
  <h1>Create project</h1>
  <p class="lede">Three short steps. It gets its own port the moment you create it; a domain is optional.</p>
</div>

<div class="layout">
  <form class="wizard" bind:this={form} onsubmit={submit} novalidate>
    <section aria-labelledby="s-name">
      <h2 id="s-name"><span class="n" aria-hidden="true">1</span>Name it</h2>
      <div class="field nm">
        <label for="pname">Name</label>
        <input id="pname" class="input" bind:value={name} oninput={() => clear('name')} placeholder="shop" autocomplete="off" spellcheck="false" aria-invalid={err.name ? 'true' : undefined} aria-describedby="pname-h" />
        {#if err.name}<span class="ferr" id="pname-h">{err.name}</span>{:else}<span class="hint" id="pname-h">Only you see this, in the panel.</span>{/if}
      </div>
    </section>

    <section aria-labelledby="s-tgt">
      <h2 id="s-tgt"><span class="n" aria-hidden="true">2</span>Where should traffic go?</h2>
      <p class="intro">Targets are the servers that receive the traffic. You can add or change them later.</p>
      <div class="kind" role="radiogroup" aria-label="Kind of traffic">
        <button type="button" role="radio" aria-checked={svc.protocol !== 'tcp'} class:on={svc.protocol !== 'tcp'} onclick={() => setProtocol('http')}>
          <b>HTTP</b><span>Websites and APIs</span>
        </button>
        <button type="button" role="radio" aria-checked={svc.protocol === 'tcp'} class:on={svc.protocol === 'tcp'} onclick={() => setProtocol('tcp')}>
          <b>TCP</b><span>Databases, mail, anything else</span>
        </button>
      </div>
      <TargetPicker bind:mode={targetMode} bind:targets bind:error={targetErr} simple />
      {#if targetServerErr}<p class="ferr small" role="alert">{targetServerErr}</p>{/if}
      <div class="field port">
        <label for="sdest">Server port</label>
        <input id="sdest" class="input mono" type="number" min="1" max="65535" inputmode="numeric" bind:value={svc.destination_port}
          oninput={() => clear('destination_port')} aria-invalid={err.destination_port ? 'true' : undefined} aria-describedby="sdest-h" />
        {#if err.destination_port}<span class="ferr" id="sdest-h">{err.destination_port}</span>
        {:else}<span class="hint" id="sdest-h">The port your servers answer on.</span>{/if}
      </div>
    </section>

    <section aria-labelledby="s-reach">
      <h2 id="s-reach"><span class="n" aria-hidden="true">3</span>How people reach it</h2>
      <div class="form-grid reach">
        <div class="field">
          <label for="slisten">Public port</label>
          <input id="slisten" class="input mono" type="number" min="1" max="65535" inputmode="numeric" placeholder="Auto" bind:value={svc.listen_port}
            oninput={() => clear('listen_port')} aria-invalid={err.listen_port ? 'true' : undefined} aria-describedby="slisten-h" />
          {#if err.listen_port}<span class="ferr" id="slisten-h">{err.listen_port}</span>
          {:else}<span class="hint" id="slisten-h">Leave on Auto: Loxy picks the next free port from 20000.</span>{/if}
        </div>
        {#if svc.protocol !== 'tcp'}
          <div class="field">
            <label for="pdom">Domain <span class="muted">(optional)</span></label>
            <input id="pdom" class="input mono" bind:value={domain} placeholder="shop.example.com" autocomplete="off" spellcheck="false" autocapitalize="off" aria-invalid={err.domain ? 'true' : undefined} aria-describedby="pdom-h" />
            {#if err.domain}<span class="ferr" id="pdom-h">{err.domain}</span>{:else}<span class="hint" id="pdom-h">Served on ports 80 and 443. You'll get the DNS record to create next.</span>{/if}
          </div>
        {/if}
      </div>
      {#if useDomain}
        <label class="check"><input type="checkbox" bind:checked={domainHttps} />HTTPS with an automatic certificate</label>
      {/if}
      {#each svcOther as m (m)}<p class="ferr small">{m}</p>{/each}
    </section>

    <details class="adv" bind:open={advanced}>
      <summary><Icon name="chevronRight" size={14} />More options <span class="muted">description, algorithm, HTTPS without a domain</span></summary>
      <div class="adv-body">
        <div class="field">
          <label for="pdesc">Description <span class="muted">(optional)</span></label>
          <input id="pdesc" class="input" bind:value={description} autocomplete="off" aria-invalid={err.description ? 'true' : undefined} />
          {#if err.description}<span class="ferr">{err.description}</span>{/if}
        </div>

        <fieldset>
          <legend class="lg">How to share traffic between servers</legend>
          <div class="algs">
            {#each ALGS as a (a.id)}
              <label class="pick" class:on={algorithm === a.id}>
                <input type="radio" name="alg" value={a.id} bind:group={algorithm} class="sr-only" />
                <span class="t">{algorithmLabel(a.id)}</span><span class="muted small">{a.text}</span>
              </label>
            {/each}
          </div>
          {#if err.algorithm}<p class="ferr small">{err.algorithm}</p>{/if}
        </fieldset>

        {#if svc.protocol !== 'tcp'}
          <label class="check"><input type="checkbox" checked={svc.protocol === 'https'} onchange={(e) => setProtocol((e.currentTarget as HTMLInputElement).checked ? 'https' : 'http')} />Use HTTPS on this port instead of HTTP (needs a certificate)</label>
          {#if svc.protocol === 'https'}
            <CertificatePicker id="scert" bind:selected={svc.certificates} certificates={certs.data ?? []} error={err.certificates} onchange={() => clear('certificates')} oncreated={() => void certs.reload()} />
            <p class="small muted">Using a domain? You don't need this: HTTPS on 443 is set up for the domain automatically.</p>
          {/if}
        {/if}
        <p class="xs muted">Health checks, timeouts, sticky sessions and PROXY protocol are on the Services tab after creating.</p>
      </div>
    </details>

    {#each generalErr as m (m)}<p class="ferr small" role="alert">{m}</p>{/each}

    <div class="submit">
      <a class="btn ghost" href="/projects">Cancel</a>
      <button class="btn primary" type="submit" disabled={busy}>{busy ? 'Creating…' : 'Create project'}</button>
    </div>
  </form>

  <aside class="preview" aria-label="Preview">
    <h2>What you'll get</h2>
    <div class="pv">
      <div class="pv-block">
        <span class="pv-label">People connect to</span>
        <span class="pv-url">{portUrl}</span>
        {#if useDomain && !domainErr}<span class="pv-url">{domainHttps ? 'https' : 'http'}://{fqdn}</span>{/if}
      </div>
      <div class="pv-wire" aria-hidden="true"><span></span><em>{name.trim() || 'your project'} · {algorithmLabel(algorithm)}</em><span></span></div>
      <div class="pv-block">
        <span class="pv-label">Traffic goes to</span>
        {#if targetLines.length}
          {#each targetLines as l (l)}<span class="pv-url">{l}</span>{/each}
          {#if targets.length > 4}<span class="muted xs">and {targets.length - 4} more</span>{/if}
        {:else}
          <span class="muted small">No targets yet. You can add them later.</span>
        {/if}
      </div>
    </div>
  </aside>
</div>

<style>
  .layout { display: grid; grid-template-columns: minmax(0, 1fr) 320px; gap: 40px; align-items: start; max-width: 1080px; }
  .wizard { display: flex; flex-direction: column; min-width: 0; }
  section { display: flex; flex-direction: column; gap: 14px; padding: 26px 0; border-top: 1px solid var(--border); }
  section:first-child { border-top: 0; padding-top: 0; }
  h2 { font-size: var(--fs-xl); font-weight: 600; letter-spacing: -0.01em; display: flex; align-items: center; gap: 12px; }
  .n { display: inline-grid; place-items: center; width: 26px; height: 26px; border-radius: 50%; background: var(--accent-soft); color: var(--accent); font-size: var(--fs-sm); font-weight: 650; font-variant-numeric: tabular-nums; flex: none; }
  .intro { color: var(--text-2); margin-top: -6px; }
  .nm, .port { max-width: 320px; }
  .reach { grid-template-columns: repeat(auto-fit, minmax(min(100%, 220px), 1fr)); }

  .kind { display: grid; grid-template-columns: 1fr 1fr; gap: 10px; max-width: 520px; }
  .kind button, .pick {
    display: flex; flex-direction: column; align-items: flex-start; gap: 2px; padding: 12px 14px; border-radius: var(--radius);
    border: 1px solid var(--border-strong); background: var(--surface); cursor: pointer; text-align: left;
    transition: border-color 120ms ease, box-shadow 120ms ease, transform 120ms var(--ease-out);
  }
  .kind button:active, .pick:active { transform: scale(0.98); }
  .kind b { font-weight: 650; }
  .kind span { font-size: var(--fs-sm); color: var(--muted); }
  .kind button.on, .pick.on { border-color: var(--accent); box-shadow: inset 0 0 0 1px var(--accent); background: var(--accent-soft); }
  @media (hover: hover) and (pointer: fine) {
    .kind button:hover:not(.on), .pick:hover:not(.on) { border-color: color-mix(in srgb, var(--text) 30%, var(--border-strong)); }
  }

  .adv { border-top: 1px solid var(--border); padding: 18px 0; }
  .adv summary { display: flex; align-items: center; gap: 8px; cursor: pointer; font-weight: 600; list-style: none; flex-wrap: wrap; min-height: 32px; }
  .adv summary::-webkit-details-marker { display: none; }
  .adv summary :global(.icon) { transition: transform 160ms var(--ease-out); color: var(--muted); }
  .adv[open] summary :global(.icon) { transform: rotate(90deg); }
  .adv summary .muted { font-weight: 400; font-size: var(--fs-sm); }
  .adv-body { display: flex; flex-direction: column; gap: 16px; padding-top: 16px; }
  .lg { font-size: var(--fs-sm); font-weight: 550; margin-bottom: 8px; padding: 0; }
  .algs { display: grid; grid-template-columns: repeat(auto-fill, minmax(min(100%, 220px), 1fr)); gap: 10px; }
  .pick:has(:focus-visible) { outline: 2px solid var(--focus); outline-offset: 2px; }
  .t { font-weight: 600; }

  .submit {
    position: sticky; bottom: 0; display: flex; justify-content: flex-end; align-items: center; gap: 8px; padding: 14px 0;
    border-top: 1px solid var(--border); background: var(--bg); z-index: 5;
  }

  .preview { position: sticky; top: calc(var(--topbar-h) + 24px); display: flex; flex-direction: column; gap: 12px; }
  .preview h2 { font-size: var(--fs-sm); color: var(--muted); font-weight: 550; }
  .pv { background: var(--surface); border: 1px solid var(--border); border-radius: var(--radius-lg); padding: 18px; display: flex; flex-direction: column; }
  .pv-block { display: flex; flex-direction: column; gap: 4px; min-width: 0; }
  .pv-label { font-size: var(--fs-xs); color: var(--muted); }
  .pv-url { font-family: var(--mono); font-size: var(--fs-sm); overflow-wrap: anywhere; }
  .pv-wire { display: flex; flex-direction: column; align-items: flex-start; gap: 4px; margin: 10px 0 10px 10px; padding-left: 0; }
  .pv-wire span { width: 2px; height: 14px; background: var(--accent-line); border-radius: 2px; }
  .pv-wire em { font-style: normal; font-size: var(--fs-xs); color: var(--accent); background: var(--accent-soft); padding: 3px 9px; border-radius: 999px; margin-left: -10px; font-weight: 550; max-width: 100%; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }

  @media (max-width: 1099px) {
    .layout { grid-template-columns: minmax(0, 1fr); gap: 0; }
    .preview { position: static; order: -1; margin-bottom: 24px; }
  }
  @media (max-width: 699px) {
    .preview { display: none; }
    .kind { grid-template-columns: 1fr 1fr; }
    .nm, .port { max-width: none; }
    .submit { bottom: calc(var(--bottombar-h) + env(safe-area-inset-bottom)); }
    .submit .btn { flex: 1; }
  }
</style>
