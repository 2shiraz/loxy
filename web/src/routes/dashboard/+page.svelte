<script lang="ts">
  import { api, unwrap, mutate } from '$lib/api/client';
  import { resource } from '$lib/stores/resource.svelte';
  import { num, compact, bytes, duration, relTime, dateTime } from '$lib/util/format';
  import Icon from '$lib/components/Icon.svelte';
  import StatBar from '$lib/components/StatBar.svelte';
  import StatCard from '$lib/components/StatCard.svelte';
  import HealthBadge from '$lib/components/HealthBadge.svelte';
  import Skeleton from '$lib/components/Skeleton.svelte';
  import ErrorBox from '$lib/components/ErrorBox.svelte';
  import EmptyState from '$lib/components/EmptyState.svelte';

  const ov = resource(() => unwrap(api.GET('/overview')), { keys: () => ['overview', 'nodes'] });
  const events = resource(async () => (await unwrap(api.GET('/events/history', { params: { query: { limit: 10 } } }))).events, { keys: () => ['events'] });
  const fes = resource(async () => (await unwrap(api.GET('/frontends'))).frontends, { keys: () => ['frontends'] });
  const failing = $derived((fes.data ?? []).filter((f) => f.status === 'apply_error'));

  let retrying = $state(false);
  async function retry() {
    retrying = true;
    const r = await mutate(api.POST('/apply/retry'), { success: 'Retrying apply…' });
    retrying = false;
    if (r !== undefined) { void ov.reload(); void fes.reload(); }
  }
  const sevIcon = (s: string) => (s === 'error' ? 'x' : s === 'warning' ? 'alert' : 'info');
</script>

<svelte:head><title>Dashboard · Loxy</title></svelte:head>
<div class="page-head"><h1>Dashboard</h1></div>

