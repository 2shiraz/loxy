<script lang="ts">
  import { toasts, dismiss } from '$lib/stores/toast.svelte';
  import Icon from './Icon.svelte';
</script>

<div class="toasts" role="region" aria-label="Notifications" aria-live="polite">
  {#each toasts.items as t (t.id)}
    <div class="toast {t.kind}" role={t.kind === 'error' ? 'alert' : 'status'}>
      <Icon name={t.kind === 'error' ? 'alert' : t.kind === 'success' ? 'check' : 'info'} />
      <span class="msg">{t.message}</span>
      <button class="btn ghost icon sm" aria-label="Dismiss notification" onclick={() => dismiss(t.id)}><Icon name="x" size={14} /></button>
    </div>
  {/each}
</div>

<style>
  .toasts { position: fixed; right: 16px; bottom: 16px; z-index: 100; display: flex; flex-direction: column; gap: 8px; width: min(380px, calc(100vw - 32px)); }
  .toast {
    display: flex; align-items: flex-start; gap: 10px; padding: 10px 8px 10px 12px;
    background: var(--surface-2); border-radius: var(--radius); box-shadow: var(--shadow-pop);
    font-size: var(--fs-md); animation: in 200ms var(--ease-out);
  }
  .toast :global(.icon) { margin-top: 2px; }
  .toast.success :global(.icon:first-child) { color: var(--ok); }
  .toast.error :global(.icon:first-child) { color: var(--err); }
  .toast.info :global(.icon:first-child) { color: var(--info); }
  .msg { flex: 1; overflow-wrap: anywhere; }
  @keyframes in { from { opacity: 0; transform: translateY(6px); } }
</style>
