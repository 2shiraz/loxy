export interface ConfirmOpts { title: string; message: string; confirmLabel?: string; danger?: boolean; typed?: string }
export const confirmState = $state<{ open: boolean; opts: ConfirmOpts | null; resolve: ((v: boolean) => void) | null }>({ open: false, opts: null, resolve: null });

/** Promise-based confirm dialog. With `typed`, the user must type that exact text. */
export function confirm(opts: ConfirmOpts): Promise<boolean> {
  confirmState.resolve?.(false);
  return new Promise((resolve) => {
    confirmState.opts = opts;
    confirmState.resolve = resolve;
    confirmState.open = true;
  });
}
export function settle(v: boolean) {
  const r = confirmState.resolve;
  confirmState.resolve = null;
  confirmState.open = false;
  r?.(v);
}
