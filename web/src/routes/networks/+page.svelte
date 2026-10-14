<script lang="ts">
  import { api, unwrap, mutate, type Network } from '$lib/api/client';
  import { resource } from '$lib/stores/resource.svelte';
  import { confirm } from '$lib/stores/confirm.svelte';
  import Icon from '$lib/components/Icon.svelte';
  import Modal from '$lib/components/Modal.svelte';
  import Skeleton from '$lib/components/Skeleton.svelte';
  import EmptyState from '$lib/components/EmptyState.svelte';
  import ErrorBox from '$lib/components/ErrorBox.svelte';

  const nets = resource(async () => (await unwrap(api.GET('/networks'))).networks);
  let open = $state(false);
  let name = $state('');
  let cidr = $state('');
  let errs = $state<Record<string, string>>({});
  const CIDR = /^((25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)\.){3}(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)\/([0-9]|[12]\d|3[0-2])$/;

  async function create() {
    errs = {};
    if (!name.trim()) errs.name = 'Required';
    if (!CIDR.test(cidr.trim())) errs.cidr = 'Use CIDR notation, e.g. 10.0.0.0/16';
    if (Object.keys(errs).length) return;
    const r = await mutate(api.POST('/networks', { body: { name: name.trim(), cidr: cidr.trim() } }), { success: 'Network created', onFieldErrors: (f) => (errs = f) });
    if (r) { open = false; void nets.reload(); }
  }
  async function remove(n: Network) {
    const ok = await confirm({ title: `Delete ${n.name}?`, message: 'Load balancers must be detached from the network first.', confirmLabel: 'Delete network', danger: true });
    if (!ok) return;
    const r = await mutate(api.DELETE('/networks/{id}', { params: { path: { id: n.id } } }), { success: 'Network deleted' });
    if (r !== undefined) void nets.reload();
  }
</script>

<svelte:head><title>Networks · Loxy</title></svelte:head>
<div class="page-head">
  <h1>Networks</h1>
  <button class="btn primary" onclick={() => { name = ''; cidr = ''; errs = {}; open = true; }}><Icon name="plus" size={14} />Create network</button>
</div>
<p class="section-intro">Private networks the node is connected to. Load balancers attached to a network can reach targets by their private IPs.</p>

{#if nets.error && !nets.data}<ErrorBox message={nets.error} onretry={nets.reload} />
{:else if !nets.data}<Skeleton card rows={3} />
{:else if !nets.data.length}<div class="card"><EmptyState icon="network" title="No networks yet" text="Create a network to keep traffic between the load balancer and your servers private." /></div>
{:else}
  <div class="table-wrap">
    <table class="tbl">
      <thead><tr><th>Name</th><th>CIDR</th><th class="right">ID</th><th><span class="sr-only">Actions</span></th></tr></thead>
      <tbody>
        {#each nets.data as n (n.id)}
          <tr><td><b>{n.name}</b></td><td class="mono small">{n.cidr}</td><td class="right mono small muted">#{n.id}</td>
            <td class="actions"><button class="btn sm danger" onclick={() => remove(n)}>Delete</button></td></tr>
        {/each}
      </tbody>
    </table>
  </div>
{/if}

<Modal bind:open title="Create network" width={420}>
  <form id="netf" class="stack" onsubmit={(e) => { e.preventDefault(); void create(); }} novalidate>
    <div class="field"><label for="nn">Name</label><input id="nn" class="input" bind:value={name} aria-invalid={errs.name ? 'true' : undefined} />{#if errs.name}<span class="ferr">{errs.name}</span>{/if}</div>
    <div class="field"><label for="nc">IP range</label><input id="nc" class="input mono" bind:value={cidr} placeholder="10.0.0.0/16" aria-invalid={errs.cidr ? 'true' : undefined} />{#if errs.cidr}<span class="ferr">{errs.cidr}</span>{/if}</div>
  </form>
  {#snippet footer()}
    <button class="btn" onclick={() => (open = false)}>Cancel</button>
    <button class="btn primary" type="submit" form="netf">Create network</button>
  {/snippet}
</Modal>
