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
	const res = await send(method, path, body);
	if (res.status === 204) return undefined as T;
	return (await res.json()) as T;
}

/** A page of a list, with the counts the server sends in response headers. */
export interface Page<T> {
	items: T[];
	/** Items matching the query across all pages. */
	total: number;
	headers: Headers;
}

async function requestPage<T>(path: string, query: Record<string, unknown>): Promise<Page<T>> {
	const res = await send('GET', path + queryString(query));
	return {
		items: (await res.json()) as T[],
		total: Number(res.headers.get('X-Total-Count') ?? 0),
		headers: res.headers
	};
}

function queryString(query: Record<string, unknown>) {
	const q = new URLSearchParams();
	for (const [k, v] of Object.entries(query)) {
		if (v !== undefined && v !== '' && v !== false && v !== null) q.set(k, String(v));
	}
	const qs = q.toString();
	return qs ? `?${qs}` : '';
}

async function send(method: string, path: string, body?: unknown): Promise<Response> {
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
	return res;
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

	/** Keys, active ones first and then newest first. */
	keys: (
		lane: string,
		query: { kind?: KeyKind; status?: 'active' | 'inactive'; limit: number; offset?: number }
	) => requestPage<Key>(`/lanes/${enc(lane)}/keys`, query),
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
	) => request<AuditEntry[]>('GET', `/lanes/${enc(lane)}/audit${queryString(filter)}`)
};
