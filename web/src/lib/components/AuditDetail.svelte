<script lang="ts">
	import { num, str } from '#lib/audit.ts';
	import { bytes, platform, shortDuration } from '#lib/format.ts';
	import { t, tn } from '#lib/i18n/index.svelte.ts';
	import type { AuditEntry } from '#lib/types.ts';
	import Badge from './Badge.svelte';

	let { entry }: { entry: AuditEntry } = $props();

	const d = $derived(entry.detail ?? {});
	const exit = $derived(num(d.exit_code));
	const dur = $derived(num(d.duration_ms));
	const size = $derived(num(d.bytes));
</script>

{#snippet exitCode()}
	{#if exit !== null}
		<span class={['exit', exit === 0 ? 'ok' : 'bad']}>{t('activity.exit', { code: exit })}</span>
	{/if}
{/snippet}

<div class="detail">
	{#if entry.action === 'exec' || entry.action === 'job.exited'}
		<code class="cmd" title={str(d.command)}
			><span class="prompt" aria-hidden="true">$</span>{str(d.command)}</code
		>
		<div class="meta">
			{@render exitCode()}
			{#if str(d.status) === 'running'}<span class="running">{t('activity.running')}</span>{/if}
			{#if dur !== null}<span>{shortDuration(dur)}</span>{/if}
			{#if str(d.skill)}<span>{t('activity.inSkill', { skill: str(d.skill) })}</span>{/if}
			{#if str(d.cwd)}<span class="mono path">{str(d.cwd)}</span>{/if}
			{#if str(d.job_id)}<span class="mono id">{str(d.job_id)}</span>{/if}
		</div>
	{:else if entry.action === 'file.read' || entry.action === 'file.write'}
		<code class="cmd path-main" title={str(d.path)}>{str(d.path)}</code>
		<div class="meta">
			{#if size !== null && entry.result === 'ok'}<span>{bytes(size)}</span>{/if}
			{#if d.overwrite === true}<span>{t('activity.overwrite')}</span>{/if}
		</div>
	{:else if entry.action === 'transfer'}
		<code class="cmd" title="{str(d.from_path)} → {str(d.to_machine)}:{str(d.to_path)}"
			>{str(d.from_path)} <span class="arrow">→</span>
			<span class="to">{str(d.to_machine)}:</span>{str(d.to_path)}</code
		>
		<div class="meta">
			{#if size !== null && entry.result === 'ok'}<span>{bytes(size)}</span>{/if}
			{#if num(d.files)}<span>{tn('activity.files', num(d.files) ?? 0)}</span>{/if}
		</div>
	{:else if entry.action === 'job.kill' || entry.action === 'job.write'}
		<div class="meta solo">
			<span class="mono id">{str(d.job_id)}</span>
			{#if str(d.signal)}<span>{t('activity.signal', { signal: str(d.signal) })}</span>{/if}
			{#if size !== null}<span>{bytes(size)}</span>{/if}
			{#if d.close_stdin === true}<span>{t('activity.closeStdin')}</span>{/if}
		</div>
	{:else if entry.action === 'machine.join'}
		<div class="meta solo">
			<span class="strong">{str(d.name)}</span>
			<span>{platform(str(d.os), str(d.arch))}</span>
			{#if d.allow_exec === false}<span>{t('machines.execDisabled')}</span>{/if}
		</div>
	{:else if entry.action === 'key.create'}
		<div class="meta solo">
			<Badge tone="outline">{str(d.kind)}</Badge>
			<span class="strong">{str(d.name)}</span>
		</div>
	{:else if entry.action === 'key.revoke'}
		<div class="meta solo">
			<span class="strong">{str(d.name) || str(d.key_id)}</span>
		</div>
	{:else if entry.action.startsWith('project.')}
		<div class="meta solo">
			<span class="strong">{str(d.name)}</span>
			{#if num(d.revision)}<span>{t('activity.revision', { n: num(d.revision) ?? 0 })}</span>{/if}
			{#if str(d.changes)}<span>{str(d.changes)}</span>{/if}
		</div>
	{:else if entry.action === 'lane.create' || entry.action === 'lane.delete'}
		<div class="meta solo"><span class="strong">{str(d.name)}</span></div>
	{:else if Object.keys(d).length > 0}
		<div class="meta solo">
			{#each Object.entries(d) as [k, v] (k)}
				<span><span class="faint">{k}=</span><span class="mono">{str(v)}</span></span>
			{/each}
		</div>
	{/if}
	{#if entry.result.startsWith('error')}
		<p class="error">{entry.result.replace(/^error:\s*/, '')}</p>
	{/if}
</div>

<style>
	.detail {
		display: grid;
		gap: 3px;
		min-width: 0;
	}
	.cmd {
		display: block;
		font-size: 12.5px;
		color: var(--fg);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.prompt {
		color: var(--fg-4);
		margin-right: 7px;
		user-select: none;
	}
	.arrow {
		color: var(--fg-3);
	}
	.to {
		color: var(--fg-2);
	}
	.meta {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 2px 12px;
		font-size: var(--text-xs);
		color: var(--fg-3);
		min-width: 0;
	}
	.meta:empty {
		display: none;
	}
	.meta.solo {
		font-size: var(--text-sm);
		color: var(--fg-2);
	}
	.strong {
		color: var(--fg);
	}
	.path,
	.id {
		font-size: 11px;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		max-width: 100%;
	}
	.exit {
		font-family: var(--font-mono);
		font-size: 11px;
	}
	.exit.ok {
		color: var(--fg-3);
	}
	.exit.bad {
		color: var(--red);
	}
	.running {
		color: var(--accent-text);
	}
	.error {
		font-size: var(--text-xs);
		color: var(--red);
		overflow-wrap: anywhere;
	}
</style>
