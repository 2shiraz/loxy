import { getContext, setContext } from 'svelte';
import type { Project } from '$lib/api/client';

/** Shared state between the project layout and its tab pages. */
export interface ProjectCtx {
  readonly project: Project | undefined;
  readonly id: number;
  reload: () => Promise<void>;
  set: (p: Project) => void;
}
const KEY = Symbol('project');
export const setProject = (c: ProjectCtx) => setContext(KEY, c);
export const getProject = () => getContext<ProjectCtx>(KEY);
