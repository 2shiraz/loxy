<script lang="ts">
  /** healthy/total with colour + icon (never colour only). */
  import Icon from './Icon.svelte';
  let { healthy, total, title }: { healthy: number; total: number; title?: string } = $props();
  const cls = $derived(total === 0 ? '' : healthy === total ? 'ok' : healthy === 0 ? 'err' : 'warn');
  const icon = $derived(total === 0 ? 'minus' : healthy === total ? 'check' : healthy === 0 ? 'x' : 'alert');
  const text = $derived(total === 0 ? 'unknown' : healthy === total ? 'healthy' : healthy === 0 ? 'unhealthy' : 'partially healthy');
</script>

<span class="hc {cls}" title={title ?? `${healthy} of ${total} checks healthy`}>
  <Icon name={icon} size={13} /><span class="num">{total === 0 ? '—' : `${healthy}/${total}`}</span><span class="sr-only">{text}</span>
</span>

<style>
  .hc { display: inline-flex; align-items: center; gap: 5px; font-variant-numeric: tabular-nums; color: var(--muted); font-weight: 500; }
  .hc.ok { color: var(--ok); }
  .hc.warn { color: var(--warn); }
  .hc.err { color: var(--err); }
</style>
