<script lang="ts">
	import Rich from '#lib/components/Rich.svelte';
	import { resolve } from '$app/paths';
	import Callout from '#lib/components/Callout.svelte';
	import ClientSnippets from '#lib/components/ClientSnippets.svelte';
	import CodeBlock from '#lib/components/CodeBlock.svelte';
	import CreateKeyDialog from '#lib/components/CreateKeyDialog.svelte';
	import Icon from '#lib/components/Icon.svelte';
	import InstallSnippets from '#lib/components/InstallSnippets.svelte';
	import StatusDot from '#lib/components/StatusDot.svelte';
	import { t } from '#lib/i18n/index.svelte.ts';
	import { app } from '#lib/state/app.svelte.ts';
	import { lane } from '#lib/state/lane.svelte.ts';
	import { mcpUrl } from '#lib/snippets.ts';
	import type { KeyKind } from '#lib/types.ts';
	import type { PageProps } from './$types';

	let { params }: PageProps = $props();

	let createKind = $state<KeyKind>('join');
	let creating = $state(false);

	const endpoint = $derived(mcpUrl(app.baseUrl, params.lane));
	const online = $derived(lane.machines?.filter((m) => m.online) ?? []);

	function create(kind: KeyKind) {
		createKind = kind;
		creating = true;
	}
</script>

<svelte:head>
	<title>{lane.lane?.name ?? 'Lane'} · {t('lane.tabs.connect')} · Lanesh</title>
</svelte:head>

