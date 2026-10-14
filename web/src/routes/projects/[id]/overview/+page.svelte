<script lang="ts">
  import { onDestroy } from 'svelte';
  import { api, unwrap, type MetricsPoint } from '$lib/api/client';
  import { getProject } from '$lib/stores/project';
  import { resource } from '$lib/stores/resource.svelte';
  import { bytes, compact, relTime, dateTime, algorithmLabel } from '$lib/util/format';
  import { serviceTitle } from '$lib/util/services';
  import Icon from '$lib/components/Icon.svelte';
  import StatBar from '$lib/components/StatBar.svelte';
  import StatCard from '$lib/components/StatCard.svelte';
  import Donut from '$lib/components/Donut.svelte';
  import HealthBadge from '$lib/components/HealthBadge.svelte';
  import HealthCount from '$lib/components/HealthCount.svelte';
  import LabelChips from '$lib/components/LabelChips.svelte';
  import Endpoints from '$lib/components/Endpoints.svelte';
  import Skeleton from '$lib/components/Skeleton.svelte';
  import EmptyState from '$lib/components/EmptyState.svelte';

  const ctx = getProject();
  const id = $derived(ctx.id);
  const p = $derived(ctx.project!);
  const base = $derived(`/projects/${id}`);

  const services = resource(async () => (await unwrap(api.GET('/projects/{id}/services', { params: { path: { id } } }))).services, { deps: () => id, keys: () => [`p:${id}`, 'p:*'] });
  const targets = resource(async () => (await unwrap(api.GET('/projects/{id}/targets', { params: { path: { id } } }))).targets, { deps: () => id, keys: () => [`p:${id}`, 'p:*'] });
  const events = resource(async () => (await unwrap(api.GET('/events/history', { params: { query: { project_id: id, limit: 8 } } }))).events, { deps: () => id, keys: () => ['events'] });

  // Live numbers from the 1 s metrics stream; fall back to the project's last stats.
  let livePt = $state<MetricsPoint | null>(null);
  let es: EventSource | null = null;
  $effect(() => {
    const pid = id;
    livePt = null;
    if (typeof EventSource === 'undefined') return;
    const src = new EventSource(`/api/v1/projects/${pid}/metrics/stream`, { withCredentials: true });
    es = src;
    src.addEventListener('metrics.tick', (e) => {
      try { livePt = JSON.parse((e as MessageEvent).data) as MetricsPoint; } catch { /* ignore malformed */ }
    });
    return () => { src.close(); if (es === src) es = null; };
  });
  onDestroy(() => es?.close());

  const conns = $derived(livePt?.open_connections ?? p.stats.open_connections);
  const rps = $derived(livePt?.requests_per_second ?? p.stats.requests_per_second);
  const outBps = $derived(livePt?.['bandwidth.out'] ?? p.stats.bytes_out_per_second);
  const inBps = $derived(livePt?.['bandwidth.in']);
  const hasDomain = $derived(p.endpoints.some((e) => e.kind === 'domain'));

  type T = NonNullable<typeof targets.data>[number];
  function targetName(t: T) {
    if (t.type === 'label_selector') return t.label_selector ?? 'selector';
    const r = t.resolved[0];
    if (t.type === 'host') return r?.name ?? `Host #${t.host_id}`;
    return t.ip ?? r?.address ?? 'IP';
  }
  const sevIcon = (s: string) => (s === 'error' ? 'x' : s === 'warning' ? 'alert' : 'info');
</script>

