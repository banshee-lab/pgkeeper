import { beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
	getMe: vi.fn(),
	logoutRequest: vi.fn(),
	setUnauthorizedHandler: vi.fn()
}));

vi.mock('$lib/api/client', () => ({
	ApiError: class ApiError extends Error {
		constructor(
			public status: number,
			public code: string,
			message: string
		) {
			super(message);
		}
	},
	getMe: mocks.getMe,
	logoutRequest: mocks.logoutRequest,
	setUnauthorizedHandler: mocks.setUnauthorizedHandler,
	listBackups: vi.fn().mockResolvedValue([]),
	listActivities: vi.fn().mockResolvedValue([]),
	serverNow: () => Date.now()
}));

import { ApiError } from '$lib/api/client';
import { loginRedirectUrl, session } from './session.svelte';

describe('session', () => {
	beforeEach(() => {
		vi.clearAllMocks();
	});

	it('opens the app when authentication is disabled', async () => {
		mocks.getMe.mockResolvedValue({
			auth_enabled: false,
			authenticated: true,
			user: null
		});

		await session.load();

		expect(session.status).toBe('ready');
		expect(session.authEnabled).toBe(false);
	});

	it('opens the app for an authenticated user when authentication is enabled', async () => {
		mocks.getMe.mockResolvedValue({
			auth_enabled: true,
			authenticated: true,
			user: { name: 'Operator' }
		});

		await session.load();

		expect(session.status).toBe('ready');
		expect(session.authEnabled).toBe(true);
		expect(session.user?.name).toBe('Operator');
	});

	it('shows login for a 401 and an unauthenticated 200 response', async () => {
		mocks.getMe.mockRejectedValue(new ApiError(401, 'unauthenticated', 'Sign in required'));
		await session.load();
		expect(session.status).toBe('unauthenticated');

		mocks.getMe.mockResolvedValue({
			auth_enabled: true,
			authenticated: false,
			user: null
		});
		await session.load();
		expect(session.status).toBe('unauthenticated');
	});

	it('keeps network errors distinct and retryable', async () => {
		mocks.getMe.mockRejectedValue(new Error('offline'));

		await session.load();

		expect(session.status).toBe('error');
		expect(session.error).toBe('offline');
	});

	it('only builds a single-slash relative sign-in redirect', () => {
		expect(loginRedirectUrl('/backups/a?activity=123')).toBe(
			'/auth/login?redirect_to=%2Fbackups%2Fa%3Factivity%3D123'
		);
		expect(loginRedirectUrl('//evil.example/path')).toBe('/auth/login?redirect_to=%2F');
		expect(loginRedirectUrl('/\\\\evil.example/path')).toBe('/auth/login?redirect_to=%2F');
		expect(loginRedirectUrl('https://evil.example')).toBe('/auth/login?redirect_to=%2F');
	});
});
