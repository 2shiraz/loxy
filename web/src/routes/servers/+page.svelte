<script lang="ts">
  import { api, unwrap, mutate, type Server, type Labels } from '$lib/api/client';
  import { resource } from '$lib/stores/resource.svelte';
  import { confirm } from '$lib/stores/confirm.svelte';
  import { isIPv4, isIPv6, matchSelector } from '$lib/util/validate';
  import Icon from '$lib/components/Icon.svelte';
  import Menu from '$lib/components/Menu.svelte';
  import Modal from '$lib/components/Modal.svelte';
  import LabelChips from '$lib/components/LabelChips.svelte';
  import LabelEditor from '$lib/components/LabelEditor.svelte';
  import CopyText from '$lib/components/CopyText.svelte';
  import Skeleton from '$lib/components/Skeleton.svelte';
  import EmptyState from '$lib/components/EmptyState.svelte';
  import ErrorBox from '$lib/components/ErrorBox.svelte';

  const servers = resource(async () => (await unwrap(api.GET('/servers'))).servers);
  let q = $state('');
  const rows = $derived((servers.data ?? []).filter((s) => {
    const t = q.trim();
    if (!t) return true;
    if (t.includes('=')) return matchSelector(s.labels, t);
    const l = t.toLowerCase();
    return s.name.toLowerCase().includes(l) || [s.public_ipv4, s.public_ipv6, s.private_ipv4].some((x) => x?.includes(l));
  }));

  let open = $state(false);
  let editId = $state<number | null>(null);
  let f = $state({ name: '', public_ipv4: '', public_ipv6: '', private_ipv4: '', notes: '' });
  let labels = $state<Labels>({});
  let labelErr = $state('');
  let errs = $state<Record<string, string>>({});
  let formKey = $state(0);

  function openForm(s?: Server) {
    editId = s?.id ?? null;
    f = { name: s?.name ?? '', public_ipv4: s?.public_ipv4 ?? '', public_ipv6: s?.public_ipv6 ?? '', private_ipv4: s?.private_ipv4 ?? '', notes: s?.notes ?? '' };
    labels = { ...(s?.labels ?? {}) };
    errs = {};
    formKey++;
    open = true;
  }
  async function save() {
    errs = {};
    if (!f.name.trim()) errs.name = 'Required';
    if (f.public_ipv4 && !isIPv4(f.public_ipv4)) errs.public_ipv4 = 'Not a valid IPv4 address';
    if (f.public_ipv6 && !isIPv6(f.public_ipv6)) errs.public_ipv6 = 'Not a valid IPv6 address';
    if (f.private_ipv4 && !isIPv4(f.private_ipv4)) errs.private_ipv4 = 'Not a valid IPv4 address';
    if (!f.public_ipv4 && !f.public_ipv6 && !f.private_ipv4) errs.public_ipv4 = 'Give at least one address';
    if (Object.keys(errs).length || labelErr) return;
    const body = { name: f.name.trim(), public_ipv4: f.public_ipv4 || null, public_ipv6: f.public_ipv6 || null, private_ipv4: f.private_ipv4 || null, notes: f.notes, labels };
    const r = editId
      ? await mutate(api.PATCH('/servers/{id}', { params: { path: { id: editId } }, body }), { success: 'Server saved', onFieldErrors: (x) => (errs = x) })
      : await mutate(api.POST('/servers', { body }), { success: 'Server added', onFieldErrors: (x) => (errs = x) });
    if (r) { open = false; void servers.reload(); }
  }
  async function remove(s: Server) {
    const ok = await confirm({ title: `Delete ${s.name}?`, message: 'The server is removed from the inventory. Servers still used as targets cannot be deleted.', confirmLabel: 'Delete server', danger: true });
    if (!ok) return;
    const r = await mutate(api.DELETE('/servers/{id}', { params: { path: { id: s.id } } }), { success: 'Server deleted' });
    if (r !== undefined) void servers.reload();
  }
</script>

<svelte:head><title>Servers · Loxy</title></svelte:head>

