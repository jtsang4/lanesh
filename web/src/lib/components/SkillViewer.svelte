<script lang="ts">
	import { api, ApiError } from '#lib/api.ts';
	import { bytes, errorMessage } from '#lib/format.ts';
	import { t, tn } from '#lib/i18n/index.svelte.ts';
	import { splitFrontmatter } from '#lib/markdown.ts';
	import type { FileEntry, Skill, SkillReadResult } from '#lib/types.ts';
	import Badge from './Badge.svelte';
	import CopyButton from './CopyButton.svelte';
	import FileTree from './FileTree.svelte';
	import Icon from './Icon.svelte';
	import Markdown from './Markdown.svelte';
	import Segmented from './Segmented.svelte';

	let {
		laneId,
		machineId,
		skill,
		online,
		file,
		onfile
	}: {
		laneId: string;
		machineId: string;
		skill: Skill;
		online: boolean;
		file: string;
		onfile: (path: string) => void;
	} = $props();

	type Result = { ok: true; data: SkillReadResult } | { ok: false; error: unknown };

	// Per-viewer cache (keyed by skill hash) so switching between files is instant.
	// Failed reads are dropped so they are retried, e.g. once the machine is back online.
	const cache = new Map<string, Promise<Result>>();
	let result = $state.raw<Result | null>(null);
	let files = $state.raw<FileEntry[]>([]);
	let mode = $state<'rendered' | 'raw'>('rendered');

	const path = $derived(file || 'SKILL.md');

	async function fetchResult(skillName: string, p: string, key: string): Promise<Result> {
		try {
			const data = await api.readSkill(
				laneId,
				machineId,
				skillName,
				p === 'SKILL.md' ? undefined : p
			);
			return { ok: true, data };
		} catch (error) {
			cache.delete(key);
			return { ok: false, error };
		}
	}

	function read(skillName: string, p: string, hash: string): Promise<Result> {
		const key = `${hash}:${skillName}:${p}`;
		let pending = cache.get(key);
		if (!pending) {
			pending = fetchResult(skillName, p, key);
			cache.set(key, pending);
		}
		return pending;
	}

	async function loadTree(name: string, hash: string) {
		const r = await read(name, 'SKILL.md', hash);
		if (name === skill.name && r.ok) files = r.data.files ?? [];
	}

	async function loadFile(name: string, p: string, hash: string) {
		const r = await read(name, p, hash);
		if (name === skill.name && p === path) result = r;
	}

	// Load SKILL.md (which also returns the file tree) whenever the skill changes,
	// and again when the machine comes back online.
	$effect(() => {
		void online;
		files = [];
		void loadTree(skill.name, skill.hash);
	});

	// Load the selected file.
	$effect(() => {
		void online;
		result = null;
		void loadFile(skill.name, path, skill.hash);
	});

	const isOffline = $derived(
		!online ||
			(result && !result.ok && result.error instanceof ApiError && result.error.status === 503)
	);
	const data = $derived(result?.ok ? result.data : null);
	const isMarkdown = $derived(/\.(md|markdown)$/i.test(path));
	const isBinary = $derived(data?.encoding === 'base64');
	const fm = $derived(data && isMarkdown && !isBinary ? splitFrontmatter(data.content) : null);
	const lines = $derived(data && !isBinary ? data.content.replace(/\n$/, '').split('\n') : []);
</script>

