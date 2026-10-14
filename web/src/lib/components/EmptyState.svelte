<script lang="ts">
  import type { Snippet } from 'svelte';
  import Icon from './Icon.svelte';
  let { title, text, icon = 'info', children }: { title: string; text?: string; icon?: string; children?: Snippet } = $props();
</script>

<div class="empty">
  <span class="ic"><Icon name={icon} size={20} /></span>
  <h3>{title}</h3>
  {#if text}<p>{text}</p>{/if}
  {#if children}<div class="act">{@render children()}</div>{/if}
</div>

<style>
  .empty { display: flex; flex-direction: column; align-items: center; text-align: center; gap: 6px; padding: 36px 20px; }
  .ic { display: grid; place-items: center; width: 44px; height: 44px; border-radius: 12px; background: var(--accent-soft); color: var(--accent); margin-bottom: 6px; }
  h3 { font-size: var(--fs-lg); }
  p { color: var(--text-2); max-width: 48ch; font-size: var(--fs-md); }
  .act { margin-top: 10px; display: flex; gap: 8px; flex-wrap: wrap; justify-content: center; }
</style>
