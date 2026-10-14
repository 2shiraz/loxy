<script lang="ts">
  import { tick } from 'svelte';
  import { goto } from '$app/navigation';
  import { api, unwrap, mutate, type TargetInput, type ProjectCreate } from '$lib/api/client';
  import { resource } from '$lib/stores/resource.svelte';
  import { toast } from '$lib/stores/toast.svelte';
  import { bump } from '$lib/stores/live.svelte';
  import { newService, normalise, validateService, serviceFieldErrors, serviceTitle, type Protocol } from '$lib/util/services';
  import { fqdnError } from '$lib/util/validate';
  import { algorithmLabel } from '$lib/util/format';
  import Icon from '$lib/components/Icon.svelte';
  import TargetPicker from '$lib/components/TargetPicker.svelte';
  import CertificatePicker from '$lib/components/CertificatePicker.svelte';

  const certs = resource(async () => (await unwrap(api.GET('/certificates'))).certificates, { keys: () => ['certificates'] });

  type Alg = NonNullable<ProjectCreate['algorithm']>;
  const ALGS: { id: Alg; text: string }[] = [
    { id: 'roundrobin', text: 'Spread requests evenly, respecting weights.' },
    { id: 'leastconn', text: 'Send new connections to the target with the fewest open ones.' },
    { id: 'source', text: 'The same client IP always reaches the same target.' },
    { id: 'random', text: 'Pick a random target, respecting weights.' }
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
  let form: HTMLFormElement | undefined = $state();

  const nameErr = $derived(!name.trim() ? 'Required' : !/^[A-Za-z0-9][A-Za-z0-9._-]{0,62}$/.test(name.trim()) ? 'Letters, digits, dots, dashes and underscores; up to 63 characters' : '');
  const svcLocal = $derived.by(() => {
    const e = validateService(svc);
    if (svc.protocol === 'https' && !svc.certificates?.length) e.certificates = 'An HTTPS port needs a certificate';
    return e;
  });
  const svcServer = $derived(serviceFieldErrors(serverErrs, 'service.'));
  const domainErr = $derived(domain.trim() ? fqdnError(domain.trim().toLowerCase()) : '');
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
    toast.success(`Project ${p.name} created`);
    const fqdn = domain.trim().toLowerCase();
    if (fqdn) {
      // A failed domain still leaves a working project; the Domains tab shows what to fix.
      await mutate(api.POST('/domains', { body: { fqdn, project_id: p.id, https: domainHttps } }));
      busy = false;
      void goto(`/projects/${p.id}/domains`);
      return;
    }
    busy = false;
    void goto(`/projects/${p.id}/overview`);
  }
</script>

<svelte:head><title>Create project · Loxy</title></svelte:head>

<nav class="crumbs small" aria-label="Breadcrumb"><a href="/projects">Projects</a><Icon name="chevronRight" size={12} /><span aria-current="page">New</span></nav>
<div class="page-head"><h1>Create project</h1></div>

