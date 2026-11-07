<script lang="ts">
	import { renderMarkdown } from '#lib/markdown.ts';

	let { source }: { source: string } = $props();

	// renderMarkdown always sanitizes with DOMPurify; the content is untrusted.
	const html = $derived(renderMarkdown(source));
</script>

<div class="prose">
	<!-- Safe: `html` is DOMPurify output (see markdown.ts). -->
	{@html html}
</div>

<style>
	.prose {
		font-size: var(--text-md);
		line-height: 1.7;
		color: var(--fg);
		overflow-wrap: anywhere;
		min-width: 0;
	}
	.prose :global {
		> :first-child {
			margin-top: 0;
		}
		> :last-child {
			margin-bottom: 0;
		}
		h1,
		h2,
		h3,
		h4,
		h5,
		h6 {
			margin: 1.6em 0 0.5em;
			font-weight: 600;
			letter-spacing: -0.015em;
			line-height: 1.3;
		}
		h1 {
			font-size: 20px;
		}
		h2 {
			font-size: 16px;
			padding-bottom: 6px;
			border-bottom: 1px solid var(--border);
		}
		h3 {
			font-size: 14px;
		}
		h4,
		h5,
		h6 {
			font-size: 13px;
			color: var(--fg-2);
		}
		p,
		ul,
		ol,
		pre,
		blockquote,
		table {
			margin: 0 0 0.9em;
		}
		ul,
		ol {
			padding-left: 1.4em;
		}
		ul {
			list-style: disc;
		}
		ol {
			list-style: decimal;
		}
		li + li {
			margin-top: 0.25em;
		}
		li::marker {
			color: var(--fg-3);
		}
		a {
			color: var(--accent-text);
			text-decoration: underline;
			text-decoration-color: color-mix(in srgb, var(--accent) 35%, transparent);
			text-underline-offset: 3px;
		}
		a:hover {
			text-decoration-color: currentColor;
		}
		code {
			font-family: var(--font-mono);
			font-size: 0.86em;
			padding: 1px 5px;
			border-radius: 4px;
			background: var(--code-bg);
			border: 1px solid var(--border);
		}
		pre {
			padding: 12px 14px;
			border-radius: var(--radius);
			background: var(--code-bg);
			border: 1px solid var(--border);
			overflow-x: auto;
			line-height: 1.6;
		}
		pre code {
			padding: 0;
			border: 0;
			background: none;
			font-size: 12.5px;
		}
		blockquote {
			padding: 2px 0 2px 14px;
			border-left: 2px solid var(--border-strong);
			color: var(--fg-2);
		}
		hr {
			border: 0;
			border-top: 1px solid var(--border);
			margin: 1.6em 0;
		}
		table {
			border-collapse: collapse;
			font-size: var(--text-sm);
			display: block;
			overflow-x: auto;
		}
		th,
		td {
			padding: 6px 10px;
			border: 1px solid var(--border);
			text-align: left;
		}
		th {
			background: var(--surface-2);
			font-weight: 500;
		}
	}
</style>
