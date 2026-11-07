// JSON shapes of the admin API (internal/server, internal/store, internal/protocol).

export interface Session {
	authenticated: boolean;
	version: string;
}

export interface Info {
	version: string;
	commit: string;
	protocol: number;
	public_url: string;
	public_url_configured: boolean;
	data_dir: string;
	started_at: string;
	lanes: number;
	machines: number;
	online: number;
}

export interface Lane {
	id: string;
	name: string;
	created_at: string;
	machine_count: number;
	online_count: number;
}

export interface Policy {
	expose_skills: boolean;
	allow_exec: boolean;
}

export interface Environment {
	os: string;
	arch: string;
	hostname: string;
	user: string;
	home: string;
	default_shell: string;
	shells: string[] | null;
	version: string;
}

export interface Machine {
	id: string;
	name: string;
	os: string;
	arch: string;
	hostname: string;
	version: string;
	online: boolean;
	connected_at?: string;
	last_seen: string;
	joined_at: string;
	policy: Policy;
	skill_count: number;
	skills_hash: string;
	environment: Environment;
}

export interface Skill {
	name: string;
	description: string;
	source: string;
	path: string;
	hash: string;
}

export interface ShadowedSkill {
	name: string;
	source: string;
	path: string;
}

export interface SkillIndex {
	skills: Skill[] | null;
	shadowed?: ShadowedSkill[] | null;
	hash: string;
	live: boolean;
	updated_at: string;
}

export interface FileEntry {
	path: string;
	size: number;
	dir?: boolean;
}

export interface SkillReadResult {
	skill: string;
	dir: string;
	path: string;
	content: string;
	encoding: 'utf-8' | 'base64' | string;
	size: number;
	offset: number;
	truncated?: boolean;
	files?: FileEntry[];
}

export interface LaneSkillMachine {
	machine_id: string;
	name: string;
	online: boolean;
	source: string;
	hash: string;
}

export interface LaneSkill {
	name: string;
	description: string;
	consistent: boolean;
	machines: LaneSkillMachine[];
}

export type KeyKind = 'join' | 'access';

export interface Key {
	id: string;
	lane_id: string;
	kind: KeyKind;
	name: string;
	prefix: string;
	expires_at?: string;
	created_at: string;
	last_used_at?: string;
	revoked_at?: string;
}

export interface CreatedKey {
	key: Key;
	token: string;
}

export interface AuditEntry {
	id: number;
	time: string;
	lane_id: string;
	actor: string;
	action: string;
	machine_id: string;
	detail: Record<string, unknown> | null;
	result: string;
}
