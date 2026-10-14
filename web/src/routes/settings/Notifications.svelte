<script lang="ts">
  import { api, unwrap, mutate, type NotificationChannel, type AlertRule } from '$lib/api/client';
  import { resource } from '$lib/stores/resource.svelte';
  import { confirm } from '$lib/stores/confirm.svelte';
  import { dateTime, duration } from '$lib/util/format';
  import Icon from '$lib/components/Icon.svelte';
  import Modal from '$lib/components/Modal.svelte';
  import Skeleton from '$lib/components/Skeleton.svelte';
  import ErrorBox from '$lib/components/ErrorBox.svelte';
  import EmptyState from '$lib/components/EmptyState.svelte';

  let { canAdmin }: { canAdmin: boolean } = $props();

  type Kind = NotificationChannel['kind'];
  type Severity = AlertRule['min_severity'];

  const channels = resource(async () => unwrap(api.GET('/notification_channels')));
  const rules = resource(async () => (await unwrap(api.GET('/alert_rules'))).alert_rules);

  const KIND_LABEL: Record<Kind, string> = { webhook: 'Webhook', slack: 'Slack', telegram: 'Telegram', email: 'Email' };
  const EVENT_KINDS = [
    ['target.health.changed', 'Target health changed'],
    ['apply.failed', 'Config apply failed'],
    ['config.applied', 'Config applied'],
    ['config.rollback', 'Config rolled back'],
    ['domain.status.changed', 'Domain status changed'],
    ['domain.sync.failed', 'Domain sync failed'],
    ['certificate.updated', 'Certificate updated'],
    ['backup.failed', 'Backup failed']
  ] as const;
  const SEVERITIES: [Severity, string][] = [['info', 'Info and above'], ['warning', 'Warning and above'], ['error', 'Errors only']];

  const kinds = $derived(channels.data?.kinds ?? {});
  const kindList = $derived((Object.keys(kinds) as Kind[]).filter((k) => k in KIND_LABEL));
  const chanById = $derived(new Map((channels.data?.notification_channels ?? []).map((c) => [c.id, c])));

  // ---- Channels ----
  let cOpen = $state(false);
  let cBusy = $state(false);
  let nc = $state<{ name: string; kind: Kind; config: Record<string, string> }>({ name: '', kind: 'webhook', config: {} });
  let cErr = $state<Record<string, string>>({});
  const fields = $derived(kinds[nc.kind] ?? []);
  let testing = $state<number | null>(null);

  function startChannel() {
    nc = { name: '', kind: kindList[0] ?? 'webhook', config: {} };
    cErr = {};
    cOpen = true;
  }
  function setKind(k: Kind) {
    nc.kind = k;
    nc.config = {};
    cErr = {};
  }
  async function createChannel() {
    cErr = {};
    const name = nc.name.trim();
    if (!name) cErr.name = 'Give the channel a name';
    const config: Record<string, string> = {};
    for (const f of fields) {
      const v = (nc.config[f.name] ?? '').trim();
      if (!v) cErr[`config.${f.name}`] = `${f.label} is required`;
      else config[f.name] = v;
    }
    if (Object.keys(cErr).length) return;
    cBusy = true;
    const r = await mutate(api.POST('/notification_channels', { body: { name, kind: nc.kind, config } }), { success: 'Channel added', onFieldErrors: (f) => (cErr = f) });
    cBusy = false;
    if (r) { cOpen = false; void channels.reload(); }
  }
  async function testChannel(c: NotificationChannel) {
    testing = c.id;
    await mutate(api.POST('/notification_channels/{id}/actions/test', { params: { path: { id: c.id } } }), { success: `Test notification sent to ${c.name}` });
    testing = null;
  }
  async function delChannel(c: NotificationChannel) {
    const used = (rules.data ?? []).filter((r) => r.channel_id === c.id).length;
    const ok = await confirm({
      title: `Delete channel “${c.name}”?`,
      message: used ? `${used} alert rule${used === 1 ? '' : 's'} deliver to this channel and will stop notifying.` : 'Nothing will be delivered to it any more.',
      confirmLabel: 'Delete channel',
      danger: true
    });
    if (!ok) return;
    const r = await mutate(api.DELETE('/notification_channels/{id}', { params: { path: { id: c.id } } }), { success: 'Channel deleted' });
    if (r !== undefined) { void channels.reload(); void rules.reload(); }
  }
  function fieldErr(name: string) { return cErr[`config.${name}`] ?? cErr[name]; }

  // ---- Rules ----
  let rOpen = $state(false);
  let rBusy = $state(false);
  let nr = $state<{ event_kinds: string[]; label_filter: string; channel_id: number | null; min_severity: Severity; cooldown_s: number }>({ event_kinds: [], label_filter: '', channel_id: null, min_severity: 'warning', cooldown_s: 300 });
  let rErr = $state<Record<string, string>>({});

  function startRule() {
    nr = { event_kinds: [], label_filter: '', channel_id: channels.data?.notification_channels[0]?.id ?? null, min_severity: 'warning', cooldown_s: 300 };
    rErr = {};
    rOpen = true;
  }
  function toggleKind(k: string, on: boolean) {
    nr.event_kinds = on ? [...nr.event_kinds, k] : nr.event_kinds.filter((x) => x !== k);
  }
  async function createRule() {
    rErr = {};
    if (nr.channel_id == null) rErr.channel_id = 'Choose a channel';
    const filter = nr.label_filter.trim();
    if (filter && !/^[A-Za-z0-9._/-]+(!?=[A-Za-z0-9._-]*)?(\s*,\s*[A-Za-z0-9._/-]+(!?=[A-Za-z0-9._-]*)?)*$/.test(filter)) rErr.label_filter = 'Use key=value pairs separated by commas';
    if (!Number.isInteger(nr.cooldown_s) || nr.cooldown_s < 0) rErr.cooldown_s = 'Whole seconds, 0 or more';
    if (Object.keys(rErr).length) return;
    rBusy = true;
    const body = { event_kinds: nr.event_kinds, label_filter: filter, channel_id: nr.channel_id as number, min_severity: nr.min_severity, cooldown_s: nr.cooldown_s };
    const r = await mutate(api.POST('/alert_rules', { body }), { success: 'Alert rule added', onFieldErrors: (f) => (rErr = f) });
    rBusy = false;
    if (r) { rOpen = false; void rules.reload(); }
  }
  async function delRule(r: AlertRule) {
    const ok = await confirm({ title: 'Delete alert rule?', message: `Notifications to ${chanById.get(r.channel_id)?.name ?? 'this channel'} from this rule stop immediately.`, confirmLabel: 'Delete rule', danger: true });
    if (!ok) return;
    const x = await mutate(api.DELETE('/alert_rules/{id}', { params: { path: { id: r.id } } }), { success: 'Alert rule deleted' });
    if (x !== undefined) void rules.reload();
  }
  const hasChannels = $derived((channels.data?.notification_channels.length ?? 0) > 0);
