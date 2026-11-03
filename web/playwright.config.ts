import { defineConfig } from '@playwright/test';

export default defineConfig({
	webServer: { command: 'yarn build && yarn preview --host 127.0.0.1', port: 4173 },
	testMatch: '**/*.e2e.{ts,js}'
});
