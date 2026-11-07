<script lang="ts">
	import type { Snippet } from 'svelte';

	let {
		tone = 'neutral',
		title,
		children
	}: {
		tone?: 'neutral' | 'green' | 'amber' | 'red' | 'accent' | 'outline';
		title?: string;
		children: Snippet;
	} = $props();
</script>

<span class={['badge', tone]} {title}>{@render children()}</span>

<style>
	.badge {
		display: inline-flex;
		align-items: center;
		gap: 5px;
		height: 20px;
		padding: 0 7px;
		border-radius: 5px;
		font-size: var(--text-2xs);
		font-weight: 500;
		line-height: 1;
		white-space: nowrap;
		letter-spacing: 0.01em;
	}
	.neutral {
		background: var(--hover);
		color: var(--fg-2);
	}
	.outline {
		box-shadow: inset 0 0 0 1px var(--border-strong);
		color: var(--fg-2);
	}
	.green {
		background: var(--green-soft);
		color: var(--green);
	}
	.amber {
		background: var(--amber-soft);
		color: var(--amber);
	}
	.red {
		background: var(--red-soft);
		color: var(--red);
	}
	.accent {
		background: var(--accent-soft);
		color: var(--accent-text);
	}
</style>
