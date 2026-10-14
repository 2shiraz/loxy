import { getContext, setContext } from 'svelte';
import type { Frontend, Backend } from '$lib/api/client';

/** Shared state between a detail layout and its tab pages. */
export interface DetailCtx<T> {
  readonly item: T | undefined;
  readonly id: number;
  reload: () => Promise<void>;
  set: (v: T) => void;
}
const FE = Symbol('frontend');
const BE = Symbol('backend');
export const setFe = (c: DetailCtx<Frontend>) => setContext(FE, c);
export const getFe = () => getContext<DetailCtx<Frontend>>(FE);
export const setBe = (c: DetailCtx<Backend>) => setContext(BE, c);
export const getBe = () => getContext<DetailCtx<Backend>>(BE);
