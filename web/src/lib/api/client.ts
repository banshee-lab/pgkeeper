import type { Activity, ActivityList, ApiErrorBody, Backup, BackupList, Me } from './types';
import {
	copyMockActivities,
	copyMockBackups,
	copyMockMe,
	getMockLog,
	mockBackups,
	mockActivities
} from './mock';

export class ApiError extends Error {
	constructor(
		public readonly status: number,
		public readonly code: string,
		message: string
	) {
		super(message);
		this.name = 'ApiError';
	}
}

let unauthorizedHandler: (() => void) | undefined;
let serverClockOffset = 0;

export function setUnauthorizedHandler(handler: () => void): void {
	unauthorizedHandler = handler;
}

export function serverNow(): number {
	return Date.now() + serverClockOffset;
}

export async function request<T>(
	path: string,
	init: RequestInit = {},
	options: { expireOnUnauthorized?: boolean } = {}
): Promise<T> {
	let response: Response;
	try {
		response = await fetch(path, {
			...init,
			credentials: init.credentials ?? 'same-origin',
			headers: {
				Accept: 'application/json',
				...init.headers
			}
		});
	} catch (error) {
		throw new ApiError(
			0,
			'network_error',
			error instanceof Error ? error.message : 'The server could not be reached.'
		);
	}

	const dateHeader = response.headers.get('Date');
	if (dateHeader) {
		const serverDate = Date.parse(dateHeader);
		if (!Number.isNaN(serverDate)) serverClockOffset = serverDate - Date.now();
	}

	if (response.status === 401 && options.expireOnUnauthorized !== false) {
		unauthorizedHandler?.();
	}

	if (!response.ok) {
		let body: ApiErrorBody | undefined;
		try {
			body = (await response.json()) as ApiErrorBody;
		} catch {
			body = undefined;
		}
		throw new ApiError(
			response.status,
			body?.error?.code ?? 'request_failed',
			body?.error?.message ?? `Request failed with status ${response.status}.`
		);
	}

	if (response.status === 204) return undefined as T;

	try {
		return (await response.json()) as T;
	} catch {
		throw new ApiError(response.status, 'invalid_response', 'The server returned invalid JSON.');
	}
}

export async function requestText(
	path: string,
	init: RequestInit = {},
	options: { expireOnUnauthorized?: boolean } = {}
): Promise<string> {
	let response: Response;
	try {
		response = await fetch(path, {
			...init,
			credentials: init.credentials ?? 'same-origin',
			headers: {
				Accept: 'text/plain',
				...init.headers
			}
		});
	} catch (error) {
		throw new ApiError(
			0,
			'network_error',
			error instanceof Error ? error.message : 'The server could not be reached.'
		);
	}

	if (response.status === 401 && options.expireOnUnauthorized !== false) {
		unauthorizedHandler?.();
	}

	if (!response.ok) {
		let body: ApiErrorBody | undefined;
		try {
			body = (await response.json()) as ApiErrorBody;
		} catch {
			try {
				const text = await response.text();
				body = text ? ({ error: { code: 'request_failed', message: text } } as ApiErrorBody) : undefined;
			} catch {
				body = undefined;
			}
		}
		throw new ApiError(
			response.status,
			body?.error?.code ?? 'request_failed',
			body?.error?.message ?? `Request failed with status ${response.status}.`
		);
	}

	return await response.text();
}

const mockEnabled = import.meta.env.VITE_MOCK_API === 'true';

export async function getMe(): Promise<Me> {
	if (mockEnabled) return copyMockMe();
	return request<Me>('/api/v1/me', {}, { expireOnUnauthorized: false });
}

export async function listBackups(): Promise<Backup[]> {
	if (mockEnabled) return copyMockBackups();
	const result = await request<BackupList>('/api/v1/backups');
	return result.items;
}

export async function listActivities(): Promise<Activity[]> {
	if (mockEnabled) return copyMockActivities().slice(0, 10);
	const result = await request<ActivityList>('/api/v1/activities?limit=10');
	return result.items;
}

export async function getBackup(name: string): Promise<Backup> {
	if (mockEnabled) {
		const backup = mockBackups.find((candidate) => candidate.name === name);
		if (!backup) throw new ApiError(404, 'backup_not_found', `Backup "${name}" was not found.`);
		return structuredClone(backup);
	}
	return request<Backup>(`/api/v1/backups/${encodeURIComponent(name)}`);
}

export async function listBackupActivities(name: string, limit = 10): Promise<Activity[]> {
	if (mockEnabled) {
		return copyMockActivities()
			.filter((activity) => activity.backup_name === name)
			.slice(0, limit);
	}
	const result = await request<ActivityList>(`/api/v1/backups/${encodeURIComponent(name)}/activities?limit=${limit}`);
	return result.items;
}

export async function getActivity(id: string): Promise<Activity> {
	if (mockEnabled) {
		const activity = copyMockActivities().find((candidate) => candidate.id === id);
		if (!activity) throw new ApiError(404, 'activity_not_found', `Activity "${id}" was not found.`);
		return activity;
	}
	return request<Activity>(`/api/v1/activities/${encodeURIComponent(id)}`);
}

export async function getActivityLog(id: string): Promise<string> {
	if (mockEnabled) {
		const activity = mockActivities.find((candidate) => candidate.id === id);
		if (!activity) throw new ApiError(404, 'log_not_found', `Log "${id}" was not found.`);
		return getMockLog(id);
	}
	return requestText(`/api/v1/activities/${encodeURIComponent(id)}/log`);
}

export async function runBackup(name: string): Promise<Activity> {
	if (mockEnabled) {
		const activity: Activity = {
			id: crypto.randomUUID(),
			backup_name: name,
			database: mockBackups.find((backup) => backup.name === name)?.database ?? name,
			triggered_by: 'manual',
			status: 'running',
			started_at: new Date().toISOString(),
			finished_at: null,
			duration_ms: null,
			error: null,
			log_url: ''
		};
		mockActivities.unshift(activity);
		const backup = mockBackups.find((candidate) => candidate.name === name);
		if (backup) {
			backup.running = true;
			backup.last_activity = activity;
		}
		return structuredClone(activity);
	}

	return request<Activity>(`/api/v1/backups/${encodeURIComponent(name)}/run`, {
		method: 'POST'
	});
}

export function logoutRequest(): Promise<void> {
	if (mockEnabled) return Promise.resolve();
	return request<void>('/auth/logout', { method: 'POST' });
}
