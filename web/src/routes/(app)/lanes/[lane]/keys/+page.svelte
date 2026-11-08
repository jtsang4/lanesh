<script lang="ts">
	import Badge from '#lib/components/Badge.svelte';
	import ConfirmDialog from '#lib/components/ConfirmDialog.svelte';
	import CreateKeyDialog from '#lib/components/CreateKeyDialog.svelte';
	import ErrorState from '#lib/components/ErrorState.svelte';
	import Icon from '#lib/components/Icon.svelte';
	import RelTime from '#lib/components/RelTime.svelte';
	import { t } from '#lib/i18n/index.svelte.ts';
	import Pager from '#lib/components/Pager.svelte';
	import { api } from '#lib/api.ts';
	import { keyStatus } from '#lib/keys.ts';
	import { clock } from '#lib/state/clock.svelte.ts';
	import { lane } from '#lib/state/lane.svelte.ts';
	import { toasts } from '#lib/state/toasts.svelte.ts';
	import type { Key, KeyKind } from '#lib/types.ts';
	import type { PageProps } from './$types';

	let { params }: PageProps = $props();

	const PAGE = 20;
	const KINDS = ['join', 'access'] as const;

	let createKind = $state<KeyKind>('join');
	let creating = $state(false);
	let revokeTarget = $state<Key | null>(null);
	let revoking = $state(false);

	// Each kind is its own paged list: active keys first, then newest first.
	type List = { keys: Key[] | null; total: number; offset: number; error: unknown; busy: boolean };
	const blank = (): List => ({ keys: null, total: 0, offset: 0, error: null, busy: false });
	let lists = $state<Record<KeyKind, List>>({ join: blank(), access: blank() });
	const seq: Record<KeyKind, number> = { join: 0, access: 0 };

	$effect.pre(() => {
		void params.lane;
		lists = { join: blank(), access: blank() };
	});

	async function load(kind: KeyKind) {
		const list = lists[kind];
		const mine = ++seq[kind];
		list.busy = true;
		try {
			const page = await api.keys(params.lane, { kind, limit: PAGE, offset: list.offset });
			if (mine !== seq[kind]) return;
			if (page.items.length === 0 && list.offset > 0 && page.total > 0) {
				list.offset = Math.floor((page.total - 1) / PAGE) * PAGE;
				return;
			}
			list.keys = page.items;
			list.total = page.total;
			list.error = null;
		} catch (err) {
			if (mine === seq[kind]) list.error = err;
		} finally {
			if (mine === seq[kind]) list.busy = false;
		}
	}

	for (const kind of KINDS) {
		$effect(() => {
			void params.lane;
			void lists[kind].offset;
			void lane.keysVersion;
			void load(kind);
		});
	}

	const failed = $derived(KINDS.find((k) => lists[k].error && !lists[k].keys));

	function create(kind: KeyKind) {
		createKind = kind;
		creating = true;
	}

	function revoke(k: Key) {
		revokeTarget = k;
		revoking = true;
	}
</script>

<svelte:head>
	<title>{lane.lane?.name ?? 'Lane'} · {t('lane.tabs.keys')} · Lanesh</title>
</svelte:head>

