<script lang="ts">
  import { getBe } from '$lib/stores/ctx';
  import BackendForm from '$lib/components/BackendForm.svelte';
  const ctx = getBe();
</script>

<svelte:head><title>Settings · {ctx.item?.name ?? 'Backend'} · Loxy</title></svelte:head>

{#if ctx.item}
  {#key ctx.item.id}
    <BackendForm initial={ctx.item} onsaved={(b) => ctx.set(b)} />
  {/key}
{/if}
