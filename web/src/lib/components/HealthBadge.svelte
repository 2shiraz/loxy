<script lang="ts">
  import Icon from './Icon.svelte';
  let { status, label }: { status: string; label?: string } = $props();
  const map: Record<string, { cls: string; icon: string; text: string }> = {
    healthy: { cls: 'ok', icon: 'check', text: 'Healthy' },
    ok: { cls: 'ok', icon: 'check', text: 'OK' },
    issued: { cls: 'ok', icon: 'check', text: 'Issued' },
    active: { cls: 'ok', icon: 'check', text: 'Active' },
    degraded: { cls: 'warn', icon: 'alert', text: 'Degraded' },
    draining: { cls: 'warn', icon: 'minus', text: 'Draining' },
    maintenance: { cls: '', icon: 'pause', text: 'Maintenance' },
    pending: { cls: 'warn', icon: 'refresh', text: 'Pending' },
    renewing: { cls: 'warn', icon: 'refresh', text: 'Renewing' },
    applying: { cls: 'warn', icon: 'refresh', text: 'Applying' },
    unhealthy: { cls: 'err', icon: 'x', text: 'Unhealthy' },
    failed: { cls: 'err', icon: 'x', text: 'Failed' },
    error: { cls: 'err', icon: 'x', text: 'Error' },
    apply_error: { cls: 'err', icon: 'alert', text: 'Apply error' },
    unknown: { cls: '', icon: 'info', text: 'Unknown' }
  };
  const m = $derived(map[status] ?? { cls: '', icon: 'info', text: status });
</script>

<span class="badge {m.cls}"><Icon name={m.icon} size={12} />{label ?? m.text}</span>
