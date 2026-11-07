<script lang="ts">
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import Icon from '#lib/components/Icon.svelte';
	import Logo from '#lib/components/Logo.svelte';
	import { t } from '#lib/i18n/index.svelte.ts';

	const notFound = $derived(page.status === 404);
</script>

<svelte:head>
	<title>{notFound ? t('errors.notFoundTitle') : t('errors.title')} · Lanesh</title>
</svelte:head>

<main class="wrap">
	<div class="box fade-in">
		<Logo size={24} />
		<p class="code mono">{page.status}</p>
		<h1>{notFound ? t('errors.notFoundTitle') : t('errors.title')}</h1>
		<p class="msg">
			{notFound ? t('errors.notFoundBody') : page.error?.message || t('common.networkError')}
		</p>
		<div class="actions">
			<a class="btn btn-primary" href={resolve('/(app)')}>
				<Icon name="arrowLeft" size={14} />
				{t('errors.home')}
			</a>
			{#if !notFound}
				<button type="button" class="btn" onclick={() => location.reload()}>
					<Icon name="refresh" size={14} />
					{t('common.retry')}
				</button>
			{/if}
		</div>
	</div>
</main>

<style>
	.wrap {
		min-height: 100dvh;
		display: grid;
		place-items: center;
		padding: 24px;
	}
	.box {
		display: grid;
		justify-items: start;
		gap: 8px;
		width: min(420px, 100%);
	}
	.code {
		margin-top: 16px;
		font-size: var(--text-xs);
		color: var(--fg-3);
	}
	h1 {
		font-size: var(--text-xl);
		font-weight: 600;
		letter-spacing: -0.02em;
	}
	.msg {
		font-size: var(--text-sm);
		color: var(--fg-2);
	}
	.actions {
		display: flex;
		gap: 8px;
		margin-top: 12px;
	}
</style>
