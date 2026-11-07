<script lang="ts">
	import Rich from '#lib/components/Rich.svelte';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import { api } from '#lib/api.ts';
	import Badge from '#lib/components/Badge.svelte';
	import Callout from '#lib/components/Callout.svelte';
	import CopyButton from '#lib/components/CopyButton.svelte';
	import EmptyState from '#lib/components/EmptyState.svelte';
	import ErrorState from '#lib/components/ErrorState.svelte';
	import Icon from '#lib/components/Icon.svelte';
	import MachineDialogs from '#lib/components/MachineDialogs.svelte';
	import PageHeader from '#lib/components/PageHeader.svelte';
	import RelTime from '#lib/components/RelTime.svelte';
	import SkillViewer from '#lib/components/SkillViewer.svelte';
	import StatusDot from '#lib/components/StatusDot.svelte';
	import { osName, relativeTime, versionLabel } from '#lib/format.ts';
	import { t } from '#lib/i18n/index.svelte.ts';
	import { clock } from '#lib/state/clock.svelte.ts';
	import { lane } from '#lib/state/lane.svelte.ts';
	import type { SkillIndex } from '#lib/types.ts';
	import type { PageProps } from './$types';

	let { params }: PageProps = $props();

	const machine = $derived(lane.machines?.find((m) => m.id === params.machine) ?? null);
	const missing = $derived(lane.machines !== null && machine === null);

	let index = $state.raw<SkillIndex | null>(null);
	let indexError = $state.raw<unknown>(null);
	let renaming = $state(false);
	let removing = $state(false);

	// Refetch the skill index when the machine's index or connection changes.
	const indexSig = $derived(
		machine ? `${machine.id}:${machine.skills_hash}:${machine.online}` : ''
	);
	async function loadIndex(id: string) {
		try {
			const res = await api.machineSkills(params.lane, id);
			if (id !== params.machine) return;
			index = res;
			indexError = null;
		} catch (err) {
			if (id === params.machine) indexError = err;
		}
	}

	$effect(() => {
		if (indexSig) void loadIndex(params.machine);
	});

	const skills = $derived(index?.skills ?? []);
	const shadowed = $derived(index?.shadowed ?? []);
	const selectedName = $derived(page.url.searchParams.get('skill') ?? skills[0]?.name ?? '');
	const selected = $derived(skills.find((s) => s.name === selectedName) ?? null);
	const file = $derived(page.url.searchParams.get('file') ?? '');

	function select(skill: string, path = '') {
		const url = new URL(page.url.href);
		url.searchParams.set('skill', skill);
		if (path && path !== 'SKILL.md') url.searchParams.set('file', path);
		else url.searchParams.delete('file');
		void goto(url, { replace: true, reset: false });
	}

	const env = $derived(machine?.environment);
	const hasEnv = $derived(Boolean(env?.os));
</script>

<svelte:head>
	<title>{machine?.name ?? t('machine.back')} · {lane.lane?.name ?? 'Lane'} · Lanesh</title>
</svelte:head>

