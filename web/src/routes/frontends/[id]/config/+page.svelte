<script lang="ts">
  import { getText } from '$lib/api/client';
  import { getFe } from '$lib/stores/ctx';
  import { resource } from '$lib/stores/resource.svelte';
  import { toast } from '$lib/stores/toast.svelte';
  import Icon from '$lib/components/Icon.svelte';
  import CodeView from '$lib/components/CodeView.svelte';
  import Skeleton from '$lib/components/Skeleton.svelte';
  import ErrorBox from '$lib/components/ErrorBox.svelte';

  const ctx = getFe();
  const id = $derived(ctx.id);
  const cfg = resource(() => getText(`/frontends/${id}/config`), { deps: () => [id, ctx.item?.version], keys: () => [`fe:${id}`] });

  async function copy() {
    try { await navigator.clipboard.writeText(cfg.data ?? ''); toast.info('Config copied'); } catch { toast.error('Clipboard unavailable'); }
  }
  function download() {
    const a = document.createElement('a');
    a.href = URL.createObjectURL(new Blob([cfg.data ?? ''], { type: 'text/plain' }));
    a.download = `${ctx.item?.name ?? 'frontend'}-haproxy.cfg`;
    a.click();
    URL.revokeObjectURL(a.href);
  }
</script>

<svelte:head><title>Config · {ctx.item?.name ?? 'Frontend'} · Loxy</title></svelte:head>

<div class="row bar">
  <p class="muted small">Rendered HAProxy sections for this frontend and the backends it uses. Read-only; edit the frontend to change it.</p>
  <span class="spacer"></span>
  <button class="btn sm" onclick={copy} disabled={!cfg.data}><Icon name="copy" size={13} />Copy</button>
  <button class="btn sm" onclick={download} disabled={!cfg.data}><Icon name="download" size={13} />Download</button>
</div>
{#if cfg.error && cfg.data == null}<ErrorBox message={cfg.error} onretry={cfg.reload} />
{:else if cfg.data == null}<Skeleton card rows={10} />
{:else}<CodeView text={cfg.data} maxHeight={720} label="HAProxy configuration" />{/if}

<style>
  .bar { margin-bottom: 12px; flex-wrap: wrap; }
</style>
