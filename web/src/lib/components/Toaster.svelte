<script lang="ts">
	import { flip } from 'svelte/animate';
	import { fly } from 'svelte/transition';
	import { toasts } from '#lib/state/toasts.svelte.ts';
	import { t } from '#lib/i18n/index.svelte.ts';
	import Icon from './Icon.svelte';

	let el: HTMLDivElement | undefined = $state();

	// Re-enter the top layer on each new toast so it shows above open dialogs.
	$effect(() => {
		const count = toasts.items.length;
		if (!el) return;
		if (el.matches(':popover-open')) el.hidePopover();
		if (count > 0) el.showPopover();
	});
</script>

<div bind:this={el} popover="manual" class="toaster" aria-live="polite" aria-relevant="additions">
	{#each toasts.items as toast (toast.id)}
		<div
			class={['toast', toast.kind]}
			role={toast.kind === 'error' ? 'alert' : 'status'}
			animate:flip={{ duration: 200 }}
			in:fly={{ y: 8, duration: 220 }}
			out:fly={{ x: 12, duration: 160 }}
		>
			<span class="ic">
				<Icon
					name={toast.kind === 'error' ? 'alert' : toast.kind === 'info' ? 'info' : 'check'}
					size={14}
					strokeWidth={1.8}
				/>
			</span>
			<span class="msg">{toast.message}</span>
			<button
				type="button"
				class="x"
				aria-label={t('common.close')}
				onclick={() => toasts.dismiss(toast.id)}
			>
				<Icon name="x" size={12} />
			</button>
		</div>
	{/each}
</div>

<style>
	.toaster {
		position: fixed;
		inset: auto 16px 16px auto;
		margin: 0;
		padding: 0;
		border: 0;
		background: transparent;
		overflow: visible;
		right: 16px;
		bottom: 16px;
		z-index: 100;
		display: flex;
		flex-direction: column;
		align-items: flex-end;
		gap: 8px;
		pointer-events: none;
	}
	.toast {
		pointer-events: auto;
		display: flex;
		align-items: center;
		gap: 9px;
		max-width: min(380px, calc(100vw - 32px));
		padding: 9px 9px 9px 12px;
		border-radius: var(--radius-lg);
		background: var(--surface);
		box-shadow: var(--shadow-pop);
		font-size: var(--text-sm);
	}
	.ic {
		display: grid;
		place-items: center;
		width: 18px;
		height: 18px;
		border-radius: 50%;
		flex: none;
	}
	.success .ic {
		color: var(--green);
		background: var(--green-soft);
	}
	.error .ic {
		color: var(--red);
		background: var(--red-soft);
	}
	.info .ic {
		color: var(--accent-text);
		background: var(--accent-soft);
	}
	.msg {
		flex: 1;
		min-width: 0;
		overflow-wrap: anywhere;
	}
	.x {
		display: grid;
		place-items: center;
		width: 20px;
		height: 20px;
		border-radius: 5px;
		color: var(--fg-3);
		flex: none;
	}
	.x:hover {
		background: var(--hover);
		color: var(--fg);
	}
	@media (max-width: 640px) {
		.toaster {
			inset: auto 12px 12px 12px;
			left: 12px;
			right: 12px;
			bottom: 12px;
			align-items: stretch;
		}
		.toast {
			max-width: none;
		}
	}
</style>
