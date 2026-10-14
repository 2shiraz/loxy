<script lang="ts">
  import type { Labels } from '$lib/api/client';
  let { labels, max = 99 }: { labels: Labels | undefined; max?: number } = $props();
  const entries = $derived(Object.entries(labels ?? {}));
</script>

{#if entries.length}
  <span class="chips">
    {#each entries.slice(0, max) as [k, v] (k)}
      <span class="chip"><span><b>{k}</b>{v ? `=${v}` : ""}</span></span>
    {/each}
    {#if entries.length > max}<span class="chip">+{entries.length - max}</span>{/if}
  </span>
{/if}

<style>
  .chips { display: inline-flex; flex-wrap: wrap; gap: 4px; }
</style>
