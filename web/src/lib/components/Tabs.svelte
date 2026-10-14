<script lang="ts">
  /** Link tabs (route-based) or button tabs (state-based). Scrolls sideways on narrow screens. */
  interface Tab { id: string; label: string; href?: string; count?: number }
  let { tabs, active = $bindable(), label = 'Sections' }: { tabs: Tab[]; active: string; label?: string } = $props();
  let bar: HTMLDivElement | undefined = $state();

  function onkey(e: KeyboardEvent, i: number) {
    if (e.key !== 'ArrowRight' && e.key !== 'ArrowLeft') return;
    e.preventDefault();
    const n = (i + (e.key === 'ArrowRight' ? 1 : -1) + tabs.length) % tabs.length;
    const el = (e.currentTarget as HTMLElement).parentElement?.children[n] as HTMLElement | undefined;
    el?.focus();
    if (!tabs[n].href) active = tabs[n].id;
  }
  // Keep the active tab in view when the bar scrolls (phones).
  $effect(() => {
    void active;
    const el = bar?.querySelector<HTMLElement>('.tab.active');
    if (el && bar && bar.scrollWidth > bar.clientWidth) el.scrollIntoView({ block: 'nearest', inline: 'center' });
  });
</script>

<div class="tabs" bind:this={bar} role={tabs[0]?.href ? undefined : 'tablist'} aria-label={label}>
  {#each tabs as t, i (t.id)}
    {#if t.href}
      <a href={t.href} class="tab" class:active={active === t.id} aria-current={active === t.id ? 'page' : undefined} onkeydown={(e) => onkey(e, i)} data-sveltekit-noscroll>
        {t.label}{#if t.count != null}<span class="count">{t.count}</span>{/if}
      </a>
    {:else}
      <button role="tab" class="tab" class:active={active === t.id} aria-selected={active === t.id} tabindex={active === t.id ? 0 : -1} onclick={() => (active = t.id)} onkeydown={(e) => onkey(e, i)}>
        {t.label}{#if t.count != null}<span class="count">{t.count}</span>{/if}
      </button>
    {/if}
  {/each}
</div>

<style>
  .tabs { display: flex; gap: 4px; border-bottom: 1px solid var(--border); overflow-x: auto; scrollbar-width: none; scroll-padding-inline: 16px; }
  .tabs::-webkit-scrollbar { display: none; }
  .tab {
    position: relative; display: inline-flex; align-items: center; gap: 7px; height: 42px; padding: 0 12px;
    border: 0; background: none; color: var(--muted); font-size: var(--fs-md); font-weight: 550;
    text-decoration: none; white-space: nowrap; cursor: pointer; flex: none;
    transition: color 120ms ease;
  }
  .tab:hover { color: var(--text); }
  .tab.active { color: var(--text); }
  .tab.active::after { content: ''; position: absolute; left: 10px; right: 10px; bottom: -1px; height: 2px; background: var(--accent); border-radius: 2px 2px 0 0; }
  .tab:focus-visible { outline-offset: -2px; }
  .count { font-size: var(--fs-xs); color: var(--muted); background: var(--surface-2); border-radius: 999px; min-width: 20px; height: 18px; padding: 0 6px; display: inline-grid; place-items: center; font-variant-numeric: tabular-nums; }
  .tab.active .count { color: var(--accent); background: var(--accent-soft); }
</style>
