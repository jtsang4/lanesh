<script lang="ts">
	import { resolve } from '$app/paths';
	import EmptyState from '#lib/components/EmptyState.svelte';
	import ErrorState from '#lib/components/ErrorState.svelte';
	import Icon from '#lib/components/Icon.svelte';
	import PageHeader from '#lib/components/PageHeader.svelte';
	import RelTime from '#lib/components/RelTime.svelte';
	import { t } from '#lib/i18n/index.svelte.ts';
	import { app } from '#lib/state/app.svelte.ts';
	import { ui } from '#lib/state/ui.svelte.ts';

	const totals = $derived.by(() => {
		const lanes = app.lanes ?? [];
		return {
			lanes: lanes.length,
			machines: lanes.reduce((n, l) => n + l.machine_count, 0),
			online: lanes.reduce((n, l) => n + l.online_count, 0)
		};
	});
</script>

<svelte:head>
	<title>{t('overview.title')} · Lanesh</title>
</svelte:head>

<PageHeader title={t('overview.title')} description={t('overview.subtitle')}>
	{#snippet actions()}
		<button type="button" class="btn btn-primary" onclick={() => (ui.createLaneOpen = true)}>
			<Icon name="plus" size={14} />
			{t('nav.newLane')}
		</button>
	{/snippet}
</PageHeader>

{#if app.lanesError && !app.lanes}
	<ErrorState error={app.lanesError} onretry={() => app.refreshLanes().catch(() => {})} />
{:else if app.lanes === null}
	<div class="stats" aria-hidden="true">
		{#each [0, 1, 2] as i (i)}
			<div class="stat">
				<span class="skeleton" style:width="40%" style:height="10px"></span><span
					class="skeleton"
					style:width="30%"
					style:height="22px"
				></span>
			</div>
		{/each}
	</div>
	<div class="list card" aria-busy="true">
		{#each [0, 1, 2] as i (i)}
			<div class="row skel">
				<span class="skeleton" style:width="140px" style:height="12px"></span>
				<span class="skeleton" style:width="90px" style:height="10px"></span>
			</div>
		{/each}
	</div>
{:else if app.lanes.length === 0}
	<EmptyState icon="layers" title={t('overview.emptyTitle')} body={t('overview.emptyBody')}>
		<button type="button" class="btn btn-primary" onclick={() => (ui.createLaneOpen = true)}>
			<Icon name="plus" size={14} />
			{t('nav.newLane')}
		</button>
	</EmptyState>
{:else}
	<dl class="stats fade-in">
		<div class="stat">
			<dt>{t('overview.statLanes')}</dt>
			<dd class="tnum">{totals.lanes}</dd>
		</div>
		<div class="stat">
			<dt>{t('overview.statMachines')}</dt>
			<dd class="tnum">{totals.machines}</dd>
		</div>
		<div class="stat">
			<dt>{t('overview.statOnline')}</dt>
			<dd class="tnum">
				{totals.online}
				{#if totals.online > 0}<span class="live" aria-hidden="true"></span>{/if}
			</dd>
		</div>
	</dl>

	<div class="list card fade-in">
		<div class="row head" aria-hidden="true">
			<span>{t('overview.colLane')}</span>
			<span>{t('overview.colMachines')}</span>
			<span class="c-created">{t('overview.colCreated')}</span>
			<span></span>
		</div>
		<ul>
			{#each app.lanes as l (l.id)}
				<li class="row lane">
					<div class="name">
						<a href={resolve('/(app)/lanes/[lane]', { lane: l.id })} class="stretch">{l.name}</a>
						<span class="id mono">{l.id}</span>
					</div>
					<div class="machines">
						{#if l.machine_count === 0}
							<span class="faint">{t('overview.noMachines')}</span>
						{:else}
							<span class="bar" aria-hidden="true">
								{#each Array.from({ length: Math.min(l.machine_count, 12) }, (_, i) => i) as i (i)}
									<span class={['seg', i < l.online_count && 'on']}></span>
								{/each}
							</span>
							<span class="tnum">
								{t('overview.onlineOf', { online: l.online_count, total: l.machine_count })}
							</span>
							<span class="short tnum" aria-hidden="true">{l.online_count}/{l.machine_count}</span>
						{/if}
					</div>
					<div class="c-created faint"><RelTime time={l.created_at} /></div>
					<div class="chev" aria-hidden="true"><Icon name="chevronRight" size={14} /></div>
				</li>
			{/each}
		</ul>
	</div>
{/if}

<style>
	.stats {
		display: grid;
		grid-template-columns: repeat(3, minmax(0, 1fr));
		margin-bottom: 20px;
		border: 1px solid var(--border);
		border-radius: var(--radius-lg);
		background: var(--surface);
	}
	.stat {
		display: grid;
		gap: 6px;
		padding: 14px 18px 16px;
	}
	.stat + .stat {
		border-left: 1px solid var(--border);
	}
	dt {
		font-size: var(--text-xs);
		color: var(--fg-3);
	}
	dd {
		display: flex;
		align-items: center;
		gap: 10px;
		font-size: 22px;
		font-weight: 500;
		letter-spacing: -0.02em;
		line-height: 1.1;
	}
	.live {
		width: 7px;
		height: 7px;
		border-radius: 50%;
		background: var(--green);
		box-shadow: 0 0 0 3px var(--green-soft);
	}
	.list {
		overflow: hidden;
	}
	.row {
		display: grid;
		grid-template-columns: minmax(0, 1.4fr) minmax(0, 1fr) 140px 20px;
		align-items: center;
		gap: 16px;
		padding: 0 16px;
	}
	.head {
		height: 34px;
		font-size: var(--text-xs);
		color: var(--fg-3);
		border-bottom: 1px solid var(--border);
		background: var(--surface-2);
	}
	.skel {
		height: 60px;
		grid-template-columns: 1fr 1fr;
	}
	.skel + .skel {
		border-top: 1px solid var(--border);
	}
	.lane {
		position: relative;
		min-height: 60px;
		transition: background-color var(--dur-1) var(--ease);
	}
	li + li {
		border-top: 1px solid var(--border);
	}
	.lane:hover {
		background: var(--surface-2);
	}
	.lane:hover .chev {
		color: var(--fg);
		transform: translateX(2px);
	}
	.name {
		display: grid;
		gap: 1px;
		min-width: 0;
		padding: 10px 0;
	}
	.name a {
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
		border-radius: var(--radius-lg);
	}
	.id {
		font-size: var(--text-2xs);
		color: var(--fg-3);
		overflow: hidden;
		text-overflow: ellipsis;
	}
	.machines {
		display: flex;
		align-items: center;
		gap: 10px;
		font-size: var(--text-sm);
		color: var(--fg-2);
		min-width: 0;
	}
	.bar {
		display: inline-flex;
		gap: 2px;
	}
	.seg {
		width: 4px;
		height: 14px;
		border-radius: 2px;
		background: var(--border-strong);
	}
	.seg.on {
		background: var(--green);
	}
	.c-created {
		font-size: var(--text-sm);
	}
	.short {
		display: none;
	}
	.chev {
		color: var(--fg-4);
		transition:
			color var(--dur-1) var(--ease),
			transform var(--dur-2) var(--ease-out);
	}
	@media (max-width: 640px) {
		.stat {
			padding: 12px 14px;
		}
		dd {
			font-size: 18px;
		}
		.row {
			grid-template-columns: minmax(0, 1fr) auto 16px;
		}
		.c-created {
			display: none;
		}
		.machines .tnum {
			display: none;
		}
		.machines .short {
			display: inline;
		}
	}
</style>