<div class="page-head">
  <h1>Servers</h1>
  <button class="btn primary" onclick={() => openForm()}><Icon name="plus" size={14} />Add server</button>
</div>
<p class="section-intro">Your inventory of backend servers. Add them to load balancers directly or through label selectors.</p>

<label class="search">
  <Icon name="search" size={14} /><span class="sr-only">Search servers</span>
  <input class="input" placeholder="Search name or IP, or filter env=prod" bind:value={q} spellcheck="false" />
</label>

{#if servers.error && !servers.data}<ErrorBox message={servers.error} onretry={servers.reload} />
{:else if !servers.data}<Skeleton card rows={5} />
{:else if !servers.data.length}
  <div class="card"><EmptyState icon="server" title="No servers yet" text="Add the machines that should receive traffic. Labels let load balancers pick them up automatically."><button class="btn primary" onclick={() => openForm()}>Add server</button></EmptyState></div>
{:else}
  <div class="table-wrap">
    <table class="tbl">
      <thead><tr><th>Name</th><th>Public IPv4</th><th>Public IPv6</th><th>Private IPv4</th><th>Labels</th><th>Source</th><th><span class="sr-only">Actions</span></th></tr></thead>
      <tbody>
        {#each rows as s (s.id)}
          <tr>
            <td><b class="nm">{s.name}</b>{#if s.notes}<div class="xs muted note" title={s.notes}>{s.notes}</div>{/if}</td>
            <td><CopyText text={s.public_ipv4} /></td>
            <td><CopyText text={s.public_ipv6} /></td>
            <td><CopyText text={s.private_ipv4} /></td>
            <td><LabelChips labels={s.labels} max={4} /></td>
            <td class="small muted">{s.source}</td>
            <td class="actions"><Menu label="Actions for {s.name}" items={[{ label: 'Edit', onclick: () => openForm(s) }, { label: 'Delete', onclick: () => remove(s), danger: true, separator: true }]} /></td>
          </tr>
        {:else}
          <tr><td colspan="7" class="muted small" style:text-align="center" style:padding="24px">No servers match.</td></tr>
        {/each}
      </tbody>
    </table>
  </div>
{/if}

<Modal bind:open title={editId ? 'Edit server' : 'Add server'} width={560}>
  {#key formKey}
    <form id="srv" class="stack" onsubmit={(e) => { e.preventDefault(); void save(); }} novalidate>
      <div class="field">
        <label for="sn">Name</label>
        <input id="sn" class="input" bind:value={f.name} aria-invalid={errs.name ? 'true' : undefined} />
        {#if errs.name}<span class="ferr">{errs.name}</span>{/if}
      </div>
      <div class="form-grid">
        {#each [['public_ipv4', 'Public IPv4'], ['public_ipv6', 'Public IPv6'], ['private_ipv4', 'Private IPv4']] as const as [k, l] (k)}
          <div class="field">
            <label for="s-{k}">{l}</label>
            <input id="s-{k}" class="input mono" bind:value={f[k]} spellcheck="false" aria-invalid={errs[k] ? 'true' : undefined} />
            {#if errs[k]}<span class="ferr">{errs[k]}</span>{/if}
          </div>
        {/each}
      </div>
      <div class="field"><span class="label">Labels</span><LabelEditor bind:labels bind:error={labelErr} /></div>
      <div class="field">
        <label for="snotes">Notes</label>
        <textarea id="snotes" class="textarea" rows="2" bind:value={f.notes} style:font-family="var(--font)"></textarea>
      </div>
    </form>
  {/key}
  {#snippet footer()}
    <button class="btn" onclick={() => (open = false)}>Cancel</button>
    <button class="btn primary" type="submit" form="srv">{editId ? 'Save server' : 'Add server'}</button>
  {/snippet}
</Modal>

<style>
  .search { position: relative; display: flex; align-items: center; max-width: 380px; margin-bottom: 14px; }
  .search :global(.icon) { position: absolute; left: 10px; color: var(--muted); }
  .search .input { padding-left: 30px; }
  .nm { font-weight: 500; }
  .note { max-width: 240px; overflow: hidden; text-overflow: ellipsis; }
</style>
