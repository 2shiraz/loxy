<script lang="ts">
  /** Tiny live area chart from a number array (oldest first). Pure SVG, no chart library. */
  let { values, height = 32, label, color = 'var(--accent)', min = 2 }: {
    values: number[];
    height?: number;
    label?: string;
    color?: string;
    /** Points needed before a line is drawn. */
    min?: number;
  } = $props();

  const W = 100;
  const geo = $derived.by(() => {
    const v = values.length > 60 ? values.slice(-60) : values;
    if (v.length < min) return null;
    const max = Math.max(1, ...v) * 1.15;
    const step = W / Math.max(1, v.length - 1);
    const pts = v.map((y, i) => [i * step, height - 1 - (y / max) * (height - 3)] as const);
    const line = pts.map(([x, y], i) => `${i ? 'L' : 'M'}${x.toFixed(2)} ${y.toFixed(2)}`).join('');
    const last = pts[pts.length - 1];
    return { line, area: `${line}L${W} ${height}L0 ${height}Z`, lastY: last[1] };
  });
</script>

<span class="spark" style:height="{height}px" style:--c={color} role={label ? 'img' : undefined} aria-label={label} aria-hidden={label ? undefined : 'true'}>
  {#if geo}
    <svg viewBox="0 0 {W} {height}" preserveAspectRatio="none" width="100%" height={height}>
      <path class="area" d={geo.area} />
      <path class="line" d={geo.line} vector-effect="non-scaling-stroke" />
    </svg>
    <i class="head" style:top="{geo.lastY}px"></i>
  {:else}
    <i class="flat"></i>
  {/if}
</span>

<style>
  .spark { position: relative; display: block; width: 100%; min-width: 40px; }
  svg { display: block; overflow: visible; }
  .area { fill: color-mix(in srgb, var(--c) 12%, transparent); stroke: none; }
  .line { fill: none; stroke: var(--c); stroke-width: 1.5; stroke-linejoin: round; stroke-linecap: round; }
  .head { position: absolute; right: -2.5px; width: 5px; height: 5px; margin-top: -2.5px; border-radius: 50%; background: var(--c); transition: top 300ms var(--ease-out); }
  .flat { position: absolute; left: 0; right: 0; bottom: 1px; border-top: 1px dashed var(--border-strong); }
</style>
