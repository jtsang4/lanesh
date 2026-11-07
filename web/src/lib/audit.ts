import type { IconName } from './components/Icon.svelte';
import type { MessageKey } from './i18n/en';

export const ACTIONS = [
	'exec',
	'job.exited',
	'job.kill',
	'job.write',
	'file.read',
	'file.write',
	'transfer',
	'machine.join',
	'machine.leave',
	'machine.remove',
	'key.create',
	'key.revoke',
	'lane.create'
] as const;

const ICONS: Record<string, IconName> = {
	exec: 'terminal',
	'job.exited': 'terminal',
	'job.kill': 'x',
	'job.write': 'arrowRight',
	'file.read': 'fileText',
	'file.write': 'pencil',
	transfer: 'arrowsDiff',
	'machine.join': 'server',
	'machine.leave': 'server',
	'machine.remove': 'server',
	'key.create': 'key',
	'key.revoke': 'key',
	'lane.create': 'layers',
	'lane.delete': 'layers'
};

export function actionIcon(action: string): IconName {
	return ICONS[action] ?? 'activity';
}

export function actionKey(action: string): MessageKey | null {
	const key = `action.${action}`;
	return ICONS[action] ? (key as MessageKey) : null;
}

export function str(v: unknown): string {
	if (v === null || v === undefined) return '';
	if (typeof v === 'string') return v;
	if (typeof v === 'number' || typeof v === 'boolean') return String(v);
	return JSON.stringify(v);
}

export function num(v: unknown): number | null {
	return typeof v === 'number' && Number.isFinite(v) ? v : null;
}
