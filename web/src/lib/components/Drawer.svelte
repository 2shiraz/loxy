<script lang="ts">
  import type { Snippet } from 'svelte';
  import Icon from './Icon.svelte';

  let { open = $bindable(false), title, children, actions }: { open?: boolean; title: string; children: Snippet; actions?: Snippet } = $props();
  let dlg: HTMLDialogElement | undefined = $state();

  $effect(() => {
    if (!dlg) return;
    if (open && !dlg.open) dlg.showModal();
    else if (!open && dlg.open) dlg.close();
  });
</script>

<dialog bind:this={dlg} class="drawer" aria-label={title} onclose={() => (open = false)} onclick={(e) => { if (e.target === dlg) open = false; }}>
  {#if open}
    <div class="inner">
      <header>
        <h2>{title}</h2>
        {#if actions}{@render actions()}{/if}
        <button class="btn ghost icon sm" aria-label="Close panel" onclick={() => (open = false)}><Icon name="x" size={14} /></button>
      </header>
      <div class="body">{@render children()}</div>
    </div>
  {/if}
</dialog>

<style>
  .drawer {
    margin: 0 0 0 auto; height: 100dvh; max-height: 100dvh; width: min(560px, 100vw);
    padding: 0; border: 0; border-left: 1px solid var(--border-strong);
    background: var(--surface); color: var(--text);
  }
  .drawer[open] { animation: slide 220ms var(--ease-out); }
  .drawer::backdrop { background: var(--overlay); }
  .inner { display: flex; flex-direction: column; height: 100%; }
  header { display: flex; align-items: center; gap: 8px; padding: 14px 16px 14px 20px; border-bottom: 1px solid var(--border); }
  header h2 { flex: 1; font-size: var(--fs-lg); overflow-wrap: anywhere; }
  .body { flex: 1; overflow: auto; overscroll-behavior: contain; padding: 18px 20px 28px; }
  @keyframes slide { from { transform: translateX(24px); opacity: 0; } }
</style>
