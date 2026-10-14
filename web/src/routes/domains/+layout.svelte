<script lang="ts">
  import { page } from '$app/state';
  import Tabs from '$lib/components/Tabs.svelte';
  let { children } = $props();
  const active = $derived(page.url.pathname.startsWith('/domains/providers') ? 'providers' : page.url.pathname.startsWith('/domains/zones') ? 'zones' : 'domains');
</script>

<div class="page-head"><h1>Domains &amp; DNS</h1></div>
<Tabs tabs={[{ id: 'domains', label: 'Domains', href: '/domains' }, { id: 'providers', label: 'DNS providers', href: '/domains/providers' }, { id: 'zones', label: 'Zones', href: '/domains/zones' }]} {active} label="Domains sections" />
<div style:padding-top="18px">{@render children()}</div>
