<script lang="ts">
  import { api, unwrap, mutate } from '$lib/api/client';
  import { resource } from '$lib/stores/resource.svelte';
  import { live, withLive, applying } from '$lib/stores/live.svelte';
  import { num, compact, rate, bytes, duration, relTime, dateTime, projectStatus, endpointLabel, eventText } from '$lib/util/format';
  import Icon from '$lib/components/Icon.svelte';
  import HealthBadge from '$lib/components/HealthBadge.svelte';
  import HealthCount from '$lib/components/HealthCount.svelte';
  import StatusDot from '$lib/components/StatusDot.svelte';
  import Skeleton from '$lib/components/Skeleton.svelte';
  import ErrorBox from '$lib/components/ErrorBox.svelte';
  import EmptyState from '$lib/components/EmptyState.svelte';
  import LiveNumber from '$lib/components/LiveNumber.svelte';
  import Sparkline from '$lib/components/Sparkline.svelte';

  const ov = resource(() => unwrap(api.GET('/overview')), { keys: () => ['overview', 'nodes'] });
  const events = resource(async () => (await unwrap(api.GET('/events/history', { params: { query: { limit: 8 } } }))).events, { keys: () => ['events'] });
  const projects = resource(async () => (await unwrap(api.GET('/projects'))).projects, { keys: () => ['projects'] });

  const plist = $derived((projects.data ?? []).map(withLive));
  const failing = $derived(plist.filter((p) => p.status === 'error'));
  const attention = $derived(plist.filter((p) => p.status !== 'error' && (p.health.status === 'unhealthy' || p.health.status === 'degraded')));
  const busiest = $derived([...plist].sort((a, b) => b.stats.requests_per_second - a.stats.requests_per_second || a.name.localeCompare(b.name)).slice(0, 6));

  // Project health counts follow the live tick; the overview fetch is the fallback.
  const counts = $derived.by(() => {
    const t = live.tick?.projects;
    const o = ov.data?.projects;
    if (!t) return o ? { total: o.total, healthy: o.healthy, degraded: o.degraded, unhealthy: o.unhealthy, errors: o.apply_errors } : null;
    return {
      total: t.length,
      healthy: t.filter((p) => p.health.status === 'healthy').length,
      degraded: t.filter((p) => p.health.status === 'degraded').length,
      unhealthy: t.filter((p) => p.health.status === 'unhealthy').length,
      errors: t.filter((p) => p.status === 'error').length
    };
  });
  const traffic = $derived(live.tick?.traffic ?? ov.data?.traffic ?? null);
  const nodeV = $derived(live.tick?.node ?? (ov.data ? { applied_version: ov.data.node.applied_version, desired_version: ov.data.node.desired_version, status: ov.data.node.status } : null));

  let retrying = $state(false);
  async function retry() {
    retrying = true;
    const r = await mutate(api.POST('/apply/retry'), { success: 'Retrying apply…' });
    retrying = false;
    if (r !== undefined) { void ov.reload(); void projects.reload(); }
  }
  const sevIcon = (s: string) => (s === 'error' ? 'x' : s === 'warning' ? 'alert' : 'info');
  const one = rate;
  const primaryEp = (p: (typeof plist)[number]) => [...p.endpoints].sort((a, b) => (a.kind === b.kind ? 0 : a.kind === 'domain' ? -1 : 1))[0];
</script>

<svelte:head><title>Dashboard · Loxy</title></svelte:head>
<div class="page-head">
  <h1>Dashboard</h1>
  <a class="btn primary" href="/projects/new"><Icon name="plus" size={15} />Create project</a>
</div>

