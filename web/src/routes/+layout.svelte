<script lang="ts">
  import '$lib/styles/app.css';
  import { page } from '$app/state';
  import { goto } from '$app/navigation';
  import { onMount } from 'svelte';
  import { session, loadSession, logout } from '$lib/stores/session.svelte';
  import { live, connectLive, disconnectLive } from '$lib/stores/live.svelte';
  import Toasts from '$lib/components/Toasts.svelte';
  import ConfirmTyped from '$lib/components/ConfirmTyped.svelte';
  import Icon from '$lib/components/Icon.svelte';
  import Menu from '$lib/components/Menu.svelte';
  import ThemeToggle from '$lib/components/ThemeToggle.svelte';

  let { children } = $props();

  const nav = [
    { href: '/dashboard', label: 'Dashboard', icon: 'dashboard' },
    { href: '/frontends', label: 'Frontends', icon: 'frontend' },
    { href: '/backends', label: 'Backends', icon: 'backend' },
    { href: '/hosts', label: 'Hosts', icon: 'host' },
    { href: '/certificates', label: 'Certificates', icon: 'cert' },
    { href: '/domains', label: 'Domains', icon: 'dns' },
    { href: '/activity', label: 'Activity', icon: 'activity' },
    { href: '/system', label: 'System', icon: 'node' },
    { href: '/settings', label: 'Settings', icon: 'settings' }
  ];

  const isLogin = $derived(page.url.pathname.startsWith('/login'));
  let navOpen = $state(false);

  onMount(() => {
    if (isLogin) { session.checked = true; return; }
    void loadSession().then((u) => {
      if (!u) void goto(`/login?next=${encodeURIComponent(page.url.pathname)}`, { replaceState: true });
    });
  });

  $effect(() => {
    if (session.user && !isLogin) connectLive();
    else disconnectLive();
  });

  $effect(() => { void page.url.pathname; navOpen = false; });

  const liveLabel = $derived(live.state === 'open' ? 'Live' : live.state === 'connecting' ? 'Reconnecting' : 'Offline');

  async function doLogout() {
    await logout();
    void goto('/login');
  }
</script>

<a class="skip" href="#main">Skip to content</a>

