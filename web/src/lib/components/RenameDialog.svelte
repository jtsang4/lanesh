<script lang="ts">
	import { errorMessage } from '#lib/format.ts';
	import { t } from '#lib/i18n/index.svelte.ts';
	import Dialog from './Dialog.svelte';

	let {
		open = $bindable(false),
		title,
		description,
		label,
		initial,
		required,
		onsave
	}: {
		open?: boolean;
		title: string;
		description?: string;
		label: string;
		initial: string;
		required: string;
		onsave: (name: string) => Promise<void>;
	} = $props();

	const id = $props.id();
	let value = $state('');
	let error = $state('');
	let busy = $state(false);
	let input: HTMLInputElement | undefined = $state();

	// Seed the field each time the dialog opens.
	$effect(() => {
		if (open) {
			value = initial;
			error = '';
			queueMicrotask(() => input?.select());
		}
	});

	async function submit(e: SubmitEvent) {
		e.preventDefault();
		const name = value.trim();
		if (!name || name.length > 64) {
			error = required;
			return;
		}
		if (name === initial) {
			open = false;
			return;
		}
		busy = true;
		error = '';
		try {
			await onsave(name);
			open = false;
		} catch (err) {
			error = errorMessage(err);
		} finally {
			busy = false;
		}
	}
</script>

<Dialog bind:open {title} {description} width={420}>
	<form id="{id}-form" onsubmit={submit} novalidate>
		<div class="field">
			<label class="label" for="{id}-input">{label}</label>
			<input
				bind:this={input}
				id="{id}-input"
				class="input"
				bind:value
				maxlength="64"
				autocomplete="off"
				spellcheck="false"
				aria-invalid={error ? 'true' : undefined}
				aria-describedby={error ? `${id}-err` : undefined}
			/>
			{#if error}<p id="{id}-err" class="error-text" role="alert">{error}</p>{/if}
		</div>
	</form>
	{#snippet footer()}
		<button type="button" class="btn" onclick={() => (open = false)}>{t('common.cancel')}</button>
		<button type="submit" form="{id}-form" class="btn btn-primary" disabled={busy}>
			{busy ? t('common.saving') : t('common.save')}
		</button>
	{/snippet}
</Dialog>
