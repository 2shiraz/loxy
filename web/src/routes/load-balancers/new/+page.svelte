<script lang="ts">
  import { goto } from '$app/navigation';
  import { api, unwrap, mutate, type ServiceInput, type TargetInput, type Labels } from '$lib/api/client';
  import { resource } from '$lib/stores/resource.svelte';
  import { TEMPLATES, serviceTitle, validateService, normalise } from '$lib/util/services';
  import { bytes, flag, price, compact } from '$lib/util/format';
  import Icon from '$lib/components/Icon.svelte';
  import TargetPicker from '$lib/components/TargetPicker.svelte';
  import LabelEditor from '$lib/components/LabelEditor.svelte';
  import Skeleton from '$lib/components/Skeleton.svelte';
  import CertificatePicker from '$lib/components/CertificatePicker.svelte';
  import { toast } from '$lib/stores/toast.svelte';

  const nodes = resource(async () => (await unwrap(api.GET('/nodes'))).nodes);
  const plans = resource(async () => (await unwrap(api.GET('/plans'))).plans);
  const certs = resource(async () => (await unwrap(api.GET('/certificates'))).certificates, { keys: () => ['certificates'] });

  let nodeId = $state<number | null>(null);
  let planId = $state<number | null>(null);
  let publicEnabled = $state(true);
  let algorithm = $state<'roundrobin' | 'leastconn'>('roundrobin');
  let services = $state<ServiceInput[]>([]);
  // Stable keys so per-service component state survives removals.
  let svcKeys = $state<number[]>([]);
  let nextKey = 0;
  let certTried = $state(false);
  let targetMode = $state<'server' | 'label_selector' | 'ip'>('ip');
  let targets = $state<TargetInput[]>([]);
  let targetErr = $state('');
  let name = $state('');
  let labels = $state<Labels>({});
  let labelErr = $state('');
  let fieldErrs = $state<Record<string, string>>({});
  let busy = $state(false);

  $effect(() => { if (nodeId == null && nodes.data?.length) nodeId = nodes.data[0].id; });
  $effect(() => { if (planId == null && plans.data?.length) planId = plans.data[0].id; });

  const node = $derived(nodes.data?.find((n) => n.id === nodeId));
  const freeV4 = $derived(node?.ips.filter((i) => i.family === 4 && i.assigned_lb_id == null && i.kind !== 'private').length ?? 0);
  const freeV6 = $derived(node?.ips.filter((i) => i.family === 6 && i.assigned_lb_id == null && i.kind !== 'private').length ?? 0);
  const plan = $derived(plans.data?.find((p) => p.id === planId));

  const svcErrs = $derived(services.map((s, i) => validateService(s, services.filter((_, j) => j !== i))));
  const nameErr = $derived(name && !/^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/i.test(name) ? 'Use letters, digits and dashes (max 63)' : '');
  const blocking = $derived(!name || !!nameErr || !planId || !!labelErr || !!targetErr || svcErrs.some((e) => Object.keys(e).some((k) => k !== 'certificates')));

  /** Server field errors for services[i].*, keyed by the last path segment (e.g. `certificates`). */
  const svcServerErrs = $derived.by(() => {
    const out: Record<string, string>[] = services.map(() => ({}));
    for (const [k, msg] of Object.entries(fieldErrs)) {
      const m = /^services\[(\d+)\]\.(.+)$/.exec(k);
      if (!m || !out[Number(m[1])]) continue;
      out[Number(m[1])][m[2].split('.').pop()!] = msg;
    }
    return out;
  });
  const KNOWN = ['listen_port', 'destination_port', 'certificates'];
  const svcOtherErrs = $derived(svcServerErrs.map((e) => Object.entries(e).filter(([k]) => !KNOWN.includes(k)).map(([, v]) => v)));
  const certErr = (i: number) => svcServerErrs[i]?.certificates || (certTried ? svcErrs[i]?.certificates : '') || '';

  function clearServerErr(i: number, field: string) {
    const next = { ...fieldErrs };
    for (const k of Object.keys(next)) if (k.startsWith(`services[${i}].`) && k.endsWith(field)) delete next[k];
    fieldErrs = next;
  }
  function setProtocol(i: number, p: 'tcp' | 'http' | 'https') {
    const s = normalise({ ...services[i], protocol: p });
    if (s.health_check) s.health_check.protocol = p === 'tcp' ? 'tcp' : 'http';
    services[i] = normalise(s);
  }
  function removeService(i: number) {
    services.splice(i, 1);
    svcKeys.splice(i, 1);
    // Server errors are index-based; they no longer line up.
    fieldErrs = Object.fromEntries(Object.entries(fieldErrs).filter(([k]) => !k.startsWith('services[')));
  }

  function addTemplate(id: string) {
    const t = TEMPLATES.find((x) => x.id === id);
    if (t) { services.push(t.make()); svcKeys.push(nextKey++); }
  }

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    if (blocking) return;
    const missing = services.findIndex((s, i) => s.protocol === 'https' && svcErrs[i].certificates);
    if (missing >= 0) {
      certTried = true;
      document.getElementById(`sc-${svcKeys[missing]}-sel`)?.focus();
      return;
    }
    busy = true;
    fieldErrs = {};
    const res = await mutate(
      api.POST('/load_balancers', {
        body: {
          name, plan_id: planId!, node_id: nodeId ?? undefined, algorithm, public_enabled: publicEnabled, labels,
          services: services.map(normalise), targets
        }
      }),
      { onFieldErrors: (f) => (fieldErrs = f) }
    );
    busy = false;
    if (res) {
      toast.success(`Load balancer ${name} created`);
      void goto(`/load-balancers/${res.load_balancer.id}/overview`);
    }
  }
