<script lang="ts">
  import type { Snippet } from 'svelte';
  let { children }: { children: Snippet } = $props();
</script>

<div class="statbar">{@render children()}</div>

<style>
  .statbar {
    display: grid; grid-template-columns: repeat(auto-fit, minmax(150px, 1fr));
    background: var(--surface); border: 1px solid var(--border); border-radius: var(--radius); overflow: hidden;
  }
  .statbar > :global(*) { border-right: 1px solid var(--border); border-bottom: 1px solid var(--border); margin: 0 -1px -1px 0; }
</style>
