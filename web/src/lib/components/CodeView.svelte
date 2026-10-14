<script lang="ts">
  let { text, diff = false, maxHeight = 520, label = 'Code' }: { text: string; diff?: boolean; maxHeight?: number; label?: string } = $props();
  const lines = $derived(text.split('\n'));
  function cls(l: string) {
    if (!diff) return '';
    if (l.startsWith('+++') || l.startsWith('---')) return 'meta';
    if (l.startsWith('@@')) return 'hunk';
    if (l.startsWith('+')) return 'add';
    if (l.startsWith('-')) return 'del';
    return '';
  }
</script>

<!-- svelte-ignore a11y_no_noninteractive_tabindex -->
<pre class="code" style="max-height: {maxHeight}px" aria-label={label} tabindex="0">{#each lines as l, i (i)}<span class="ln {cls(l)}">{l || ' '}</span>
{/each}</pre>

<style>
  .code {
    margin: 0; padding: 10px 0; overflow: auto; background: var(--bg); border: 1px solid var(--border);
    border-radius: var(--radius-sm); font: 12px/1.55 var(--mono); color: var(--text-2); tab-size: 4;
  }
  .ln { display: inline-block; min-width: 100%; padding: 0 14px; white-space: pre; }
  .add { background: var(--ok-soft); color: var(--text); }
  .del { background: var(--err-soft); color: var(--text); }
  .hunk { color: var(--info); }
  .meta { color: var(--muted); }
</style>
