<script lang="ts">
  import { getProject } from '$lib/stores/project';
  import LiveLogs from '$lib/components/LiveLogs.svelte';

  const ctx = getProject();
</script>

<svelte:head><title>Logs · {ctx.project?.name ?? 'Project'} · Loxy</title></svelte:head>

<LiveLogs id={ctx.id} />
