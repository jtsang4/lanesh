export type ThemePref = 'system' | 'light' | 'dark';

const STORAGE_KEY = 'lanesh.theme';

function initialPref(): ThemePref {
	try {
		const saved = localStorage.getItem(STORAGE_KEY);
		if (saved === 'light' || saved === 'dark') return saved;
	} catch {
		// Storage unavailable.
	}
	return 'system';
}

class Theme {
	pref = $state<ThemePref>(initialPref());
	#systemDark = $state(matchMedia('(prefers-color-scheme: dark)').matches);
	resolved = $derived<'light' | 'dark'>(
		this.pref === 'system' ? (this.#systemDark ? 'dark' : 'light') : this.pref
	);

	constructor() {
		matchMedia('(prefers-color-scheme: dark)').addEventListener('change', (e) => {
			this.#systemDark = e.matches;
			this.#apply();
		});
	}

	set(pref: ThemePref) {
		this.pref = pref;
		try {
			if (pref === 'system') localStorage.removeItem(STORAGE_KEY);
			else localStorage.setItem(STORAGE_KEY, pref);
		} catch {
			// Ignore; the choice simply won't persist.
		}
		this.#apply();
	}

	#apply() {
		const root = document.documentElement;
		// Suppress transitions for one frame so the whole UI switches at once.
		root.classList.add('theme-switching');
		root.dataset.theme = this.resolved;
		requestAnimationFrame(() =>
			requestAnimationFrame(() => root.classList.remove('theme-switching'))
		);
	}
}

export const theme = new Theme();
