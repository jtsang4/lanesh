<script lang="ts">
	import { t } from '#lib/i18n/index.svelte.ts';
	import Icon from './Icon.svelte';

	let {
		total,
		limit,
		offset = $bindable(0),
		busy = false
	}: { total: number; limit: number; offset?: number; busy?: boolean } = $props();

	const from = $derived(total === 0 ? 0 : offset + 1);
	const to = $derived(Math.min(offset + limit, total));

	let nav = $state<HTMLElement>();

	/** Moves to another page, bringing the top of the list back into view. */
	function go(next: number) {
		offset = next;
		const top = nav?.parentElement?.getBoundingClientRect().top ?? 0;
		if (top < 0) window.scrollBy({ top: top - 96, behavior: 'smooth' });
	}
</script>

{#if total > limit}
	<nav class="pager" aria-label={t('pager.label')} aria-busy={busy} bind:this={nav}>
		<span class="range tnum">{t('pager.range', { from, to, total })}</span>
		<div class="buttons">
			<button
				type="button"
				class="btn btn-sm btn-icon"
				aria-label={t('pager.previous')}
				title={t('pager.previous')}
				disabled={offset === 0}
				onclick={() => go(Math.max(0, offset - limit))}
			>
				<Icon name="chevronLeft" size={14} />
			</button>
			<button
				type="button"
				class="btn btn-sm btn-icon"
				aria-label={t('pager.next')}
				title={t('pager.next')}
				disabled={to >= total}
				onclick={() => go(offset + limit)}
			>
				<Icon name="chevronRight" size={14} />
			</button>
		</div>
	</nav>
{/if}

<style>
	.pager {
		display: flex;
		align-items: center;
		justify-content: flex-end;
		gap: 12px;
		margin-top: 10px;
		font-size: var(--text-xs);
		color: var(--fg-3);
	}
	.pager[aria-busy='true'] .range {
		opacity: 0.6;
	}
	.buttons {
		display: flex;
		gap: 4px;
	}
</style>
