<script lang="ts">
	import { errorMessage } from '#lib/format.ts';
	import { t, tn } from '#lib/i18n/index.svelte.ts';
	import { lane } from '#lib/state/lane.svelte.ts';
	import { toasts } from '#lib/state/toasts.svelte.ts';
	import type { CreatedKey, KeyKind } from '#lib/types.ts';
	import ClientSnippets from './ClientSnippets.svelte';
	import CopyButton from './CopyButton.svelte';
	import Dialog from './Dialog.svelte';
	import Icon from './Icon.svelte';
	import InstallSnippets from './InstallSnippets.svelte';

	let { open = $bindable(false), kind }: { open?: boolean; kind: KeyKind } = $props();

	const id = $props.id();
	let name = $state('');
	let days = $state('0');
	let error = $state('');
	let busy = $state(false);
	let created = $state.raw<CreatedKey | null>(null);

	const expiryOptions = [0, 1, 7, 30, 90, 365];

	function reset() {
		name = '';
		days = '0';
		error = '';
		created = null;
	}

	async function submit(e: SubmitEvent) {
		e.preventDefault();
		const value = name.trim();
		if (!value || value.length > 64) {
			error = t('keys.create.nameRequired');
			return;
		}
		busy = true;
		error = '';
		try {
			created = await lane.createKey(kind, value, Number(days) || undefined);
			toasts.show(t('keys.created', { name: created.key.name }));
		} catch (err) {
			error = errorMessage(err);
		} finally {
			busy = false;
		}
	}
</script>

<Dialog
	bind:open
	title={created
		? t('keys.reveal.title')
		: t(kind === 'join' ? 'keys.create.title.join' : 'keys.create.title.access')}
	description={created ? undefined : t(kind === 'join' ? 'keys.join.desc' : 'keys.access.desc')}
	width={created ? 600 : 440}
	dismissable={!created}
	onclose={reset}
>
	{#if created}
		<div class="reveal fade-in">
			<div class="token">
				<code class="mono">{created.token}</code>
				<CopyButton text={created.token} variant="primary" showLabel size="sm" />
			</div>
			<p class="warning">
				<Icon name="alert" size={14} />
				{t('keys.reveal.warning')}
			</p>
			<div class="next">
				<h3>{t(kind === 'join' ? 'keys.reveal.next.join' : 'keys.reveal.next.access')}</h3>
				{#if kind === 'join'}
					<InstallSnippets laneId={lane.id} token={created.token} />
				{:else}
					<ClientSnippets laneId={lane.id} token={created.token} />
				{/if}
			</div>
		</div>
	{:else}
		<form id="{id}-form" class="form" onsubmit={submit} novalidate>
			<div class="field">
				<label class="label" for="{id}-name">{t('keys.create.nameLabel')}</label>
				<!-- svelte-ignore a11y_autofocus -->
				<input
					id="{id}-name"
					class="input"
					bind:value={name}
					maxlength="64"
					autocomplete="off"
					spellcheck="false"
					autofocus
					placeholder={t(
						kind === 'join'
							? 'keys.create.namePlaceholder.join'
							: 'keys.create.namePlaceholder.access'
					)}
					aria-invalid={error ? 'true' : undefined}
					aria-describedby="{id}-hint"
				/>
				<p id="{id}-hint" class="hint">{t('keys.create.nameHint')}</p>
			</div>
			<div class="field">
				<label class="label" for="{id}-exp">{t('keys.create.expiry')}</label>
				<select id="{id}-exp" class="select" bind:value={days}>
					{#each expiryOptions as d (d)}
						<option value={String(d)}>
							{d === 0 ? t('keys.create.expiryNever') : tn('keys.create.expiryDays', d)}
						</option>
					{/each}
				</select>
			</div>
			{#if error}<p class="error-text" role="alert">{error}</p>{/if}
		</form>
	{/if}

	{#snippet footer()}
		{#if created}
			<button type="button" class="btn btn-primary" onclick={() => (open = false)}>
				{t('common.done')}
			</button>
		{:else}
			<button type="button" class="btn" onclick={() => (open = false)}>{t('common.cancel')}</button>
			<button type="submit" form="{id}-form" class="btn btn-primary" disabled={busy}>
				{busy ? t('common.creating') : t('common.create')}
			</button>
		{/if}
	{/snippet}
</Dialog>

<style>
	.form {
		display: grid;
		gap: 16px;
	}
	.reveal {
		display: grid;
		gap: 12px;
		min-width: 0;
	}
	.token {
		display: flex;
		align-items: center;
		gap: 10px;
		padding: 10px 10px 10px 14px;
		border-radius: var(--radius);
		border: 1px solid var(--border-strong);
		background: var(--code-bg);
		min-width: 0;
	}
	.token code {
		flex: 1;
		min-width: 0;
		font-size: 13px;
		word-break: break-all;
		user-select: all;
	}
	.warning {
		display: flex;
		align-items: center;
		gap: 8px;
		font-size: var(--text-xs);
		color: var(--amber);
	}
	.next {
		display: grid;
		gap: 10px;
		margin-top: 8px;
		padding-top: 16px;
		border-top: 1px solid var(--border);
		min-width: 0;
	}
	h3 {
		font-size: var(--text-sm);
		font-weight: 500;
	}
</style>
