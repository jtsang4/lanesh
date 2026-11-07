<script lang="ts">
	import CopyButton from './CopyButton.svelte';

	let {
		code,
		copyLabel,
		wrap = true
	}: { code: string; copyLabel?: string; wrap?: boolean } = $props();

	type Token = { text: string; kind: 'plain' | 'ph' | 'comment' };

	// Highlights <placeholders> and # comments; everything else is plain text.
	const lines = $derived(
		code.split('\n').map((line): Token[] => {
			if (/^\s*#/.test(line)) return [{ text: line, kind: 'comment' }];
			const out: Token[] = [];
			let last = 0;
			for (const m of line.matchAll(/<[a-z][a-z-]*>/g)) {
				if (m.index > last) out.push({ text: line.slice(last, m.index), kind: 'plain' });
				out.push({ text: m[0], kind: 'ph' });
				last = m.index + m[0].length;
			}
			if (last < line.length) out.push({ text: line.slice(last), kind: 'plain' });
			return out;
		})
	);
</script>

<div class="code">
	<pre class={[wrap && 'wrap']}><code
			>{#each lines as line, i (i)}{#each line as tok, j (j)}{#if tok.kind === 'plain'}{tok.text}{:else}<span
							class={tok.kind}>{tok.text}</span
						>{/if}{/each}{#if i < lines.length - 1}{'\n'}{/if}{/each}</code
		></pre>
	<div class="copy">
		<CopyButton text={code} label={copyLabel} />
	</div>
</div>

<style>
	.code {
		position: relative;
		border: 1px solid var(--border);
		background: var(--code-bg);
		border-radius: var(--radius);
		min-width: 0;
	}
	pre {
		padding: 11px 44px 11px 13px;
		font-size: 12.5px;
		line-height: 1.65;
		color: var(--fg);
		overflow-x: auto;
		tab-size: 2;
	}
	pre.wrap {
		white-space: pre-wrap;
		overflow-wrap: anywhere;
	}
	.ph {
		color: var(--accent-text);
		background: var(--accent-soft);
		border-radius: 3px;
		padding: 0 2px;
	}
	.comment {
		color: var(--fg-3);
	}
	.copy {
		position: absolute;
		top: 6px;
		right: 6px;
	}
</style>
