<script lang="ts" module>
  export interface MenuItem { label: string; onclick: () => void; danger?: boolean; disabled?: boolean; separator?: boolean }
</script>

<script lang="ts">
  import type { Snippet } from 'svelte';
  import Icon from './Icon.svelte';

  let {
    items,
    label = 'More actions',
    align = 'right',
    trigger,
    btnClass = 'btn ghost icon sm'
  }: { items: MenuItem[]; label?: string; align?: 'left' | 'right'; trigger?: Snippet; btnClass?: string } = $props();

  let open = $state(false);
  let root: HTMLDivElement | undefined = $state();
  let menu: HTMLDivElement | undefined = $state();
  const id = 'mn' + Math.random().toString(36).slice(2, 8);

  let pos = $state({ top: 0, left: 0, right: 0, up: false });
  function place() {
    const b = root?.querySelector('button')?.getBoundingClientRect();
    if (!b) return;
    const up = b.bottom + 260 > innerHeight && b.top > 260;
    pos = { top: up ? b.top - 4 : b.bottom + 4, left: b.left, right: innerWidth - b.right, up };
  }
  function toggle() {
    open = !open;
    if (open) { place(); queueMicrotask(() => focusItem(0)); }
  }
  function focusItem(i: number) {
    const els = menu?.querySelectorAll<HTMLButtonElement>('[role=menuitem]:not(:disabled)');
    if (!els?.length) return;
    els[(i + els.length) % els.length].focus();
  }
  function onkey(e: KeyboardEvent) {
    const els = [...(menu?.querySelectorAll<HTMLButtonElement>('[role=menuitem]:not(:disabled)') ?? [])];
    const cur = els.indexOf(document.activeElement as HTMLButtonElement);
    if (e.key === 'ArrowDown') { e.preventDefault(); focusItem(cur + 1); }
    else if (e.key === 'ArrowUp') { e.preventDefault(); focusItem(cur - 1); }
    else if (e.key === 'Home') { e.preventDefault(); focusItem(0); }
    else if (e.key === 'End') { e.preventDefault(); focusItem(-1); }
    else if (e.key === 'Escape') { e.preventDefault(); close(true); }
    else if (e.key === 'Tab') close(false);
  }
  function close(refocus: boolean) {
    open = false;
    if (refocus) root?.querySelector<HTMLButtonElement>('button')?.focus();
  }
  function pick(it: MenuItem) {
    close(true);
    it.onclick();
  }
  $effect(() => {
    if (!open) return;
    const h = (e: MouseEvent) => { if (root && !root.contains(e.target as Node)) open = false; };
    const c = () => (open = false);
    document.addEventListener('mousedown', h);
    addEventListener('scroll', c, true);
    addEventListener('resize', c);
    return () => { document.removeEventListener('mousedown', h); removeEventListener('scroll', c, true); removeEventListener('resize', c); };
  });
</script>

<div class="menu-root" bind:this={root}>
  <button class={btnClass} aria-haspopup="menu" aria-expanded={open} aria-controls={id} aria-label={trigger ? undefined : label} onclick={toggle}>
    {#if trigger}{@render trigger()}{:else}<Icon name="more" />{/if}
  </button>
  {#if open}
    <div class="menu {align}" class:up={pos.up} role="menu" id={id} tabindex="-1" bind:this={menu} onkeydown={onkey}
      style:top="{pos.top}px" style:left={align === 'left' ? `${pos.left}px` : null} style:right={align === 'right' ? `${pos.right}px` : null}>
      {#each items as it, i (i)}
        {#if it.separator}<div class="sep" role="separator"></div>{/if}
        <button role="menuitem" class:danger={it.danger} disabled={it.disabled} onclick={() => pick(it)}>{it.label}</button>
      {/each}
    </div>
  {/if}
</div>

<style>
  .menu-root { position: relative; display: inline-flex; }
  .menu {
    position: fixed; z-index: 60; min-width: 200px; padding: 5px;
    background: var(--surface); border-radius: 10px; box-shadow: var(--shadow-pop);
    animation: m 160ms var(--ease-out); transform-origin: top right;
  }
  .menu.left { transform-origin: top left; }
  .menu.up { transform: translateY(-100%); animation: none; }
  .menu button {
    display: block; width: 100%; text-align: left; min-height: 34px; padding: 6px 10px; border: 0; border-radius: 6px;
    background: none; cursor: pointer; font-size: var(--fs-md); white-space: nowrap; color: var(--text);
  }
  .menu button:hover:not(:disabled), .menu button:focus-visible { background: var(--surface-2); outline: none; }
  .menu button:focus-visible { box-shadow: inset 0 0 0 2px var(--focus); }
  @media (max-width: 699px) { .menu button { min-height: 42px; font-size: var(--fs-lg); } }
  .menu button.danger { color: var(--err); }
  .menu button:disabled { color: var(--muted); cursor: not-allowed; }
  .sep { height: 1px; background: var(--border); margin: 4px 2px; }
  @keyframes m { from { opacity: 0; transform: scale(0.97); } }
</style>
