import tailwindcss from '@tailwindcss/vite';
import { defineConfig } from 'vitest/config';
import { playwright } from '@vitest/browser-playwright';
import { loadEnv } from 'vite';
import adapter from '@sveltejs/adapter-static';
import { sveltekit } from '@sveltejs/kit/vite';

export default defineConfig(({ mode }) => {
	const backend =
		mode === 'development'
			? (loadEnv(mode, '.', '').PGKEEPER_URL ?? 'http://localhost:8080')
			: undefined;
	return {
		plugins: [
			tailwindcss(),
			sveltekit({
				adapter: adapter({ fallback: 'index.html', strict: false }),
				alias: { '$lib': './src/lib' },
				compilerOptions: {
					runes: ({ filename }) =>
						filename.split(/[/\\]/).includes('node_modules') ? undefined : true
				}
			})
		],
		server: backend
			? {
					proxy: {
						'/api': backend,
						'/auth': backend,
						'/healthz': backend
					}
				}
			: undefined,
		optimizeDeps: {
			exclude: ['@runwisp/ui']
		},
		test: {
			expect: { requireAssertions: true },
			projects: [
				{
					extends: './vite.config.ts',
					test: {
						name: 'client',
						browser: {
							enabled: true,
							provider: playwright(),
							instances: [{ browser: 'chromium', headless: true }]
						},
						include: ['src/**/*.svelte.{test,spec}.{js,ts}'],
						exclude: ['src/lib/server/**']
					}
				},
				{
					extends: './vite.config.ts',
					test: {
						name: 'server',
						environment: 'node',
						include: ['src/**/*.{test,spec}.{js,ts}'],
						exclude: ['src/**/*.svelte.{test,spec}.{js,ts}']
					}
				}
			]
		}
	};
});
