<script lang="ts">
  /** uPlot wrapper. uPlot is dynamically imported so it never lands in the initial bundle. */
  import { onMount } from 'svelte';
  import type uPlotT from 'uplot';

  interface S { label: string; color: string; values: (number | null)[] }
  let {
    title,
    ts,
    series,
    fmt = (v: number) => String(v),
    syncKey = 'loxy',
    height = 180,
    onhover,
    xWindow
  }: { title: string; ts: number[]; series: S[]; fmt?: (v: number) => string; syncKey?: string; height?: number; onhover?: (h: boolean) => void; xWindow?: number } = $props();

  let el: HTMLDivElement | undefined = $state();
  let u: uPlotT | null = null;
  let UPlot: typeof uPlotT | null = null;
  let legend = $state<{ label: string; color: string; value: string }[]>([]);
  let when = $state('');

  function css(name: string) { return getComputedStyle(document.documentElement).getPropertyValue(name).trim() || '#888'; }

  function build() {
    if (!UPlot || !el) return;
    u?.destroy();
    const grid = css('--chart-grid'), axis = css('--chart-axis');
    const opts: uPlotT.Options = {
      width: el.clientWidth || 600,
      height,
      legend: { show: false },
      cursor: { sync: { key: syncKey }, points: { size: 6 }, drag: { x: false, y: false } },
      scales: { x: { time: true, ...(xWindow ? { range: (_u: uPlotT, _min: number, max: number): uPlotT.Range.MinMax => [max - xWindow, max] } : {}) }, y: { range: (_u, _min, max) => [0, max > 0 ? max * 1.1 : 1] } },
      axes: [
        { stroke: axis, grid: { stroke: grid, width: 1 }, ticks: { show: false }, font: '11px "Hanken Grotesk", ui-sans-serif, system-ui', size: 40 },
        { stroke: axis, grid: { stroke: grid, width: 1 }, ticks: { show: false }, font: '11px "Hanken Grotesk", ui-sans-serif, system-ui', size: 60, values: (_u, vals) => vals.map((v) => fmt(v)) }
      ],
      series: [{}, ...series.map((s) => ({ label: s.label, stroke: css(s.color), width: 1.75, fill: css(s.color) + '1a', points: { show: false } }))],
      hooks: {
        setCursor: [(uu) => {
          const i = uu.cursor.idx;
          if (i == null) { legend = series.map((s) => ({ label: s.label, color: s.color, value: last(s.values) })); when = ''; return; }
          when = new Date((uu.data[0][i] as number) * 1000).toLocaleString();
          legend = series.map((s, k) => ({ label: s.label, color: s.color, value: fmtV(uu.data[k + 1][i] as number | null) }));
        }]
      }
    };
    u = new UPlot(opts, [ts, ...series.map((s) => s.values)] as uPlotT.AlignedData, el);
  }
  function fmtV(v: number | null | undefined) { return v == null ? '—' : fmt(v); }
  function last(v: (number | null)[]) { for (let i = v.length - 1; i >= 0; i--) if (v[i] != null) return fmtV(v[i]); return '—'; }

  onMount(() => {
    let ro: ResizeObserver | undefined;
    let cancelled = false;
    void Promise.all([import('uplot'), import('uplot/dist/uPlot.min.css')]).then(([m]) => {
      if (cancelled) return;
      UPlot = m.default;
      build();
      ro = new ResizeObserver(() => { if (u && el) u.setSize({ width: el.clientWidth, height }); });
      if (el) ro.observe(el);
    });
    const mq = matchMedia('(prefers-color-scheme: light)');
    const mo = new MutationObserver(() => build());
    mo.observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme'] });
    mq.addEventListener('change', build);
    return () => { cancelled = true; ro?.disconnect(); mo.disconnect(); mq.removeEventListener('change', build); u?.destroy(); u = null; };
  });

  $effect(() => {
    const data = [ts, ...series.map((s) => s.values)];
    legend = series.map((s) => ({ label: s.label, color: s.color, value: last(s.values) }));
    if (u && u.series.length === data.length) u.setData(data as uPlotT.AlignedData);
    else if (UPlot) build();
  });
</script>

<section class="card chart" aria-label={title}>
  <div class="head">
    <h3>{title}</h3>
    <span class="spacer"></span>
    <span class="when xs muted">{when}</span>
    {#each legend as l (l.label)}
      <span class="lg small"><i style:background="var({l.color})"></i>{l.label} <b>{l.value}</b></span>
    {/each}
  </div>
  <div class="plot" bind:this={el} style:height="{height}px" role="img" aria-label="{title} chart"
    onmouseenter={() => onhover?.(true)} onmouseleave={() => onhover?.(false)}></div>
</section>

<style>
  .chart { padding: 16px 16px 10px; min-width: 0; }
  .head { display: flex; align-items: center; gap: 8px 14px; flex-wrap: wrap; margin-bottom: 10px; min-height: 24px; }
  h3 { font-size: var(--fs-md); font-weight: 600; }
  .when { font-variant-numeric: tabular-nums; }
  .lg { display: inline-flex; align-items: center; gap: 6px; color: var(--muted); }
  .lg i { width: 10px; height: 3px; border-radius: 2px; }
  .lg b { color: var(--text); font-weight: 600; font-variant-numeric: tabular-nums; }
  .plot { width: 100%; }
  .plot :global(.u-select) { background: var(--surface-3); }
  .plot :global(.u-cursor-x), .plot :global(.u-cursor-y) { border-color: var(--border-strong) !important; }
</style>