{#snippet keyStep(kind: KeyKind)}
	{@const fresh = lane.fresh[kind]}
	{#if fresh}
		<p class="ready">
			<span class="ok"><Icon name="check" size={12} strokeWidth={2} /></span>
			{t(kind === 'join' ? 'connect.step.keyReady' : 'connect.step.keyReadyAccess', {
				name: fresh.name
			})}
		</p>
	{:else}
		<p class="step-desc">
			{t(kind === 'join' ? 'connect.step.keyDesc' : 'connect.step.accessKeyDesc')}
		</p>
		<div>
			<button type="button" class="btn btn-sm" onclick={() => create(kind)}>
				<Icon name="key" size={13} />
				{t(kind === 'join' ? 'connect.createJoinKey' : 'connect.createAccessKey')}
			</button>
		</div>
	{/if}
{/snippet}

<div class="page">
	{#if app.publicUrlMissing}
		<Callout tone="warn">
			<Rich text={t('connect.publicUrlWarning', { origin: app.baseUrl })} />
		</Callout>
	{/if}

	<section class="block" aria-labelledby="add-machine">
		<div class="intro">
			<h2 id="add-machine" class="section-title">{t('connect.machine.title')}</h2>
			<p class="section-desc">{t('connect.machine.desc')}</p>
		</div>
		<ol class="steps">
			<li>
				<span class="num" aria-hidden="true">1</span>
				<div class="content">
					<h3>{t('connect.step.key')}</h3>
					{@render keyStep('join')}
				</div>
			</li>
			<li>
				<span class="num" aria-hidden="true">2</span>
				<div class="content">
					<h3>{t('connect.step.install')}</h3>
					<p class="step-desc"><Rich text={t('connect.step.installDesc')} /></p>
					<InstallSnippets laneId={params.lane} token={lane.fresh.join?.token} />
				</div>
			</li>
			<li>
				<span class="num" aria-hidden="true">3</span>
				<div class="content">
					<h3>{t('connect.step.verify')}</h3>
					<p class="step-desc">{t('connect.step.verifyDesc')}</p>
					<a class="live" href={resolve('/(app)/lanes/[lane]', { lane: params.lane })}>
						<StatusDot online={online.length > 0} pulse label={false} />
						<span>
							{t('overview.onlineOf', {
								online: online.length,
								total: lane.machines?.length ?? 0
							})}
						</span>
						{#if online.length > 0}
							<span class="names">{online.map((m) => m.name).join(', ')}</span>
						{/if}
						<Icon name="arrowRight" size={13} />
					</a>
				</div>
			</li>
		</ol>
	</section>

	<section class="block" aria-labelledby="add-agent">
		<div class="intro">
			<h2 id="add-agent" class="section-title">{t('connect.agent.title')}</h2>
			<p class="section-desc">{t('connect.agent.desc')}</p>
		</div>
		<ol class="steps">
			<li>
				<span class="num" aria-hidden="true">1</span>
				<div class="content">
					<h3>{t('connect.step.accessKey')}</h3>
					{@render keyStep('access')}
				</div>
			</li>
			<li>
				<span class="num" aria-hidden="true">2</span>
				<div class="content">
					<h3>{t('connect.step.endpoint')}</h3>
					<p class="step-desc"><Rich text={t('connect.step.endpointDesc')} /></p>
					<CodeBlock code={endpoint} />
				</div>
			</li>
			<li>
				<span class="num" aria-hidden="true">3</span>
				<div class="content">
					<h3>{t('connect.step.client')}</h3>
					<ClientSnippets laneId={params.lane} token={lane.fresh.access?.token} />
				</div>
			</li>
		</ol>
	</section>
</div>

<CreateKeyDialog bind:open={creating} kind={createKind} />

<style>
	.page {
		display: grid;
		gap: 40px;
	}
	.block {
		display: grid;
		grid-template-columns: minmax(0, 280px) minmax(0, 1fr);
		gap: 40px;
	}
	.block + .block {
		padding-top: 40px;
		border-top: 1px solid var(--border);
	}
	.intro {
		display: grid;
		gap: 6px;
		align-content: start;
	}
	.steps {
		display: grid;
		gap: 0;
		min-width: 0;
	}
	.steps li {
		position: relative;
		display: grid;
		grid-template-columns: 24px minmax(0, 1fr);
		gap: 16px;
		padding-bottom: 28px;
	}
	.steps li:last-child {
		padding-bottom: 0;
	}
	.steps li:not(:last-child)::before {
		content: '';
		position: absolute;
		left: 11.5px;
		top: 30px;
		bottom: 6px;
		width: 1px;
		background: var(--border);
	}
	.num {
		display: grid;
		place-items: center;
		width: 24px;
		height: 24px;
		border-radius: 50%;
		border: 1px solid var(--border-strong);
		background: var(--surface);
		font-size: var(--text-xs);
		font-weight: 500;
		color: var(--fg-2);
		font-variant-numeric: tabular-nums;
	}
	.content {
		display: grid;
		gap: 8px;
		min-width: 0;
		padding-top: 2px;
	}
	h3 {
		font-size: var(--text-sm);
		font-weight: 500;
		line-height: 20px;
	}
	.step-desc {
		font-size: var(--text-sm);
		color: var(--fg-2);
		max-width: 70ch;
	}
	.ready {
		display: flex;
		align-items: center;
		gap: 8px;
		font-size: var(--text-sm);
		color: var(--fg-2);
	}
	.ok {
		display: grid;
		place-items: center;
		width: 18px;
		height: 18px;
		border-radius: 50%;
		background: var(--green-soft);
		color: var(--green);
		flex: none;
	}
	.live {
		display: inline-flex;
		align-items: center;
		gap: 10px;
		width: fit-content;
		max-width: 100%;
		height: 32px;
		padding: 0 12px;
		border-radius: var(--radius);
		border: 1px solid var(--border);
		background: var(--surface);
		font-size: var(--text-sm);
		color: var(--fg);
		transition: border-color var(--dur-1) var(--ease);
	}
	.live:hover {
		border-color: var(--border-strong);
	}
	.live :global(svg) {
		color: var(--fg-3);
	}
	.names {
		color: var(--fg-3);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		min-width: 0;
	}
	@media (max-width: 1000px) {
		.block {
			grid-template-columns: minmax(0, 1fr);
			gap: 20px;
		}
	}
</style>
