<script lang="ts">
  import { tick } from 'svelte';
  import { goto } from '$app/navigation';
  import { api, mutate, type Labels, type ProjectUpdate } from '$lib/api/client';
  import { getProject } from '$lib/stores/project';
  import { confirm } from '$lib/stores/confirm.svelte';
  import { bump } from '$lib/stores/live.svelte';
  import { algorithmLabel } from '$lib/util/format';
  import LabelEditor from '$lib/components/LabelEditor.svelte';

  const ctx = getProject();
  const p = $derived(ctx.project!);

  type Alg = NonNullable<ProjectUpdate['algorithm']>;
  // svelte-ignore state_referenced_locally
  let name = $state(p.name);
  // svelte-ignore state_referenced_locally
  let description = $state(p.description);
  // svelte-ignore state_referenced_locally
  let algorithm = $state<Alg>(p.algorithm);
  // svelte-ignore state_referenced_locally
  let labels = $state<Labels>({ ...p.labels });
  let labelErr = $state('');
  let errs = $state<Record<string, string>>({});
  let busy = $state(false);
  let formKey = $state(0);
  let form: HTMLFormElement | undefined = $state();

  const dirty = $derived(name !== p.name || description !== p.description || algorithm !== p.algorithm || JSON.stringify(labels) !== JSON.stringify(p.labels));

  function reset() {
    name = p.name; description = p.description; algorithm = p.algorithm; labels = { ...p.labels };
    errs = {}; formKey++;
  }

  async function save(e: SubmitEvent) {
    e.preventDefault();
    errs = {};
    if (!name.trim()) errs = { name: 'Required' };
    if (Object.keys(errs).length || labelErr) { await focusInvalid(); return; }
    busy = true;
    const r = await mutate(api.PATCH('/projects/{id}', { params: { path: { id: p.id } }, body: { name: name.trim(), description: description.trim(), algorithm, labels } }), {
      success: 'Settings saved',
      onFieldErrors: (f) => { errs = Object.fromEntries(Object.entries(f).map(([k, v]) => [k.startsWith('labels') ? 'labels' : k, v])); void focusInvalid(); }
    });
    busy = false;
    if (r) { ctx.set(r.project); bump('projects'); name = r.project.name; description = r.project.description; }
  }
  async function focusInvalid() {
    await tick();
    form?.querySelector<HTMLElement>('[aria-invalid="true"]')?.focus();
  }

  async function remove() {
    const ok = await confirm({ title: `Delete ${p.name}?`, message: 'All services stop listening and its domains and targets are removed. Hosts and certificates are kept. This cannot be undone.', confirmLabel: 'Delete project', danger: true, typed: p.name });
    if (!ok) return;
    const r = await mutate(api.DELETE('/projects/{id}', { params: { path: { id: p.id } } }), { success: `${p.name} deleted` });
    if (r !== undefined) { bump('projects', 'overview'); void goto('/projects'); }
  }
</script>

<svelte:head><title>Settings · {p.name} · Loxy</title></svelte:head>

<div class="stack-lg wrap">
  <form class="card stack" bind:this={form} onsubmit={save} novalidate aria-labelledby="h-gen">
    <div class="card-head"><h2 id="h-gen">General</h2></div>
    <div class="field">
      <label for="sname">Name</label>
      <input id="sname" class="input" bind:value={name} autocomplete="off" spellcheck="false" aria-invalid={errs.name ? 'true' : undefined} />
      {#if errs.name}<span class="ferr">{errs.name}</span>{/if}
    </div>
    <div class="field">
      <label for="sdesc">Description</label>
      <textarea id="sdesc" class="textarea" rows="2" bind:value={description} aria-invalid={errs.description ? 'true' : undefined}></textarea>
      {#if errs.description}<span class="ferr">{errs.description}</span>{/if}
    </div>
    <div class="field">
      <label for="salg">Algorithm</label>
      <select id="salg" class="select" bind:value={algorithm} aria-invalid={errs.algorithm ? 'true' : undefined} aria-describedby="salg-h">
        {#each ['roundrobin', 'leastconn', 'source', 'random'] as const as a (a)}<option value={a}>{algorithmLabel(a)}</option>{/each}
      </select>
      {#if errs.algorithm}<span class="ferr" id="salg-h">{errs.algorithm}</span>{:else}<span class="hint" id="salg-h">How new connections are spread across healthy targets. Applies to all services.</span>{/if}
    </div>
    <div class="field">
      <span class="label">Labels</span>
      {#key formKey}<LabelEditor bind:labels bind:error={labelErr} />{/key}
      {#if errs.labels}<span class="ferr">{errs.labels}</span>{:else}<span class="hint">Used by alert rules to pick projects, e.g. <code>env=prod</code>.</span>{/if}
    </div>
    <div class="row">
      <span class="spacer"></span>
      <button type="button" class="btn" onclick={reset} disabled={!dirty || busy}>Discard</button>
      <button type="submit" class="btn primary" disabled={!dirty || busy || !!labelErr}>{busy ? 'Saving…' : 'Save changes'}</button>
    </div>
  </form>

  <section class="card stack danger-zone" aria-labelledby="h-del">
    <div class="card-head"><h2 id="h-del">Delete project</h2></div>
    <p class="small">Stops all services of <b>{p.name}</b> and removes its targets and domains. Hosts and certificates are not touched. Metrics are kept until they expire.</p>
    <div><button class="btn danger solid" onclick={remove}>Delete project</button></div>
  </section>
</div>

<style>
  .wrap { max-width: 720px; }
  .danger-zone p { color: var(--text-2); }
</style>
