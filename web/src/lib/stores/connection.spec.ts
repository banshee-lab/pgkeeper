import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { Activity, Backup, Job, Snapshot } from '$lib/api/types';

const mocks = vi.hoisted(() => ({
	getBackup: vi.fn(),
	listBackups: vi.fn(),
	listActivities: vi.fn(),
	listBackupActivities: vi.fn(),
	getActivity: vi.fn(),
	setUnauthorizedHandler: vi.fn()
}));

vi.mock('$lib/api/client', () => ({
	getBackup: mocks.getBackup,
	listBackups: mocks.listBackups,
	listActivities: mocks.listActivities,
	listBackupActivities: mocks.listBackupActivities,
	getActivity: mocks.getActivity,
	setUnauthorizedHandler: mocks.setUnauthorizedHandler
}));

import { backups } from './backups.svelte';
import { connection } from './connection.svelte';

class TestEventSource {
	static instance: TestEventSource;
	static readonly CLOSED = 2;
	readyState = 1;
	private readonly handlers = new Map<string, EventListener[]>();

	constructor() {
		TestEventSource.instance = this;
	}

	addEventListener(type: string, callback: EventListener): void {
		const handlers = this.handlers.get(type) ?? [];
		handlers.push(callback);
		this.handlers.set(type, handlers);
	}

	close(): void {}

	dispatch(type: string, data: unknown): void {
		const event = { data: JSON.stringify(data) } as MessageEvent;
		for (const handler of this.handlers.get(type) ?? []) handler(event);
	}
}

function snapshot(id: string): Snapshot {
	return {
		id,
		timestamp: '2026-01-01T00:00:00Z',
		files_added: 1,
		files_changed: 0,
		bytes_added: 128
	};
}

function backup(latestSnapshot: Snapshot | null): Backup {
	return {
		name: 'primary',
		cron: '* * * * *',
		timezone: 'UTC',
		container: 'postgres',
		database: 'app',
		username: 'backup',
		host: 'localhost',
		port: 5432,
		roles: false,
		restic: { repository: 'local:/backup', tag: 'pgkeeper' },
		running: true,
		next_run: null,
		last_success_at: null,
		last_activity: null,
		latest_snapshot: latestSnapshot
	};
}

function finishedActivity(status: Activity['status']): Activity {
	return {
		id: 'activity-1',
		backup_name: 'primary',
		database: 'app',
		triggered_by: 'manual',
		status,
		started_at: '2026-01-01T00:00:00Z',
		finished_at: '2026-01-01T00:01:00Z',
		duration_ms: 60_000,
		error: status === 'error' ? 'backup failed' : null,
		log_url: '/api/v1/activities/activity-1/log'
	};
}

function finishedJob(): Job {
	return {
		activity_id: 'activity-1',
		backup_name: 'primary',
		database: 'app',
		triggered_by: 'manual',
		status: 'running',
		phase: 'cleanup',
		started_at: '2026-01-01T00:00:00Z'
	};
}

describe('connection backup refresh', () => {
	beforeEach(() => {
		backups.clear();
		vi.clearAllMocks();
		Object.defineProperty(globalThis, 'EventSource', {
			configurable: true,
			value: TestEventSource
		});
	});

	it('reloads backup details when a job finishes successfully', async () => {
		const latestSnapshot = snapshot('new-snapshot');
		mocks.getBackup
			.mockResolvedValueOnce(backup(snapshot('old-snapshot')))
			.mockResolvedValueOnce(backup(latestSnapshot));
		await backups.loadOne('primary');

		connection.connect();
		TestEventSource.instance.dispatch('job.finished', {
			job: finishedJob(),
			activity: finishedActivity('success')
		});

		await vi.waitFor(() => {
			expect(backups.list[0].latest_snapshot).toEqual(latestSnapshot);
		});
		expect(mocks.getBackup).toHaveBeenCalledTimes(2);
		connection.close();
	});

	it('does not reload backup details when a job fails', async () => {
		mocks.getBackup.mockResolvedValueOnce(backup(snapshot('old-snapshot')));
		await backups.loadOne('primary');

		connection.connect();
		TestEventSource.instance.dispatch('job.finished', {
			job: finishedJob(),
			activity: finishedActivity('error')
		});

		expect(mocks.getBackup).toHaveBeenCalledTimes(1);
		connection.close();
	});
});
