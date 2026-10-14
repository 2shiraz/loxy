<script lang="ts">
  import { api, unwrap, mutate, type Node } from '$lib/api/client';
  import { resource } from '$lib/stores/resource.svelte';
  import { confirm } from '$lib/stores/confirm.svelte';
  import { duration, flag, num } from '$lib/util/format';
  import { isIPv4, isIPv6 } from '$lib/util/validate';
  import Icon from '$lib/components/Icon.svelte';
  import Modal from '$lib/components/Modal.svelte';
  import HealthBadge from '$lib/components/HealthBadge.svelte';
  import CopyText from '$lib/components/CopyText.svelte';
  import Skeleton from '$lib/components/Skeleton.svelte';
  import ErrorBox from '$lib/components/ErrorBox.svelte';

  const nodes = resource(async () => (await unwrap(api.GET('/nodes'))).nodes, { keys: () => ['nodes'] });

  let locOpen = $state(false);
  let locNode = $state<Node | null>(null);
  let loc = $state({ city: '', country: '', network_zone: '', lat: '' as string | number, lon: '' as string | number });
  function editLoc(n: Node) {
    locNode = n;
    loc = { city: n.location.city ?? '', country: n.location.country ?? '', network_zone: n.location.network_zone ?? '', lat: n.location.lat ?? '', lon: n.location.lon ?? '' };
    locOpen = true;
  }
  async function saveLoc() {
    if (!locNode) return;
    const body = { location: { city: loc.city, country: loc.country.toUpperCase(), network_zone: loc.network_zone, lat: loc.lat === '' ? undefined : Number(loc.lat), lon: loc.lon === '' ? undefined : Number(loc.lon) } };
    const r = await mutate(api.PATCH('/nodes/{id}', { params: { path: { id: locNode.id } }, body }), { success: 'Location saved' });
    if (r) { locOpen = false; void nodes.reload(); }
  }

  let ipOpen = $state(false);
  let ipNode = $state<Node | null>(null);
  let addr = $state('');
  let kind = $state<'primary' | 'additional' | 'failover' | 'private'>('additional');
  let ipErr = $state('');
  async function addIp() {
    if (!ipNode) return;
    ipErr = '';
    if (!isIPv4(addr) && !isIPv6(addr)) { ipErr = 'Enter an IPv4 or IPv6 address'; return; }
    const r = await mutate(api.POST('/nodes/{id}/ips', { params: { path: { id: ipNode.id } }, body: { address: addr, kind } }), { success: 'Address added', onFieldErrors: (f) => (ipErr = Object.values(f)[0] ?? '') });
    if (r) { ipOpen = false; void nodes.reload(); }
  }
  async function delIp(n: Node, ipid: number, a: string) {
    const ok = await confirm({ title: `Remove ${a}?`, message: 'The address is removed from the pool. Addresses assigned to a load balancer cannot be removed.', confirmLabel: 'Remove address', danger: true });
    if (!ok) return;
    const r = await mutate(api.DELETE('/nodes/{id}/ips/{ipid}', { params: { path: { id: n.id, ipid } } }), { success: 'Address removed' });
    if (r !== undefined) void nodes.reload();
  }
</script>

<svelte:head><title>Nodes · Loxy</title></svelte:head>
<div class="page-head"><h1>Nodes</h1></div>

