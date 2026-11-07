<script lang="ts">
	import Rich from '#lib/components/Rich.svelte';
	import Callout from '#lib/components/Callout.svelte';
	import CopyButton from '#lib/components/CopyButton.svelte';
	import ErrorState from '#lib/components/ErrorState.svelte';
	import PageHeader from '#lib/components/PageHeader.svelte';
	import RelTime from '#lib/components/RelTime.svelte';
	import { duration } from '#lib/format.ts';
	import { t } from '#lib/i18n/index.svelte.ts';
	import { app } from '#lib/state/app.svelte.ts';
	import { clock } from '#lib/state/clock.svelte.ts';
	import { poll } from '#lib/state/poll.ts';

	let error = $state.raw<unknown>(null);

	async function refresh() {
		try {
			await app.refreshInfo();
			error = null;
		} catch (err) {
			error = err;
		}
	}

	$effect(() => {
		void refresh();
		return poll(refresh, 10_000);
	});

	const info = $derived(app.info);
	const uptime = $derived(info ? duration(clock.now - new Date(info.started_at).getTime()) : '');
</script>

<svelte:head>
	<title>{t('instance.title')} · Lanesh</title>
</svelte:head>

<PageHeader title={t('instance.title')} description={t('instance.subtitle')} />

{#if error && !info}
	<ErrorState {error} onretry={refresh} />
{:else}
	<div class="page">
		{#if info && !info.public_url_configured}
			<Callout tone="warn" title={t('instance.warnTitle')}>
				<Rich text={t('instance.warnBody', { origin: location.origin })} />
			</Callout>
		{/if}

		<dl class="stats card">
			<div class="stat">
				<dt>{t('instance.uptime')}</dt>
				<dd class="tnum">
					{#if info}{uptime}{:else}<span class="skeleton" style:width="70px" style:height="20px"
						></span>{/if}
				</dd>
				{#if info}
					<p class="sub">
						<RelTime
							time={info.started_at}
							prefix={(r: string) => t('instance.startedAt', { time: r })}
						/>
					</p>
				{/if}
			</div>
			<div class="stat">
				<dt>{t('instance.lanes')}</dt>
				<dd class="tnum">{info?.lanes ?? '–'}</dd>
			</div>
			<div class="stat">
				<dt>{t('instance.machines')}</dt>
				<dd class="tnum">{info?.machines ?? '–'}</dd>
			</div>
			<div class="stat">
				<dt>{t('instance.online')}</dt>
				<dd class="tnum">
					{info?.online ?? '–'}
					{#if info && info.online > 0}<span class="live" aria-hidden="true"></span>{/if}
				</dd>
			</div>
		</dl>

		<section class="card details" aria-labelledby="server-h">
			<h2 id="server-h">{t('instance.server')}</h2>
			<dl>
				<div>
					<dt>{t('instance.version')}</dt>
					<dd class="mono">{info?.version ?? '…'}</dd>
				</div>
				<div>
					<dt>{t('instance.commit')}</dt>
					<dd class="mono">{info?.commit ?? '…'}</dd>
				</div>
				<div>
					<dt>{t('instance.protocol')}</dt>
					<dd class="mono">{info?.protocol ?? '…'}</dd>
				</div>
				<div>
					<dt>{t('instance.publicUrl')}</dt>
					<dd class="url">
						{#if info}
							<span class="mono">{info.public_url}</span>
							<span class={['tag', info.public_url_configured ? 'ok' : 'warn']}>
								<Rich
									text={info.public_url_configured
										? t('instance.publicUrlConfigured')
										: t('instance.publicUrlMissing')}
								/>
							</span>
							<CopyButton text={info.public_url} />
						{:else}…{/if}
					</dd>
				</div>
				<div>
					<dt>{t('instance.dataDir')}</dt>
					<dd class="mono">{info?.data_dir ?? '…'}</dd>
				</div>
			</dl>
		</section>
	</div>
{/if}

<style>
	.page {
		display: grid;
		gap: 20px;
	}
	.stats {
		display: grid;
		grid-template-columns: repeat(4, minmax(0, 1fr));
	}
	.stat {
		display: grid;
		align-content: start;
		gap: 6px;
		padding: 14px 18px 16px;
	}
	.stat + .stat {
		border-left: 1px solid var(--border);
	}
	.stat dt {
		font-size: var(--text-xs);
		color: var(--fg-3);
	}
	.stat dd {
		display: flex;
		align-items: center;
		gap: 10px;
		font-size: 22px;
		font-weight: 500;
		letter-spacing: -0.02em;
		line-height: 1.1;
		min-height: 24px;
	}
	.sub {
		font-size: var(--text-xs);
		color: var(--fg-3);
	}
	.live {
		width: 7px;
		height: 7px;
		border-radius: 50%;
		background: var(--green);
		box-shadow: 0 0 0 3px var(--green-soft);
	}
	.details {
		overflow: hidden;
	}
	.details h2 {
		padding: 14px 18px;
		font-size: var(--text-sm);
		font-weight: 600;
		border-bottom: 1px solid var(--border);
	}
	.details dl div {
		display: grid;
		grid-template-columns: 180px minmax(0, 1fr);
		gap: 16px;
		align-items: center;
		min-height: 44px;
		padding: 8px 18px;
		font-size: var(--text-sm);
	}
	.details dl div + div {
		border-top: 1px solid var(--border);
	}
	.details dt {
		color: var(--fg-2);
	}
	.details dd {
		min-width: 0;
		overflow-wrap: anywhere;
	}
	.details dd.mono {
		font-size: 12.5px;
	}
	.url {
		display: flex;
		align-items: center;
		flex-wrap: wrap;
		gap: 4px 10px;
	}
	.url .mono {
		font-size: 12.5px;
	}
	.tag {
		font-size: var(--text-2xs);
		padding: 2px 7px;
		border-radius: 5px;
	}
	.tag.ok {
		background: var(--green-soft);
		color: var(--green);
	}
	.tag.warn {
		background: var(--amber-soft);
		color: var(--amber);
	}
	@media (max-width: 860px) {
		.stats {
			grid-template-columns: repeat(2, minmax(0, 1fr));
		}
		.stat:nth-child(3) {
			border-left: 0;
		}
		.stat:nth-child(n + 3) {
			border-top: 1px solid var(--border);
		}
		.details dl div {
			grid-template-columns: minmax(0, 1fr);
			gap: 2px;
			padding: 10px 16px;
		}
	}
</style>
