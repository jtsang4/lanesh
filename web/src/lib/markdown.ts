// Renders untrusted SKILL.md content from remote machines.
// Markdown is converted with marked and always sanitized with DOMPurify before use.
import DOMPurify from 'dompurify';
import { Marked } from 'marked';

const marked = new Marked({ gfm: true, breaks: false, async: false });

let hooked = false;

function setupPurify() {
	if (hooked) return;
	hooked = true;
	DOMPurify.addHook('afterSanitizeAttributes', (node) => {
		if (node.tagName === 'A') {
			const href = node.getAttribute('href') ?? '';
			// In-document anchors stay; everything else opens outside the app.
			if (!href.startsWith('#')) {
				node.setAttribute('target', '_blank');
				node.setAttribute('rel', 'noopener noreferrer');
			}
		}
	});
}

export interface Frontmatter {
	fields: [string, string][];
	body: string;
}

/** Splits simple YAML frontmatter (`key: value` lines) from the Markdown body. */
export function splitFrontmatter(src: string): Frontmatter {
	const m = /^---\r?\n([\s\S]*?)\r?\n---\r?\n?/.exec(src);
	if (!m) return { fields: [], body: src };
	const fields: [string, string][] = [];
	let last: [string, string] | undefined;
	for (const line of m[1].split(/\r?\n/)) {
		const kv = /^([A-Za-z0-9_-]+):\s*(.*)$/.exec(line);
		if (kv) {
			last = [kv[1], kv[2].replace(/^(['"])(.*)\1$/, '$2')];
			fields.push(last);
		} else if (last && /^\s+\S/.test(line)) {
			last[1] = `${last[1]} ${line.trim()}`.trim();
		}
	}
	return { fields, body: src.slice(m[0].length) };
}

/** Returns sanitized HTML. Images are dropped: they would load remote URLs. */
export function renderMarkdown(src: string): string {
	setupPurify();
	const html = marked.parse(src) as string;
	return DOMPurify.sanitize(html, {
		USE_PROFILES: { html: true },
		FORBID_TAGS: [
			'img',
			'style',
			'form',
			'input',
			'button',
			'iframe',
			'video',
			'audio',
			'picture',
			'source'
		],
		FORBID_ATTR: ['style', 'srcset']
	});
}
