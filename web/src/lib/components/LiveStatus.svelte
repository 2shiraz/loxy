<script lang="ts">
  /** Connection to the event stream, plus an "Applying…" note while HAProxy catches up. */
  import { live, applying } from '$lib/stores/live.svelte';
  import Icon from './Icon.svelte';

  let { compact = false }: { compact?: boolean } = $props();
  const st = $derived(live.state === 'open' && !live.stale ? 'open' : live.state === 'closed' ? 'closed' : 'connecting');
  const label = $derived(st === 'open' ? 'Live' : st === 'connecting' ? 'Reconnecting' : 'Offline');
  const n = $derived(live.tick?.node);
</script>

<span class="wrap">
  {#if applying()}
    <span class="apply" role="status" title="HAProxy is loading change v{n?.desired_version} (running v{n?.applied_version})">
      <Icon name="refresh" size={13} class="spin" /><span class:sr-only={compact}>Applying…</span>
    </span>
  {/if}
  <span class="live {st}" role="status" aria-label="Updates: {label}" title={st === 'open' ? 'Receiving live updates every 2 seconds' : st === 'connecting' ? 'Connection lost, reconnecting…' : 'Not connected'}>
    {#key live.tick?.ts}<span class="led" aria-hidden="true"></span>{/key}
    <span class:sr-only={compact}>{label}</span>
  </span>
</span>

<style>
  .wrap { display: inline-flex; align-items: center; gap: 6px; }
  .live, .apply {
    display: inline-flex; align-items: center; gap: 7px; height: 28px; padding: 0 10px; border-radius: 999px;
    font-size: var(--fs-sm); font-weight: 550; color: var(--text-2); white-space: nowrap;
  }
  .live { border: 1px solid var(--border); background: var(--surface); }
  .apply { color: var(--warn); background: var(--warn-soft); }
  .apply :global(.spin) { animation: spin 1s linear infinite; }
  .led { width: 7px; height: 7px; border-radius: 50%; background: var(--unknown); flex: none; }
  /* Like a port's activity LED: a short blink on every tick. */
  .open .led { background: var(--ok); animation: blink 900ms var(--ease-out); }
  .connecting { color: var(--warn); }
  .connecting .led { background: var(--warn); animation: pulse 1.2s ease-in-out infinite; }
  .closed .led { background: var(--err); }
  @keyframes blink { from { box-shadow: 0 0 0 4px color-mix(in srgb, var(--ok) 30%, transparent); background: color-mix(in srgb, var(--ok) 60%, white); } }
  @keyframes pulse { 50% { opacity: 0.35; } }
  @keyframes spin { to { transform: rotate(360deg); } }
</style>
