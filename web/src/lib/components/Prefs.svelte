<script lang="ts">
	import { i18n, locales, t, type Locale } from '#lib/i18n/index.svelte.ts';
	import { theme, type ThemePref } from '#lib/state/theme.svelte.ts';
	import Icon, { type IconName } from './Icon.svelte';

	let { compact = false }: { compact?: boolean } = $props();

	const themes: {
		id: ThemePref;
		icon: IconName;
		key: 'prefs.light' | 'prefs.system' | 'prefs.dark';
	}[] = [
		{ id: 'light', icon: 'sun', key: 'prefs.light' },
		{ id: 'system', icon: 'monitor', key: 'prefs.system' },
		{ id: 'dark', icon: 'moon', key: 'prefs.dark' }
	];

	const nextLocale = $derived<Locale>(i18n.locale === 'en' ? 'zh-CN' : 'en');
	let buttons: HTMLButtonElement[] = $state([]);

	function onkeydown(e: KeyboardEvent, i: number) {
		const d = e.key === 'ArrowRight' ? 1 : e.key === 'ArrowLeft' ? -1 : 0;
		if (!d) return;
		e.preventDefault();
		const n = (i + d + themes.length) % themes.length;
		theme.set(themes[n].id);
		buttons[n]?.focus();
	}
</script>

<div class={['prefs', compact && 'compact']}>
	<div class="theme" role="radiogroup" aria-label={t('prefs.theme')}>
		{#each themes as th, i (th.id)}
			<button
				bind:this={buttons[i]}
				type="button"
				role="radio"
				aria-checked={theme.pref === th.id}
				tabindex={theme.pref === th.id ? 0 : -1}
				aria-label={t(th.key)}
				title={t(th.key)}
				class={[theme.pref === th.id && 'on']}
				onclick={() => theme.set(th.id)}
				onkeydown={(e) => onkeydown(e, i)}
			>
				<Icon name={th.icon} size={14} />
			</button>
		{/each}
	</div>
	<button
		type="button"
		class="lang"
		onclick={() => i18n.set(nextLocale)}
		title={t('prefs.language')}
		aria-label="{t('prefs.language')}: {locales.find((l) => l.id === nextLocale)?.label}"
		lang={nextLocale}
	>
		<Icon name="globe" size={14} />
		<span>{nextLocale === 'zh-CN' ? '中文' : 'EN'}</span>
	</button>
</div>

<style>
	.prefs {
		display: flex;
		align-items: center;
		gap: 6px;
	}
	.theme {
		display: inline-flex;
		padding: 2px;
		gap: 1px;
		border-radius: var(--radius);
		background: var(--hover);
	}
	.theme button {
		display: grid;
		place-items: center;
		width: 26px;
		height: 22px;
		border-radius: 5px;
		color: var(--fg-3);
		transition:
			background-color var(--dur-1) var(--ease),
			color var(--dur-1) var(--ease);
	}
	.theme button:hover {
		color: var(--fg);
	}
	.theme button.on {
		background: var(--surface);
		color: var(--fg);
		box-shadow:
			0 0 0 1px var(--border),
			0 1px 2px rgb(0 0 0 / 0.06);
	}
	.lang {
		display: inline-flex;
		align-items: center;
		gap: 5px;
		height: 26px;
		padding: 0 8px;
		border-radius: var(--radius);
		font-size: var(--text-xs);
		font-weight: 500;
		color: var(--fg-2);
		transition:
			background-color var(--dur-1) var(--ease),
			color var(--dur-1) var(--ease);
	}
	.lang:hover {
		background: var(--hover);
		color: var(--fg);
	}
</style>
