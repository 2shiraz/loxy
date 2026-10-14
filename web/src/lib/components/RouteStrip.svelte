<script lang="ts">
  /**
   * "How to reach it": entry points → Loxy → targets, drawn as a route.
   * The wire's flow moves only while requests are flowing, faster when busier.
   * Under reduced motion the flow is static (app.css stops animations).
   */
  import type { Endpoint, Target } from '$lib/api/client';
  import { rate as fmtRate, algorithmLabel, isWebUrl } from '$lib/util/format';
  import Icon from './Icon.svelte';
  import HealthCount from './HealthCount.svelte';
  import LiveNumber from './LiveNumber.svelte';

  let { endpoints, targets, rate, rateUnit = 'req/s', algorithm, maintenance = false, base }: {
    endpoints: Endpoint[];
    targets: Target[] | undefined;
    rate: number;
    rateUnit?: string;
    algorithm: string;
    maintenance?: boolean;
    base: string;
  } = $props();

  const sorted = $derived([...endpoints].sort((a, b) => (a.kind === b.kind ? 0 : a.kind === 'domain' ? -1 : 1)));
  const hasDomain = $derived(endpoints.some((e) => e.kind === 'domain'));
  const flowing = $derived(rate > 0.05 && !maintenance);
  // 0.4 s per dash cycle when very busy, 1.6 s when barely anything flows.
  const dur = $derived(flowing ? Math.max(0.4, Math.min(1.6, 2.4 / Math.log2(rate + 2))) : 0);

  let copied = $state('');
  async function copy(u: string) {
    try {
      await navigator.clipboard.writeText(u);
      copied = u;
      setTimeout(() => { if (copied === u) copied = ''; }, 1400);
    } catch { /* clipboard unavailable */ }
  }

  function tName(t: Target) {
    if (t.type === 'label_selector') return t.label_selector ?? 'selector';
    const r = t.resolved[0];
    if (t.type === 'host') return r?.name ?? `Host #${t.host_id}`;
    return t.ip ?? r?.address ?? 'IP';
  }
  function tNote(t: Target) {
    if (t.removing) return 'Removing';
    if (t.admin_state === 'drain') return 'Draining';
    if (t.admin_state === 'maint') return 'Maintenance';
    if (t.type === 'label_selector') return `${t.resolved.length} host${t.resolved.length === 1 ? '' : 's'}`;
    if (t.backup) return 'Backup';
    return t.port ? `port ${t.port}` : '';
  }
  const shownTargets = $derived((targets ?? []).slice(0, 5));
</script>