</script>

<svelte:head><title>Create Load Balancer · Loxy</title></svelte:head>

<div class="page-head">
  <h1>Create Load Balancer</h1>
</div>

<form class="wizard" onsubmit={submit} novalidate>
  <section aria-labelledby="s-loc">
    <h2 id="s-loc">Location</h2>
    {#if !nodes.data}<Skeleton rows={2} />{:else}
      <div class="cards">
        {#each nodes.data as n (n.id)}
          <label class="pick" class:on={nodeId === n.id}>
            <input type="radio" name="node" value={n.id} bind:group={nodeId} class="sr-only" />
            <span class="t">{flag(n.location.country)} {n.location.city || n.name}</span>
            <span class="muted small">{n.location.country ?? ''}{n.location.network_zone ? ` · ${n.location.network_zone}` : ''}</span>
            <span class="muted xs">{n.name}</span>
          </label>
        {/each}
      </div>
      {#if fieldErrs.node_id}<p class="ferr small err-text">{fieldErrs.node_id}</p>{/if}
    {/if}
  </section>

  <section aria-labelledby="s-type">
    <h2 id="s-type">Type</h2>
    {#if !plans.data}<Skeleton rows={2} />{:else}
      <div class="cards">
        {#each plans.data as p (p.id)}
          <label class="pick plan" class:on={planId === p.id}>
            <input type="radio" name="plan" value={p.id} bind:group={planId} class="sr-only" />
            <span class="row"><span class="badge plan">{p.name}</span><span class="spacer"></span>{#if p.price_monthly_cents != null}<span class="price">{price(p.price_monthly_cents, p.currency)}<span class="muted xs">/mo</span></span>{/if}</span>
            <dl class="limits">
              <dt>Connections</dt><dd>{compact(p.max_connections)}</dd>
              <dt>Services</dt><dd>{p.max_services}</dd>
              <dt>Targets</dt><dd>{p.max_targets}</dd>
              <dt>Certificates</dt><dd>{p.max_certificates}</dd>
              <dt>Traffic</dt><dd>{bytes(p.traffic_quota_bytes)}</dd>
            </dl>
          </label>
        {/each}
      </div>
    {/if}
  </section>

  <section aria-labelledby="s-net">
    <h2 id="s-net">Network</h2>
    <label class="check"><input type="checkbox" bind:checked={publicEnabled} />Public network (IPv4 and IPv6)</label>
    <p class="muted small">Addresses are assigned automatically from the node's pool{node ? `: ${freeV4} free IPv4, ${freeV6} free IPv6` : ''}. When none are free, the node's shared address is used. Private networks can be attached after creation.</p>
  </section>

  <section aria-labelledby="s-alg">
    <h2 id="s-alg">Algorithm</h2>
    <div class="cards narrow">
      <label class="pick" class:on={algorithm === 'roundrobin'}>
        <input type="radio" name="alg" value="roundrobin" bind:group={algorithm} class="sr-only" />
        <span class="t">Round Robin</span><span class="muted small">Requests are spread evenly, respecting weights.</span>
      </label>
      <label class="pick" class:on={algorithm === 'leastconn'}>
        <input type="radio" name="alg" value="leastconn" bind:group={algorithm} class="sr-only" />
        <span class="t">Least Connections</span><span class="muted small">New connections go to the target with the fewest open ones.</span>
      </label>
    </div>
  </section>

  <section aria-labelledby="s-svc">
    <h2 id="s-svc">Services <span class="opt">optional</span></h2>
    <div class="row-wrap">
      {#each TEMPLATES as t (t.id)}
        <button type="button" class="btn sm" onclick={() => addTemplate(t.id)} title={t.desc}><Icon name="plus" size={13} />{t.label}</button>
      {/each}
    </div>
    {#each services as s, i (svcKeys[i])}
      <div class="svc">
        <div class="row">
          <b class="mono">{serviceTitle(s)}</b>
          <span class="spacer"></span>
          <button type="button" class="btn ghost icon sm" aria-label="Remove service {serviceTitle(s)}" onclick={() => removeService(i)}><Icon name="x" size={14} /></button>
        </div>
        <div class="form-grid">
          <div class="field">
            <label for="sp-{i}">Protocol</label>
            <select id="sp-{i}" class="select" value={s.protocol} onchange={(e) => setProtocol(i, (e.currentTarget as HTMLSelectElement).value as 'tcp')}><option value="tcp">TCP</option><option value="http">HTTP</option><option value="https">HTTPS</option></select>
          </div>
          <div class="field">
            <label for="sl-{i}">Source port</label>
            <input id="sl-{i}" class="input mono" type="number" bind:value={s.listen_port} aria-invalid={svcErrs[i].listen_port || svcServerErrs[i]?.listen_port ? 'true' : undefined} />
            {#if svcErrs[i].listen_port || svcServerErrs[i]?.listen_port}<span class="ferr">{svcErrs[i].listen_port || svcServerErrs[i].listen_port}</span>{/if}
          </div>
          <div class="field">
            <label for="sd-{i}">Destination port</label>
            <input id="sd-{i}" class="input mono" type="number" bind:value={s.destination_port} aria-invalid={svcErrs[i].destination_port || svcServerErrs[i]?.destination_port ? 'true' : undefined} />
            {#if svcErrs[i].destination_port || svcServerErrs[i]?.destination_port}<span class="ferr">{svcErrs[i].destination_port || svcServerErrs[i].destination_port}</span>{/if}
          </div>
        </div>
        {#if s.protocol === 'https'}
          {#if s.http}
            <CertificatePicker
              id="sc-{svcKeys[i]}" bind:selected={s.http.certificates} certificates={certs.data ?? []} error={certErr(i)}
              onchange={() => clearServerErr(i, 'certificates')} oncreated={() => void certs.reload()}
            />
          {/if}
          <p class="small muted note"><Icon name="info" size={13} /> Pick or create a certificate. Managed certificates are issued automatically once the domain points to this load balancer; until then a self-signed placeholder is served.</p>
        {/if}
        {#each svcOtherErrs[i] ?? [] as msg (msg)}<p class="ferr small err-text">{msg}</p>{/each}
      </div>
    {/each}
    {#if fieldErrs.services}<p class="ferr small err-text">{fieldErrs.services}</p>{/if}
  </section>

  <section aria-labelledby="s-tgt">
    <h2 id="s-tgt">Targets <span class="opt">optional</span></h2>
    <TargetPicker bind:mode={targetMode} bind:targets bind:error={targetErr} />
    {#if plan && targets.length > plan.max_targets}<p class="small warn-text">{plan.name.toUpperCase()} allows {plan.max_targets} targets.</p>{/if}
    {#if fieldErrs.targets}<p class="ferr small err-text">{fieldErrs.targets}</p>{/if}
  </section>

  <section aria-labelledby="s-name">
    <h2 id="s-name">Name &amp; labels</h2>
    <div class="field" style:max-width="420px">
      <label for="lbname">Name</label>
      <input id="lbname" class="input" bind:value={name} placeholder="load-balancer-1" autocomplete="off" spellcheck="false" aria-invalid={nameErr || fieldErrs.name ? 'true' : undefined} />
      {#if nameErr || fieldErrs.name}<span class="ferr">{nameErr || fieldErrs.name}</span>{/if}
    </div>
    <div class="field" style:max-width="560px">
      <span class="label">Labels</span>
      <LabelEditor bind:labels bind:error={labelErr} />
    </div>
  </section>

  <div class="submit">
    <div class="summary small">
      {#if plan}<span class="badge plan">{plan.name}</span>{/if}
      <span class="muted">{services.length} service{services.length === 1 ? '' : 's'} · {targets.length} target{targets.length === 1 ? '' : 's'}</span>
      {#if plan?.price_monthly_cents != null}<span>{price(plan.price_monthly_cents, plan.currency)}/mo</span>{/if}
    </div>
    <a class="btn" href="/load-balancers">Cancel</a>
    <button class="btn primary" type="submit" disabled={blocking || busy}>{busy ? 'Creating…' : 'Create & apply'}</button>
  </div>
</form>

<style>
  .wizard { display: flex; flex-direction: column; gap: 0; max-width: 980px; }
  section { display: flex; flex-direction: column; gap: 12px; padding: 22px 0; border-top: 1px solid var(--border); }
  section:first-child { border-top: 0; padding-top: 0; }
  h2 { font-size: var(--fs-lg); }
  .opt { font-weight: 400; color: var(--muted); font-size: var(--fs-sm); margin-left: 6px; }
  .cards { display: grid; grid-template-columns: repeat(auto-fill, minmax(190px, 1fr)); gap: 10px; }
  .cards.narrow { grid-template-columns: repeat(auto-fill, minmax(260px, 1fr)); }
  .pick {
    display: flex; flex-direction: column; gap: 4px; padding: 12px 14px; border-radius: var(--radius);
    border: 1px solid var(--border); background: var(--surface); cursor: pointer; transition: border-color 120ms ease;
  }
  .pick:hover { border-color: var(--border-strong); }
  .pick.on { border-color: var(--text-2); box-shadow: inset 0 0 0 1px var(--text-2); }
  .pick:has(:focus-visible) { outline: 2px solid var(--focus); outline-offset: 2px; }
  .t { font-weight: 600; }
  .price { font-weight: 600; font-variant-numeric: tabular-nums; }
  .limits { display: grid; grid-template-columns: 1fr auto; gap: 2px 8px; margin: 8px 0 0; font-size: var(--fs-sm); }
  .limits dt { color: var(--muted); }
  .limits dd { margin: 0; text-align: right; font-variant-numeric: tabular-nums; }
  .note { display: flex; align-items: flex-start; gap: 6px; }
  .note :global(svg) { flex: none; margin-top: 3px; }
  .svc { border: 1px solid var(--border); border-radius: var(--radius); background: var(--surface); padding: 12px 14px; display: flex; flex-direction: column; gap: 10px; }
  .submit {
    position: sticky; bottom: 0; display: flex; align-items: center; gap: 8px; padding: 12px 0;
    border-top: 1px solid var(--border); background: var(--bg);
  }
  .summary { display: flex; align-items: center; gap: 10px; margin-right: auto; }
</style>
