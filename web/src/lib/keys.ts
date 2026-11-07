import type { Key } from './types';

export type KeyStatus = 'active' | 'expired' | 'revoked';

export function keyStatus(k: Key, now = Date.now()): KeyStatus {
	if (k.revoked_at) return 'revoked';
	if (k.expires_at && new Date(k.expires_at).getTime() <= now) return 'expired';
	return 'active';
}

const ORDER: Record<KeyStatus, number> = { active: 0, expired: 1, revoked: 2 };

export function sortKeys(keys: Key[], now = Date.now()): Key[] {
	return keys.toSorted(
		(a, b) =>
			ORDER[keyStatus(a, now)] - ORDER[keyStatus(b, now)] ||
			new Date(b.created_at).getTime() - new Date(a.created_at).getTime()
	);
}
