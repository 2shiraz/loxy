<script lang="ts">
  /** Shape + colour status marker (never colour only). */
  let { status }: { status: string } = $props();
  const kind = $derived(
    ['healthy', 'active', 'ok', 'issued'].includes(status) ? 'ok'
      : ['degraded', 'applying', 'pending', 'renewing', 'wrong_ip'].includes(status) ? 'warn'
      : ['unhealthy', 'apply_error', 'failed', 'error', 'missing'].includes(status) ? 'err' : 'unk'
  );
</script>

<span class="sd {kind}" title={status.replace('_', ' ')}>
  <svg width="12" height="12" viewBox="0 0 12 12" aria-hidden="true">
    {#if kind === 'ok'}<circle cx="6" cy="6" r="5" />
    {:else if kind === 'warn'}<path d="M6 1 11 10.5H1Z" />
    {:else if kind === 'err'}<rect x="1.5" y="1.5" width="9" height="9" rx="1" transform="rotate(45 6 6)" />
    {:else}<circle cx="6" cy="6" r="4.5" fill="none" stroke-width="1.5" />{/if}
  </svg>
  <span class="sr-only">{status.replace('_', ' ')}</span>
</span>

<style>
  .sd { display: inline-flex; vertical-align: middle; }
  .ok { fill: var(--ok); }
  .warn { fill: var(--warn); }
  .err { fill: var(--err); }
  .unk { stroke: var(--unknown); fill: none; }
  .unk svg { stroke: var(--unknown); }
</style>
