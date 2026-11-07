import { goto } from '$app/navigation';
import { resolve } from '$app/paths';
import type {
	AuditEntry,
	CreatedKey,
	Info,
	Key,
	KeyKind,
	Lane,
	LaneSkill,
	Machine,
	Session,
	SkillIndex,
	SkillReadResult
} from './types';

export class ApiError extends Error {
	readonly status: number;

	constructor(status: number, message: string) {
		super(message);
		this.name = 'ApiError';
		this.status = status;
	}
}

let redirecting = false;

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
	let res: Response;
	try {
		res = await fetch(`/api/v1${path}`, {
			method,
			credentials: 'same-origin',
			headers: body === undefined ? undefined : { 'Content-Type': 'application/json' },
			body: body === undefined ? undefined : JSON.stringify(body)
		});
	} catch {
		throw new ApiError(0, 'network');
	}
	if (res.status === 401 && path !== '/login') {
		sessionExpired();
		throw new ApiError(401, 'authentication required');
	}
	if (!res.ok) {
		let message = res.statusText;
		try {
			const data = (await res.json()) as { error?: string };
			if (data.error) message = data.error;
		} catch {
			// Non-JSON error body; keep the status text.
		}
		throw new ApiError(res.status, message);
	}
	if (res.status === 204) return undefined as T;
	return (await res.json()) as T;
}

/** Sends the user to the login page, remembering where they were. */
function sessionExpired() {
	if (redirecting || location.pathname === resolve('/login')) return;
	redirecting = true;
	const next = location.pathname + location.search;
	void goto(`${resolve('/login')}?expired=1&next=${encodeURIComponent(next)}`, {
		replace: true
	}).finally(() => {
		redirecting = false;
	});
}

const enc = encodeURIComponent;

export const api = {
	session: () => request<Session>('GET', '/session'),
	login: (token: string) => request<{ authenticated: boolean }>('POST', '/login', { token }),
	logout: () => request<void>('POST', '/logout'),
	info: () => request<Info>('GET', '/info'),

	lanes: () => request<Lane[]>('GET', '/lanes'),
	lane: (id: string) => request<Lane>('GET', `/lanes/${enc(id)}`),
	createLane: (name: string) => request<Lane>('POST', '/lanes', { name }),
	renameLane: (id: string, name: string) => request<Lane>('PATCH', `/lanes/${enc(id)}`, { name }),
	deleteLane: (id: string) => request<void>('DELETE', `/lanes/${enc(id)}`),

	machines: (lane: string) => request<Machine[]>('GET', `/lanes/${enc(lane)}/machines`),
	renameMachine: (lane: string, id: string, name: string) =>
		request<{ name: string }>('PATCH', `/lanes/${enc(lane)}/machines/${enc(id)}`, { name }),
	removeMachine: (lane: string, id: string) =>
		request<void>('DELETE', `/lanes/${enc(lane)}/machines/${enc(id)}`),
	machineSkills: (lane: string, id: string) =>
		request<SkillIndex>('GET', `/lanes/${enc(lane)}/machines/${enc(id)}/skills`),
	readSkill: (lane: string, id: string, skill: string, path?: string) =>
		request<SkillReadResult>(
			'GET',
			`/lanes/${enc(lane)}/machines/${enc(id)}/skills/${enc(skill)}${path ? `?path=${enc(path)}` : ''}`
		),

	laneSkills: (lane: string) => request<LaneSkill[]>('GET', `/lanes/${enc(lane)}/skills`),

	keys: (lane: string) => request<Key[]>('GET', `/lanes/${enc(lane)}/keys`),
	createKey: (lane: string, kind: KeyKind, name: string, expiresInDays?: number) =>
		request<CreatedKey>('POST', `/lanes/${enc(lane)}/keys`, {
			kind,
			name,
			expires_in_days: expiresInDays || undefined
		}),
	revokeKey: (lane: string, id: string) =>
		request<void>('DELETE', `/lanes/${enc(lane)}/keys/${enc(id)}`),

	audit: (
		lane: string,
		filter: { machine?: string; action?: string; actor?: string; before?: number; limit?: number }
	) => {
		const q = new URLSearchParams();
		for (const [k, v] of Object.entries(filter)) if (v) q.set(k, String(v));
		const qs = q.toString();
		return request<AuditEntry[]>('GET', `/lanes/${enc(lane)}/audit${qs ? `?${qs}` : ''}`);
	}
};
