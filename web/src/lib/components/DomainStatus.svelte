<script lang="ts">
  import Icon from './Icon.svelte';
  let { status }: { status: string } = $props();
  const m = $derived(({
    ok: { c: 'ok', i: 'check', t: 'Resolves correctly' },
    wrong_ip: { c: 'err', i: 'x', t: 'Wrong IP' },
    missing: { c: 'warn', i: 'alert', t: 'Missing' },
    error: { c: 'err', i: 'alert', t: 'Check failed' },
    unknown: { c: '', i: 'info', t: 'Unknown' }
  } as Record<string, { c: string; i: string; t: string }>)[status] ?? { c: '', i: 'info', t: status });
</script>

<span class="badge {m.c}"><Icon name={m.i} size={11} />{m.t}</span>
