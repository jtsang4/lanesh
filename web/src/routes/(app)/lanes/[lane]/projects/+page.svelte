<script lang="ts">
	import { resolve } from '$app/paths';
	import CreateProjectDialog from '#lib/components/CreateProjectDialog.svelte';
	import EmptyState from '#lib/components/EmptyState.svelte';
	import ErrorState from '#lib/components/ErrorState.svelte';
	import Icon from '#lib/components/Icon.svelte';
	import RelTime from '#lib/components/RelTime.svelte';
	import Rich from '#lib/components/Rich.svelte';
	import StatusDot from '#lib/components/StatusDot.svelte';
	import { t, tn } from '#lib/i18n/index.svelte.ts';
	import { lane } from '#lib/state/lane.svelte.ts';
	import type { PageProps } from './$types';

	let { params }: PageProps = $props();

	let creating = $state(false);

	// Projects may have been changed by agents since the lane was opened.
	$effect(() => {
		void params.lane;
		void lane.loadProjects();
	});
</script>

<svelte:head>
	<title>{lane.lane?.name ?? 'Lane'} · {t('lane.tabs.projects')} · Lanesh</title>
</svelte:head>

{#if lane.projectsError && !lane.projects}
	<ErrorState error={lane.projectsError} onretry={() => lane.loadProjects()} />
{:else if lane.projects === null}
	<div class="list card" aria-busy="true">
		{#each [0, 1, 2] as i (i)}
			<div class="row skel">
				<span class="skeleton" style:width="140px" style:height="12px"></span>
				<span class="skeleton" style:width="320px" style:height="10px"></span>
			</div>
		{/each}
	</div>
{:else if lane.projects.length === 0}
	<EmptyState icon="folder" title={t('projects.emptyTitle')} body={t('projects.emptyBody')}>
		<button type="button" class="btn btn-primary" onclick={() => (creating = true)}>
			<Icon name="plus" size={14} />
			{t('projects.new')}
		</button>
	</EmptyState>
{:else}
	<div class="toolbar">
		<p class="section-desc"><Rich text={t('projects.desc')} /></p>
		<button type="button" class="btn" onclick={() => (creating = true)}>
			<Icon name="plus" size={14} />
			{t('projects.new')}
		</button>
	</div>
	<ul class="list card">
		{#each lane.projects as p (p.id)}
			{@const href = resolve('/(app)/lanes/[lane]/projects/[project]', {
				lane: params.lane,
				project: p.id
			})}
			<li class="row project">
				<div class="main">
					<a {href} class="name stretch">{p.name}</a>
					<p class={['summary', !p.summary && 'none']}>{p.summary || t('projects.noSummary')}</p>
				</div>
				<ul class="locations">
					{#each p.locations as l (l.machine_id + l.path)}
						<li class={['loc', l.removed && 'removed']} title="{l.machine_name}: {l.path}">
							<StatusDot online={l.online} label={false} />
							<span class="machine">{l.machine_name}</span>
							<span class="path mono">{l.path}</span>
						</li>
					{:else}
						<li class="loc empty">{t('projects.noLocations')}</li>
					{/each}
				</ul>
				<span class="updated">
					<RelTime
						time={p.updated_at}
						prefix={(r: string) => t('projects.updated', { time: r, who: p.updated_by })}
					/>
				</span>
			</li>
		{/each}
	</ul>
	<p class="count faint">{tn('projects.summary', lane.projects.length)}</p>
{/if}

<CreateProjectDialog bind:open={creating} />

<style>
	.toolbar {
		display: flex;
		align-items: flex-end;
		justify-content: space-between;
		gap: 16px;
		margin-bottom: 14px;
	}
	.toolbar .section-desc {
		max-width: 64ch;
	}
	.list {
		overflow: hidden;
	}
	.row {
		display: grid;
		grid-template-columns: minmax(0, 1.2fr) minmax(0, 1fr) auto;
		align-items: center;
		gap: 20px;
		padding: 14px 16px;
	}
	.skel {
		grid-template-columns: 1fr 2fr;
		height: 64px;
	}
	.skel + .skel,
	.project + .project {
		border-top: 1px solid var(--border);
	}
	.project {
		position: relative;
		animation: rise-in var(--dur-3) var(--ease-out) both;
		transition: background-color var(--dur-1) var(--ease);
	}
	.project:hover {
		background: var(--surface-2);
	}
	.main {
		display: grid;
		gap: 3px;
		min-width: 0;
	}
	.name {
		font-weight: 500;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.stretch::after {
		content: '';
		position: absolute;
		inset: 0;
	}
	.stretch:focus-visible {
		outline: none;
	}
	.stretch:focus-visible::after {
		outline: 2px solid var(--focus);
		outline-offset: -2px;
	}
	.summary {
		font-size: var(--text-sm);
		color: var(--fg-2);
		display: -webkit-box;
		-webkit-line-clamp: 2;
		line-clamp: 2;
		-webkit-box-orient: vertical;
		overflow: hidden;
	}
	.summary.none {
		color: var(--fg-3);
	}
	.locations {
		display: grid;
		gap: 4px;
		min-width: 0;
	}
	.loc {
		display: flex;
		align-items: center;
		gap: 7px;
		min-width: 0;
		font-size: var(--text-xs);
		color: var(--fg-2);
	}
	.loc .machine {
		flex: none;
		color: var(--fg);
	}
	.loc .path {
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		color: var(--fg-3);
	}
	.loc.removed .machine {
		text-decoration: line-through;
		color: var(--fg-3);
	}
	.loc.empty {
		color: var(--fg-3);
	}
	.updated {
		font-size: var(--text-xs);
		color: var(--fg-3);
		white-space: nowrap;
	}
	.count {
		margin-top: 10px;
		font-size: var(--text-xs);
		text-align: right;
	}
	@media (max-width: 860px) {
		.toolbar {
			flex-direction: column;
			align-items: stretch;
		}
		.row {
			grid-template-columns: minmax(0, 1fr);
			gap: 8px;
		}
	}
</style>
