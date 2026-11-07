<script lang="ts">
	import type { Snippet } from 'svelte';

	let {
		title,
		loading = false,
		description,
		eyebrow,
		meta,
		actions
	}: {
		title?: string;
		loading?: boolean;
		description?: string;
		eyebrow?: Snippet;
		meta?: Snippet;
		actions?: Snippet;
	} = $props();
</script>

<header class="ph">
	{#if eyebrow}<div class="eyebrow">{@render eyebrow()}</div>{/if}
	<div class="row">
		<div class="titles">
			{#if loading}
				<span class="skeleton title-skel" aria-hidden="true"></span>
			{:else if title !== undefined}
				<h1>{title}</h1>
			{/if}
			{#if description}<p class="desc">{description}</p>{/if}
			{#if meta}<div class="meta">{@render meta()}</div>{/if}
		</div>
		{#if actions}<div class="actions">{@render actions()}</div>{/if}
	</div>
</header>

<style>
	.ph {
		display: grid;
		grid-template-columns: minmax(0, 1fr);
		gap: 10px;
		margin-bottom: 24px;
	}
	.eyebrow {
		display: flex;
		align-items: center;
		gap: 6px;
		font-size: var(--text-xs);
		color: var(--fg-3);
		min-height: 18px;
	}
	.eyebrow :global(a) {
		color: var(--fg-3);
		transition: color var(--dur-1) var(--ease);
	}
	.eyebrow :global(a:hover) {
		color: var(--fg);
	}
	.row {
		display: flex;
		align-items: flex-end;
		justify-content: space-between;
		gap: 16px;
		flex-wrap: wrap;
	}
	.titles {
		min-width: 0;
		display: grid;
		gap: 6px;
	}
	h1 {
		font-size: var(--text-2xl);
		font-weight: 600;
		letter-spacing: -0.025em;
		line-height: 1.2;
		overflow-wrap: anywhere;
	}
	.title-skel {
		width: 180px;
		height: 29px;
		border-radius: 6px;
	}
	.desc {
		font-size: var(--text-sm);
		color: var(--fg-2);
		max-width: 64ch;
	}
	.meta {
		display: flex;
		align-items: center;
		flex-wrap: wrap;
		gap: 6px 14px;
		font-size: var(--text-xs);
		color: var(--fg-3);
	}
	.actions {
		display: flex;
		gap: 8px;
		align-items: center;
		min-width: 0;
		max-width: 100%;
	}
	@media (max-width: 640px) {
		.actions {
			width: 100%;
		}
		h1 {
			font-size: var(--text-xl);
		}
	}
</style>
