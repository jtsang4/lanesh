import type { Key } from './types';

export type KeyStatus = 'active' | 'expired' | 'revoked';

export function keyStatus(k: Key, now = Date.now()): KeyStatus {
	if (k.revoked_at) return 'revoked';
	if (k.expires_at && new Date(k.expires_at).getTime() <= now) return 'expired';
	return 'active';
}
