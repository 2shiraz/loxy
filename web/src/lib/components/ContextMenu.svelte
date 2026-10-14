<script lang="ts">
  import { tick } from 'svelte';
  import type { MenuItem } from './Menu.svelte';

  let { point = $bindable(null), items, label = 'Target actions' }: {
    point: { x: number; y: number } | null; items: MenuItem[]; label?: string;
  } = $props();
  let menu: HTMLDivElement | undefined = $state();
  let pos = $state({ x: 0, y: 0 });
  let previous: HTMLElement | null = null;
  function close(refocus = false) {
    point = null;
    if (refocus) previous?.focus();
  }
  function key(e: KeyboardEvent) {
    const buttons = [...(menu?.querySelectorAll<HTMLButtonElement>('button:not(:disabled)') ?? [])];
    const index = buttons.indexOf(document.activeElement as HTMLButtonElement);
    if (['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(e.key)) {
      e.preventDefault();
      const next = e.key === 'Home' ? 0 : e.key === 'End' ? buttons.length - 1 : index + (e.key === 'ArrowDown' ? 1 : -1);
      buttons[(next + buttons.length) % buttons.length]?.focus();
    } else if (e.key === 'Escape') { e.preventDefault(); close(true); }
    else if (e.key === 'Tab') close();
  }
  $effect(() => {
    const anchor = point;
    if (!anchor) return;
    previous = document.activeElement as HTMLElement;
    pos = anchor;
    let cancelled = false;
    void tick().then(() => {
      if (cancelled || !menu) return;
      const box = menu.getBoundingClientRect();
      pos = { x: Math.max(8, Math.min(anchor.x, innerWidth - box.width - 8)), y: Math.max(8, Math.min(anchor.y, innerHeight - box.height - 8)) };
      menu.querySelector<HTMLButtonElement>('button:not(:disabled)')?.focus();
    });
    const outside = (e: MouseEvent) => { if (!menu?.contains(e.target as Node)) close(); };
    const dismiss = () => close();
    document.addEventListener('mousedown', outside);
    window.addEventListener('resize', dismiss);
    window.addEventListener('scroll', dismiss, true);
    return () => {
      cancelled = true;
      document.removeEventListener('mousedown', outside);
      window.removeEventListener('resize', dismiss);
      window.removeEventListener('scroll', dismiss, true);
    };
  });
</script>

{#if point}
  <div class="context-menu" role="menu" aria-label={label} tabindex="-1" bind:this={menu} onkeydown={key} style:left="{pos.x}px" style:top="{pos.y}px">
    {#each items as item}
      {#if item.separator}<div class="sep" role="separator"></div>{/if}
      <button role="menuitem" class:danger={item.danger} disabled={item.disabled} onclick={() => { close(true); item.onclick(); }}>{item.label}</button>
    {/each}
  </div>
{/if}

<style>
  .context-menu { position: fixed; z-index: 70; width: 220px; max-width: calc(100vw - 16px); max-height: calc(100dvh - 16px); overflow-y: auto; padding: 5px; border-radius: 10px; background: var(--surface); box-shadow: var(--shadow-pop); }
  button { display: block; width: 100%; min-height: 36px; padding: 6px 10px; border: 0; border-radius: 6px; text-align: left; background: none; color: var(--text); cursor: pointer; }
  button:hover:not(:disabled), button:focus-visible { background: var(--surface-2); }
  button:focus-visible { outline: 2px solid var(--focus); outline-offset: -2px; }
  button.danger { color: var(--err); }
  button:disabled { color: var(--muted); cursor: not-allowed; }
  .sep { height: 1px; margin: 4px 2px; background: var(--border); }
  @media (max-width: 699px) { button { min-height: 44px; } }
</style>