<div class="stack-lg">
  <StatBar>
    <StatCard label="Open connections" value={compact(conns)} />
    <StatCard label="Requests / s" value={compact(rps)} />
    {#if inBps != null}<StatCard label="Inbound" value={bytes(inBps, '/s')} />{/if}
    <StatCard label="Outbound" value={bytes(outBps, '/s')} hint={livePt ? 'live' : undefined} />
  </StatBar>

  <div class="grid-2">
    <section class="card" aria-labelledby="h-reach">
      <div class="card-head"><h2 id="h-reach">How to reach it</h2></div>
      {#if p.endpoints.length}
        <ul class="eplist">
          {#each p.endpoints as e (e.url)}
            <li>
              <span class="badge">{e.kind === 'domain' ? 'Domain' : 'Port'}</span>
              <Endpoints endpoints={[e]} full />
            </li>
          {/each}
        </ul>
      {:else}
        <p class="muted small">No endpoints yet. <a href="{base}/services">Add a service</a>.</p>
      {/if}
      {#if !hasDomain}
        <p class="small muted hint2"><Icon name="info" size={13} />Want a hostname with HTTPS? <a href="{base}/domains">Attach a domain</a> — traffic on ports 80 and 443 is then routed here and a certificate is issued automatically.</p>
      {/if}
    </section>

    <section class="card" aria-labelledby="h-health">
      <div class="card-head"><h2 id="h-health">Health</h2><span class="spacer"></span><HealthBadge status={p.health.status} /></div>
      <div class="hbody">
        <Donut value={p.health.healthy} total={p.health.total} label="{p.health.healthy} of {p.health.total} target checks healthy" />
        <div class="msgs">
          {#if p.maintenance}
            <p class="warn-text small"><Icon name="pause" size={13} /> Maintenance is on: every request gets a 503.</p>
          {/if}
          {#if p.health.total === 0}
            <p class="muted small">No checks yet. Add targets to start health checking.</p>
          {:else if p.health.status === 'healthy' || !p.health.messages.length}
            <p class="small">All target checks pass.</p>
          {:else}
            <ul>{#each p.health.messages.slice(0, 4) as m (m)}<li class="mono small">{m}</li>{/each}</ul>
          {/if}
          <a class="small" href="{base}/targets">Per-target status</a>
        </div>
      </div>
    </section>
  </div>

  <div class="grid-2">
    <section class="card flush" aria-labelledby="h-svc">
      <div class="card-head pad"><h2 id="h-svc">Services</h2><span class="spacer"></span><a class="small" href="{base}/services">Manage</a></div>
      {#if !services.data}<div class="pad2"><Skeleton rows={3} /></div>
      {:else if !services.data.length}<EmptyState icon="globe" title="No services" text="Without a service the project accepts no traffic."><a class="btn sm" href="{base}/services">Add service</a></EmptyState>
      {:else}
        <table class="tbl">
          <tbody>
            {#each services.data as s (s.id)}
              <tr>
                <td class="mono small">{serviceTitle(s)}</td>
                <td class="small muted">{s.domains?.length ? s.domains.join(', ') : ''}</td>
                <td class="right"><HealthCount healthy={s.health.healthy} total={s.health.total} /></td>
              </tr>
            {/each}
          </tbody>
        </table>
      {/if}
    </section>

    <section class="card flush" aria-labelledby="h-tgt">
      <div class="card-head pad"><h2 id="h-tgt">Targets</h2><span class="spacer"></span><a class="small" href="{base}/targets">Manage</a></div>
      {#if !targets.data}<div class="pad2"><Skeleton rows={3} /></div>
      {:else if !targets.data.length}<EmptyState icon="server" title="No targets" text="Targets are the servers that receive the traffic."><a class="btn sm" href="{base}/targets">Add targets</a></EmptyState>
      {:else}
        <table class="tbl">
          <tbody>
            {#each targets.data.slice(0, 6) as t (t.id)}
              <tr>
                <td class="small" class:mono={t.type !== 'host'}>{targetName(t)}</td>
                <td class="small muted">{t.type === 'label_selector' ? `${t.resolved.length} host${t.resolved.length === 1 ? '' : 's'}` : t.admin_state !== 'ready' ? (t.admin_state === 'drain' ? 'Draining' : 'Maintenance') : ''}</td>
                <td class="right"><HealthCount healthy={t.resolved.filter((r) => r.status === 'healthy').length} total={t.resolved.length} title="healthy addresses" /></td>
              </tr>
            {/each}
          </tbody>
        </table>
        {#if targets.data.length > 6}<p class="small more"><a href="{base}/targets">All {targets.data.length} targets</a></p>{/if}
      {/if}
    </section>
  </div>

  <div class="grid-2">
    <section class="card flush" aria-labelledby="h-ev">
      <div class="card-head pad"><h2 id="h-ev">Recent events</h2><span class="spacer"></span><a class="small" href="/activity?project_id={id}">All activity</a></div>
      {#if !events.data}<div class="pad2"><Skeleton rows={4} /></div>
      {:else if !events.data.length}<EmptyState icon="activity" title="Nothing yet" text="Health changes and applied changes for this project show up here." />
      {:else}
        <ol class="feed">
          {#each events.data as e (e.id)}
            <li>
              <span class="sev {e.severity}"><Icon name={sevIcon(e.severity)} size={13} label={e.severity} /></span>
              <span class="msg" title={e.message}>{e.message}</span>
              <time class="xs muted" datetime={new Date(e.ts).toISOString()} title={dateTime(e.ts)}>{relTime(e.ts)}</time>
            </li>
          {/each}
        </ol>
      {/if}
    </section>

    <section class="card" aria-labelledby="h-set">
      <div class="card-head"><h2 id="h-set">Settings</h2><span class="spacer"></span><a class="small" href="{base}/settings">Edit</a></div>
      <dl class="kv">
        <dt>Algorithm</dt><dd>{algorithmLabel(p.algorithm)}</dd>
        <dt>Maintenance</dt><dd>{p.maintenance ? 'On' : 'Off'}</dd>
        <dt>Labels</dt><dd>{#if Object.keys(p.labels).length}<LabelChips labels={p.labels} />{:else}<span class="muted">None</span>{/if}</dd>
        <dt>Created</dt><dd>{dateTime(p.created_at)}</dd>
        <dt>ID</dt><dd class="mono small">{p.id}</dd>
      </dl>
    </section>
  </div>
</div>

<style>
  .eplist { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 8px; }
  .eplist li { display: flex; align-items: center; gap: 10px; min-width: 0; }
  .hint2 { display: flex; gap: 6px; align-items: flex-start; margin-top: 12px; }
  .hint2 :global(svg) { flex: none; margin-top: 3px; }
  .hbody { display: flex; gap: 18px; align-items: center; }
  .msgs { display: flex; flex-direction: column; gap: 8px; min-width: 0; }
  .msgs ul { margin: 0; padding-left: 16px; }
  .msgs li { overflow-wrap: anywhere; color: var(--text-2); }
  .pad { padding: 12px 16px 0; margin-bottom: 4px; }
  .pad2 { padding: 12px 16px; }
  .more { padding: 8px 16px 12px; }
  .feed { list-style: none; margin: 0; padding: 0; }
  .feed li { display: flex; align-items: baseline; gap: 10px; padding: 8px 16px; border-top: 1px solid var(--border); }
  .msg { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .sev { color: var(--info); display: inline-flex; align-self: center; }
  .sev.warning { color: var(--warn); }
  .sev.error { color: var(--err); }
  time { flex: none; }
</style>
