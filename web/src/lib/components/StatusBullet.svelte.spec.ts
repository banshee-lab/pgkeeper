import { page } from 'vitest/browser';
import { describe, expect, it } from 'vitest';
import { render } from 'vitest-browser-svelte';
import StatusBullet from './StatusBullet.svelte';

describe('StatusBullet', () => {
	it.each([
		['running', 'Running'],
		['success', 'Success'],
		['error', 'Failed'],
		['never', 'Never run']
	] as const)('provides a text equivalent for %s status', async (status, label) => {
		render(StatusBullet, { status });
		await expect.element(page.getByText(label)).toBeInTheDocument();
	});
});
