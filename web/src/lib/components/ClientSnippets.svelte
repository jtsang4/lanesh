<script lang="ts">
	import { t } from '#lib/i18n/index.svelte.ts';
	import { app } from '#lib/state/app.svelte.ts';
	import { clientSnippets } from '#lib/snippets.ts';
	import CodeBlock from './CodeBlock.svelte';
	import Rich from './Rich.svelte';
	import Segmented from './Segmented.svelte';

	let { laneId, token }: { laneId: string; token?: string } = $props();

	type Variant = 'claude' | 'codex' | 'json';
	let variant = $state<Variant>('claude');
	const s = $derived(clientSnippets(app.baseUrl, laneId, token));
</script>

<div class="snips">
	<Segmented
		label={t('connect.snippetsLabel')}
		bind:value={variant}
		options={[
			{ id: 'claude', label: 'Claude Code' },
			{ id: 'codex', label: 'Codex' },
			{ id: 'json', label: 'Cursor' }
		]}
	/>
	{#if variant === 'claude'}
		<CodeBlock code={s.claude} />
		<p class="note"><Rich text={t('connect.client.claudeNote')} /></p>
	{:else if variant === 'codex'}
		<p class="note"><Rich text={t('connect.client.codexNote')} /></p>
		<CodeBlock code={s.codex} wrap={false} />
	{:else}
		<p class="note"><Rich text={t('connect.client.cursorNote')} /></p>
		<CodeBlock code={s.json} wrap={false} />
	{/if}
</div>

<style>
	.snips {
		display: grid;
		gap: 10px;
		min-width: 0;
	}
	.note {
		font-size: var(--text-xs);
		color: var(--fg-2);
		margin-top: 2px;
	}
</style>