<form class="wizard" bind:this={form} onsubmit={submit} novalidate>
  <section aria-labelledby="s-name">
    <h2 id="s-name"><span class="n">1</span>Name</h2>
    <div class="form-grid two">
      <div class="field">
        <label for="pname">Name</label>
        <input id="pname" class="input" bind:value={name} oninput={() => clear('name')} placeholder="shop" autocomplete="off" spellcheck="false" aria-invalid={err.name ? 'true' : undefined} aria-describedby="pname-h" />
        {#if err.name}<span class="ferr" id="pname-h">{err.name}</span>{:else}<span class="hint" id="pname-h">Shown in the panel only.</span>{/if}
      </div>
      <div class="field">
        <label for="pdesc">Description <span class="muted">(optional)</span></label>
        <input id="pdesc" class="input" bind:value={description} autocomplete="off" aria-invalid={err.description ? 'true' : undefined} />
        {#if err.description}<span class="ferr">{err.description}</span>{/if}
      </div>
    </div>
  </section>

  <section aria-labelledby="s-tgt">
    <h2 id="s-tgt"><span class="n">2</span>Targets <span class="opt">optional — add them later on the Targets tab</span></h2>
    <p class="muted small">The servers that receive the traffic.</p>
    <TargetPicker bind:mode={targetMode} bind:targets bind:error={targetErr} />
    {#if targetServerErr}<p class="ferr small" role="alert">{targetServerErr}</p>{/if}
  </section>

  <section aria-labelledby="s-svc">
    <h2 id="s-svc"><span class="n">3</span>Service</h2>
    <p class="muted small">The port clients connect to and the port on your targets. Without a domain, every project gets its own port.</p>
    <div class="form-grid">
      <div class="field">
        <label for="sproto">Protocol</label>
        <select id="sproto" class="select" value={svc.protocol} onchange={(e) => setProtocol((e.currentTarget as HTMLSelectElement).value as Protocol)}>
          <option value="http">HTTP</option><option value="https">HTTPS</option><option value="tcp">TCP</option>
        </select>
      </div>
      <div class="field">
        <label for="slisten">Listen port</label>
        <input id="slisten" class="input mono" type="number" min="1" max="65535" inputmode="numeric" placeholder="Auto" bind:value={svc.listen_port}
          oninput={() => clear('listen_port')} aria-invalid={err.listen_port ? 'true' : undefined} aria-describedby="slisten-h" />
        {#if err.listen_port}<span class="ferr" id="slisten-h">{err.listen_port}</span>
        {:else}<span class="hint" id="slisten-h">Empty = next free port from 20000.</span>{/if}
      </div>
      <div class="field">
        <label for="sdest">Destination port</label>
        <input id="sdest" class="input mono" type="number" min="1" max="65535" inputmode="numeric" bind:value={svc.destination_port}
          oninput={() => clear('destination_port')} aria-invalid={err.destination_port ? 'true' : undefined} aria-describedby="sdest-h" />
        {#if err.destination_port}<span class="ferr" id="sdest-h">{err.destination_port}</span>
        {:else}<span class="hint" id="sdest-h">Port your targets answer on.</span>{/if}
      </div>
    </div>
    {#if svc.protocol === 'https'}
      <CertificatePicker id="scert" bind:selected={svc.certificates} certificates={certs.data ?? []} error={err.certificates} onchange={() => clear('certificates')} oncreated={() => void certs.reload()} />
      <p class="small muted note"><Icon name="info" size={13} />Only needed for HTTPS on a custom port. For a domain, keep HTTP here: step 5 sets up HTTPS on 443 automatically.</p>
    {/if}
    {#each svcOther as m (m)}<p class="ferr small">{m}</p>{/each}
    <p class="muted xs">Health checks, timeouts, sticky sessions and PROXY protocol can be changed on the Services tab.</p>
  </section>

  <section aria-labelledby="s-alg">
    <h2 id="s-alg"><span class="n">4</span>Algorithm</h2>
    <div class="cards" role="radiogroup" aria-labelledby="s-alg">
      {#each ALGS as a (a.id)}
        <label class="pick" class:on={algorithm === a.id}>
          <input type="radio" name="alg" value={a.id} bind:group={algorithm} class="sr-only" />
          <span class="t">{algorithmLabel(a.id)}</span><span class="muted small">{a.text}</span>
        </label>
      {/each}
    </div>
    {#if err.algorithm}<p class="ferr small">{err.algorithm}</p>{/if}
  </section>

  <section aria-labelledby="s-dom">
    <h2 id="s-dom"><span class="n">5</span>Domain <span class="opt">optional</span></h2>
    <p class="muted small">Route a hostname on the shared ports 80 and 443 to this project. You'll see the DNS records to create next.</p>
    <div class="form-grid two">
      <div class="field">
        <label for="pdom">Domain name</label>
        <input id="pdom" class="input mono" bind:value={domain} placeholder="shop.example.com" autocomplete="off" spellcheck="false" autocapitalize="off" aria-invalid={err.domain ? 'true' : undefined} aria-describedby="pdom-h" />
        {#if err.domain}<span class="ferr" id="pdom-h">{err.domain}</span>{:else}<span class="hint" id="pdom-h">Leave empty to use the project's own port only.</span>{/if}
      </div>
      <label class="check dh"><input type="checkbox" bind:checked={domainHttps} disabled={!domain.trim()} />HTTPS with an automatic certificate</label>
    </div>
  </section>

  {#each generalErr as m (m)}<p class="ferr small" role="alert">{m}</p>{/each}

  <div class="submit">
    <div class="summary small muted">
      <span class="mono">{serviceTitle({ ...svc, listen_port: svc.listen_port || undefined })}</span>
      <span>· {targets.length} target{targets.length === 1 ? '' : 's'}</span>
      <span>· {algorithmLabel(algorithm)}</span>
      {#if domain.trim() && !domainErr}<span class="mono">· {domain.trim().toLowerCase()}</span>{/if}
    </div>
    <a class="btn" href="/projects">Cancel</a>
    <button class="btn primary" type="submit" disabled={busy}>{busy ? 'Creating…' : 'Create project'}</button>
  </div>
</form>

<style>
  .wizard { display: flex; flex-direction: column; max-width: 900px; }
  section { display: flex; flex-direction: column; gap: 12px; padding: 22px 0; border-top: 1px solid var(--border); }
  section:first-child { border-top: 0; padding-top: 0; }
  h2 { font-size: var(--fs-lg); display: flex; align-items: baseline; gap: 10px; flex-wrap: wrap; }
  .n { display: inline-grid; place-items: center; width: 22px; height: 22px; border-radius: 50%; background: var(--surface-2); color: var(--muted); font-size: var(--fs-xs); font-weight: 600; font-variant-numeric: tabular-nums; transform: translateY(-1px); }
  .opt { font-weight: 400; color: var(--muted); font-size: var(--fs-sm); }
  .two { grid-template-columns: repeat(auto-fit, minmax(240px, 1fr)); }
  .cards { display: grid; grid-template-columns: repeat(auto-fill, minmax(200px, 1fr)); gap: 10px; }
  .pick {
    display: flex; flex-direction: column; gap: 4px; padding: 12px 14px; border-radius: var(--radius);
    border: 1px solid var(--border); background: var(--surface); cursor: pointer; transition: border-color 120ms ease;
  }
  .pick:hover { border-color: var(--border-strong); }
  .pick.on { border-color: var(--text-2); box-shadow: inset 0 0 0 1px var(--text-2); }
  .pick:has(:focus-visible) { outline: 2px solid var(--focus); outline-offset: 2px; }
  .t { font-weight: 600; }
  .note { display: flex; align-items: flex-start; gap: 6px; }
  .note :global(svg) { flex: none; margin-top: 3px; }
  .dh { align-self: start; margin-top: 26px; }
  .submit {
    position: sticky; bottom: 0; display: flex; align-items: center; gap: 8px; padding: 12px 0;
    border-top: 1px solid var(--border); background: var(--bg); flex-wrap: wrap;
  }
  .summary { display: flex; align-items: center; gap: 6px; margin-right: auto; flex-wrap: wrap; min-width: 0; }
</style>
