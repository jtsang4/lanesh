<script lang="ts">
	import { errorMessage } from '#lib/format.ts';
	import { t } from '#lib/i18n/index.svelte.ts';
	import Dialog from './Dialog.svelte';

	let {
		open = $bindable(false),
		title,
		body,
		confirmLabel,
		confirmText,
		confirmPrompt,
		onconfirm
	}: {
		open?: boolean;
		title: string;
		body: string;
		confirmLabel: string;
		/** When set, the user must type this text before confirming. */
		confirmText?: string;
		confirmPrompt?: string;
		onconfirm: () => Promise<void>;
	} = $props();

	const id = $props.id();
	let typed = $state('');
	let busy = $state(false);
	let error = $state('');

	const ready = $derived(!confirmText || typed.trim() === confirmText);

	function reset() {
		typed = '';
		error = '';
	}

	async function submit(e: SubmitEvent) {
		e.preventDefault();
		if (!ready || busy) return;
		busy = true;
		error = '';
		try {
			await onconfirm();
			open = false;
		} catch (err) {
			error = errorMessage(err);
		} finally {
			busy = false;
		}
	}
</script>

<Dialog bind:open {title} width={420} onclose={reset}>
	<form id="{id}-form" class="form" onsubmit={submit}>
		<p class="body">{body}</p>
		{#if confirmText}
			<div class="field">
				<label class="label" for="{id}-typed">{confirmPrompt}</label>
				<!-- svelte-ignore a11y_autofocus -->
				<input
					autofocus
					id="{id}-typed"
					class="input mono"
					bind:value={typed}
					autocomplete="off"
					spellcheck="false"
				/>
			</div>
		{/if}
		{#if error}<p class="error-text" role="alert">{error}</p>{/if}
	</form>
	{#snippet footer()}
		<button type="button" class="btn" onclick={() => (open = false)}>{t('common.cancel')}</button>
		<button type="submit" form="{id}-form" class="btn btn-danger" disabled={!ready || busy}>
			{confirmLabel}
		</button>
	{/snippet}
</Dialog>

<style>
	.form {
		display: grid;
		gap: var(--s-4);
	}
	.body {
		font-size: var(--text-sm);
		color: var(--fg-2);
		line-height: 1.55;
	}
</style>
