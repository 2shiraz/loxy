export type ToastKind = 'info' | 'success' | 'error';
export interface ToastItem { id: number; kind: ToastKind; message: string }

let seq = 0;
export const toasts = $state<{ items: ToastItem[] }>({ items: [] });

function push(kind: ToastKind, message: string, ms = kind === 'error' ? 7000 : 3500) {
  const id = ++seq;
  toasts.items = [...toasts.items.slice(-4), { id, kind, message }];
  setTimeout(() => dismiss(id), ms);
}
export function dismiss(id: number) {
  toasts.items = toasts.items.filter((t) => t.id !== id);
}
export const toast = {
  info: (m: string) => push('info', m),
  success: (m: string) => push('success', m),
  error: (m: string) => push('error', m)
};
