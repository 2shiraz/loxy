<script lang="ts">
  /**
   * A number that eases to each new value (450 ms, ease-out) instead of jumping.
   * Tabular numerals keep the width steady; reduced motion snaps straight to the value.
   */
  import { Tween, prefersReducedMotion } from 'svelte/motion';
  import { cubicOut } from 'svelte/easing';

  let { value, format = (n: number) => Math.round(n).toLocaleString(), class: cls = '' }: {
    value: number | null | undefined;
    format?: (n: number) => string;
    class?: string;
  } = $props();

  const t = new Tween(0, { duration: 450, easing: cubicOut });
  let first = true;
  $effect(() => {
    const v = value ?? 0;
    if (first || prefersReducedMotion.current || !Number.isFinite(v)) { first = false; void t.set(v, { duration: 0 }); }
    else t.target = v;
  });
</script>

<span class="ln {cls}">{value == null ? '—' : format(t.current)}</span>

<style>
  .ln { font-variant-numeric: tabular-nums; }
</style>
