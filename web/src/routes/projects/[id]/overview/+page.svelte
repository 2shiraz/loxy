<script lang="ts">
  import { api, unwrap, type MetricsPoint } from '$lib/api/client';
  import { getProject } from '$lib/stores/project';
  import { resource } from '$lib/stores/resource.svelte';
  import { bytes, compact, rate as fmtRate, relTime, dateTime, algorithmLabel, checkText, eventText } from '$lib/util/format';
  import { serviceTitle } from '$lib/util/services';
  import Icon from '$lib/components/Icon.svelte';
  import HealthBadge from '$lib/components/HealthBadge.svelte';
  import HealthCount from '$lib/components/HealthCount.svelte';
  import LabelChips from '$lib/components/LabelChips.svelte';
  import Skeleton from '$lib/components/Skeleton.svelte';
  import EmptyState from '$lib/components/EmptyState.svelte';
  import RouteStrip from '$lib/components/RouteStrip.svelte';
  import LiveNumber from '$lib/components/LiveNumber.svelte';
  import Sparkline from '$lib/components/Sparkline.svelte';

  const ctx = getProject();
  const id = $derived(ctx.id);
  const p = $derived(ctx.project!);
  const base = $derived(`/projects/${id}`);

  const services = resource(async () => (await unwrap(api.GET('/projects/{id}/services', { params: { path: { id } } }))).services, { deps: () => id, keys: () => [`p:${id}`, 'p:*'] });
  const targets = resource(async () => (await unwrap(api.GET('/projects/{id}/targets', { params: { path: { id } } }))).targets, { deps: () => id, keys: () => [`p:${id}`, 'p:*'] });
  const events = resource(async () => (await unwrap(api.GET('/events/history', { params: { query: { project_id: id, limit: 6 } } }))).events, { deps: () => id, keys: () => ['events'] });

  // HTTP projects chart requests; TCP-only projects chart new connections.
  const http = $derived(services.data ? services.data.some((s) => s.protocol !== 'tcp') : true);
  const rateKey = $derived(http ? 'requests_per_second' : 'connections_per_second');
  const WINDOW = 120; // seconds of 1 s points in the sparkline

  // Live numbers from the 1 s metrics stream, seeded with the last two minutes.
  let livePt = $state<MetricsPoint | null>(null);
  let hist = $state<{ ts: number; v: number }[]>([]);
  $effect(() => {
    const pid = id;
    const key = rateKey;
    livePt = null;
    hist = [];
    if (typeof EventSource === 'undefined') return;
    let closed = false;
    const end = Math.floor(Date.now() / 1000);
    void unwrap(api.GET('/projects/{id}/metrics', { params: { path: { id: pid }, query: { type: key, start: end - WINDOW, end, step: 1 } } }))
      .then((m) => {
        if (closed) return;
        const vals = m.series[key] ?? [];
        const seed = m.timestamps.flatMap((ts, i) => (vals[i] == null ? [] : [{ ts, v: vals[i] as number }]));
        const newer = hist.filter((h) => h.ts > (seed.at(-1)?.ts ?? 0));
        hist = [...seed, ...newer].slice(-WINDOW);
      })
      .catch(() => { /* the stream still fills the sparkline */ });
    const src = new EventSource(`/api/v1/projects/${pid}/metrics/stream`, { withCredentials: true });
    src.addEventListener('metrics.tick', (e) => {
      try {
        const pt = JSON.parse((e as MessageEvent).data) as MetricsPoint & Record<string, number>;
        livePt = pt;
        hist = [...hist, { ts: pt.ts, v: pt[key] ?? 0 }].slice(-WINDOW);
      } catch { /* ignore malformed */ }
    });
    return () => { closed = true; src.close(); };
  });

  const pt = $derived(livePt as (MetricsPoint & Record<string, number>) | null);
  const rate = $derived(pt?.[rateKey] ?? (http ? p.stats.requests_per_second : 0));
  const conns = $derived(pt?.open_connections ?? p.stats.open_connections);
  const outBps = $derived(pt?.['bandwidth.out'] ?? p.stats.bytes_out_per_second);
  const inBps = $derived(pt?.['bandwidth.in'] ?? null);
  const resp = $derived(pt?.response_time_ms ?? null);
  const peak = $derived(hist.reduce((m, h) => Math.max(m, h.v), 0));
  const one = fmtRate;

  const sevIcon = (s: string) => (s === 'error' ? 'x' : s === 'warning' ? 'alert' : 'info');
