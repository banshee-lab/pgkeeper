import { ApiError, getMe, logoutRequest, setUnauthorizedHandler } from '$lib/api/client';
import type { Me } from '$lib/api/types';
import { activities } from './activities.svelte';
import { backups } from './backups.svelte';
import { jobs } from './jobs.svelte';

export type SessionStatus = 'loading' | 'ready' | 'unauthenticated' | 'expired' | 'error';

let status = $state<SessionStatus>('loading');
let user = $state<Me['user']>(null);
let authEnabled = $state(false);
let error = $state<string | null>(null);

function expire(): void {
	status = 'expired';
	user = null;
	backups.clear();
	activities.clear();
	jobs.clear();
}

setUnauthorizedHandler(expire);

export function loginRedirectUrl(path: string): string {
	const safePath =
		path.startsWith('/') && !path.startsWith('//') && !path.startsWith('/\\') ? path : '/';
	return `/auth/login?redirect_to=${encodeURIComponent(safePath)}`;
}

export const session = {
	get status() {
		return status;
	},
	get user() {
		return user;
	},
	get authEnabled() {
		return authEnabled;
	},
	get error() {
		return error;
	},
	async load() {
		status = 'loading';
		error = null;
		try {
			const me = await getMe();
			authEnabled = me.auth_enabled;
			user = me.user;
			status = !me.auth_enabled || me.authenticated ? 'ready' : 'unauthenticated';
		} catch (reason) {
			if (reason instanceof ApiError && reason.status === 401) {
				user = null;
				status = 'unauthenticated';
				return;
			}
			error = reason instanceof Error ? reason.message : 'Could not check your session.';
			status = 'error';
		}
	},
	login(path: string) {
		window.location.assign(loginRedirectUrl(path));
	},
	async logout() {
		await logoutRequest();
		user = null;
		authEnabled = true;
		status = 'unauthenticated';
		backups.clear();
		activities.clear();
		jobs.clear();
	},
	expire
};
