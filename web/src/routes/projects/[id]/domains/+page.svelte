<script lang="ts">
  import { api, unwrap, type Domain } from '$lib/api/client';
  import { getProject } from '$lib/stores/project';
  import { resource } from '$lib/stores/resource.svelte';
  import { bump } from '$lib/stores/live.svelte';
  import Icon from '$lib/components/Icon.svelte';
  import DomainAddForm from '$lib/components/DomainAddForm.svelte';
  import DomainDetails from '$lib/components/DomainDetails.svelte';
  import DomainStatus from '$lib/components/DomainStatus.svelte';
  import Skeleton from '$lib/components/Skeleton.svelte';
  import ErrorBox from '$lib/components/ErrorBox.svelte';

  const ctx = getProject();
  const id = $derived(ctx.id);

  const domains = resource(async () => (await unwrap(api.GET('/domains', { params: { query: { project_id: id } } }))).domains, { deps: () => id, keys: () => ['domains', `p:${id}`] });
  const certs = resource(async () => (await unwrap(api.GET('/certificates'))).certificates, { keys: () => ['certificates'] });
  const providers = resource(async () => (await unwrap(api.GET('/dns_providers'))).dns_providers);

  // Local overrides from actions, cleared on the next fetch.
  let fresh = $state<Record<number, Domain>>({});
  $effect(() => { void domains.data; fresh = {}; });
  const list = $derived((domains.data ?? []).map((d) => fresh[d.id] ?? d));
  let open = $state<Record<number, boolean>>({});

  function changed(d: Domain) { fresh = { ...fresh, [d.id]: d }; void certs.reload(); }
  function added(d: Domain) {
    open = { ...open, [d.id]: true };
    void domains.reload();
    void ctx.reload();
    bump('domains');
  }
  function removed() { void domains.reload(); void ctx.reload(); bump('domains'); }
</script>

<svelte:head><title>Domains · {ctx.project?.name ?? 'Project'} · Loxy</title></svelte:head>

<div class="stack-lg">
  <section class="card" aria-labelledby="h-add">
    <div class="card-head"><h2 id="h-add">Attach a domain</h2></div>
    <DomainAddForm projectId={id} providers={providers.data ?? []} onadded={added} />
  </section>

  {#if domains.error && !domains.data}
    <ErrorBox message={domains.error} onretry={domains.reload} />
  {:else if !domains.data}
    <Skeleton card rows={4} />
  {:else if !list.length}
    <p class="muted small empty"><Icon name="dns" size={14} />No domains yet. Without one, the project is reachable on its own port only.</p>
  {:else}
    <div class="stack">
      {#each list as d (d.id)}
        <!-- Domains that still need DNS work start expanded. -->
        <details class="card dom" open={open[d.id] ?? (list.length === 1 || d.status !== 'ok')}
          ontoggle={(e) => (open[d.id] = (e.currentTarget as HTMLDetailsElement).open)}>
          <summary>
            <span class="fq mono">{d.fqdn}</span>
            <DomainStatus status={d.status} />
            {#if d.https}<span class="badge {d.url?.startsWith('https') ? 'ok' : ''}"><Icon name="shield" size={11} />{d.url?.startsWith('https') ? 'HTTPS' : 'HTTPS pending'}</span>{:else}<span class="badge">HTTP only</span>{/if}
            <span class="spacer"></span>
            {#if d.url}<a class="small mono" href={d.url} target="_blank" rel="noopener noreferrer" onclick={(e) => e.stopPropagation()}>{d.url}</a>{/if}
          </summary>
          <div class="body">
            <DomainDetails domain={d} certificates={certs.data ?? []} onchange={changed} onremoved={removed} onrefresh={() => { void domains.reload(); void certs.reload(); }} />
          </div>
        </details>
      {/each}
    </div>
  {/if}
</div>

<style>
  .empty { display: flex; align-items: center; gap: 8px; }
  .dom { padding: 0; }
  .dom summary { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; padding: 12px 16px; cursor: pointer; list-style: none; }
  .dom summary::-webkit-details-marker { display: none; }
  .dom summary::before { content: '▸'; color: var(--muted); font-size: 11px; transition: transform 120ms var(--ease-out); }
  .dom[open] summary::before { transform: rotate(90deg); }
  .dom summary:focus-visible { outline: 2px solid var(--focus); outline-offset: -2px; border-radius: var(--radius); }
  .fq { font-weight: 600; overflow-wrap: anywhere; }
  .body { padding: 4px 16px 16px; border-top: 1px solid var(--border); padding-top: 14px; }
</style>
