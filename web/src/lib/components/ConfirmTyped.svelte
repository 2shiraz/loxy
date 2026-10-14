<script lang="ts">
  import Modal from './Modal.svelte';
  import { confirmState, settle } from '$lib/stores/confirm.svelte';

  let typed = $state('');
  let open = $state(false);
  $effect(() => { open = confirmState.open; if (open) typed = ''; });
  const o = $derived(confirmState.opts);
  const ok = $derived(!o?.typed || typed === o.typed);
</script>

<Modal bind:open title={o?.title ?? ''} width={460} onclose={() => settle(false)}>
  <form class="stack" id="confirm-form" onsubmit={(e) => { e.preventDefault(); if (ok) settle(true); }}>
    <p class="text">{o?.message}</p>
    {#if o?.typed}
      <div class="field">
        <label for="confirm-typed">Type <code>{o.typed}</code> to confirm</label>
        <input id="confirm-typed" class="input mono" bind:value={typed} autocomplete="off" spellcheck="false" />
      </div>
    {/if}
  </form>
  {#snippet footer()}
    <button class="btn" type="button" onclick={() => settle(false)}>Cancel</button>
    <button class="btn {o?.danger ? 'danger solid' : 'primary'}" type="submit" form="confirm-form" disabled={!ok}>{o?.confirmLabel ?? 'Confirm'}</button>
  {/snippet}
</Modal>

<style>
  .text { color: var(--text-2); }
</style>
