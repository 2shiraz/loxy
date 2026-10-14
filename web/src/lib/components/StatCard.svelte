<script lang="ts">
  let { label, value, of, ratio, hint }: { label: string; value: string; of?: string; ratio?: number; hint?: string } = $props();
  const level = $derived(ratio == null ? '' : ratio >= 0.95 ? 'err' : ratio >= 0.8 ? 'warn' : '');
</script>

<div class="stat">
  <div class="label">{label}</div>
  <div class="value"><span class="v">{value}</span>{#if of}<span class="of"> / {of}</span>{/if}</div>
  {#if ratio != null}
    <div class="meter" role="meter" aria-label="{label} usage" aria-valuemin="0" aria-valuemax="100" aria-valuenow={Math.round(Math.min(1, ratio) * 100)}>
      <span class={level} style="width: {Math.min(100, ratio * 100)}%"></span>
    </div>
  {/if}
  {#if hint}<div class="hint">{hint}</div>{/if}
</div>

<style>
  .stat { padding: 12px 16px 14px; min-width: 0; }
  .label { font-size: var(--fs-sm); color: var(--muted); }
  .value { margin-top: 2px; font-variant-numeric: tabular-nums; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .v { font-size: var(--fs-xl); font-weight: 600; letter-spacing: -0.01em; }
  .of { color: var(--muted); font-size: var(--fs-md); }
  .meter { margin-top: 8px; height: 3px; border-radius: 2px; background: var(--surface-3); overflow: hidden; }
  .meter span { display: block; height: 100%; background: var(--text-2); border-radius: 2px; transition: width 300ms var(--ease-out); }
  .meter span.warn { background: var(--warn); }
  .meter span.err { background: var(--err); }
  .hint { margin-top: 4px; font-size: var(--fs-xs); color: var(--muted); }
</style>
