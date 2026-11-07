<script lang="ts">
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { api } from '#lib/api.ts';
	import { errorMessage } from '#lib/format.ts';
	import { t } from '#lib/i18n/index.svelte.ts';
	import { app } from '#lib/state/app.svelte.ts';
	import { toasts } from '#lib/state/toasts.svelte.ts';
	import { ui } from '#lib/state/ui.svelte.ts';
	import Dialog from './Dialog.svelte';

	const id = $props.id();
	let name = $state('');
	let error = $state('');
	let busy = $state(false);

	function reset() {
		name = '';
		error = '';
	}

	async function submit(e: SubmitEvent) {
		e.preventDefault();
		const value = name.trim();
		if (!value || value.length > 64) {
			error = t('lane.create.nameRequired');
			return;
		}
		busy = true;
		error = '';
		try {
			const lane = await api.createLane(value);
			ui.createLaneOpen = false;
			toasts.show(t('lane.created', { name: lane.name }));
			void app.refreshLanes().catch(() => {});
			await goto(resolve('/(app)/lanes/[lane]/setup', { lane: lane.id }));
		} catch (err) {
			error = errorMessage(err);
		} finally {
			busy = false;
		}
	}
</script>

<Dialog
	bind:open={ui.createLaneOpen}
	title={t('lane.create.title')}
	description={t('lane.create.desc')}
	width={420}
	onclose={reset}
>
	<form id="{id}-form" onsubmit={submit} novalidate>
		<div class="field">
			<label class="label" for="{id}-name">{t('common.name')}</label>
			<!-- svelte-ignore a11y_autofocus -->
			<input
				id="{id}-name"
				class="input"
				bind:value={name}
				maxlength="64"
				placeholder={t('lane.create.placeholder')}
				autocomplete="off"
				autofocus
				aria-invalid={error ? 'true' : undefined}
				aria-describedby={error ? `${id}-err` : undefined}
			/>
			{#if error}<p id="{id}-err" class="error-text" role="alert">{error}</p>{/if}
		</div>
	</form>
	{#snippet footer()}
		<button type="button" class="btn" onclick={() => (ui.createLaneOpen = false)}>
			{t('common.cancel')}
		</button>
		<button type="submit" form="{id}-form" class="btn btn-primary" disabled={busy}>
			{busy ? t('common.creating') : t('common.create')}
		</button>
	{/snippet}
</Dialog>
