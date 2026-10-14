<script lang="ts">
  import { api, mutate, type Labels } from '$lib/api/client';
  import { getLb } from '$lib/stores/lbctx';
  import { confirm } from '$lib/stores/confirm.svelte';
  import { bytes, compact, num, price, flag, algorithmLabel } from '$lib/util/format';
  import StatBar from '$lib/components/StatBar.svelte';
  import StatCard from '$lib/components/StatCard.svelte';
  import Donut from '$lib/components/Donut.svelte';
  import HealthBadge from '$lib/components/HealthBadge.svelte';
  import LabelChips from '$lib/components/LabelChips.svelte';
  import LabelEditor from '$lib/components/LabelEditor.svelte';
  import Modal from '$lib/components/Modal.svelte';
  import Icon from '$lib/components/Icon.svelte';

  const ctx = getLb();
  const lb = $derived(ctx.lb!);
  const p = $derived(lb.plan);
  const u = $derived(lb.usage);

  let alg = $state('');
  $effect(() => { alg = lb.algorithm; });

  async function changeAlg(e: Event) {
    const v = (e.currentTarget as HTMLSelectElement).value as typeof lb.algorithm;
    if (v === lb.algorithm) return;
    const ok = await confirm({ title: 'Change algorithm?', message: `Switch from ${algorithmLabel(lb.algorithm)} to ${algorithmLabel(v)}. This applies immediately.`, confirmLabel: 'Change algorithm' });
    if (!ok) { alg = lb.algorithm; return; }
    const r = await mutate(api.PATCH('/load_balancers/{id}', { params: { path: { id: lb.id } }, body: { algorithm: v } }), { success: 'Algorithm changed' });
    if (r) ctx.set(r.load_balancer); else alg = lb.algorithm;
  }

  let labelsOpen = $state(false);
  let draft = $state<Labels>({});
  let draftErr = $state('');
  async function saveLabels() {
    const prev = lb;
    ctx.set({ ...lb, labels: draft }); // optimistic
    labelsOpen = false;
    const r = await mutate(api.PATCH('/load_balancers/{id}', { params: { path: { id: lb.id } }, body: { labels: draft } }), { success: 'Labels saved' });
    ctx.set(r ? r.load_balancer : prev);
  }
</script>

<div class="stack-lg">
  <StatBar>
    <StatCard label="Targets" value={num(u.targets)} of={num(p.max_targets)} ratio={p.max_targets ? u.targets / p.max_targets : undefined} />
    <StatCard label="Services" value={num(u.services)} of={num(p.max_services)} ratio={p.max_services ? u.services / p.max_services : undefined} />
    <StatCard label="Certificates" value={num(u.certificates)} of={num(p.max_certificates)} ratio={p.max_certificates ? u.certificates / p.max_certificates : undefined} />
    <StatCard label="Connections" value={compact(u.open_connections)} of={compact(p.max_connections)} ratio={p.max_connections ? u.open_connections / p.max_connections : undefined} />
    <StatCard label="Traffic out (month)" value={bytes(u.traffic_out_month_bytes)} of={bytes(p.traffic_quota_bytes)} ratio={p.traffic_quota_bytes ? u.traffic_out_month_bytes / p.traffic_quota_bytes : undefined} />
    {#if p.price_monthly_cents != null}<StatCard label="Price" value={price(p.price_monthly_cents, p.currency)} hint="per month" />{/if}
  </StatBar>

  <div class="labels-row">
    <span class="muted small">Labels</span>
    {#if Object.keys(lb.labels).length}<LabelChips labels={lb.labels} />{:else}<span class="muted small">None</span>{/if}
    <button class="btn sm ghost" onclick={() => { draft = { ...lb.labels }; labelsOpen = true; }}><Icon name={Object.keys(lb.labels).length ? 'edit' : 'plus'} size={13} />{Object.keys(lb.labels).length ? 'Edit labels' : 'Add labels'}</button>
  </div>

  <div class="grid-3">
    <section class="card health" aria-labelledby="h-health">
      <div class="card-head"><h2 id="h-health">Health status</h2><span class="spacer"></span><HealthBadge status={lb.health.status} /></div>
      <div class="hbody">
        <Donut value={lb.health.healthy} total={lb.health.total} />
        <div class="msgs">
          {#if lb.health.total === 0}
            <p class="muted">No checks yet. Add a service and targets to start health checking.</p>
          {:else if lb.health.messages.length === 0 || lb.health.status === 'healthy'}
            <p>All services are healthy.</p>
          {:else}
            <ul>
              {#each lb.health.messages.slice(0, 3) as m (m)}<li class="mono small">{m}</li>{/each}
            </ul>
          {/if}
          <a class="small" href="/load-balancers/{lb.id}/services">View per-service status</a>
        </div>
      </div>
    </section>

    <section class="card" aria-labelledby="h-loc">
      <div class="card-head"><h2 id="h-loc">Location</h2></div>
      <dl class="kv">
        <dt>City</dt><dd>{lb.location.city ?? '—'}</dd>
        <dt>Country</dt><dd>{flag(lb.location.country)} {lb.location.country ?? '—'}</dd>
        <dt>Network zone</dt><dd class="mono small">{lb.location.network_zone ?? '—'}</dd>
        {#if lb.location.lat != null && lb.location.lon != null}<dt>Coordinates</dt><dd class="mono small">{lb.location.lat.toFixed(2)}, {lb.location.lon.toFixed(2)}</dd>{/if}
      </dl>
    </section>

    <section class="card" aria-labelledby="h-opt">
      <div class="card-head"><h2 id="h-opt">Options</h2></div>
      <div class="stack">
        <div class="field">
          <label for="alg">Algorithm</label>
          <select id="alg" class="select" bind:value={alg} onchange={changeAlg}>
            <option value="roundrobin">Round Robin</option>
            <option value="leastconn">Least Connections</option>
            {#if lb.algorithm === 'source' || lb.algorithm === 'random'}<option value={lb.algorithm}>{algorithmLabel(lb.algorithm)}</option>{/if}
          </select>
        </div>
        <dl class="kv">
          <dt>Public network</dt>
          <dd><a href="/load-balancers/{lb.id}/networking">{lb.public_net.enabled ? 'Enabled' : 'Disabled'}</a></dd>
          <dt>Maintenance</dt><dd>{lb.maintenance ? 'On' : 'Off'}</dd>
          <dt>Delete protection</dt><dd>{lb.delete_protected ? 'On' : 'Off'}</dd>
        </dl>
      </div>
    </section>
  </div>
</div>

<Modal bind:open={labelsOpen} title="Edit labels">
  <LabelEditor bind:labels={draft} bind:error={draftErr} />
  {#snippet footer()}
    <button class="btn" onclick={() => (labelsOpen = false)}>Cancel</button>
    <button class="btn primary" disabled={!!draftErr} onclick={saveLabels}>Save labels</button>
  {/snippet}
</Modal>

<style>
  .labels-row { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; }
  .hbody { display: flex; gap: 18px; align-items: center; }
  .msgs { display: flex; flex-direction: column; gap: 8px; min-width: 0; }
  ul { margin: 0; padding-left: 16px; color: var(--err); }
  li { overflow-wrap: anywhere; color: var(--text-2); }
</style>
