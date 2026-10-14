<script lang="ts">
  import { api, unwrap, mutate, type Service, type ServiceInput } from '$lib/api/client';
  import { getProject } from '$lib/stores/project';
  import { resource } from '$lib/stores/resource.svelte';
  import { confirm } from '$lib/stores/confirm.svelte';
  import { checkText } from '$lib/util/format';
  import { newService, toInput, serviceTitle, hcSummary, serviceFieldErrors } from '$lib/util/services';
  import Icon from '$lib/components/Icon.svelte';
  import Menu from '$lib/components/Menu.svelte';
  import ServiceEditor from '$lib/components/ServiceEditor.svelte';
  import HealthCount from '$lib/components/HealthCount.svelte';
  import Skeleton from '$lib/components/Skeleton.svelte';
  import EmptyState from '$lib/components/EmptyState.svelte';
  import ErrorBox from '$lib/components/ErrorBox.svelte';

  const ctx = getProject();
  const id = $derived(ctx.id);
  const base = $derived(`/projects/${id}`);

  const services = resource(async () => (await unwrap(api.GET('/projects/{id}/services', { params: { path: { id } } }))).services, { deps: () => id, keys: () => [`p:${id}`, 'p:*'] });
  const certs = resource(async () => (await unwrap(api.GET('/certificates'))).certificates, { keys: () => ['certificates'] });
  const certName = (cid: number) => certs.data?.find((c) => c.id === cid)?.name ?? `#${cid}`;
  const endpointFor = (s: Service) => ctx.project?.endpoints.find((e) => e.kind === 'port' && e.service_id === s.id)?.url;

  let editing = $state<number | 'new' | null>(null);
  let draft = $state<ServiceInput | null>(null);
  let serverErrs = $state<Record<string, string>>({});
  let busy = $state(false);

  function startNew(proto: 'http' | 'https' | 'tcp' = 'http') {
    draft = newService(proto, proto === 'tcp' ? 443 : 80);
    serverErrs = {};
    editing = 'new';
  }
  function startEdit(s: Service) { draft = toInput(s); serverErrs = {}; editing = s.id; }
  function duplicate(s: Service) {
    const d = toInput(s);
    delete d.listen_port; // a fresh free port
    draft = d;
    serverErrs = {};
    editing = 'new';
  }

  async function save(s: ServiceInput) {
    busy = true;
    serverErrs = {};
    const opts = { onFieldErrors: (f: Record<string, string>) => (serverErrs = serviceFieldErrors(f)) };
    const r = editing === 'new'
      ? await mutate(api.POST('/projects/{id}/services', { params: { path: { id } }, body: s }), { ...opts, success: 'Service added' })
      : await mutate(api.PUT('/projects/{id}/services/{sid}', { params: { path: { id, sid: editing as number } }, body: s }), { ...opts, success: 'Service saved' });
    busy = false;
    if (r) { editing = null; void services.reload(); void ctx.reload(); }
  }

  async function remove(s: Service) {
    const doms = s.domains?.length ? ` Domains routed to it (${s.domains.join(', ')}) move to another HTTP service or stop working.` : '';
    const ok = await confirm({ title: `Delete ${serviceTitle(s)}?`, message: `Port ${s.listen_port} stops accepting connections. Targets are kept.${doms}`, confirmLabel: 'Delete service', danger: true });
    if (!ok) return;
    const r = await mutate(api.DELETE('/projects/{id}/services/{sid}', { params: { path: { id, sid: s.id } } }), { success: 'Service deleted' });
    if (r !== undefined) { void services.reload(); void ctx.reload(); }
  }

  const others = (except: number | 'new' | null) => (services.data ?? []).filter((x) => x.id !== except).map((x) => ({ listen_port: x.listen_port }));
</script>

<svelte:head><title>Services · {ctx.project?.name ?? 'Project'} · Loxy</title></svelte:head>

