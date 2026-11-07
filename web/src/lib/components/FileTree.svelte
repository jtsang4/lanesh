<script lang="ts">
	import { bytes } from '#lib/format.ts';
	import { t } from '#lib/i18n/index.svelte.ts';
	import type { FileEntry } from '#lib/types.ts';
	import Icon from './Icon.svelte';

	let {
		files,
		selected,
		onselect
	}: { files: FileEntry[]; selected: string; onselect: (path: string) => void } = $props();

	// SKILL.md first, then the rest in path order so directories group naturally.
	const sorted = $derived(
		files.toSorted((a, b) => {
			if (a.path === 'SKILL.md') return -1;
			if (b.path === 'SKILL.md') return 1;
			return a.path.localeCompare(b.path);
		})
	);
</script>

<nav class="tree" aria-label={t('skill.files')}>
	<ul>
		{#each sorted as f (f.path)}
			{@const parts = f.path.split('/')}
			{@const depth = parts.length - 1}
			<li style:--depth={depth}>
				{#if f.dir}
					<span class="node dir">
						<Icon name="folder" size={13} />
						<span class="label">{parts.at(-1)}</span>
					</span>
				{:else}
					<button
						type="button"
						class={['node file', selected === f.path && 'on']}
						aria-current={selected === f.path ? 'true' : undefined}
						title="{f.path} · {bytes(f.size)}"
						onclick={() => onselect(f.path)}
					>
						<Icon name={f.path.endsWith('.md') ? 'fileText' : 'file'} size={13} />
						<span class="label">{parts.at(-1)}</span>
					</button>
				{/if}
			</li>
		{/each}
	</ul>
</nav>

<style>
	.tree {
		min-width: 0;
	}
	ul {
		display: grid;
		gap: 1px;
	}
	.node {
		display: flex;
		align-items: center;
		gap: 7px;
		width: 100%;
		height: 26px;
		padding: 0 8px 0 calc(8px + var(--depth) * 14px);
		border-radius: 5px;
		font-size: var(--text-xs);
		text-align: left;
		min-width: 0;
	}
	.node :global(svg) {
		color: var(--fg-3);
	}
	.dir {
		color: var(--fg-3);
	}
	.file {
		color: var(--fg-2);
		transition:
			background-color var(--dur-1) var(--ease),
			color var(--dur-1) var(--ease);
	}
	.file:hover {
		background: var(--hover);
		color: var(--fg);
	}
	.file.on {
		background: var(--active);
		color: var(--fg);
		font-weight: 500;
	}
	.file.on :global(svg) {
		color: var(--fg);
	}
	.file:focus-visible {
		outline-offset: -2px;
	}
	.label {
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		font-family: var(--font-mono);
		font-size: 11.5px;
	}
</style>
