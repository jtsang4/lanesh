<script lang="ts">
	import type { Snippet } from 'svelte';
	import Icon, { type IconName } from './Icon.svelte';

	let {
		tone = 'info',
		title,
		icon,
		children
	}: {
		tone?: 'info' | 'warn';
		title?: string;
		icon?: IconName;
		children: Snippet;
	} = $props();
</script>

<div class={['callout', tone]} role={tone === 'warn' ? 'note' : undefined}>
	<Icon name={icon ?? (tone === 'warn' ? 'alert' : 'info')} size={15} />
	<div class="text">
		{#if title}<p class="title">{title}</p>{/if}
		<div class="body">{@render children()}</div>
	</div>
</div>

<style>
	.callout {
		display: flex;
		gap: 10px;
		padding: 11px 14px;
		border-radius: var(--radius-lg);
		border: 1px solid;
		font-size: var(--text-sm);
	}
	.callout > :global(svg) {
		margin-top: 2px;
	}
	.info {
		background: var(--accent-soft);
		border-color: color-mix(in srgb, var(--accent) 22%, transparent);
		color: var(--accent-text);
	}
	.warn {
		background: var(--amber-soft);
		border-color: color-mix(in srgb, var(--amber-dot) 32%, transparent);
		color: var(--amber);
	}
	.text {
		flex: 1;
		min-width: 0;
		display: grid;
		gap: 2px;
	}
	.title {
		font-weight: 500;
	}
	.body {
		color: var(--fg-2);
		line-height: 1.55;
		overflow-wrap: anywhere;
	}
</style>
