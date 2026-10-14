<script lang="ts">
  import { theme, setTheme, effectiveTheme } from '$lib/stores/theme.svelte';
  import Icon from './Icon.svelte';
  let eff = $state<'dark' | 'light'>('dark');
  $effect(() => { void theme.pref; eff = effectiveTheme(); });
</script>

<button class="btn ghost icon" aria-label="Switch to {eff === 'dark' ? 'light' : 'dark'} theme" title="Toggle theme" onclick={() => setTheme(eff === 'dark' ? 'light' : 'dark')}>
  <Icon name={eff === 'dark' ? 'sun' : 'moon'} />
</button>
