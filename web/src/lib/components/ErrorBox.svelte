<script lang="ts">
  import Icon from './Icon.svelte';
  let { message, onretry }: { message: string; onretry?: () => void } = $props();
</script>

<div class="banner err" role="alert">
  <Icon name="alert" />
  <div class="spacer">{message}</div>
  {#if onretry}<button class="btn sm" onclick={onretry}>Try again</button>{/if}
</div>
