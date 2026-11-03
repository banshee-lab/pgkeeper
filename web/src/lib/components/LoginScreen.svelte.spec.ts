import { page } from 'vitest/browser';
import { describe, expect, it, vi } from 'vitest';
import { render } from 'vitest-browser-svelte';
import LoginScreen from './LoginScreen.svelte';

describe('LoginScreen', () => {
	it('explains expired sessions and starts sign-in when requested', async () => {
		const onSignIn = vi.fn();
		render(LoginScreen, { expired: true, onSignIn });

		await expect.element(page.getByText('Your session expired')).toBeInTheDocument();
		await page.getByRole('button', { name: 'Sign in' }).click();
		expect(onSignIn).toHaveBeenCalledOnce();
	});
});
