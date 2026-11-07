<script lang="ts">
	import { afterNavigate } from '$app/navigation';
	import { resolve } from '$app/paths';
	import CreateLaneDialog from '#lib/components/CreateLaneDialog.svelte';
	import Icon from '#lib/components/Icon.svelte';
	import Logo from '#lib/components/Logo.svelte';
	import Sidebar from '#lib/components/Sidebar.svelte';
	import { t } from '#lib/i18n/index.svelte.ts';
	import { app } from '#lib/state/app.svelte.ts';
	import { poll } from '#lib/state/poll.ts';
	import { ui } from '#lib/state/ui.svelte.ts';
	import type { LayoutProps } from './$types';

	let { data, children }: LayoutProps = $props();

	let drawer: HTMLDialogElement | undefined = $state();

	$effect(() => {
		void app.refreshLanes().catch(() => {});
		void app.refreshInfo().catch(() => {});
		return poll(() => app.refreshLanes(), 10_000);
	});

	$effect(() => {
		if (!drawer) return;
		if (ui.navOpen && !drawer.open) drawer.showModal();
		else if (!ui.navOpen && drawer.open) drawer.close();
	});

	afterNavigate(() => {
		ui.navOpen = false;
	});
</script>

<a class="skip" href="#content">{t('nav.skip')}</a>

<div class="shell">
	<aside class="sidebar">
		<Sidebar version={data.version} />
	</aside>

	<header class="topbar">
		<a class="brand" href={resolve('/(app)')}>
			<Logo size={20} />
			<span>Lanesh</span>
		</a>
		<button
			type="button"
			class="btn btn-ghost btn-icon"
			aria-label={t('nav.menu')}
			aria-expanded={ui.navOpen}
			onclick={() => (ui.navOpen = true)}
		>
			<Icon name="menu" size={18} />
		</button>
	</header>

	<main id="content" tabindex="-1">
		{@render children()}
	</main>
</div>

<dialog
	bind:this={drawer}
	class="drawer"
	aria-label={t('nav.mainNav')}
	onclose={() => (ui.navOpen = false)}
	onclick={(e) => {
		if (e.target === drawer) ui.navOpen = false;
	}}
>
	<div class="drawer-panel">
		<Sidebar version={data.version} />
	</div>
</dialog>

<CreateLaneDialog />

<style>
	.skip {
		position: fixed;
		top: 8px;
		left: 8px;
		z-index: 200;
		padding: 6px 10px;
		border-radius: var(--radius);
		background: var(--primary);
		color: var(--primary-fg);
		font-size: var(--text-sm);
		transform: translateY(-200%);
	}
	.skip:focus {
		transform: none;
	}
	.shell {
		display: grid;
		grid-template-columns: var(--sidebar-w) minmax(0, 1fr);
		min-height: 100dvh;
	}
	.sidebar {
		position: sticky;
		top: 0;
		height: 100dvh;
		background: var(--bg-sidebar);
		border-right: 1px solid var(--border);
	}
	.topbar {
		display: none;
	}
	main {
		min-width: 0;
		width: 100%;
		max-width: calc(var(--content-max) + 2 * 48px);
		margin: 0 auto;
		padding: 28px 48px 80px;
		outline: none;
	}
	.drawer {
		margin: 0;
		padding: 0;
		border: 0;
		width: min(300px, 86vw);
		max-width: none;
		height: 100dvh;
		max-height: none;
		background: var(--bg-sidebar);
		color: var(--fg);
		box-shadow: var(--shadow-dialog);
		transform: translateX(-100%);
		transition:
			transform var(--dur-3) var(--ease-out),
			overlay var(--dur-3) allow-discrete,
			display var(--dur-3) allow-discrete;
	}
	.drawer[open] {
		transform: none;
	}
	@starting-style {
		.drawer[open] {
			transform: translateX(-100%);
		}
	}
	.drawer::backdrop {
		background: var(--backdrop);
		opacity: 0;
		transition:
			opacity var(--dur-3) var(--ease),
			overlay var(--dur-3) allow-discrete,
			display var(--dur-3) allow-discrete;
	}
	.drawer[open]::backdrop {
		opacity: 1;
	}
	@starting-style {
		.drawer[open]::backdrop {
			opacity: 0;
		}
	}
	.drawer-panel {
		height: 100%;
	}

	@media (max-width: 1100px) {
		main {
			padding: 24px 32px 72px;
		}
	}

	@media (max-width: 860px) {
		.shell {
			grid-template-columns: minmax(0, 1fr);
			grid-template-rows: auto 1fr;
			align-content: start;
		}
		.sidebar {
			display: none;
		}
		.topbar {
			position: sticky;
			top: 0;
			z-index: 20;
			display: flex;
			align-items: center;
			justify-content: space-between;
			height: 52px;
			padding: 0 10px 0 16px;
			background: color-mix(in srgb, var(--bg) 86%, transparent);
			backdrop-filter: saturate(1.4) blur(10px);
			border-bottom: 1px solid var(--border);
		}
		.brand {
			display: flex;
			align-items: center;
			gap: 9px;
			font-weight: 600;
			font-size: 15px;
			letter-spacing: -0.02em;
		}
		main {
			padding: 20px 16px 64px;
		}
	}
</style>
