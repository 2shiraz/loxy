<script lang="ts">
  /** Everything about one domain: DNS records to create, what DNS returns, HTTPS and actions. */
  import { api, mutate, type Domain, type Certificate } from '$lib/api/client';
  import { confirm } from '$lib/stores/confirm.svelte';
  import { relTime, daysUntil } from '$lib/util/format';
  import Icon from './Icon.svelte';
  import CopyText from './CopyText.svelte';
  import DomainStatus from './DomainStatus.svelte';

  let {
    domain,
    certificates = [],
    onchange,
    onremoved,
    onrefresh,
    headingLevel = 3
  }: {
    domain: Domain;
    certificates?: Certificate[];
    onchange?: (d: Domain) => void;
    onremoved?: () => void;
    /** Something changed elsewhere (e.g. a certificate was requested); refetch. */
    onrefresh?: () => void;
    headingLevel?: 3 | 4;
  } = $props();

  const d = $derived(domain);
  const cert = $derived(d.certificate_id ? certificates.find((c) => c.id === d.certificate_id) : undefined);
  const canIssue = $derived(d.https && d.status === 'ok' && (!d.certificate_id || cert?.status === 'failed'));
  const issueWhy = $derived(
    !d.https ? 'Turn HTTPS on first'
      : d.status !== 'ok' ? 'Available once the DNS records resolve correctly'
      : d.certificate_id && cert?.status !== 'failed' ? 'A certificate already exists' : undefined
  );

  const certInfo = $derived.by((): { cls: string; text: string; detail?: string } => {
    if (!d.https) return { cls: '', text: 'HTTPS off', detail: 'Served over plain HTTP on :80.' };
    if (!d.certificate_id) {
      return d.status === 'ok'
        ? { cls: 'warn', text: 'Being issued', detail: 'DNS is correct. The certificate is requested automatically.' }
        : { cls: '', text: 'Waiting for DNS', detail: 'Issued automatically once the records below resolve.' };
    }
    if (!cert) return { cls: 'warn', text: 'Pending' };
    if (cert.status === 'issued') {
      const days = daysUntil(cert.not_after);
      return { cls: days != null && days < 14 ? 'warn' : 'ok', text: 'Issued', detail: days != null ? `Valid for ${days} more day${days === 1 ? '' : 's'}. Renewed automatically.` : undefined };
    }
    if (cert.status === 'failed') return { cls: 'err', text: 'Failed', detail: cert.last_error ?? undefined };
    return { cls: 'warn', text: cert.status === 'renewing' ? 'Renewing' : 'Pending' };
  });

  let checking = $state(false);
  let issuing = $state(false);
  let saving = $state(false);

  async function verify() {
    checking = true;
    const r = await mutate(api.POST('/domains/{id}/actions/verify', { params: { path: { id: d.id } } }));
    checking = false;
    if (r) onchange?.(r.domain);
  }
  async function issue() {
    issuing = true;
    const r = await mutate(api.POST('/domains/{id}/actions/issue_certificate', { params: { path: { id: d.id } } }), { success: `Requesting a certificate for ${d.fqdn}` });
    issuing = false;
    if (r) onrefresh?.();
  }
  async function setHttps(on: boolean) {
    if (!on) {
      const ok = await confirm({ title: `Turn HTTPS off for ${d.fqdn}?`, message: 'The domain is then served over plain HTTP only and :80 no longer redirects to HTTPS.', confirmLabel: 'Turn HTTPS off', danger: true });
      if (!ok) return;
    }
    saving = true;
    const r = await mutate(api.PATCH('/domains/{id}', { params: { path: { id: d.id } }, body: { https: on } }), { success: on ? 'HTTPS on' : 'HTTPS off' });
    saving = false;
    if (r) onchange?.(r.domain);
  }
  async function remove() {
    const ok = await confirm({
      title: `Remove ${d.fqdn}?`,
      message: d.auto_records ? 'Traffic for this name stops reaching the project. The A/AAAA records Loxy created are deleted at the DNS provider.' : 'Traffic for this name stops reaching the project. Your DNS records are not changed.',
      confirmLabel: 'Remove domain',
      danger: true
    });
    if (!ok) return;
    const r = await mutate(api.DELETE('/domains/{id}', { params: { path: { id: d.id } } }), { success: `${d.fqdn} removed` });
    if (r !== undefined) onremoved?.();
  }

  function family(type: 'A' | 'AAAA') {
    const want = d.required_records.filter((r) => r.type === type).map((r) => r.value);
    const got = type === 'A' ? d.observed.a : d.observed.aaaa;
    if (!want.length) return { state: 'na', got };
    if (!got.length) return { state: 'missing', got };
    return { state: want.every((w) => got.includes(w)) && got.every((g) => want.includes(g)) ? 'ok' : 'wrong', got };
  }
  const H = $derived(`h${headingLevel}`);
