<script lang="ts" module>
  const P: Record<string, string> = {
    lb: 'M4 6h16M4 12h16M4 18h16M8 6v12',
    dashboard: 'M4 4h7v7H4zM13 4h7v4h-7zM13 10h7v10h-7zM4 13h7v7H4z',
    frontend: 'M3 12h7M7 8l4 4-4 4M14 4h6v16h-6',
    backend: 'M4 5h16v4H4zM4 15h16v4H4zM12 9v6M8 12h8',
    host: 'M4 5h16v10H4zM9 19h6M12 15v4',
    arrowUp: 'M12 19V5M6 11l6-6 6 6',
    arrowDown: 'M12 5v14M6 13l6 6 6-6',
    globe: 'M12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18Zm-9 9h18M12 3c2.5 2.6 3.8 5.6 3.8 9s-1.3 6.4-3.8 9c-2.5-2.6-3.8-5.6-3.8-9S9.5 5.6 12 3Z',
    shield: 'M12 3 5 6v6c0 4.2 2.9 7.6 7 9 4.1-1.4 7-4.8 7-9V6l-7-3Z',
    server: 'M4 4h16v6H4zM4 14h16v6H4zM8 7h.01M8 17h.01',
    network: 'M12 3v6M5 21v-4h14v4M12 9v8M9 3h6v6H9z',
    node: 'M5 5h14v14H5zM9 9h6v6H9zM9 2v3M15 2v3M9 19v3M15 19v3M2 9h3M2 15h3M19 9h3M19 15h3',
    activity: 'M3 12h4l3 7 4-14 3 7h4',
    settings: 'M12 9a3 3 0 1 0 0 6 3 3 0 0 0 0-6Zm7.4 3c0-.5 0-1-.1-1.4l2-1.6-2-3.4-2.4 1a7.4 7.4 0 0 0-2.4-1.4L14 3h-4l-.5 2.6c-.9.3-1.7.8-2.4 1.4l-2.4-1-2 3.4 2 1.6a7.6 7.6 0 0 0 0 2.8l-2 1.6 2 3.4 2.4-1c.7.6 1.5 1.1 2.4 1.4L10 21h4l.5-2.6c.9-.3 1.7-.8 2.4-1.4l2.4 1 2-3.4-2-1.6c.1-.4.1-.9.1-1.4Z',
    plus: 'M12 5v14M5 12h14',
    x: 'M6 6l12 12M18 6 6 18',
    check: 'M5 12.5l4.5 4.5L19 7.5',
    chevron: 'M6 9l6 6 6-6',
    chevronRight: 'M9 6l6 6-6 6',
    more: 'M5 12h.01M12 12h.01M19 12h.01',
    lock: 'M7 11V8a5 5 0 0 1 10 0v3M5 11h14v10H5z',
    unlock: 'M7 11V8a5 5 0 0 1 9.6-2M5 11h14v10H5z',
    copy: 'M9 9h11v11H9zM5 15H4V4h11v1',
    search: 'M11 4a7 7 0 1 0 0 14 7 7 0 0 0 0-14Zm9 16-4-4',
    tag: 'M3 12V4h8l10 10-8 8L3 12Zm4.5-4.5h.01',
    alert: 'M12 4 2.5 20h19L12 4Zm0 6v4m0 3h.01',
    info: 'M12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18Zm0 8v5m0-8h.01',
    trash: 'M4 7h16M9 7V4h6v3M6 7l1 13h10l1-13',
    edit: 'M4 20h4L19 9l-4-4L4 16v4Z',
    download: 'M12 4v11m-5-5 5 5 5-5M4 20h16',
    refresh: 'M20 11a8 8 0 0 0-14.7-4M4 4v4h4M4 13a8 8 0 0 0 14.7 4M20 20v-4h-4',
    sun: 'M12 8a4 4 0 1 0 0 8 4 4 0 0 0 0-8Zm0-6v2m0 16v2M4.9 4.9l1.4 1.4m11.4 11.4 1.4 1.4M2 12h2m16 0h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4',
    moon: 'M20 14.5A8 8 0 0 1 9.5 4a8 8 0 1 0 10.5 10.5Z',
    logout: 'M15 4h4v16h-4M10 8l-4 4 4 4M6 12h11',
    cert: 'M4 4h16v12H4zM8 8h8M8 11h5M14 16l-1 5 3-2 3 2-1-5',
    dns: 'M4 5h16v5H4zM4 14h16v5H4zM8 7.5h.01M8 16.5h.01M12 7.5h5M12 16.5h5',
    pulse: 'M12 12m-3 0a3 3 0 1 0 6 0 3 3 0 1 0-6 0',
    menu: 'M4 7h16M4 12h16M4 17h16',
    external: 'M14 4h6v6M20 4l-9 9M18 14v6H4V6h6',
    play: 'M7 5v14l11-7L7 5Z',
    pause: 'M8 5v14M16 5v14',
    triangleUp: 'M12 5l8 14H4l8-14Z',
    circle: 'M12 4a8 8 0 1 0 0 16 8 8 0 0 0 0-16Z',
    minus: 'M5 12h14'
  };
  export type IconName = keyof typeof P;
</script>

<script lang="ts">
  let { name, size = 16, label, class: cls = '' }: { name: string; size?: number; label?: string; class?: string } = $props();
</script>

<svg
  class="icon {cls}"
  width={size}
  height={size}
  viewBox="0 0 24 24"
  fill="none"
  stroke="currentColor"
  stroke-width="1.75"
  stroke-linecap="round"
  stroke-linejoin="round"
  role={label ? 'img' : undefined}
  aria-label={label}
  aria-hidden={label ? undefined : 'true'}
  focusable="false"><path d={P[name] ?? P.circle} /></svg>

<style>
  .icon { flex: none; display: inline-block; vertical-align: middle; }
</style>
