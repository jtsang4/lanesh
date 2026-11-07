<script lang="ts">
	import type { Snippet } from 'svelte';
	import Icon, { type IconName } from './Icon.svelte';

	let {
		icon,
		title,
		body,
		compact = false,
		children
	}: {
		icon?: IconName;
		title: string;
		body?: string;
		compact?: boolean;
		children?: Snippet;
	} = $props();
</script>

<div class={['empty fade-in', compact && 'compact']}>
	{#if icon}
		<div class="mark"><Icon name={icon} size={18} /></div>
	{/if}
	<h3>{title}</h3>
	{#if body}<p>{body}</p>{/if}
	{#if children}<div class="actions">{@render children()}</div>{/if}
</div>

<style>
	.empty {
		display: flex;
		flex-direction: column;
		align-items: center;
		text-align: center;
		padding: 56px 24px;
		border: 1px dashed var(--border-strong);
		border-radius: var(--radius-lg);
	}
	.compact {
		padding: 32px 20px;
	}
	.mark {
		display: grid;
		place-items: center;
		width: 36px;
		height: 36px;
		margin-bottom: 14px;
		border-radius: 9px;
		background: var(--surface);
		border: 1px solid var(--border);
		box-shadow: var(--shadow-sm);
		color: var(--fg-2);
	}
	h3 {
		font-size: var(--text-md);
		font-weight: 600;
		letter-spacing: -0.006em;
	}
	p {
		margin-top: 6px;
		max-width: 46ch;
		font-size: var(--text-sm);
		color: var(--fg-2);
		line-height: 1.55;
	}
	.actions {
		display: flex;
		gap: var(--s-2);
		margin-top: 18px;
	}
</style>
