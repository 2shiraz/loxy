<script lang="ts">
  import { api, unwrap, mutate } from '$lib/api/client';
  import { getLb } from '$lib/stores/lbctx';
  import { resource } from '$lib/stores/resource.svelte';
  import { confirm } from '$lib/stores/confirm.svelte';
  import { isIPv4 } from '$lib/util/validate';
  import CopyText from '$lib/components/CopyText.svelte';
  import Icon from '$lib/components/Icon.svelte';
  import Modal from '$lib/components/Modal.svelte';

  const ctx = getLb();
  const lb = $derived(ctx.lb!);
  const id = $derived(ctx.id);
  const networks = resource(async () => (await unwrap(api.GET('/networks'))).networks);
  const netName = (nid: number) => networks.data?.find((n) => n.id === nid);

  async function togglePublic() {
    const on = !lb.public_net.enabled;
    if (!on && lb.private_net.length === 0) return;
    const ok = await confirm({
      title: on ? 'Enable public interface?' : 'Disable public interface?',
      message: on ? 'Services become reachable on the public IPv4 and IPv6 addresses again.' : 'Services will only be reachable through attached private networks. Public clients are disconnected.',
      confirmLabel: on ? 'Enable' : 'Disable public interface',
      danger: !on
    });
    if (!ok) return;
    const r = on
      ? await mutate(api.POST('/load_balancers/{id}/actions/enable_public_interface', { params: { path: { id } } }), { success: 'Public interface enabled' })
      : await mutate(api.POST('/load_balancers/{id}/actions/disable_public_interface', { params: { path: { id } } }), { success: 'Public interface disabled' });
    if (r) ctx.set(r.load_balancer);
  }

  let attachOpen = $state(false);
  let netId = $state<number | ''>('');
  let ip = $state('');
  let errs = $state<Record<string, string>>({});
  const available = $derived((networks.data ?? []).filter((n) => !lb.private_net.some((p) => p.network_id === n.id)));

  async function attach() {
    errs = {};
    if (netId === '') { errs = { network_id: 'Choose a network' }; return; }
    if (!isIPv4(ip)) { errs = { ip: 'Enter an IPv4 address inside the network' }; return; }
    const r = await mutate(api.POST('/load_balancers/{id}/actions/attach_to_network', { params: { path: { id } }, body: { network_id: netId, ip } }), { success: 'Attached to network', onFieldErrors: (f) => (errs = f) });
    if (r) { ctx.set(r.load_balancer); attachOpen = false; }
  }
  async function detach(nid: number) {
    const n = netName(nid);
    const ok = await confirm({ title: 'Detach from network?', message: `The load balancer leaves ${n?.name ?? 'this network'}. Targets using private IPs through it must be removed first.`, confirmLabel: 'Detach', danger: true });
    if (!ok) return;
    const r = await mutate(api.POST('/load_balancers/{id}/actions/detach_from_network', { params: { path: { id } }, body: { network_id: nid } }), { success: 'Detached' });
    if (r) ctx.set(r.load_balancer);
  }
</script>

<div class="stack-lg">
  <section class="card" aria-labelledby="pub">
    <div class="card-head">
      <h2 id="pub">Public network</h2>
      <span class="badge {lb.public_net.enabled ? 'ok' : ''}"><Icon name={lb.public_net.enabled ? 'check' : 'minus'} size={11} />{lb.public_net.enabled ? 'Enabled' : 'Disabled'}</span>
      <span class="spacer"></span>
      <button class="btn sm {lb.public_net.enabled ? 'danger' : ''}" onclick={togglePublic} disabled={lb.public_net.enabled && lb.private_net.length === 0}
        title={lb.public_net.enabled && lb.private_net.length === 0 ? 'Attach a private network first' : undefined}>
        {lb.public_net.enabled ? 'Disable public interface' : 'Enable public interface'}
      </button>
    </div>
    <dl class="kv">
      <dt>IPv4</dt><dd><CopyText text={lb.public_net.ipv4} label="IPv4 address" /></dd>
      <dt>IPv6</dt><dd><CopyText text={lb.public_net.ipv6} label="IPv6 address" /></dd>
    </dl>
    {#if lb.public_net.enabled && lb.private_net.length === 0}<p class="muted xs" style:margin-top="10px">The public interface can only be disabled while at least one private network is attached.</p>{/if}
  </section>

  <section class="card" aria-labelledby="priv">
    <div class="card-head">
      <h2 id="priv">Private networks</h2>
      <span class="spacer"></span>
      <button class="btn sm" onclick={() => { netId = available[0]?.id ?? ''; ip = ''; errs = {}; attachOpen = true; }} disabled={!available.length}><Icon name="plus" size={13} />Attach to network</button>
    </div>
    {#if lb.private_net.length === 0}
      <p class="muted small">Not attached to any private network.{#if networks.data && !networks.data.length} <a href="/networks">Create a network</a> first.{/if}</p>
    {:else}
      <div class="table-wrap bare">
        <table class="tbl">
          <thead><tr><th>Network</th><th>CIDR</th><th>Private IP</th><th><span class="sr-only">Actions</span></th></tr></thead>
          <tbody>
            {#each lb.private_net as p (p.network_id)}
              {@const n = netName(p.network_id)}
              <tr><td>{n?.name ?? `#${p.network_id}`}</td><td class="mono small">{n?.cidr ?? '—'}</td><td><CopyText text={p.ip} /></td>
                <td class="actions"><button class="btn sm danger" onclick={() => detach(p.network_id)}>Detach</button></td></tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
  </section>
</div>

<Modal bind:open={attachOpen} title="Attach to network" width={440}>
  <form id="attach" class="stack" onsubmit={(e) => { e.preventDefault(); void attach(); }}>
    <div class="field">
      <label for="net">Network</label>
      <select id="net" class="select" bind:value={netId} aria-invalid={errs.network_id ? 'true' : undefined}>
        {#each available as n (n.id)}<option value={n.id}>{n.name} ({n.cidr})</option>{/each}
      </select>
      {#if errs.network_id}<span class="ferr">{errs.network_id}</span>{/if}
    </div>
    <div class="field">
      <label for="pip">Private IP</label>
      <input id="pip" class="input mono" bind:value={ip} placeholder="10.0.0.5" aria-invalid={errs.ip ? 'true' : undefined} />
      {#if errs.ip}<span class="ferr">{errs.ip}</span>{:else}<span class="hint">Must be a free address inside the network's range.</span>{/if}
    </div>
  </form>
  {#snippet footer()}
    <button class="btn" onclick={() => (attachOpen = false)}>Cancel</button>
    <button class="btn primary" type="submit" form="attach">Attach</button>
  {/snippet}
</Modal>
