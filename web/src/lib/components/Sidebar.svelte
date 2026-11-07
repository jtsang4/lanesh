<script lang="ts">
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import { api } from '#lib/api.ts';
	import { versionLabel } from '#lib/format.ts';
	import { t } from '#lib/i18n/index.svelte.ts';
	import { app } from '#lib/state/app.svelte.ts';
	import { ui } from '#lib/state/ui.svelte.ts';
	import Icon from './Icon.svelte';
	import Logo from './Logo.svelte';
	import Prefs from './Prefs.svelte';

	let { version }: { version: string } = $props();

	const path = $derived(page.url.pathname);
	const laneId = $derived(page.params.lane);

	async function signOut() {
		try {
			await api.logout();
		} finally {
			app.reset();
			await goto(resolve('/login'));
		}
	}
</script>

<div class="side">
	<a class="brand" href={resolve('/(app)')}>
		<Logo size={20} />
		<span>Lanesh</span>
	</a>

	<nav aria-label={t('nav.mainNav')}>
		<ul class="group">
			<li>
				<a
					class={['item', path === resolve('/(app)') && 'active']}
					href={resolve('/(app)')}
					aria-current={path === resolve('/(app)') ? 'page' : undefined}
				>
					<Icon name="home" size={15} />
					<span>{t('nav.overview')}</span>
				</a>
			</li>
			<li>
				<a
					class={['item', path === resolve('/(app)/instance') && 'active']}
					href={resolve('/(app)/instance')}
					aria-current={path === resolve('/(app)/instance') ? 'page' : undefined}
				>
					<Icon name="server" size={15} />
					<span>{t('nav.instance')}</span>
				</a>
			</li>
		</ul>

		<div class="heading">
			<span>{t('nav.lanes')}</span>
			<button
				type="button"
				class="add"
				onclick={() => (ui.createLaneOpen = true)}
				aria-label={t('nav.newLane')}
				title={t('nav.newLane')}
			>
				<Icon name="plus" size={14} />
			</button>
		</div>

		{#if app.lanes === null}
			<ul class="group" aria-busy="true">
				{#each [72, 54] as w (w)}
					<li class="item skel">
						<span class="skeleton" style:width="{w}%" style:height="10px"></span>
					</li>
				{/each}
			</ul>
		{:else if app.lanes.length === 0}
			<p class="none">{t('nav.noLanes')}</p>
		{:else}
			<ul class="group">
				{#each app.lanes as l (l.id)}
					<li>
						<a
							class={['item lane', laneId === l.id && 'active']}
							href={resolve('/(app)/lanes/[lane]', { lane: l.id })}
							aria-current={laneId === l.id ? 'page' : undefined}
						>
							<span class={['ldot', l.online_count > 0 && 'on']} aria-hidden="true"></span>
							<span class="lname">{l.name}</span>
							<span
								class="count tnum"
								title={t('overview.onlineOf', { online: l.online_count, total: l.machine_count })}
								>{l.online_count}/{l.machine_count}</span
							>
						</a>
					</li>
				{/each}
			</ul>
		{/if}
	</nav>

	<div class="foot">
		<Prefs />
		<div class="row">
			<span class="ver mono" title="Lanesh {version}">{versionLabel(version)}</span>
			<button type="button" class="btn btn-ghost btn-sm signout" onclick={signOut}>
				<Icon name="logout" size={14} />
				{t('nav.signOut')}
			</button>
		</div>
	</div>
</div>

<style>
	.side {
		display: flex;
		flex-direction: column;
		height: 100%;
		padding: 14px 10px 12px;
		gap: 4px;
	}
	.brand {
		display: flex;
		align-items: center;
		gap: 9px;
		height: 32px;
		padding: 0 8px;
		margin-bottom: 14px;
		border-radius: var(--radius);
		font-weight: 600;
		font-size: 15px;
		letter-spacing: -0.02em;
		width: fit-content;
	}
	nav {
		flex: 1;
		min-height: 0;
		overflow-y: auto;
		display: flex;
		flex-direction: column;
		gap: 2px;
		margin: 0 -4px;
		padding: 0 4px;
	}
	.group {
		display: grid;
		gap: 1px;
	}
	.item {
		display: flex;
		align-items: center;
		gap: 9px;
		height: 30px;
		padding: 0 8px;
		border-radius: 6px;
		font-size: var(--text-sm);
		color: var(--fg-2);
		transition:
			background-color var(--dur-1) var(--ease),
			color var(--dur-1) var(--ease);
	}
	.item :global(svg) {
		color: var(--fg-3);
		transition: color var(--dur-1) var(--ease);
	}
	.item:hover {
		background: var(--hover);
		color: var(--fg);
	}
	.item.active {
		background: var(--active);
		color: var(--fg);
		font-weight: 500;
	}
	.item.active :global(svg) {
		color: var(--fg);
	}
	.item.skel {
		pointer-events: none;
	}
	.heading {
		display: flex;
		align-items: center;
		justify-content: space-between;
		margin: 18px 0 4px;
		padding: 0 4px 0 8px;
		font-size: var(--text-2xs);
		font-weight: 500;
		letter-spacing: 0.04em;
		text-transform: uppercase;
		color: var(--fg-3);
	}
	.add {
		display: grid;
		place-items: center;
		width: 22px;
		height: 22px;
		border-radius: 5px;
		color: var(--fg-3);
		transition:
			background-color var(--dur-1) var(--ease),
			color var(--dur-1) var(--ease);
	}
	.add:hover {
		background: var(--hover);
		color: var(--fg);
	}
	.ldot {
		width: 6px;
		height: 6px;
		margin: 0 4px 0 5px;
		border-radius: 50%;
		box-shadow: inset 0 0 0 1.5px var(--fg-4);
		flex: none;
		transition: background-color var(--dur-3) var(--ease);
	}
	.ldot.on {
		background: var(--green);
		box-shadow: none;
	}
	.lname {
		flex: 1;
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.count {
		font-size: var(--text-2xs);
		color: var(--fg-3);
		font-weight: 400;
	}
	.none {
		padding: 4px 8px;
		font-size: var(--text-xs);
		color: var(--fg-3);
	}
	.foot {
		display: grid;
		gap: 8px;
		padding: 10px 4px 0;
		border-top: 1px solid var(--border);
	}
	.row {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 8px;
	}
	.ver {
		font-size: var(--text-2xs);
		color: var(--fg-3);
		padding-left: 4px;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.signout {
		margin-right: -4px;
	}
</style>
