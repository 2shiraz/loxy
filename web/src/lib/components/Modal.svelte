<script lang="ts">
  import type { Snippet } from 'svelte';
  import Icon from './Icon.svelte';

  let {
    open = $bindable(false),
    title,
    width = 520,
    children,
    footer,
    onclose
  }: { open?: boolean; title: string; width?: number; children: Snippet; footer?: Snippet; onclose?: () => void } = $props();

  let dlg: HTMLDialogElement | undefined = $state();
  const id = 'm' + Math.random().toString(36).slice(2, 8);

  $effect(() => {
    if (!dlg) return;
    if (open && !dlg.open) dlg.showModal();
    else if (!open && dlg.open) dlg.close();
  });

  function close() {
    open = false;
    onclose?.();
  }
</script>

<dialog
  bind:this={dlg}
  class="modal"
  style:--w="{width}px"
  aria-labelledby={id}
  onclose={() => { if (open) close(); }}
  onclick={(e) => { if (e.target === dlg) close(); }}
>
  {#if open}
    <div class="inner">
      <header>
        <h2 id={id}>{title}</h2>
        <button class="btn ghost icon sm" aria-label="Close dialog" onclick={close}><Icon name="x" size={14} /></button>
      </header>
      <div class="body">{@render children()}</div>
      {#if footer}<footer>{@render footer()}</footer>{/if}
    </div>
  {/if}
</dialog>

<style>
  .modal {
    padding: 0; border: 0; border-radius: 12px; background: var(--surface); color: var(--text);
    width: min(var(--w), calc(100vw - 24px)); max-height: calc(100dvh - 48px);
    box-shadow: var(--shadow-pop);
  }
  .modal[open] { animation: pop 180ms var(--ease-out); }
  .modal::backdrop { background: var(--overlay); }
  .inner { display: flex; flex-direction: column; max-height: calc(100dvh - 48px); }
  header { display: flex; align-items: center; gap: 8px; padding: 14px 16px 10px 20px; }
  header h2 { flex: 1; font-size: var(--fs-lg); }
  .body { padding: 6px 20px 18px; overflow: auto; }
  footer { display: flex; justify-content: flex-end; gap: 8px; padding: 12px 20px; border-top: 1px solid var(--border); background: var(--surface); }
  @keyframes pop { from { opacity: 0; transform: scale(0.97); } }
</style>
