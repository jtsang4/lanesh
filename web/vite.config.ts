import adapter from '@sveltejs/adapter-static';
import { sveltekit } from '@sveltejs/kit/vite';
import { defineConfig } from 'vite';

// The Go server embeds build/app (see embed.go) and serves it as a SPA.
const apiTarget = process.env.LANESH_DEV_API ?? 'http://localhost:8080';

export default defineConfig({
	plugins: [
		sveltekit({
			compilerOptions: {
				// Force runes mode for the project, except for libraries. Can be removed in svelte 6.
				runes: ({ filename }) =>
					filename.split(/[/\\]/).includes('node_modules') ? undefined : true
			},
			adapter: adapter({
				pages: 'build/app',
				assets: 'build/app',
				fallback: 'index.html',
				strict: true
			})
		})
	],
	server: {
		proxy: {
			'/api': apiTarget,
			'/healthz': apiTarget,
			// Only the server's own lane endpoints; /lanes/<id>/... pages belong to the SPA.
			'^/lanes/[^/]+/(mcp|join|connect|membership)': { target: apiTarget, ws: true },
			'/install.sh': apiTarget,
			'/install.ps1': apiTarget,
			'/transfers': apiTarget
		}
	}
});
