<script lang="ts">
  import { api, unwrap, mutate, type Service, type ServiceInput, type CheckStatus } from '$lib/api/client';
  import { getLb } from '$lib/stores/lbctx';
  import { resource } from '$lib/stores/resource.svelte';
  import { confirm } from '$lib/stores/confirm.svelte';
  import { TEMPLATES, newService, toInput, serviceTitle, hcSummary } from '$lib/util/services';
  import Icon from '$lib/components/Icon.svelte';
  import Menu from '$lib/components/Menu.svelte';
  import ServiceEditor from '$lib/components/ServiceEditor.svelte';
  import HealthBadge from '$lib/components/HealthBadge.svelte';
  import HealthCount from '$lib/components/HealthCount.svelte';
  import Skeleton from '$lib/components/Skeleton.svelte';
  import EmptyState from '$lib/components/EmptyState.svelte';
  import ErrorBox from '$lib/components/ErrorBox.svelte';

  const ctx = getLb();
  const id = $derived(ctx.id);
  const lb = $derived(ctx.lb!);

  const services = resource(async () => (await unwrap(api.GET('/load_balancers/{id}/services', { params: { path: { id } } }))).services, { deps: () => id, keys: () => [`lb:${id}`] });
  const status = resource(async () => await unwrap(api.GET('/load_balancers/{id}/status', { params: { path: { id } } })), { deps: () => id, keys: () => [`lb:${id}`] });
  const certs = resource(async () => (await unwrap(api.GET('/certificates'))).certificates, { keys: () => ['certificates'] });

  let editing = $state<number | 'new' | null>(null);
  let draft = $state<ServiceInput | null>(null);
  let serverErrs = $state<Record<string, string>>({});
  let busy = $state(false);

  function startNew(t?: string) {
    draft = t ? TEMPLATES.find((x) => x.id === t)!.make() : newService('http', 80, 80);
    serverErrs = {};
    editing = 'new';
  }
  function startEdit(s: Service) { draft = toInput(s); serverErrs = {}; editing = s.id; }
  function duplicate(s: Service) {
    const d = toInput(s);
    const used = new Set((services.data ?? []).map((x) => x.listen_port));
    let p = d.listen_port;
    while (used.has(p) && p < 65535) p++;
    d.listen_port = p;
    draft = d;
    serverErrs = {};
    editing = 'new';
  }

  async function save(s: ServiceInput) {
    busy = true;
    serverErrs = {};
    const opts = { onFieldErrors: (f: Record<string, string>) => (serverErrs = f) };
    const r = editing === 'new'
      ? await mutate(api.POST('/load_balancers/{id}/services', { params: { path: { id } }, body: s }), { ...opts, success: `Service ${serviceTitle(s)} created` })
      : await mutate(api.PUT('/load_balancers/{id}/services/{sid}', { params: { path: { id, sid: editing as number } }, body: s }), { ...opts, success: 'Service saved' });
    busy = false;
    if (r) { editing = null; void services.reload(); void status.reload(); void ctx.reload(); }
  }

  async function remove(s: Service) {
    const ok = await confirm({ title: `Delete ${serviceTitle(s)}?`, message: 'The listener stops accepting connections. Targets and certificates are kept.', confirmLabel: 'Delete service', danger: true });
    if (!ok) return;
    const r = await mutate(api.DELETE('/load_balancers/{id}/services/{sid}', { params: { path: { id, sid: s.id } } }), { success: 'Service deleted' });
    if (r !== undefined) { void services.reload(); void ctx.reload(); }
  }

  const others = (except: number | 'new' | null) => (services.data ?? []).filter((x) => x.id !== except).map((x) => ({ listen_port: x.listen_port }));
  const svcStatus = (sid: number) => status.data?.services.find((x) => x.service_id === sid);
  const certName = (cid: number) => certs.data?.find((c) => c.id === cid)?.name ?? `#${cid}`;
  function checkBadge(c: CheckStatus) { return c.status; }
</script>

