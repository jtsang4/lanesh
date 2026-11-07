import { i18n, t } from './i18n/index.svelte';
import { ApiError } from './api';

const rtfCache = new Map<string, Intl.RelativeTimeFormat>();

function rtf() {
	let f = rtfCache.get(i18n.locale);
	if (!f) {
		f = new Intl.RelativeTimeFormat(i18n.locale, { numeric: 'auto', style: 'long' });
		rtfCache.set(i18n.locale, f);
	}
	return f;
}

const DAY = 24 * 3600;
// Each unit is used from its threshold upward (in seconds); days run to ~6 weeks.
const UNITS: [Intl.RelativeTimeFormatUnit, number, number][] = [
	['year', 365 * DAY, 350 * DAY],
	['month', 30 * DAY, 45 * DAY],
	['day', DAY, 22 * 3600],
	['hour', 3600, 3300],
	['minute', 60, 0]
];

/** "2 min ago" / "in 3 days", relative to `now`. */
export function relativeTime(iso: string | number | Date, now: number): string {
	const ts = new Date(iso).getTime();
	if (Number.isNaN(ts)) return '';
	const diff = (ts - now) / 1000;
	const abs = Math.abs(diff);
	if (abs < 45) return t('time.justNow');
	for (const [unit, secs, from] of UNITS) {
		if (abs >= from) return rtf().format(Math.round(diff / secs), unit);
	}
	return '';
}

export function absoluteTime(iso: string | number | Date): string {
	const d = new Date(iso);
	if (Number.isNaN(d.getTime())) return '';
	return d.toLocaleString(i18n.locale, {
		year: 'numeric',
		month: 'short',
		day: 'numeric',
		hour: '2-digit',
		minute: '2-digit',
		second: '2-digit'
	});
}

export function shortDate(iso: string | number | Date): string {
	return new Date(iso).toLocaleDateString(i18n.locale, {
		year: 'numeric',
		month: 'short',
		day: 'numeric'
	});
}

export function clockTime(iso: string | number | Date): string {
	return new Date(iso).toLocaleTimeString(i18n.locale, {
		hourCycle: 'h23',
		hour: '2-digit',
		minute: '2-digit',
		second: '2-digit'
	});
}

export function duration(ms: number): string {
	const s = Math.max(0, Math.floor(ms / 1000));
	const d = Math.floor(s / 86400);
	const h = Math.floor((s % 86400) / 3600);
	const m = d || h ? Math.floor((s % 3600) / 60) : Math.round(s / 60);
	const zh = i18n.locale === 'zh-CN';
	const parts: string[] = [];
	if (d) parts.push(zh ? `${d} 天` : `${d}d`);
	if (h || d) parts.push(zh ? `${h} 小时` : `${h}h`);
	parts.push(zh ? `${m} 分钟` : `${m}m`);
	return parts.slice(0, 2).join(' ');
}

export function shortDuration(ms: number): string {
	if (ms < 1000) return `${ms} ms`;
	if (ms < 60_000) return `${(ms / 1000).toFixed(ms < 10_000 ? 1 : 0)} s`;
	return duration(ms);
}

export function bytes(n: number): string {
	if (!Number.isFinite(n)) return '';
	if (n < 1024) return t('common.bytes', { n });
	const units = ['KB', 'MB', 'GB', 'TB'];
	let v = n / 1024;
	let i = 0;
	while (v >= 1024 && i < units.length - 1) {
		v /= 1024;
		i++;
	}
	return `${v.toFixed(v < 10 ? 1 : 0)} ${units[i]}`;
}

const OS_NAMES: Record<string, string> = {
	darwin: 'macOS',
	linux: 'Linux',
	windows: 'Windows',
	freebsd: 'FreeBSD'
};

export function osName(os: string): string {
	return OS_NAMES[os] ?? (os || t('common.unknown'));
}

export function platform(os: string, arch: string): string {
	return [osName(os), arch].filter(Boolean).join(' · ');
}

/** "v1.2.3" for releases, the raw string (e.g. "dev") otherwise. */
export function versionLabel(v: string): string {
	return /^\d/.test(v) ? `v${v}` : v || '?';
}

export function errorMessage(err: unknown): string {
	if (err instanceof ApiError) {
		if (err.status === 0) return t('common.networkError');
		return err.message;
	}
	if (err instanceof Error) return err.message;
	return t('common.error');
}

export function isNotFound(err: unknown): boolean {
	return err instanceof ApiError && err.status === 404;
}
