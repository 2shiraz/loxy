<script lang="ts">
  /**
   * Target picker for a project (§8.6): hosts from the inventory, a label
   * selector with live preview, or a list of IPs with an optional port.
   * Emits TargetInput[] for POST /projects/{id}/targets (or ProjectCreate.targets).
   */
  import { api, unwrap, type Host, type TargetInput } from '$lib/api/client';
  import { targetIpError, portError } from '$lib/util/validate';
  import LabelChips from './LabelChips.svelte';

  type Mode = 'host' | 'label_selector' | 'ip';
  let {
    mode = $bindable('ip'),
    targets = $bindable([]),
    error = $bindable(''),
    showModes = true,
    simple = false
  }: {
    mode?: Mode;
    targets?: TargetInput[];
    error?: string;
    showModes?: boolean;
    /** Tuck port override, weight and backup into a disclosure. */
    simple?: boolean;
  } = $props();

  let hosts = $state<Host[] | null>(null);
  let hostQ = $state('');
  let picked = $state<number[]>([]);
  let usePrivate = $state(false);
  let selector = $state('');
  let preview = $state<{ n: number; names: string[]; err: string } | null>(null);
  let ipText = $state('');
  let port = $state<number | ''>('');
  let weight = $state(100);
  let backup = $state(false);

  $effect(() => {
    if (mode === 'host' && hosts === null) {
      hosts = [];
      void unwrap(api.GET('/hosts')).then((d) => (hosts = d.hosts)).catch(() => (hosts = []));
    }
  });

  let timer: ReturnType<typeof setTimeout> | undefined;
  $effect(() => {
    const s = selector.trim();
    clearTimeout(timer);
    if (mode !== 'label_selector' || !s) { preview = null; return; }
    timer = setTimeout(async () => {
      const r = await api.GET('/label_selector/preview', { params: { query: { selector: s } } });
      if (r.data) preview = { n: r.data.hosts.length, names: r.data.hosts.slice(0, 6).map((x) => x.name), err: '' };
      else preview = { n: 0, names: [], err: (r.error as { error?: { message?: string } })?.error?.message ?? 'Invalid selector' };
    }, 250);
  });

  const filtered = $derived((hosts ?? []).filter((s) => {
    const t = hostQ.trim().toLowerCase();
    return !t || s.name.toLowerCase().includes(t) || [s.ipv4, s.ipv6, s.private_ipv4].some((x) => x?.includes(t)) || Object.entries(s.labels).some(([k, v]) => `${k}=${v}`.includes(t));
  }));

  /** "10.0.0.1", "10.0.0.1:8080", "2001:db8::1", "[2001:db8::1]:8080" */
  function parseAddr(l: string): { ip: string; port?: number; err: string } {
    let m = l.match(/^\[([^\]]+)\](?::(\d+))?$/);
    if (m) return { ip: m[1], port: m[2] ? Number(m[2]) : undefined, err: '' };
    m = l.match(/^([^:]+):(\d+)$/);
    if (m) return { ip: m[1], port: Number(m[2]), err: '' };
    return { ip: l, err: '' };
  }

  $effect(() => {
    const common = { weight, backup, ...(port !== '' ? { port: Number(port) } : {}) };
    const pErr = port !== '' ? portError(port) : '';
    const wErr = weight >= 0 && weight <= 256 ? '' : 'Weight must be 0–256';
    if (mode === 'ip') {
      const lines = ipText.split(/[\s,]+/).map((l) => l.trim()).filter(Boolean);
      let err = pErr || wErr;
      const seen = new Set<string>();
      const out: TargetInput[] = [];
      for (const l of lines) {
        if (err) break;
        const a = parseAddr(l);
        const e = targetIpError(a.ip) || (a.port != null ? portError(a.port) : '') || (seen.has(l) ? 'is listed twice' : '');
        if (e) { err = `${l}: ${e}`; break; }
        seen.add(l);
        out.push({ type: 'ip', ip: a.ip, ...common, ...(a.port != null ? { port: a.port } : {}) });
      }
      error = err;
      targets = err ? [] : out;
    } else if (mode === 'host') {
      error = pErr || wErr;
      targets = picked.map((id) => ({ type: 'host', host_id: id, use_private_ip: usePrivate, ...common }));
    } else {
      error = preview?.err || pErr || wErr;
      targets = selector.trim() ? [{ type: 'label_selector', label_selector: selector.trim(), use_private_ip: usePrivate, ...common }] : [];
    }
  });

  function toggle(id: number) {
    picked = picked.includes(id) ? picked.filter((x) => x !== id) : [...picked, id];
  }
</script>

