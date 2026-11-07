<script lang="ts">
	// Renders a translated string, turning `backtick` spans into inline code. Plain text only.
	let { text }: { text: string } = $props();

	const parts = $derived(text.split(/`([^`]+)`/));
</script>

{#each parts as part, i (i)}{#if i % 2 === 1}<code class="inline">{part}</code
		>{:else}{part}{/if}{/each}
