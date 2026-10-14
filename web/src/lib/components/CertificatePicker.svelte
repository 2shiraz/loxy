<script lang="ts">
  /**
   * Certificate chip picker for HTTPS services, with an inline
   * "Create managed certificate" form (HTTP-01). Used by ServiceEditor and
   * the create wizard. Must not render a <form>: it lives inside one.
   */
  import { api, mutate, type Certificate } from '$lib/api/client';
  import { fqdnError } from '$lib/util/validate';
  import Icon from './Icon.svelte';

  let {
    selected = $bindable([]),
    certificates,
    error = '',
    id,
    onchange,
    oncreated
  }: {
    selected: number[] | undefined;
    certificates: Certificate[];
    error?: string;
    id: string;
    onchange?: () => void;
    oncreated?: (c: Certificate) => void;
  } = $props();

  // Certificates created here, so their names resolve before the list reloads.
  let created = $state<Certificate[]>([]);
  const all = $derived([...certificates, ...created.filter((c) => !certificates.some((x) => x.id === c.id))]);
  const certName = (cid: number) => all.find((c) => c.id === cid)?.name ?? `#${cid}`;

  let pick = $state<number | ''>('');
  let newOpen = $state(false);
  let domainsText = $state('');
  let newErr = $state('');
  let creating = $state(false);
  let domainsEl = $state<HTMLInputElement | null>(null);

  function add(cid: number) {
    if (!selected?.includes(cid)) selected = [...(selected ?? []), cid];
    onchange?.();
  }
  function remove(cid: number) {
    selected = (selected ?? []).filter((c) => c !== cid);
    onchange?.();
  }
  function onPick() {
    if (pick !== '') add(pick);
    pick = '';
  }
  function openNew() {
    newOpen = true;
    newErr = '';
    queueMicrotask(() => domainsEl?.focus());
  }

  async function createManaged() {
    if (creating) return;
    newErr = '';
    const domains = [...new Set(domainsText.split(',').map((d) => d.trim().toLowerCase()).filter(Boolean))];
    if (!domains.length) newErr = 'Enter at least one domain';
    else if (domains.length > 100) newErr = 'At most 100 domains';
    else if (domains.some((d) => d.startsWith('*.'))) newErr = 'Wildcard domains need a DNS challenge. Create them on the Certificates page.';
    else {
      const bad = domains.find((d) => fqdnError(d));
      if (bad) newErr = `${bad} is not a valid domain`;
    }
    if (newErr) { domainsEl?.focus(); return; }
    creating = true;
    const r = await mutate(
      api.POST('/certificates', { body: { type: 'managed', name: domains[0], domains, challenge: 'http01' } }),
      { success: `Certificate ${domains[0]} requested`, onFieldErrors: (f) => (newErr = Object.values(f).join('. ')) }
    );
    creating = false;
    if (r?.certificate) {
      created = [...created, r.certificate];
      add(r.certificate.id);
      oncreated?.(r.certificate);
      domainsText = '';
      newOpen = false;
    } else {
      domainsEl?.focus();
    }
  }
  function onKey(e: KeyboardEvent) {
    if (e.key === 'Enter') { e.preventDefault(); void createManaged(); }
    else if (e.key === 'Escape') { e.preventDefault(); newOpen = false; }
  }
</script>

<div class="field">
  <span class="label" id="{id}-lbl">Certificates</span>
  <div class="row-wrap">
    {#each selected ?? [] as cid (cid)}
      <span class="chip"><Icon name="shield" size={11} />{certName(cid)}
        <button type="button" class="x" aria-label="Remove certificate {certName(cid)}" onclick={() => remove(cid)}><Icon name="x" size={11} /></button>
      </span>
    {/each}
    <select id="{id}-sel" class="select sm" aria-labelledby="{id}-lbl" aria-invalid={error ? 'true' : undefined} aria-describedby={error ? `${id}-err` : undefined} bind:value={pick} onchange={onPick}>
      <option value="">Add certificate…</option>
      {#each all.filter((c) => !selected?.includes(c.id)) as c (c.id)}
        <option value={c.id}>{c.name} ({c.domains.slice(0, 2).join(', ')}{c.domains.length > 2 ? '…' : ''}) · {c.status}</option>
      {/each}
    </select>
    {#if !newOpen}
      <button type="button" class="btn sm" onclick={openNew}><Icon name="plus" size={13} />Create managed certificate</button>
    {/if}
    <a class="small" href="/certificates">Manage certificates</a>
  </div>
  {#if newOpen}
    <div class="new">
      <label for="{id}-dom" class="small">Domains <span class="muted">(comma separated)</span></label>
      <div class="row-wrap">
        <input
          id="{id}-dom" bind:this={domainsEl} class="input mono dom" bind:value={domainsText}
          placeholder="example.com, www.example.com…" autocomplete="off" spellcheck="false" autocapitalize="off" inputmode="url"
          aria-invalid={newErr ? 'true' : undefined} aria-describedby="{id}-newhint" onkeydown={onKey}
        />
        <button type="button" class="btn sm primary" disabled={creating} onclick={createManaged}>{creating ? 'Requesting…' : 'Create'}</button>
        <button type="button" class="btn sm ghost" onclick={() => (newOpen = false)}>Cancel</button>
      </div>
      <span id="{id}-newhint" class={newErr ? 'ferr' : 'hint'} aria-live="polite">{newErr || 'Issued automatically via HTTP-01 once the domains resolve to this HAProxy node.'}</span>
    </div>
  {/if}
  {#if error}<span class="ferr" id="{id}-err">{error}</span>{/if}
</div>

<style>
  .select.sm { height: 26px; width: auto; max-width: 100%; font-size: var(--fs-sm); }
  .x { border: 0; background: none; padding: 0; cursor: pointer; color: var(--muted); display: inline-flex; }
  .x:hover { color: var(--text); }
  .new { display: flex; flex-direction: column; gap: 4px; margin-top: 6px; padding: 10px 12px; background: var(--surface-2); border-radius: var(--radius-sm); }
  .dom { flex: 1 1 260px; min-width: 0; height: 26px; }
  .new .ferr, .new .hint { font-size: var(--fs-xs); }
  .new .hint { color: var(--muted); }
</style>
