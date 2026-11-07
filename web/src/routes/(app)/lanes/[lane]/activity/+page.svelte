<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { api } from '#lib/api.ts';
	import { ACTIONS, actionIcon, actionKey } from '#lib/audit.ts';
	import AuditDetail from '#lib/components/AuditDetail.svelte';
	import EmptyState from '#lib/components/EmptyState.svelte';
	import ErrorState from '#lib/components/ErrorState.svelte';
	import Icon from '#lib/components/Icon.svelte';
	import RelTime from '#lib/components/RelTime.svelte';
	import { clockTime, shortDate } from '#lib/format.ts';
	import { i18n, t } from '#lib/i18n/index.svelte.ts';
	import { lane } from '#lib/state/lane.svelte.ts';
	import { poll } from '#lib/state/poll.ts';
	import type { AuditEntry } from '#lib/types.ts';
	import type { PageProps } from './$types';

	let { params }: PageProps = $props();

	const PAGE = 50;

	const filter = $derived({
		action: page.url.searchParams.get('action') ?? '',
		machine: page.url.searchParams.get('machine') ?? '',
		actor: page.url.searchParams.get('actor') ?? ''
	});
	const filtered = $derived(Boolean(filter.action || filter.machine || filter.actor));

	let entries = $state.raw<AuditEntry[] | null>(null);
	let error = $state.raw<unknown>(null);
	let done = $state(false);
	let loadingMore = $state(false);
	let fresh = $state.raw(new Set<number>());
	let seq = 0;

	async function load() {
		const mine = ++seq;
		try {
			const list = await api.audit(params.lane, { ...filter, limit: PAGE });
			if (mine !== seq) return;
			entries = list;
			done = list.length < PAGE;
			error = null;
		} catch (err) {
			if (mine === seq) error = err;
		}
	}

	async function loadMore() {
		if (!entries?.length || loadingMore) return;
		loadingMore = true;
		const mine = seq;
		try {
			const before = entries[entries.length - 1].id;
			const list = await api.audit(params.lane, { ...filter, before, limit: PAGE });
			if (mine !== seq) return;
			entries = [...entries, ...list];
			done = list.length < PAGE;
		} catch (err) {
			error = err;
		} finally {
			loadingMore = false;
		}
	}

	/** Prepends entries newer than the newest one shown. */
	async function refreshHead() {
		if (!entries) return;
		const mine = seq;
		const list = await api.audit(params.lane, { ...filter, limit: PAGE });
		if (mine !== seq || !entries) return;
		const top = entries[0]?.id ?? 0;
		const newer = list.filter((e) => e.id > top);
		if (newer.length === 0) return;
		fresh = new Set(newer.map((e) => e.id));
		entries = newer.length === list.length ? list : [...newer, ...entries];
	}

	$effect(() => {
		// Reload whenever the lane or the filters change.
		void params.lane;
		void filter;
		entries = null;
		error = null;
		done = false;
		void load();
		return poll(refreshHead, 6000);
	});

	function setFilter(key: 'action' | 'machine' | 'actor', value: string) {
		const url = new URL(page.url.href);
		if (value) url.searchParams.set(key, value);
		else url.searchParams.delete(key);
		void goto(url, { replace: true, reset: false });
	}

	function clearFilters() {
		const url = new URL(page.url.href);
		url.search = '';
		void goto(url, { replace: true, reset: false });
	}

	function dayKey(iso: string) {
		const d = new Date(iso);
		return `${d.getFullYear()}-${d.getMonth()}-${d.getDate()}`;
	}

	const groups = $derived.by(() => {
		void i18n.locale;
		const out: { key: string; label: string; items: AuditEntry[] }[] = [];
		const today = dayKey(new Date().toISOString());
		const yesterday = dayKey(new Date(Date.now() - 86_400_000).toISOString());
		for (const e of entries ?? []) {
			const k = dayKey(e.time);
			let g = out.at(-1);
			if (!g || g.key !== k) {
				const label =
					k === today
						? t('activity.today')
						: k === yesterday
							? t('activity.yesterday')
							: shortDate(e.time);
				g = { key: k, label, items: [] };
				out.push(g);
			}
			g.items.push(e);
		}
		return out;
	});

	function actorLabel(actor: string): {
		icon: 'lock' | 'key' | 'server' | 'activity';
		text: string;
	} {
		if (actor === 'admin') return { icon: 'lock', text: t('activity.admin') };
		const key = lane.keyNames.get(actor);
		if (key) return { icon: 'key', text: key };
		const machine = lane.machineNames.get(actor);
		if (machine) return { icon: 'server', text: machine };
		if (actor.startsWith('key_')) return { icon: 'key', text: actor };
		if (actor.startsWith('mch_')) return { icon: 'server', text: actor };
		return { icon: 'activity', text: actor };
	}
</script>

<svelte:head>
	<title>{lane.lane?.name ?? 'Lane'} · {t('lane.tabs.activity')} · Lanesh</title>
</svelte:head>

