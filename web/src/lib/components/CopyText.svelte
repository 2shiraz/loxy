<script lang="ts">
  import Icon from './Icon.svelte';
  let { text, label, mono = true }: { text: string | null | undefined; label?: string; mono?: boolean } = $props();
  let copied = $state(false);
  async function copy() {
    if (!text) return;
    try {
      await navigator.clipboard.writeText(text);
      copied = true;
      setTimeout(() => (copied = false), 1200);
    } catch { /* clipboard unavailable */ }
  }
</script>

{#if text}
  <button type="button" class="copy" class:mono onclick={copy} title="Copy {label ?? text}" aria-label="Copy {label ?? text}">
    <span class="t">{text}</span>
    <Icon name={copied ? 'check' : 'copy'} size={12} class={copied ? 'done' : 'ic'} />
    <span class="sr-only" aria-live="polite">{copied ? 'Copied' : ''}</span>
  </button>
{:else}
  <span class="muted">—</span>
{/if}

<style>
  .copy {
    display: inline-flex; align-items: center; gap: 6px; max-width: 100%; padding: 1px 4px; margin: -1px -4px;
    border: 0; border-radius: var(--radius-xs); background: none; cursor: pointer; color: inherit; font-size: inherit;
  }
  .copy.mono { font-family: var(--mono); font-size: 0.93em; }
  .copy:hover { background: var(--surface-2); }
  .t { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .copy :global(.ic) { color: var(--muted); opacity: 0; transition: opacity 100ms ease; }
  .copy:hover :global(.ic), .copy:focus-visible :global(.ic) { opacity: 1; }
  .copy :global(.done) { color: var(--ok); }
</style>