</script>

<div class="stack-lg">
  <section class="card" aria-labelledby="snc">
    <div class="card-head">
      <h2 id="snc">Channels</h2><span class="spacer"></span>
      {#if canAdmin && hasChannels}<button class="btn sm" onclick={startChannel}><Icon name="plus" size={13} />Add channel</button>{/if}
    </div>
    {#if channels.error && !channels.data}<ErrorBox message={channels.error} onretry={channels.reload} />
    {:else if !channels.data}<Skeleton rows={3} />
    {:else if !hasChannels}
      <EmptyState icon="activity" title="No notification channels" text="Channels are where alerts go: a webhook, Slack, Telegram or email.">
        {#if canAdmin}<button class="btn primary sm" onclick={startChannel}><Icon name="plus" size={13} />Add channel</button>{/if}
      </EmptyState>
    {:else}
      <div class="table-wrap bare">
        <table class="tbl">
          <thead><tr><th>Name</th><th>Type</th><th>Status</th><th>Created</th><th><span class="sr-only">Actions</span></th></tr></thead>
          <tbody>
            {#each channels.data.notification_channels as c (c.id)}
              <tr>
                <td class="wrap">{c.name}</td>
                <td class="small">{KIND_LABEL[c.kind] ?? c.kind}</td>
                <td>{#if c.enabled}<span class="badge ok">Enabled</span>{:else}<span class="badge">Disabled</span>{/if}</td>
                <td class="small muted">{dateTime(c.created_at)}</td>
                <td class="actions"><div class="row">
                  {#if canAdmin}
                    <button class="btn sm" onclick={() => testChannel(c)} disabled={testing === c.id} aria-label="Send test notification to {c.name}">{testing === c.id ? 'Sending…' : 'Send test'}</button>
                    <button class="btn sm danger" onclick={() => delChannel(c)} aria-label="Delete channel {c.name}">Delete</button>
                  {/if}
                </div></td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
  </section>

  <section class="card" aria-labelledby="snr">
    <div class="card-head">
      <h2 id="snr">Alert rules</h2><span class="spacer"></span>
      {#if canAdmin && hasChannels && rules.data?.length}<button class="btn sm" onclick={startRule}><Icon name="plus" size={13} />Add rule</button>{/if}
    </div>
    {#if rules.error && !rules.data}<ErrorBox message={rules.error} onretry={rules.reload} />
    {:else if !rules.data || !channels.data}<Skeleton rows={3} />
    {:else if rules.data.length === 0}
      <EmptyState icon="alert" title="No alert rules" text={hasChannels ? 'Rules decide which events are sent to which channel.' : 'Add a channel first, then create rules that route events to it.'}>
        {#if canAdmin && hasChannels}<button class="btn primary sm" onclick={startRule}><Icon name="plus" size={13} />Add rule</button>{/if}
      </EmptyState>
    {:else}
      <div class="table-wrap bare">
        <table class="tbl">
          <thead><tr><th>Events</th><th>Label filter</th><th>Severity</th><th>Channel</th><th class="right">Cooldown</th><th><span class="sr-only">Actions</span></th></tr></thead>
          <tbody>
            {#each rules.data as r (r.id)}
              <tr>
                <td class="wrap">{#if r.event_kinds.length}<span class="row-wrap">{#each r.event_kinds as k (k)}<span class="chip">{k}</span>{/each}</span>{:else}<span class="muted">All events</span>{/if}</td>
                <td>{#if r.label_filter}<span class="chip">{r.label_filter}</span>{:else}<span class="muted small">All</span>{/if}</td>
                <td class="small">{r.min_severity}+</td>
                <td class="small">{chanById.get(r.channel_id)?.name ?? `#${r.channel_id}`}</td>
                <td class="small muted right">{r.cooldown_s ? duration(r.cooldown_s) : 'None'}</td>
                <td class="actions">{#if canAdmin}<button class="btn sm danger" onclick={() => delRule(r)} aria-label="Delete alert rule">Delete</button>{/if}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
  </section>
</div>

<Modal bind:open={cOpen} title="Add notification channel" width={460}>
  <form id="ncf" class="stack" onsubmit={(e) => { e.preventDefault(); void createChannel(); }} novalidate autocomplete="off">
    <div class="field">
      <label for="ncn">Name</label>
      <input id="ncn" class="input" bind:value={nc.name} placeholder="e.g. ops-slack…" aria-invalid={cErr.name ? 'true' : undefined} />
      {#if cErr.name}<span class="ferr">{cErr.name}</span>{/if}
    </div>
    <div class="field">
      <label for="nck">Type</label>
      <select id="nck" class="select" value={nc.kind} onchange={(e) => setKind(e.currentTarget.value as Kind)}>
        {#each kindList as k (k)}<option value={k}>{KIND_LABEL[k]}</option>{/each}
      </select>
    </div>
    {#each fields as f (nc.kind + f.name)}
      {@const fe = fieldErr(f.name)}
      <div class="field">
        <label for="ncf-{f.name}">{f.label}</label>
        <input id="ncf-{f.name}" class="input" class:mono={!f.secret} type={f.secret ? 'password' : 'text'} autocomplete={f.secret ? 'new-password' : 'off'} spellcheck="false" bind:value={nc.config[f.name]} aria-invalid={fe ? 'true' : undefined} />
        {#if fe}<span class="ferr">{fe}</span>{:else if f.secret}<span class="hint">Stored encrypted; never shown again.</span>{/if}
      </div>
    {/each}
  </form>
  {#snippet footer()}
    <button class="btn" onclick={() => (cOpen = false)}>Cancel</button>
    <button class="btn primary" type="submit" form="ncf" disabled={cBusy}>{cBusy ? 'Adding…' : 'Add channel'}</button>
  {/snippet}
</Modal>

<Modal bind:open={rOpen} title="Add alert rule" width={500}>
  <form id="nrf" class="stack" onsubmit={(e) => { e.preventDefault(); void createRule(); }} novalidate autocomplete="off">
    <fieldset class="field">
      <legend class="label">Events</legend>
      <div class="kinds">
        {#each EVENT_KINDS as [k, l] (k)}
          <label class="check"><input type="checkbox" checked={nr.event_kinds.includes(k)} onchange={(e) => toggleKind(k, e.currentTarget.checked)} /><span>{l} <span class="mono xs muted">{k}</span></span></label>
        {/each}
      </div>
      {#if rErr.event_kinds}<span class="ferr">{rErr.event_kinds}</span>{:else}<span class="hint">{nr.event_kinds.length ? `${nr.event_kinds.length} selected.` : 'None selected — the rule matches all events.'}</span>{/if}
    </fieldset>
    <div class="field">
      <label for="nrl">Label filter</label>
      <input id="nrl" class="input mono" bind:value={nr.label_filter} placeholder="env=prod" spellcheck="false" aria-invalid={rErr.label_filter ? 'true' : undefined} />
      {#if rErr.label_filter}<span class="ferr">{rErr.label_filter}</span>{:else}<span class="hint">Label selector on project labels. Leave empty to match everything.</span>{/if}
    </div>
    <div class="form-grid">
      <div class="field">
        <label for="nrs">Minimum severity</label>
        <select id="nrs" class="select" bind:value={nr.min_severity}>{#each SEVERITIES as [v, l] (v)}<option value={v}>{l}</option>{/each}</select>
      </div>
      <div class="field">
        <label for="nrcd">Cooldown (seconds)</label>
        <input id="nrcd" class="input" type="number" min="0" step="1" inputmode="numeric" bind:value={nr.cooldown_s} aria-invalid={rErr.cooldown_s ? 'true' : undefined} />
        {#if rErr.cooldown_s}<span class="ferr">{rErr.cooldown_s}</span>{:else}<span class="hint">Minimum gap between repeats.</span>{/if}
      </div>
    </div>
    <div class="field">
      <label for="nrc">Send to</label>
      <select id="nrc" class="select" bind:value={nr.channel_id} aria-invalid={rErr.channel_id ? 'true' : undefined}>
        {#each channels.data?.notification_channels ?? [] as c (c.id)}<option value={c.id}>{c.name} ({KIND_LABEL[c.kind] ?? c.kind})</option>{/each}
      </select>
      {#if rErr.channel_id}<span class="ferr">{rErr.channel_id}</span>{/if}
    </div>
  </form>
  {#snippet footer()}
    <button class="btn" onclick={() => (rOpen = false)}>Cancel</button>
    <button class="btn primary" type="submit" form="nrf" disabled={rBusy}>{rBusy ? 'Adding…' : 'Add rule'}</button>
  {/snippet}
</Modal>

<style>
  .kinds { display: grid; gap: 8px; padding: 2px 0; }
  legend { padding: 0; margin-bottom: 5px; font-size: var(--fs-sm); color: var(--text-2); font-weight: 500; }
  .tbl td.wrap { white-space: normal; overflow-wrap: anywhere; }
  .actions .row { justify-content: flex-end; }
</style>
