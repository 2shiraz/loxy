<script lang="ts">
  import { api, unwrap } from '$lib/api/client';
  import { getFe } from '$lib/stores/ctx';
  import { resource } from '$lib/stores/resource.svelte';
  import { compact, bytes, num, dateTime, daysUntil } from '$lib/util/format';
  import StatBar from '$lib/components/StatBar.svelte';
  import StatCard from '$lib/components/StatCard.svelte';
  import HealthBadge from '$lib/components/HealthBadge.svelte';
  import HealthCount from '$lib/components/HealthCount.svelte';
  import LabelChips from '$lib/components/LabelChips.svelte';
  import Icon from '$lib/components/Icon.svelte';

  const ctx = getFe();
  const fe = $derived(ctx.item!);

  const bes = resource(async () => (await unwrap(api.GET('/backends'))).backends, { keys: () => ['backends'] });
  const certs = resource(async () => (fe.tls ? (await unwrap(api.GET('/certificates'))).certificates : []), { deps: () => fe.tls, keys: () => ['certificates'] });

  const byId = $derived(new Map((bes.data ?? []).map((b) => [b.id, b])));
  const usedIds = $derived([...new Set([fe.default_backend_id, ...fe.routes.map((r) => r.backend_id)].filter((x): x is number => x != null))]);
  const myCerts = $derived((certs.data ?? []).filter((c) => fe.certificates.includes(c.id)));
</script>

