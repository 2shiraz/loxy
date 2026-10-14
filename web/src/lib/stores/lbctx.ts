import { getContext, setContext } from 'svelte';
import type { LoadBalancer } from '$lib/api/client';

export interface LbCtx {
  readonly lb: LoadBalancer | undefined;
  readonly id: number;
  reload: () => Promise<void>;
  set: (lb: LoadBalancer) => void;
}
const KEY = Symbol('lb');
export const setLb = (c: LbCtx) => setContext(KEY, c);
export const getLb = () => getContext<LbCtx>(KEY);
