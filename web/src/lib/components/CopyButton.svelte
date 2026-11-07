<script lang="ts">
	import { copyText } from '#lib/clipboard.ts';
	import { t } from '#lib/i18n/index.svelte.ts';
	import { toasts } from '#lib/state/toasts.svelte.ts';
	import Icon from './Icon.svelte';

	let {
		text,
		label,
		showLabel = false,
		size = 'sm',
		variant = 'ghost'
	}: {
		text: string;
		label?: string;
		showLabel?: boolean;
		size?: 'sm' | 'md';
		variant?: 'ghost' | 'default' | 'primary';
	} = $props();

	let copied = $state(false);
	let timer: ReturnType<typeof setTimeout> | undefined;

	async function copy() {
		if (await copyText(text)) {
			copied = true;
			clearTimeout(timer);
			timer = setTimeout(() => (copied = false), 1600);
		} else {
			toasts.error(t('common.copyFailed'));
		}
	}
</script>

<button
	type="button"
	class={[
		'btn copy',
		size === 'sm' && 'btn-sm',
		variant === 'ghost' && 'btn-ghost',
		variant === 'primary' && 'btn-primary',
		!showLabel && 'btn-icon',
		copied && 'copied'
	]}
	onclick={copy}
	aria-label={showLabel ? undefined : (label ?? t('common.copy'))}
	title={showLabel ? undefined : (label ?? t('common.copy'))}
>
	<span class="swap" aria-hidden="true">
		<span class={['ic', !copied && 'show']}><Icon name="copy" size={14} /></span>
		<span class={['ic', copied && 'show']}><Icon name="check" size={14} strokeWidth={1.8} /></span>
	</span>
	{#if showLabel}
		<span>{copied ? t('common.copied') : (label ?? t('common.copy'))}</span>
	{/if}
	<span class="sr-only" aria-live="polite">{copied ? t('common.copied') : ''}</span>
</button>

<style>
	.swap {
		position: relative;
		width: 14px;
		height: 14px;
		display: inline-block;
	}
	.ic {
		position: absolute;
		inset: 0;
		opacity: 0;
		transform: scale(0.6);
		transition:
			opacity var(--dur-2) var(--ease),
			transform var(--dur-2) var(--ease-out);
	}
	.ic.show {
		opacity: 1;
		transform: none;
	}
	.copied .ic.show {
		color: var(--green);
	}
	.btn-primary.copied .ic.show {
		color: inherit;
	}
</style>
