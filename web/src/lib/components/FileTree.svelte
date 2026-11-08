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

	// Long names are shortened in the middle so that the extension stays
	// visible ("example-determi….png"). Names use a monospace font, so the
	// number of characters that fit is computed from the row width; CSS
	// ellipsis would leave a gap because the clip edge rarely falls on a glyph
	// boundary.
	const ROW_CHROME = 8 + 8 + 13 + 7; // padding left/right, icon, gap
	const INDENT = 14;

	let width = $state(0);
	let charWidth = $state(0);
	let probe: HTMLSpanElement | undefined = $state();

	$effect(() => {
		if (probe) charWidth = probe.getBoundingClientRect().width / 10;
	});

	function fit(name: string, depth: number): string {
		if (!width || !charWidth) return name;
		const max = Math.floor((width - ROW_CHROME - depth * INDENT) / charWidth);
		if (name.length <= max || max < 4) return name;
		const dot = name.lastIndexOf('.');
		const ext = dot > 0 && name.length - dot <= 8 ? name.slice(dot) : '';
		const keep = max - ext.length - 1;
		return keep >= 3 ? name.slice(0, keep) + '…' + ext : name.slice(0, max - 1) + '…';
	}

	// SKILL.md first, then the rest in path order so directories group naturally.
	const sorted = $derived(
		files.toSorted((a, b) => {
			if (a.path === 'SKILL.md') return -1;
			if (b.path === 'SKILL.md') return 1;
			return a.path.localeCompare(b.path);
		})
	);
</script>

<nav class="tree" aria-label={t('skill.files')} bind:clientWidth={width}>
	<span class="probe" aria-hidden="true" bind:this={probe}>MMMMMMMMMM</span>
	<ul>
		{#each sorted as f (f.path)}
			{@const parts = f.path.split('/')}
			{@const depth = parts.length - 1}
			{@const name = parts.at(-1) ?? ''}
			<li style:--depth={depth}>
				{#if f.dir}
					<span class="node dir" title={f.path}>
						<Icon name="folder" size={13} />
						<span class="label">{fit(name, depth)}</span>
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
						<span class="label">{fit(name, depth)}</span>
					</button>
				{/if}
			</li>
		{/each}
	</ul>
</nav>

<style>
	.tree {
		position: relative;
		min-width: 0;
	}
	ul {
		display: grid;
		/* Without minmax(0, …) the track grows to the longest name and rows overflow the pane. */
		grid-template-columns: minmax(0, 1fr);
		gap: 1px;
	}
	li {
		min-width: 0;
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
		flex: none;
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
	.label,
	.probe {
		white-space: nowrap;
		font-family: var(--font-mono);
		font-size: 11.5px;
	}
	.label {
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis; /* fallback before the width is measured */
	}
	.probe {
		position: absolute;
		visibility: hidden;
		pointer-events: none;
	}
</style>
