<script lang="ts">
  import { goto } from '$app/navigation';
  import { getFe } from '$lib/stores/ctx';
  import FrontendForm from '$lib/components/FrontendForm.svelte';
  const ctx = getFe();
</script>

<svelte:head><title>Edit · {ctx.item?.name ?? 'Frontend'} · Loxy</title></svelte:head>

{#if ctx.item}
  {#key ctx.item.id}
    <FrontendForm initial={ctx.item} onsaved={(f) => ctx.set(f)} oncancel={() => goto(`/frontends/${ctx.id}`)} />
  {/key}
{/if}
