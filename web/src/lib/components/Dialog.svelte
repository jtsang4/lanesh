<script lang="ts">
	import type { Snippet } from 'svelte';
	import { t } from '#lib/i18n/index.svelte.ts';
	import Icon from './Icon.svelte';

	let {
		open = $bindable(false),
		title,
		description,
		width = 440,
		dismissable = true,
		children,
		footer,
		onclose
	}: {
		open?: boolean;
		title: string;
		description?: string;
		width?: number;
		dismissable?: boolean;
		children?: Snippet;
		footer?: Snippet;
		onclose?: () => void;
	} = $props();

	const id = $props.id();
	let el: HTMLDialogElement | undefined = $state();
	let downOnBackdrop = false;

	$effect(() => {
		if (!el) return;
		if (open && !el.open) el.showModal();
		else if (!open && el.open) el.close();
	});

	function handleClose() {
		open = false;
		onclose?.();
	}

	function oncancel(e: Event) {
		if (!dismissable) e.preventDefault();
	}
</script>

<dialog
	bind:this={el}
	style:--w="{width}px"
	aria-labelledby="{id}-title"
	aria-describedby={description ? `${id}-desc` : undefined}
	onclose={handleClose}
	{oncancel}
	onpointerdown={(e) => (downOnBackdrop = e.target === el)}
	onclick={(e) => {
		if (dismissable && downOnBackdrop && e.target === el) open = false;
	}}
>
	<div class="panel">
		<header>
			<div class="titles">
				<h2 id="{id}-title">{title}</h2>
				{#if description}<p id="{id}-desc">{description}</p>{/if}
			</div>
			{#if dismissable}
				<button
					type="button"
					class="btn btn-ghost btn-icon btn-sm close"
					aria-label={t('common.close')}
					onclick={() => (open = false)}
				>
					<Icon name="x" size={14} />
				</button>
			{/if}
		</header>
		{#if children}<div class="body">{@render children()}</div>{/if}
		{#if footer}<footer>{@render footer()}</footer>{/if}
	</div>
</dialog>

<style>
	dialog {
		width: min(var(--w), calc(100vw - 24px));
		max-height: calc(100dvh - 48px);
		padding: 0;
		border: 0;
		border-radius: var(--radius-xl);
		background: var(--surface);
		color: var(--fg);
		box-shadow: var(--shadow-dialog);
		overflow: auto;
		opacity: 0;
		transform: translateY(6px) scale(0.985);
		transition:
			opacity var(--dur-2) var(--ease),
			transform var(--dur-3) var(--ease-out),
			overlay var(--dur-2) allow-discrete,
			display var(--dur-2) allow-discrete;
	}
	dialog[open] {
		opacity: 1;
		transform: none;
	}
	@starting-style {
		dialog[open] {
			opacity: 0;
			transform: translateY(6px) scale(0.985);
		}
	}
	dialog::backdrop {
		background: var(--backdrop);
		backdrop-filter: blur(2px);
		opacity: 0;
		transition:
			opacity var(--dur-2) var(--ease),
			overlay var(--dur-2) allow-discrete,
			display var(--dur-2) allow-discrete;
	}
	dialog[open]::backdrop {
		opacity: 1;
	}
	@starting-style {
		dialog[open]::backdrop {
			opacity: 0;
		}
	}
	.panel {
		display: flex;
		flex-direction: column;
	}
	header {
		display: flex;
		align-items: flex-start;
		gap: var(--s-3);
		padding: 18px 20px 0;
	}
	.titles {
		flex: 1;
		min-width: 0;
		display: grid;
		gap: 4px;
	}
	h2 {
		font-size: 15px;
		font-weight: 600;
		letter-spacing: -0.01em;
		line-height: 1.4;
		overflow-wrap: anywhere;
	}
	header p {
		font-size: var(--text-sm);
		color: var(--fg-2);
		line-height: 1.5;
	}
	.close {
		margin: -4px -6px 0 0;
	}
	.body {
		padding: 16px 20px 4px;
		display: grid;
		gap: var(--s-4);
		min-width: 0;
	}
	footer {
		display: flex;
		justify-content: flex-end;
		gap: var(--s-2);
		padding: 16px 20px 18px;
	}
	@media (max-width: 520px) {
		footer {
			flex-direction: column-reverse;
		}
		footer :global(.btn) {
			width: 100%;
			height: 34px;
		}
	}
</style>
