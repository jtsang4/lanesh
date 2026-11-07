<script lang="ts">
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { api } from '#lib/api.ts';
	import ConfirmDialog from '#lib/components/ConfirmDialog.svelte';
	import CopyButton from '#lib/components/CopyButton.svelte';
	import { errorMessage } from '#lib/format.ts';
	import { t, tn } from '#lib/i18n/index.svelte.ts';
	import { app } from '#lib/state/app.svelte.ts';
	import { lane } from '#lib/state/lane.svelte.ts';
	import { toasts } from '#lib/state/toasts.svelte.ts';
	import type { PageProps } from './$types';

	let { params }: PageProps = $props();

	let name = $state('');
	let seeded = $state('');
	let busy = $state(false);
	let error = $state('');
	let deleting = $state(false);

	// Seed the form from the loaded lane, and again if it's renamed elsewhere.
	$effect(() => {
		const current = lane.lane?.name ?? '';
		if (current !== seeded) {
			seeded = current;
			name = current;
		}
	});

	const dirty = $derived(name.trim() !== (lane.lane?.name ?? '') && name.trim() !== '');

	async function save(e: SubmitEvent) {
		e.preventDefault();
		const value = name.trim();
		if (!value || value.length > 64) {
			error = t('lane.create.nameRequired');
			return;
		}
		busy = true;
		error = '';
		try {
			await lane.rename(value);
			toasts.show(t('settings.renamed'));
		} catch (err) {
			error = errorMessage(err);
		} finally {
			busy = false;
		}
	}
</script>

<svelte:head>
	<title>{lane.lane?.name ?? 'Lane'} · {t('lane.tabs.settings')} · Lanesh</title>
</svelte:head>

<div class="page">
	<section class="block" aria-labelledby="general">
		<h2 id="general" class="section-title">{t('settings.general')}</h2>
		<div class="card panel">
			<form class="row" onsubmit={save} novalidate>
				<div class="field grow">
					<label class="label" for="lane-name">{t('settings.nameLabel')}</label>
					<div class="inline">
						<input
							id="lane-name"
							class="input"
							bind:value={name}
							maxlength="64"
							autocomplete="off"
							disabled={!lane.lane}
							aria-invalid={error ? 'true' : undefined}
						/>
						<button type="submit" class="btn btn-primary" disabled={!dirty || busy}>
							{busy ? t('common.saving') : t('common.save')}
						</button>
					</div>
					{#if error}<p class="error-text" role="alert">{error}</p>{/if}
				</div>
			</form>
			<div class="row id-row">
				<div class="field grow">
					<span class="label">{t('settings.idLabel')}</span>
					<div class="id">
						<code class="mono">{params.lane}</code>
						<CopyButton text={params.lane} />
					</div>
					<p class="hint">{t('settings.idHint')}</p>
				</div>
			</div>
		</div>
	</section>

	<section class="block" aria-labelledby="danger">
		<h2 id="danger" class="section-title danger-title">{t('settings.danger')}</h2>
		<div class="card panel danger">
			<div class="row">
				<div class="grow">
					<p class="d-title">{t('settings.delete.title')}</p>
					<p class="d-desc">{t('settings.delete.desc')}</p>
				</div>
				<button
					type="button"
					class="btn btn-danger"
					disabled={!lane.lane}
					onclick={() => (deleting = true)}
				>
					{t('settings.delete.button')}
				</button>
			</div>
		</div>
	</section>
</div>

{#if lane.lane}
	{@const l = lane.lane}
	<ConfirmDialog
		bind:open={deleting}
		title={t('settings.delete.dialogTitle', { name: l.name })}
		body={t('settings.delete.dialogBody', { machines: tn('machines.count', l.machine_count) })}
		confirmLabel={t('settings.delete.confirm')}
		confirmText={l.name}
		confirmPrompt={t('settings.delete.typeToConfirm', { name: l.name })}
		onconfirm={async () => {
			await api.deleteLane(l.id);
			toasts.show(t('settings.deleted', { name: l.name }));
			await app.refreshLanes().catch(() => {});
			await goto(resolve('/(app)'));
		}}
	/>
{/if}

<style>
	.page {
		display: grid;
		gap: 36px;
		max-width: 760px;
	}
	.block {
		display: grid;
		gap: 12px;
	}
	.panel {
		overflow: hidden;
	}
	.row {
		display: flex;
		align-items: flex-end;
		gap: 16px;
		padding: 18px 20px;
	}
	.row + .row {
		border-top: 1px solid var(--border);
	}
	.grow {
		flex: 1;
		min-width: 0;
	}
	.inline {
		display: flex;
		gap: 8px;
		max-width: 480px;
	}
	.inline .input {
		flex: 1;
		min-width: 0;
	}
	.id {
		display: flex;
		align-items: center;
		gap: 6px;
	}
	.id code {
		font-size: var(--text-sm);
		color: var(--fg-2);
	}
	.danger-title {
		color: var(--red);
	}
	.danger {
		border-color: var(--red-border);
	}
	.danger .row {
		align-items: center;
	}
	.d-title {
		font-size: var(--text-sm);
		font-weight: 500;
	}
	.d-desc {
		margin-top: 2px;
		font-size: var(--text-sm);
		color: var(--fg-2);
		max-width: 60ch;
	}
	@media (max-width: 640px) {
		.row,
		.danger .row {
			flex-direction: column;
			align-items: stretch;
		}
		.row .btn {
			align-self: flex-start;
		}
	}
</style>
