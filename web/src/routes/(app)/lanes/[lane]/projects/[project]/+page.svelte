<script lang="ts">
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { ApiError, api } from '#lib/api.ts';
	import Badge from '#lib/components/Badge.svelte';
	import Callout from '#lib/components/Callout.svelte';
	import ConfirmDialog from '#lib/components/ConfirmDialog.svelte';
	import Dialog from '#lib/components/Dialog.svelte';
	import EmptyState from '#lib/components/EmptyState.svelte';
	import ErrorState from '#lib/components/ErrorState.svelte';
	import Icon from '#lib/components/Icon.svelte';
	import Markdown from '#lib/components/Markdown.svelte';
	import PageHeader from '#lib/components/PageHeader.svelte';
	import RelTime from '#lib/components/RelTime.svelte';
	import RenameDialog from '#lib/components/RenameDialog.svelte';
	import Rich from '#lib/components/Rich.svelte';
	import StatusDot from '#lib/components/StatusDot.svelte';
	import { bytes, errorMessage } from '#lib/format.ts';
	import { t } from '#lib/i18n/index.svelte.ts';
	import { lane } from '#lib/state/lane.svelte.ts';
	import { poll } from '#lib/state/poll.ts';
	import { toasts } from '#lib/state/toasts.svelte.ts';
	import type { Project, ProjectLocation, ProjectRevision } from '#lib/types.ts';
	import type { PageProps } from './$types';

	let { params }: PageProps = $props();

	/** Keep in step with store.MaxProjectContext. */
	const MAX_CONTEXT = 20 << 10;

	let project = $state.raw<Project | null>(null);
	let error = $state.raw<unknown>(null);
	let revisions = $state.raw<ProjectRevision[]>([]);

	let editing = $state(false);
	let draft = $state('');
	/** The revision the draft started from: saving fails if it changed since. */
	let editBase = 0;
	let saving = $state(false);
	let saveError = $state('');
	/** Set when someone else saved while we were editing. */
	let stale = $state.raw<Project | null>(null);

	let addMachine = $state('');
	let addPath = $state('');
	let addError = $state('');
	let locBusy = $state(false);

	let renaming = $state(false);
	let deleting = $state(false);
	let viewing = $state.raw<ProjectRevision | null>(null);
	let viewOpen = $state(false);

	const notFound = $derived(error instanceof ApiError && error.status === 404);
	const draftSize = $derived(new TextEncoder().encode(draft).length);

	async function load() {
		const ref = params.project;
		try {
			const p = await api.project(params.lane, ref);
			if (ref !== params.project) return;
			project = p;
			error = null;
		} catch (err) {
			if (ref === params.project) error = err;
		}
	}

	async function loadRevisions() {
		const ref = params.project;
		try {
			const list = await api.projectRevisions(params.lane, ref);
			if (ref === params.project) revisions = list;
		} catch {
			// History is secondary; the page itself reports load errors.
		}
	}

	$effect(() => {
		void params.project;
		project = null;
		error = null;
		revisions = [];
		editing = false;
		void loadRevisions();
		// Locations are checked on their machines on every load; refresh now and
		// then, but not while editing.
		return poll(() => (editing ? undefined : load()), 30_000);
	});
	$effect(() => {
		void params.project;
		void load();
	});

	/** Applies a saved project and refreshes what depends on it. */
	function saved(p: Project) {
		project = p;
		void loadRevisions();
		void lane.loadProjects();
	}

	function startEdit() {
		if (!project) return;
		draft = project.context;
		editBase = project.revision;
		saveError = '';
		stale = null;
		editing = true;
	}

	async function save(base = editBase) {
		if (!project) return;
		if (draftSize > MAX_CONTEXT) {
			saveError = t('project.contextTooLong', { limit: bytes(MAX_CONTEXT) });
			return;
		}
		saving = true;
		saveError = '';
		try {
			const p = await api.updateProject(params.lane, project.id, {
				context: draft,
				base_revision: base
			});
			stale = null;
			editing = false;
			if (p.revision !== base) toasts.show(t('project.saved', { n: p.revision }));
			saved(p);
		} catch (err) {
			if (err instanceof ApiError && err.status === 409) {
				stale = await api.project(params.lane, project.id).catch(() => null);
				void loadRevisions();
			} else {
				saveError = errorMessage(err);
			}
		} finally {
			saving = false;
		}
	}

	function useLatest() {
		if (stale) project = stale;
		stale = null;
		editing = false;
		void loadRevisions();
	}

	function onEditorKey(e: KeyboardEvent) {
		if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
			e.preventDefault();
			void save();
		}
	}

	const plain = (locs: ProjectLocation[]) =>
		locs.map((l) => ({ machine_id: l.machine_id, path: l.path }));

	async function setLocations(locations: ProjectLocation[]) {
		if (!project) return;
		locBusy = true;
		addError = '';
		try {
			saved(
				await api.updateProject(params.lane, project.id, {
					locations: plain(locations),
					base_revision: project.revision
				})
			);
			return true;
		} catch (err) {
			if (err instanceof ApiError && err.status === 409) void load();
			addError = errorMessage(err);
			return false;
		} finally {
			locBusy = false;
		}
	}

	async function addLocation(e: SubmitEvent) {
		e.preventDefault();
		if (!project) return;
		const path = addPath.trim();
		if (!addMachine || !path) {
			addError = t('project.pathRequired');
			return;
		}
		if (await setLocations([...project.locations, { machine_id: addMachine, path }])) addPath = '';
	}

	function removeLocation(i: number) {
		if (project) void setLocations(project.locations.filter((_, j) => j !== i));
	}

	async function restore(rev: ProjectRevision) {
		if (!project) return;
		try {
			const p = await api.updateProject(params.lane, project.id, {
				name: rev.name,
				context: rev.context,
				locations: plain(rev.locations),
				base_revision: project.revision
			});
			viewOpen = false;
			toasts.show(t('project.restored', { n: rev.revision, m: p.revision }));
			saved(p);
		} catch (err) {
			toasts.show(errorMessage(err), 'error');
		}
	}

	/** What a revision changed compared with the one before it. */
	function changes(i: number): string[] {
		const rev = revisions[i];
		const prev = revisions[i + 1];
		if (!prev) return rev.revision === 1 ? [t('project.changes.created')] : [];
		const out: string[] = [];
		if (rev.name !== prev.name) out.push(t('project.changes.renamed'));
		if (rev.context !== prev.context) out.push(t('project.changes.context'));
		if (JSON.stringify(rev.locations) !== JSON.stringify(prev.locations)) {
			out.push(t('project.changes.locations'));
		}
		return out.length ? out : [t('project.changes.restored')];
	}

	function machineName(id: string) {
		return lane.machineNames.get(id) ?? id;
	}

	// Default the machine picker to the first machine of the lane.
	$effect(() => {
		if (!addMachine && lane.machines?.length) addMachine = lane.machines[0].id;
	});