{#if ov.error && !ov.data}
  <ErrorBox message={ov.error} onretry={ov.reload} />
{:else if !ov.data}
  <div class="stack-lg"><Skeleton card rows={3} height={28} /><Skeleton card rows={5} /></div>
{:else}
  {@const o = ov.data}
  {@const n = o.node}
  <div class="dash">
    {#if (counts?.errors ?? 0) > 0 || failing.length}
      <div class="banner err" role="alert">
        <Icon name="alert" />
        <div class="spacer">
          <b>{counts?.errors || failing.length} project{(counts?.errors || failing.length) === 1 ? '' : 's'} could not be applied.</b>
          HAProxy keeps running the last good configuration.
          {#if failing.length}<span class="names">{#each failing as f, i (f.id)}{i ? ', ' : ''}<a href="/projects/{f.id}/overview">{f.name}</a>{/each}</span>{/if}
        </div>
        <button class="btn sm" onclick={retry} disabled={retrying}><Icon name="refresh" size={13} />{retrying ? 'Retrying…' : 'Retry'}</button>
      </div>
    {/if}

    <section class="card now" aria-labelledby="now-h">
      <div class="now-main">
        <h2 id="now-h">Requests per second, all projects</h2>
        <div class="big"><LiveNumber value={traffic?.requests_per_second ?? null} format={one} /></div>
        <div class="spark"><Sparkline values={live.trafficRps} height={64} min={3} label="Requests per second over the last 3 minutes" /></div>
        <p class="xs muted">Last 3 minutes, updated every 2 seconds.</p>
      </div>
      <dl class="now-side">
        <div><dt>Open connections</dt><dd><LiveNumber value={traffic?.open_connections ?? null} format={compact} /></dd></div>
        <div><dt>Inbound</dt><dd><LiveNumber value={traffic?.bytes_in_per_second ?? null} format={(v) => bytes(v, '/s')} /></dd></div>
        <div><dt>Outbound</dt><dd><LiveNumber value={traffic?.bytes_out_per_second ?? null} format={(v) => bytes(v, '/s')} /></dd></div>
      </dl>
    </section>

    <div class="cols">
      <section class="card flush plist" aria-labelledby="pl-h">
        <div class="card-head pad"><h2 id="pl-h">Projects</h2><span class="spacer"></span><a class="link small" href="/projects">All projects</a></div>
        {#if !projects.data}<div class="pad2"><Skeleton rows={4} /></div>
        {:else if !plist.length}
          <EmptyState icon="route" title="No projects yet" text="A project is one load balancer: an address people connect to and the servers behind it.">
            <a class="btn primary sm" href="/projects/new"><Icon name="plus" size={13} />Create project</a>
          </EmptyState>
        {:else}
          <ul class="prow">
            {#each busiest as p (p.id)}
              {@const ep = primaryEp(p)}
              <li>
                <a href="/projects/{p.id}/overview">
                  <StatusDot status={projectStatus(p)} />
                  <span class="pn">
                    <b>{p.name}</b>
                    <span class="mono xs muted">{ep ? endpointLabel(ep) : 'no address yet'}</span>
                  </span>
                  <HealthCount healthy={p.health.healthy} total={p.health.total} />
                  <span class="ps"><Sparkline values={live.rps[p.id] ?? []} height={22} /></span>
                  <span class="pr"><LiveNumber value={p.stats.requests_per_second} format={one} /><span class="xs muted unit">req/s</span></span>
                </a>
              </li>
            {/each}
          </ul>
        {/if}
      </section>

      <div class="side">
        <section class="card" aria-labelledby="hs-h">
          <div class="card-head"><h2 id="hs-h">Health</h2></div>
          <dl class="hs">
            <div>
              <dt><a href="/projects">Projects</a></dt>
              <dd>
                {#if !counts?.total}<span class="muted">none yet</span>
                {:else}
                  <span class="ok-text"><b>{counts.healthy}</b> healthy</span>
                  {#if counts.degraded}<span class="warn-text"><b>{counts.degraded}</b> degraded</span>{/if}
                  {#if counts.unhealthy}<span class="err-text"><b>{counts.unhealthy}</b> unhealthy</span>{/if}
                {/if}
              </dd>
            </div>
            <div>
              <dt>Targets</dt>
              <dd>
                {#if !o.targets.total}<span class="muted">none yet</span>
                {:else}<span class={o.targets.healthy < o.targets.total ? 'warn-text' : 'ok-text'}><b>{num(o.targets.healthy)}</b> of {num(o.targets.total)} healthy</span>{/if}
              </dd>
            </div>
            <div>
              <dt><a href="/certificates">Certificates</a></dt>
              <dd>
                {#if !o.certificates.total}<span class="muted">none yet</span>
                {:else if !o.certificates.expiring_soon && !o.certificates.failed}<span class="ok-text"><b>{o.certificates.total}</b> valid</span>{/if}
                {#if o.certificates.expiring_soon}<span class="warn-text"><b>{o.certificates.expiring_soon}</b> expiring soon</span>{/if}
                {#if o.certificates.failed}<span class="err-text"><b>{o.certificates.failed}</b> failed</span>{/if}
              </dd>
            </div>
          </dl>
          {#if attention.length}
            <ul class="att" aria-label="Needs attention">
              {#each attention.slice(0, 4) as p (p.id)}
                <li><HealthBadge status={p.health.status} /><a href="/projects/{p.id}/targets">{p.name}</a></li>
              {/each}
            </ul>
          {/if}
        </section>

        <section class="card" aria-labelledby="node-h">
          <div class="card-head">
            <h2 id="node-h">HAProxy</h2>
            <HealthBadge status={(nodeV?.status ?? n.status) === 'online' ? 'healthy' : (nodeV?.status ?? n.status)} label={nodeV?.status ?? n.status} />
            <span class="spacer"></span>
            <a class="link small" href="/system">System</a>
          </div>
          <dl class="kv">
            <dt>Version</dt><dd class="mono small">{n.haproxy_running_version ?? n.haproxy_version ?? '—'}</dd>
            <dt>Uptime</dt><dd>{duration(n.uptime_s)}</dd>
            <dt>Config</dt>
            <dd>
              {#if applying()}<span class="warn-text">Applying v{nodeV?.desired_version}…</span>
              {:else}<span class="ok-text">Up to date</span> <span class="muted small mono">v{nodeV?.applied_version ?? n.applied_version}</span>{/if}
            </dd>
          </dl>
        </section>
      </div>
    </div>

    <section class="card flush" aria-labelledby="ev-h">
      <div class="card-head pad"><h2 id="ev-h">Recent events</h2><span class="spacer"></span><a class="link small" href="/activity">All activity</a></div>
      {#if events.error && !events.data}<div class="pad2"><ErrorBox message={events.error} onretry={events.reload} /></div>
      {:else if !events.data}<div class="pad2"><Skeleton rows={5} /></div>
      {:else if !events.data.length}<EmptyState icon="activity" title="Quiet so far" text="Health changes, reloads and certificate events show up here." />
      {:else}
        <ol class="feed">
          {#each events.data as e (e.id)}
            <li>
              <span class="sev {e.severity}"><Icon name={sevIcon(e.severity)} size={14} label={e.severity} /></span>
              <span class="msg">
                {#if e.project_id}<a href="/projects/{e.project_id}/overview" title={e.message}>{eventText(e.message)}</a>
                {:else}<span title={e.message}>{eventText(e.message)}</span>{/if}
              </span>
              <time class="xs muted" datetime={new Date(e.ts).toISOString()} title={dateTime(e.ts)}>{relTime(e.ts)}</time>
            </li>
          {/each}
        </ol>
      {/if}
    </section>
  </div>
{/if}

<style>
  .dash { display: flex; flex-direction: column; gap: 16px; }
  .names { display: inline; margin-left: 4px; }
  .banner :global(.icon) { color: var(--err); margin-top: 2px; }

  .now { display: grid; grid-template-columns: minmax(0, 1fr) auto; gap: 16px 40px; padding: 22px 24px; }
  .now-main { min-width: 0; display: flex; flex-direction: column; gap: 4px; }
  .now h2 { font-size: var(--fs-md); font-weight: 600; color: var(--text-2); }
  .big { font-size: 48px; font-weight: 650; letter-spacing: -0.03em; line-height: 1.05; }
  .spark { margin: 8px 0 4px; }
  .now-side { margin: 0; display: flex; flex-direction: column; gap: 16px; justify-content: center; min-width: 180px; padding-left: 28px; border-left: 1px solid var(--border); }
  .now-side dt { font-size: var(--fs-xs); color: var(--muted); }
  .now-side dd { margin: 2px 0 0; font-size: var(--fs-xl); font-weight: 600; white-space: nowrap; }

  .cols { display: grid; grid-template-columns: minmax(0, 1.7fr) minmax(280px, 1fr); gap: 16px; align-items: start; }
  .side { display: flex; flex-direction: column; gap: 16px; min-width: 0; }
  .pad { padding: 16px 20px 0; margin-bottom: 6px; }
  .pad2 { padding: 12px 20px; }

  .prow { list-style: none; margin: 0; padding: 0; }
  .prow a { display: grid; grid-template-columns: auto minmax(0, 1fr) auto 80px 92px; align-items: center; gap: 14px; padding: 12px 20px; border-top: 1px solid var(--border); text-decoration: none; transition: background-color 120ms ease; }
  .prow a:hover { background: color-mix(in srgb, var(--surface-2) 70%, transparent); }
  .pn { display: flex; flex-direction: column; min-width: 0; }
  .pn b { font-weight: 600; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .pn span { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .pr { text-align: right; font-weight: 600; white-space: nowrap; }
  .unit { margin-left: 3px; font-weight: 400; }

  .hs { margin: 0; display: flex; flex-direction: column; gap: 12px; }
  .hs > div { display: flex; justify-content: space-between; gap: 12px; align-items: baseline; }
  .hs dt { color: var(--text-2); }
  .hs dt a { text-decoration: none; }
  .hs dt a:hover { text-decoration: underline; }
  .hs dd { margin: 0; display: flex; gap: 10px; flex-wrap: wrap; justify-content: flex-end; font-size: var(--fs-sm); }
  .hs b { font-weight: 650; font-variant-numeric: tabular-nums; }
  .att { list-style: none; margin: 14px 0 0; padding: 12px 0 0; border-top: 1px solid var(--border); display: flex; flex-direction: column; gap: 8px; }
  .att li { display: flex; align-items: center; gap: 10px; min-width: 0; }
  .att a { font-weight: 550; }

  .feed { list-style: none; margin: 0; padding: 0; }
  .feed li { display: flex; align-items: center; gap: 10px; padding: 10px 20px; border-top: 1px solid var(--border); min-width: 0; }
  .msg { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .msg a { text-decoration: none; }
  .msg a:hover { text-decoration: underline; }
  .sev { color: var(--info); display: inline-flex; }
  .sev.warning { color: var(--warn); }
  .sev.error { color: var(--err); }
  time { flex: none; }

  @media (max-width: 999px) {
    .cols { grid-template-columns: minmax(0, 1fr); }
  }
  @media (max-width: 699px) {
    .now { grid-template-columns: minmax(0, 1fr); padding: 16px; }
    .big { font-size: 40px; }
    .now-side { flex-direction: row; flex-wrap: wrap; padding: 14px 0 0; border-left: 0; border-top: 1px solid var(--border); gap: 12px 20px; }
    .now-side > div { flex: 1 1 40%; }
    .now-side dd { font-size: var(--fs-lg); }
    .prow a { grid-template-columns: auto minmax(0, 1fr) auto 64px; padding: 12px 16px; gap: 10px; }
    .ps { display: none; }
    .pad { padding: 14px 16px 0; }
    .feed li { padding: 10px 16px; }
  }
</style>
