<script lang="ts">
  /** Shared target pickers (§8.6): servers, label selector with live preview, IP list. */
  import { api, unwrap, type Server, type TargetInput } from '$lib/api/client';
  import { targetIpError } from '$lib/util/validate';
  import LabelChips from './LabelChips.svelte';

  let {
    mode = $bindable('ip'),
    targets = $bindable([]),
    error = $bindable(''),
    exclude = [],
    allowPrivate = false,
    showModes = true
  }: {
    mode?: 'server' | 'label_selector' | 'ip';
    targets?: TargetInput[];
    error?: string;
    exclude?: string[];
    allowPrivate?: boolean;
    showModes?: boolean;
  } = $props();

  let servers = $state<Server[] | null>(null);
  let serverQ = $state('');
  let picked = $state<number[]>([]);
  let usePrivate = $state(false);
  let selector = $state('');
  let preview = $state<{ n: number; names: string[]; err: string } | null>(null);
  let ipText = $state('');

  $effect(() => {
    if (mode === 'server' && servers === null) {
      servers = [];
      void unwrap(api.GET('/servers')).then((d) => (servers = d.servers)).catch(() => (servers = []));
    }
  });

  let timer: ReturnType<typeof setTimeout> | undefined;
  $effect(() => {
    const s = selector.trim();
    clearTimeout(timer);
    if (mode !== 'label_selector' || !s) { preview = null; return; }
    timer = setTimeout(async () => {
      const r = await api.GET('/label_selector/preview', { params: { query: { selector: s } } });
      if (r.data) preview = { n: r.data.servers.length, names: r.data.servers.slice(0, 6).map((x) => x.name), err: '' };
      else preview = { n: 0, names: [], err: (r.error as { error?: { message?: string } })?.error?.message ?? 'Invalid selector' };
    }, 250);
  });

  const filtered = $derived((servers ?? []).filter((s) => {
    const t = serverQ.trim().toLowerCase();
    return !t || s.name.toLowerCase().includes(t) || (s.public_ipv4 ?? '').includes(t) || Object.entries(s.labels).some(([k, v]) => `${k}=${v}`.includes(t));
  }));

  $effect(() => {
    if (mode === 'ip') {
      const lines = ipText.split(/[\s,]+/).map((l) => l.trim()).filter(Boolean);
      let err = '';
      const seen = new Set<string>();
      for (const l of lines) {
        const e = targetIpError(l) || (exclude.includes(l) ? "is one of the load balancer's own IPs" : '') || (seen.has(l) ? 'is listed twice' : '');
        if (e) { err = `${l}: ${e}`; break; }
        seen.add(l);
      }
      error = err;
      targets = err ? [] : lines.map((ip) => ({ type: 'ip', ip }));
    } else if (mode === 'server') {
      error = '';
      targets = picked.map((id) => ({ type: 'server', server_id: id, use_private_ip: usePrivate }));
    } else {
      error = preview?.err ?? '';
      targets = selector.trim() ? [{ type: 'label_selector', label_selector: selector.trim(), use_private_ip: usePrivate }] : [];
    }
  });

  function toggle(id: number) {
    picked = picked.includes(id) ? picked.filter((x) => x !== id) : [...picked, id];
  }
</script>

<div class="tp">
  {#if showModes}
    <div class="seg" role="radiogroup" aria-label="Target type">
      <button type="button" role="radio" aria-checked={mode === 'server'} class:on={mode === 'server'} onclick={() => (mode = 'server')}>Server</button>
      <button type="button" role="radio" aria-checked={mode === 'label_selector'} class:on={mode === 'label_selector'} onclick={() => (mode = 'label_selector')}>Label selector</button>
      <button type="button" role="radio" aria-checked={mode === 'ip'} class:on={mode === 'ip'} onclick={() => (mode = 'ip')}>IP</button>
    </div>
  {/if}

  {#if mode === 'ip'}
    <div class="field">
      <label for="tp-ips">IP addresses, one per line</label>
      <textarea id="tp-ips" class="textarea" rows="5" placeholder={'10.0.0.11\n10.0.0.12\n2a01:4f8::10'} bind:value={ipText} aria-invalid={error ? 'true' : undefined} spellcheck="false"></textarea>
      {#if error}<span class="ferr">{error}</span>{:else}<span class="hint">{targets.length ? `${targets.length} address${targets.length === 1 ? '' : 'es'}` : 'IPv4 or IPv6. Loopback, link-local and multicast are rejected.'}</span>{/if}
    </div>
  {:else if mode === 'server'}
    <div class="field">
      <label for="tp-sq">Servers</label>
      <input id="tp-sq" class="input" placeholder="Search by name, IP or label" bind:value={serverQ} />
    </div>
    <div class="list" role="group" aria-label="Servers">
      {#if servers === null || (servers.length === 0 && !serverQ)}
        <p class="muted small pad">{servers === null ? 'Loading…' : 'No servers in the inventory yet. Add them under Servers.'}</p>
      {:else}
        {#each filtered as s (s.id)}
          <label class="srow">
            <input type="checkbox" checked={picked.includes(s.id)} onchange={() => toggle(s.id)} />
            <span class="nm">{s.name}</span>
            <span class="mono small muted">{s.public_ipv4 ?? s.private_ipv4 ?? ''}</span>
            <span class="spacer"></span>
            <LabelChips labels={s.labels} max={2} />
          </label>
        {:else}
          <p class="muted small pad">No servers match.</p>
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
        {:else}<span class="small" aria-live="polite">Matches <b>{preview.n}</b> server{preview.n === 1 ? '' : 's'}{preview.names.length ? ': ' + preview.names.join(', ') + (preview.n > preview.names.length ? '…' : '') : ''}</span>{/if}
      {/if}
    </div>
  {/if}

  {#if mode !== 'ip' && allowPrivate}
    <label class="check"><input type="checkbox" bind:checked={usePrivate} />Use private IP</label>
  {/if}
</div>

<style>
  .tp { display: flex; flex-direction: column; gap: 12px; }
  .seg { display: inline-flex; padding: 2px; background: var(--surface-2); border-radius: 8px; align-self: flex-start; }
  .seg button { border: 0; background: none; padding: 5px 12px; border-radius: 6px; color: var(--muted); cursor: pointer; font-weight: 500; font-size: var(--fs-sm); }
  .seg button.on { background: var(--surface); color: var(--text); box-shadow: 0 0 0 1px var(--border-strong); }
  .list { max-height: 240px; overflow: auto; border: 1px solid var(--border); border-radius: var(--radius-sm); }
  .srow { display: flex; align-items: center; gap: 10px; padding: 7px 10px; border-bottom: 1px solid var(--border); cursor: pointer; }
  .srow:last-child { border-bottom: 0; }
  .srow:hover { background: var(--surface-2); }
  .srow input { accent-color: var(--accent); }
  .nm { font-weight: 500; }
  .pad { padding: 12px; }
</style>
