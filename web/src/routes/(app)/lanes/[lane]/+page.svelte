<script lang="ts">
	import { resolve } from '$app/paths';
	import Badge from '#lib/components/Badge.svelte';
	import EmptyState from '#lib/components/EmptyState.svelte';
	import ErrorState from '#lib/components/ErrorState.svelte';
	import Icon from '#lib/components/Icon.svelte';
	import MachineDialogs from '#lib/components/MachineDialogs.svelte';
	import Menu from '#lib/components/Menu.svelte';
	import RelTime from '#lib/components/RelTime.svelte';
	import StatusDot from '#lib/components/StatusDot.svelte';
	import { osName } from '#lib/format.ts';
	import { t, tn } from '#lib/i18n/index.svelte.ts';
	import { lane } from '#lib/state/lane.svelte.ts';
	import type { Machine } from '#lib/types.ts';
	import type { PageProps } from './$types';

	let { params }: PageProps = $props();

	let target = $state<Machine | null>(null);
	let renaming = $state(false);
	let removing = $state(false);

	function rename(m: Machine) {
		target = m;
		renaming = true;
	}
	function remove(m: Machine) {
		target = m;
		removing = true;
	}
</script>

<svelte:head>
	<title>{lane.lane?.name ?? 'Lane'} · {t('lane.tabs.machines')} · Lanesh</title>
</svelte:head>

{#if lane.machinesError && !lane.machines}
	<ErrorState error={lane.machinesError} onretry={() => lane.loadMachines()} />
{:else if lane.machines === null}
	<div class="table card" aria-busy="true">
		{#each [0, 1, 2] as i (i)}
			<div class="row skel">
				<span class="skeleton" style:width="160px" style:height="12px"></span>
				<span class="skeleton" style:width="80px" style:height="10px"></span>
				<span class="skeleton" style:width="50px" style:height="10px"></span>
			</div>
		{/each}
	</div>
{:else if lane.machines.length === 0}
	<EmptyState icon="server" title={t('machines.emptyTitle')} body={t('machines.emptyBody')}>
		<a class="btn btn-primary" href={resolve('/(app)/lanes/[lane]/setup', { lane: params.lane })}>
			<Icon name="plus" size={14} />
			{t('machines.emptyAction')}
		</a>
	</EmptyState>
{:else}
	<div class="table card">
		<div class="row head" aria-hidden="true">
			<span>{t('machines.colMachine')}</span>
			<span>{t('machines.colPlatform')}</span>
			<span>{t('machines.colVersion')}</span>
			<span class="num">{t('machines.colSkills')}</span>
			<span>{t('machines.colExec')}</span>
			<span>{t('machines.colSeen')}</span>
			<span></span>
		</div>
		<ul>
			{#each lane.machines as m (m.id)}
				{@const href = resolve('/(app)/lanes/[lane]/machines/[machine]', {
					lane: params.lane,
					machine: m.id
				})}
				<li class={['row machine', !m.online && 'is-offline']}>
					<div class="who">
						<StatusDot online={m.online} />
						<div class="names">
							<a {href} class="name stretch">{m.name}</a>
							<span class="host">{m.hostname || m.environment.hostname || '—'}</span>
						</div>
					</div>
					<div class="cell platform">
						<span>{osName(m.os)}</span>
						<span class="faint">{m.arch}</span>
					</div>
					<div class="cell mono ver">{m.version || '—'}</div>
					<div class="cell num tnum">{m.policy.expose_skills ? m.skill_count : '—'}</div>
					<div class="cell">
						{#if m.policy.allow_exec}
							<Badge tone="outline" title={t('machines.execAllowedHint')}>
								<Icon name="terminal" size={11} strokeWidth={1.8} />
								{t('machines.execAllowed')}
							</Badge>
						{:else}
							<span class="faint" title={t('machines.execDisabledHint')}
								>{t('machines.execDisabled')}</span
							>
						{/if}
					</div>
					<div class="cell seen">
						{#if m.online}
							<span class="online">{t('common.online')}</span>
						{:else}
							<RelTime time={m.last_seen} />
						{/if}
					</div>
					<div class="menu">
						<Menu
							items={[
								{ label: t('common.open'), icon: 'arrowRight', href },
								{ label: t('common.rename'), icon: 'pencil', onselect: () => rename(m) },
								{
									label: t('common.remove'),
									icon: 'trash',
									danger: true,
									onselect: () => remove(m)
								}
							]}
						/>
					</div>
					<div class="mobile-meta">
						<span>{osName(m.os)} {m.arch}</span>
						<span>{tn('machines.skillCount', m.policy.expose_skills ? m.skill_count : 0)}</span>
						{#if m.online}
							<span class="online">{t('common.online')}</span>
						{:else}
							<RelTime time={m.last_seen} />
						{/if}
					</div>
				</li>
			{/each}
		</ul>
	</div>
{/if}

<MachineDialogs machine={target} bind:renaming bind:removing />

<style>
	.table {
		overflow: hidden;
	}
	.row {
		display: grid;
		grid-template-columns:
			minmax(0, 1.6fr) minmax(0, 0.9fr) minmax(0, 0.6fr) 56px minmax(0, 0.7fr)
			minmax(0, 0.8fr) 32px;
		align-items: center;
		gap: 16px;
		padding: 0 12px 0 16px;
	}
	.head {
		height: 34px;
		font-size: var(--text-xs);
		color: var(--fg-3);
		background: var(--surface-2);
		border-bottom: 1px solid var(--border);
	}
	.skel {
		height: 58px;
		grid-template-columns: 1.6fr 1fr 1fr;
	}
	.skel + .skel,
	li + li {
		border-top: 1px solid var(--border);
	}
	.machine {
		position: relative;
		min-height: 58px;
		animation: rise-in var(--dur-3) var(--ease-out) both;
		transition: background-color var(--dur-1) var(--ease);
	}
	.machine:hover {
		background: var(--surface-2);
	}
	.who {
		display: flex;
		align-items: center;
		gap: 12px;
		min-width: 0;
		padding: 10px 0;
	}
	.names {
		display: grid;
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
	.host {
		font-size: var(--text-xs);
		color: var(--fg-3);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.cell {
		font-size: var(--text-sm);
		color: var(--fg-2);
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.platform {
		display: flex;
		gap: 6px;
	}
	.ver {
		font-size: var(--text-xs);
	}
	.num {
		text-align: right;
	}
	.online {
		color: var(--green);
	}
	.is-offline .names .name {
		color: var(--fg-2);
	}
	.menu {
		position: relative;
		z-index: 1;
		display: flex;
		justify-content: flex-end;
	}
	.mobile-meta {
		display: none;
	}
	@media (max-width: 860px) {
		.row.head {
			display: none;
		}
		.machine {
			grid-template-columns: minmax(0, 1fr) 32px;
			grid-template-areas: 'who menu' 'meta meta';
			row-gap: 0;
			padding: 12px 8px 12px 14px;
		}
		.who {
			grid-area: who;
			padding: 0;
		}
		.menu {
			grid-area: menu;
		}
		.cell {
			display: none;
		}
		.mobile-meta {
			grid-area: meta;
			display: flex;
			flex-wrap: wrap;
			gap: 4px 12px;
			padding-left: 19px;
			margin-top: 2px;
			font-size: var(--text-xs);
			color: var(--fg-3);
		}
		.host {
			display: none;
		}
	}
</style>
