<script lang="ts">
  import type { Snippet } from 'svelte';
  import Icon from './Icon.svelte';
  let { title, text, icon = 'info', children }: { title: string; text?: string; icon?: string; children?: Snippet } = $props();
</script>

<div class="empty">
  <span class="ic"><Icon name={icon} size={18} /></span>
  <h3>{title}</h3>
  {#if text}<p>{text}</p>{/if}
  {#if children}<div class="act">{@render children()}</div>{/if}
</div>

<style>
  .empty { display: flex; flex-direction: column; align-items: center; text-align: center; gap: 6px; padding: 40px 20px; }
  .ic { display: grid; place-items: center; width: 36px; height: 36px; border-radius: 50%; background: var(--surface-2); color: var(--muted); margin-bottom: 4px; }
  h3 { font-size: var(--fs-md); }
  p { color: var(--muted); max-width: 46ch; font-size: var(--fs-md); }
  .act { margin-top: 8px; display: flex; gap: 8px; }
</style>
