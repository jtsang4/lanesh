<script lang="ts">
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { api } from '#lib/api.ts';
	import { errorMessage } from '#lib/format.ts';
	import { t } from '#lib/i18n/index.svelte.ts';
	import { lane } from '#lib/state/lane.svelte.ts';
	import { toasts } from '#lib/state/toasts.svelte.ts';
	import Dialog from './Dialog.svelte';

	let { open = $bindable(false) }: { open?: boolean } = $props();

	const id = $props.id();
	let name = $state('');
	let summary = $state('');
	let error = $state('');
	let busy = $state(false);

	function reset() {
		name = '';
		summary = '';
		error = '';
	}

	async function submit(e: SubmitEvent) {
		e.preventDefault();
		const value = name.trim();
		if (!value || [...value].length > 64) {
			error = t('projects.create.nameRequired');
			return;
		}
		busy = true;
		error = '';
		try {
			// A starting context the user and agents are free to rewrite.
			const context = t('projects.create.template', {
				name: value,
				summary: summary.trim()
			}).replace(/\n{3,}/g, '\n\n');
			const project = await api.createProject(lane.id, { name: value, context });
			open = false;
			toasts.show(t('projects.created', { name: project.name }));
			void lane.loadProjects();
			await goto(
				resolve('/(app)/lanes/[lane]/projects/[project]', { lane: lane.id, project: project.id })
			);
		} catch (err) {
			error = errorMessage(err);
		} finally {
			busy = false;
		}
	}
</script>

<Dialog
	bind:open
	title={t('projects.create.title')}
	description={t('projects.create.desc')}
	width={460}
	onclose={reset}
>
	<form id="{id}-form" class="form" onsubmit={submit} novalidate>
		<div class="field">
			<label class="label" for="{id}-name">{t('common.name')}</label>
			<!-- svelte-ignore a11y_autofocus -->
			<input
				id="{id}-name"
				class="input"
				bind:value={name}
				maxlength="64"
				placeholder={t('projects.create.namePlaceholder')}
				autocomplete="off"
				spellcheck="false"
				autofocus
				aria-invalid={error ? 'true' : undefined}
				aria-describedby={error ? `${id}-err` : undefined}
			/>
			{#if error}<p id="{id}-err" class="error-text" role="alert">{error}</p>{/if}
		</div>
		<div class="field">
			<label class="label" for="{id}-summary">{t('projects.create.oneLine')}</label>
			<input id="{id}-summary" class="input" bind:value={summary} autocomplete="off" />
			<p class="hint">{t('projects.create.oneLineHint')}</p>
		</div>
	</form>
	{#snippet footer()}
		<button type="button" class="btn" onclick={() => (open = false)}>{t('common.cancel')}</button>
		<button type="submit" form="{id}-form" class="btn btn-primary" disabled={busy}>
			{busy ? t('common.creating') : t('common.create')}
		</button>
	{/snippet}
</Dialog>

<style>
	.form {
		display: grid;
		gap: 16px;
	}
	.hint {
		margin-top: 6px;
		font-size: var(--text-xs);
		color: var(--fg-3);
	}
</style>
