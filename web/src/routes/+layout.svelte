<script lang="ts">
  import '$lib/styles/app.css';
  import { page } from '$app/state';
  import { goto } from '$app/navigation';
  import { onMount } from 'svelte';
  import { session, loadSession, logout } from '$lib/stores/session.svelte';
  import { live, connectLive, disconnectLive } from '$lib/stores/live.svelte';
  import { rate } from '$lib/util/format';
  import Toasts from '$lib/components/Toasts.svelte';
  import ConfirmTyped from '$lib/components/ConfirmTyped.svelte';
  import Icon from '$lib/components/Icon.svelte';
  import Menu from '$lib/components/Menu.svelte';
  import ThemeToggle from '$lib/components/ThemeToggle.svelte';
  import LiveStatus from '$lib/components/LiveStatus.svelte';
  import LiveNumber from '$lib/components/LiveNumber.svelte';
  import Sparkline from '$lib/components/Sparkline.svelte';
  import Logo from '$lib/components/Logo.svelte';

  let { children } = $props();

  interface NavItem { href: string; label: string; icon: string }
  const groups: NavItem[][] = [
    [
      { href: '/dashboard', label: 'Dashboard', icon: 'dashboard' },
      { href: '/projects', label: 'Projects', icon: 'lb' }
    ],
    [
      { href: '/domains', label: 'Domains', icon: 'globe' },
      { href: '/certificates', label: 'Certificates', icon: 'shield' },
      { href: '/hosts', label: 'Hosts', icon: 'server' }
    ],
    [
      { href: '/activity', label: 'Activity', icon: 'activity' },
      { href: '/system', label: 'System', icon: 'node' },
      { href: '/settings', label: 'Settings', icon: 'settings' }
    ]
  ];
  const all = groups.flat();
  // Phone bottom bar: the four most used places, the rest live in "More".
  const bottom = ['/dashboard', '/projects', '/domains', '/activity'].map((h) => all.find((n) => n.href === h)!);
  const more = all.filter((n) => !bottom.includes(n));

  const isLogin = $derived(page.url.pathname.startsWith('/login'));
  const isActive = (href: string) => page.url.pathname === href || page.url.pathname.startsWith(href + '/');
  const moreActive = $derived(more.some((n) => isActive(n.href)));

  let rail = $state(false);
  let sheetOpen = $state(false);
  let sheet: HTMLDialogElement | undefined = $state();

  onMount(() => {
    try { rail = localStorage.getItem('loxy.nav') === 'rail'; } catch { /* storage unavailable */ }
    if (isLogin) { session.checked = true; return; }
    void loadSession().then((u) => {
      if (!u) void goto(`/login?next=${encodeURIComponent(page.url.pathname)}`, { replaceState: true });
    });
  });

  function toggleRail() {
    rail = !rail;
    try { localStorage.setItem('loxy.nav', rail ? 'rail' : 'full'); } catch { /* ignore */ }
  }

  $effect(() => {
    if (session.user && !isLogin) connectLive();
    else disconnectLive();
  });

  $effect(() => { void page.url.pathname; sheetOpen = false; });
  $effect(() => {
    if (!sheet) return;
    if (sheetOpen && !sheet.open) sheet.showModal();
    else if (!sheetOpen && sheet.open) sheet.close();
  });

  const traffic = $derived(live.tick?.traffic);

  async function doLogout() {
    sheetOpen = false;
    await logout();
    void goto('/login');
  }
</script>

<a class="skip" href="#main">Skip to content</a>

