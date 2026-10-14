<script lang="ts">
  import { getFe } from '$lib/stores/ctx';
  import LiveLogs from '$lib/components/LiveLogs.svelte';
  const ctx = getFe();
</script>

<svelte:head><title>Logs · {ctx.item?.name ?? 'Frontend'} · Loxy</title></svelte:head>

<LiveLogs id={ctx.id} />