{#if lane.machinesError && !lane.machines}
	<ErrorState error={lane.machinesError} onretry={() => lane.loadMachines()} />
{:else if missing}
	<EmptyState icon="server" title={t('machine.notFoundTitle')} body={t('machine.notFoundBody')}>
		<a class="btn" href={resolve('/(app)/lanes/[lane]', { lane: params.lane })}>
			<Icon name="arrowLeft" size={14} />
			{t('machine.back')}
		</a>
	</EmptyState>
{:else}
	<PageHeader title={machine?.name} loading={!machine}>
		{#snippet eyebrow()}
			<a href={resolve('/(app)')}>{t('nav.lanes')}</a>
			<Icon name="chevronRight" size={12} />
			<a href={resolve('/(app)/lanes/[lane]', { lane: params.lane })}>{lane.lane?.name ?? '…'}</a>
			<Icon name="chevronRight" size={12} />
			<a href={resolve('/(app)/lanes/[lane]', { lane: params.lane })}>{t('machine.back')}</a>
		{/snippet}
		{#snippet meta()}
			{#if machine}
				<span class="m">
					<StatusDot online={machine.online} pulse label={false} />
					{#if machine.online && machine.connected_at}
						<RelTime
							time={machine.connected_at}
							prefix={(r: string) => t('machines.onlineSince', { time: r })}
						/>
					{:else}
						<RelTime
							time={machine.last_seen}
							prefix={(r: string) => t('machines.seen', { time: r })}
						/>
					{/if}
				</span>
				<span>{osName(machine.os)} · {machine.arch}</span>
				{#if machine.hostname}<span class="mono">{machine.hostname}</span>{/if}
				<span class="mono">{versionLabel(machine.version)}</span>
			{/if}
		{/snippet}
		{#snippet actions()}
			<button type="button" class="btn" disabled={!machine} onclick={() => (renaming = true)}>
				<Icon name="pencil" size={13} />
				{t('common.rename')}
			</button>
			<button
				type="button"
				class="btn btn-danger-ghost"
				disabled={!machine}
				onclick={() => (removing = true)}
			>
				<Icon name="trash" size={13} />
				{t('common.remove')}
			</button>
		{/snippet}
	</PageHeader>

	<div class="facts card">
		<section>
			<h2>{t('machine.environment')}</h2>
			{#if !machine}
				<div class="skel-lines">
					{#each [60, 80, 50] as w (w)}<span class="skeleton" style:width="{w}%" style:height="10px"
						></span>{/each}
				</div>
			{:else if machine}
				<dl>
					<div>
						<dt>{t('machine.os')}</dt>
						<dd>{osName(env?.os || machine.os)} · {env?.arch || machine.arch}</dd>
					</div>
					<div>
						<dt>{t('machine.hostname')}</dt>
						<dd class="mono" title={machine.hostname}>
							{env?.hostname || machine.hostname || '—'}
						</dd>
					</div>
					{#if hasEnv && env}
						<div>
							<dt>{t('machine.user')}</dt>
							<dd class="mono">{env.user || '—'}</dd>
						</div>
						<div>
							<dt>{t('machine.home')}</dt>
							<dd class="mono" title={env.home}>{env.home || '—'}</dd>
						</div>
						<div>
							<dt>{t('machine.shells')}</dt>
							<dd class="shells">
								{#each env.shells ?? [] as sh (sh)}
									<span
										class={['shell mono', sh === env.default_shell && 'default']}
										title={sh === env.default_shell ? t('machine.shell') : undefined}>{sh}</span
									>
								{/each}
							</dd>
						</div>
					{/if}
				</dl>
				{#if !hasEnv}
					<p class="faint small env-note">{t('machine.envOffline')}</p>
				{/if}
			{/if}
		</section>
		<section>
			<h2>{t('machine.policy')}</h2>
			{#if machine}
				<dl>
					<div>
						<dt>{t('machine.exposeSkills')}</dt>
						<dd>
							{#if machine.policy.expose_skills}
								<span class="yes"
									><Icon name="check" size={13} strokeWidth={2} />{t('common.yes')}</span
								>
							{:else}
								<span class="no">{t('common.no')}</span>
							{/if}
						</dd>
					</div>
					<div>
						<dt>{t('machine.allowExec')}</dt>
						<dd>
							{#if machine.policy.allow_exec}
								<span class="yes"
									><Icon name="check" size={13} strokeWidth={2} />{t('common.yes')}</span
								>
							{:else}
								<span class="no">{t('common.no')}</span>
							{/if}
						</dd>
					</div>
				</dl>
			{/if}
		</section>
		<section>
			<h2>{t('machine.connection')}</h2>
			{#if machine}
				<dl>
					<div>
						<dt>{t('machine.lastSeen')}</dt>
						<dd>
							{#if machine.online}<span class="online">{t('common.online')}</span>{:else}<RelTime
									time={machine.last_seen}
								/>{/if}
						</dd>
					</div>
					<div>
						<dt>{t('machine.joinedAt')}</dt>
						<dd><RelTime time={machine.joined_at} /></dd>
					</div>
					<div>
						<dt>{t('machine.machineId')}</dt>
						<dd class="idrow">
							<span class="mono">{machine.id}</span>
							<CopyButton text={machine.id} />
						</dd>
					</div>
				</dl>
			{/if}
		</section>
	</div>

	<section class="skills" aria-labelledby="skills-title">
		<div class="sh">
			<div>
				<h2 id="skills-title" class="section-title">
					{t('machine.skills')}
					{#if index}<span class="count tnum">{skills.length}</span>{/if}
				</h2>
				<p class="section-desc"><Rich text={t('machine.skillsDesc')} /></p>
			</div>
		</div>

		{#if machine && !machine.online && index}
			<Callout tone="warn">
				{t('machine.offlineBanner', {
					time:
						index.updated_at && !index.updated_at.startsWith('0001')
							? relativeTime(index.updated_at, clock.now)
							: t('common.unknown')
				})}
			</Callout>
		{/if}

		{#if indexError && !index}
			<ErrorState error={indexError} />
		{:else if !index}
			<div class="split card" aria-busy="true">
				<div class="list-col">
					{#each [0, 1, 2] as i (i)}
						<div class="sk-item">
							<span class="skeleton" style:width="70%" style:height="11px"></span>
						</div>
					{/each}
				</div>
				<div class="viewer-col"></div>
			</div>
		{:else if machine && !machine.policy.expose_skills}
			<EmptyState compact icon="lock" title={t('machine.skillsHidden')} />
		{:else if skills.length === 0}
			<EmptyState compact icon="sparkle" title={t('machine.noSkills')} />
		{:else}
			<div class="split card">
				<div class="list-col">
					<ul class="skill-list">
						{#each skills as s (s.name)}
							<li>
								<button
									type="button"
									class={['skill', selected?.name === s.name && 'on']}
									aria-current={selected?.name === s.name ? 'true' : undefined}
									onclick={() => select(s.name)}
								>
									<span class="sname mono">{s.name}</span>
									<span class={['sdesc', !s.description && 'none']}
										>{s.description || s.source}</span
									>
								</button>
							</li>
						{/each}
					</ul>
					{#if shadowed.length > 0}
						<div class="shadowed">
							<p class="sh-title" title={t('machine.shadowedDesc')}>
								{t('machine.shadowed')}
								<span class="count tnum">{shadowed.length}</span>
							</p>
							<p class="sh-desc">{t('machine.shadowedDesc')}</p>
							<ul>
								{#each shadowed as s (s.path)}
									<li class="shadow-item" title={s.path}>
										<span class="mono">{s.name}</span>
										<Badge tone="outline">{s.source}</Badge>
									</li>
								{/each}
							</ul>
						</div>
					{/if}
				</div>
				<div class="viewer-col">
					{#if selected && machine}
						<SkillViewer
							laneId={params.lane}
							machineId={machine.id}
							skill={selected}
							online={machine.online}
							{file}
							onfile={(p) => select(selected.name, p)}
						/>
					{:else}
						<div class="pick">
							<Icon name="sparkle" size={18} />
							<p>{t('machine.selectSkill')}</p>
						</div>
					{/if}
				</div>
			</div>
		{/if}
	</section>
{/if}

<MachineDialogs
	{machine}
	bind:renaming
	bind:removing
	onremoved={() => goto(resolve('/(app)/lanes/[lane]', { lane: params.lane }))}
/>

<style>
	.m {
		display: inline-flex;
		align-items: center;
		gap: 7px;
	}
	.facts {
		display: grid;
		grid-template-columns: 1.3fr 1fr 1fr;
		margin-bottom: 36px;
	}
	.facts section {
		padding: 16px 18px 18px;
		min-width: 0;
	}
	.facts section + section {
		border-left: 1px solid var(--border);
	}
	.facts h2 {
		margin-bottom: 12px;
		font-size: var(--text-xs);
		font-weight: 500;
		color: var(--fg-3);
	}
	dl {
		display: grid;
		gap: 9px;
	}
	dl div {
		display: grid;
		grid-template-columns: minmax(84px, auto) minmax(0, 1fr);
		gap: 12px;
		align-items: baseline;
		font-size: var(--text-sm);
	}
	dt {
		color: var(--fg-2);
		white-space: nowrap;
	}
	dd {
		text-align: right;
		color: var(--fg);
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	dd.mono {
		font-size: 12px;
	}
	.shells {
		display: flex;
		justify-content: flex-end;
		gap: 4px;
		flex-wrap: wrap;
	}
	.shell {
		font-size: 11px;
		padding: 1px 6px;
		border-radius: 4px;
		color: var(--fg-2);
		box-shadow: inset 0 0 0 1px var(--border);
	}
	.shell.default {
		color: var(--fg);
		background: var(--hover);
		box-shadow: inset 0 0 0 1px var(--border-strong);
	}
	.yes {
		display: inline-flex;
		align-items: center;
		gap: 5px;
		color: var(--green);
	}
	.no {
		color: var(--fg-3);
	}
	.online {
		color: var(--green);
	}
	.idrow {
		display: flex;
		align-items: center;
		justify-content: flex-end;
		gap: 2px;
		margin-right: -6px;
	}
	.idrow .mono {
		font-size: 11px;
		color: var(--fg-2);
		overflow: hidden;
		text-overflow: ellipsis;
	}
	.small {
		font-size: var(--text-sm);
	}
	.env-note {
		margin-top: 12px;
		font-size: var(--text-xs);
	}
	.skel-lines {
		display: grid;
		gap: 12px;
	}
	.skills {
		display: grid;
		gap: 14px;
	}
	.sh h2 {
		display: flex;
		align-items: center;
		gap: 8px;
	}
	.sh .section-desc {
		margin-top: 4px;
	}
	.count {
		min-width: 18px;
		height: 18px;
		padding: 0 5px;
		border-radius: 9px;
		background: var(--hover);
		font-size: var(--text-2xs);
		font-weight: 500;
		color: var(--fg-2);
		display: inline-grid;
		place-items: center;
	}
	.split {
		display: grid;
		grid-template-columns: 248px minmax(0, 1fr);
		min-height: 420px;
		overflow: hidden;
	}
	.list-col {
		border-right: 1px solid var(--border);
		padding: 8px;
		display: flex;
		flex-direction: column;
		gap: 16px;
		min-width: 0;
	}
	.sk-item {
		padding: 12px 10px;
	}
	.skill-list {
		display: grid;
		gap: 2px;
	}
	.skill {
		display: grid;
		gap: 1px;
		width: 100%;
		padding: 8px 10px;
		border-radius: 6px;
		text-align: left;
		transition: background-color var(--dur-1) var(--ease);
	}
	.skill:hover {
		background: var(--hover);
	}
	.skill.on {
		background: var(--active);
	}
	.skill:focus-visible {
		outline-offset: -2px;
	}
	.sname {
		font-size: 12.5px;
		font-weight: 500;
		color: var(--fg);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.sdesc {
		font-size: var(--text-xs);
		color: var(--fg-3);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.shadowed {
		padding: 10px 10px 4px;
		border-top: 1px solid var(--border);
		display: grid;
		gap: 6px;
	}
	.sh-title {
		display: flex;
		align-items: center;
		gap: 6px;
		font-size: var(--text-xs);
		font-weight: 500;
		color: var(--fg-2);
	}
	.sh-desc {
		font-size: var(--text-2xs);
		color: var(--fg-3);
		line-height: 1.45;
	}
	.shadow-item {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 8px;
		padding: 4px 0;
		font-size: 12px;
		color: var(--fg-3);
		text-decoration: line-through;
		text-decoration-color: var(--fg-4);
	}
	.viewer-col {
		min-width: 0;
	}
	.pick {
		height: 100%;
		display: grid;
		place-content: center;
		justify-items: center;
		gap: 10px;
		padding: 40px;
		font-size: var(--text-sm);
		color: var(--fg-3);
	}
	@media (max-width: 1000px) {
		.facts {
			grid-template-columns: 1fr;
		}
		.facts section + section {
			border-left: 0;
			border-top: 1px solid var(--border);
		}
	}
	@media (max-width: 860px) {
		.split {
			grid-template-columns: minmax(0, 1fr);
			min-height: 0;
		}
		.list-col {
			border-right: 0;
			border-bottom: 1px solid var(--border);
		}
		.skill-list {
			display: flex;
			gap: 4px;
			overflow-x: auto;
			scrollbar-width: none;
		}
		.skill-list li {
			flex: none;
		}
		.skill {
			padding: 6px 10px;
		}
		.sdesc {
			display: none;
		}
	}
</style>
