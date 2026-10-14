<script lang="ts">
  import Icon from '$lib/components/Icon.svelte';
  import FrontendForm from '$lib/components/FrontendForm.svelte';
</script>

<svelte:head><title>New frontend · Loxy</title></svelte:head>

<nav class="crumbs small" aria-label="Breadcrumb"><a href="/frontends">Frontends</a><Icon name="chevronRight" size={12} /><span aria-current="page">New</span></nav>
<div class="page-head"><h1>New frontend</h1></div>
<FrontendForm oncancel={() => history.back()} />
