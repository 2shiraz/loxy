<script lang="ts">
  import { api, unwrap, mutate, getText, type Node, type Revision } from '$lib/api/client';
  import { resource } from '$lib/stores/resource.svelte';
  import { confirm } from '$lib/stores/confirm.svelte';
  import { duration, num, dateTime, relTime } from '$lib/util/format';
  import { isIPv4, isIPv6 } from '$lib/util/validate';
  import Icon from '$lib/components/Icon.svelte';
  import Modal from '$lib/components/Modal.svelte';
  import HealthBadge from '$lib/components/HealthBadge.svelte';
  import CopyText from '$lib/components/CopyText.svelte';
  import CodeView from '$lib/components/CodeView.svelte';
  import Skeleton from '$lib/components/Skeleton.svelte';
  import EmptyState from '$lib/components/EmptyState.svelte';
  import ErrorBox from '$lib/components/ErrorBox.svelte';

  const nodes = resource(async () => (await unwrap(api.GET('/nodes'))).nodes, { keys: () => ['nodes'] });
  const nodeId = $derived(nodes.data?.[0]?.id ?? 1);
  const revs = resource(async () => (await unwrap(api.GET('/nodes/{id}/revisions', { params: { path: { id: nodeId } } }))).revisions, { deps: () => nodeId, keys: () => ['nodes'] });

  // Node addresses
  let ipOpen = $state(false);
  let ipNode = $state<Node | null>(null);
  let addr = $state('');
  let ipLabel = $state('');
  let ipErr = $state('');
  let ipBusy = $state(false);
  function openIp(n: Node) { ipNode = n; addr = ''; ipLabel = ''; ipErr = ''; ipOpen = true; }
  async function addIp() {
    if (!ipNode) return;
    ipErr = '';
    const a = addr.trim();
    if (!isIPv4(a) && !isIPv6(a)) { ipErr = 'Enter an IPv4 or IPv6 address'; return; }
    ipBusy = true;
    const r = await mutate(api.POST('/nodes/{id}/ips', { params: { path: { id: ipNode.id } }, body: { address: a, label: ipLabel.trim() || undefined } }), { success: 'Address added', onFieldErrors: (f) => (ipErr = Object.values(f)[0] ?? '') });
    ipBusy = false;
    if (r) { ipOpen = false; void nodes.reload(); }
  }
  async function delIp(n: Node, ipid: number, a: string) {
    const ok = await confirm({ title: `Remove ${a}?`, message: 'Frontends bound to this address keep their bind, but it is no longer suggested. Remove those binds first if the address is going away.', confirmLabel: 'Remove address', danger: true });
    if (!ok) return;
    const r = await mutate(api.DELETE('/nodes/{id}/ips/{ipid}', { params: { path: { id: n.id, ipid } } }), { success: 'Address removed' });
    if (r !== undefined) void nodes.reload();
  }

  // Revisions
  let diffOpen = $state(false);
  let diffRev = $state<Revision | null>(null);
  let diffText = $state('');
  let diffErr = $state('');
  async function showDiff(r: Revision) {
    diffRev = r; diffText = ''; diffErr = ''; diffOpen = true;
    try { diffText = (await getText(`/nodes/${nodeId}/revisions/${r.revision}/diff`)) || '(no changes)'; } catch (e) { diffErr = (e as Error).message; }
  }
  async function rollback(r: Revision) {
    const ok = await confirm({ title: `Roll back to revision ${r.revision}?`, message: 'The desired state stored with this revision is validated and applied again. Changes made since are reverted.', confirmLabel: 'Roll back', danger: true });
    if (!ok) return;
    const res = await mutate(api.POST('/nodes/{id}/revisions/{rev}/actions/rollback', { params: { path: { id: nodeId, rev: r.revision } } }), { success: `Rolling back to revision ${r.revision}` });
    if (res !== undefined) { diffOpen = false; void revs.reload(); }
  }
</script>

<svelte:head><title>System · Loxy</title></svelte:head>
<div class="page-head"><h1>System</h1></div>