{#if ov.error && !ov.data}
  <ErrorBox message={ov.error} onretry={ov.reload} />
{:else if !ov.data}
  <div class="stack-lg"><Skeleton card rows={2} height={28} /><Skeleton card rows={4} /></div>
{:else}
  {@const o = ov.data}
  {@const n = o.node}
  <div class="stack-lg">
    {#if o.frontends.apply_errors > 0 || failing.length}
      <div class="banner err" role="alert">
        <Icon name="alert" />
        <div class="spacer">
          <b>{o.frontends.apply_errors || failing.length} frontend{(o.frontends.apply_errors || failing.length) === 1 ? '' : 's'} could not be applied.</b>
          HAProxy keeps running the last good configuration.
          {#if failing.length}<span class="names">{#each failing as f, i (f.id)}{i ? ', ' : ''}<a href="/frontends/{f.id}">{f.name}</a>{/each}</span>{/if}
        </div>
        <button class="btn sm" onclick={retry} disabled={retrying}><Icon name="refresh" size={13} />{retrying ? 'Retrying…' : 'Retry'}</button>
      </div>
    {/if}

    <div class="cards">
      <a class="card sc" href="/frontends">
        <span class="lbl">Frontends</span>
        <span class="big">{num(o.frontends.total)}</span>
        <span class="sub small">{#if o.frontends.apply_errors}<span class="err-text">{o.frontends.apply_errors} apply error{o.frontends.apply_errors === 1 ? '' : 's'}</span>{:else}<span class="muted">all applied</span>{/if}</span>
      </a>
      <a class="card sc" href="/backends">
        <span class="lbl">Backends</span>
        <span class="big">{num(o.backends.total)}</span>
        <span class="sub small">
          <span class="ok-text">{o.backends.healthy} healthy</span>
          {#if o.backends.degraded}<span class="warn-text"> · {o.backends.degraded} degraded</span>{/if}
          {#if o.backends.unhealthy}<span class="err-text"> · {o.backends.unhealthy} unhealthy</span>{/if}
        </span>
      </a>
      <a class="card sc" href="/backends">
        <span class="lbl">Servers</span>
        <span class="big">{num(o.servers.healthy)}<span class="of"> / {num(o.servers.total)}</span></span>
        <span class="sub small">{#if o.servers.total && o.servers.healthy < o.servers.total}<span class="warn-text">{o.servers.total - o.servers.healthy} not healthy</span>{:else if o.servers.total}<span class="muted">all healthy</span>{:else}<span class="muted">none yet</span>{/if}</span>
      </a>
      <a class="card sc" href="/certificates">
        <span class="lbl">Certificates</span>
        <span class="big">{num(o.certificates.total)}</span>
        <span class="sub small">
          {#if !o.certificates.expiring_soon && !o.certificates.failed}<span class="muted">none expiring soon</span>{/if}
          {#if o.certificates.expiring_soon}<span class="warn-text">{o.certificates.expiring_soon} expiring soon</span>{/if}
          {#if o.certificates.failed}<span class="err-text">{o.certificates.expiring_soon ? ' · ' : ''}{o.certificates.failed} failed</span>{/if}
        </span>
      </a>
    </div>

    <section aria-labelledby="tr-h">
      <h2 id="tr-h" class="section-title">Current traffic</h2>
      {#if o.traffic}
        <StatBar>
          <StatCard label="Open connections" value={num(o.traffic.open_connections)} />
          <StatCard label="Requests / s" value={compact(o.traffic.requests_per_second)} />
          <StatCard label="Inbound" value={bytes(o.traffic.bytes_in_per_second, '/s')} />
          <StatCard label="Outbound" value={bytes(o.traffic.bytes_out_per_second, '/s')} />
        </StatBar>
      {:else}
        <p class="muted small">No traffic data yet.</p>
      {/if}
    </section>

    <div class="grid-2">
      <section class="card" aria-labelledby="node-h">
        <div class="card-head">
          <Icon name="node" />
          <h2 id="node-h">HAProxy · {n.name}</h2>
          <HealthBadge status={n.status === 'online' ? 'healthy' : n.status} label={n.status} />
          <span class="spacer"></span>
          <a class="small" href="/system">System</a>
        </div>
        <dl class="kv">
          <dt>Version</dt><dd class="mono small">{n.haproxy_running_version ?? n.haproxy_version ?? '—'}</dd>
          <dt>Uptime</dt><dd>{duration(n.uptime_s)}</dd>
          <dt>Reloads</dt><dd>{num(n.reloads)}</dd>
          <dt>Config</dt>
          <dd class="mono small">
            applied v{n.applied_version} · desired v{n.desired_version}
            {#if n.applied_version === n.desired_version}<span class="ok-text"> in sync</span>{:else}<span class="warn-text"> applying</span>{/if}
          </dd>
        </dl>
      </section>

      <section class="card flush" aria-labelledby="ev-h">
        <div class="card-head pad"><h2 id="ev-h">Recent events</h2><span class="spacer"></span><a class="small" href="/activity">All activity</a></div>
        {#if events.error && !events.data}<div class="pad"><ErrorBox message={events.error} onretry={events.reload} /></div>
        {:else if !events.data}<div class="pad"><Skeleton rows={5} /></div>
        {:else if !events.data.length}<EmptyState icon="activity" title="Nothing yet" text="Health changes, reloads and certificate events show up here." />
        {:else}
          <ol class="feed">
            {#each events.data as e (e.id)}
              <li>
                <span class="sev {e.severity}"><Icon name={sevIcon(e.severity)} size={13} label={e.severity} /></span>
                <span class="msg">
                  {#if e.frontend_id}<a href="/frontends/{e.frontend_id}">{e.message}</a>
                  {:else if e.backend_id}<a href="/backends/{e.backend_id}">{e.message}</a>
                  {:else}{e.message}{/if}
                </span>
                <time class="xs muted" datetime={new Date(e.ts).toISOString()} title={dateTime(e.ts)}>{relTime(e.ts)}</time>
              </li>
            {/each}
          </ol>
        {/if}
      </section>
    </div>
  </div>
{/if}

<style>
  .names { display: inline; margin-left: 4px; }
  .banner :global(.icon) { color: var(--err); margin-top: 2px; }
  .cards { display: grid; grid-template-columns: repeat(auto-fit, minmax(200px, 1fr)); gap: 12px; }
  .sc { display: flex; flex-direction: column; gap: 2px; text-decoration: none; padding: 14px 16px; transition: border-color 120ms ease; }
  .sc:hover { border-color: var(--border-strong); }
  .lbl { font-size: var(--fs-sm); color: var(--muted); }
  .big { font-size: var(--fs-2xl); font-weight: 600; letter-spacing: -0.01em; font-variant-numeric: tabular-nums; }
  .of { font-size: var(--fs-md); color: var(--muted); font-weight: 500; }
  .sub { min-height: 1.5em; }
  .pad { padding: 12px 16px 0; margin-bottom: 4px; }
  .feed { list-style: none; margin: 0; padding: 0; }
  .feed li { display: flex; align-items: baseline; gap: 10px; padding: 8px 16px; border-top: 1px solid var(--border); }
  .msg { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .msg a { text-decoration: none; }
  .msg a:hover { text-decoration: underline; }
  .sev { color: var(--info); display: inline-flex; align-self: center; }
  .sev.warning { color: var(--warn); }
  .sev.error { color: var(--err); }
  time { flex: none; }
</style>