<div class="tp">
  {#if showModes}
    <div class="seg" role="radiogroup" aria-label="Target source">
      <button type="button" role="radio" aria-checked={mode === 'ip'} class:on={mode === 'ip'} onclick={() => (mode = 'ip')}>IP addresses</button>
      <button type="button" role="radio" aria-checked={mode === 'host'} class:on={mode === 'host'} onclick={() => (mode = 'host')}>Hosts</button>
      <button type="button" role="radio" aria-checked={mode === 'label_selector'} class:on={mode === 'label_selector'} onclick={() => (mode = 'label_selector')}>Label selector</button>
    </div>
  {/if}

  {#if mode === 'ip'}
    <div class="field">
      <label for="tp-ips">Addresses, one per line</label>
      <textarea id="tp-ips" class="textarea" rows="5" placeholder={'10.0.0.11\n10.0.0.12:8080\n[2001:db8::10]:8080'} bind:value={ipText} aria-invalid={error ? 'true' : undefined} spellcheck="false"></textarea>
      {#if error}<span class="ferr">{error}</span>{:else}<span class="hint">{targets.length ? `${targets.length} address${targets.length === 1 ? '' : 'es'}` : 'IPv4 or IPv6, optionally with :port. Loopback, link-local and multicast are rejected.'}</span>{/if}
    </div>
  {:else if mode === 'host'}
    <div class="field">
      <label for="tp-sq">Hosts</label>
      <input id="tp-sq" class="input" placeholder="Search by name, IP or label…" bind:value={hostQ} />
    </div>
    <div class="list" role="group" aria-label="Hosts">
      {#if hosts === null || (hosts.length === 0 && !hostQ)}
        <p class="muted small pad">{hosts === null ? 'Loading…' : 'No hosts in the inventory yet. Add them under Hosts.'}</p>
      {:else}
        {#each filtered as s (s.id)}
          <label class="srow">
            <input type="checkbox" checked={picked.includes(s.id)} onchange={() => toggle(s.id)} />
            <span class="nm">{s.name}</span>
            <span class="mono small muted">{(usePrivate ? s.private_ipv4 : null) ?? s.ipv4 ?? s.ipv6 ?? s.private_ipv4 ?? ''}</span>
            <span class="spacer"></span>
            <LabelChips labels={s.labels} max={2} />
          </label>
        {:else}
          <p class="muted small pad">No hosts match.</p>
        {/each}
      {/if}
    </div>
    <span class="hint small muted">{picked.length} selected</span>
  {:else}
    <div class="field">
      <label for="tp-sel">Label selector</label>
      <input id="tp-sel" class="input mono" placeholder="env=prod,role in (web,api)" bind:value={selector} spellcheck="false" aria-invalid={preview?.err ? 'true' : undefined} />
      <span class="hint">Syntax: <code>key=value</code>, <code>key!=value</code>, <code>key in (a,b)</code>, <code>key</code>. Comma means AND.</span>
      {#if preview}
        {#if preview.err}<span class="ferr">{preview.err}</span>
        {:else}<span class="small" aria-live="polite">Matches <b>{preview.n}</b> host{preview.n === 1 ? '' : 's'}{preview.names.length ? ': ' + preview.names.join(', ') + (preview.n > preview.names.length ? '…' : '') : ''}</span>{/if}
      {/if}
    </div>
  {/if}

  {#if mode !== 'ip'}
    <label class="check"><input type="checkbox" bind:checked={usePrivate} />Use the host's private IPv4</label>
  {/if}
  {#snippet extras()}
  <div class="form-grid">
    <div class="field">
      <label for="tp-port">Port override <span class="muted">(optional)</span></label>
      <input id="tp-port" class="input mono" type="number" min="1" max="65535" placeholder="service port" bind:value={port} />
      <span class="hint">Empty = each service's destination port.</span>
    </div>
    <div class="field">
      <label for="tp-w">Weight</label>
      <input id="tp-w" class="input mono" type="number" min="0" max="256" bind:value={weight} />
    </div>
    <label class="check bk"><input type="checkbox" bind:checked={backup} />Backup target</label>
  </div>
  {/snippet}
  {#if simple}
    <details class="more">
      <summary>Port override, weight and backup</summary>
      <div class="more-body">{@render extras()}</div>
    </details>
  {:else}
    {@render extras()}
  {/if}
</div>

<style>
  .tp { display: flex; flex-direction: column; gap: 14px; }
  .seg { display: inline-flex; padding: 3px; background: var(--surface-2); border-radius: 10px; align-self: flex-start; max-width: 100%; overflow-x: auto; scrollbar-width: none; }
  .seg button { border: 0; background: none; height: 32px; padding: 0 12px; border-radius: 7px; color: var(--muted); cursor: pointer; font-weight: 550; font-size: var(--fs-sm); white-space: nowrap; transition: color 120ms ease, background-color 120ms ease; }
  .seg button:hover { color: var(--text); }
  .seg button.on { background: var(--surface); color: var(--text); box-shadow: 0 1px 2px rgb(0 0 0 / 0.08), 0 0 0 1px var(--border); }
  .list { max-height: 260px; overflow: auto; border: 1px solid var(--border); border-radius: var(--radius-sm); background: var(--surface); }
  .srow { display: flex; align-items: center; gap: 10px; min-height: 40px; padding: 6px 12px; border-bottom: 1px solid var(--border); cursor: pointer; }
  .srow:last-child { border-bottom: 0; }
  .srow:hover { background: var(--surface-2); }
  .srow input { accent-color: var(--accent); width: 16px; height: 16px; }
  .nm { font-weight: 550; }
  .pad { padding: 12px; }
  .bk { align-self: end; height: var(--control-h); }
  .more summary { cursor: pointer; font-size: var(--fs-sm); color: var(--text-2); font-weight: 550; width: fit-content; padding: 4px 0; }
  .more summary:hover { color: var(--text); }
  .more-body { padding-top: 12px; }
</style>
