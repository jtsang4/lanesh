import { en, type MessageKey } from './en';
import { zhCN } from './zh-CN';

export type Locale = 'en' | 'zh-CN';
export type { MessageKey };

export const locales: { id: Locale; label: string }[] = [
	{ id: 'en', label: 'English' },
	{ id: 'zh-CN', label: '简体中文' }
];

const dictionaries: Record<Locale, Record<MessageKey, string>> = { en, 'zh-CN': zhCN };
const STORAGE_KEY = 'lanesh.locale';

type Params = Record<string, string | number>;
type PluralBase = {
	[K in MessageKey]: K extends `${infer B}.one` ? B : never;
}[MessageKey];

function initialLocale(): Locale {
	try {
		const saved = localStorage.getItem(STORAGE_KEY);
		if (saved === 'en' || saved === 'zh-CN') return saved;
	} catch {
		// Storage unavailable; fall back to English.
	}
	return 'en';
}

class I18n {
	locale = $state<Locale>(initialLocale());

	set(locale: Locale) {
		this.locale = locale;
		document.documentElement.lang = locale;
		try {
			localStorage.setItem(STORAGE_KEY, locale);
		} catch {
			// Ignore; the choice simply won't persist.
		}
	}
}

export const i18n = new I18n();

function format(template: string, params?: Params) {
	if (!params) return template;
	return template.replace(/\{(\w+)\}/g, (m, k: string) => (k in params ? String(params[k]) : m));
}

/** Translates a key in the current locale. Reactive when read in templates or deriveds. */
export function t(key: MessageKey, params?: Params): string {
	return format(dictionaries[i18n.locale][key] ?? en[key], params);
}

const pluralRules = new Map<Locale, Intl.PluralRules>();

/** Translates a plural pair (`<base>.one` / `<base>.other`) for count `n`. */
export function tn(base: PluralBase, n: number, params?: Params): string {
	let rules = pluralRules.get(i18n.locale);
	if (!rules) {
		rules = new Intl.PluralRules(i18n.locale);
		pluralRules.set(i18n.locale, rules);
	}
	const form = rules.select(n) === 'one' ? 'one' : 'other';
	return t(`${base}.${form}` as MessageKey, { n, ...params });
}
