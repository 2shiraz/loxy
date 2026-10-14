<script lang="ts">
  import type { Snippet } from 'svelte';
  let { children, label, bare = false }: { children: Snippet; label?: string; bare?: boolean } = $props();
</script>

<!-- svelte-ignore a11y_no_noninteractive_tabindex -->
<div class="table-wrap" class:bare role="region" aria-label={label} tabindex={label ? 0 : undefined}>
  <table class="tbl">{@render children()}</table>
</div>
