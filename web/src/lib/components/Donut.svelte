<script lang="ts">
  let { value, total, size = 88, label }: { value: number; total: number; size?: number; label?: string } = $props();
  const r = 15.9155; // circumference 100
  const pct = $derived(total > 0 ? (value / total) * 100 : 0);
  const color = $derived(total === 0 ? 'var(--unknown)' : value === total ? 'var(--ok)' : value === 0 ? 'var(--err)' : 'var(--warn)');
</script>

<svg width={size} height={size} viewBox="0 0 36 36" role="img" aria-label={label ?? `${value} of ${total} healthy`}>
  <circle cx="18" cy="18" r={r} fill="none" stroke="var(--surface-3)" stroke-width="3.2" />
  {#if total > 0}
    <circle cx="18" cy="18" r={r} fill="none" stroke={color} stroke-width="3.2" stroke-linecap={pct > 0 && pct < 100 ? 'round' : 'butt'}
      stroke-dasharray="{pct} {100 - pct}" stroke-dashoffset="25" style="transition: stroke-dasharray 400ms var(--ease-out)" />
  {/if}
  <text x="18" y="17.2" text-anchor="middle" font-size="7" font-weight="600" fill="var(--text)" style="font-variant-numeric: tabular-nums">{total ? value : '—'}</text>
  <text x="18" y="23.5" text-anchor="middle" font-size="4" fill="var(--muted)">{total ? `of ${total}` : 'no checks'}</text>
</svg>
