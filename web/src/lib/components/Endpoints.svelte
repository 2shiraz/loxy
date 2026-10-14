<script lang="ts">
  /** Where a project is reachable: its ports and domains, as links with a copy button. */
  import type { Endpoint } from '$lib/api/client';
  import { endpointLabel, isWebUrl } from '$lib/util/format';
  import Icon from './Icon.svelte';

  let { endpoints, full = false, max = 3 }: { endpoints: Endpoint[]; full?: boolean; max?: number } = $props();

  // Domains first: they are what people type.
  const sorted = $derived([...endpoints].sort((a, b) => (a.kind === b.kind ? 0 : a.kind === 'domain' ? -1 : 1)));
  const shown = $derived(full ? sorted : sorted.slice(0, max));
  let copied = $state('');
  async function copy(u: string) {
    try {
      await navigator.clipboard.writeText(u);
      copied = u;
      setTimeout(() => { if (copied === u) copied = ''; }, 1200);
    } catch { /* clipboard unavailable */ }
  }
</script>

{#if !endpoints.length}
  <span class="muted">—</span>
{:else}
  <span class="eps" class:full>
    {#each shown as e (e.url)}
      <span class="ep">
        {#if isWebUrl(e.url)}
          <a class="mono" href={e.url} target="_blank" rel="noopener noreferrer" title={e.url}>{full ? e.url : endpointLabel(e)}</a>
        {:else}
          <span class="mono" title={e.url}>{full ? e.url : endpointLabel(e)}</span>
        {/if}
        {#if full}
          <button type="button" class="cp" onclick={() => copy(e.url)} aria-label="Copy {e.url}" title="Copy">
            <Icon name={copied === e.url ? 'check' : 'copy'} size={12} />
          </button>
        {/if}
      </span>
    {/each}
    {#if !full && sorted.length > shown.length}<span class="muted xs">+{sorted.length - shown.length}</span>{/if}
    <span class="sr-only" aria-live="polite">{copied ? 'Copied' : ''}</span>
  </span>
{/if}

<style>
  .eps { display: inline-flex; align-items: center; gap: 4px 10px; flex-wrap: wrap; min-width: 0; }
  .ep { display: inline-flex; align-items: center; gap: 4px; min-width: 0; }
  .ep a, .ep span.mono { font-size: 0.93em; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .ep a { text-decoration: none; }
  .ep a:hover { text-decoration: underline; }
  .cp { display: inline-grid; place-items: center; width: 22px; height: 22px; border: 0; border-radius: var(--radius-xs); background: none; color: var(--muted); cursor: pointer; }
  .cp:hover { background: var(--surface-2); color: var(--text); }
  .cp:focus-visible { outline: 2px solid var(--focus); outline-offset: 1px; }
</style>
