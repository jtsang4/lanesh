<script lang="ts">
	import { resolve } from '$app/paths';
	import Badge from '#lib/components/Badge.svelte';
	import EmptyState from '#lib/components/EmptyState.svelte';
	import ErrorState from '#lib/components/ErrorState.svelte';
	import Icon from '#lib/components/Icon.svelte';
	import StatusDot from '#lib/components/StatusDot.svelte';
	import { t, tn } from '#lib/i18n/index.svelte.ts';
	import { lane } from '#lib/state/lane.svelte.ts';
	import type { PageProps } from './$types';

	let { params }: PageProps = $props();

	let query = $state('');

	const filtered = $derived.by(() => {
		const q = query.trim().toLowerCase();
		const all = lane.skills ?? [];
		if (!q) return all;
		return all.filter(
			(s) =>
				s.name.toLowerCase().includes(q) ||
				s.description.toLowerCase().includes(q) ||
				s.machines.some((m) => m.name.toLowerCase().includes(q))
		);
	});
	const differing = $derived(lane.skills?.filter((s) => !s.consistent).length ?? 0);

	function machineHref(machineId: string, skill: string) {
		const base = resolve('/(app)/lanes/[lane]/machines/[machine]', {
			lane: params.lane,
			machine: machineId
		});
		return `${base}?skill=${encodeURIComponent(skill)}`;
	}
</script>

<svelte:head>
	<title>{lane.lane?.name ?? 'Lane'} · {t('lane.tabs.skills')} · Lanesh</title>
</svelte:head>

{#if lane.skillsError && !lane.skills}
	<ErrorState error={lane.skillsError} onretry={() => lane.loadSkills()} />
{:else if lane.skills === null}
	<div class="list card" aria-busy="true">
		{#each [0, 1, 2, 3] as i (i)}
			<div class="row skel">
				<div class="grow">
					<span class="skeleton" style:width="120px" style:height="12px"></span>
					<span class="skeleton" style:width="260px" style:height="10px"></span>
				</div>
				<span class="skeleton" style:width="90px" style:height="20px"></span>
			</div>
		{/each}
	</div>
{:else if lane.skills.length === 0}
	<EmptyState icon="sparkle" title={t('skills.emptyTitle')} body={t('skills.emptyBody')} />
{:else}
	<div class="toolbar">
		<label class="search">
			<Icon name="search" size={14} />
			<span class="sr-only">{t('common.search')}</span>
			<input
				class="input"
				type="search"
				bind:value={query}
				placeholder={t('skills.searchPlaceholder')}
				autocomplete="off"
				spellcheck="false"
			/>
		</label>
		<p class="summary">
			{tn('skills.summary', lane.skills.length)}
			{#if differing > 0}
				<span class="sep">·</span>
				<span class="warn">{tn('skills.differ', differing)}</span>
			{/if}
		</p>
	</div>

	{#if filtered.length === 0}
		<EmptyState compact icon="search" title={t('skills.noMatch', { q: query.trim() })} />
	{:else}
		<ul class="list card">
			{#each filtered as s (s.name)}
				<li class="row">
					<div class="main">
						<div class="title">
							<span class="name mono">{s.name}</span>
							{#if !s.consistent}
								<Badge tone="amber" title={t('skills.differsHint')}>
									<Icon name="arrowsDiff" size={11} strokeWidth={1.8} />
									{t('skills.differs')}
								</Badge>
							{/if}
						</div>
						<p class={['desc', !s.description && 'none']}>
							{s.description || t('skills.noDescription')}
						</p>
					</div>
					<ul class="machines" aria-label={t('skill.onMachines')}>
						{#each s.machines as m (m.machine_id)}
							<li>
								<a
									class="chip"
									href={machineHref(m.machine_id, s.name)}
									title="{m.name} · {m.source}{s.consistent ? '' : ` · ${m.hash.slice(0, 7)}`}"
								>
									<StatusDot online={m.online} label={false} />
									<span>{m.name}</span>
									{#if !s.consistent}<span class="hash mono">{m.hash.slice(0, 6)}</span>{/if}
								</a>
							</li>
						{/each}
					</ul>
				</li>
			{/each}
		</ul>
	{/if}
{/if}

<style>
	.toolbar {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 16px;
		margin-bottom: 14px;
		flex-wrap: wrap;
	}
	.search {
		position: relative;
		display: block;
		width: min(280px, 100%);
		color: var(--fg-3);
	}
	.search :global(svg) {
		position: absolute;
		left: 10px;
		top: 50%;
		transform: translateY(-50%);
		pointer-events: none;
	}
	.search .input {
		padding-left: 31px;
	}
	.summary {
		font-size: var(--text-xs);
		color: var(--fg-3);
	}
	.sep {
		margin: 0 4px;
	}
	.warn {
		color: var(--amber);
	}
	.list {
		overflow: hidden;
	}
	.row {
		display: grid;
		grid-template-columns: minmax(0, 1fr) minmax(0, auto);
		align-items: center;
		gap: 24px;
		padding: 13px 16px;
	}
	.skel .grow {
		display: grid;
		gap: 8px;
	}
	.row + .row {
		border-top: 1px solid var(--border);
	}
	.main {
		min-width: 0;
		display: grid;
		gap: 3px;
	}
	.title {
		display: flex;
		align-items: center;
		gap: 8px;
		min-width: 0;
	}
	.name {
		font-size: 13px;
		font-weight: 500;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.desc {
		font-size: var(--text-sm);
		color: var(--fg-2);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.desc.none {
		color: var(--fg-4);
	}
	.machines {
		display: flex;
		flex-wrap: wrap;
		justify-content: flex-end;
		gap: 6px;
	}
	.chip {
		display: inline-flex;
		align-items: center;
		gap: 7px;
		height: 24px;
		padding: 0 9px 0 8px;
		border-radius: 12px;
		border: 1px solid var(--border);
		background: var(--surface);
		font-size: var(--text-xs);
		color: var(--fg-2);
		white-space: nowrap;
		transition:
			border-color var(--dur-1) var(--ease),
			color var(--dur-1) var(--ease),
			background-color var(--dur-1) var(--ease);
	}
	.chip:hover {
		border-color: var(--border-strong);
		color: var(--fg);
		background: var(--surface-2);
	}
	.hash {
		font-size: 10.5px;
		color: var(--amber);
	}
	@media (max-width: 720px) {
		.row {
			grid-template-columns: minmax(0, 1fr);
			gap: 10px;
		}
		.machines {
			justify-content: flex-start;
		}
		.desc {
			white-space: normal;
		}
	}
</style>
