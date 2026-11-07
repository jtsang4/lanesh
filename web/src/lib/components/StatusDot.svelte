<script lang="ts">
	import { t } from '#lib/i18n/index.svelte.ts';

	let {
		online,
		pulse = false,
		label = true
	}: { online: boolean; pulse?: boolean; label?: boolean } = $props();
</script>

<span
	class={['dot', online ? 'on' : 'off', pulse && online && 'pulse']}
	role={label ? 'img' : undefined}
	aria-label={label ? (online ? t('common.online') : t('common.offline')) : undefined}
	aria-hidden={label ? undefined : 'true'}
></span>

<style>
	.dot {
		position: relative;
		display: inline-block;
		flex: none;
		width: 7px;
		height: 7px;
		border-radius: 50%;
		transition: background-color var(--dur-3) var(--ease);
	}
	.on {
		background: var(--green);
	}
	.off {
		background: transparent;
		box-shadow: inset 0 0 0 1.5px var(--fg-4);
	}
	.pulse::after {
		content: '';
		position: absolute;
		inset: 0;
		border-radius: 50%;
		background: var(--green);
		animation: ping 2.4s var(--ease-out) infinite;
	}
	@keyframes ping {
		0% {
			transform: scale(1);
			opacity: 0.5;
		}
		70%,
		100% {
			transform: scale(2.6);
			opacity: 0;
		}
	}
</style>
