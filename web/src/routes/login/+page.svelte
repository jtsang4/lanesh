<script lang="ts">
	import Rich from '#lib/components/Rich.svelte';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import { api, ApiError } from '#lib/api.ts';
	import Icon from '#lib/components/Icon.svelte';
	import Logo from '#lib/components/Logo.svelte';
	import Prefs from '#lib/components/Prefs.svelte';
	import { errorMessage } from '#lib/format.ts';
	import { t } from '#lib/i18n/index.svelte.ts';
	import { app } from '#lib/state/app.svelte.ts';

	let token = $state('');
	let error = $state('');
	let busy = $state(false);
	let reveal = $state(false);

	const next = $derived.by(() => {
		const n = page.url.searchParams.get('next');
		// Only same-app paths; never an absolute or protocol-relative URL.
		return n && n.startsWith('/') && !n.startsWith('//') ? n : resolve('/(app)');
	});
	const expired = $derived(page.url.searchParams.has('expired'));

	async function redirectIfSignedIn() {
		try {
			const session = await api.session();
			if (session.authenticated) await goto(next, { replace: true });
		} catch {
			// Stay on the login page.
		}
	}

	$effect(() => {
		void redirectIfSignedIn();
	});

	async function submit(e: SubmitEvent) {
		e.preventDefault();
		if (!token.trim()) {
			error = t('login.empty');
			return;
		}
		busy = true;
		error = '';
		try {
			await api.login(token.trim());
			app.reset();
			await goto(next, { replace: true });
		} catch (err) {
			if (err instanceof ApiError && err.status === 401) error = t('login.invalid');
			else if (err instanceof ApiError && err.status === 429) error = t('login.rateLimited');
			else error = errorMessage(err);
			busy = false;
		}
	}
</script>

<svelte:head>
	<title>{t('login.title')}</title>
</svelte:head>

<div class="page">
	<div class="top">
		<Prefs />
	</div>
	<main class="center">
		<div class="card fade-in">
			<Logo size={28} />
			<h1>{t('login.title')}</h1>
			<p class="sub">{t('login.subtitle')}</p>

			{#if expired && !error}
				<p class="notice" role="status">
					<Icon name="info" size={14} />
					{t('login.expired')}
				</p>
			{/if}

			<form onsubmit={submit} novalidate>
				<div class="field">
					<label class="label" for="token">{t('login.token')}</label>
					<div class="pw">
						<!-- svelte-ignore a11y_autofocus -->
						<input
							id="token"
							class="input mono"
							type={reveal ? 'text' : 'password'}
							bind:value={token}
							placeholder="lsa_…"
							autocomplete="current-password"
							spellcheck="false"
							autofocus
							aria-invalid={error ? 'true' : undefined}
							aria-describedby={error ? 'login-error' : 'login-help'}
						/>
						<button
							type="button"
							class="eye"
							aria-pressed={reveal}
							aria-label={t('login.token')}
							onclick={() => (reveal = !reveal)}
						>
							<Icon name={reveal ? 'lock' : 'eye'} size={14} />
						</button>
					</div>
					{#if error}
						<p id="login-error" class="error-text" role="alert">{error}</p>
					{/if}
				</div>
				<button type="submit" class="btn btn-primary submit" disabled={busy}>
					{busy ? t('login.submitting') : t('login.submit')}
					{#if !busy}<Icon name="arrowRight" size={14} />{/if}
				</button>
			</form>
			<p id="login-help" class="help"><Rich text={t('login.help')} /></p>
		</div>
	</main>
</div>

<style>
	.page {
		min-height: 100dvh;
		display: flex;
		flex-direction: column;
		background:
			radial-gradient(circle at 1px 1px, var(--border) 1px, transparent 0) 0 0 / 22px 22px,
			var(--bg);
	}
	.top {
		display: flex;
		justify-content: flex-end;
		padding: 14px 16px;
	}
	.center {
		flex: 1;
		display: grid;
		place-items: center;
		padding: 24px 16px 15vh;
	}
	.card {
		width: min(380px, 100%);
		padding: 28px 28px 24px;
		background: var(--surface);
		border: 1px solid var(--border);
		border-radius: var(--radius-xl);
		box-shadow:
			0 1px 2px rgb(0 0 0 / 0.03),
			0 12px 40px -16px rgb(0 0 0 / 0.12);
	}
	h1 {
		margin-top: 20px;
		font-size: var(--text-xl);
		font-weight: 600;
		letter-spacing: -0.02em;
		line-height: 1.3;
	}
	.sub {
		margin-top: 4px;
		font-size: var(--text-sm);
		color: var(--fg-2);
	}
	.notice {
		display: flex;
		align-items: center;
		gap: 8px;
		margin-top: 16px;
		padding: 8px 10px;
		border-radius: var(--radius);
		background: var(--accent-soft);
		color: var(--accent-text);
		font-size: var(--text-xs);
	}
	form {
		display: grid;
		gap: 14px;
		margin-top: 20px;
	}
	.pw {
		position: relative;
	}
	.pw .input {
		height: 36px;
		padding-right: 38px;
	}
	.eye {
		position: absolute;
		top: 50%;
		right: 5px;
		transform: translateY(-50%);
		display: grid;
		place-items: center;
		width: 26px;
		height: 26px;
		border-radius: 5px;
		color: var(--fg-3);
	}
	.eye:hover {
		background: var(--hover);
		color: var(--fg);
	}
	.submit {
		height: 36px;
		width: 100%;
	}
	.help {
		margin-top: 18px;
		padding-top: 14px;
		border-top: 1px solid var(--border);
		font-size: var(--text-xs);
		color: var(--fg-3);
		line-height: 1.55;
	}
</style>
