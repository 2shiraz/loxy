import { untrack } from 'svelte';
import { tick } from './live.svelte';

export interface Resource<T> {
  readonly data: T | undefined;
  readonly error: string;
  readonly loading: boolean;
  reload: () => Promise<void>;
}

/**
 * Fetch data inside a component and refetch when `deps()` changes or any of
 * the live invalidation keys tick. Must be called during component init.
 */
export function resource<T>(fetcher: () => Promise<T>, opts: { keys?: () => string[]; deps?: () => unknown } = {}): Resource<T> {
  const r = $state<{ data: T | undefined; error: string; loading: boolean }>({ data: undefined, error: '', loading: true });
  let seq = 0;
  async function run() {
    const my = ++seq;
    try {
      const d = await fetcher();
      if (my === seq) { r.data = d; r.error = ''; }
    } catch (e) {
      if (my === seq) r.error = (e as Error).message || 'Failed to load';
    } finally {
      if (my === seq) r.loading = false;
    }
  }
  $effect(() => {
    opts.deps?.();
    for (const k of opts.keys?.() ?? []) tick(k);
    untrack(() => void run());
  });
  return {
    get data() { return r.data; },
    get error() { return r.error; },
    get loading() { return r.loading; },
    reload: run
  };
}