</script>

<svelte:head>
	<title>{project?.name ?? t('project.back')} · {lane.lane?.name ?? 'Lane'} · Lanesh</title>
</svelte:head>

{#if notFound}
	<EmptyState icon="folder" title={t('project.notFoundTitle')} body={t('project.notFoundBody')}>
		<a class="btn" href={resolve('/(app)/lanes/[lane]/projects', { lane: params.lane })}>
			<Icon name="arrowLeft" size={14} />
			{t('project.back')}
		</a>
	</EmptyState>
{:else if error && !project}
	<ErrorState {error} onretry={load} />
{:else}
	<PageHeader title={project?.name} loading={!project}>
		{#snippet eyebrow()}
			<a href={resolve('/(app)')}>{t('nav.lanes')}</a>
			<Icon name="chevronRight" size={12} />
			<a href={resolve('/(app)/lanes/[lane]', { lane: params.lane })}>{lane.lane?.name ?? '…'}</a>
			<Icon name="chevronRight" size={12} />
			<a href={resolve('/(app)/lanes/[lane]/projects', { lane: params.lane })}
				>{t('project.back')}</a
			>
		{/snippet}
		{#snippet meta()}
			{#if project}
				<span class="tnum">{t('project.revision', { n: project.revision })}</span>
				<RelTime
					time={project.updated_at}
					prefix={(r: string) =>
						t('projects.updated', { time: r, who: project?.updated_by_name ?? '' })}
				/>
			{/if}
		{/snippet}
		{#snippet actions()}
			<button type="button" class="btn" disabled={!project} onclick={() => (renaming = true)}>
				<Icon name="pencil" size={13} />
				{t('common.rename')}
			</button>
			<button
				type="button"
				class="btn btn-danger-ghost"
				disabled={!project}
				onclick={() => (deleting = true)}
			>
				<Icon name="trash" size={13} />
				{t('common.delete')}
			</button>
		{/snippet}
	</PageHeader>

	<section class="block" aria-labelledby="loc-title">
		<h2 id="loc-title" class="section-title">
			{t('project.locations')}
			{#if project}<span class="count tnum">{project.locations.length}</span>{/if}
		</h2>
		<p class="section-desc">{t('project.locationsDesc')}</p>

		<div class="card locs">
			{#if !project}
				<div class="loc skel">
					<span class="skeleton" style:width="45%" style:height="12px"></span>
				</div>
			{:else}
				<ul>
					{#each project.locations as l, i (l.machine_id + l.path)}
						<li class={['loc', l.removed && 'removed']}>
							<div class="where">
								<StatusDot online={l.online} label={false} />
								<span class="machine">{l.machine_name}</span>
								<span class="path mono" title={l.info?.resolved ?? l.path}>{l.path}</span>
							</div>
							<div class="facts">
								{#if l.removed}
									<Badge tone="neutral">{t('project.loc.removed')}</Badge>
								{:else if !l.info}
									<span class="note">{l.note}</span>
								{:else if l.info.error}
									<Badge tone="red">{l.info.error}</Badge>
								{:else if !l.info.exists}
									<Badge tone="amber">{t('project.loc.missing')}</Badge>
								{:else if !l.info.dir}
									<Badge tone="amber">{t('project.loc.notDir')}</Badge>
								{:else}
									{#if l.info.git}
										<Badge tone="outline">
											<Icon name="branch" size={11} />
											{l.info.branch || 'git'}
											{#if l.info.worktree}<span class="faint">· {t('project.loc.worktree')}</span
												>{/if}
										</Badge>
									{/if}
									{#each l.info.agent_files ?? [] as f (f)}
										<Badge tone="outline" title={t('project.loc.agentFilesHint')}>
											<Icon name="file" size={11} />
											<span class="mono">{f.replace(' -> ', ' → ')}</span>
										</Badge>
									{/each}
									{#if !l.info.git && !l.info.agent_files?.length}
										<span class="note ok">
											<Icon name="check" size={12} strokeWidth={2} />{t('project.loc.exists')}
										</span>
									{/if}
								{/if}
							</div>
							<button
								type="button"
								class="btn btn-sm btn-icon btn-ghost remove"
								aria-label={t('project.removeLocation', { path: l.path })}
								title={t('project.removeLocation', { path: l.path })}
								disabled={locBusy}
								onclick={() => removeLocation(i)}
							>
								<Icon name="x" size={13} />
							</button>
						</li>
					{/each}
				</ul>
				<form class="add" onsubmit={addLocation} novalidate>
					<label class="sr-only" for="add-machine">{t('project.machine')}</label>
					<select id="add-machine" class="select" bind:value={addMachine}>
						{#each lane.machines ?? [] as m (m.id)}
							<option value={m.id}>{m.name}</option>
						{/each}
					</select>
					<label class="sr-only" for="add-path">{t('project.path')}</label>
					<input
						id="add-path"
						class="input mono"
						bind:value={addPath}
						placeholder={t('project.pathPlaceholder')}
						autocomplete="off"
						spellcheck="false"
					/>
					<button type="submit" class="btn" disabled={locBusy || !lane.machines?.length}>
						<Icon name="plus" size={13} />
						{t('project.add')}
					</button>
				</form>
				{#if addError}<p class="error-text loc-error" role="alert">{addError}</p>{/if}
			{/if}
		</div>
	</section>

	<div class="split">
		<section class="block" aria-labelledby="ctx-title">
			<div class="sh">
				<div>
					<h2 id="ctx-title" class="section-title">{t('project.context')}</h2>
					<p class="section-desc"><Rich text={t('project.contextDesc')} /></p>
				</div>
				{#if project && !editing}
					<button type="button" class="btn" onclick={startEdit}>
						<Icon name="pencil" size={13} />
						{t('project.edit')}
					</button>
				{/if}
			</div>

			{#if editing}
				{#if stale}
					<Callout tone="warn">
						<p>
							{t('project.stale', { who: stale.updated_by_name, n: stale.revision })}
						</p>
						<div class="stale-actions">
							<button type="button" class="btn btn-sm" onclick={useLatest}>
								{t('project.useLatest')}
							</button>
							<button
								type="button"
								class="btn btn-sm"
								disabled={saving}
								onclick={() => {
									editBase = stale?.revision ?? editBase;
									void save();
								}}
							>
								{t('project.saveAnyway')}
							</button>
						</div>
					</Callout>
				{/if}
				<div class="editor card">
					<!-- svelte-ignore a11y_autofocus -->
					<textarea
						class="mono"
						bind:value={draft}
						onkeydown={onEditorKey}
						spellcheck="false"
						aria-label={t('project.context')}
						autofocus></textarea>
					<div class="editor-bar">
						<span class={['size tnum', draftSize > MAX_CONTEXT && 'over']}>
							Markdown · {t('project.contextSize', {
								size: bytes(draftSize),
								limit: bytes(MAX_CONTEXT)
							})}
						</span>
						{#if saveError}<span class="error-text">{saveError}</span>{/if}
						<div class="editor-actions">
							<button type="button" class="btn" onclick={() => (editing = false)}>
								{t('common.cancel')}
							</button>
							<button
								type="button"
								class="btn btn-primary"
								disabled={saving}
								title="⌘/Ctrl + Enter"
								onclick={() => save()}
							>
								{saving ? t('common.saving') : t('common.save')}
							</button>
						</div>
					</div>
				</div>
			{:else if !project}
				<div class="card doc">
					{#each [70, 90, 60, 80] as w (w)}<span
							class="skeleton"
							style:width="{w}%"
							style:height="10px"
						></span>{/each}
				</div>
			{:else if project.context.trim()}
				<div class="card doc"><Markdown source={project.context} /></div>
			{:else}
				<div class="card doc empty">
					<p class="faint">{t('project.contextEmpty')}</p>
					<button type="button" class="btn btn-sm" onclick={startEdit}>{t('project.edit')}</button>
				</div>
			{/if}
		</section>

		<section class="block history" aria-labelledby="hist-title">
			<h2 id="hist-title" class="section-title">
				<Icon name="history" size={15} />
				{t('project.history')}
			</h2>
			<p class="section-desc">{t('project.historyDesc')}</p>
			<ol class="card revs">
				{#each revisions as r, i (r.revision)}
					<li>
						<button
							type="button"
							class="rev"
							onclick={() => {
								viewing = r;
								viewOpen = true;
							}}
						>
							<span class="rn tnum">#{r.revision}</span>
							<span class="rmeta">
								<span class="rwho">{r.updated_by_name}</span>
								<RelTime time={r.updated_at} />
							</span>
							<span class="rchanges">
								{#if project && r.revision === project.revision}
									<Badge tone="green">{t('project.current')}</Badge>
								{/if}
								{changes(i).join(' · ')}
							</span>
						</button>
					</li>
				{/each}
			</ol>
		</section>
	</div>
{/if}

{#if project}
	{@const p = project}
	<RenameDialog
		bind:open={renaming}
		title={t('project.rename.title')}
		label={t('common.name')}
		initial={p.name}
		required={t('projects.create.nameRequired')}
		onsave={async (name: string) => {
			saved(await api.updateProject(params.lane, p.id, { name }));
		}}
	/>
	<ConfirmDialog
		bind:open={deleting}
		title={t('project.delete.title', { name: p.name })}
		body={t('project.delete.body')}
		confirmLabel={t('project.delete.confirm')}
		confirmText={p.name}
		onconfirm={async () => {
			await api.deleteProject(params.lane, p.id);
			toasts.show(t('project.deleted', { name: p.name }));
			void lane.loadProjects();
			await goto(resolve('/(app)/lanes/[lane]/projects', { lane: params.lane }));
		}}
	/>
{/if}

{#if viewing}
	{@const r = viewing}
	<Dialog
		bind:open={viewOpen}
		title={t('project.revisionTitle', { name: r.name, n: r.revision })}
		width={720}
	>
		<div class="rev-view">
			<ul class="rev-locs">
				{#each r.locations as l (l.machine_id + l.path)}
					<li>
						<Icon name="folder" size={13} />
						<span>{machineName(l.machine_id)}</span>
						<span class="mono faint">{l.path}</span>
					</li>
				{:else}
					<li class="faint">{t('projects.noLocations')}</li>
				{/each}
			</ul>
			{#if r.context.trim()}
				<div class="rev-doc"><Markdown source={r.context} /></div>
			{:else}
				<p class="faint">{t('project.contextEmpty')}</p>
			{/if}
		</div>
		{#snippet footer()}
			<button type="button" class="btn" onclick={() => (viewOpen = false)}
				>{t('common.close')}</button
			>
			<button
				type="button"
				class="btn btn-primary"
				disabled={!project || r.revision === project.revision}
				onclick={() => restore(r)}
			>
				{t('project.restore')}
			</button>
		{/snippet}
	</Dialog>
{/if}

<style>
	.block {
		margin-bottom: 36px;
		min-width: 0;
	}
	.section-desc {
		margin-bottom: 14px;
		max-width: 72ch;
	}
	.count {
		margin-left: 6px;
		font-size: var(--text-xs);
		font-weight: 400;
		color: var(--fg-3);
	}
	.sh {
		display: flex;
		align-items: flex-end;
		justify-content: space-between;
		gap: 16px;
	}
	.locs {
		overflow: hidden;
	}
	.loc {
		display: grid;
		grid-template-columns: minmax(0, 1.1fr) minmax(0, 1fr) 28px;
		align-items: center;
		gap: 16px;
		min-height: 52px;
		padding: 8px 10px 8px 16px;
		border-bottom: 1px solid var(--border);
	}
	.loc.skel {
		grid-template-columns: 1fr;
	}
	.where {
		display: flex;
		align-items: center;
		gap: 9px;
		min-width: 0;
	}
	.machine {
		flex: none;
		font-weight: 500;
	}
	.path {
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		font-size: var(--text-sm);
		color: var(--fg-2);
	}
	.removed .machine,
	.removed .path {
		color: var(--fg-3);
		text-decoration: line-through;
	}
	.facts {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 6px;
		min-width: 0;
	}
	.note {
		font-size: var(--text-xs);
		color: var(--fg-3);
	}
	.note.ok {
		display: inline-flex;
		align-items: center;
		gap: 4px;
		color: var(--green);
	}
	.remove {
		color: var(--fg-3);
	}
	.add {
		display: grid;
		grid-template-columns: minmax(120px, 0.35fr) minmax(0, 1fr) auto;
		gap: 8px;
		padding: 10px 10px 10px 16px;
		background: var(--surface-2);
	}
	.loc-error {
		padding: 0 16px 10px;
		background: var(--surface-2);
		margin: 0;
	}
	.split {
		display: grid;
		grid-template-columns: minmax(0, 1fr) 300px;
		gap: 28px;
		align-items: start;
	}
	.doc {
		display: grid;
		gap: 10px;
		padding: 22px 26px;
	}
	.doc.empty {
		justify-items: start;
	}
	.editor {
		display: grid;
		overflow: hidden;
	}
	.editor textarea {
		min-height: 420px;
		field-sizing: content;
		max-height: 75vh;
		padding: 18px 20px;
		border: 0;
		resize: vertical;
		background: transparent;
		color: var(--fg);
		font-size: var(--text-sm);
		line-height: 1.65;
	}
	.editor textarea:focus {
		outline: none;
	}
	.editor:focus-within {
		box-shadow:
			0 0 0 1px var(--border-strong),
			0 0 0 3px var(--focus-ring, transparent);
	}
	.editor-bar {
		display: flex;
		align-items: center;
		gap: 12px;
		padding: 10px 12px 10px 20px;
		border-top: 1px solid var(--border);
		background: var(--surface-2);
	}
	.size {
		font-size: var(--text-xs);
		color: var(--fg-3);
	}
	.size.over {
		color: var(--red);
	}
	.editor-actions {
		margin-left: auto;
		display: flex;
		gap: 8px;
	}
	.stale-actions {
		display: flex;
		gap: 8px;
		margin-top: 8px;
	}
	.history .section-title {
		display: flex;
		align-items: center;
		gap: 7px;
	}
	.history .section-title :global(svg) {
		color: var(--fg-3);
	}
	.revs {
		overflow: hidden;
	}
	.revs li + li {
		border-top: 1px solid var(--border);
	}
	.rev {
		display: grid;
		grid-template-columns: 34px minmax(0, 1fr);
		grid-template-areas: 'n meta' 'n changes';
		gap: 2px 8px;
		width: 100%;
		padding: 9px 14px;
		text-align: left;
		transition: background-color var(--dur-1) var(--ease);
	}
	.rev:hover {
		background: var(--surface-2);
	}
	.rn {
		grid-area: n;
		font-size: var(--text-xs);
		color: var(--fg-3);
		padding-top: 1px;
	}
	.rmeta {
		grid-area: meta;
		display: flex;
		justify-content: space-between;
		gap: 8px;
		font-size: var(--text-xs);
		color: var(--fg-3);
		min-width: 0;
	}
	.rwho {
		color: var(--fg);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.rchanges {
		grid-area: changes;
		display: flex;
		align-items: center;
		gap: 6px;
		font-size: var(--text-xs);
		color: var(--fg-2);
	}
	.rev-view {
		display: grid;
		gap: 16px;
	}
	.rev-locs {
		display: grid;
		gap: 4px;
		font-size: var(--text-sm);
	}
	.rev-locs li {
		display: flex;
		align-items: center;
		gap: 8px;
	}
	.rev-locs :global(svg) {
		color: var(--fg-3);
	}
	.rev-doc {
		max-height: 55vh;
		overflow: auto;
		padding: 16px 18px;
		border: 1px solid var(--border);
		border-radius: var(--radius, 8px);
	}
	@media (max-width: 960px) {
		.split {
			grid-template-columns: minmax(0, 1fr);
		}
	}
	@media (max-width: 720px) {
		.loc {
			grid-template-columns: minmax(0, 1fr) 28px;
			grid-template-areas: 'where remove' 'facts facts';
			gap: 6px 10px;
			padding: 10px 8px 10px 14px;
		}
		.where {
			grid-area: where;
		}
		.facts {
			grid-area: facts;
			padding-left: 17px;
		}
		.remove {
			grid-area: remove;
		}
		.add {
			grid-template-columns: minmax(0, 1fr) auto;
		}
		.add .select {
			grid-column: 1 / -1;
		}
		.doc {
			padding: 18px;
		}
	}
</style>
