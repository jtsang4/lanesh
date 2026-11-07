import { redirect } from '@sveltejs/kit';
import { resolve } from '$app/paths';
import { api } from '#lib/api.ts';
import type { LayoutLoad } from './$types';

export const load: LayoutLoad = async ({ url, untrack }) => {
	const session = await api.session();
	if (!session.authenticated) {
		const next = untrack(() => url.pathname + url.search);
		const query = next === '/' ? '' : `?next=${encodeURIComponent(next)}`;
		redirect(307, `${resolve('/login')}${query}`);
	}
	return { version: session.version };
};
