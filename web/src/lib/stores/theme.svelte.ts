export type ThemePref = 'system' | 'dark' | 'light';

function read(): ThemePref {
  try {
    const t = localStorage.getItem('loxy.theme');
    if (t === 'dark' || t === 'light') return t;
  } catch { /* ignore */ }
  return 'system';
}

export const theme = $state<{ pref: ThemePref }>({ pref: read() });

export function setTheme(p: ThemePref) {
  theme.pref = p;
  try {
    if (p === 'system') localStorage.removeItem('loxy.theme');
    else localStorage.setItem('loxy.theme', p);
  } catch { /* ignore */ }
  if (p === 'system') delete document.documentElement.dataset.theme;
  else document.documentElement.dataset.theme = p;
}

export function effectiveTheme(): 'dark' | 'light' {
  if (theme.pref !== 'system') return theme.pref;
  return matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark';
}