<div class="stack-lg">
  {#if nodes.error && !nodes.data}<ErrorBox message={nodes.error} onretry={nodes.reload} />
  {:else if !nodes.data}<Skeleton card rows={6} />
  {:else}
    {#each nodes.data as n (n.id)}
      <section class="card" aria-labelledby="n{n.id}">
        <div class="card-head">
          <Icon name="node" />
          <h2 id="n{n.id}">{n.name}</h2>
          <HealthBadge status={n.status === 'online' ? 'healthy' : n.status} label={n.status} />
          {#if n.desired_version !== n.applied_version}<span class="badge warn"><Icon name="refresh" size={11} />Applying v{n.desired_version}</span>{/if}
        </div>
        <div class="grid-2">
          <dl class="kv">
            <dt>HAProxy</dt>
            <dd class="mono small">{n.haproxy_version ?? '—'}{#if n.haproxy_running_version && n.haproxy_running_version !== n.haproxy_version} <span class="warn-text">(running {n.haproxy_running_version})</span>{/if}</dd>
            <dt>Loxy</dt><dd class="mono small">{n.loxy_version ?? '—'}</dd>
            <dt>Uptime</dt><dd>{duration(n.uptime_s)}</dd>
            <dt>Reloads</dt><dd>{num(n.reloads)}</dd>
          </dl>
          <dl class="kv">
            <dt>Config version</dt>
            <dd class="mono small">applied {n.applied_version} · desired {n.desired_version}{#if n.applied_version === n.desired_version} <span class="ok-text">in sync</span>{/if}</dd>
            <dt>Features</dt><dd><span class="row-wrap">{#each n.features ?? [] as f (f)}<span class="chip">{f}</span>{:else}<span class="muted">—</span>{/each}</span></dd>
          </dl>
        </div>

        <div class="iph">
          <h3>Node addresses</h3>
          <span class="muted small">Suggested when you add binds to a frontend.</span>
          <span class="spacer"></span>
          <button class="btn sm" onclick={() => openIp(n)}><Icon name="plus" size={13} />Add address</button>
        </div>
        {#if n.ips.length}
          <div class="table-wrap">
            <table class="tbl">
              <thead><tr><th>Address</th><th>Family</th><th>Label</th><th><span class="sr-only">Actions</span></th></tr></thead>
              <tbody>
                {#each n.ips as ip (ip.id)}
                  <tr>
                    <td><CopyText text={ip.address} /></td>
                    <td class="small">IPv{ip.family}</td>
                    <td class="small">{ip.label || '—'}</td>
                    <td class="actions"><button class="btn ghost icon sm" aria-label="Remove {ip.address}" onclick={() => delIp(n, ip.id, ip.address)}><Icon name="trash" size={14} /></button></td>
                  </tr>
                {/each}
              </tbody>
            </table>
          </div>
        {:else}<p class="muted small">No addresses yet. Frontends can still bind to “*” (all addresses).</p>{/if}
      </section>
    {/each}
  {/if}

  <section id="revisions" aria-labelledby="rev-h">
    <div class="iph top"><h2 id="rev-h" class="section-title">Config revisions</h2></div>
    <p class="section-intro">Every configuration HAProxy accepted (or rejected) is kept. Inspect the diff or roll back to a known-good revision.</p>
    {#if revs.error && !revs.data}<ErrorBox message={revs.error} onretry={revs.reload} />
    {:else if !revs.data}<Skeleton card rows={6} />
    {:else if !revs.data.length}<div class="card"><EmptyState icon="activity" title="No revisions yet" text="Revisions appear after the first configuration is applied." /></div>
    {:else}
      <div class="table-wrap">
        <table class="tbl">
          <thead><tr><th class="right">Revision</th><th>Kind</th><th>Result</th><th class="right">Duration</th><th>SHA-256</th><th>Created</th><th><span class="sr-only">Actions</span></th></tr></thead>
          <tbody>
            {#each revs.data as r (r.id)}
              <tr>
                <td class="right mono">{r.revision}{#if r.pinned} <Icon name="tag" size={12} label="Pinned" />{/if}</td>
                <td class="small">{r.kind}</td>
                <td><HealthBadge status={r.result === 'ok' ? 'ok' : 'failed'} /></td>
                <td class="right small">{r.duration_ms} ms</td>
                <td class="mono xs muted">{r.sha256.slice(0, 12)}</td>
                <td class="small" title={dateTime(r.created_at)}>{relTime(r.created_at)}</td>
                <td class="actions"><span class="row"><button class="btn sm" onclick={() => showDiff(r)}>View diff</button><button class="btn sm ghost" onclick={() => rollback(r)} disabled={r.result !== 'ok'} title={r.result !== 'ok' ? 'Only revisions HAProxy accepted can be restored' : undefined}>Roll back</button></span></td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
  </section>
</div>

<Modal bind:open={ipOpen} title="Add node address" width={420}>
  <form id="ipf" class="stack" onsubmit={(e) => { e.preventDefault(); void addIp(); }} novalidate>
    <div class="field">
      <label for="ia">Address</label>
      <input id="ia" class="input mono" bind:value={addr} placeholder="203.0.113.20" spellcheck="false" autocomplete="off" aria-invalid={ipErr ? 'true' : undefined} aria-describedby="ia-e" />
      {#if ipErr}<span class="ferr" id="ia-e">{ipErr}</span>{:else}<span class="hint" id="ia-e">An address configured on this host's interfaces.</span>{/if}
    </div>
    <div class="field">
      <label for="il">Label <span class="muted">(optional)</span></label>
      <input id="il" class="input" bind:value={ipLabel} placeholder="public, internal…" autocomplete="off" />
    </div>
  </form>
  {#snippet footer()}
    <button class="btn" onclick={() => (ipOpen = false)}>Cancel</button>
    <button class="btn primary" type="submit" form="ipf" disabled={ipBusy}>{ipBusy ? 'Adding…' : 'Add address'}</button>
  {/snippet}
</Modal>

<Modal bind:open={diffOpen} title="Revision {diffRev?.revision ?? ''}" width={900}>
  {#if diffErr}<ErrorBox message={diffErr} />
  {:else if !diffText}<Skeleton rows={10} />
  {:else}<CodeView text={diffText} diff label="Revision diff" />{/if}
  {#if diffRev?.haproxy_output}<h3 style:margin="14px 0 6px">HAProxy output</h3><CodeView text={diffRev.haproxy_output} maxHeight={200} label="HAProxy output" />{/if}
  {#snippet footer()}
    <button class="btn" onclick={() => (diffOpen = false)}>Close</button>
    {#if diffRev && diffRev.result === 'ok'}<button class="btn danger" onclick={() => rollback(diffRev!)}>Roll back to this revision</button>{/if}
  {/snippet}
</Modal>

<style>
  .iph { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; margin: 20px 0 10px; }
  .iph.top { margin-top: 0; }
</style>