<section class="route" aria-labelledby="route-h">
  <h2 id="route-h" class="sr-only">How to reach it</h2>
  <div class="grid">
    <div class="side entries">
      <h3>People connect to</h3>
      {#if sorted.length}
        <ul>
          {#each sorted as e (e.url)}
            <li>
              <span class="kind" title={e.kind === 'domain' ? 'Domain on shared ports 80/443' : 'This project’s own port'}><Icon name={e.kind === 'domain' ? 'globe' : 'zap'} size={15} /></span>
              {#if isWebUrl(e.url)}
                <a class="url" href={e.url} target="_blank" rel="noopener noreferrer" title={e.url}>{e.url}</a>
              {:else}
                <span class="url" title={e.url}>{e.url}</span>
              {/if}
              <button type="button" class="cp" onclick={() => copy(e.url)} aria-label="Copy {e.url}" title="Copy">
                <Icon name={copied === e.url ? 'check' : 'copy'} size={14} />
              </button>
            </li>
          {/each}
        </ul>
        <span class="sr-only" aria-live="polite">{copied ? 'Copied to clipboard' : ''}</span>
        {#if !hasDomain}
          <p class="hint"><a href="{base}/domains">Attach a domain</a> to serve it on ports 80 and 443 with automatic HTTPS.</p>
        {/if}
      {:else}
        <p class="none">Nothing listens yet.</p>
        <a class="btn sm" href="{base}/services"><Icon name="plus" size={13} />Add a service</a>
      {/if}
    </div>

    <div class="hub" class:flowing class:maint={maintenance} style:--dur="{dur}s">
      <span class="wire in" aria-hidden="true"></span>
      <div class="node">
        <span class="rate">{#if maintenance}Maintenance{:else}<LiveNumber value={rate} format={fmtRate} /> {rateUnit}{/if}</span>
        <span class="alg">{maintenance ? 'answers 503' : algorithmLabel(algorithm)}</span>
      </div>
      <span class="wire out" aria-hidden="true"></span>
    </div>

    <div class="side targets">
      <h3>Traffic goes to</h3>
      {#if !targets}
        <p class="none">Loading targets…</p>
      {:else if !targets.length}
        <p class="none">No servers yet. Targets are the servers that receive the traffic.</p>
        <a class="btn sm primary" href="{base}/targets"><Icon name="plus" size={13} />Add targets</a>
      {:else}
        <ul>
          {#each shownTargets as t (t.id)}
            {@const ok = t.resolved.filter((r) => r.status === 'healthy').length}
            <li class:dim={t.admin_state !== 'ready' || t.removing}>
              <HealthCount healthy={ok} total={t.resolved.length} title="{ok} of {t.resolved.length} addresses healthy" />
              <span class="url t" class:sans={t.type === 'host'} title={tName(t)}>{tName(t)}</span>
              <span class="note">{tNote(t)}</span>
            </li>
          {/each}
        </ul>
        <a class="more" href="{base}/targets">{(targets.length > 5) ? `All ${targets.length} targets` : 'Manage targets'}</a>
      {/if}
    </div>
  </div>
</section>

<style>
  .route { container-type: inline-size; background: var(--surface); border: 1px solid var(--border); border-radius: var(--radius-lg); }
  .grid { display: grid; grid-template-columns: minmax(0, 1fr); }
  .side { padding: 20px 22px; min-width: 0; display: flex; flex-direction: column; align-items: flex-start; gap: 10px; }
  h3 { font-size: var(--fs-sm); font-weight: 550; color: var(--muted); }
  ul { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 8px; width: 100%; }
  li { display: flex; align-items: center; gap: 10px; min-width: 0; }
  .kind { display: grid; place-items: center; width: 28px; height: 28px; flex: none; border-radius: 8px; background: var(--accent-soft); color: var(--accent); }
  .url { font-family: var(--mono); font-size: var(--fs-md); font-weight: 500; color: var(--text); min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; text-decoration: none; }
  .entries .url { font-size: 15px; }
  a.url:hover { text-decoration: underline; text-decoration-color: var(--accent-line); }
  .t.sans { font-family: var(--font); font-weight: 550; }
  .cp { display: grid; place-items: center; width: 32px; height: 32px; flex: none; border: 0; border-radius: var(--radius-sm); background: none; color: var(--muted); cursor: pointer; transition: background-color 120ms ease, color 120ms ease, transform 120ms var(--ease-out); }
  .cp:hover { background: var(--surface-2); color: var(--text); }
  .cp:active { transform: scale(0.92); }
  .hint { font-size: var(--fs-sm); color: var(--text-2); margin-top: 2px; }
  .hint a { color: var(--accent); font-weight: 550; text-decoration: none; }
  .hint a:hover { text-decoration: underline; }
  .none { color: var(--text-2); font-size: var(--fs-sm); max-width: 36ch; }
  .note { margin-left: auto; font-size: var(--fs-xs); color: var(--muted); white-space: nowrap; flex: none; }
  .dim .url { color: var(--muted); }
  .more { font-size: var(--fs-sm); color: var(--accent); font-weight: 550; text-decoration: none; }
  .more:hover { text-decoration: underline; }

  /* The hub: wire → node → wire. Vertical when narrow, horizontal when wide. */
  .hub { display: flex; flex-direction: column; align-items: center; padding: 0 22px; --wire: var(--border-strong); }
  .wire { width: 2px; height: 22px; background: var(--wire); border-radius: 2px; }
  .flowing { --wire: var(--accent); }
  .flowing .wire {
    background: repeating-linear-gradient(180deg, var(--accent) 0 6px, transparent 6px 11px);
    background-size: 100% 11px;
    animation: flow-v var(--dur) linear infinite;
  }
  .node {
    display: flex; flex-direction: column; align-items: center; gap: 1px; padding: 9px 16px; border-radius: 12px;
    background: var(--surface-2); border: 1px solid var(--border); min-width: 132px; text-align: center;
  }
  .flowing .node { border-color: var(--accent-line); background: var(--accent-soft); }
  .maint .node { border-color: color-mix(in srgb, var(--warn) 40%, transparent); background: var(--warn-soft); }
  .rate { font-size: var(--fs-lg); font-weight: 650; white-space: nowrap; font-variant-numeric: tabular-nums; }
  .flowing .rate { color: var(--accent); }
  .maint .rate { color: var(--warn); }
  .alg { font-size: var(--fs-xs); color: var(--muted); white-space: nowrap; }
  .targets { border-top: 0; }

  @container (min-width: 760px) {
    .grid { grid-template-columns: minmax(0, 1.15fr) minmax(170px, 0.7fr) minmax(0, 1fr); align-items: stretch; }
    .side { padding: 22px 24px; }
    .hub { flex-direction: row; padding: 0; }
    .wire { flex: 1; min-width: 16px; width: auto; height: 2px; }
    .flowing .wire { background: repeating-linear-gradient(90deg, var(--accent) 0 6px, transparent 6px 11px); background-size: 11px 100%; animation-name: flow-h; }
    .targets { border-left: 0; }
  }
  @keyframes flow-v { to { background-position: 0 11px; } }
  @keyframes flow-h { to { background-position: 11px 0; } }
</style>
