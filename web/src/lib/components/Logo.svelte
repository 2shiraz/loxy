<script lang="ts">
  /** Loxy mark: one line in, a junction, three lines out — what a load balancer does. */
  let { size = 28 }: { size?: number } = $props();
</script>

<svg width={size} height={size} viewBox="0 0 32 32" aria-hidden="true" focusable="false">
  <rect width="32" height="32" rx="9" fill="var(--accent)" />
  <path d="M6.5 16H13M13 16l11-7.5M13 16h11M13 16l11 7.5" stroke="var(--accent-fg)" stroke-width="2.4" fill="none" stroke-linecap="round" stroke-linejoin="round" />
  <circle cx="13" cy="16" r="3" fill="var(--accent-fg)" />
</svg>