{#if failed}
	<ErrorState error={lists[failed].error} onretry={() => KINDS.forEach((k) => load(k))} />
{:else}
	<div class="sections">
		{#each KINDS.map((kind) => ({ kind, list: lists[kind] })) as g (g.kind)}
			<section aria-labelledby="keys-{g.kind}">
				<div class="head">
					<div class="titles">
						<h2 id="keys-{g.kind}" class="section-title">
							<Icon name={g.kind === 'join' ? 'server' : 'plug'} size={15} />
							{t(g.kind === 'join' ? 'keys.join.title' : 'keys.access.title')}
						</h2>
						<p class="section-desc">
							{t(g.kind === 'join' ? 'keys.join.desc' : 'keys.access.desc')}
						</p>
					</div>
					<button type="button" class="btn" onclick={() => create(g.kind)}>
						<Icon name="plus" size={14} />
						{t(g.kind === 'join' ? 'keys.new.join' : 'keys.new.access')}
					</button>
				</div>

				{#if g.list.keys === null}
					<div class="table card" aria-busy="true">
						{#each [0, 1] as i (i)}
							<div class="row skel">
								<span class="skeleton" style:width="140px" style:height="12px"></span>
								<span class="skeleton" style:width="80px" style:height="10px"></span>
							</div>
						{/each}
					</div>
				{:else if g.list.keys.length === 0}
					<div class="empty">
						<p>{t(g.kind === 'join' ? 'keys.empty.join' : 'keys.empty.access')}</p>
					</div>
				{:else}
					<div class={['table card', g.list.busy && 'busy']}>
						<div class="row head-row" aria-hidden="true">
							<span>{t('keys.colKey')}</span>
							<span>{t('keys.colCreated')}</span>
							<span>{t('keys.colLastUsed')}</span>
							<span>{t('keys.colExpires')}</span>
							<span>{t('keys.colStatus')}</span>
							<span></span>
						</div>
						<ul>
							{#each g.list.keys as k (k.id)}
								{@const status = keyStatus(k, clock.now)}
								<li class={['row key', status !== 'active' && 'inactive']}>
									<div class="who">
										<span class="name">{k.name}</span>
										<span class="prefix mono">{k.prefix}…</span>
									</div>
									<div class="cell" data-label={t('keys.colCreated')}>
										<RelTime time={k.created_at} />
									</div>
									<div class="cell" data-label={t('keys.colLastUsed')}>
										{#if k.last_used_at}<RelTime time={k.last_used_at} />{:else}<span class="faint"
												>{t('keys.neverUsed')}</span
											>{/if}
									</div>
									<div class="cell" data-label={t('keys.colExpires')}>
										{#if status === 'revoked'}<span class="faint">—</span
											>{:else if k.expires_at}<RelTime time={k.expires_at} />{:else}<span
												class="faint">{t('common.never')}</span
											>{/if}
									</div>
									<div class="cell status">
										{#if status === 'active'}
											<Badge tone="green">{t('keys.status.active')}</Badge>
										{:else if status === 'expired'}
											<Badge tone="amber">{t('keys.status.expired')}</Badge>
										{:else}
											<Badge tone="neutral">{t('keys.status.revoked')}</Badge>
										{/if}
									</div>
									<div class="mobile-meta">
										{#if k.last_used_at}
											<RelTime
												time={k.last_used_at}
												prefix={(r: string) => `${t('keys.colLastUsed')} ${r}`}
											/>
										{:else}
											<span>{t('keys.neverUsed')}</span>
										{/if}
										{#if k.expires_at}
											<RelTime
												time={k.expires_at}
												prefix={(r: string) => `${t('keys.colExpires')} ${r}`}
											/>
										{/if}
									</div>
									<div class="act">
										{#if status !== 'revoked'}
											<button
												type="button"
												class="btn btn-sm btn-ghost revoke"
												onclick={() => revoke(k)}
											>
												{t('common.revoke')}
											</button>
										{/if}
									</div>
								</li>
							{/each}
						</ul>
					</div>
					<Pager
						total={g.list.total}
						limit={PAGE}
						bind:offset={lists[g.kind].offset}
						busy={g.list.busy}
					/>
				{/if}
			</section>
		{/each}
	</div>
{/if}

<CreateKeyDialog bind:open={creating} kind={createKind} />

{#if revokeTarget}
	{@const k = revokeTarget}
	<ConfirmDialog
		bind:open={revoking}
		title={t('keys.revoke.title', { name: k.name })}
		body={t(k.kind === 'join' ? 'keys.revoke.body.join' : 'keys.revoke.body.access')}
		confirmLabel={t('keys.revoke.confirm')}
		onconfirm={async () => {
			await lane.revokeKey(k);
			toasts.show(t('keys.revoked', { name: k.name }));
		}}
	/>
{/if}

<style>
	.table.busy {
		opacity: 0.65;
		transition: opacity var(--dur-1) var(--ease);
	}
	.sections {
		display: grid;
		gap: 40px;
	}
	.head {
		display: flex;
		align-items: flex-end;
		justify-content: space-between;
		gap: 16px;
		margin-bottom: 14px;
		flex-wrap: wrap;
	}
	.titles {
		display: grid;
		gap: 4px;
		min-width: 0;
	}
	h2 {
		display: flex;
		align-items: center;
		gap: 8px;
	}
	h2 :global(svg) {
		color: var(--fg-3);
	}
	.empty {
		padding: 22px 16px;
		border: 1px dashed var(--border-strong);
		border-radius: var(--radius-lg);
		font-size: var(--text-sm);
		color: var(--fg-3);
		text-align: center;
	}
	.table {
		overflow: hidden;
	}
	.row {
		display: grid;
		grid-template-columns:
			minmax(0, 1.5fr) minmax(0, 0.8fr) minmax(0, 0.8fr) minmax(0, 0.8fr)
			84px 76px;
		align-items: center;
		gap: 16px;
		padding: 0 10px 0 16px;
	}
	.head-row {
		height: 34px;
		font-size: var(--text-xs);
		color: var(--fg-3);
		background: var(--surface-2);
		border-bottom: 1px solid var(--border);
	}
	.skel {
		height: 54px;
		grid-template-columns: 1fr 1fr;
	}
	.skel + .skel,
	li + li {
		border-top: 1px solid var(--border);
	}
	.key {
		min-height: 54px;
		animation: rise-in var(--dur-3) var(--ease-out) both;
		transition: opacity var(--dur-2) var(--ease);
	}
	.who {
		display: grid;
		min-width: 0;
		padding: 9px 0;
	}
	.name {
		font-weight: 500;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.prefix {
		font-size: var(--text-2xs);
		color: var(--fg-3);
	}
	.cell {
		font-size: var(--text-sm);
		color: var(--fg-2);
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.inactive .name,
	.inactive .cell:not(.status) {
		color: var(--fg-3);
	}
	.inactive .name {
		text-decoration: line-through;
		text-decoration-color: var(--fg-4);
	}
	.act {
		display: flex;
		justify-content: flex-end;
	}
	.mobile-meta {
		display: none;
	}
	.revoke:hover {
		color: var(--red);
		background: var(--red-soft);
	}
	@media (max-width: 860px) {
		.head-row {
			display: none;
		}
		.key {
			grid-template-columns: minmax(0, 1fr) auto;
			grid-template-areas: 'who status' 'meta act';
			gap: 6px 12px;
			padding: 12px 10px 12px 14px;
		}
		.who {
			grid-area: who;
			padding: 0;
		}
		.status {
			grid-area: status;
			justify-self: end;
		}
		.cell[data-label] {
			display: none;
		}
		.act {
			grid-area: act;
		}
		.mobile-meta {
			grid-area: meta;
			display: flex;
			flex-wrap: wrap;
			align-items: center;
			gap: 2px 12px;
			font-size: var(--text-xs);
			color: var(--fg-3);
		}
	}
</style>
