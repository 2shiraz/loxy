<script lang="ts">
  /** Link tabs (route-based) or button tabs (state-based). */
  interface Tab { id: string; label: string; href?: string; count?: number }
  let { tabs, active = $bindable(), label = 'Sections' }: { tabs: Tab[]; active: string; label?: string } = $props();

  function onkey(e: KeyboardEvent, i: number) {
    if (e.key !== 'ArrowRight' && e.key !== 'ArrowLeft') return;
    e.preventDefault();
    const n = (i + (e.key === 'ArrowRight' ? 1 : -1) + tabs.length) % tabs.length;
    const el = (e.currentTarget as HTMLElement).parentElement?.children[n] as HTMLElement | undefined;
    el?.focus();
    if (!tabs[n].href) active = tabs[n].id;
  }
</script>

<div class="tabs" role={tabs[0]?.href ? undefined : 'tablist'} aria-label={label}>
  {#each tabs as t, i (t.id)}
    {#if t.href}
      <a href={t.href} class="tab" class:active={active === t.id} aria-current={active === t.id ? 'page' : undefined} onkeydown={(e) => onkey(e, i)}>
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
  .tabs { display: flex; gap: 2px; border-bottom: 1px solid var(--border); overflow-x: auto; scrollbar-width: none; }
  .tab {
    position: relative; display: inline-flex; align-items: center; gap: 6px; padding: 9px 12px 10px;
    border: 0; background: none; color: var(--muted); font-size: var(--fs-md); font-weight: 500;
    text-decoration: none; white-space: nowrap; cursor: pointer;
  }
  .tab:hover { color: var(--text); }
  .tab.active { color: var(--text); }
  .tab.active::after { content: ''; position: absolute; left: 8px; right: 8px; bottom: -1px; height: 2px; background: var(--text); border-radius: 2px; }
  .count { font-size: var(--fs-xs); color: var(--muted); background: var(--surface-2); border-radius: 999px; padding: 0 6px; }
</style>