{#if nodes.error && !nodes.data}<ErrorBox message={nodes.error} onretry={nodes.reload} />
{:else if !nodes.data}<Skeleton card rows={6} />
{:else}
  <div class="stack-lg">
    {#each nodes.data as n (n.id)}
      <section class="card" aria-labelledby="n{n.id}">
        <div class="card-head">
          <Icon name="node" />
          <h2 id="n{n.id}">{n.name}</h2>
          <HealthBadge status={n.status === 'online' ? 'healthy' : n.status} label={n.status} />
          {#if n.desired_version !== n.applied_version}<span class="badge warn">Applying v{n.desired_version}</span>{/if}
          <span class="spacer"></span>
          <button class="btn sm" onclick={() => editLoc(n)}><Icon name="edit" size={13} />Edit location</button>
        </div>
        <div class="grid-2">
          <dl class="kv">
            <dt>HAProxy</dt><dd class="mono small">{n.haproxy_version ?? '—'}{n.haproxy_running_version && n.haproxy_running_version !== n.haproxy_version ? ` (running ${n.haproxy_running_version})` : ''}</dd>
            <dt>Loxy</dt><dd class="mono small">{n.loxy_version ?? '—'}</dd>
            <dt>Uptime</dt><dd>{duration(n.uptime_s)}</dd>
            <dt>Reloads</dt><dd>{num(n.reloads)}</dd>
            <dt>Config version</dt><dd class="mono small">applied {n.applied_version} / desired {n.desired_version}</dd>
          </dl>
          <dl class="kv">
            <dt>Location</dt><dd>{flag(n.location.country)} {n.location.city || '—'}{n.location.country ? `, ${n.location.country}` : ''}</dd>
            <dt>Network zone</dt><dd class="mono small">{n.location.network_zone || '—'}</dd>
            <dt>Features</dt><dd><span class="row-wrap">{#each n.features ?? [] as f (f)}<span class="chip">{f}</span>{:else}<span class="muted">—</span>{/each}</span></dd>
          </dl>
        </div>

        <div class="iph">
          <h3>IP pool</h3>
          <span class="spacer"></span>
          <button class="btn sm" onclick={() => { ipNode = n; addr = ''; kind = 'additional'; ipErr = ''; ipOpen = true; }}><Icon name="plus" size={13} />Add address</button>
        </div>
        {#if n.ips.length}
          <div class="table-wrap">
            <table class="tbl">
              <thead><tr><th>Address</th><th>Family</th><th>Kind</th><th>Assigned to</th><th><span class="sr-only">Actions</span></th></tr></thead>
              <tbody>
                {#each n.ips as ip (ip.id)}
                  <tr>
                    <td><CopyText text={ip.address} /></td><td class="small">IPv{ip.family}</td><td class="small">{ip.kind}</td>
                    <td>{#if ip.assigned_lb_id}<a href="/load-balancers/{ip.assigned_lb_id}/overview">LB #{ip.assigned_lb_id}</a>{:else}<span class="muted small">Free</span>{/if}</td>
                    <td class="actions"><button class="btn ghost icon sm" aria-label="Remove {ip.address}" disabled={!!ip.assigned_lb_id} onclick={() => delIp(n, ip.id, ip.address)}><Icon name="trash" size={14} /></button></td>
                  </tr>
                {/each}
              </tbody>
            </table>
          </div>
        {:else}<p class="muted small">No addresses in the pool. Load balancers will share the node's primary address.</p>{/if}
      </section>
    {/each}
  </div>
{/if}

<Modal bind:open={locOpen} title="Edit location" width={480}>
  <form id="locf" class="form-grid" onsubmit={(e) => { e.preventDefault(); void saveLoc(); }}>
    <div class="field"><label for="lc">City</label><input id="lc" class="input" bind:value={loc.city} /></div>
    <div class="field"><label for="lco">Country code</label><input id="lco" class="input mono" maxlength="2" bind:value={loc.country} placeholder="DE" /></div>
    <div class="field"><label for="lz">Network zone</label><input id="lz" class="input mono" bind:value={loc.network_zone} placeholder="eu-central" /></div>
    <div class="field"><label for="lla">Latitude</label><input id="lla" class="input mono" type="number" step="any" bind:value={loc.lat} /></div>
    <div class="field"><label for="llo">Longitude</label><input id="llo" class="input mono" type="number" step="any" bind:value={loc.lon} /></div>
  </form>
  {#snippet footer()}
    <button class="btn" onclick={() => (locOpen = false)}>Cancel</button>
    <button class="btn primary" type="submit" form="locf">Save location</button>
  {/snippet}
</Modal>

<Modal bind:open={ipOpen} title="Add address to pool" width={420}>
  <form id="ipf" class="stack" onsubmit={(e) => { e.preventDefault(); void addIp(); }} novalidate>
    <div class="field"><label for="ia">Address</label><input id="ia" class="input mono" bind:value={addr} placeholder="203.0.113.20" aria-invalid={ipErr ? 'true' : undefined} />{#if ipErr}<span class="ferr">{ipErr}</span>{/if}</div>
    <div class="field"><label for="ik">Kind</label>
      <select id="ik" class="select" bind:value={kind}><option value="primary">Primary</option><option value="additional">Additional</option><option value="failover">Failover</option><option value="private">Private</option></select></div>
  </form>
  {#snippet footer()}
    <button class="btn" onclick={() => (ipOpen = false)}>Cancel</button>
    <button class="btn primary" type="submit" form="ipf">Add address</button>
  {/snippet}
</Modal>

<style>
  .iph { display: flex; align-items: center; margin: 20px 0 10px; }
</style>