{#if isLogin}
  {@render children()}
{:else}
  <div class="shell" class:nav-open={navOpen}>
    <aside class="sidebar" aria-label="Primary">
      <a class="brand" href="/dashboard" aria-label="Loxy home">
        <svg width="22" height="22" viewBox="0 0 32 32" aria-hidden="true"><rect width="32" height="32" rx="7" fill="var(--surface-3)"/><path d="M9 8v16h6M17 12l6 6m0-6-6 6" stroke="var(--accent)" stroke-width="2.6" fill="none" stroke-linecap="round" stroke-linejoin="round"/></svg>
        <span>Loxy</span>
      </a>
      <nav>
        {#each nav as n (n.href)}
          {@const active = page.url.pathname.startsWith(n.href)}
          <a href={n.href} class:active aria-current={active ? 'page' : undefined}><Icon name={n.icon} />{n.label}</a>
        {/each}
      </nav>
    </aside>
    {#if navOpen}<button class="scrim" aria-label="Close navigation" onclick={() => (navOpen = false)}></button>{/if}

    <div class="main-col">
      <header class="topbar">
        <button class="btn ghost icon menu-btn" aria-label="Open navigation" aria-expanded={navOpen} onclick={() => (navOpen = !navOpen)}><Icon name="menu" /></button>
        <div class="spacer"></div>
        <span class="live {live.state}" role="status" title="Event stream: {liveLabel}">
          <span class="ldot" aria-hidden="true"></span>{liveLabel}
        </span>
        <ThemeToggle />
        {#if session.user}
          <Menu
            label="Account"
            btnClass="btn ghost user-btn"
            items={[
              { label: 'Settings', onclick: () => goto('/settings') },
              { label: 'Sign out', onclick: doLogout, separator: true }
            ]}
          >
            {#snippet trigger()}
              <span class="avatar" aria-hidden="true">{session.user?.email.slice(0, 1).toUpperCase()}</span>
              <span class="email">{session.user?.email}</span>
              <Icon name="chevron" size={14} />
            {/snippet}
          </Menu>
        {/if}
      </header>
      <main id="main" tabindex="-1">
        {#if session.user}
          {@render children()}
        {:else}
          <div class="boot" aria-busy="true"><span class="sr-only">Loading</span></div>
        {/if}
      </main>
    </div>
  </div>
{/if}

<Toasts />
<ConfirmTyped />

<style>
  .skip { position: absolute; left: -9999px; top: 8px; z-index: 200; background: var(--surface); padding: 8px 12px; border-radius: var(--radius-sm); }
  .skip:focus { left: 8px; }
  .shell { display: grid; grid-template-columns: var(--sidebar-w) minmax(0, 1fr); min-height: 100dvh; }
  .sidebar {
    position: sticky; top: 0; height: 100dvh; display: flex; flex-direction: column; gap: 4px;
    border-right: 1px solid var(--border); background: var(--bg); padding: 10px 10px 16px;
  }
  .brand { display: flex; align-items: center; gap: 10px; height: 36px; padding: 0 8px; margin-bottom: 12px; text-decoration: none; font-weight: 600; font-size: var(--fs-lg); letter-spacing: -0.01em; }
  nav { display: flex; flex-direction: column; gap: 1px; }
  nav a {
    display: flex; align-items: center; gap: 10px; height: 32px; padding: 0 10px; border-radius: var(--radius-sm);
    color: var(--muted); text-decoration: none; font-weight: 500;
  }
  nav a:hover { color: var(--text); background: var(--surface); }
  nav a.active { color: var(--text); background: var(--surface-2); }
  nav a.active :global(.icon) { color: var(--accent); }
  .main-col { min-width: 0; display: flex; flex-direction: column; }
  .topbar {
    position: sticky; top: 0; z-index: 20; height: var(--topbar-h); display: flex; align-items: center; gap: 6px;
    padding: 0 16px; background: color-mix(in srgb, var(--bg) 88%, transparent); backdrop-filter: blur(8px);
    border-bottom: 1px solid var(--border);
  }
  .menu-btn { display: none; }
  .live { display: inline-flex; align-items: center; gap: 6px; font-size: var(--fs-sm); color: var(--muted); padding: 0 8px; }
  .ldot { width: 7px; height: 7px; border-radius: 50%; background: var(--unknown); }
  .live.open .ldot { background: var(--ok); box-shadow: 0 0 0 3px var(--ok-soft); }
  .live.connecting .ldot { background: var(--warn); }
  .live.closed .ldot { background: var(--err); }
  :global(.user-btn) { gap: 8px !important; padding: 0 6px !important; }
  .avatar { display: grid; place-items: center; width: 22px; height: 22px; border-radius: 50%; background: var(--surface-3); font-size: var(--fs-xs); font-weight: 600; color: var(--text); }
  .email { max-width: 180px; overflow: hidden; text-overflow: ellipsis; font-size: var(--fs-sm); }
  main { padding: 24px 28px 64px; max-width: 1280px; width: 100%; outline: none; }
  .boot { height: 40vh; }
  .scrim { display: none; }

  @media (max-width: 860px) {
    .shell { grid-template-columns: minmax(0, 1fr); }
    .sidebar { position: fixed; inset: 0 auto 0 0; z-index: 50; width: 240px; transform: translateX(-100%); transition: transform 200ms var(--ease-out); }
    .nav-open .sidebar { transform: none; }
    .scrim { display: block; position: fixed; inset: 0; z-index: 40; background: var(--overlay); border: 0; }
    .menu-btn { display: inline-flex; }
    .email { display: none; }
    main { padding: 16px 16px 48px; }
  }
</style>
