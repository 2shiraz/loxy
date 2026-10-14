<script lang="ts">
  /** Attach a domain to a project. Without `projectId`, the user picks the project. */
  import { tick } from 'svelte';
  import { api, unwrap, mutate, type Domain, type Project, type Service, type DNSProvider } from '$lib/api/client';
  import { fqdnError } from '$lib/util/validate';
  import { serviceTitle } from '$lib/util/services';
  import Icon from './Icon.svelte';

  let {
    projectId,
    projects = [],
    providers = [],
    onadded,
    oncancel
  }: {
    projectId?: number;
    projects?: Project[];
    providers?: DNSProvider[];
    onadded: (d: Domain) => void;
    oncancel?: () => void;
  } = $props();

  const uid = Math.random().toString(36).slice(2, 7);
  let fqdn = $state('');
  // svelte-ignore state_referenced_locally
  let pid = $state<number | ''>(projectId ?? '');
  let sid = $state<number | ''>('');
  let https = $state(true);
  let provId = $state<number | ''>('');
  let auto = $state(false);
  let errs = $state<Record<string, string>>({});
  let busy = $state(false);
  let services = $state<Service[]>([]);
  let form: HTMLFormElement | undefined = $state();

  $effect(() => { if (projectId != null) pid = projectId; });
  $effect(() => { if (pid === '' && projects.length) pid = projects[0].id; });
  $effect(() => { if (provId === '') auto = false; });
  $effect(() => {
    const p = pid;
    services = [];
    sid = '';
    if (p === '') return;
    void unwrap(api.GET('/projects/{id}/services', { params: { path: { id: p } } }))
      .then((r) => { if (pid === p) services = r.services; })
      .catch(() => {});
  });
  const webServices = $derived(services.filter((s) => s.protocol !== 'tcp'));

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    const name = fqdn.trim().toLowerCase().replace(/\.$/, '');
    const next: Record<string, string> = {};
    const fe = fqdnError(name);
    if (fe) next.fqdn = fe;
    if (pid === '') next.project_id = 'Choose a project';
    errs = next;
    if (Object.keys(next).length) { await focusInvalid(); return; }
    busy = true;
    const r = await mutate(api.POST('/domains', {
      body: {
        fqdn: name, project_id: pid as number, https,
        ...(sid !== '' ? { service_id: sid } : {}),
        ...(provId !== '' ? { dns_provider_id: provId, auto_records: auto } : {})
      }
    }), { success: `${name} added. Checking DNS…`, onFieldErrors: (f) => { errs = f; void focusInvalid(); } });
    busy = false;
    if (r) { fqdn = ''; errs = {}; onadded(r.domain); }
  }
  async function focusInvalid() {
    await tick();
    form?.querySelector<HTMLElement>('[aria-invalid="true"]')?.focus();
  }
  const known = ['fqdn', 'project_id', 'service_id', 'dns_provider_id', 'https', 'auto_records'];
  const other = $derived(Object.entries(errs).filter(([k]) => !known.includes(k)).map(([, v]) => v));
</script>

<form class="daf" bind:this={form} onsubmit={submit} novalidate>
  <div class="form-grid">
    {#if projectId == null}
      <div class="field">
        <label for="dp-{uid}">Project</label>
        <select id="dp-{uid}" class="select" bind:value={pid} aria-invalid={errs.project_id ? 'true' : undefined}>
          {#if !projects.length}<option value="">No projects yet</option>{/if}
          {#each projects as p (p.id)}<option value={p.id}>{p.name}</option>{/each}
        </select>
        {#if errs.project_id}<span class="ferr">{errs.project_id}</span>{/if}
      </div>
    {/if}
    <div class="field grow">
      <label for="df-{uid}">Domain name</label>
      <input id="df-{uid}" class="input mono" bind:value={fqdn} placeholder="shop.example.com" autocomplete="off" spellcheck="false" autocapitalize="off"
        aria-invalid={errs.fqdn ? 'true' : undefined} aria-describedby="df-{uid}-h" />
      {#if errs.fqdn}<span class="ferr" id="df-{uid}-h">{errs.fqdn}</span>
      {:else}<span class="hint" id="df-{uid}-h">Traffic for this name on ports 80 and 443 goes to the project.</span>{/if}
    </div>
    {#if webServices.length > 1}
      <div class="field">
        <label for="ds-{uid}">Service</label>
        <select id="ds-{uid}" class="select" bind:value={sid} aria-invalid={errs.service_id ? 'true' : undefined}>
          <option value="">First HTTP service</option>
          {#each webServices as s (s.id)}<option value={s.id}>{serviceTitle(s)}</option>{/each}
        </select>
        {#if errs.service_id}<span class="ferr">{errs.service_id}</span>{/if}
      </div>
    {/if}
    {#if providers.length}
      <div class="field">
        <label for="dv-{uid}">DNS provider <span class="muted">(optional)</span></label>
        <select id="dv-{uid}" class="select" bind:value={provId} aria-invalid={errs.dns_provider_id ? 'true' : undefined}>
          <option value="">Manual — I'll create the records</option>
          {#each providers as p (p.id)}<option value={p.id}>{p.name}</option>{/each}
        </select>
        {#if errs.dns_provider_id}<span class="ferr">{errs.dns_provider_id}</span>{/if}
      </div>
    {/if}
  </div>
  <div class="row-wrap opts">
    <label class="check"><input type="checkbox" bind:checked={https} />HTTPS with an automatic certificate</label>
    {#if providers.length}
      <label class="check" class:muted={provId === ''}><input type="checkbox" bind:checked={auto} disabled={provId === ''} />Create the DNS records for me</label>
    {/if}
  </div>
  {#if services.length && !webServices.length}
    <p class="small warn-text"><Icon name="alert" size={13} /> This project has only TCP services. Domains route HTTP traffic, so add an HTTP service first.</p>
  {/if}
  {#each other as m (m)}<p class="ferr small">{m}</p>{/each}
  <div class="row">
    <span class="spacer"></span>
    {#if oncancel}<button type="button" class="btn" onclick={oncancel}>Cancel</button>{/if}
    <button type="submit" class="btn primary" disabled={busy}><Icon name="plus" size={14} />{busy ? 'Adding…' : 'Add domain'}</button>
  </div>
</form>

<style>
  .daf { display: flex; flex-direction: column; gap: 12px; }
  .grow { min-width: 0; }
  .opts { gap: 8px 20px; }
</style>