<div class="viewer">
	<header class="vh">
		<div class="vt">
			<h3 class="mono">{skill.name}</h3>
			<Badge tone="outline">{skill.source}</Badge>
		</div>
		<div class="vpath">
			<code class="mono" title={skill.path}>{skill.path}</code>
			<CopyButton text={skill.path} />
		</div>
	</header>

	<div class="body">
		{#if files.length > 0}
			<aside class="side">
				<FileTree {files} selected={path} onselect={onfile} />
			</aside>
		{/if}

		<div class="content">
			<div class="ch">
				<span class="fname mono">
					<Icon name={isMarkdown ? 'fileText' : 'file'} size={13} />
					{path}
				</span>
				{#if data}
					<span class="fmeta">
						{#if !isBinary}{tn('skill.lines', lines.length)} ·{/if}
						{bytes(data.size)}
					</span>
					{#if isMarkdown && !isBinary}
						<Segmented
							size="xs"
							label={t('skill.viewMode')}
							bind:value={mode}
							options={[
								{ id: 'rendered', label: t('skill.rendered') },
								{ id: 'raw', label: t('skill.raw') }
							]}
						/>
					{/if}
				{/if}
			</div>

			{#if isOffline && !data}
				<div class="state">
					<Icon name="info" size={16} />
					<p>{t('skill.offline')}</p>
				</div>
			{:else if result === null}
				<div class="loading" aria-busy="true">
					{#each [70, 92, 54, 80, 38] as w, i (i)}
						<span class="skeleton" style:width="{w}%" style:height="10px"></span>
					{/each}
				</div>
			{:else if !result.ok}
				<div class="state error" role="alert">
					<Icon name="alert" size={16} />
					<div>
						<p>{t('skill.loadError')}</p>
						<p class="sub">{errorMessage(result.error)}</p>
					</div>
				</div>
			{:else if data && isBinary}
				<div class="state">
					<Icon name="file" size={16} />
					<p>{t('skill.binary', { size: bytes(data.size) })}</p>
				</div>
			{:else if data && data.content === ''}
				<div class="state"><p>{t('skill.empty')}</p></div>
			{:else if data && fm && mode === 'rendered'}
				<div class="doc fade-in">
					{#if fm.fields.length > 0}
						<dl class="fm">
							{#each fm.fields as [k, v] (k)}
								<div>
									<dt class="mono">{k}</dt>
									<dd>{v}</dd>
								</div>
							{/each}
						</dl>
					{/if}
					<Markdown source={fm.body} />
				</div>
			{:else if data}
				<pre class="code fade-in"><code
						>{#each lines as line, i (i)}<span class="ln">{line}{'\n'}</span>{/each}</code
					></pre>
			{/if}

			{#if data?.truncated}
				<p class="trunc">
					<Icon name="info" size={13} />
					{t('skill.truncated', { size: bytes(data.content.length) })}
				</p>
			{/if}
		</div>
	</div>
</div>

<style>
	.viewer {
		display: flex;
		flex-direction: column;
		min-width: 0;
		height: 100%;
	}
	.vh {
		display: grid;
		gap: 4px;
		padding: 14px 18px 12px;
		border-bottom: 1px solid var(--border);
	}
	.vt {
		display: flex;
		align-items: center;
		gap: 10px;
		min-width: 0;
	}
	h3 {
		font-size: 14px;
		font-weight: 600;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.vpath {
		display: flex;
		align-items: center;
		gap: 2px;
		min-width: 0;
	}
	.vpath code {
		min-width: 0;
		font-size: 11px;
		color: var(--fg-3);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.body {
		display: grid;
		grid-template-columns: 188px minmax(0, 1fr);
		flex: 1;
		min-height: 0;
	}
	.body:has(> .content:only-child) {
		grid-template-columns: minmax(0, 1fr);
	}
	.side {
		padding: 10px 8px;
		border-right: 1px solid var(--border);
		background: var(--surface-2);
	}
	.content {
		min-width: 0;
		display: flex;
		flex-direction: column;
	}
	.ch {
		display: flex;
		align-items: center;
		gap: 10px;
		height: 42px;
		padding: 0 12px 0 18px;
		border-bottom: 1px solid var(--border);
	}
	.fname {
		display: inline-flex;
		align-items: center;
		gap: 7px;
		font-size: 12px;
		color: var(--fg);
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.fname :global(svg) {
		color: var(--fg-3);
	}
	.fmeta {
		margin-right: auto;
		font-size: var(--text-2xs);
		color: var(--fg-3);
		white-space: nowrap;
	}
	.doc {
		padding: 20px 24px 28px;
		display: grid;
		gap: 20px;
		min-width: 0;
	}
	.fm {
		display: grid;
		border: 1px solid var(--border);
		border-radius: var(--radius);
		overflow: hidden;
		font-size: var(--text-sm);
	}
	.fm div {
		display: grid;
		grid-template-columns: 110px minmax(0, 1fr);
	}
	.fm div + div {
		border-top: 1px solid var(--border);
	}
	.fm dt {
		padding: 7px 12px;
		background: var(--surface-2);
		border-right: 1px solid var(--border);
		font-size: 11.5px;
		color: var(--fg-3);
	}
	.fm dd {
		padding: 7px 12px;
		color: var(--fg);
		overflow-wrap: anywhere;
	}
	.code {
		margin: 0;
		padding: 14px 0;
		font-size: 12.5px;
		line-height: 1.7;
		overflow-x: auto;
		counter-reset: ln;
		tab-size: 4;
	}
	.ln {
		display: block;
		padding: 0 18px 0 0;
		white-space: pre;
	}
	.ln::before {
		counter-increment: ln;
		content: counter(ln);
		display: inline-block;
		width: 3.2em;
		margin-right: 16px;
		padding-right: 4px;
		text-align: right;
		color: var(--fg-4);
		user-select: none;
	}
	.ln:hover {
		background: var(--hover);
	}
	.loading {
		display: grid;
		gap: 12px;
		padding: 22px 24px;
	}
	.state {
		display: flex;
		align-items: flex-start;
		gap: 10px;
		margin: 20px 24px;
		padding: 14px 16px;
		border-radius: var(--radius);
		background: var(--surface-2);
		border: 1px solid var(--border);
		font-size: var(--text-sm);
		color: var(--fg-2);
	}
	.state :global(svg) {
		flex: none;
		margin-top: 2px;
		color: var(--fg-3);
	}
	.state.error {
		color: var(--red);
		background: var(--red-soft);
		border-color: var(--red-border);
	}
	.state.error :global(svg) {
		color: var(--red);
	}
	.state .sub {
		color: var(--fg-2);
		overflow-wrap: anywhere;
	}
	.trunc {
		display: flex;
		align-items: center;
		gap: 6px;
		margin: 0 18px 16px;
		font-size: var(--text-xs);
		color: var(--fg-3);
	}
	@media (max-width: 720px) {
		.body {
			grid-template-columns: minmax(0, 1fr);
		}
		.side {
			border-right: 0;
			border-bottom: 1px solid var(--border);
		}
		.doc {
			padding: 16px;
		}
		.fm div {
			grid-template-columns: 90px minmax(0, 1fr);
		}
	}
</style>
