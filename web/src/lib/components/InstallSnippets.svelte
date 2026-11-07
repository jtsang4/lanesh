<script lang="ts">
	import { t } from '#lib/i18n/index.svelte.ts';
	import { app } from '#lib/state/app.svelte.ts';
	import { installSnippets } from '#lib/snippets.ts';
	import CodeBlock from './CodeBlock.svelte';
	import Segmented from './Segmented.svelte';

	let { laneId, token }: { laneId: string; token?: string } = $props();

	type Variant = 'unix' | 'windows' | 'cli';
	let variant = $state<Variant>(
		typeof navigator !== 'undefined' && /Windows/i.test(navigator.userAgent) ? 'windows' : 'unix'
	);
	const snippets = $derived(installSnippets(app.baseUrl, laneId, token));
</script>

<div class="snips">
	<Segmented
		label={t('connect.snippetsLabel')}
		bind:value={variant}
		options={[
			{ id: 'unix', label: t('connect.os.unix') },
			{ id: 'windows', label: t('connect.os.windows') },
			{ id: 'cli', label: t('connect.os.cli') }
		]}
	/>
	<CodeBlock code={snippets[variant]} />
</div>

<style>
	.snips {
		display: grid;
		gap: 10px;
		min-width: 0;
	}
</style>
