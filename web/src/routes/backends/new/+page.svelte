<script lang="ts">
  import Icon from '$lib/components/Icon.svelte';
  import BackendForm from '$lib/components/BackendForm.svelte';
</script>

<svelte:head><title>New backend · Loxy</title></svelte:head>

<nav class="crumbs small" aria-label="Breadcrumb"><a href="/backends">Backends</a><Icon name="chevronRight" size={12} /><span aria-current="page">New</span></nav>
<div class="page-head"><h1>New backend</h1></div>
<p class="section-intro">A backend is a pool of servers with a balancing algorithm and a health check. Add servers after creating it.</p>
<BackendForm />