<div class="toolbar">
  <p class="section-intro" style:margin="0">Each service is a port this project listens on and the port it forwards to. Ports 80 and 443 are shared by all projects — use <a href="{base}/domains">Domains</a> for those.</p>
  <span class="spacer"></span>
  <Menu
    label="Add service"
    btnClass="btn primary"
    items={[
      { label: 'HTTP', onclick: () => startNew('http') },
      { label: 'HTTPS (TLS termination)', onclick: () => startNew('https') },
      { label: 'TCP', onclick: () => startNew('tcp') }
    ]}
  >
    {#snippet trigger()}<Icon name="plus" size={14} />Add service<Icon name="chevron" size={13} />{/snippet}
  </Menu>
</div>

{#if editing === 'new' && draft}
  <section class="card editing" aria-label="New service">
    <div class="card-head"><h2>New service</h2></div>
    {#key draft}<ServiceEditor value={draft} others={others(null)} certificates={certs.data ?? []} serverErrors={serverErrs} {busy} allowAuto
      submitLabel="Add service" onsave={save} oncancel={() => (editing = null)} oncertcreated={() => void certs.reload()} />{/key}
  </section>
{/if}

{#if services.error && !services.data}
  <ErrorBox message={services.error} onretry={services.reload} />
{:else if !services.data}
  <Skeleton card rows={5} />
{:else if services.data.length === 0 && editing !== 'new'}
  <div class="card">
    <EmptyState icon="globe" title="No services yet" text="Without a service the project accepts no traffic.">
      <button class="btn primary" onclick={() => startNew('http')}>Add HTTP service</button>
      <button class="btn" onclick={() => startNew('tcp')}>Add TCP service</button>
    </EmptyState>
  </div>
{:else}
  <div class="stack">
    {#each services.data as s (s.id)}
      {@const url = endpointFor(s)}
      <section class="card svc" aria-label={serviceTitle(s)}>
        <div class="card-head">
          {#if s.protocol === 'https'}<Icon name="shield" label="TLS" />{/if}
          <h2 class="mono">{serviceTitle(s)}</h2>
          {#if s.sticky_sessions}<span class="badge">Sticky</span>{/if}
          {#if s.proxy_protocol !== 'none'}<span class="badge warn">PROXY {s.proxy_protocol}</span>{/if}
          <HealthCount healthy={s.health.healthy} total={s.health.total} />
          <span class="spacer"></span>
          {#if editing !== s.id}
            <Menu label="Actions for {serviceTitle(s)}" items={[
              { label: 'Edit', onclick: () => startEdit(s) },
              { label: 'Duplicate', onclick: () => duplicate(s) },
              { label: 'Delete', onclick: () => remove(s), danger: true, separator: true }
            ]} />
          {/if}
        </div>
        {#if editing === s.id && draft}
          <ServiceEditor value={draft} others={others(s.id)} certificates={certs.data ?? []} serverErrors={serverErrs} {busy}
            onsave={save} oncancel={() => (editing = null)} oncertcreated={() => void certs.reload()} />
        {:else}
          <dl class="kv">
            {#if url}<dt>Endpoint</dt><dd>{#if /^https?:/.test(url)}<a class="mono small" href={url} target="_blank" rel="noopener noreferrer">{url}</a>{:else}<span class="mono small">{url}</span>{/if}</dd>{/if}
            {#if s.domains?.length}<dt>Domains</dt><dd class="mono small">{s.domains.join(', ')}</dd>{/if}
            <dt>Health check</dt><dd class="mono xs">{hcSummary(s)}</dd>
            <dt>Timeout</dt><dd>{s.timeout_s} s</dd>
            {#if s.sticky_sessions}<dt>Sticky cookie</dt><dd class="mono small">{s.cookie_name} · {s.cookie_lifetime_s} s</dd>{/if}
            {#if s.certificates.length}<dt>Certificates</dt><dd>{s.certificates.map(certName).join(', ')}</dd>{/if}
          </dl>
          {#if s.health.status !== 'healthy' && s.health.messages.length}
            <ul class="msgs">{#each s.health.messages.slice(0, 3) as m (m)}<li class="small" title={m}>{checkText(m)}</li>{/each}</ul>
          {/if}
          {#if s.health.total === 0}<p class="muted small">No targets to check. <a href="{base}/targets">Add targets</a></p>{/if}
        {/if}
      </section>
    {/each}
  </div>
{/if}

<style>
  .toolbar { display: flex; align-items: flex-start; gap: 16px; margin-bottom: 16px; flex-wrap: wrap; }
  .toolbar p { max-width: 70ch; }
  .svc h2 { font-size: var(--fs-lg); }
  .kv dd { overflow-wrap: anywhere; }
  .msgs { margin: 10px 0 0; padding-left: 16px; color: var(--text-2); }
  .editing { margin-bottom: 12px; border-color: var(--border-strong); }
</style>