<div class="stack-lg">
  <StatBar>
    <StatCard label="Open connections" value={num(fe.stats.open_connections)} hint={fe.maxconn ? `limit ${num(fe.maxconn)}` : undefined} ratio={fe.maxconn ? fe.stats.open_connections / fe.maxconn : undefined} />
    {#if fe.mode === 'http'}<StatCard label="Requests / s" value={compact(fe.stats.requests_per_second)} />{/if}
    <StatCard label="Outbound" value={bytes(fe.stats.bytes_out_per_second, '/s')} />
    <StatCard label="Backend servers" value={fe.health.total ? `${fe.health.healthy}` : '—'} of={fe.health.total ? String(fe.health.total) : undefined} hint={fe.health.status === 'unknown' ? 'no checks yet' : fe.health.status} />
  </StatBar>

  {#if fe.health.messages.length}
    <div class="banner warn" role="status">
      <Icon name="alert" />
      <ul class="msgs">{#each fe.health.messages as m, i (i)}<li>{m}</li>{/each}</ul>
    </div>
  {/if}

  <div class="grid-2">
    <section class="card" aria-labelledby="ov-listen">
      <div class="card-head"><h2 id="ov-listen">Listening</h2></div>
      <dl class="kv">
        <dt>Binds</dt>
        <dd><span class="row-wrap">{#each fe.binds as b, i (i)}<span class="chip">{b.address.includes(':') ? `[${b.address}]` : b.address}:{b.port}</span>{/each}</span></dd>
        <dt>Mode</dt><dd>{fe.mode === 'tcp' ? 'TCP (layer 4)' : 'HTTP (layer 7)'}</dd>
        {#if fe.mode === 'http'}
          <dt>TLS</dt>
          <dd>{#if fe.tls}On{fe.http_redirect ? ' · HTTP redirects to HTTPS' : ''}{:else}<span class="muted">Off</span>{/if}</dd>
          <dt>Forwarded headers</dt><dd>{fe.forwarded_headers ? 'On' : 'Off'}</dd>
        {/if}
        <dt>Client timeout</dt><dd>{fe.timeout_client_s} s</dd>
        <dt>Max connections</dt><dd>{fe.maxconn ? num(fe.maxconn) : 'Unlimited'}</dd>
        <dt>Created</dt><dd>{dateTime(fe.created_at)}</dd>
        {#if Object.keys(fe.labels).length}<dt>Labels</dt><dd><LabelChips labels={fe.labels} /></dd>{/if}
      </dl>
    </section>

    {#if fe.tls}
      <section class="card" aria-labelledby="ov-certs">
        <div class="card-head"><h2 id="ov-certs">Certificates</h2><span class="spacer"></span><a class="small" href="/certificates">Manage</a></div>
        {#if !certs.data}
          <p class="muted small">Loading…</p>
        {:else if !myCerts.length}
          <p class="err-text small">No certificate attached. HTTPS clients will fail the handshake.</p>
        {:else}
          <ul class="list">
            {#each myCerts as c (c.id)}
              {@const d = daysUntil(c.not_after)}
              <li>
                <Icon name="shield" size={14} />
                <span class="grow"><b>{c.name}</b> <span class="muted xs mono">{c.domains.slice(0, 3).join(', ')}{c.domains.length > 3 ? '…' : ''}</span></span>
                {#if d != null && d < 14 && c.status === 'issued'}<span class="badge warn">expires in {d} d</span>{/if}
                <HealthBadge status={c.status} />
              </li>
            {/each}
          </ul>
        {/if}
      </section>
    {/if}
  </div>

  <section class="card flush" aria-labelledby="ov-route">
    <div class="card-head pad"><h2 id="ov-route">Routing</h2><span class="spacer"></span><a class="small" href="/frontends/{fe.id}/edit">Edit routing</a></div>
    <div class="table-wrap bare">
      <table class="tbl">
        <thead><tr><th class="right" style:width="40px">#</th><th>Match</th><th>Backend</th><th>Servers</th></tr></thead>
        <tbody>
          {#each fe.routes as r, i (r.id ?? i)}
            {@const b = byId.get(r.backend_id)}
            <tr>
              <td class="right muted">{i + 1}</td>
              <td class="mono small">
                {#if r.hosts?.length}<span>host {r.hosts.join(', ')}</span>{/if}
                {#if r.hosts?.length && r.path_prefix}<span class="muted"> and </span>{/if}
                {#if r.path_prefix}<span>path {r.path_prefix}*</span>{/if}
              </td>
              <td><a href="/backends/{r.backend_id}">{b?.name ?? `#${r.backend_id}`}</a></td>
              <td>{#if b}<HealthCount healthy={b.health.healthy} total={b.health.total} />{/if}</td>
            </tr>
          {/each}
          <tr>
            <td class="right muted">{fe.routes.length ? '—' : ''}</td>
            <td class="small muted">{fe.routes.length ? 'Everything else' : 'All traffic'}</td>
            <td>
              {#if fe.default_backend_id != null}
                <a href="/backends/{fe.default_backend_id}">{byId.get(fe.default_backend_id)?.name ?? `#${fe.default_backend_id}`}</a> <span class="muted xs">default</span>
              {:else}<span class="muted small">No default — 503</span>{/if}
            </td>
            <td>{#if fe.default_backend_id != null && byId.get(fe.default_backend_id)}{@const b = byId.get(fe.default_backend_id)!}<HealthCount healthy={b.health.healthy} total={b.health.total} />{/if}</td>
          </tr>
        </tbody>
      </table>
    </div>
  </section>

  {#if usedIds.length}
    <section aria-labelledby="ov-be">
      <h2 id="ov-be" class="section-title">Backends in use</h2>
      <div class="grid-3">
        {#each usedIds as bid (bid)}
          {@const b = byId.get(bid)}
          <a class="card becard" href="/backends/{bid}">
            <span class="row"><b>{b?.name ?? `#${bid}`}</b><span class="spacer"></span>{#if b}<HealthBadge status={b.health.status} />{/if}</span>
            {#if b}
              <span class="small muted">{b.servers_count} server{b.servers_count === 1 ? '' : 's'} · {b.health.healthy}/{b.health.total} healthy · {num(b.stats.sessions)} sessions{b.stats.queue ? ` · ${num(b.stats.queue)} queued` : ''}</span>
            {/if}
          </a>
        {/each}
      </div>
    </section>
  {/if}
</div>

<style>
  .msgs { margin: 0; padding-left: 16px; }
  .banner :global(.icon) { color: var(--warn); margin-top: 2px; }
  .list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 8px; }
  .list li { display: flex; align-items: center; gap: 8px; }
  .list :global(.icon) { color: var(--muted); }
  .grow { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .pad { padding: 12px 16px 0; margin-bottom: 8px; }
  .becard { display: flex; flex-direction: column; gap: 6px; text-decoration: none; transition: border-color 120ms ease; }
  .becard:hover { border-color: var(--border-strong); }
</style>