<div class="toolbar">
	<div class="filters">
		<label class="filter">
			<span class="sr-only">{t('activity.filterAction')}</span>
			<select
				class={['select', filter.action && 'set']}
				value={filter.action}
				onchange={(e) => setFilter('action', e.currentTarget.value)}
			>
				<option value="">{t('activity.allActions')}</option>
				{#each ACTIONS as a (a)}
					{@const key = actionKey(a)}
					<option value={a}>{key ? t(key) : a}</option>
				{/each}
			</select>
		</label>
		<label class="filter">
			<span class="sr-only">{t('activity.filterMachine')}</span>
			<select
				class={['select', filter.machine && 'set']}
				value={filter.machine}
				onchange={(e) => setFilter('machine', e.currentTarget.value)}
			>
				<option value="">{t('activity.allMachines')}</option>
				{#each lane.machines ?? [] as m (m.id)}
					<option value={m.id}>{m.name}</option>
				{/each}
				{#if filter.machine && !lane.machineNames.has(filter.machine)}
					<option value={filter.machine}>{filter.machine}</option>
				{/if}
			</select>
		</label>
		<label class="filter">
			<span class="sr-only">{t('activity.filterActor')}</span>
			<select
				class={['select', filter.actor && 'set']}
				value={filter.actor}
				onchange={(e) => setFilter('actor', e.currentTarget.value)}
			>
				<option value="">{t('activity.allActors')}</option>
				<option value="admin">{t('activity.admin')}</option>
				{#each lane.keys ?? [] as k (k.id)}
					<option value={k.id}>{k.name} · {k.kind}</option>
				{/each}
			</select>
		</label>
		{#if filtered}
			<button type="button" class="btn btn-ghost btn-sm" onclick={clearFilters}>
				<Icon name="x" size={13} />
				{t('activity.clearFilters')}
			</button>
		{/if}
	</div>
	<span class="live" title={t('activity.liveHint')}>
		<span class="pulse" aria-hidden="true"></span>
		{t('activity.live')}
	</span>
</div>

{#if error && !entries}
	<ErrorState {error} onretry={load} />
{:else if entries === null}
	<div class="feed card" aria-busy="true">
		{#each [0, 1, 2, 3, 4] as i (i)}
			<div class="row skel">
				<span class="skeleton" style:width="56px" style:height="10px"></span>
				<span class="skeleton" style:width="90px" style:height="10px"></span>
				<span class="skeleton" style:width="{60 - i * 6}%" style:height="10px"></span>
			</div>
		{/each}
	</div>
{:else if entries.length === 0}
	{#if filtered}
		<EmptyState compact icon="search" title={t('activity.noMatch')}>
			<button type="button" class="btn btn-sm" onclick={clearFilters}
				>{t('activity.clearFilters')}</button
			>
		</EmptyState>
	{:else}
		<EmptyState icon="activity" title={t('activity.emptyTitle')} body={t('activity.emptyBody')} />
	{/if}
{:else}
	<div class="feed">
		{#each groups as g (g.key)}
			<section class="day" aria-label={g.label}>
				<h2 class="day-label">{g.label}</h2>
				<ol class="card list">
					{#each g.items as e (e.id)}
						{@const key = actionKey(e.action)}
						{@const actor = actorLabel(e.actor)}
						{@const failed = e.result.startsWith('error')}
						<li class={['row entry', fresh.has(e.id) && 'new']}>
							<span class="time tnum">
								<span class="clock-main">{clockTime(e.time)}</span>
								<span class="rel"><RelTime time={e.time} /></span>
							</span>
							<span class={['action', failed && 'failed']}>
								<Icon name={actionIcon(e.action)} size={13} />
								{key ? t(key) : e.action}
							</span>
							<AuditDetail entry={e} />
							<span class="who">
								{#if e.machine_id}
									<button
										type="button"
										class="tag"
										title={t('activity.filterMachine')}
										onclick={() => setFilter('machine', e.machine_id)}
									>
										<Icon name="server" size={12} />
										<span>{lane.machineNames.get(e.machine_id) ?? e.machine_id.slice(0, 12)}</span>
									</button>
								{/if}
								<button
									type="button"
									class="tag subtle"
									title={t('activity.filterActor')}
									onclick={() => setFilter('actor', e.actor)}
								>
									<Icon name={actor.icon} size={12} />
									<span>{actor.text}</span>
								</button>
							</span>
						</li>
					{/each}
				</ol>
			</section>
		{/each}

		<div class="more">
			{#if !done}
				<button type="button" class="btn" onclick={loadMore} disabled={loadingMore}>
					{loadingMore ? t('activity.loadingMore') : t('activity.loadMore')}
				</button>
			{:else}
				<p class="faint">{t('activity.end')}</p>
			{/if}
		</div>
	</div>
{/if}

<style>
	.toolbar {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 12px;
		margin-bottom: 18px;
		flex-wrap: wrap;
	}
	.filters {
		display: flex;
		align-items: center;
		gap: 8px;
		flex-wrap: wrap;
	}
	.filter .select {
		width: auto;
		min-width: 140px;
		max-width: 220px;
	}
	.select.set {
		border-color: color-mix(in srgb, var(--accent) 45%, var(--border-strong));
		background-color: var(--accent-soft);
		color: var(--accent-text);
	}
	.live {
		display: inline-flex;
		align-items: center;
		gap: 7px;
		font-size: var(--text-xs);
		color: var(--fg-3);
	}
	.pulse {
		position: relative;
		width: 6px;
		height: 6px;
		border-radius: 50%;
		background: var(--green);
	}
	.pulse::after {
		content: '';
		position: absolute;
		inset: 0;
		border-radius: 50%;
		background: var(--green);
		animation: ping 2.4s var(--ease-out) infinite;
	}
	@keyframes ping {
		0% {
			transform: scale(1);
			opacity: 0.5;
		}
		70%,
		100% {
			transform: scale(2.8);
			opacity: 0;
		}
	}
	.feed {
		display: grid;
		gap: 22px;
	}
	.feed.card {
		gap: 0;
		overflow: hidden;
	}
	.day {
		display: grid;
		gap: 8px;
	}
	.day-label {
		font-size: var(--text-xs);
		font-weight: 500;
		color: var(--fg-3);
		padding-left: 2px;
		position: sticky;
		top: 41px;
		z-index: 2;
		background: var(--bg);
		padding-block: 4px;
	}
	.list {
		overflow: hidden;
	}
	.row {
		display: grid;
		grid-template-columns: 92px 124px minmax(0, 1fr) minmax(0, auto);
		align-items: start;
		gap: 16px;
		padding: 11px 14px 11px 16px;
	}
	.skel {
		align-items: center;
		grid-template-columns: 92px 124px 1fr;
		height: 48px;
	}
	.skel + .skel,
	.entry + .entry {
		border-top: 1px solid var(--border);
	}
	.entry {
		transition: background-color var(--dur-1) var(--ease);
	}
	.entry:hover {
		background: var(--surface-2);
	}
	.entry.new {
		animation: flash 1.6s var(--ease) both;
	}
	@keyframes flash {
		0% {
			background: var(--accent-soft);
		}
		100% {
			background: transparent;
		}
	}
	.time {
		display: grid;
		font-size: var(--text-xs);
		color: var(--fg-2);
		line-height: 1.5;
		padding-top: 1px;
	}
	.clock-main {
		color: var(--fg);
		font-size: var(--text-xs);
	}
	.rel {
		font-size: var(--text-2xs);
		color: var(--fg-3);
	}
	.action {
		display: inline-flex;
		align-items: center;
		gap: 7px;
		font-size: var(--text-xs);
		font-weight: 500;
		color: var(--fg);
		padding-top: 2px;
		white-space: nowrap;
	}
	.action :global(svg) {
		color: var(--fg-3);
	}
	.action.failed,
	.action.failed :global(svg) {
		color: var(--red);
	}
	.who {
		display: flex;
		align-items: center;
		justify-content: flex-end;
		gap: 6px;
		flex-wrap: wrap;
		min-width: 0;
	}
	.tag {
		display: inline-flex;
		align-items: center;
		gap: 5px;
		height: 22px;
		max-width: 180px;
		padding: 0 7px;
		border-radius: 5px;
		background: var(--hover);
		font-size: var(--text-2xs);
		color: var(--fg-2);
		white-space: nowrap;
		transition:
			background-color var(--dur-1) var(--ease),
			color var(--dur-1) var(--ease);
	}
	.tag span {
		overflow: hidden;
		text-overflow: ellipsis;
	}
	.tag.subtle {
		background: transparent;
		box-shadow: inset 0 0 0 1px var(--border);
	}
	.tag:hover {
		background: var(--active);
		color: var(--fg);
	}
	.tag :global(svg) {
		color: var(--fg-3);
	}
	.more {
		display: flex;
		justify-content: center;
		padding: 4px 0 8px;
		font-size: var(--text-xs);
	}
	@media (max-width: 860px) {
		.day-label {
			top: 93px;
		}
		.row {
			grid-template-columns: minmax(0, 1fr) auto;
			grid-template-areas: 'action time' 'detail detail' 'who who';
			gap: 6px 12px;
			padding: 12px 14px;
		}
		.skel {
			grid-template-columns: 1fr 1fr;
			grid-template-areas: none;
		}
		.time {
			grid-area: time;
			text-align: right;
		}
		.rel {
			display: none;
		}
		.action {
			grid-area: action;
		}
		.entry > :global(.detail) {
			grid-area: detail;
		}
		.who {
			grid-area: who;
			justify-content: flex-start;
		}
		.filter .select {
			width: 100%;
			min-width: 0;
			max-width: none;
		}
		.filters {
			display: grid;
			grid-template-columns: 1fr 1fr;
			width: 100%;
		}
		.filter:nth-child(3) {
			grid-column: 1 / -1;
		}
	}
</style>
