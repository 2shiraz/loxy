<script lang="ts">
  import { api, unwrap, mutate, type Plan } from '$lib/api/client';
  import { getLb } from '$lib/stores/lbctx';
  import { resource } from '$lib/stores/resource.svelte';
  import { confirm } from '$lib/stores/confirm.svelte';
  import { bytes, compact, price } from '$lib/util/format';
  import Skeleton from '$lib/components/Skeleton.svelte';

  const ctx = getLb();
  const lb = $derived(ctx.lb!);
  const plans = resource(async () => (await unwrap(api.GET('/plans'))).plans);
  let busy = $state(false);

  function reason(p: Plan): string {
    const u = lb.usage;
    const n = p.name.toUpperCase();
    if (u.services > p.max_services) return `You use ${u.services} services; ${n} allows ${p.max_services}`;
    if (u.targets > p.max_targets) return `You use ${u.targets} targets; ${n} allows ${p.max_targets}`;
    if (u.certificates > p.max_certificates) return `You use ${u.certificates} certificates; ${n} allows ${p.max_certificates}`;
    return '';
  }
  async function pick(p: Plan) {
    const ok = await confirm({ title: `Change plan to ${p.name.toUpperCase()}?`, message: 'Quotas and connection limits change with one reload. Traffic counters are kept.', confirmLabel: 'Change plan' });
    if (!ok) return;
    busy = true;
    const r = await mutate(api.POST('/load_balancers/{id}/actions/change_plan', { params: { path: { id: lb.id } }, body: { plan_id: p.id } }), { success: `Plan changed to ${p.name.toUpperCase()}` });
    busy = false;
    if (r) ctx.set(r.load_balancer);
  }
</script>

<p class="section-intro">Pick a plan that fits your usage. Plans below your current usage are unavailable.</p>
{#if !plans.data}<Skeleton card rows={4} />{:else}
  <div class="cards">
    {#each plans.data as p (p.id)}
      {@const cur = p.id === lb.plan.id || p.name === lb.plan.name}
      {@const why = reason(p)}
      <div class="plan card" class:cur class:dis={!!why}>
        <div class="row"><span class="badge plan">{p.name}</span><span class="spacer"></span>{#if p.price_monthly_cents != null}<b class="num">{price(p.price_monthly_cents, p.currency)}</b><span class="muted xs">/mo</span>{/if}</div>
        <dl class="limits">
          <dt>Connections</dt><dd>{compact(p.max_connections)}</dd>
          <dt>Services</dt><dd>{p.max_services}</dd>
          <dt>Targets</dt><dd>{p.max_targets}</dd>
          <dt>Certificates</dt><dd>{p.max_certificates}</dd>
          <dt>Traffic</dt><dd>{bytes(p.traffic_quota_bytes)}</dd>
        </dl>
        {#if cur}<span class="badge ok">Current plan</span>
        {:else if why}<p class="xs warn-text">{why}</p>
        {:else}<button class="btn sm" disabled={busy} onclick={() => pick(p)}>Change to {p.name.toUpperCase()}</button>{/if}
      </div>
    {/each}
  </div>
{/if}

<style>
  .cards { display: grid; grid-template-columns: repeat(auto-fill, minmax(200px, 1fr)); gap: 12px; }
  .plan { display: flex; flex-direction: column; gap: 10px; align-items: stretch; }
  .plan.cur { border-color: var(--text-2); box-shadow: inset 0 0 0 1px var(--text-2); }
  .plan.dis { opacity: 0.7; }
  .plan .badge.ok { align-self: flex-start; }
  .limits { display: grid; grid-template-columns: 1fr auto; gap: 2px 8px; margin: 0; font-size: var(--fs-sm); }
  .limits dt { color: var(--muted); }
  .limits dd { margin: 0; text-align: right; font-variant-numeric: tabular-nums; }
</style>
