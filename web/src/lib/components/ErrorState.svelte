<script lang="ts">
	import { errorMessage } from '#lib/format.ts';
	import { t } from '#lib/i18n/index.svelte.ts';
	import Icon from './Icon.svelte';

	let { error, title, onretry }: { error: unknown; title?: string; onretry?: () => void } =
		$props();
</script>

<div class="err fade-in" role="alert">
	<Icon name="alert" size={16} />
	<div class="text">
		<p class="title">{title ?? t('common.error')}</p>
		<p class="msg">{errorMessage(error)}</p>
	</div>
	{#if onretry}
		<button type="button" class="btn btn-sm" onclick={onretry}>
			<Icon name="refresh" size={13} />
			{t('common.retry')}
		</button>
	{/if}
</div>

<style>
	.err {
		display: flex;
		align-items: flex-start;
		gap: 10px;
		padding: 12px 14px;
		border-radius: var(--radius-lg);
		border: 1px solid var(--red-border);
		background: var(--red-soft);
		color: var(--red);
	}
	.err > :global(svg) {
		margin-top: 2px;
	}
	.text {
		flex: 1;
		min-width: 0;
	}
	.title {
		font-size: var(--text-sm);
		font-weight: 500;
	}
	.msg {
		font-size: var(--text-sm);
		color: var(--fg-2);
		overflow-wrap: anywhere;
	}
	.btn {
		align-self: center;
	}
</style>
