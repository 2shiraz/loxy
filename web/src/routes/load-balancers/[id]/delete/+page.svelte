<script lang="ts">
  import { goto } from '$app/navigation';
  import { api, mutate } from '$lib/api/client';
  import { getLb } from '$lib/stores/lbctx';
  import { confirm } from '$lib/stores/confirm.svelte';
  import { bump } from '$lib/stores/live.svelte';

  const ctx = getLb();
  const lb = $derived(ctx.lb!);

  async function unprotect() {
    const ok = await confirm({ title: 'Disable delete protection?', message: `${lb.name} can then be deleted.`, confirmLabel: 'Disable protection', danger: true });
    if (!ok) return;
    const r = await mutate(api.PATCH('/load_balancers/{id}', { params: { path: { id: lb.id } }, body: { delete_protected: false } }), { success: 'Delete protection disabled' });
    if (r) ctx.set(r.load_balancer);
  }
  async function del() {
    const ok = await confirm({ title: `Delete ${lb.name}?`, message: 'This removes the load balancer, its services and target assignments, and releases its IP addresses. This cannot be undone.', confirmLabel: 'Delete load balancer', danger: true, typed: lb.name });
    if (!ok) return;
    const r = await mutate(api.DELETE('/load_balancers/{id}', { params: { path: { id: lb.id } } }), { success: `${lb.name} deleted` });
    if (r !== undefined) { bump('lbs'); void goto('/load-balancers'); }
  }
</script>

<section class="card del" aria-labelledby="dh">
  <h2 id="dh">Delete load balancer</h2>
  <p>Deleting removes all services and target assignments of <b>{lb.name}</b> and releases its IP addresses. Your servers, networks and certificates are not touched. Metrics are kept for 7 days.</p>
  {#if lb.delete_protected}
    <p class="small warn-text">Delete protection is on. <button class="linklike" onclick={unprotect}>Disable protection</button> to delete.</p>
  {/if}
  <div><button class="btn danger solid" disabled={lb.delete_protected} onclick={del}>Delete load balancer</button></div>
</section>

<style>
  .del { display: flex; flex-direction: column; gap: 12px; max-width: 680px; }
  .del p { color: var(--text-2); }
</style>
