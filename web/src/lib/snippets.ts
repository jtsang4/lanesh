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

export function clientSnippets(base: string, laneId: string, key = ACCESS_PLACEHOLDER) {
	const url = mcpUrl(base, laneId);
	const json = JSON.stringify(
		{ mcpServers: { lanesh: { url, headers: { Authorization: `Bearer ${key}` } } } },
		null,
		2
	);
	return {
		claude: `claude mcp add --transport http lanesh ${url} \\\n  --header "Authorization: Bearer ${key}"`,
		codex: `# ~/.codex/config.toml\n[mcp_servers.lanesh]\nurl = "${url}"\nbearer_token_env_var = "LANESH_ACCESS_KEY"`,
		codexEnv: `export LANESH_ACCESS_KEY="${key}"`,
		json
	};
}