</script>

<div class="dd">
  <div class="row-wrap top">
    <DomainStatus status={d.status} />
    <span class="muted small">Checked {relTime(d.checked_at)}</span>
    <span class="spacer"></span>
    <button class="btn sm" onclick={verify} disabled={checking}><Icon name="refresh" size={13} />{checking ? 'Checking…' : 'Check now'}</button>
    <button class="btn sm" onclick={issue} disabled={!canIssue || issuing} title={issueWhy}><Icon name="shield" size={13} />{issuing ? 'Requesting…' : 'Issue certificate'}</button>
  </div>
  {#if d.status_detail}<p class="small muted">{d.status_detail}</p>{/if}

  <section class="stack">
    <svelte:element this={H} class="sh">DNS records to create</svelte:element>
    {#if d.required_records.length}
      <p class="small muted">Create these at your DNS provider{d.auto_records ? ' — Loxy manages them for you' : ''}. Changes can take a few minutes to show up.</p>
      <div class="table-wrap">
        <table class="tbl">
          <thead><tr><th>Type</th><th>Name</th><th>Value</th><th class="right">TTL</th></tr></thead>
          <tbody>
            {#each d.required_records as r (r.type + r.name + r.value)}
              <tr><td class="mono small">{r.type}</td><td><CopyText text={r.name} label="record name {r.name}" /></td><td><CopyText text={r.value} label="record value {r.value}" /></td><td class="right mono small">{r.ttl}</td></tr>
            {/each}
          </tbody>
        </table>
      </div>
    {:else}
      <p class="muted small">No server addresses are configured yet, so Loxy can't tell which records to create. Add them under System.</p>
    {/if}
  </section>

  <section class="stack">
    <svelte:element this={H} class="sh">What DNS returns</svelte:element>
    {#each ['A', 'AAAA'] as const as t (t)}
      {@const f = family(t)}
      {#if f.state !== 'na'}
        <div class="obs">
          <span class="mono small fam">{t}</span>
          {#if f.state === 'ok'}<span class="ok-text small"><Icon name="check" size={13} /> Resolves correctly</span>
          {:else if f.state === 'missing'}<span class="warn-text small"><Icon name="alert" size={13} /> No record yet</span>
          {:else}<span class="err-text small"><Icon name="x" size={13} /> Points elsewhere</span>{/if}
          <span class="mono xs muted">{f.got.join(', ') || 'no answer'}</span>
        </div>
      {/if}
    {/each}
  </section>

  <section class="stack">
    <svelte:element this={H} class="sh">HTTPS</svelte:element>
    <label class="check"><input type="checkbox" checked={d.https} disabled={saving} onchange={(e) => setHttps((e.currentTarget as HTMLInputElement).checked)} />Serve over HTTPS with an automatic certificate (redirect :80 once issued)</label>
    <div class="cert">
      <span class="muted small">Certificate</span>
      <span class="badge {certInfo.cls}">{certInfo.text}</span>
      {#if certInfo.detail}<span class="small muted">{certInfo.detail}</span>{/if}
      {#if cert}<a class="small" href="/certificates">Details</a>{/if}
    </div>
    {#if d.url}<div class="small"><span class="muted">Reachable at</span> <a class="mono" href={d.url} target="_blank" rel="noopener noreferrer">{d.url}</a></div>{/if}
  </section>

  <div><button class="btn danger sm" onclick={remove}>Remove domain</button></div>
</div>

<style>
  .dd { display: flex; flex-direction: column; gap: 18px; }
  .top { gap: 8px; }
  .sh { font-size: var(--fs-md); font-weight: 600; }
  .obs { display: flex; align-items: center; gap: 12px; flex-wrap: wrap; padding: 8px 12px; background: var(--surface-2); border-radius: var(--radius-sm); }
  .fam { width: 40px; color: var(--muted); }
  .obs :global(.icon) { vertical-align: -2px; }
  .cert { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; }
</style>
