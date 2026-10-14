<script lang="ts">
  import { onDestroy, tick } from 'svelte';
  import { goto } from '$app/navigation';
  import Icon from '$lib/components/Icon.svelte';
  import EmptyState from '$lib/components/EmptyState.svelte';
  import ErrorBox from '$lib/components/ErrorBox.svelte';
  import Skeleton from '$lib/components/Skeleton.svelte';

  type Line = { n: number; ts: number; time: string; text: string };

  const MAX = 500;
  let { id }: { id: number } = $props();

  let status = $state<'connecting' | 'live' | 'reconnecting' | 'off' | 'error'>('connecting');
  let errMsg = $state('');
  let lines = $state.raw<Line[]>([]);
  let held = $state.raw<Line[]>([]);
  let paused = $state(false);
  let filter = $state('');
  let stick = $state(true);
  let box: HTMLDivElement | undefined = $state();

  let es: EventSource | undefined;
  let probe: AbortController | undefined;
  let seq = 0;
  let gen = 0;

  const pad = (n: number) => String(n).padStart(2, '0');
  function hms(ms: number) {
    const d = new Date(ms);
    return `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
  }

  const q = $derived(filter.trim().toLowerCase());
  const shown = $derived(q ? lines.filter((l) => l.text.toLowerCase().includes(q)) : lines);

  function close() {
    gen++;
    probe?.abort();
    probe = undefined;
    es?.close();
    es = undefined;
  }

  async function connect(feId: number) {
    close();
    const my = ++gen;
    status = 'connecting';
    errMsg = '';
    const url = `/api/v1/frontends/${feId}/logs/stream`;

    // Probe first: EventSource hides the HTTP status, so a 404 (live logs off)
    // would otherwise look like a generic connection error.
    const ac = new AbortController();
    probe = ac;
    const timer = setTimeout(() => ac.abort(), 4000);
    try {
      const r = await fetch(url, { credentials: 'same-origin', signal: ac.signal, headers: { Accept: 'text/event-stream' } });
      if (my !== gen) return;
      if (r.status === 404) { status = 'off'; return; }
      if (r.status === 401) { void goto(`/login?next=${encodeURIComponent(location.pathname)}`, { replaceState: true }); return; }
      if (!r.ok) {
        let m = '';
        try { m = ((await r.json()) as { error?: { message?: string } })?.error?.message ?? ''; } catch { /* not JSON */ }
        if (my !== gen) return;
        status = 'error';
        errMsg = m || `The log stream could not be opened (${r.status}).`;
        return;
      }
    } catch {
      // Aborted after the timeout or a network error; let EventSource decide.
      if (my !== gen) return;
    } finally {
      clearTimeout(timer);
      ac.abort(); // drop the probe's open stream
      if (probe === ac) probe = undefined;
    }

    let gotMessage = false;
    const src = new EventSource(url, { withCredentials: true });
    es = src;
    src.onopen = () => { if (es === src) status = 'live'; };
    src.addEventListener('log', (ev) => {
      if (es !== src) return;
      gotMessage = true;
      status = 'live';
      let d: { ts?: number; text?: string };
      try { d = JSON.parse((ev as MessageEvent<string>).data); } catch { return; }
      const ts = typeof d.ts === 'number' ? d.ts : Date.now();
      push({ n: ++seq, ts, time: hms(ts), text: String(d.text ?? '') });
    });
    src.onerror = () => {
      if (es !== src) return;
      if (src.readyState === EventSource.CLOSED) {
        status = 'error';
        errMsg = gotMessage ? 'The log stream closed.' : 'The log stream could not be opened.';
        close();
      } else {
        status = 'reconnecting';
      }
    };
  }

  function push(l: Line) {
    if (paused) {
      held = held.length >= MAX ? [...held.slice(held.length - MAX + 1), l] : [...held, l];
      return;
    }
    lines = lines.length >= MAX ? [...lines.slice(lines.length - MAX + 1), l] : [...lines, l];
  }

  function togglePause() {
    if (paused) {
      const all = held.length ? [...lines, ...held] : lines;
      lines = all.length > MAX ? all.slice(all.length - MAX) : all;
      held = [];
      paused = false;
    } else {
      paused = true;
    }
  }

  function clear() {
    lines = [];
    held = [];
    stick = true;
  }

  function onScroll() {
    if (!box) return;
    stick = box.scrollHeight - box.scrollTop - box.clientHeight < 24;
  }

  function toBottom() {
    if (!box) return;
    box.scrollTop = box.scrollHeight;
    stick = true;
  }

  $effect(() => {
    void shown;
    if (!stick || !box) return;
    void tick().then(() => { if (box && stick) box.scrollTop = box.scrollHeight; });
  });

  $effect(() => {
    const feId = id;
    lines = [];
    held = [];
    void connect(feId);
    return close;
  });

  onDestroy(close);

  const statusText = $derived(
    paused ? `Paused${held.length ? ` · ${held.length} new` : ''}`
      : status === 'live' ? 'Live'
      : status === 'reconnecting' ? 'Reconnecting…'
      : 'Connecting…'
  );
</script>


{#if status === 'off'}
  <div class="card">
    <EmptyState icon="info" title="Live logs are off"
      text="Start Loxy with LOXY_LIVE_LOGS=1 (or a sampling rate such as 10) to see sampled request logs here. Loxy keeps no request logs by default." />
  </div>
{:else if status === 'error'}
  <ErrorBox message={errMsg} onretry={() => void connect(id)} />
{:else}
  <div class="toolbar">
    <div class="filter">
      <Icon name="search" size={14} />
      <input class="input" type="search" placeholder="Filter lines…" aria-label="Filter log lines" spellcheck="false" autocomplete="off" bind:value={filter} />
    </div>
    <span class="state small" class:live={status === 'live' && !paused} aria-live="polite">
      <span class="dot" aria-hidden="true"></span>{statusText}
    </span>
    <span class="spacer"></span>
    <button class="btn sm" onclick={togglePause} aria-pressed={paused}>
      <Icon name={paused ? 'play' : 'pause'} size={13} />{paused ? 'Resume' : 'Pause'}
    </button>
    <button class="btn sm ghost" onclick={clear} disabled={!lines.length && !held.length}><Icon name="trash" size={13} />Clear</button>
  </div>

  <div class="logwrap">
    <!-- svelte-ignore a11y_no_noninteractive_tabindex (scrollable region must be keyboard-reachable) -->
    <div class="log mono" bind:this={box} onscroll={onScroll} role="log" aria-label="Live request log" aria-live="off" tabindex="0">
      {#if status === 'connecting' && !lines.length}
        <div class="pad"><Skeleton rows={4} height={14} /></div>
      {:else if !lines.length}
        <p class="note muted">{paused ? 'Paused. New lines are held until you resume.' : 'Waiting for requests… Lines appear here as traffic is sampled.'}</p>
      {:else if !shown.length}
        <p class="note muted">No lines match “{filter}”.</p>
      {:else}
        {#each shown as l (l.n)}
          <div class="ln"><time datetime={new Date(l.ts).toISOString()}>{l.time}</time><span class="tx">{l.text}</span></div>
        {/each}
      {/if}
    </div>
    {#if !stick && shown.length}
      <button class="resume small" onclick={toBottom}>Paused — scroll to bottom to resume<Icon name="chevron" size={12} /></button>
    {/if}
  </div>
  <p class="muted xs foot">Showing the last {MAX} lines{q ? ` · ${shown.length} match` : ''}. Lines are not stored.</p>
{/if}

<style>
  .toolbar { display: flex; align-items: center; gap: var(--sp-2); flex-wrap: wrap; margin-bottom: var(--sp-3); }
  .filter { position: relative; display: flex; align-items: center; width: min(320px, 100%); }
  .filter :global(.icon) { position: absolute; left: 9px; color: var(--muted); pointer-events: none; }
  .filter .input { width: 100%; padding-left: 28px; }
  .state { display: inline-flex; align-items: center; gap: 6px; color: var(--muted); font-variant-numeric: tabular-nums; }
  .dot { width: 7px; height: 7px; border-radius: 50%; background: var(--muted); }
  .state.live { color: var(--text-2); }
  .state.live .dot { background: var(--ok); }
  .logwrap { position: relative; }
  .log {
    height: min(62dvh, 640px); overflow: auto; overscroll-behavior: contain;
    background: var(--surface); border: 1px solid var(--border); border-radius: var(--radius);
    font-size: var(--fs-sm); line-height: 1.55; padding: 8px 0;
  }
  .log:focus-visible { outline: 2px solid var(--focus); outline-offset: -1px; }
  .ln { display: flex; gap: 12px; padding: 0 12px; }
  .ln:hover { background: var(--surface-2); }
  time { flex: none; color: var(--muted); font-variant-numeric: tabular-nums; user-select: none; }
  .tx { white-space: pre-wrap; overflow-wrap: anywhere; min-width: 0; }
  .note { padding: 16px 12px; font-family: inherit; }
  .pad { padding: 8px 12px; }
  .resume {
    position: absolute; left: 50%; bottom: 12px; transform: translateX(-50%);
    display: inline-flex; align-items: center; gap: 6px; padding: 5px 12px; border-radius: 999px; cursor: pointer;
    background: var(--surface-3); color: var(--text); border: 1px solid var(--border-strong); box-shadow: 0 4px 14px rgb(0 0 0 / 0.18);
  }
  .resume:hover { background: var(--surface-2); }
  .resume:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; }
  .foot { margin-top: 8px; }
</style>
