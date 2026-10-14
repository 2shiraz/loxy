<script lang="ts">
  let { value = $bindable(), label, id, error = '', disabled = false }: { value: number; label: string; id: string; error?: string; disabled?: boolean } = $props();
</script>

<div class="field">
  <label for={id}>{label}</label>
  <input {id} class="input mono" type="number" min="1" max="65535" inputmode="numeric" bind:value {disabled} aria-invalid={error ? 'true' : undefined} aria-describedby={error ? id + '-err' : undefined} />
  {#if error}<span class="ferr" id="{id}-err">{error}</span>{/if}
</div>
