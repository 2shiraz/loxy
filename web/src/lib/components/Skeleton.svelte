<script lang="ts">
  let { rows = 4, height = 14, card = false }: { rows?: number; height?: number; card?: boolean } = $props();
</script>

<div class="sk" class:card aria-busy="true" aria-label="Loading">
  {#each Array(rows) as _, i (i)}
    <span style="height: {height}px; width: {i === rows - 1 ? 60 : 100 - (i % 3) * 12}%"></span>
  {/each}
</div>

<style>
  .sk { display: flex; flex-direction: column; gap: 12px; padding: 4px 0; }
  .sk.card { background: var(--surface); border: 1px solid var(--border); border-radius: var(--radius); padding: 18px 20px; }
  span { display: block; border-radius: 4px; background: linear-gradient(90deg, var(--surface-2), var(--surface-3), var(--surface-2)); background-size: 200% 100%; animation: sh 1.4s ease-in-out infinite; }
  @keyframes sh { from { background-position: 100% 0; } to { background-position: -100% 0; } }
</style>
