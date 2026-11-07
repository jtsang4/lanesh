<script lang="ts" generics="T extends string">
	let {
		options,
		value = $bindable(),
		label,
		size = 'sm',
		onchange
	}: {
		options: { id: T; label: string }[];
		value: T;
		label: string;
		size?: 'sm' | 'xs';
		onchange?: (v: T) => void;
	} = $props();

	let buttons: HTMLButtonElement[] = $state([]);

	function select(v: T) {
		value = v;
		onchange?.(v);
	}

	function onkeydown(e: KeyboardEvent, i: number) {
		const delta = e.key === 'ArrowRight' ? 1 : e.key === 'ArrowLeft' ? -1 : 0;
		if (!delta) return;
		e.preventDefault();
		const next = (i + delta + options.length) % options.length;
		select(options[next].id);
		buttons[next]?.focus();
	}
</script>

<div class={['seg', size]} role="radiogroup" aria-label={label}>
	{#each options as opt, i (opt.id)}
		<button
			bind:this={buttons[i]}
			type="button"
			role="radio"
			aria-checked={value === opt.id}
			tabindex={value === opt.id ? 0 : -1}
			class={[value === opt.id && 'on']}
			onclick={() => select(opt.id)}
			onkeydown={(e) => onkeydown(e, i)}>{opt.label}</button
		>
	{/each}
</div>

<style>
	.seg {
		display: inline-flex;
		justify-self: start;
		width: fit-content;
		padding: 2px;
		gap: 2px;
		border-radius: var(--radius);
		background: var(--hover);
		max-width: 100%;
		overflow-x: auto;
		scrollbar-width: none;
	}
	button {
		height: 24px;
		padding: 0 10px;
		border-radius: 5px;
		font-size: var(--text-xs);
		font-weight: 500;
		color: var(--fg-2);
		white-space: nowrap;
		transition:
			background-color var(--dur-1) var(--ease),
			color var(--dur-1) var(--ease),
			box-shadow var(--dur-1) var(--ease);
	}
	.xs button {
		height: 22px;
		padding: 0 8px;
	}
	button:hover {
		color: var(--fg);
	}
	button.on {
		background: var(--surface);
		color: var(--fg);
		box-shadow:
			0 0 0 1px var(--border),
			0 1px 2px rgb(0 0 0 / 0.06);
	}
	button:focus-visible {
		outline-offset: 0;
	}
</style>
