<script lang="ts">
  /** Key/value editor for labels (§14.4 rules). */
  import type { Labels } from '$lib/api/client';
  import { labelError } from '$lib/util/validate';
  import Icon from './Icon.svelte';

  let { labels = $bindable({}), error = $bindable('') }: { labels?: Labels; error?: string } = $props();
  let rows = $state<{ k: string; v: string }[]>(Object.entries(labels ?? {}).map(([k, v]) => ({ k, v })));
  if (!rows.length) rows.push({ k: '', v: '' });

  $effect(() => {
    const out: Labels = {};
    let err = '';
    for (const r of rows) {
      if (!r.k && !r.v) continue;
      const e = labelError(r.k.trim(), r.v.trim());
      if (e && !err) err = e;
      if (out[r.k.trim()] !== undefined && !err) err = `Duplicate key “${r.k}”`;
      out[r.k.trim()] = r.v.trim();
    }
    labels = out;
    error = err;
  });
</script>

<div class="le">
  {#each rows as r, i (i)}
    <div class="lrow">
      <input class="input mono" placeholder="key" aria-label="Label key {i + 1}" bind:value={r.k} spellcheck="false" autocomplete="off" />
      <span class="eq">=</span>
      <input class="input mono" placeholder="value" aria-label="Label value {i + 1}" bind:value={r.v} spellcheck="false" autocomplete="off" />
      <button type="button" class="btn ghost icon" aria-label="Remove label {i + 1}" onclick={() => rows.splice(i, 1)}><Icon name="x" size={14} /></button>
    </div>
  {/each}
  <div class="row">
    <button type="button" class="btn sm" onclick={() => rows.push({ k: '', v: '' })}><Icon name="plus" size={13} />Add label</button>
    {#if error}<span class="ferr small err-text" role="alert">{error}</span>{/if}
  </div>
</div>

<style>
  .le { display: flex; flex-direction: column; gap: 6px; }
  .lrow { display: grid; grid-template-columns: 1fr auto 1fr auto; gap: 6px; align-items: center; }
  .eq { color: var(--muted); font-family: var(--mono); }
</style>
