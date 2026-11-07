<script lang="ts" module>
	import type { IconName } from './Icon.svelte';

	export interface MenuItem {
		label: string;
		icon?: IconName;
		danger?: boolean;
		href?: string;
		onselect?: () => void;
	}
</script>

<script lang="ts">
	import { t } from '#lib/i18n/index.svelte.ts';
	import Icon from './Icon.svelte';

	let { items, label }: { items: MenuItem[]; label?: string } = $props();

	const id = $props.id();
	let trigger: HTMLButtonElement | undefined = $state();
	let pop: HTMLDivElement | undefined = $state();
	let open = $state(false);

	function place() {
		if (!trigger || !pop) return;
		const r = trigger.getBoundingClientRect();
		const w = pop.offsetWidth;
		const h = pop.offsetHeight;
		const left = Math.max(8, Math.min(r.right - w, innerWidth - w - 8));
		const below = r.bottom + 4 + h < innerHeight - 8;
		pop.style.left = `${left}px`;
		pop.style.top = `${below ? r.bottom + 4 : r.top - h - 4}px`;
	}

	function ontoggle(e: ToggleEvent) {
		open = e.newState === 'open';
		if (open) {
			place();
			pop?.querySelector<HTMLElement>('[role=menuitem]')?.focus();
		}
	}

	function onkeydown(e: KeyboardEvent) {
		if (!pop) return;
		const els = [...pop.querySelectorAll<HTMLElement>('[role=menuitem]')];
		const i = els.indexOf(document.activeElement as HTMLElement);
		let next = -1;
		if (e.key === 'ArrowDown') next = (i + 1) % els.length;
		else if (e.key === 'ArrowUp') next = (i - 1 + els.length) % els.length;
		else if (e.key === 'Home') next = 0;
		else if (e.key === 'End') next = els.length - 1;
		else if (e.key === 'Tab') pop.hidePopover();
		if (next !== -1) {
			e.preventDefault();
			els[next].focus();
		}
	}

	function choose(item: MenuItem) {
		pop?.hidePopover();
		item.onselect?.();
	}
</script>

<button
	bind:this={trigger}
	type="button"
	class={['btn btn-ghost btn-icon btn-sm trigger', open && 'open']}
	popovertarget={id}
	aria-haspopup="menu"
	aria-expanded={open}
	aria-label={label ?? t('common.more')}
	title={label ?? t('common.more')}
>
	<Icon name="more" size={16} strokeWidth={2.2} />
</button>

<div
	bind:this={pop}
	{id}
	popover="auto"
	role="menu"
	tabindex="-1"
	class="menu"
	{ontoggle}
	{onkeydown}
>
	{#each items as item (item.label)}
		{#if item.href}
			<a
				role="menuitem"
				href={item.href}
				class={['item', item.danger && 'danger']}
				onclick={() => pop?.hidePopover()}
			>
				{#if item.icon}<Icon name={item.icon} size={14} />{/if}
				{item.label}
			</a>
		{:else}
			<button
				type="button"
				role="menuitem"
				class={['item', item.danger && 'danger']}
				onclick={() => choose(item)}
			>
				{#if item.icon}<Icon name={item.icon} size={14} />{/if}
				{item.label}
			</button>
		{/if}
	{/each}
</div>

<style>
	.trigger.open {
		background: var(--active);
		color: var(--fg);
	}
	.menu {
		position: fixed;
		inset: auto;
		margin: 0;
		min-width: 168px;
		padding: 4px;
		border: 0;
		border-radius: var(--radius-lg);
		background: var(--surface);
		color: var(--fg);
		box-shadow: var(--shadow-pop);
		opacity: 0;
		transform: translateY(-3px) scale(0.98);
		transform-origin: top right;
		transition:
			opacity var(--dur-1) var(--ease),
			transform var(--dur-2) var(--ease-out),
			overlay var(--dur-1) allow-discrete,
			display var(--dur-1) allow-discrete;
	}
	.menu:popover-open {
		opacity: 1;
		transform: none;
	}
	@starting-style {
		.menu:popover-open {
			opacity: 0;
			transform: translateY(-3px) scale(0.98);
		}
	}
	.item {
		display: flex;
		align-items: center;
		gap: 9px;
		width: 100%;
		height: 30px;
		padding: 0 9px;
		border-radius: 6px;
		font-size: var(--text-sm);
		color: var(--fg);
		text-align: left;
		white-space: nowrap;
	}
	.item :global(svg) {
		color: var(--fg-3);
	}
	.item:hover,
	.item:focus-visible {
		background: var(--hover);
		outline: none;
	}
	.danger {
		color: var(--red);
	}
	.danger :global(svg) {
		color: var(--red);
	}
	.danger:hover,
	.danger:focus-visible {
		background: var(--red-soft);
	}
</style>
