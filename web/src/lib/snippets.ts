// Ready-to-paste commands and configs. Tokens fall back to visible placeholders.

export const JOIN_PLACEHOLDER = '<join-key>';
export const ACCESS_PLACEHOLDER = '<access-key>';

export function laneUrl(base: string, laneId: string) {
	return `${base}/lanes/${laneId}`;
}

export function mcpUrl(base: string, laneId: string) {
	return `${laneUrl(base, laneId)}/mcp`;
}

export interface Snippet {
	id: string;
	label: string;
	code: string;
	lang: 'sh' | 'powershell' | 'toml' | 'json';
	note?: string;
	extra?: { code: string; lang: Snippet['lang'] };
}

export function installSnippets(base: string, laneId: string, key = JOIN_PLACEHOLDER) {
	const lane = laneUrl(base, laneId);
	return {
		unix: `curl -fsSL ${base}/install.sh | sh -s -- join ${lane} --key ${key}`,
		windows: `& ([scriptblock]::Create((irm ${base}/install.ps1))) join ${lane} --key ${key}`,
		cli: `lanesh join ${lane} --key ${key}`
	};
}

// The Codex section of ~/.codex/config.toml. The key is written into the
// header directly, and Lanesh tools run without asking for approval each time.
export function codexConfig(url: string, key = ACCESS_PLACEHOLDER) {
	return [
		'# ~/.codex/config.toml',
		'[mcp_servers.lanesh]',
		`url = "${url}"`,
		'default_tools_approval_mode = "approve"',
		'',
		'[mcp_servers.lanesh.http_headers]',
		`Authorization = "Bearer ${key}"`
	].join('\n');
}

export function clientSnippets(base: string, laneId: string, key = ACCESS_PLACEHOLDER) {
	const url = mcpUrl(base, laneId);
	const json = JSON.stringify(
		{ mcpServers: { lanesh: { url, headers: { Authorization: `Bearer ${key}` } } } },
		null,
		2
	);
	return {
		// All three are global (user-level) configurations, available in every project.
		claude: `claude mcp add --scope user --transport http lanesh ${url} \\\n  --header "Authorization: Bearer ${key}"`,
		codex: codexConfig(url, key),
		json
	};
}