<div class="toolbar">
  <p class="section-intro" style:margin="0">Services define which ports the load balancer listens on and where traffic goes. HTTPS services terminate TLS with your certificates.</p>
  <span class="spacer"></span>
  <Menu
    label="Add service"
    btnClass="btn primary"
    items={[
      { label: 'New service', onclick: () => startNew() },
      ...TEMPLATES.map((t, i) => ({ label: `Template: ${t.label}`, onclick: () => startNew(t.id), separator: i === 0 }))
    ]}
  >
    {#snippet trigger()}<Icon name="plus" size={14} />Add service<Icon name="chevron" size={13} />{/snippet}
  </Menu>
</div>

{#if editing === 'new' && draft}
  <section class="card editing" aria-label="New service">
    <div class="card-head"><h2>New service</h2></div>
    <ServiceEditor value={draft} others={others(null)} certificates={certs.data ?? []} serverErrors={serverErrs} {busy} submitLabel="Add service" onsave={save} oncancel={() => (editing = null)} />
  </section>
{/if}

{#if services.error && !services.data}
  <ErrorBox message={services.error} onretry={services.reload} />
{:else if !services.data}
  <Skeleton card rows={5} />
{:else if services.data.length === 0 && editing !== 'new'}
  <div class="card">
    <EmptyState icon="globe" title="No services yet" text="Add a service to start accepting traffic. Templates cover the common cases.">
      <button class="btn" onclick={() => startNew('http')}>HTTP</button>
      <button class="btn" onclick={() => startNew('tls-term')}>TLS Termination</button>
      <button class="btn" onclick={() => startNew('tls-pass')}>TLS Passthrough</button>
    </EmptyState>
  </div>
{:else}
  <div class="stack">
    {#each services.data as s (s.id)}
      {@const st = svcStatus(s.id)}
      <section class="card svc" aria-label={serviceTitle(s)}>
        <div class="card-head">
          {#if s.protocol === 'https' && s.http?.certificates?.length}<Icon name="shield" label="TLS certificates attached" />{/if}
          <h2 class="mono">{serviceTitle(s)}</h2>
          {#if s.http?.redirect_http}<span class="badge">:80 → 301</span>{/if}
          {#if s.proxy_protocol !== 'none'}<span class="badge warn">PROXY {s.proxy_protocol}</span>{/if}
          {#if st}<HealthCount healthy={st.healthy} total={st.total} />{/if}
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
          <ServiceEditor value={draft} others={others(s.id)} certificates={certs.data ?? []} serverErrors={serverErrs} {busy} onsave={save} oncancel={() => (editing = null)} />
        {:else}
          <p class="hcline mono xs muted">{hcSummary(s)}</p>
          {#if s.http?.certificates?.length}
            <p class="small certs"><span class="muted">Certificates</span> {s.http.certificates.map(certName).join(', ')}</p>
          {/if}
          {#if st && st.checks.length}
            <div class="table-wrap bare">
              <table class="tbl">
                <thead><tr><th>Name</th><th>Address</th><th>Status</th><th>Last check</th></tr></thead>
                <tbody>
                  {#each st.checks as c (c.slot_id)}
                    <tr>
                      <td>{c.name ?? '—'}</td>
                      <td class="mono small">{c.address}</td>
                      <td><span title={c.check ?? ''}><HealthBadge status={checkBadge(c)} /></span></td>
                      <td class="mono xs muted">{c.check ?? '—'}</td>
                    </tr>
                  {/each}
                </tbody>
              </table>
            </div>
          {:else}
            <p class="muted small">No targets to check. <a href="/load-balancers/{lb.id}/targets">Add targets</a></p>
          {/if}
        {/if}
      </section>
    {/each}
  </div>
{/if}

<style>
  .toolbar { display: flex; align-items: flex-start; gap: 16px; margin-bottom: 16px; flex-wrap: wrap; }
  .svc h2 { font-size: var(--fs-lg); }
  .hcline { margin-bottom: 10px; overflow-wrap: anywhere; }
  .certs { margin-bottom: 10px; }
  .editing { margin-bottom: 12px; border-color: var(--border-strong); }
</style>
