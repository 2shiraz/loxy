<script lang="ts">
  import { page } from '$app/state';
  import { api, unwrap, type Domain } from '$lib/api/client';
  import { resource } from '$lib/stores/resource.svelte';
  import { relTime } from '$lib/util/format';
  import Icon from '$lib/components/Icon.svelte';
  import Modal from '$lib/components/Modal.svelte';
  import Drawer from '$lib/components/Drawer.svelte';
  import Skeleton from '$lib/components/Skeleton.svelte';
  import EmptyState from '$lib/components/EmptyState.svelte';
  import ErrorBox from '$lib/components/ErrorBox.svelte';
  import DomainStatus from '$lib/components/DomainStatus.svelte';
  import DomainAddForm from '$lib/components/DomainAddForm.svelte';
  import DomainDetails from '$lib/components/DomainDetails.svelte';

  const projectId = $derived(Number(page.url.searchParams.get('project_id')) || undefined);
  const domains = resource(async () => (await unwrap(api.GET('/domains', { params: { query: projectId ? { project_id: projectId } : {} } }))).domains, { deps: () => projectId, keys: () => ['domains'] });
  const projects = resource(async () => (await unwrap(api.GET('/projects'))).projects, { keys: () => ['projects'] });
  const providers = resource(async () => (await unwrap(api.GET('/dns_providers'))).dns_providers);
  const certs = resource(async () => (await unwrap(api.GET('/certificates'))).certificates, { keys: () => ['certificates'] });

  const projectName = (id: number | null | undefined) => (id ? (projects.data?.find((l) => l.id === id)?.name ?? `#${id}`) : '—');
  function certState(d: Domain) {
    if (!d.https) return 'HTTP only';
    if (!d.certificate_id) return d.status === 'ok' ? 'Being issued' : 'Waiting for DNS';
    const c = certs.data?.find((x) => x.id === d.certificate_id);
    return c ? ({ issued: 'Issued', pending: 'Pending', renewing: 'Renewing', failed: 'Failed' } as Record<string, string>)[c.status] ?? c.status : 'Pending';
  }

  let addOpen = $state(false);
  let addKey = $state(0);
  function openAdd() { addKey++; addOpen = true; }
  function added(d: Domain) { addOpen = false; void domains.reload(); selectedId = d.id; fresh = d; }

  // Drawer
  let selectedId = $state<number | null>(null);
  let fresh = $state<Domain | null>(null);
  const selected = $derived(selectedId == null ? null : (fresh?.id === selectedId ? fresh : domains.data?.find((d) => d.id === selectedId) ?? null));
  let drawerOpen = $state(false);
  $effect(() => { drawerOpen = selectedId != null; });
  $effect(() => { if (!drawerOpen) selectedId = null; });
  $effect(() => { void domains.data; fresh = null; });
  function open(d: Domain) { fresh = null; selectedId = d.id; }
</script>

<div class="row-wrap intro">
  <p class="muted">Domains route a hostname on the shared ports 80 and 443 to a project, with an automatic certificate. Loxy checks that DNS points at this server.</p>
  <span class="spacer"></span>
  <button class="btn primary" onclick={openAdd} disabled={!projects.data?.length} title={projects.data && !projects.data.length ? 'Create a project first' : undefined}><Icon name="plus" size={14} />Add domain</button>
</div>

{#if projectId}
  <p class="small filt"><span class="chip">Project: {projectName(projectId)} <a href="/domains" class="clr" aria-label="Show domains of all projects">×</a></span></p>
{/if}

{#if domains.error && !domains.data}
  <ErrorBox message={domains.error} onretry={domains.reload} />
{:else if !domains.data}
  <Skeleton card rows={5} />
{:else if !domains.data.length}
  <div class="card">
    <EmptyState icon="dns" title="No domains yet" text={projects.data?.length ? 'Attach a domain to a project to serve it on ports 80 and 443 with HTTPS.' : 'Create a project first, then attach a domain to it.'}>
      {#if projects.data?.length}<button class="btn primary" onclick={openAdd}>Add domain</button>{:else}<a class="btn primary" href="/projects/new">Create project</a>{/if}
    </EmptyState>
  </div>
{:else}
  <div class="table-wrap">
    <table class="tbl cards">
      <thead><tr><th>Domain</th><th>Project</th><th>DNS</th><th>Certificate</th><th>Last checked</th></tr></thead>
      <tbody>
        {#each domains.data as d (d.id)}
          <tr class="clickable" onclick={() => open(d)}>
            <td class="lead"><button class="linklike name mono" onclick={(e) => { e.stopPropagation(); open(d); }}>{d.fqdn}</button></td>
            <td data-label="Project">{#if d.project_id}<a href="/projects/{d.project_id}/domains" onclick={(e) => e.stopPropagation()}>{projectName(d.project_id)}</a>{:else}—{/if}</td>
            <td data-label="DNS"><DomainStatus status={d.status} /></td>
            <td class="small" data-label="Certificate">{certState(d)}</td>
            <td class="muted small" data-label="Last checked">{relTime(d.checked_at)}</td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
{/if}

<Modal bind:open={addOpen} title="Add domain" width={540}>
  {#key addKey}
    <DomainAddForm projectId={projectId} projects={projects.data ?? []} providers={providers.data ?? []} onadded={added} oncancel={() => (addOpen = false)} />
  {/key}
</Modal>

<Drawer bind:open={drawerOpen} title={selected?.fqdn ?? ''}>
  {#if selected}
    <div class="stack-lg">
      {#if selected.project_id}
        <p class="small"><span class="muted">Project</span> <a href="/projects/{selected.project_id}/domains">{projectName(selected.project_id)}</a></p>
      {/if}
      <DomainDetails domain={selected} certificates={certs.data ?? []} onchange={(d) => (fresh = d)} onrefresh={() => { void domains.reload(); void certs.reload(); }} onremoved={() => { selectedId = null; void domains.reload(); }} />
    </div>
  {/if}
</Drawer>

<style>
  .intro { margin-bottom: 14px; }
  .intro p { max-width: 70ch; }
  .filt { margin-bottom: 10px; }
  .clr { text-decoration: none; margin-left: 4px; color: var(--muted); }
  .clr:hover { color: var(--text); }
  .name { text-decoration: none; font-weight: 500; }
  .name:hover { text-decoration: underline; }
</style>