</script>

<div class="ov">
  <RouteStrip endpoints={p.endpoints} targets={targets.data} {rate} rateUnit={http ? 'req/s' : 'conn/s'} algorithm={p.algorithm} maintenance={p.maintenance} {base} />

  <section class="card traffic" aria-labelledby="h-traffic">
    <div class="t-main">
      <div class="t-head">
        <h2 id="h-traffic">{http ? 'Requests per second' : 'New connections per second'}</h2>
        <span class="muted xs">last 2 minutes{peak ? ` · peak ${one(peak)}` : ''}</span>
      </div>
      <div class="t-val"><LiveNumber value={rate} format={one} /></div>
      <div class="t-spark"><Sparkline values={hist.map((h) => h.v)} height={56} label="{http ? 'Requests' : 'New connections'} per second over the last 2 minutes" min={3} /></div>
    </div>
    <dl class="t-side">
      <div><dt>Open connections</dt><dd><LiveNumber value={conns} format={compact} /></dd></div>
      <div><dt>Inbound</dt><dd>{#if inBps != null}<LiveNumber value={inBps} format={(n) => bytes(n, '/s')} />{:else}—{/if}</dd></div>
      <div><dt>Outbound</dt><dd><LiveNumber value={outBps} format={(n) => bytes(n, '/s')} /></dd></div>
      {#if http}<div><dt>Response time</dt><dd>{#if resp}<LiveNumber value={resp} format={(n) => `${Math.round(n)} ms`} />{:else}<span class="muted">—</span>{/if}</dd></div>{/if}
    </dl>
    <a class="graphs-link" href="{base}/graphs">Open graphs<Icon name="chevronRight" size={14} /></a>
  </section>

  <div class="grid-2">
    <section class="card" aria-labelledby="h-health">
      <div class="card-head"><h2 id="h-health">Health</h2><span class="spacer"></span><HealthBadge status={p.health.status} /></div>
      <div class="msgs">
        {#if p.maintenance}
          <p class="warn-text small"><Icon name="pause" size={13} /> Maintenance is on: every request gets a 503 until you turn it off.</p>
        {/if}
        {#if p.health.total === 0}
          <p class="muted">No health checks yet. They start as soon as the project has a target and a service.</p>
        {:else if p.health.status === 'healthy' || !p.health.messages.length}
          <p><b class="ok-text">{p.health.healthy} of {p.health.total}</b> checks pass. All targets answer.</p>
        {:else}
          <p><b class={p.health.healthy ? 'warn-text' : 'err-text'}>{p.health.healthy} of {p.health.total}</b> checks pass.</p>
          <ul>{#each p.health.messages.slice(0, 4) as m (m)}<li class="small" title={m}>{checkText(m)}</li>{/each}</ul>
        {/if}
        <a class="link small" href="{base}/targets">See each target</a>
      </div>
    </section>

    <section class="card flush" aria-labelledby="h-ev">
      <div class="card-head pad"><h2 id="h-ev">Recent events</h2><span class="spacer"></span><a class="link small" href="/activity?project_id={id}">All activity</a></div>
      {#if !events.data}<div class="pad2"><Skeleton rows={4} /></div>
      {:else if !events.data.length}<EmptyState icon="activity" title="Quiet so far" text="Health changes and applied changes for this project show up here." />
      {:else}
        <ol class="feed">
          {#each events.data as e (e.id)}
            <li>
              <span class="sev {e.severity}"><Icon name={sevIcon(e.severity)} size={14} label={e.severity} /></span>
              <span class="msg" title={e.message}>{eventText(e.message)}</span>
              <time class="xs muted" datetime={new Date(e.ts).toISOString()} title={dateTime(e.ts)}>{relTime(e.ts)}</time>
            </li>
          {/each}
        </ol>
      {/if}
    </section>
  </div>

  <div class="grid-2">
    <section class="card flush" aria-labelledby="h-svc">
      <div class="card-head pad"><h2 id="h-svc">Services</h2><span class="spacer"></span><a class="link small" href="{base}/services">Manage</a></div>
      {#if !services.data}<div class="pad2"><Skeleton rows={3} /></div>
      {:else if !services.data.length}<EmptyState icon="zap" title="No services" text="A service is the port people connect to. Without one, the project accepts no traffic."><a class="btn sm" href="{base}/services">Add service</a></EmptyState>
      {:else}
        <ul class="rows">
          {#each services.data as s (s.id)}
            <li>
              <span class="mono small">{serviceTitle(s)}</span>
              <span class="small muted dom">{s.domains?.length ? s.domains.join(', ') : ''}</span>
              <HealthCount healthy={s.health.healthy} total={s.health.total} />
            </li>
          {/each}
        </ul>
      {/if}
    </section>

    <section class="card" aria-labelledby="h-set">
      <div class="card-head"><h2 id="h-set">Settings</h2><span class="spacer"></span><a class="link small" href="{base}/settings">Edit</a></div>
      <dl class="kv">
        <dt>Algorithm</dt><dd>{algorithmLabel(p.algorithm)}</dd>
        <dt>Maintenance</dt><dd>{p.maintenance ? 'On' : 'Off'}</dd>
        <dt>Labels</dt><dd>{#if Object.keys(p.labels).length}<LabelChips labels={p.labels} />{:else}<span class="muted">None</span>{/if}</dd>
        <dt>Created</dt><dd>{dateTime(p.created_at)}</dd>
      </dl>
    </section>
  </div>
</div>

<style>
  .ov { display: flex; flex-direction: column; gap: 16px; }

  .traffic { display: grid; grid-template-columns: minmax(0, 1fr) auto; gap: 8px 28px; padding: 20px 22px; position: relative; }
  .t-main { min-width: 0; display: flex; flex-direction: column; gap: 4px; }
  .t-head { display: flex; align-items: baseline; gap: 10px; flex-wrap: wrap; }
  .t-head h2 { font-size: var(--fs-md); font-weight: 600; }
  .t-val { font-size: 40px; font-weight: 650; letter-spacing: -0.03em; line-height: 1.1; }
  .t-spark { margin-top: 6px; }
  .t-side { margin: 0; display: grid; grid-template-columns: repeat(2, minmax(120px, auto)); gap: 14px 28px; align-content: start; padding-top: 2px; }
  .t-side dt { font-size: var(--fs-xs); color: var(--muted); }
  .t-side dd { margin: 2px 0 0; font-size: var(--fs-lg); font-weight: 600; white-space: nowrap; }
  .graphs-link { position: absolute; right: 18px; bottom: 14px; display: inline-flex; align-items: center; gap: 2px; font-size: var(--fs-sm); color: var(--accent); font-weight: 550; text-decoration: none; }
  .graphs-link:hover { text-decoration: underline; }

  .msgs { display: flex; flex-direction: column; gap: 10px; min-width: 0; align-items: flex-start; }
  .msgs ul { margin: 0; padding-left: 16px; display: flex; flex-direction: column; gap: 4px; }
  .msgs li { overflow-wrap: anywhere; color: var(--text-2); }
  .pad { padding: 16px 20px 0; margin-bottom: 6px; }
  .pad2 { padding: 12px 20px; }
  .feed, .rows { list-style: none; margin: 0; padding: 0; }
  .feed li, .rows li { display: flex; align-items: center; gap: 10px; padding: 10px 20px; border-top: 1px solid var(--border); min-width: 0; }
  .msg { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .sev { color: var(--info); display: inline-flex; }
  .sev.warning { color: var(--warn); }
  .sev.error { color: var(--err); }
  time { flex: none; }
  .dom { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }

  @media (max-width: 799px) {
    .traffic { grid-template-columns: minmax(0, 1fr); padding: 16px; }
    .t-side { grid-template-columns: repeat(2, minmax(0, 1fr)); border-top: 1px solid var(--border); padding-top: 14px; margin-top: 8px; }
    .graphs-link { position: static; justify-self: start; }
    .t-val { font-size: 34px; }
    .pad { padding: 14px 16px 0; }
    .feed li, .rows li { padding: 10px 16px; }
  }
</style>
