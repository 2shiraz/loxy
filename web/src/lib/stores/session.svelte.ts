import { api, setCsrf, type User } from '$lib/api/client';

export const session = $state<{ user: User | null; checked: boolean }>({ user: null, checked: false });

export async function loadSession(): Promise<User | null> {
  try {
    const r = await api.GET('/auth/me');
    if (r.data) {
      session.user = r.data.user;
      setCsrf(r.data.csrf_token);
    } else {
      session.user = null;
    }
  } catch {
    session.user = null;
  }
  session.checked = true;
  return session.user;
}

export async function logout() {
  try { await api.POST('/auth/logout'); } catch { /* ignore */ }
  session.user = null;
  setCsrf('');
}