{#if isLogin}
  {@render children()}
{:else}
  <div class="shell" class:rail class:stale={live.state !== 'open' || live.stale}>
    <aside class="sidebar" aria-label="Primary">
      <div class="brand-row">
        <a class="brand" href="/dashboard" aria-label="Loxy, go to dashboard"><Logo size={28} /><span class="label">Loxy</span></a>
        <button class="btn ghost icon sm collapse" onclick={toggleRail} aria-label={rail ? 'Expand sidebar' : 'Collapse sidebar'} aria-pressed={rail} title={rail ? 'Expand sidebar' : 'Collapse sidebar'}><Icon name="panel" size={16} /></button>
      </div>
      <nav aria-label="Main">
        {#each groups as g, gi (gi)}
          <ul>
            {#each g as n (n.href)}
              {@const active = isActive(n.href)}
              <li><a href={n.href} class:active aria-current={active ? 'page' : undefined} title={n.label}><Icon name={n.icon} size={18} /><span class="label">{n.label}</span></a></li>
            {/each}
          </ul>
        {/each}
      </nav>
      <div class="side-foot" aria-label="Traffic now">
        <div class="tf-head"><span class="label">All traffic</span><b class="label"><LiveNumber value={traffic?.requests_per_second} format={rate} /> req/s</b></div>
        <Sparkline values={live.trafficRps} height={28} label="Requests per second, last 3 minutes" />
      </div>
    </aside>

    <div class="main-col">
      <header class="topbar">
        <a class="brand m-only" href="/dashboard" aria-label="Loxy, go to dashboard"><Logo size={26} /><span>Loxy</span></a>
        <div class="spacer"></div>
        <LiveStatus />
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
              <span class="sr-only">Account</span>
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

    <nav class="bottombar" aria-label="Main">
      {#each bottom as n (n.href)}
        {@const active = isActive(n.href)}
        <a href={n.href} class:active aria-current={active ? 'page' : undefined}><Icon name={n.icon} size={20} /><span>{n.label}</span></a>
      {/each}
      <button class:active={moreActive} aria-haspopup="dialog" aria-expanded={sheetOpen} onclick={() => (sheetOpen = true)}><Icon name="more" size={20} /><span>More</span></button>
    </nav>

    <dialog class="sheet" bind:this={sheet} aria-label="More" onclose={() => (sheetOpen = false)} onclick={(e) => { if (e.target === sheet) sheetOpen = false; }}>
      {#if sheetOpen}
        <div class="grab" aria-hidden="true"></div>
        <ul>
          {#each more as n (n.href)}
            <li><a href={n.href} class:active={isActive(n.href)} aria-current={isActive(n.href) ? 'page' : undefined}><Icon name={n.icon} size={18} />{n.label}</a></li>
          {/each}
          <li><button onclick={doLogout}><Icon name="logout" size={18} />Sign out</button></li>
        </ul>
        <button class="btn sheet-close" onclick={() => (sheetOpen = false)}>Close</button>
      {/if}
    </dialog>
  </div>
{/if}

<Toasts />
<ConfirmTyped />

<style>
  .skip { position: absolute; left: -9999px; top: 8px; z-index: 200; background: var(--surface); padding: 8px 12px; border-radius: var(--radius-sm); box-shadow: var(--shadow-pop); }
  .skip:focus { left: 8px; }

  .shell { display: grid; grid-template-columns: var(--sidebar-w) minmax(0, 1fr); min-height: 100dvh; }
  .shell.rail { --sidebar-w: var(--rail-w); }

  /* Sidebar */
  .sidebar {
    position: sticky; top: 0; height: 100dvh; display: flex; flex-direction: column; gap: 6px;
    border-right: 1px solid var(--border); background: var(--surface); padding: 12px 12px 14px; overflow: hidden auto;
    transition: padding 200ms var(--ease-out);
  }
  .brand-row { display: flex; align-items: center; gap: 6px; height: 40px; margin-bottom: 10px; }
  .brand { display: flex; align-items: center; gap: 10px; padding: 0 4px; text-decoration: none; font-weight: 650; font-size: var(--fs-lg); letter-spacing: -0.01em; min-width: 0; }
  .brand-row .brand { flex: 1; }
  .collapse { color: var(--muted); flex: none; }
  nav ul { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 2px; }
  nav ul + ul { margin-top: 10px; padding-top: 10px; border-top: 1px solid var(--border); }
  .sidebar nav a {
    position: relative; display: flex; align-items: center; gap: 11px; height: 38px; padding: 0 10px; border-radius: var(--radius-sm);
    color: var(--text-2); text-decoration: none; font-weight: 550; white-space: nowrap;
    transition: background-color 120ms ease, color 120ms ease;
  }
  .sidebar nav a :global(.icon) { color: var(--muted); transition: color 120ms ease; }
  .sidebar nav a.active { color: var(--text); background: var(--surface-2); }
  .sidebar nav a.active :global(.icon) { color: var(--accent); }
  .sidebar nav a.active::before { content: ''; position: absolute; left: -12px; top: 9px; bottom: 9px; width: 3px; border-radius: 0 3px 3px 0; background: var(--accent); }
  @media (hover: hover) and (pointer: fine) {
    .sidebar nav a:hover { color: var(--text); background: var(--surface-2); }
  }
  .side-foot { margin-top: auto; padding: 12px; border-radius: var(--radius-sm); background: var(--bg); display: flex; flex-direction: column; gap: 8px; }
  .tf-head { display: flex; align-items: baseline; justify-content: space-between; gap: 8px; font-size: var(--fs-xs); color: var(--muted); white-space: nowrap; }
  .tf-head b { font-weight: 600; color: var(--text); font-size: var(--fs-sm); }

  /* Rail (collapsed) */
  .rail .sidebar { padding: 12px 10px 14px; }
  .rail .label { position: absolute; width: 1px; height: 1px; overflow: hidden; clip: rect(0 0 0 0); white-space: nowrap; }
  .rail .brand-row { flex-direction: column; height: auto; gap: 8px; }
  .rail .brand { padding: 0; }
  .rail .sidebar nav a { justify-content: center; padding: 0; }
  .rail .sidebar nav a.active::before { left: -10px; }
  .rail .side-foot { display: none; }

  /* Main column */
  .main-col { min-width: 0; display: flex; flex-direction: column; }
  .topbar {
    position: sticky; top: 0; z-index: 20; height: var(--topbar-h); display: flex; align-items: center; gap: 8px;
    padding: 0 var(--gutter); background: color-mix(in srgb, var(--bg) 86%, transparent);
    backdrop-filter: saturate(1.4) blur(10px); -webkit-backdrop-filter: saturate(1.4) blur(10px);
    border-bottom: 1px solid color-mix(in srgb, var(--border) 70%, transparent);
  }
  .m-only { display: none; }
  :global(.user-btn) { gap: 8px !important; padding: 0 8px 0 4px !important; }
  .avatar { display: grid; place-items: center; width: 26px; height: 26px; border-radius: 50%; background: var(--accent-soft); color: var(--accent); font-size: var(--fs-xs); font-weight: 700; }
  .email { max-width: 200px; overflow: hidden; text-overflow: ellipsis; font-size: var(--fs-sm); color: var(--text-2); }
  main { padding: 28px var(--gutter) 72px; min-width: 0; width: 100%; outline: none; }
  .boot { height: 40vh; }

  /* While the stream is down, live values are last-known: say so by fading them. */
  .shell :global(.ln) { transition: opacity 300ms ease; }
  .stale :global(.ln) { opacity: 0.5; }

  /* Bottom bar + sheet are phone-only */
  .bottombar, .sheet { display: none; }

  /* Tablet: rail by default, no collapse control */
  @media (min-width: 700px) and (max-width: 1099px) {
    .shell { --sidebar-w: var(--rail-w); }
    .sidebar { padding: 12px 10px 14px; }
    .label { position: absolute; width: 1px; height: 1px; overflow: hidden; clip: rect(0 0 0 0); white-space: nowrap; }
    .brand-row { flex-direction: column; height: auto; gap: 8px; }
    .brand { padding: 0; }
    .collapse { display: none; }
    .sidebar nav a { justify-content: center; padding: 0; }
    .sidebar nav a.active::before { left: -10px; }
    .side-foot { display: none; }
  }

  /* Phone */
  @media (max-width: 699px) {
    .shell { grid-template-columns: minmax(0, 1fr); }
    .sidebar { display: none; }
    .m-only { display: flex; }
    .topbar .brand { padding: 0; gap: 8px; font-size: var(--fs-lg); }
    .email { display: none; }
    main { padding: 20px var(--gutter) calc(var(--bottombar-h) + 32px + env(safe-area-inset-bottom)); }
    .bottombar {
      position: fixed; z-index: 30; left: 0; right: 0; bottom: 0; display: grid; grid-template-columns: repeat(5, 1fr);
      height: calc(var(--bottombar-h) + env(safe-area-inset-bottom)); padding-bottom: env(safe-area-inset-bottom);
      background: color-mix(in srgb, var(--surface) 92%, transparent); backdrop-filter: blur(12px); -webkit-backdrop-filter: blur(12px);
      border-top: 1px solid var(--border);
    }
    .bottombar a, .bottombar button {
      display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 3px;
      border: 0; background: none; color: var(--muted); font-size: 11px; font-weight: 550; text-decoration: none; cursor: pointer;
      -webkit-tap-highlight-color: transparent; transition: color 120ms ease;
    }
    .bottombar :global(.icon) { transition: transform 160ms var(--ease-out); }
    .bottombar a:active :global(.icon), .bottombar button:active :global(.icon) { transform: scale(0.9); }
    .bottombar .active { color: var(--accent); }

    .sheet[open] {
      display: block; margin: auto 0 0; width: 100%; max-width: 100%; max-height: 80dvh; padding: 8px 12px calc(16px + env(safe-area-inset-bottom));
      border: 0; border-radius: 18px 18px 0 0; background: var(--surface); color: var(--text); box-shadow: var(--shadow-pop); overscroll-behavior: contain; overflow: auto;
      animation: sheet-in 260ms var(--ease-drawer);
    }
    .sheet::backdrop { background: var(--overlay); }
    .grab { width: 36px; height: 4px; border-radius: 4px; background: var(--border-strong); margin: 4px auto 10px; }
    .sheet ul { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 2px; }
    .sheet a, .sheet ul button {
      display: flex; align-items: center; gap: 12px; width: 100%; height: 48px; padding: 0 12px; border: 0; border-radius: var(--radius-sm);
      background: none; color: var(--text); font-size: var(--fs-lg); font-weight: 550; text-decoration: none; cursor: pointer; text-align: left;
    }
    .sheet a :global(.icon), .sheet ul button :global(.icon) { color: var(--muted); }
    .sheet a.active { background: var(--surface-2); }
    .sheet a.active :global(.icon) { color: var(--accent); }
    .sheet-close { width: 100%; margin-top: 10px; height: 44px; }
  }
  @keyframes sheet-in { from { transform: translateY(100%); } }
</style>
