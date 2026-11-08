<script lang="ts">
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import CopyButton from '#lib/components/CopyButton.svelte';
	import EmptyState from '#lib/components/EmptyState.svelte';
	import ErrorState from '#lib/components/ErrorState.svelte';
	import Icon from '#lib/components/Icon.svelte';
	import PageHeader from '#lib/components/PageHeader.svelte';
	import RelTime from '#lib/components/RelTime.svelte';
	import StatusDot from '#lib/components/StatusDot.svelte';
	import { isNotFound } from '#lib/format.ts';
	import { t, type MessageKey } from '#lib/i18n/index.svelte.ts';
	import { app } from '#lib/state/app.svelte.ts';
	import { lane } from '#lib/state/lane.svelte.ts';
	import { mcpUrl } from '#lib/snippets.ts';
	import type { LayoutProps } from './$types';

	let { children, params }: LayoutProps = $props();

	$effect(() => lane.start(params.lane));

	const isMachinePage = $derived(page.route.id?.includes('/machines/[machine]') ?? false);
	const endpoint = $derived(mcpUrl(app.baseUrl, params.lane));

	// Keep the active tab visible when the tab strip scrolls horizontally (narrow screens).
	function revealActive(node: HTMLElement) {
		void page.url.pathname;
		const frame = requestAnimationFrame(() => {
			const active = node.querySelector<HTMLElement>('.active');
			if (active)
				node.scrollLeft = active.offsetLeft - node.clientWidth / 2 + active.offsetWidth / 2;
		});
		return () => cancelAnimationFrame(frame);
	}

	type Tab = { key: MessageKey; href: string; count?: number };
	const tabs = $derived<Tab[]>([
		{
			key: 'lane.tabs.machines',
			href: resolve('/(app)/lanes/[lane]', { lane: params.lane }),
			count: lane.machines?.length
		},
		{
			key: 'lane.tabs.skills',
			href: resolve('/(app)/lanes/[lane]/skills', { lane: params.lane }),
			count: lane.skills?.length
		},
		{
			key: 'lane.tabs.keys',
			href: resolve('/(app)/lanes/[lane]/keys', { lane: params.lane }),
			count: lane.activeKeyCount ?? undefined
		},
		{ key: 'lane.tabs.connect', href: resolve('/(app)/lanes/[lane]/setup', { lane: params.lane }) },
		{
			key: 'lane.tabs.activity',
			href: resolve('/(app)/lanes/[lane]/activity', { lane: params.lane })
		},
		{
			key: 'lane.tabs.settings',
			href: resolve('/(app)/lanes/[lane]/settings', { lane: params.lane })
		}
	]);
</script>

{#if lane.error && isNotFound(lane.error)}
	<EmptyState icon="layers" title={t('lane.notFoundTitle')} body={t('lane.notFoundBody')}>
		<a class="btn" href={resolve('/(app)')}>
			<Icon name="arrowLeft" size={14} />
			{t('lane.backToLanes')}
		</a>
	</EmptyState>
{:else if lane.error}
	<ErrorState error={lane.error} title={t('errors.title')} onretry={() => lane.loadLane()} />
{:else if isMachinePage}
	{@render children()}
{:else}
	<PageHeader title={lane.lane?.name} loading={!lane.lane}>
		{#snippet eyebrow()}
			<a href={resolve('/(app)')}>{t('nav.lanes')}</a>
			<Icon name="chevronRight" size={12} />
			<span class="mono crumb-id">{params.lane}</span>
		{/snippet}
		{#snippet meta()}
			{#if lane.lane}
				<span class="m">
					<StatusDot online={lane.lane.online_count > 0} label={false} />
					{t('overview.onlineOf', {
						online: lane.lane.online_count,
						total: lane.lane.machine_count
					})}
				</span>
				<span class="m">
					<RelTime
						time={lane.lane.created_at}
						prefix={(r: string) => t('lane.created_at', { time: r })}
					/>
				</span>
			{:else}
				<span class="skeleton" style:width="180px" style:height="10px"></span>
			{/if}
		{/snippet}
		{#snippet actions()}
			<div class="endpoint" title={endpoint}>
				<span class="ep-label">
					<Icon name="plug" size={13} />
					{t('lane.mcpUrl')}
				</span>
				<code class="ep-url">{endpoint.replace(/^https?:\/\//, '')}</code>
				<CopyButton text={endpoint} label={t('lane.copyMcp')} />
			</div>
		{/snippet}
	</PageHeader>

	<nav class="tabs" aria-label={t('lane.tabsLabel')} {@attach revealActive}>
		{#each tabs as tab (tab.key)}
			{@const active = page.url.pathname === tab.href}
			<a
				href={tab.href}
				class={['tab', active && 'active']}
				aria-current={active ? 'page' : undefined}
			>
				{t(tab.key)}
				{#if tab.count !== undefined}<span class="count tnum">{tab.count}</span>{/if}
			</a>
		{/each}
	</nav>

	<div class="panel">
		{@render children()}
	</div>
{/if}

<style>
	.crumb-id {
		font-size: var(--text-2xs);
	}
	.m {
		display: inline-flex;
		align-items: center;
		gap: 7px;
	}
	.endpoint {
		display: flex;
		align-items: center;
		gap: 8px;
		height: 32px;
		padding: 0 3px 0 10px;
		max-width: 100%;
		border: 1px solid var(--border);
		border-radius: var(--radius);
		background: var(--surface);
		box-shadow: var(--shadow-sm);
		min-width: 0;
	}
	.ep-label {
		display: inline-flex;
		align-items: center;
		gap: 6px;
		font-size: var(--text-xs);
		color: var(--fg-3);
		white-space: nowrap;
		padding-right: 8px;
		border-right: 1px solid var(--border);
	}
	.ep-url {
		font-size: var(--text-xs);
		color: var(--fg-2);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		min-width: 0;
		max-width: 340px;
	}
	.tabs {
		position: sticky;
		top: 0;
		z-index: 10;
		display: flex;
		gap: 2px;
		margin: 0 0 24px;
		border-bottom: 1px solid var(--border);
		background: var(--bg);
		overflow-x: auto;
		scrollbar-width: none;
	}
	.tab {
		position: relative;
		display: inline-flex;
		align-items: center;
		gap: 7px;
		height: 40px;
		padding: 0 10px;
		font-size: var(--text-sm);
		font-weight: 500;
		color: var(--fg-3);
		white-space: nowrap;
		transition: color var(--dur-1) var(--ease);
	}
	.tab:first-child {
		margin-left: -10px;
	}
	.tab::after {
		content: '';
		position: absolute;
		left: 10px;
		right: 10px;
		bottom: -1px;
		height: 2px;
		border-radius: 2px 2px 0 0;
		background: var(--fg);
		transform: scaleX(0);
		transition: transform var(--dur-2) var(--ease-out);
	}
	.tab:hover {
		color: var(--fg);
	}
	.tab.active {
		color: var(--fg);
	}
	.tab.active::after {
		transform: none;
	}
	.tab:focus-visible {
		outline-offset: -4px;
		border-radius: 8px;
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
	.active .count {
		background: var(--active);
		color: var(--fg);
	}
	@media (max-width: 860px) {
		.tabs {
			top: 52px;
			margin-left: -16px;
			margin-right: -16px;
			padding: 0 16px;
		}
		.tab:first-child {
			margin-left: -10px;
		}
		.endpoint {
			width: 100%;
		}
		.ep-url {
			max-width: none;
			flex: 1;
		}
	}
</style>
