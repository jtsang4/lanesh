<script lang="ts">
	import { t } from '#lib/i18n/index.svelte.ts';
	import { app } from '#lib/state/app.svelte.ts';
	import { clientSnippets } from '#lib/snippets.ts';
	import CodeBlock from './CodeBlock.svelte';
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
			{ id: 'json', label: 'Cursor · mcp.json' }
		]}
	/>
	{#if variant === 'claude'}
		<CodeBlock code={s.claude} />
	{:else if variant === 'codex'}
		<CodeBlock code={s.codex} />
		<p class="note">{t('connect.client.codexNote')}</p>
		<CodeBlock code={s.codexEnv} />
	{:else}
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
